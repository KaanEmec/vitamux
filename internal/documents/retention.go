package documents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

// Owner settings (settings table) of the retention policy (lab-documents.md#privacy-controls).
const (
	SettingRetentionDays           = "documents.retention_days"                     // number of days, or null to keep
	SettingDeleteAfterConfirmation = "documents.delete_original_after_confirmation" // boolean
)

// KindRetention is the daily job that deletes originals past their retention.
const KindRetention = "document_retention"

// maxRetentionDays bounds documents.retention_days (100 years).
const maxRetentionDays = 36500

// Policy is the owner's retention policy for originals. Derived lab results are always kept.
type Policy struct {
	RetentionDays                   *int // nil keeps originals (the default)
	DeleteOriginalAfterConfirmation bool
}

// GetPolicy reads the user's policy; missing settings mean the defaults.
func GetPolicy(ctx context.Context, q *dbq.Queries, user uuid.UUID) (Policy, error) {
	var p Policy
	for key, dst := range map[string]any{SettingRetentionDays: &p.RetentionDays, SettingDeleteAfterConfirmation: &p.DeleteOriginalAfterConfirmation} {
		v, err := q.GetUserSetting(ctx, dbq.GetUserSettingParams{UserID: user, Key: key})
		if errors.Is(db.MapErr(err), db.ErrNotFound) {
			continue
		}
		if err != nil {
			return Policy{}, err
		}
		if err := json.Unmarshal(v, dst); err != nil {
			return Policy{}, fmt.Errorf("documents: setting %s: %w", key, err)
		}
	}
	return p, nil
}

// SetPolicy stores the user's policy and applies the retention period to every live
// document (retention_until = uploaded_at + days), in one audited transaction.
func SetPolicy(ctx context.Context, d *db.DB, user uuid.UUID, actor string, p Policy) error {
	if p.RetentionDays != nil && (*p.RetentionDays < 1 || *p.RetentionDays > maxRetentionDays) {
		return fmt.Errorf("documents: retention_days must be between 1 and %d", maxRetentionDays)
	}
	days, err := json.Marshal(p.RetentionDays)
	if err != nil {
		return err
	}
	after, err := json.Marshal(p.DeleteOriginalAfterConfirmation)
	if err != nil {
		return err
	}
	return d.Tx(ctx, func(q *dbq.Queries) error {
		before, err := GetPolicy(ctx, q, user)
		if err != nil {
			return err
		}
		for key, v := range map[string][]byte{SettingRetentionDays: days, SettingDeleteAfterConfirmation: after} {
			if err := q.PutUserSetting(ctx, dbq.PutUserSettingParams{UserID: user, Key: key, Value: v}); err != nil {
				return err
			}
		}
		var d32 *int32
		if p.RetentionDays != nil {
			v := int32(*p.RetentionDays) //nolint:gosec // bounded above
			d32 = &v
		}
		if err := q.SetDocumentRetention(ctx, dbq.SetDocumentRetentionParams{Days: d32, UserID: user}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &user, Actor: actor, Action: "documents.retention_policy",
			Detail: audit.Diff(policyMap(before), policyMap(p))})
	})
}

func policyMap(p Policy) map[string]any {
	var days any
	if p.RetentionDays != nil {
		days = *p.RetentionDays
	}
	return map[string]any{"retention_days": days, "delete_original_after_confirmation": p.DeleteOriginalAfterConfirmation}
}

// retentionBatch is the number of documents the retention job deletes per query.
const retentionBatch = 100

// RetentionJob returns the KindRetention handler: it deletes (derived=keep) every original
// past retention_until, and confirmed originals whose owner chose deletion after confirmation.
func (s *Store) RetentionJob(log *slog.Logger) jobs.Handler {
	return func(ctx context.Context, _ jobs.Job) error {
		n := 0
		for {
			due, err := s.db.Q().ListDocumentsDue(ctx, dbq.ListDocumentsDueParams{
				AfterConfirmationKey: SettingDeleteAfterConfirmation, Now: s.now(), Lim: retentionBatch})
			if err != nil {
				return err
			}
			for _, d := range due {
				if _, err := s.delete(ctx, d.UserID, d.ID, KeepDerived, audit.System, "retention"); err != nil {
					return err
				}
				n++
			}
			if len(due) < retentionBatch {
				break
			}
		}
		log.Info("document retention", "deleted", n)
		return nil
	}
}

// Register adds the document jobs to runner and the daily schedule. Without a blob store
// (no master key) it does nothing: there are no documents to keep or delete.
func Register(runner *jobs.Runner, sch *jobs.Scheduler, d *db.DB, blobs *blob.Store, keys *crypto.Keyring, log *slog.Logger) {
	if blobs == nil || keys == nil {
		return
	}
	runner.Register(KindRetention, New(d, blobs, keys).RetentionJob(log))
	sch.Daily(KindRetention)
}
