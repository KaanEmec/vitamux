package documents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// Document statuses (documents.status).
const (
	StatusUploaded    = "uploaded"
	StatusExtracting  = "extracting"
	StatusNeedsReview = "needs_review"
	StatusConfirmed   = "confirmed"
	StatusDeleted     = "deleted"
)

// Derived says what deleting a document does to the lab results confirmed from it.
type Derived string

const (
	KeepDerived   Derived = "keep"
	DeleteDerived Derived = "delete"
)

// Store keeps lab PDFs: validated, sealed with a per-document key, deduplicated by SHA-256.
type Store struct {
	db     *db.DB
	blobs  *blob.Store
	keys   *crypto.Keyring
	limits Limits
	now    func() time.Time
}

// New returns a Store with DefaultLimits.
func New(d *db.DB, blobs *blob.Store, keys *crypto.Keyring) *Store {
	return &Store{db: d, blobs: blobs, keys: keys, limits: DefaultLimits, now: time.Now}
}

// Limits returns the upload limits the store enforces.
func (s *Store) Limits() Limits { return s.limits }

// Document is a stored document with its filename decrypted. A deleted document keeps only
// its id, status, sizes and times.
type Document struct {
	ID             uuid.UUID
	Status         string
	SHA256         []byte
	Filename       string // empty when none was given, or after deletion
	SizeBytes      int64
	Pages          int
	UploadedAt     time.Time
	RetentionUntil *time.Time
	DeletedAt      *time.Time
}

// Upload validates the PDF read from r and stores it for user. When the user already has a
// live document with the same content, that document is returned with existing set and
// nothing new is stored. A *RejectError says why a file was refused; errors from r (such as
// *http.MaxBytesError) are returned as they are.
func (s *Store) Upload(ctx context.Context, user uuid.UUID, actor, filename string, r io.Reader) (doc Document, existing bool, err error) {
	pdf, err := io.ReadAll(io.LimitReader(r, s.limits.MaxBytes+1))
	if err != nil {
		return Document{}, false, err
	}
	pages, err := Validate(pdf, s.limits)
	if err != nil {
		return Document{}, false, err
	}
	sum := sha256.Sum256(pdf)

	id, err := uuid.NewV7()
	if err != nil {
		return Document{}, false, err
	}
	key, sealedKey, err := newDataKey(s.keys, id)
	if err != nil {
		return Document{}, false, err
	}
	defer clear(key)
	sealed, err := sealWith(key, pdf, fieldAAD(id, "pdf"))
	if err != nil {
		return Document{}, false, err
	}
	var name []byte
	if filename != "" {
		if name, err = sealWith(key, []byte(filename), fieldAAD(id, "filename")); err != nil {
			return Document{}, false, err
		}
	}

	var row dbq.Document
	err = s.db.Tx(ctx, func(q *dbq.Queries) error {
		existing = false
		row, err = q.GetLiveDocumentBySHA256(ctx, dbq.GetLiveDocumentBySHA256Params{UserID: user, Sha256: sum[:]})
		if err == nil {
			existing = true
			return audit.Record(ctx, q, audit.Event{UserID: &user, Actor: actor, Action: "document.upload",
				TargetType: "document", TargetID: row.ID.String(), Detail: map[string]any{"duplicate": true}})
		}
		if err = db.MapErr(err); !errors.Is(err, db.ErrNotFound) {
			return err
		}
		policy, err := GetPolicy(ctx, q, user)
		if err != nil {
			return err
		}
		var until *time.Time
		if policy.RetentionDays != nil {
			t := s.now().AddDate(0, 0, *policy.RetentionDays)
			until = &t
		}
		info, err := s.blobs.Put(ctx, q, bytes.NewReader(sealed), blob.Plain)
		if err != nil {
			return err
		}
		if err := blob.Retain(ctx, q, info.SHA256); err != nil {
			return err
		}
		if err := q.InsertDocument(ctx, dbq.InsertDocumentParams{
			ID: id, UserID: user, Sha256: sum[:], BlobSha256: info.SHA256, FilenameCiphertext: name,
			SizeBytes: int64(len(pdf)), PageCount: int32(pages), UploadedBy: actor, RetentionUntil: until, //nolint:gosec // pages <= MaxPages
		}); err != nil {
			return err
		}
		if err := q.InsertDocumentKey(ctx, dbq.InsertDocumentKeyParams{DocumentID: id, Ciphertext: sealedKey, KeyID: s.keys.KeyID()}); err != nil {
			return err
		}
		if err := audit.Record(ctx, q, audit.Event{UserID: &user, Actor: actor, Action: "document.upload",
			TargetType: "document", TargetID: id.String(), Detail: map[string]any{"size_bytes": len(pdf), "pages": pages}}); err != nil {
			return err
		}
		row, err = q.GetDocument(ctx, dbq.GetDocumentParams{UserID: user, ID: id})
		return err
	})
	if errors.Is(err, db.ErrConflict) { // the same content was uploaded concurrently
		row, err = s.db.Q().GetLiveDocumentBySHA256(ctx, dbq.GetLiveDocumentBySHA256Params{UserID: user, Sha256: sum[:]})
		existing, err = true, db.MapErr(err)
	}
	if err != nil {
		return Document{}, false, err
	}
	doc, err = s.decode(ctx, s.db.Q(), row)
	return doc, existing, err
}

// Get returns one of the user's documents, including a deleted one.
func (s *Store) Get(ctx context.Context, user, id uuid.UUID) (Document, error) {
	q := s.db.Q()
	row, err := q.GetDocument(ctx, dbq.GetDocumentParams{UserID: user, ID: id})
	if err != nil {
		return Document{}, db.MapErr(err)
	}
	return s.decode(ctx, q, row)
}

