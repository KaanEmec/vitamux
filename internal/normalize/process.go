package normalize

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/ingest"
)

// Processor normalizes stored raw payloads one at a time, each in its own transaction, so one
// bad payload never costs the others (docs/plan/E07-normalization/J07.5-normalize-reprocess.md).
// A failure leaves the raw payload stored and marks it normalize_failed with a status_detail
// code and a warning; reprocess retries it after the normalizer is fixed.
type Processor struct {
	DB       *db.DB
	Blobs    *blob.Store
	Registry *Registry
	Log      *slog.Logger
}

// Outcome is what Process did with one raw payload.
type Outcome string

const (
	Normalized Outcome = "normalized" // canonical rows written (or confirmed unchanged)
	Failed     Outcome = "failed"     // marked normalize_failed; raw kept
	Skipped    Outcome = "skipped"    // quarantined, or an older raw version superseded by a newer one
)

// Failure codes, stored in raw_payloads.status_detail and as the code of the warning.
const (
	CodeNoNormalizer    = "no_normalizer"    // nothing registered accepts the stream and shape
	CodeAmbiguous       = "ambiguous"        // several normalizers accept it: a wiring error
	CodeRawUnreadable   = "raw_unreadable"   // the blob is missing or corrupt
	CodeNormalizerError = "normalizer_error" // Normalize returned an error (e.g. malformed payload)
	CodePanic           = "normalizer_panic" // Normalize or the writer panicked
	CodeInvalidOutput   = "invalid_output"   // the output breaks the canonical schema
	codeSuperseded      = "superseded_raw"   // not a failure: a newer raw version exists
)

// Result is the outcome of Process; Stats is zero unless the payload was Normalized.
type Result struct {
	Outcome Outcome
	Code    string // failure code, or the reason for a skip
	Stats   WriteStats
}

// failure is a normalization problem that belongs to the payload or the normalizer (never a
// database fault, which is returned as an error and retried by the job).
type failure struct {
	code, detail string
	versionID    *int32 // normalizer version that was tried; nil when none was chosen
}

const maxDetail = 200

// versions registers the registry's normalizer versions and returns their ids.
func (p *Processor) versions(ctx context.Context) (map[string]int32, error) {
	return RegisterVersions(ctx, p.DB.Q(), p.Registry)
}

// Process normalizes raw payload id. vers is the result of versions. Failures of the payload
// are recorded and reported as Outcome Failed with a nil error; a non-nil error is a database or
// context problem and the caller should retry (nothing was half-written).
func (p *Processor) Process(ctx context.Context, id int64, vers map[string]int32) (Result, error) {
	var res Result
	var err error
	// A writer racing on the same new key gets ErrConflict; the second try sees its row.
	for range 3 {
		if res, err = p.attempt(ctx, id, vers); !errors.Is(err, db.ErrConflict) {
			break
		}
	}
	return res, err
}

