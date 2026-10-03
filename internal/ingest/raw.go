package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// SourceKind says how a batch arrived (ingest_batches.source_kind).
type SourceKind string

const (
	SourceSync   SourceKind = "sync"
	SourcePush   SourceKind = "push"
	SourceImport SourceKind = "import"
	SourceManual SourceKind = "manual"
)

// BatchInfo describes a new ingest batch.
type BatchInfo struct {
	UserID, ConnectionID uuid.UUID
	ClientID             *uuid.UUID // pushing client; nil for in-process syncs
	SourceKind           SourceKind
	MigrationSource      string // optional, migration importers only
	IdempotencyKey       string // optional; unique per client
}

// BatchRef names the batch that raw rows belong to.
type BatchRef struct{ ID, UserID, ConnectionID uuid.UUID }

// CreateBatch inserts an ingest_batches row. A repeated (client, idempotency key) fails with
// db.ErrConflict once the surrounding Tx maps the error.
func CreateBatch(ctx context.Context, q *dbq.Queries, in BatchInfo) (BatchRef, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return BatchRef{}, err
	}
	err = q.InsertIngestBatch(ctx, dbq.InsertIngestBatchParams{
		ID: id, UserID: in.UserID, ConnectionID: in.ConnectionID, ClientID: in.ClientID,
		SourceKind: string(in.SourceKind), MigrationSource: optional(in.MigrationSource), IdempotencyKey: optional(in.IdempotencyKey),
	})
	return BatchRef{ID: id, UserID: in.UserID, ConnectionID: in.ConnectionID}, err
}

// RawItem is one verbatim source record for StoreRaw: inline Body, or BlobSHA256 of a blob
// already stored with blob.Store.Put (binary files uploaded separately).
type RawItem struct {
	Stream      string
	ExternalKey string
	ContentType string
	FetchedAt   time.Time
	Body        []byte
	BlobSHA256  []byte
	Request     Request // sanitized before storage
	Quarantine  bool    // store as quarantined (the connector detected schema drift)
}

// Outcome is what StoreRaw did with an item.
type Outcome string

const (
	Stored     Outcome = "stored"      // first version of this external key
	Duplicate  Outcome = "duplicate"   // same content as the latest version; nothing written
	NewVersion Outcome = "new_version" // changed content; supersedes the latest version
)

// Result reports one item, in input order.
type Result struct {
	ExternalKey  string
	Outcome      Outcome
	RawPayloadID int64
	Version      int32
	SupersedesID *int64
}

// rawKeyLockClass is the advisory lock class (two-int form) for one raw versioning key.
const rawKeyLockClass int32 = 0x766d7872 // "vmxr"

// StoreRaw stores items as raw payloads of batch b, inside the caller's transaction, so blob
// file, raw rows and (for syncs) the cursor advance commit together. Per (connection,
// stream, external_key) it is idempotent: content equal to the latest version is a
// Duplicate, other content a NewVersion that points at the version it supersedes.
func StoreRaw(ctx context.Context, q *dbq.Queries, blobs *blob.Store, b BatchRef, items []RawItem) ([]Result, error) {
	for i, it := range items {
		if err := it.check(); err != nil {
			return nil, fmt.Errorf("ingest: item %d: %w", i, err)
		}
	}
	out := make([]Result, len(items))
	for i, it := range items {
		r, err := storeOne(ctx, q, blobs, b, it)
		if err != nil {
			return nil, fmt.Errorf("ingest: item %d: %w", i, err)
		}
		out[i] = r
	}
	return out, nil
}

func (it RawItem) check() error {
	switch {
	case it.Stream == "" || it.ExternalKey == "" || it.ContentType == "":
		return errors.New("stream, external key and content type are required")
	case it.FetchedAt.IsZero():
		return errors.New("fetched_at is required")
	case (it.Body == nil) == (it.BlobSHA256 == nil):
		return errors.New("exactly one of body or blob hash is required")
	case it.BlobSHA256 != nil && len(it.BlobSHA256) != sha256.Size:
		return errors.New("blob hash must be 32 bytes")
	}
	return nil
}

