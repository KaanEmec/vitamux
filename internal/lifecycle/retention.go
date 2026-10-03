package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

// Owner settings (settings table) of the retention policy.
const (
	SettingRawDays            = "retention.raw_days"              // {"<provider code>": days}
	SettingSupersededDays     = "retention.superseded_after_days" // days; 0 keeps
	SettingIdempotencyKeyDays = "retention.idempotency_key_days"  // days
)

// Job kinds of the daily retention jobs (reliability.md#job-queue).
const (
	KindPruneRaw             = "prune_raw"
	KindPruneSuperseded      = "prune_superseded"
	KindPruneIdempotencyKeys = "prune_idempotency_keys"
)

const (
	// DefaultIdempotencyKeyDays keeps a stored ingest response replayable for a month.
	DefaultIdempotencyKeyDays = 30
	// MinIdempotencyKeyDays leaves clients a week to retry a request.
	MinIdempotencyKeyDays = 7
	maxDays               = 36500 // 100 years
	pruneBatch            = 500
)

// ErrInvalidRetention wraps validation failures of a Retention.
var ErrInvalidRetention = errors.New("lifecycle: invalid retention")

// Retention is the owner's retention policy. Pruning is off by default except for idempotency keys.
type Retention struct {
	RawDays            map[string]int // provider code -> days; a missing provider or 0 keeps its raw payloads
	SupersededDays     int            // 0 keeps superseded canonical rows
	IdempotencyKeyDays int
}

// GetRetention reads user's policy; missing settings mean the defaults.
func GetRetention(ctx context.Context, q *dbq.Queries, user uuid.UUID) (Retention, error) {
	r := Retention{RawDays: map[string]int{}, IdempotencyKeyDays: DefaultIdempotencyKeyDays}
	for key, dst := range map[string]any{SettingRawDays: &r.RawDays, SettingSupersededDays: &r.SupersededDays, SettingIdempotencyKeyDays: &r.IdempotencyKeyDays} {
		v, err := q.GetUserSetting(ctx, dbq.GetUserSettingParams{UserID: user, Key: key})
		if errors.Is(db.MapErr(err), db.ErrNotFound) {
			continue
		}
		if err != nil {
			return Retention{}, err
		}
		if err := json.Unmarshal(v, dst); err != nil {
			return Retention{}, fmt.Errorf("lifecycle: setting %s: %w", key, err)
		}
	}
	return r, nil
}

// SetRetention validates r, stores it for user and audits the change, inside the caller's
// transaction. Raw retention providers must exist; 0 days removes a provider's entry.
func SetRetention(ctx context.Context, q *dbq.Queries, user uuid.UUID, actor string, r Retention) error {
	raw := map[string]int{}
	for code, days := range r.RawDays {
		if days < 0 || days > maxDays {
			return fmt.Errorf("%w: raw days for %s must be between 0 and %d", ErrInvalidRetention, code, maxDays)
		}
		ok, err := q.ProviderExists(ctx, code)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: unknown provider %q", ErrInvalidRetention, code)
		}
		if days > 0 {
			raw[code] = days
		}
	}
	switch {
	case r.SupersededDays < 0 || r.SupersededDays > maxDays:
		return fmt.Errorf("%w: superseded days must be between 0 and %d", ErrInvalidRetention, maxDays)
	case r.IdempotencyKeyDays < MinIdempotencyKeyDays || r.IdempotencyKeyDays > maxDays:
		return fmt.Errorf("%w: idempotency key days must be between %d and %d", ErrInvalidRetention, MinIdempotencyKeyDays, maxDays)
	}
	r.RawDays = raw
	before, err := GetRetention(ctx, q, user)
	if err != nil {
		return err
	}
	for key, v := range map[string]any{SettingRawDays: r.RawDays, SettingSupersededDays: r.SupersededDays, SettingIdempotencyKeyDays: r.IdempotencyKeyDays} {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if err := q.PutUserSetting(ctx, dbq.PutUserSettingParams{UserID: user, Key: key, Value: b}); err != nil {
			return err
		}
	}
	diff := audit.Diff(before.fields(), r.fields())
	if len(diff) == 0 {
		return nil
	}
	return audit.Record(ctx, q, audit.Event{UserID: &user, Actor: actor, Action: "settings.update",
		TargetType: "setting", TargetID: "retention", Detail: diff})
}

