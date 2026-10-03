//go:build integration

package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
)

type env struct {
	d     *db.DB
	scan  func(sql string, args []any, dest ...any) error // test-only reads on the pool
	blobs *blob.Store
	batch BatchRef
}

func setup(t *testing.T) env {
	t.Helper()
	ctx := context.Background()
	_, pool := dbtest.Migrated(t)
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
	userID, connID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO users (id, username, password_hash) VALUES ($1, 'owner', 'synthetic')", []any{userID}},
		{`INSERT INTO connections (id, user_id, provider_id, mode, status)
		  VALUES ($1, $2, (SELECT id FROM providers WHERE code = 'withings'), 'in_process', 'active')`, []any{connID, userID}},
	} {
		if _, err := pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	e := env{d: db.New(pool), blobs: blobs, scan: func(sql string, args []any, dest ...any) error {
		return pool.QueryRow(ctx, sql, args...).Scan(dest...)
	}}
	err = e.d.Tx(ctx, func(q *dbq.Queries) error {
		e.batch, err = CreateBatch(ctx, q, BatchInfo{UserID: userID, ConnectionID: connID, SourceKind: SourceSync})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e env) store(t *testing.T, items ...RawItem) []Result {
	t.Helper()
	var res []Result
	err := e.d.Tx(context.Background(), func(q *dbq.Queries) error {
		var err error
		res, err = StoreRaw(context.Background(), q, e.blobs, e.batch, items)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (e env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.scan(sql, args, &n); err != nil {
		t.Fatal(err)
	}
	return n
}

func item(key, body string) RawItem {
	return RawItem{
		Stream: "withings.measures", ExternalKey: key, ContentType: "application/json",
		FetchedAt: time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC), Body: []byte(body),
	}
}

func TestStoreRawVersions(t *testing.T) {
	e := setup(t)
	a, b := `{"synthetic": true, "value": 61}`, `{"synthetic": true, "value": 62}`

	r := e.store(t, item("grp:1", a))[0]
	if r.Outcome != Stored || r.Version != 1 || r.SupersedesID != nil {
		t.Fatalf("first store: %+v", r)
	}
	first := r.RawPayloadID

	r = e.store(t, item("grp:1", a))[0]
	if r.Outcome != Duplicate || r.RawPayloadID != first {
		t.Fatalf("replay: %+v", r)
	}
	if n := e.count(t, "SELECT count(*) FROM raw_payloads"); n != 1 {
		t.Fatalf("duplicate wrote rows: %d", n)
	}

	r = e.store(t, item("grp:1", b))[0]
	if r.Outcome != NewVersion || r.Version != 2 || r.SupersedesID == nil || *r.SupersedesID != first {
		t.Fatalf("changed content: %+v", r)
	}
	// Reverting to earlier content is a new version, not a duplicate of version 1.
	r = e.store(t, item("grp:1", a))[0]
	if r.Outcome != NewVersion || r.Version != 3 {
		t.Fatalf("A→B→A: %+v", r)
	}
	sumA := sha256.Sum256([]byte(a))
	if n := e.count(t, "SELECT refcount FROM blobs WHERE sha256 = $1", sumA[:]); n != 2 {
		t.Fatalf("refcount of A = %d, want 2", n)
	}
	if n := e.count(t, "SELECT count(*) FROM blobs"); n != 2 {
		t.Fatalf("blobs = %d, want 2 (content stored once)", n)
	}

	// Two items with the same key in one call: the second sees the first.
	res := e.store(t, item("grp:2", a), item("grp:2", a))
	if res[0].Outcome != Stored || res[1].Outcome != Duplicate || res[0].RawPayloadID != res[1].RawPayloadID {
		t.Fatalf("same call: %+v", res)
	}

	got, err := e.blobs.Get(sumA[:])
	if err != nil || string(got) != a {
		t.Fatalf("raw content not retrievable verbatim: %v", err)
	}
	var fp *string
	if err := e.scan("SELECT shape_fingerprint FROM raw_payloads WHERE id = $1", []any{first}, &fp); err != nil || fp == nil || !strings.HasPrefix(*fp, "v1:") {
		t.Fatalf("fingerprint not stored: %v", err)
	}
}

func TestStoreRawStripsRequestCredentials(t *testing.T) {
	e := setup(t)
	it := item("grp:1", `{"synthetic": true}`)
	it.Request = sentinelRequest()
	e.store(t, it)
	if n := e.count(t, "SELECT count(*) FROM raw_payloads WHERE request_meta::text LIKE '%' || $1 || '%'", sentinel); n != 0 {
		t.Fatal("sentinel stored in request_meta")
	}
	if n := e.count(t, "SELECT count(*) FROM raw_payloads WHERE request_meta->'params'->>'action' = 'getmeas'"); n != 1 {
		t.Fatal("non-secret params not stored")
	}
}

func TestStoreRawBlobItemAndQuarantine(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	file := []byte("synthetic FIT bytes")
	var info blob.Info
	err := e.d.Tx(ctx, func(q *dbq.Queries) error {
		var err error
		info, err = e.blobs.Put(ctx, q, bytes.NewReader(file), blob.Plain)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	it := RawItem{Stream: "files.fit.v1", ExternalKey: "a.fit", ContentType: "application/vnd.ant.fit",
		FetchedAt: time.Now(), BlobSHA256: info.SHA256, Quarantine: true}
	r := e.store(t, it)[0]
	if r.Outcome != Stored {
		t.Fatalf("%+v", r)
	}
	if n := e.count(t, "SELECT count(*) FROM raw_payloads WHERE id = $1 AND status = 'quarantined' AND shape_fingerprint IS NULL", r.RawPayloadID); n != 1 {
		t.Fatal("blob item not stored as quarantined")
	}

	missing := it
	missing.ExternalKey, missing.BlobSHA256 = "b.fit", make([]byte, 32)
	err = e.d.Tx(ctx, func(q *dbq.Queries) error {
		_, err := StoreRaw(ctx, q, e.blobs, e.batch, []RawItem{missing})
		return err
	})
	if !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("unknown blob: got %v, want ErrNotFound", err)
	}
}

func TestSetStatus(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	id := e.store(t, item("grp:1", `{"synthetic": true}`))[0].RawPayloadID
	set := func(id int64, to Status) error {
		return e.d.Tx(ctx, func(q *dbq.Queries) error { return SetStatus(ctx, q, id, to) })
	}
	steps := []struct {
		to Status
		ok bool
	}{
		{StatusQuarantined, true},
		{StatusNormalized, false}, // quarantined must be released first
		{StatusQuarantined, true}, // same status is a no-op
		{StatusStored, true},
		{StatusNormalizeFailed, true},
		{StatusNormalized, true},
		{StatusQuarantined, false},
		{StatusStored, true}, // reprocess
	}
	for i, s := range steps {
		err := set(id, s.to)
		if s.ok && err != nil || !s.ok && !errors.Is(err, ErrTransition) {
			t.Fatalf("step %d → %s: %v", i, s.to, err)
		}
	}
	if err := set(id+1000, StatusStored); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("missing row: %v", err)
	}
}