func (p *Processor) attempt(ctx context.Context, id int64, vers map[string]int32) (Result, error) {
	var res Result
	var fail *failure
	err := p.DB.Tx(ctx, func(q *dbq.Queries) error {
		res, fail = Result{}, nil // the Tx may run fn again
		row, err := q.LockRawForNormalize(ctx, id)
		if err != nil {
			return fmt.Errorf("normalize: raw payload %d: %w", id, db.MapErr(err))
		}
		switch {
		case ingest.Status(row.Status) == ingest.StatusQuarantined:
			res = Result{Outcome: Skipped, Code: string(ingest.StatusQuarantined)}
			return nil
		case row.Superseded:
			// A newer raw version of this record owns the canonical rows; writing this one would revert it.
			res = Result{Outcome: Skipped, Code: codeSuperseded}
			if ingest.Status(row.Status) == ingest.StatusNormalized {
				return nil
			}
			if err := ingest.SetStatus(ctx, q, id, ingest.StatusNormalized); err != nil {
				return err
			}
			return setResult(ctx, q, id, nil, codeSuperseded, nil)
		}

		fp := ""
		if row.ShapeFingerprint != nil {
			fp = *row.ShapeFingerprint
		}
		n, err := p.Registry.For(row.Stream, fp)
		if err != nil {
			code := CodeNoNormalizer
			if !errors.Is(err, ErrNoNormalizer) {
				code = CodeAmbiguous
			}
			fail = &failure{code: code, detail: "stream " + row.Stream}
			return errRollback
		}
		versionID := vers[n.ID()]
		who := fmt.Sprintf("%s@%d", n.ID(), n.Version())
		fail = &failure{versionID: &versionID}

		body, err := p.Blobs.Get(row.ContentSha256)
		if err != nil {
			fail.code, fail.detail = CodeRawUnreadable, who
			return errRollback
		}
		raw := RawPayload{ID: id, Stream: row.Stream, ExternalKey: row.ExternalKey, ContentType: row.ContentType,
			FetchedAt: row.FetchedAt, RequestMeta: row.RequestMeta, Body: body}
		var out Output
		var stats WriteStats
		err = p.guard(who, id, func() (err error) {
			if out, err = n.Normalize(ctx, raw, Env{Provider: row.Provider}); err != nil {
				return &normalizerError{err}
			}
			stats, err = Write(ctx, q, Source{ConnectionID: row.ConnectionID, RawPayloadID: id, NormalizerVersionID: versionID, Blobs: p.Blobs}, out)
			return err
		})
		var ne *normalizerError
		switch {
		case err == nil:
		case errors.Is(err, errPanic):
			fail.code, fail.detail = CodePanic, who
			return errRollback
		case errors.As(err, &ne):
			fail.code, fail.detail = CodeNormalizerError, who+": "+truncate(ne.Error())
			return errRollback
		case errors.Is(err, ErrInvalidOutput):
			fail.code, fail.detail = CodeInvalidOutput, who+": "+truncate(err.Error())
			return errRollback
		default:
			return err // database or context: the job retries
		}
		fail = nil

		if err := ingest.SetStatus(ctx, q, id, ingest.StatusNormalized); err != nil {
			return err
		}
		warnings := append(slices.Clip(out.Warnings), stats.Warnings...)
		if err := setResult(ctx, q, id, &versionID, "", warnings); err != nil {
			return err
		}
		res = Result{Outcome: Normalized, Stats: stats}
		return nil
	})
	switch {
	case errors.Is(err, errRollback) && fail != nil:
		// The attempt was rolled back; record the failure in its own transaction.
		return Result{Outcome: Failed, Code: fail.code}, p.markFailed(ctx, id, *fail)
	case err != nil:
		return Result{}, err
	}
	return res, nil
}

// errRollback aborts the attempt's transaction after a failure that is recorded separately.
var errRollback = errors.New("normalize: attempt rolled back")

var errPanic = errors.New("normalize: panic")

// normalizerError marks an error returned by Normalize, as opposed to one from the writer.
type normalizerError struct{ err error }

func (e *normalizerError) Error() string { return e.err.Error() }
func (e *normalizerError) Unwrap() error { return e.err }

// guard runs fn and turns a panic into errPanic. The panic value is not stored or logged
// (it could echo payload content); its type and the stack are.
func (p *Processor) guard(who string, id int64, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			p.Log.Error("normalizer panicked", "normalizer", who, "raw_payload_id", id,
				"panic_type", fmt.Sprintf("%T", r), "stack", string(debug.Stack()))
			err = errPanic
		}
	}()
	return fn()
}

// markFailed moves the payload to normalize_failed with its reason. Canonical rows of an earlier
// successful version stay as they are.
func (p *Processor) markFailed(ctx context.Context, id int64, f failure) error {
	return p.DB.Tx(ctx, func(q *dbq.Queries) error {
		st, err := q.GetRawStatus(ctx, id)
		if err != nil {
			return db.MapErr(err)
		}
		if ingest.Status(st) == ingest.StatusNormalized { // normalized → failed goes through stored
			if err := ingest.SetStatus(ctx, q, id, ingest.StatusStored); err != nil {
				return err
			}
		}
		if err := ingest.SetStatus(ctx, q, id, ingest.StatusNormalizeFailed); err != nil {
			return err
		}
		return setResult(ctx, q, id, f.versionID, f.code, []Warning{{Code: f.code, Detail: f.detail}})
	})
}

// setResult stores the outcome columns; detail "" means none.
func setResult(ctx context.Context, q *dbq.Queries, id int64, versionID *int32, detail string, warnings []Warning) error {
	if warnings == nil {
		warnings = []Warning{}
	}
	b, err := json.Marshal(warnings)
	if err != nil {
		return err
	}
	var d *string
	if detail != "" {
		d = &detail
	}
	return db.MapErr(q.SetRawNormalizeResult(ctx, dbq.SetRawNormalizeResultParams{
		ID: id, NormalizerVersionID: versionID, StatusDetail: d, Warnings: b}))
}

func truncate(s string) string {
	if len(s) > maxDetail {
		s = strings.ToValidUTF8(s[:maxDetail], "")
	}
	return s
}
