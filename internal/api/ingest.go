package api

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/ingest"
)

// Push ingest API (docs/architecture/connectors.md#push-ingest-contract).
func (rt *router) ingestRoutes() {
	client := access{ingest: true}
	rt.handle("POST /api/ingest/v1/batches", client, rt.postBatch)
	rt.handle("GET /api/ingest/v1/batches/{batch}", client, rt.getBatch)
	rt.handle("POST /api/ingest/v1/batches/{batch}/blobs", client, rt.postBlob)
	rt.handle("POST /api/ingest/v1/heartbeat", client, rt.postHeartbeat)
}

const (
	idempotencyHeader = "Idempotency-Key"
	maxIdempotencyKey = 255
	// Decompression guard for gzip bodies: at most maxDecompressed in total, and at most
	// maxRatio times the compressed bytes read plus ratioSlack, so a small body cannot
	// expand into a large allocation.
	maxDecompressed = 50 * miB
	maxRatio        = 100
	ratioSlack      = 1 * miB
)

var (
	errDecompressedTooLarge = errors.New("decompressed body exceeds the limit")
	errBadEncoding          = errors.New("bad content encoding")
	errKeyReused            = errors.New("idempotency key reused with a different request")
)

func (rt *router) postBatch(w http.ResponseWriter, r *http.Request) {
	if !rt.ingestReady(w, r) {
		return
	}
	key, body, ok := readIngest(w, r)
	if !ok {
		return
	}
	if err := checkJSONDepth(body); err != nil {
		writeBodyError(w, r, err)
		return
	}
	b, err := ingest.DecodeBatch(body)
	if err != nil {
		writeValidation(w, r, err)
		return
	}
	p := auth.PrincipalFrom(r.Context())
	conn, err := ingest.ParseConnectionID(b.ConnectionID)
	if err != nil || !p.CanIngest(conn) {
		writeProblem(w, r, CodeForbidden, "this token may not ingest into connection_id")
		return
	}
	info := ingest.BatchInfo{UserID: p.UserID, ConnectionID: conn, ClientID: &p.ID, SourceKind: ingest.SourcePush, IdempotencyKey: key}
	if b.Provenance != nil && b.Provenance.MigrationSource != nil {
		info.MigrationSource = *b.Provenance.MigrationSource
	}
	rt.idempotent(w, r, p, key, body, func(q *dbq.Queries) (int, any, error) {
		acc, err := ingest.AcceptPush(r.Context(), q, rt.opts.Blobs, info, b)
		return http.StatusAccepted, acc, err
	})
}

func (rt *router) postBlob(w http.ResponseWriter, r *http.Request) {
	if !rt.ingestReady(w, r) || !rt.ownBatch(w, r) {
		return
	}
	key, body, ok := readIngest(w, r)
	if !ok {
		return
	}
	rt.idempotent(w, r, auth.PrincipalFrom(r.Context()), key, body, func(q *dbq.Queries) (int, any, error) {
		info, err := rt.opts.Blobs.Put(r.Context(), q, bytes.NewReader(body), blob.Plain)
		return http.StatusCreated, map[string]any{"blob_sha256": hex.EncodeToString(info.SHA256), "size_bytes": info.Size}, err
	})
}

func (rt *router) getBatch(w http.ResponseWriter, r *http.Request) {
	if !rt.ingestReady(w, r) {
		return
	}
	id, err := ingest.ParseBatchID(r.PathValue("batch"))
	if err != nil {
		writeProblem(w, r, CodeNotFound, "no such batch")
		return
	}
	st, err := ingest.GetBatchStatus(r.Context(), rt.opts.DB.Q(), id)
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeProblem(w, r, CodeNotFound, "no such batch")
	case err != nil:
		rt.internal(w, r, "batch status", err)
	case !auth.PrincipalFrom(r.Context()).CanIngest(st.ConnectionID):
		writeProblem(w, r, CodeForbidden, "the batch belongs to another connection")
	default:
		writeJSON(rt.log, w, http.StatusOK, st)
	}
}

