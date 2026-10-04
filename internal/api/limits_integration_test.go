//go:build integration

package api

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
)

// limitEnv is the real router with body limits over a fresh database, an owner and one push
// connection. Requests carry a principal directly (access has its own tests).
type limitEnv struct {
	t      *testing.T
	h      http.Handler
	owner  *auth.Principal
	client *auth.Principal
	batch  string // bat_ id of an accepted batch, for blob uploads
}

func newLimitEnv(t *testing.T) *limitEnv {
	t.Helper()
	u, app := dbtest.Migrated(t)
	d := db.New(app)
	user, conn := uuid.New(), uuid.New()
	ownerPool := dbtest.Pool(t, u, db.OwnerRole)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users (id, username, password_hash) VALUES ($1, 'owner', 'synthetic')`, []any{user}},
		{`INSERT INTO connections (id, user_id, provider_id, mode, status)
			SELECT $1, $2, id, 'push', 'active' FROM providers WHERE code = 'apple_health'`, []any{conn, user}},
	} {
		if _, err := ownerPool.Exec(t.Context(), q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	keyPath := filepath.Join(t.TempDir(), "master.key")
	if _, err := crypto.WriteKeyFile(keyPath); err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.Load(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.Open(filepath.Join(t.TempDir(), "blobs"), kr)
	if err != nil {
		t.Fatal(err)
	}
	clientID, _, err := auth.CreateClientToken(t.Context(), d, user, conn, "device", "phone")
	if err != nil {
		t.Fatal(err)
	}
	rt, err := newRouter(slog.New(slog.DiscardHandler), newUITestFS(), Options{DB: d, Blobs: blobs, Keys: kr})
	if err != nil {
		t.Fatal(err)
	}
	e := &limitEnv{t: t, h: rt.handler(),
		owner:  &auth.Principal{Kind: auth.APIKey, UserID: user, ID: uuid.New(), Scopes: []auth.Scope{auth.Admin}},
		client: &auth.Principal{Kind: auth.Client, UserID: user, ID: clientID, ConnectionID: conn}}
	res := e.do(limitReq{p: e.client, method: http.MethodPost, target: "/api/ingest/v1/batches", key: "seed",
		body: batchBody(conn, testItem{key: "a", body: `{"v": 1}`})})
	var accepted struct {
		BatchID string `json:"batch_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&accepted); err != nil || res.StatusCode != http.StatusAccepted {
		t.Fatalf("seed batch: %d %v", res.StatusCode, err)
	}
	e.batch = accepted.BatchID
	return e
}

type limitReq struct {
	p                *auth.Principal
	method, target   string
	contentType, enc string
	key              string // Idempotency-Key
	body             []byte
	chunked          bool // no Content-Length: only the reading handler can notice an oversize body
}

func (e *limitEnv) do(r limitReq) *http.Response {
	e.t.Helper()
	var rd io.Reader = bytes.NewReader(r.body)
	if r.chunked {
		rd = io.MultiReader(rd) // hides the length from httptest
	}
	req := request(e.t, r.method, r.target, rd)
	if r.chunked {
		req.ContentLength = -1
	}
	if r.contentType != "" {
		req.Header.Set("Content-Type", r.contentType)
	}
	if r.enc != "" {
		req.Header.Set("Content-Encoding", r.enc)
	}
	if r.key != "" {
		req.Header.Set("Idempotency-Key", r.key)
	}
	return serve(e.t, e.h, req.WithContext(auth.WithPrincipal(req.Context(), r.p)))
}