func (r Retention) fields() map[string]any {
	return map[string]any{SettingRawDays: r.RawDays, SettingSupersededDays: r.SupersededDays, SettingIdempotencyKeyDays: r.IdempotencyKeyDays}
}

// RawStats counts what PruneRaw did.
type RawStats struct {
	Pruned       int64 // raw payloads deleted
	RefusedStale int64 // past retention but refused: output of an older normalizer version
}

// PruneRaw deletes, per owner and provider with raw retention, the raw payloads stored more
// than that many days before now that are safe to lose (dbq LockPrunableRaw lists the rules):
// the record's oldest version first, never one that reprocessing would read or that has active
// canonical rows, never one with output from an older normalizer version. References from
// inactive rows, import items and the next version are cleared, blob references released.
func PruneRaw(ctx context.Context, d *db.DB, now time.Time) (RawStats, error) {
	var st RawStats
	err := forEachUser(ctx, d, func(user uuid.UUID, pol Retention) error {
		var pruned int64
		for _, provider := range slices.Sorted(maps.Keys(pol.RawDays)) {
			cutoff := now.AddDate(0, 0, -pol.RawDays[provider])
			for {
				n, err := pruneRawBatch(ctx, d, user, provider, cutoff)
				if err != nil {
					return err
				}
				if n == 0 {
					break
				}
				pruned += n
			}
			refused, err := d.Q().CountRawRefusedStale(ctx, dbq.CountRawRefusedStaleParams{UserID: user, Provider: provider, Cutoff: cutoff})
			if err != nil {
				return err
			}
			st.RefusedStale += refused
		}
		st.Pruned += pruned
		return auditPrune(ctx, d, user, KindPruneRaw, map[string]any{"raw_payloads": pruned}, pruned)
	})
	return st, err
}

func pruneRawBatch(ctx context.Context, d *db.DB, user uuid.UUID, provider string, cutoff time.Time) (int64, error) {
	var n int64
	err := d.Tx(ctx, func(q *dbq.Queries) error {
		n = 0
		rows, err := q.LockPrunableRaw(ctx, dbq.LockPrunableRawParams{UserID: user, Provider: provider, Cutoff: cutoff, MaxRows: pruneBatch})
		if err != nil || len(rows) == 0 {
			return err
		}
		ids := make([]int64, len(rows))
		for i, r := range rows {
			ids[i] = r.ID
		}
		if err := q.DetachPrunedRaw(ctx, ids); err != nil {
			return err
		}
		if err := blob.LockShared(ctx, q); err != nil {
			return err
		}
		if err := q.ReleaseRawBlobs(ctx, ids); err != nil {
			return err
		}
		n, err = q.DeleteRawByIDs(ctx, ids)
		return err
	})
	return n, err
}

