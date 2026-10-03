package export

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

// Kind is the job kind that builds an export.
const Kind = "export"

const (
	// Retention is how long a finished export stays downloadable; the next export deletes it.
	Retention = 7 * 24 * time.Hour
	// TokenTTL bounds a download token, which also works only once.
	TokenTTL = 10 * time.Minute
)

var (
	// ErrRunning means the owner already has an export queued or running.
	ErrRunning = errors.New("export: an export is already queued or running")
	// ErrBadToken means the download token is unknown, expired or used.
	ErrBadToken = errors.New("export: download token is invalid, expired or already used")
)

// Export is an export's status.
type Export struct {
	ID         uuid.UUID
	Status     string // queued, running, done, failed
	Format     string
	IncludeRaw bool
	CreatedAt  time.Time
	FinishedAt *time.Time
	ExpiresAt  *time.Time
	SizeBytes  *int64
	// DownloadToken is a fresh one-time token, set by Get while the export is downloadable.
	DownloadToken string
}

type payload struct {
	ExportID uuid.UUID `json:"export_id"`
}

// Create queues an export for userID. actor is recorded in the audit trail.
func Create(ctx context.Context, d *db.DB, userID uuid.UUID, format string, includeRaw bool, actor string) (Export, error) {
	if format != FormatNDJSON && format != FormatCSV {
		return Export{}, fmt.Errorf("export: unknown format %q", format)
	}
	id := uuid.Must(uuid.NewV7())
	err := d.Tx(ctx, func(q *dbq.Queries) error {
		jobID, created, err := jobs.Enqueue(ctx, q, jobs.NewJob{Kind: Kind, Priority: jobs.PriorityHigh, MaxAttempts: 3,
			DedupeKey: "export:" + userID.String(), Payload: payload{ExportID: id}})
		if err != nil {
			return err
		}
		if !created {
			return ErrRunning
		}
		if err := q.InsertExport(ctx, dbq.InsertExportParams{ID: id, UserID: userID, JobID: &jobID, Format: format, IncludeRaw: includeRaw}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &userID, Actor: actor, Action: "export.create", TargetType: "export",
			TargetID: id.String(), Detail: map[string]any{"format": format, "include_raw": includeRaw}})
	})
	if err != nil {
		return Export{}, err
	}
	return get(ctx, d, userID, id, false)
}

// Get returns an export of userID (db.ErrNotFound if there is none). A finished, unexpired
// export carries a new download token, which replaces any earlier one.
func Get(ctx context.Context, d *db.DB, userID, id uuid.UUID) (Export, error) {
	return get(ctx, d, userID, id, true)
}

func get(ctx context.Context, d *db.DB, userID, id uuid.UUID, mint bool) (Export, error) {
	r, err := d.Q().GetExport(ctx, dbq.GetExportParams{ID: id, UserID: userID})
	if err != nil {
		return Export{}, db.MapErr(err)
	}
	e := Export{ID: r.ID, Status: r.Status, Format: r.Format, IncludeRaw: r.IncludeRaw, CreatedAt: r.CreatedAt,
		FinishedAt: r.FinishedAt, ExpiresAt: r.ExpiresAt, SizeBytes: r.SizeBytes}
	if !mint || e.Status != "done" {
		return e, nil
	}
	token := make([]byte, 32)
	_, _ = rand.Read(token) // never fails (crypto/rand)
	e.DownloadToken = base64.RawURLEncoding.EncodeToString(token)
	n, err := d.Q().SetExportToken(ctx, dbq.SetExportTokenParams{ID: id, UserID: userID, TokenHash: tokenHash(e.DownloadToken), Ttl: TokenTTL})
	if err != nil {
		return Export{}, err
	}
	if n == 0 { // expired
		e.DownloadToken = ""
	}
	return e, nil
}

func tokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// Open spends the download token of export id and returns the zip and its size: ErrBadToken
// for a wrong, expired or used token, db.ErrNotFound for an unknown export.
func Open(ctx context.Context, d *db.DB, blobs *blob.Store, userID, id uuid.UUID, token string) (io.ReadCloser, int64, error) {
	if _, err := d.Q().GetExport(ctx, dbq.GetExportParams{ID: id, UserID: userID}); err != nil {
		return nil, 0, db.MapErr(err)
	}
	r, err := d.Q().UseExportToken(ctx, dbq.UseExportTokenParams{ID: id, UserID: userID, TokenHash: tokenHash(token)})
	if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
		return nil, 0, ErrBadToken
	} else if err != nil {
		return nil, 0, err
	}
	rc, err := blobs.Open(r.BlobSha256)
	if err != nil {
		return nil, 0, err
	}
	var size int64
	if r.SizeBytes != nil {
		size = *r.SizeBytes
	}
	return rc, size, nil
}

// Handler runs export jobs.
func Handler(d *db.DB, blobs *blob.Store) jobs.Handler {
	return func(ctx context.Context, j jobs.Job) error {
		var p payload
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return jobs.Permanent(fmt.Errorf("export payload: %w", err))
		}
		return Run(ctx, d, blobs, p.ExportID)
	}
}

// Run builds export id: it streams the zip from Write straight into the blob store, so
// nothing is buffered, and marks the export done. Expired exports are deleted first.
func Run(ctx context.Context, d *db.DB, blobs *blob.Store, id uuid.UUID) error {
	e, err := d.Q().GetExportForRun(ctx, id)
	if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
		return jobs.Permanent(err)
	} else if err != nil {
		return err
	}
	if e.FinishedAt != nil {
		return nil
	}
	if err := pruneExpired(ctx, d); err != nil {
		return fmt.Errorf("prune exports: %w", err)
	}

	pr, pw := io.Pipe()
	written := make(chan error, 1)
	go func() {
		_, err := Write(ctx, d, blobs, pw, Options{UserID: e.UserID, Format: e.Format, IncludeRaw: e.IncludeRaw})
		_ = pw.CloseWithError(err) // nil closes normally: the reader sees EOF
		written <- err
	}()
	ran := false
	err = d.Tx(ctx, func(q *dbq.Queries) error {
		if ran { // the pipe is consumed; a retry would store an empty zip
			return errors.New("export: store transaction retried")
		}
		ran = true
		info, err := blobs.Put(ctx, q, pr, blob.Plain)
		if err != nil {
			return err
		}
		if err := blob.Retain(ctx, q, info.SHA256); err != nil {
			return err
		}
		return q.FinishExport(ctx, dbq.FinishExportParams{ID: id, BlobSha256: info.SHA256, SizeBytes: &info.Size, Ttl: Retention})
	})
	_ = pr.CloseWithError(errors.New("export: store stopped")) // unblocks Write if Put gave up early
	if werr := <-written; err == nil {
		err = werr
	}
	return err
}

func pruneExpired(ctx context.Context, d *db.DB) error {
	return d.Tx(ctx, func(q *dbq.Queries) error {
		sums, err := q.DeleteExpiredExports(ctx)
		if err != nil {
			return err
		}
		for _, sum := range sums {
			if err := blob.Release(ctx, q, sum); err != nil {
				return err
			}
		}
		return nil
	})
}