// List returns up to limit live documents, newest first, after the document id after (nil
// for the first page).
func (s *Store) List(ctx context.Context, user uuid.UUID, after *uuid.UUID, limit int32) ([]Document, error) {
	q := s.db.Q()
	rows, err := q.ListDocuments(ctx, dbq.ListDocumentsParams{UserID: user, After: after, Lim: limit})
	if err != nil {
		return nil, err
	}
	docs := make([]Document, len(rows))
	for i, r := range rows {
		if docs[i], err = s.decode(ctx, q, r); err != nil {
			return nil, err
		}
	}
	return docs, nil
}

// File returns the original PDF of a live document; db.ErrNotFound once it is deleted.
func (s *Store) File(ctx context.Context, user, id uuid.UUID) ([]byte, error) {
	q := s.db.Q()
	row, err := q.GetDocument(ctx, dbq.GetDocumentParams{UserID: user, ID: id})
	if err != nil {
		return nil, db.MapErr(err)
	}
	if row.Status == StatusDeleted {
		return nil, db.ErrNotFound
	}
	key, err := dataKey(ctx, q, s.keys, id)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	sealed, err := s.blobs.Get(row.BlobSha256)
	if err != nil {
		return nil, err
	}
	pdf, err := openWith(key, sealed, fieldAAD(id, "pdf"))
	if err != nil {
		return nil, err
	}
	if sum := sha256.Sum256(pdf); !bytes.Equal(sum[:], row.Sha256) {
		return nil, fmt.Errorf("documents: %s does not match its checksum", id)
	}
	return pdf, nil
}

// decode turns a row into a Document, decrypting the filename of a live document.
func (s *Store) decode(ctx context.Context, q *dbq.Queries, r dbq.Document) (Document, error) {
	d := Document{ID: r.ID, Status: r.Status, SHA256: r.Sha256, SizeBytes: r.SizeBytes, Pages: int(r.PageCount),
		UploadedAt: r.UploadedAt, RetentionUntil: r.RetentionUntil, DeletedAt: r.DeletedAt}
	if r.FilenameCiphertext == nil {
		return d, nil
	}
	key, err := dataKey(ctx, q, s.keys, r.ID)
	if err != nil {
		return Document{}, err
	}
	defer clear(key)
	name, err := openWith(key, r.FilenameCiphertext, fieldAAD(r.ID, "filename"))
	if err != nil {
		return Document{}, err
	}
	d.Filename = string(name)
	return d, nil
}

// shred destroys the document key first (crypto-shred), then tombstones the row, drops the
// extraction runs and releases the original and the raw responses to the blob sweep.
func shred(ctx context.Context, q *dbq.Queries, id uuid.UUID, original []byte) error {
	if _, err := q.DeleteDocumentKey(ctx, id); err != nil {
		return err
	}
	if err := q.TombstoneDocument(ctx, id); err != nil {
		return err
	}
	if err := blob.Release(ctx, q, original); err != nil {
		return err
	}
	responses, err := q.DeleteExtractionRuns(ctx, id)
	if err != nil {
		return err
	}
	for _, sum := range responses {
		if sum == nil {
			continue
		}
		if err := blob.Release(ctx, q, sum); err != nil {
			return err
		}
	}
	return nil
}

// DeleteResult counts what a deletion removed.
type DeleteResult struct {
	Shredded bool  // false when the original was already deleted
	Reports  int64 // lab reports deleted (derived=delete)
	Results  int64 // lab results deleted with them
}

// Delete deletes a document original: it destroys the document key first (crypto-shred),
// then tombstones the row, drops the extraction runs and releases the blobs, which the
// blob sweep removes later. derived=delete also deletes the lab results confirmed from it.
// Deleting an already deleted document only applies derived=delete. The audit event holds
// counts, never content.
func (s *Store) Delete(ctx context.Context, user, id uuid.UUID, derived Derived, actor string) (DeleteResult, error) {
	return s.delete(ctx, user, id, derived, actor, "owner")
}

func (s *Store) delete(ctx context.Context, user, id uuid.UUID, derived Derived, actor, reason string) (DeleteResult, error) {
	if derived != KeepDerived && derived != DeleteDerived {
		return DeleteResult{}, fmt.Errorf("documents: derived must be keep or delete, not %q", derived)
	}
	var res DeleteResult
	err := s.db.Tx(ctx, func(q *dbq.Queries) error {
		res = DeleteResult{}
		row, err := q.LockDocument(ctx, dbq.LockDocumentParams{UserID: user, ID: id})
		if err != nil {
			return err
		}
		if row.Status != StatusDeleted {
			if err := shred(ctx, q, id, row.BlobSha256); err != nil {
				return err
			}
			res.Shredded = true
		}
		if derived == DeleteDerived {
			if res.Results, err = q.CountDocumentResults(ctx, id); err != nil {
				return err
			}
			if res.Reports, err = q.DeleteLabReports(ctx, id); err != nil {
				return err
			}
		}
		if !res.Shredded && res.Reports == 0 {
			return nil
		}
		return audit.Record(ctx, q, audit.Event{UserID: &user, Actor: actor, Action: "document.delete",
			TargetType: "document", TargetID: id.String(), Detail: map[string]any{
				"derived": string(derived), "reason": reason, "shredded": res.Shredded,
				"reports_deleted": res.Reports, "results_deleted": res.Results,
			}})
	})
	return res, err
}