// PruneSuperseded deletes, per owner with SupersededDays set, canonical rows superseded more
// than that many days before now, oldest end of each chain first (see dbq
// PruneSupersededMeasurements), so active rows and the links between remaining rows stay
// intact. Stages and segments go with their session or workout. It returns counts per table.
func PruneSuperseded(ctx context.Context, d *db.DB, now time.Time) (map[string]int64, error) {
	total := map[string]int64{}
	err := forEachUser(ctx, d, func(user uuid.UUID, pol Retention) error {
		if pol.SupersededDays == 0 {
			return nil
		}
		cutoff := now.AddDate(0, 0, -pol.SupersededDays)
		counts := map[string]any{}
		var sum int64
		q := d.Q()
		// Measurements first: a group stays while a measurement names it.
		for _, t := range []struct {
			name string
			fn   func() (int64, error)
		}{
			{"measurements", func() (int64, error) {
				return q.PruneSupersededMeasurements(ctx, dbq.PruneSupersededMeasurementsParams{UserID: user, Cutoff: cutoff, MaxRows: pruneBatch})
			}},
			{"measurement_groups", func() (int64, error) {
				return q.PruneSupersededGroups(ctx, dbq.PruneSupersededGroupsParams{UserID: user, Cutoff: cutoff, MaxRows: pruneBatch})
			}},
			{"sleep_sessions", func() (int64, error) {
				return q.PruneSupersededSleep(ctx, dbq.PruneSupersededSleepParams{UserID: user, Cutoff: cutoff, MaxRows: pruneBatch})
			}},
			{"workouts", func() (int64, error) {
				return q.PruneSupersededWorkouts(ctx, dbq.PruneSupersededWorkoutsParams{UserID: user, Cutoff: cutoff, MaxRows: pruneBatch})
			}},
		} {
			var n int64
			for {
				m, err := t.fn()
				if err != nil {
					return err
				}
				if m == 0 {
					break
				}
				n += m
			}
			counts[t.name], total[t.name], sum = n, total[t.name]+n, sum+n
		}
		return auditPrune(ctx, d, user, KindPruneSuperseded, counts, sum)
	})
	return total, err
}

// PruneIdempotencyKeys deletes stored ingest responses older than each owner's
// IdempotencyKeyDays; a retry after that is a new request.
func PruneIdempotencyKeys(ctx context.Context, d *db.DB, now time.Time) (int64, error) {
	var total int64
	err := forEachUser(ctx, d, func(user uuid.UUID, pol Retention) error {
		n, err := d.Q().PruneIdempotencyKeys(ctx, dbq.PruneIdempotencyKeysParams{UserID: user, Cutoff: now.AddDate(0, 0, -pol.IdempotencyKeyDays)})
		total += n
		return err
	})
	return total, err
}

func forEachUser(ctx context.Context, d *db.DB, fn func(uuid.UUID, Retention) error) error {
	users, err := d.Q().ListLifecycleUsers(ctx)
	if err != nil {
		return err
	}
	for _, u := range users {
		pol, err := GetRetention(ctx, d.Q(), u.ID)
		if err != nil {
			return err
		}
		if err := fn(u.ID, pol); err != nil {
			return err
		}
	}
	return nil
}

// auditPrune records a retention deletion with its counts; nothing when nothing was deleted.
func auditPrune(ctx context.Context, d *db.DB, user uuid.UUID, kind string, counts map[string]any, n int64) error {
	if n == 0 {
		return nil
	}
	return d.Tx(ctx, func(q *dbq.Queries) error {
		return audit.Record(ctx, q, audit.Event{UserID: &user, Actor: audit.System, Action: "retention." + kind, Detail: counts})
	})
}

// Register adds the retention jobs to runner and the daily schedule.
func Register(runner *jobs.Runner, sch *jobs.Scheduler, d *db.DB, log *slog.Logger) {
	handlers := map[string]jobs.Handler{
		KindPruneRaw: func(ctx context.Context, _ jobs.Job) error {
			st, err := PruneRaw(ctx, d, time.Now())
			log.Info("raw retention", "pruned", st.Pruned, "refused_stale_normalizer", st.RefusedStale)
			return err
		},
		KindPruneSuperseded: func(ctx context.Context, _ jobs.Job) error {
			n, err := PruneSuperseded(ctx, d, time.Now())
			log.Info("superseded retention", "measurements", n["measurements"], "measurement_groups", n["measurement_groups"],
				"sleep_sessions", n["sleep_sessions"], "workouts", n["workouts"])
			return err
		},
		KindPruneIdempotencyKeys: func(ctx context.Context, _ jobs.Job) error {
			n, err := PruneIdempotencyKeys(ctx, d, time.Now())
			log.Info("idempotency key retention", "pruned", n)
			return err
		},
	}
	for _, kind := range slices.Sorted(maps.Keys(handlers)) {
		runner.Register(kind, handlers[kind])
		sch.Daily(kind)
	}
}