func storeOne(ctx context.Context, q *dbq.Queries, blobs *blob.Store, b BatchRef, it RawItem) (Result, error) {
	sum := it.BlobSHA256
	if it.Body != nil {
		s := sha256.Sum256(it.Body)
		sum = s[:]
	}
	if err := q.LockRawKey(ctx, dbq.LockRawKeyParams{
		Class: rawKeyLockClass, Key: b.ConnectionID.String() + "\n" + it.Stream + "\n" + it.ExternalKey,
	}); err != nil {
		return Result{}, err
	}
	res := Result{ExternalKey: it.ExternalKey, Outcome: Stored, Version: 1}
	latest, err := q.LatestRawVersion(ctx, dbq.LatestRawVersionParams{ConnectionID: b.ConnectionID, Stream: it.Stream, ExternalKey: it.ExternalKey})
	switch err = db.MapErr(err); {
	case err == nil && bytes.Equal(latest.ContentSha256, sum):
		return Result{ExternalKey: it.ExternalKey, Outcome: Duplicate, RawPayloadID: latest.ID, Version: latest.Version}, nil
	case err == nil:
		res.Outcome, res.Version, res.SupersedesID = NewVersion, latest.Version+1, &latest.ID
	case !errors.Is(err, db.ErrNotFound):
		return Result{}, err
	}

	var fingerprint *string
	if it.Body != nil {
		if _, err := blobs.Put(ctx, q, bytes.NewReader(it.Body), blob.Plain); err != nil {
			return Result{}, err
		}
		if jsonTypeRe.MatchString(it.ContentType) {
			// Invalid JSON is still stored verbatim; the normalizer reports it.
			if fp, err := ShapeFingerprint(it.Body); err == nil {
				fingerprint = &fp
			}
		}
	}
	if err := blob.Retain(ctx, q, sum); err != nil {
		return Result{}, fmt.Errorf("blob: %w", err)
	}
	status := StatusStored
	if it.Quarantine {
		status = StatusQuarantined
	}
	res.RawPayloadID, err = q.InsertRawPayload(ctx, dbq.InsertRawPayloadParams{
		UserID: b.UserID, ConnectionID: b.ConnectionID, BatchID: b.ID,
		Stream: it.Stream, ExternalKey: it.ExternalKey, Version: res.Version, SupersedesID: res.SupersedesID,
		ContentSha256: sum, ContentType: it.ContentType, FetchedAt: it.FetchedAt,
		RequestMeta: SanitizeRequest(it.Request), ShapeFingerprint: fingerprint, Status: string(status),
	})
	return res, err
}

// Status is a raw payload's processing state (raw_payloads.status).
type Status string

const (
	StatusStored          Status = "stored"
	StatusNormalized      Status = "normalized"
	StatusNormalizeFailed Status = "normalize_failed"
	StatusQuarantined     Status = "quarantined"
)

// transitions lists the allowed moves; staying in the same status is always allowed, so
// retried jobs are idempotent. Back to stored means "normalize again" (reprocess, retry, or
// release from quarantine after a normalizer fix).
var transitions = map[Status][]Status{
	StatusStored:          {StatusNormalized, StatusNormalizeFailed, StatusQuarantined},
	StatusNormalizeFailed: {StatusStored, StatusNormalized, StatusQuarantined},
	StatusNormalized:      {StatusStored},
	StatusQuarantined:     {StatusStored},
}

// ErrTransition means the raw payload's current status does not allow the requested one.
var ErrTransition = errors.New("ingest: raw status transition not allowed")

// SetStatus moves raw payload id to status to. It returns db.ErrNotFound for a missing row
// and ErrTransition when the current status does not allow the move.
func SetStatus(ctx context.Context, q *dbq.Queries, id int64, to Status) error {
	from := []string{string(to)}
	for f, tos := range transitions {
		if slices.Contains(tos, to) {
			from = append(from, string(f))
		}
	}
	n, err := q.SetRawStatus(ctx, dbq.SetRawStatusParams{Status: string(to), ID: id, FromStatus: from})
	if err != nil || n == 1 {
		return err
	}
	if _, err := q.GetRawStatus(ctx, id); err != nil {
		return db.MapErr(err)
	}
	return ErrTransition
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
