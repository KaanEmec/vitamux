//go:build integration

package normalize

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

const sentinel = "2000-01-01" // a deliberately wrong local date: rows that must not be touched keep it

type env struct {
	t    *testing.T
	d    *db.DB
	user uuid.UUID
	conn uuid.UUID
	// Fixtures are written as the owner role (catalogues are read-only for the app). The closures
	// keep pgx types out of this package: only internal/db may import pgx.
	run  func(sql string, args ...any) error
	scan func(dest any, sql string, args ...any) error
	col  func(sql string) ([]time.Time, error)
}

func newEnv(t *testing.T) *env {
	t.Helper()
	u, app := dbtest.Migrated(t)
	owner := dbtest.Pool(t, u, db.OwnerRole)
	e := &env{t: t, d: db.New(app), user: uuid.New(), conn: uuid.New()}
	e.run = func(sql string, args ...any) error {
		_, err := owner.Exec(context.Background(), sql, args...)
		return err
	}
	e.scan = func(dest any, sql string, args ...any) error {
		return owner.QueryRow(context.Background(), sql, args...).Scan(dest)
	}
	e.col = func(sql string) ([]time.Time, error) {
		rows, err := owner.Query(context.Background(), sql)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []time.Time
		for rows.Next() {
			var d time.Time
			if err := rows.Scan(&d); err != nil {
				return nil, err
			}
			out = append(out, d)
		}
		return out, rows.Err()
	}
	e.exec(`INSERT INTO users (id, username, password_hash) VALUES ($1, 'owner', 'synthetic')`, e.user)
	e.exec(`INSERT INTO connections (id, user_id, provider_id, account_key, mode, status)
		VALUES ($1, $2, (SELECT id FROM providers WHERE code = 'withings'), sha256('acct'), 'in_process', 'active')`, e.conn, e.user)
	e.exec(`INSERT INTO normalizer_versions (name, version, git_sha) VALUES ('test', 1, 'dev')`)
	return e
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if err := e.run(sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

const common = `provider_id, connection_id, dedupe_key, normalizer_version_id`
const commonVals = `(SELECT id FROM providers WHERE code = 'withings'), $2, substring(sha256(convert_to(gen_random_uuid()::text, 'UTF8')) for 16), 1`

// measurement inserts a heart-rate sample.
func (e *env) measurement(at, date string, offset *int16, superseded bool) int64 {
	e.t.Helper()
	var id int64
	err := e.scan(&id, fmt.Sprintf(`INSERT INTO measurements
		(user_id, metric_id, kind, start_at, local_date, value, tz_offset_min, superseded_at, %s)
		VALUES ($1, (SELECT id FROM metric_catalog WHERE code = 'heart_rate'), 'sample', $3, $4, 60, $5,
		        CASE WHEN $6 THEN now() END, %s) RETURNING id`, common, commonVals),
		e.user, e.conn, utc(at), day(date), offset, superseded)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) group(at, date string) {
	e.exec(fmt.Sprintf(`INSERT INTO measurement_groups (user_id, kind, measured_at, local_date, %s)
		VALUES ($1, 'bp_reading', $3, $4, %s)`, common, commonVals), e.user, e.conn, utc(at), day(date))
}

func (e *env) workout(at, date string) {
	e.exec(fmt.Sprintf(`INSERT INTO workouts (id, user_id, start_at, end_at, local_date, sport, %s)
		VALUES (gen_random_uuid(), $1, $3, $3::timestamptz + interval '1 hour', $4, 'run', %s)`, common, commonVals), e.user, e.conn, utc(at), day(date))
}

// sleep inserts a session ending at end; its sleep_date is the date of waking.
func (e *env) sleep(end, date string) {
	e.exec(fmt.Sprintf(`INSERT INTO sleep_sessions (id, user_id, start_at, end_at, sleep_date, has_stages, totals_basis, %s)
		VALUES (gen_random_uuid(), $1, $3::timestamptz - interval '8 hours', $3, $4, false, 'provider', %s)`, common, commonVals), e.user, e.conn, utc(end), day(date))
}

func (e *env) date(table, col, idCol string, id any) string {
	e.t.Helper()
	var d time.Time
	if err := e.scan(&d, fmt.Sprintf(`SELECT %s FROM %s WHERE %s = $1`, col, table, idCol), id); err != nil {
		e.t.Fatal(err)
	}
	return d.Format(time.DateOnly)
}

func (e *env) dates(table, col, order string) []string {
	e.t.Helper()
	ds, err := e.col(fmt.Sprintf(`SELECT %s FROM %s ORDER BY %s`, col, table, order))
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, d := range ds {
		out = append(out, d.Format(time.DateOnly))
	}
	return out
}

func (e *env) dirty() []string {
	e.t.Helper()
	return e.dates("resolution_dirty", "local_date", "local_date")
}

func TestPeriodEditRecomputesOnlyAffectedRows(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	periods := NewPeriods(e.d)
	if _, r, err := periods.Add(ctx, e.user, "owner", utc("2026-01-01T00:00:00Z"), "Europe/Amsterdam"); err != nil || r == nil || !r.From.IsZero() || !r.To.IsZero() {
		t.Fatalf("first period: %+v %v", r, err)
	}

	// Rows are written as the Amsterdam period would date them (the first period covers all time).
	inRange := e.measurement("2026-03-01T20:00:00Z", "2026-03-01", nil, false)    // 21:00 NL, 05:00 next day in Tokyo
	sameDate := e.measurement("2026-03-01T10:00:00Z", "2026-03-01", nil, false)   // 11:00 NL, 19:00 Tokyo: same date
	withOffset := e.measurement("2026-03-01T20:00:00Z", sentinel, off(60), false) // own offset: never recomputed
	superseded := e.measurement("2026-03-01T20:00:00Z", sentinel, nil, true)      // history stays as written
	before := e.measurement("2026-02-28T20:00:00Z", sentinel, nil, false)         // outside the range
	after := e.measurement("2026-06-01T20:00:00Z", sentinel, nil, false)          // outside the range, at its exclusive end
	e.group("2026-03-02T20:00:00Z", "2026-03-02")
	e.workout("2026-03-02T21:00:00Z", "2026-03-02")
	e.sleep("2026-03-02T21:30:00Z", "2026-03-02")

	// Tokyo for March..May.
	tokyo, r, err := periods.Add(ctx, e.user, "owner", utc("2026-03-01T00:00:00Z"), "Asia/Tokyo")
	if err != nil || r == nil {
		t.Fatalf("add tokyo: %+v %v", r, err)
	}
	if _, _, err := periods.Add(ctx, e.user, "owner", utc("2026-06-01T00:00:00Z"), "Europe/Amsterdam"); err != nil {
		t.Fatal(err)
	}
	// The edit that matters: Tokyo until 2026-06-01 only. Range must be exactly [03-01, 06-01).
	got, err := periods.Edit(ctx, e.user, "owner", tokyo.ID, utc("2026-03-01T00:00:00Z"), "Asia/Tokyo")
	if err != nil || got != nil {
		t.Fatalf("no-op edit should affect nothing: %+v %v", got, err)
	}
	r = &Range{From: utc("2026-03-01T00:00:00Z"), To: utc("2026-06-01T00:00:00Z")}

	res, err := RecomputeLocalDates(ctx, e.d, e.user, *r)
	if err != nil {
		t.Fatal(err)
	}
	if res != (Recomputed{Measurements: 1, Groups: 1, Workouts: 1, SleepSessions: 1}) {
		t.Fatalf("recomputed %+v", res)
	}
	for name, c := range map[string]struct {
		id   int64
		want string
	}{
		"in range moves a day": {inRange, "2026-03-02"}, "same date": {sameDate, "2026-03-01"},
		"record offset": {withOffset, sentinel}, "superseded": {superseded, sentinel},
		"before range": {before, sentinel}, "range end is exclusive": {after, sentinel},
	} {
		if got := e.date("measurements", "local_date", "id", c.id); got != c.want {
			t.Errorf("%s: local_date %s, want %s", name, got, c.want)
		}
	}
	if got := e.dates("measurement_groups", "local_date", "id"); got[0] != "2026-03-03" {
		t.Errorf("group date %v", got)
	}
	if got := e.dates("workouts", "local_date", "id"); got[0] != "2026-03-03" {
		t.Errorf("workout date %v", got)
	}
	if got := e.dates("sleep_sessions", "sleep_date", "id"); got[0] != "2026-03-03" {
		t.Errorf("sleep date %v", got)
	}
	// The row left 03-01 and joined 03-02: both days need resolving again.
	if d := e.dirty(); len(d) != 2 || d[0] != "2026-03-01" || d[1] != "2026-03-02" {
		t.Errorf("resolution_dirty = %v", d)
	}

	// A second run finds nothing to change.
	if res, err := RecomputeLocalDates(ctx, e.d, e.user, *r); err != nil || res != (Recomputed{}) {
		t.Errorf("repeat: %+v %v", res, err)
	}
}

func TestRecomputePages(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	if _, _, err := NewPeriods(e.d).Add(ctx, e.user, "owner", utc("2026-01-01T00:00:00Z"), "UTC"); err != nil {
		t.Fatal(err)
	}
	for i := range 7 { // same instant on purpose: the keyset must break ties by id
		e.measurement("2026-03-01T20:00:00Z", "2026-03-01", nil, false)
		e.measurement(fmt.Sprintf("2026-03-0%dT20:00:00Z", i+2), "2026-03-01", nil, false)
	}
	recomputeBatch = 3
	t.Cleanup(func() { recomputeBatch = 2000 })

	if _, err := NewPeriods(e.d).Edit(ctx, e.user, "owner", mustOnlyPeriod(t, e), utc("2026-01-01T00:00:00Z"), "Pacific/Auckland"); err != nil {
		t.Fatal(err)
	}
	res, err := RecomputeLocalDates(ctx, e.d, e.user, Range{})
	if err != nil {
		t.Fatal(err)
	}
	// 20:00 UTC is 09:00 the next day in Auckland (+13 in March), so every row moves.
	if res.Measurements != 14 {
		t.Errorf("changed %d rows, want 14", res.Measurements)
	}
	var wrong int
	if err := e.scan(&wrong, `SELECT count(*) FROM measurements WHERE local_date <> (start_at AT TIME ZONE 'Pacific/Auckland')::date`); err != nil || wrong != 0 {
		t.Errorf("%d rows still have the wrong date (%v)", wrong, err)
	}
}

func mustOnlyPeriod(t *testing.T, e *env) uuid.UUID {
	t.Helper()
	tl, err := NewPeriods(e.d).Timeline(context.Background(), e.user)
	if err != nil || len(tl) != 1 {
		t.Fatalf("timeline %v %v", tl, err)
	}
	return tl[0].ID
}

func TestPeriodService(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := NewPeriods(e.d)

	a, _, err := p.Add(ctx, e.user, "owner", utc("2026-01-01T00:00:00Z"), "Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.Add(ctx, e.user, "owner", utc("2026-01-01T00:00:00Z"), "Asia/Tokyo"); !errors.Is(err, db.ErrConflict) {
		t.Errorf("same start: %v, want ErrConflict", err)
	}
	if _, _, err := p.Add(ctx, e.user, "owner", utc("2026-02-01T00:00:00Z"), "Mars/Olympus"); !errors.Is(err, ErrBadTimezone) {
		t.Errorf("bad zone: %v", err)
	}
	b, _, err := p.Add(ctx, e.user, "owner", utc("2026-06-01T00:00:00Z"), "Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Edit(ctx, e.user, "owner", b.ID, utc("2026-01-01T00:00:00Z"), "Asia/Tokyo"); !errors.Is(err, db.ErrConflict) {
		t.Errorf("move onto another start: %v", err)
	}
	if _, err := p.Edit(ctx, e.user, "owner", uuid.New(), utc("2026-07-01T00:00:00Z"), "Asia/Tokyo"); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("edit unknown: %v", err)
	}
	r, err := p.Edit(ctx, e.user, "owner", b.ID, utc("2026-07-01T00:00:00Z"), "Asia/Tokyo")
	if err != nil || r == nil || !r.From.Equal(utc("2026-06-01T00:00:00Z")) || !r.To.Equal(utc("2026-07-01T00:00:00Z")) {
		t.Errorf("edit range %+v %v", r, err)
	}
	tl, err := p.Timeline(ctx, e.user)
	if err != nil || len(tl) != 2 || tl[0].ID != a.ID || tl[1].ID != b.ID || !tl[1].ValidFrom.Equal(utc("2026-07-01T00:00:00Z")) {
		t.Errorf("timeline %+v %v", tl, err)
	}
	if r, err := p.Remove(ctx, e.user, "owner", b.ID); err != nil || r == nil || !r.From.Equal(utc("2026-07-01T00:00:00Z")) || !r.To.IsZero() {
		t.Errorf("remove range %+v %v", r, err)
	}
	if _, err := p.Remove(ctx, e.user, "owner", b.ID); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("remove twice: %v", err)
	}
	// Removing the last period leaves nothing to resolve with.
	if _, err := p.Remove(ctx, e.user, "owner", a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := RecomputeLocalDates(ctx, e.d, e.user, Range{}); !errors.Is(err, ErrNoTimezone) {
		t.Errorf("recompute without periods: %v", err)
	}

	var n int
	if err := e.scan(&n, `SELECT count(*) FROM audit_events WHERE action LIKE 'timezone_period.%'`); err != nil || n != 5 {
		t.Errorf("audit events %d, want 5 (%v)", n, err)
	}
}

// TestPeriodEditEnqueuesRecomputeJob: an edit with an affected range enqueues the job in its own
// transaction, a no-op edit enqueues nothing, and the registered handler applies the new dates.
func TestPeriodEditEnqueuesRecomputeJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := newEnv(t)
	periods := NewPeriods(e.d)
	count := func() (n int) {
		if err := e.scan(&n, `SELECT count(*) FROM jobs WHERE kind = 'recompute_local_dates'`); err != nil {
			t.Fatal(err)
		}
		return n
	}
	p, _, err := periods.Add(ctx, e.user, "owner", utc("2026-01-01T00:00:00Z"), "Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	row := e.measurement("2026-03-01T20:00:00Z", "2026-03-01", nil, false) // 21:00 in Amsterdam
	if n := count(); n != 1 {
		t.Fatalf("%d jobs after the first period, want 1", n)
	}
	if r, err := periods.Edit(ctx, e.user, "owner", p.ID, utc("2026-01-01T00:00:00Z"), "Europe/Amsterdam"); err != nil || r != nil || count() != 1 {
		t.Fatalf("no-op edit: range %v, err %v, %d jobs", r, err, count())
	}
	if _, err := periods.Edit(ctx, e.user, "owner", p.ID, utc("2026-01-01T00:00:00Z"), "Asia/Tokyo"); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 2 {
		t.Fatalf("%d jobs after the zone edit, want 2", n)
	}

	r := jobs.NewRunner(e.d, jobs.Config{Poll: 50 * time.Millisecond})
	r.Register(KindRecomputeLocalDates, RecomputeJob(e.d, slog.New(slog.DiscardHandler)))
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	for range 100 {
		var open int
		if err := e.scan(&open, `SELECT count(*) FROM jobs WHERE status NOT IN ('succeeded', 'dead')`); err != nil {
			t.Fatal(err)
		}
		if open == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	var dead int
	if err := e.scan(&dead, `SELECT count(*) FROM jobs WHERE status = 'dead'`); err != nil || dead != 0 {
		t.Fatalf("dead jobs %d (%v)", dead, err)
	}
	if got := e.date("measurements", "local_date", "id", row); got != "2026-03-02" { // 05:00 in Tokyo
		t.Fatalf("local_date %s, want 2026-03-02", got)
	}
}
