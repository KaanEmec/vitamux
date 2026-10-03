//go:build integration

package normalize

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

const testStream = "test.readings"

// testNorm is a synthetic normalizer: one heart-rate sample per payload. Body flags: panic and
// bad make it fail, warn raises a warning, bump changes its value from version 2 on.
type testNorm struct{ version int }

func (n testNorm) ID() string                    { return "test.readings" }
func (n testNorm) Version() int                  { return n.version }
func (n testNorm) Accepts(stream, _ string) bool { return stream == testStream }
func (n testNorm) Normalize(_ context.Context, raw RawPayload, _ Env) (Output, error) {
	var in struct {
		HR                     float64
		Panic, Bad, Warn, Bump bool
	}
	if err := json.Unmarshal(raw.Body, &in); err != nil {
		return Output{}, err
	}
	switch {
	case in.Panic:
		panic("synthetic")
	case in.Bad:
		return Output{}, fmt.Errorf("synthetic failure")
	}
	if in.Bump && n.version >= 2 {
		in.HR++
	}
	out := Output{Measurements: []Measurement{{Metric: "heart_rate", Kind: catalog.Sample,
		Start: ts("2026-06-15T07:00:00Z"), Value: in.HR, Unit: "bpm", Key: Key{RecordType: "reading", ExternalID: raw.ExternalKey}}}}
	if in.Warn {
		out.Warnings = []Warning{{Code: "synthetic_warning"}}
	}
	return out, nil
}

type jobEnv struct {
	*env
	blobs *blob.Store
}