func (rt *router) postHeartbeat(w http.ResponseWriter, r *http.Request) {
	if rt.opts.DB == nil {
		writeProblem(w, r, CodeUnavailable, "ingest is unavailable")
		return
	}
	body, err := readBody(r)
	if err != nil {
		writeReadError(w, r, err)
		return
	}
	if err := checkJSONDepth(body); err != nil {
		writeBodyError(w, r, err)
		return
	}
	h, err := ingest.DecodeHeartbeat(body)
	if err != nil {
		writeValidation(w, r, err)
		return
	}
	p := auth.PrincipalFrom(r.Context())
	if conn, err := ingest.ParseConnectionID(h.ConnectionID); err != nil || !p.CanIngest(conn) {
		writeProblem(w, r, CodeForbidden, "this token may not report for connection_id")
		return
	}
	err = rt.opts.DB.Tx(r.Context(), func(q *dbq.Queries) error {
		return ingest.RecordHeartbeat(r.Context(), q, p.ID, p.ConnectionID, h, time.Now())
	})
	if err != nil {
		rt.internal(w, r, "heartbeat", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (rt *router) ingestReady(w http.ResponseWriter, r *http.Request) bool {
	if rt.opts.DB == nil || rt.opts.Blobs == nil {
		writeProblem(w, r, CodeUnavailable, "ingest is unavailable: the database or blob store is not ready")
		return false
	}
	return true
}

// ownBatch checks that the {batch} path value names a batch of the caller's connection.
func (rt *router) ownBatch(w http.ResponseWriter, r *http.Request) bool {
	id, err := ingest.ParseBatchID(r.PathValue("batch"))
	var b dbq.GetIngestBatchRow
	if err == nil {
		b, err = rt.opts.DB.Q().GetIngestBatch(r.Context(), id)
		err = db.MapErr(err)
	} else {
		err = db.ErrNotFound
	}
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeProblem(w, r, CodeNotFound, "no such batch")
	case err != nil:
		rt.internal(w, r, "load batch", err)
	case !auth.PrincipalFrom(r.Context()).CanIngest(b.ConnectionID):
		writeProblem(w, r, CodeForbidden, "the batch belongs to another connection")
	default:
		return true
	}
	return false
}

// readIngest reads the Idempotency-Key header and the (possibly gzip) body of an ingest POST,
// answering the problem itself when either is unusable.
func readIngest(w http.ResponseWriter, r *http.Request) (string, []byte, bool) {
	key := r.Header.Get(idempotencyHeader)
	if key == "" || len(key) > maxIdempotencyKey {
		writeProblem(w, r, CodeValidationFailed, "the "+idempotencyHeader+" header is required (1 to 255 characters)")
		return "", nil, false
	}
	body, err := readBody(r)
	if err != nil {
		writeReadError(w, r, err)
		return "", nil, false
	}
	return key, body, true
}

// readBody reads the request body, decoding Content-Encoding gzip behind the decompression guard.
func readBody(r *http.Request) ([]byte, error) {
	switch enc := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))); enc {
	case "", "identity":
		return io.ReadAll(r.Body)
	case "gzip":
		in := &countingReader{r: r.Body}
		zr, err := gzip.NewReader(in)
		if err != nil {
			return nil, encodingErr(err)
		}
		body, err := io.ReadAll(&guardReader{r: zr, in: in})
		if err != nil {
			return nil, encodingErr(err)
		}
		return body, nil
	default:
		return nil, errBadEncoding
	}
}