func gzipBytes(t *testing.T, b []byte) []byte {
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

// nested is n levels of arrays: valid JSON that is only dangerous for a recursive consumer.
func nested(n int) []byte {
	return []byte(strings.Repeat("[", n) + strings.Repeat("]", n))
}

// TestBodyLimits sends hostile bodies to every path that takes a request body of its own
// class: ingest batch (10 MiB, gzip), ingest blob (25 MiB, gzip), document upload (25 MiB
// body, 20 MiB file), manual entry, rule spec and settings PATCH (1 MiB JSON). Every case must
// answer the documented problem and never 5xx. The API has no 415: a JSON endpoint reads the
// body whatever its Content-Type says, so a wrong type fails as invalid JSON (422), and the
// document upload, which has two accepted types, answers 422 for any other.
func TestBodyLimits(t *testing.T) {
	e := newLimitEnv(t)
	const (
		batches   = "/api/ingest/v1/batches"
		documents = "/api/v1/documents"
		manual    = "/api/v1/measurements/manual"
		rule      = "/api/v1/rules/steps/versions"
		settings  = "/api/v1/settings"
	)
	blobs := "/api/ingest/v1/batches/" + e.batch + "/blobs"
	zeros := func(n int) []byte { return make([]byte, n) }
	spaces := func(n int) []byte { return bytes.Repeat([]byte(" "), n) }
	pdf := labPDF(1, "synthetic-limits")
	bigPDF := append(append([]byte{}, pdf...), zeros(21*miB)...) // over the 20 MiB file limit, under the 25 MiB body limit
	ct, mp := multipartPDF(t, "lab.pdf", append(append([]byte{}, pdf...), zeros(26*miB)...))
	multipartBig, _ := io.ReadAll(mp)
	deepSpec := []byte(`{"spec":` + string(nested(5000)) + `}`)
	deepIngest := batchBody(e.client.ConnectionID, testItem{key: "deep", body: string(nested(5000))})

	ingestPost := func(target string, body []byte) limitReq {
		return limitReq{p: e.client, method: http.MethodPost, target: target, key: "k", body: body, contentType: "application/json"}
	}
	ownerReq := func(method, target string, body []byte) limitReq {
		return limitReq{p: e.owner, method: method, target: target, body: body, contentType: "application/json"}
	}
	with := func(r limitReq, f func(*limitReq)) limitReq { f(&r); return r }
	chunk := func(r limitReq) limitReq { r.chunked = true; return r }

	for _, c := range []struct {
		name   string
		req    limitReq
		status int
		code   Code
	}{
		// Ingest batch: 10 MiB, then the gzip guards (50 MiB absolute, 100x ratio).
		{"batch over 10 MiB", ingestPost(batches, spaces(10*miB+1)), 413, CodePayloadTooLarge},
		{"batch over 10 MiB, no length", chunk(ingestPost(batches, spaces(10*miB+1))), 413, CodePayloadTooLarge},
		{"batch gzip bomb", with(ingestPost(batches, gzipBytes(t, spaces(3*miB))), func(r *limitReq) { r.enc = "gzip" }), 413, CodePayloadTooLarge},
		{"batch unknown encoding", with(ingestPost(batches, []byte(`{}`)), func(r *limitReq) { r.enc = "br" }), 422, CodeValidationFailed},
		{"batch not gzip", with(ingestPost(batches, []byte(`{}`)), func(r *limitReq) { r.enc = "gzip" }), 422, CodeValidationFailed},
		{"batch wrong content type", with(ingestPost(batches, []byte("not json")), func(r *limitReq) { r.contentType = "text/plain" }), 422, CodeValidationFailed},
		{"batch deeply nested", ingestPost(batches, deepIngest), 422, CodeValidationFailed},
		{"batch beyond the decoder depth", ingestPost(batches, []byte(`{"items":`+string(nested(100_000))+`}`)), 422, CodeValidationFailed},
		// Ingest blob: raw parts up to 25 MiB.
		{"blob over 25 MiB", ingestPost(blobs, zeros(25*miB+1)), 413, CodePayloadTooLarge},
		{"blob over 25 MiB, no length", chunk(ingestPost(blobs, zeros(25*miB+1))), 413, CodePayloadTooLarge},
		{"blob gzip bomb", with(ingestPost(blobs, gzipBytes(t, zeros(3*miB))), func(r *limitReq) { r.enc = "gzip" }), 413, CodePayloadTooLarge},
		{"blob unknown encoding", with(ingestPost(blobs, []byte("x")), func(r *limitReq) { r.enc = "deflate" }), 422, CodeValidationFailed},
		// Document upload: 25 MiB body, 20 MiB file, only a PDF.
		{"document body over 25 MiB", limitReq{p: e.owner, method: http.MethodPost, target: documents, contentType: "application/pdf", body: zeros(25*miB + 1)}, 413, CodePayloadTooLarge},
		{"document body over 25 MiB, no length", limitReq{p: e.owner, method: http.MethodPost, target: documents, contentType: "application/pdf", body: zeros(25*miB + 1), chunked: true}, 413, CodePayloadTooLarge},
		{"document file over 20 MiB", limitReq{p: e.owner, method: http.MethodPost, target: documents, contentType: "application/pdf", body: bigPDF}, 413, CodePayloadTooLarge},
		{"document multipart over 25 MiB", limitReq{p: e.owner, method: http.MethodPost, target: documents, contentType: ct, body: multipartBig}, 413, CodePayloadTooLarge},
		{"document multipart over 25 MiB, no length", limitReq{p: e.owner, method: http.MethodPost, target: documents, contentType: ct, body: multipartBig, chunked: true}, 413, CodePayloadTooLarge},
		{"document wrong content type", limitReq{p: e.owner, method: http.MethodPost, target: documents, contentType: "text/plain", body: pdf}, 422, CodeValidationFailed},
		{"document no content type", limitReq{p: e.owner, method: http.MethodPost, target: documents, body: pdf}, 422, CodeValidationFailed},
		{"document not a pdf", limitReq{p: e.owner, method: http.MethodPost, target: documents, contentType: "application/pdf", body: []byte("MZ synthetic")}, 422, CodeValidationFailed},
		{"document gzip is not unpacked", limitReq{p: e.owner, method: http.MethodPost, target: documents, contentType: "application/pdf", enc: "gzip", body: gzipBytes(t, pdf)}, 422, CodeValidationFailed},
		// Owner JSON (1 MiB): manual entry, rule spec, settings.
		{"manual over 1 MiB", ownerReq(http.MethodPost, manual, spaces(miB+1)), 413, CodePayloadTooLarge},
		{"manual over 1 MiB, no length", chunk(ownerReq(http.MethodPost, manual, spaces(miB+1))), 413, CodePayloadTooLarge},
		{"manual wrong content type", with(ownerReq(http.MethodPost, manual, []byte("value=1")), func(r *limitReq) { r.contentType = "application/x-www-form-urlencoded" }), 422, CodeValidationFailed},
		{"manual deeply nested", ownerReq(http.MethodPost, manual, []byte(`{"metric":"steps","value":`+string(nested(5000))+`}`)), 422, CodeValidationFailed},
		{"manual beyond the decoder depth", ownerReq(http.MethodPost, manual, []byte(`{"value":`+string(nested(100_000))+`}`)), 422, CodeValidationFailed},
		{"rule over 1 MiB", ownerReq(http.MethodPost, rule, spaces(miB+1)), 413, CodePayloadTooLarge},
		{"rule over 1 MiB, no length", chunk(ownerReq(http.MethodPost, rule, spaces(miB+1))), 413, CodePayloadTooLarge},
		{"rule wrong content type", with(ownerReq(http.MethodPost, rule, []byte("<spec/>")), func(r *limitReq) { r.contentType = "application/xml" }), 422, CodeValidationFailed},
		{"rule spec deeply nested", ownerReq(http.MethodPost, rule, deepSpec), 422, CodeValidationFailed},
		{"rule spec beyond the decoder depth", ownerReq(http.MethodPost, rule, []byte(`{"spec":`+string(nested(100_000))+`}`)), 422, CodeValidationFailed},
		{"settings over 1 MiB", ownerReq(http.MethodPatch, settings, spaces(miB+1)), 413, CodePayloadTooLarge},
		{"settings over 1 MiB, no length", chunk(ownerReq(http.MethodPatch, settings, spaces(miB+1))), 413, CodePayloadTooLarge},
		{"settings wrong content type", with(ownerReq(http.MethodPatch, settings, []byte("a=b")), func(r *limitReq) { r.contentType = "text/plain" }), 422, CodeValidationFailed},
		{"settings deeply nested", ownerReq(http.MethodPatch, settings, []byte(`{"retention":`+string(nested(5000))+`}`)), 422, CodeValidationFailed},
		{"settings beyond the decoder depth", ownerReq(http.MethodPatch, settings, nested(100_000)), 422, CodeValidationFailed},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := e.do(c.req)
			if res.StatusCode != c.status {
				t.Fatalf("status %d, want %d", res.StatusCode, c.status)
			}
			if p := decodeProblem(t, res); p.Code != c.code {
				t.Fatalf("code %q, want %q", p.Code, c.code)
			}
		})
	}
}