func newJobEnv(t *testing.T) *jobEnv {
	t.Helper()
	e := writerEnv(t)
	keyPath := filepath.Join(t.TempDir(), "master.key")
	if _, err := crypto.WriteKeyFile(keyPath); err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.Load(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.Open(t.TempDir(), kr)
	if err != nil {
		t.Fatal(err)
	}
	return &jobEnv{e, blobs}
}

func (e *jobEnv) processor(version int) *Processor {
	reg, err := NewRegistry(testNorm{version})
	if err != nil {
		e.t.Fatal(err)
	}
	return &Processor{DB: e.d, Blobs: e.blobs, Registry: reg, Log: slog.New(slog.DiscardHandler)}
}

// raws stores bodies (key → JSON) as one batch of testStream and returns the batch id.
func (e *jobEnv) raws(stream string, bodies ...[2]string) uuid.UUID {
	e.t.Helper()
	var ref ingest.BatchRef
	err := e.d.Tx(context.Background(), func(q *dbq.Queries) error {
		var err error
		ref, err = ingest.CreateBatch(context.Background(), q, ingest.BatchInfo{UserID: e.user, ConnectionID: e.conn, SourceKind: ingest.SourcePush})
		if err != nil {
			return err
		}
		var items []ingest.RawItem
		for _, b := range bodies {
			items = append(items, ingest.RawItem{Stream: stream, ExternalKey: b[0], ContentType: "application/json",
				FetchedAt: time.Now(), Body: []byte(b[1])})
		}
		_, err = ingest.StoreRaw(context.Background(), q, e.blobs, ref, items)
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return ref.ID
}

// run executes one job of kind through a real Runner and returns its checkpoint.
func (e *jobEnv) run(p *Processor, kind string, h jobs.Handler, payload any) json.RawMessage {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := jobs.NewRunner(e.d, jobs.Config{Workers: 1, Poll: 20 * time.Millisecond})
	r.Register(kind, h)
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	id, _, err := jobs.Enqueue(ctx, e.d.Q(), jobs.NewJob{Kind: kind, Payload: payload})
	if err != nil {
		e.t.Fatal(err)
	}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		j, err := e.d.Q().GetJob(ctx, id)
		if err != nil {
			e.t.Fatal(err)
		}
		switch j.Status {
		case "succeeded":
			return j.Checkpoint
		case "dead", "failed", "cancelled":
			e.t.Fatalf("%s job ended %s", kind, j.Status)
		}
	}
	e.t.Fatalf("%s job did not finish", kind)
	return nil
}

func (e *jobEnv) batch(p *Processor, id uuid.UUID) {
	e.t.Helper()
	e.run(p, ingest.KindNormalizeBatch, p.BatchJob(), ingest.NormalizePayload{BatchID: id})
}

func (e *jobEnv) reprocess(p *Processor, sel ReprocessPayload) Summary {
	e.t.Helper()
	cp := e.run(p, KindReprocess, p.ReprocessJob(), sel)
	var st reprocessState
	if err := json.Unmarshal(cp, &st); err != nil || !st.Done {
		e.t.Fatalf("checkpoint %s: %v", cp, err)
	}
	return st.Sum
}

// state returns "status/detail" of the newest raw payload with this external key.
func (e *jobEnv) state(key string) string {
	return e.text(`SELECT status || '/' || coalesce(status_detail, '') FROM raw_payloads WHERE external_key = $1 ORDER BY version DESC LIMIT 1`, key)
}

func TestBatchIsolatesFailures(t *testing.T) {
	e := newJobEnv(t)
	p := e.processor(1)
	b := e.raws(testStream,
		[2]string{"a", `{"HR": 60, "Warn": true}`},
		[2]string{"boom", `{"HR": 61, "Panic": true}`},
		[2]string{"c", `{"HR": 62}`},
		[2]string{"bad", `{"HR": 63, "Bad": true}`},
		[2]string{"garbled", `not json`})
	other := e.raws("other.stream", [2]string{"x", `{}`})
	e.batch(p, b)
	e.batch(p, other)

	for key, want := range map[string]string{
		"a": "normalized/", "c": "normalized/",
		"boom": "normalize_failed/normalizer_panic", "bad": "normalize_failed/normalizer_error",
		"garbled": "normalize_failed/normalizer_error", "x": "normalize_failed/no_normalizer",
	} {
		if got := e.state(key); got != want {
			t.Errorf("%s: %s, want %s", key, got, want)
		}
	}
	if n := e.int(`SELECT count(*) FROM measurements WHERE superseded_at IS NULL`); n != 2 {
		t.Errorf("%d measurements, want the 2 of the healthy payloads", n)
	}
	if got := e.text(`SELECT warnings->0->>'code' FROM raw_payloads WHERE external_key = 'a'`); got != "synthetic_warning" {
		t.Errorf("warning of a: %q", got)
	}
	if got := e.text(`SELECT warnings->0->>'code' FROM raw_payloads WHERE external_key = 'boom'`); got != "normalizer_panic" {
		t.Errorf("warning of boom: %q", got)
	}
	// Raw bodies of failed payloads are kept.
	if n := e.int(`SELECT count(*) FROM raw_payloads r JOIN blobs b ON b.sha256 = r.content_sha256 WHERE r.status = 'normalize_failed'`); n != 4 {
		t.Errorf("%d failed payloads with their blob, want 4", n)
	}
	// A repeated job has nothing left to do.
	h := e.activeHash()
	e.batch(p, b)
	if e.activeHash() != h {
		t.Error("second batch run changed canonical rows")
	}
}

func TestSupersededRawVersionIsSkipped(t *testing.T) {
	e := newJobEnv(t)
	p := e.processor(1)
	b := e.raws(testStream, [2]string{"a", `{"HR": 60}`}, [2]string{"a", `{"HR": 61}`})
	e.batch(p, b)
	if got := e.text(`SELECT string_agg(status || '/' || coalesce(status_detail, ''), ',' ORDER BY version) FROM raw_payloads`); got != "normalized/superseded_raw,normalized/" {
		t.Errorf("statuses %s", got)
	}
	if n := e.int(`SELECT count(*) FROM measurements WHERE superseded_at IS NULL AND value = 61`); n != 1 {
		t.Errorf("want the newer raw version's row, got %d", n)
	}
	if n := e.int(`SELECT count(*) FROM measurements`); n != 1 {
		t.Errorf("%d measurements, want 1 (the older raw version writes nothing)", n)
	}
}

func TestReprocessAfterVersionBump(t *testing.T) {
	e := newJobEnv(t)
	p1 := e.processor(1)
	e.batch(p1, e.raws(testStream,
		[2]string{"a", `{"HR": 60, "Bump": true}`},
		[2]string{"b", `{"HR": 70}`},
		[2]string{"c", `{"HR": 80, "Bump": true}`},
		[2]string{"boom", `{"HR": 90, "Panic": true}`}))
	e.batch(p1, e.raws("other.stream", [2]string{"x", `{}`}))

	// Every payload was already attempted by v1 (failures included), so v1 selects nothing.
	if n, _, err := p1.CountReprocess(context.Background(), ReprocessPayload{}); err != nil || n != 0 {
		t.Fatalf("v1 selects %d (%v), want 0", n, err)
	}

	p2 := e.processor(2)
	if n, scanned, err := p2.CountReprocess(context.Background(), ReprocessPayload{Normalizer: "test.readings", Stream: testStream}); err != nil || n != 4 || scanned != 4 {
		t.Fatalf("v2 selects %d of %d (%v), want 4 of 4", n, scanned, err)
	}
	sum := e.reprocess(p2, ReprocessPayload{Normalizer: "test.readings"})
	want := Summary{Normalized: 3, Failed: 1, Inserted: 2, Superseded: 2, Reversioned: 1, Unchanged: 1}
	if sum != want {
		t.Fatalf("summary %+v, want %+v", sum, want)
	}
	// Changed rows were superseded, the unchanged one only took the new version.
	if got := e.text(`SELECT string_agg(value::text || ':' || v.version, ',' ORDER BY m.value)
		FROM measurements m JOIN normalizer_versions v ON v.id = m.normalizer_version_id WHERE m.superseded_at IS NULL`); got != "61:2,70:2,81:2" {
		t.Errorf("active rows %s", got)
	}
	if n := e.int(`SELECT count(*) FROM measurements WHERE superseded_at IS NOT NULL`); n != 2 {
		t.Errorf("%d superseded rows, want 2", n)
	}
	if got := e.state("boom"); got != "normalize_failed/normalizer_panic" {
		t.Errorf("boom: %s", got)
	}
	if got := e.state("x"); got != "normalize_failed/no_normalizer" {
		t.Errorf("x (no normalizer) must stay untouched: %s", got)
	}

	// Second run is a no-op.
	h := e.activeHash()
	if n, _, err := p2.CountReprocess(context.Background(), ReprocessPayload{}); err != nil || n != 0 {
		t.Fatalf("second count %d (%v), want 0", n, err)
	}
	if sum := e.reprocess(p2, ReprocessPayload{}); sum != (Summary{}) {
		t.Errorf("second run %+v, want empty", sum)
	}
	if e.activeHash() != h || e.int(`SELECT count(*) FROM measurements`) != 5 {
		t.Error("second run changed canonical rows")
	}
	if _, _, err := p2.CountReprocess(context.Background(), ReprocessPayload{Normalizer: "nope"}); err == nil || !strings.Contains(err.Error(), "unknown normalizer") {
		t.Errorf("unknown normalizer: %v", err)
	}
}