// encodingErr keeps limit errors and turns gzip format errors into errBadEncoding.
func encodingErr(err error) error {
	if tooLarge := (*http.MaxBytesError)(nil); errors.As(err, &tooLarge) || errors.Is(err, errDecompressedTooLarge) {
		return err
	}
	return errBadEncoding
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// guardReader fails with errDecompressedTooLarge once the decompressed size passes the
// absolute or the ratio limit.
type guardReader struct {
	r  io.Reader
	in *countingReader
	n  int64
}

func (g *guardReader) Read(p []byte) (int, error) {
	n, err := g.r.Read(p)
	g.n += int64(n)
	if g.n > maxDecompressed || g.n > maxRatio*g.in.n+ratioSlack {
		return n, errDecompressedTooLarge
	}
	return n, err
}

func writeReadError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errDecompressedTooLarge):
		writeProblem(w, r, CodePayloadTooLarge, "decompressed body exceeds 50 MiB or 100 times its compressed size")
	case errors.Is(err, errBadEncoding):
		writeProblem(w, r, CodeValidationFailed, "Content-Encoding must be gzip or absent, and the body must decode")
	default:
		writeBodyError(w, r, err)
	}
}

func writeValidation(w http.ResponseWriter, r *http.Request, err error) {
	var ve *ingest.ValidationError
	if !errors.As(err, &ve) {
		writeProblem(w, r, CodeValidationFailed, "invalid body")
		return
	}
	errs := make([]FieldError, len(ve.Errors))
	for i, f := range ve.Errors {
		errs[i] = FieldError{Pointer: f.Pointer, Detail: f.Detail}
	}
	writeProblem(w, r, CodeValidationFailed, "the body does not match the schema", errs...)
}

// idempotent runs fn once per (client, Idempotency-Key), in one transaction with the claim
// of the key, and stores its response. Replaying the same request (method, path and
// decompressed body) answers the stored response; another request with the key is a 409.
// Failures store nothing, so the client may retry with the same key.
func (rt *router) idempotent(w http.ResponseWriter, r *http.Request, p *auth.Principal, key string, body []byte,
	fn func(*dbq.Queries) (int, any, error)) {
	h := sha256.New()
	h.Write([]byte(r.Method + " " + r.URL.Path + "\n"))
	h.Write(body)
	sum := h.Sum(nil)

	var status int
	var resp []byte
	var replayed bool
	err := rt.opts.DB.Tx(r.Context(), func(q *dbq.Queries) error {
		status, resp, replayed = 0, nil, false
		n, err := q.ClaimIdempotencyKey(r.Context(), dbq.ClaimIdempotencyKeyParams{ClientID: p.ID, Key: key, RequestSha256: sum})
		if err != nil {
			return err
		}
		if n == 0 { // claimed and committed before: replay or conflict
			row, err := q.GetIdempotencyKey(r.Context(), dbq.GetIdempotencyKeyParams{ClientID: p.ID, Key: key})
			switch {
			case err != nil:
				return err
			case !bytes.Equal(row.RequestSha256, sum):
				return errKeyReused
			case row.ResponseStatus == nil:
				return errors.New("idempotency key committed without a response")
			}
			status, resp, replayed = int(*row.ResponseStatus), row.ResponseBody, true
			return nil
		}
		st, v, err := fn(q)
		if err != nil {
			return err
		}
		if resp, err = json.Marshal(v); err != nil {
			return err
		}
		status = st
		resp, err = q.SetIdempotencyResponse(r.Context(), dbq.SetIdempotencyResponseParams{
			ResponseStatus: int16(st), ResponseBody: resp, ClientID: p.ID, Key: key, //nolint:gosec // HTTP status fits
		})
		return err
	})
	var ve *ingest.ValidationError
	switch {
	case errors.Is(err, errKeyReused):
		writeProblem(w, r, CodeConflict, "this "+idempotencyHeader+" was already used for a different request")
	case errors.As(err, &ve):
		writeValidation(w, r, err)
	case err != nil:
		rt.internal(w, r, "ingest", err)
	default:
		if replayed {
			w.Header().Set("Idempotent-Replayed", "true")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_, _ = w.Write(resp)
	}
}
