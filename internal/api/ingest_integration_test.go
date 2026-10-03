//go:build integration

package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/ingest"
)

// Synthetic payloads only.

func heartbeatBody(conn uuid.UUID, sentAt string, extra ...string) string {
	s := `{"schema":"vitamux.ingest.heartbeat/1","connection_id":"` + ingest.FormatConnectionID(conn) +
		`","client":{"kind":"device","name":"phone","version":"1.0"},"sent_at":"` + sentAt + `"`
	for _, e := range extra {
		s += "," + e
	}
	return s + "}"
}

type pushEnv struct {
	*authEnv
	own, otherConn uuid.UUID
	tok, otherTok  string
}

func newPushEnv(t *testing.T) *pushEnv {
	e := &pushEnv{authEnv: newAuthEnv(t, false)}
	conn := func() uuid.UUID {
		id := uuid.New()
		e.exec(`INSERT INTO connections (id, user_id, provider_id, mode, status)
			SELECT $1, $2, id, 'push', 'active' FROM providers WHERE code = 'apple_health'`, id, e.userID)
		return id
	}
	e.own, e.otherConn = conn(), conn()
	for _, c := range []struct {
		conn uuid.UUID
		tok  *string
	}{{e.own, &e.tok}, {e.otherConn, &e.otherTok}} {
		_, tok, err := auth.CreateClientToken(context.Background(), e.d, e.userID, c.conn, "device", "phone")
		if err != nil {
			t.Fatal(err)
		}
		*c.tok = tok
		e.secrets = append(e.secrets, tok)
	}
	return e
}

type pushReq struct {
	method, path string
	body         []byte
	key, bearer  string
	gzip         bool
}

func (e *pushEnv) send(r pushReq) (*http.Response, []byte) {
	e.t.Helper()
	body := r.body
	if r.gzip {
		body = gzipped(e.t, body)
	}
	var rd io.Reader
	if r.method != http.MethodGet {
		rd = bytes.NewReader(body)
	}
	req := request(e.t, r.method, r.path, rd)
	if r.key != "" {
		req.Header.Set("Idempotency-Key", r.key)
	}
	if r.gzip {
		req.Header.Set("Content-Encoding", "gzip")
	}
	bearer := r.bearer
	if bearer == "" {
		bearer = e.tok
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	res := serve(e.t, e.h, req)
	out, _ := io.ReadAll(res.Body)
	return res, out
}

func (e *pushEnv) expectCode(res *http.Response, body []byte, status int, code Code) map[string]any {
	e.t.Helper()
	var m map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &m); err != nil {
			e.t.Fatalf("%d %s", res.StatusCode, body)
		}
	}
	e.expect(res, m, status, code)
	return m
}

func gzipped(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type testItem struct {
	key, body, blob string
}

func batchBody(conn uuid.UUID, items ...testItem) []byte {
	parts := make([]string, len(items))
	for i, it := range items {
		ref := `"content_type":"application/json","body":` + it.body
		if it.blob != "" {
			ref = `"content_type":"application/vnd.ant.fit","blob_sha256":"` + it.blob + `"`
		}
		parts[i] = `{"stream":"synthetic.samples.v1","external_key":"` + it.key + `","fetched_at":"2026-09-14T09:02:11Z",` + ref + `}`
	}
	return []byte(`{"schema":"vitamux.ingest.batch/1","connection_id":"` + ingest.FormatConnectionID(conn) +
		`","client":{"kind":"device","name":"phone","version":"1.0"},"items":[` + strings.Join(parts, ",") + `]}`)
}

func statuses(m map[string]any) []string {
	var out []string
	for _, it := range m["items"].([]any) {
		out = append(out, it.(map[string]any)["status"].(string))
	}
	return out
}

func TestPushBatchIdempotency(t *testing.T) {
	e := newPushEnv(t)
	body := batchBody(e.own, testItem{key: "a", body: `{"v": 1}`}, testItem{key: "b", body: `[1, 2]`})
	post := pushReq{method: http.MethodPost, path: "/api/ingest/v1/batches", body: body, key: "k1"}

	res, first := e.send(post)
	acc := e.expectCode(res, first, http.StatusAccepted, "")
	if got := statuses(acc); fmt.Sprint(got) != "[stored stored]" || acc["normalization"] != "queued" {
		t.Fatalf("first: %s", first)
	}
	batchID, err := ingest.ParseBatchID(acc["batch_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT count(*) FROM jobs WHERE kind = 'normalize_batch' AND status = 'queued' AND payload->>'batch_id' = $1`, batchID.String()) != 1 {
		t.Fatal("normalize_batch not enqueued")
	}
	if e.count(`SELECT count(*) FROM ingest_batches WHERE id = $1 AND source_kind = 'push' AND idempotency_key = 'k1' AND client_id IS NOT NULL`, batchID) != 1 {
		t.Fatal("batch row")
	}

	// Same key and body (gzip this time: the decompressed body counts) → same response.
	post.gzip = true
	res, again := e.send(post)
	e.expectCode(res, again, http.StatusAccepted, "")
	if !bytes.Equal(first, again) || res.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay differs:\n%s\n%s", first, again)
	}
	// Same key, different body → 409, nothing stored.
	res, out := e.send(pushReq{method: http.MethodPost, path: post.path, key: "k1", body: batchBody(e.own, testItem{key: "a", body: `{"v": 2}`})})
	e.expectCode(res, out, http.StatusConflict, CodeConflict)
	if e.count("SELECT count(*) FROM ingest_batches") != 1 || e.count("SELECT count(*) FROM raw_payloads") != 2 {
		t.Fatal("replay or conflict wrote rows")
	}

	// New key, same content → duplicates and no job; changed content → new version.
	res, out = e.send(pushReq{method: http.MethodPost, path: post.path, key: "k2", body: body})
	if acc = e.expectCode(res, out, http.StatusAccepted, ""); fmt.Sprint(statuses(acc)) != "[duplicate duplicate]" {
		t.Fatalf("duplicates: %s", out)
	}
	res, out = e.send(pushReq{method: http.MethodPost, path: post.path, key: "k3", body: batchBody(e.own, testItem{key: "a", body: `{"v": 2}`})})
	if acc = e.expectCode(res, out, http.StatusAccepted, ""); fmt.Sprint(statuses(acc)) != "[new_version]" {
		t.Fatalf("new version: %s", out)
	}
	if e.count(`SELECT count(*) FROM jobs WHERE kind = 'normalize_batch'`) != 2 {
		t.Fatal("a batch of duplicates must not enqueue normalization")
	}
	// Stored bytes are the body exactly as sent.
	if e.count(`SELECT count(*) FROM raw_payloads WHERE external_key = 'b' AND content_sha256 = $1`, sha(`[1, 2]`)) != 1 {
		t.Fatal("body not stored verbatim")
	}

	// Concurrent requests with one key store one batch and answer the same.
	body = batchBody(e.own, testItem{key: "c", body: `{"v": 3}`})
	var wg sync.WaitGroup
	got := make([][]byte, 4)
	for i := range got {
		wg.Go(func() {
			req := request(t, http.MethodPost, post.path, bytes.NewReader(body))
			req.Header.Set("Idempotency-Key", "k4")
			req.Header.Set("Authorization", "Bearer "+e.tok)
			rec := serve(t, e.h, req)
			got[i], _ = io.ReadAll(rec.Body)
		})
	}
	wg.Wait()
	for i := range got {
		if !bytes.Equal(got[i], got[0]) || !strings.Contains(string(got[0]), `"batch_id"`) {
			t.Fatalf("concurrent responses differ: %s vs %s", got[i], got[0])
		}
	}
	if e.count("SELECT count(*) FROM ingest_batches WHERE idempotency_key = 'k4'") != 1 {
		t.Fatal("concurrent replays created several batches")
	}
}

func TestPushBatchRejections(t *testing.T) {
	e := newPushEnv(t)
	path := "/api/ingest/v1/batches"
	good := batchBody(e.own, testItem{key: "a", body: `{"v": 1}`})

	res, out := e.send(pushReq{method: http.MethodPost, path: path, body: good})
	e.expectCode(res, out, http.StatusUnprocessableEntity, CodeValidationFailed) // no Idempotency-Key
	res, out = e.send(pushReq{method: http.MethodPost, path: path, key: "k", body: batchBody(e.otherConn, testItem{key: "a", body: `{}`})})
	e.expectCode(res, out, http.StatusForbidden, CodeForbidden)
	res, out = e.send(pushReq{method: http.MethodPost, path: path, key: "k", body: good, bearer: e.otherTok})
	e.expectCode(res, out, http.StatusForbidden, CodeForbidden)

	bad := bytes.Replace(good, []byte(`"stream":"synthetic.samples.v1"`), []byte(`"stream":"Bad Stream"`), 1)
	res, out = e.send(pushReq{method: http.MethodPost, path: path, key: "k", body: bad})
	p := e.expectCode(res, out, http.StatusUnprocessableEntity, CodeValidationFailed)
	if errs, _ := p["errors"].([]any); len(errs) != 1 || errs[0].(map[string]any)["pointer"] != "/items/0/stream" {
		t.Fatalf("validation errors: %s", out)
	}

	// Body limits: compressed (middleware), decompressed total, and ratio.
	res, out = e.send(pushReq{method: http.MethodPost, path: path, key: "k", body: bytes.Repeat([]byte(" "), 10*miB+1)})
	e.expectCode(res, out, http.StatusRequestEntityTooLarge, CodePayloadTooLarge)
	res, out = e.send(pushReq{method: http.MethodPost, path: path, key: "k", body: bytes.Repeat([]byte(" "), 3*miB), gzip: true})
	e.expectCode(res, out, http.StatusRequestEntityTooLarge, CodePayloadTooLarge) // ~3 KiB expanding to 3 MiB
	noise := make([]byte, 2*miB)
	_, _ = rand.Read(noise)
	res, out = e.send(pushReq{method: http.MethodPost, path: path, key: "k", body: append(noise, bytes.Repeat([]byte(" "), 49*miB)...), gzip: true})
	e.expectCode(res, out, http.StatusRequestEntityTooLarge, CodePayloadTooLarge) // within ratio, over 50 MiB
	res, out = e.send(pushReq{method: http.MethodPost, path: path, key: "k", body: good, gzip: true})
	e.expectCode(res, out, http.StatusAccepted, "") // gzip within limits

	req := request(t, http.MethodPost, path, bytes.NewReader(good))
	req.Header.Set("Idempotency-Key", "k5")
	req.Header.Set("Content-Encoding", "gzip") // not actually gzip
	req.Header.Set("Authorization", "Bearer "+e.tok)
	res = serve(t, e.h, req)
	out, _ = io.ReadAll(res.Body)
	e.expectCode(res, out, http.StatusUnprocessableEntity, CodeValidationFailed)

	if e.count("SELECT count(*) FROM idempotency_keys") != 1 {
		t.Fatal("rejected requests must not store an idempotency key")
	}
}

func TestPushBlobsAndStatus(t *testing.T) {
	e := newPushEnv(t)
	res, out := e.send(pushReq{method: http.MethodPost, path: "/api/ingest/v1/batches", key: "k1",
		body: batchBody(e.own, testItem{key: "a", body: `{"v": 1}`}, testItem{key: "b", body: `{"v": 2}`})})
	batch := e.expectCode(res, out, http.StatusAccepted, "")["batch_id"].(string)

	file := []byte("synthetic FIT bytes \x00\x01\x02")
	sum := sha256.Sum256(file)
	hexSum := hex.EncodeToString(sum[:])
	blobs := "/api/ingest/v1/batches/" + batch + "/blobs"

	// A batch item may only reference an uploaded blob.
	res, out = e.send(pushReq{method: http.MethodPost, path: "/api/ingest/v1/batches", key: "k2", body: batchBody(e.own, testItem{key: "f", blob: hexSum})})
	if p := e.expectCode(res, out, http.StatusUnprocessableEntity, CodeValidationFailed); !strings.Contains(string(out), "/items/0/blob_sha256") {
		t.Fatalf("missing blob: %v", p)
	}
	res, out = e.send(pushReq{method: http.MethodPost, path: blobs, key: "b1", body: file})
	stored := e.expectCode(res, out, http.StatusCreated, "")
	if stored["blob_sha256"] != hexSum || stored["size_bytes"] != float64(len(file)) {
		t.Fatalf("blob: %s", out)
	}
	res, again := e.send(pushReq{method: http.MethodPost, path: blobs, key: "b1", body: file, gzip: true})
	if e.expectCode(res, again, http.StatusCreated, ""); !bytes.Equal(out, again) {
		t.Fatal("blob replay differs")
	}
	res, out = e.send(pushReq{method: http.MethodPost, path: blobs, key: "b1", body: []byte("other")})
	e.expectCode(res, out, http.StatusConflict, CodeConflict)
	res, out = e.send(pushReq{method: http.MethodPost, path: "/api/ingest/v1/batches", key: "k2", body: batchBody(e.own, testItem{key: "f", blob: hexSum})})
	if acc := e.expectCode(res, out, http.StatusAccepted, ""); fmt.Sprint(statuses(acc)) != "[stored]" {
		t.Fatalf("blob item: %s", out)
	}
	if e.count("SELECT count(*) FROM blobs WHERE sha256 = $1 AND refcount = 1", sum[:]) != 1 {
		t.Fatal("blob not retained by the raw row")
	}
	res, out = e.send(pushReq{method: http.MethodPost, path: blobs, key: "b2", body: file, bearer: e.otherTok})
	e.expectCode(res, out, http.StatusForbidden, CodeForbidden)
	res, out = e.send(pushReq{method: http.MethodPost, path: "/api/ingest/v1/batches/" + ingest.FormatBatchID(uuid.New()) + "/blobs", key: "b3", body: file})
	e.expectCode(res, out, http.StatusNotFound, CodeNotFound)

	// Status follows the job and the raw rows.
	get := func(bearer string) (*http.Response, []byte) {
		return e.send(pushReq{method: http.MethodGet, path: "/api/ingest/v1/batches/" + batch, bearer: bearer})
	}
	res, out = get("")
	st := e.expectCode(res, out, http.StatusOK, "")
	if st["normalization"] != "queued" || fmt.Sprint(statuses(st)) != "[stored stored]" || st["received_at"] == nil {
		t.Fatalf("status: %s", out)
	}
	id, _ := ingest.ParseBatchID(batch)
	e.exec(`UPDATE jobs SET status = 'running', lease_owner = 'w', lease_expires_at = now() + interval '1 minute' WHERE payload->>'batch_id' = $1`, id.String())
	if res, out = get(""); e.expectCode(res, out, http.StatusOK, "")["normalization"] != "running" {
		t.Fatalf("running: %s", out)
	}
	e.exec(`UPDATE jobs SET status = 'succeeded', lease_owner = NULL, lease_expires_at = NULL WHERE payload->>'batch_id' = $1`, id.String())
	e.exec(`UPDATE raw_payloads SET status = 'normalized' WHERE batch_id = $1`, id)
	if res, out = get(""); e.expectCode(res, out, http.StatusOK, "")["normalization"] != "done" {
		t.Fatalf("done: %s", out)
	}
	e.exec(`UPDATE raw_payloads SET status = 'normalize_failed' WHERE batch_id = $1 AND external_key = 'b'`, id)
	res, out = get("")
	st = e.expectCode(res, out, http.StatusOK, "")
	if st["normalization"] != "failed" || fmt.Sprint(statuses(st)) != "[normalized normalize_failed]" {
		t.Fatalf("failed: %v", st)
	}
	res, out = get(e.otherTok)
	e.expectCode(res, out, http.StatusForbidden, CodeForbidden)
	res, out = e.send(pushReq{method: http.MethodGet, path: "/api/ingest/v1/batches/bat_nope"})
	e.expectCode(res, out, http.StatusNotFound, CodeNotFound)
}

func TestPushHeartbeat(t *testing.T) {
	e := newPushEnv(t)
	hb := func(sentAt string, extra ...string) {
		t.Helper()
		res, out := e.send(pushReq{method: http.MethodPost, path: "/api/ingest/v1/heartbeat", body: []byte(heartbeatBody(e.own, sentAt, extra...))})
		e.expectCode(res, out, http.StatusNoContent, "")
	}
	hb("2026-09-14T09:00:00Z", `"pending_failed_units":2`, `"last_error_class":"network"`,
		`"streams":[{"stream":"healthkit.samples.v1","checkpoint":{"anchor_hash":"h1"},"last_success_at":"2026-09-14T08:59:00Z","last_error_class":"storage"}]`)
	if e.count(`SELECT count(*) FROM clients WHERE metadata->'heartbeat'->>'pending_failed_units' = '2'
		AND metadata->'heartbeat'->>'last_error_class' = 'network' AND metadata->'heartbeat'->'client'->>'version' = '1.0'`) != 1 {
		t.Fatal("heartbeat not in clients.metadata")
	}
	if e.count(`SELECT count(*) FROM sync_cursors WHERE connection_id = $1 AND stream = 'healthkit.samples.v1'
		AND cursor->>'anchor_hash' = 'h1' AND high_watermark = '2026-09-14T08:59:00Z' AND status = 'degraded' AND status_reason = 'storage'`, e.own) != 1 {
		t.Fatal("stream checkpoint not in sync_cursors")
	}
	// A newer heartbeat without a checkpoint keeps it and clears the error; an older one is ignored.
	hb("2026-09-14T10:00:00Z", `"streams":[{"stream":"healthkit.samples.v1"}]`)
	hb("2026-09-14T08:00:00Z", `"pending_failed_units":9`, `"streams":[{"stream":"healthkit.samples.v1","checkpoint":{"anchor_hash":"old"},"last_error_class":"network"}]`)
	if e.count(`SELECT count(*) FROM sync_cursors WHERE cursor->>'anchor_hash' = 'h1' AND status = 'ok' AND status_reason IS NULL`) != 1 ||
		e.count(`SELECT count(*) FROM clients WHERE metadata->'heartbeat'->>'pending_failed_units' = '0'`) != 1 {
		t.Fatal("heartbeat ordering")
	}
	res, out := e.send(pushReq{method: http.MethodPost, path: "/api/ingest/v1/heartbeat", body: []byte(`{"schema":"x"}`)})
	e.expectCode(res, out, http.StatusUnprocessableEntity, CodeValidationFailed)
}
