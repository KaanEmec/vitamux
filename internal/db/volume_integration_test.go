//go:build integration

package db_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/db/fixtureload"
)

// Volume and performance baseline (J04.4). TestVolumeSmoke runs in CI on a 30-day synthetic
// slice with generous thresholds. TestVolumeBaseline loads the full year (>= 6 M heart rate rows)
// and prints the numbers for docs/benchmarks/baseline.md:
//
//	VITAMUX_VOLUME_BASELINE=1 go test -tags integration -run TestVolumeBaseline -v -timeout 30m ./internal/db/
//
// VITAMUX_VOLUME_DATASET=<dir> reuses an existing fixturegen output (e.g. fixtures/generated).

const (
	hrOneDay = `SELECT start_at, value, provider_id, device_id FROM measurements
		WHERE user_id = $1 AND metric_id = $2 AND start_at >= $3 AND start_at < $4 AND superseded_at IS NULL AND deleted_at IS NULL
		ORDER BY start_at`
	hrDaily = `SELECT local_date, provider_id, device_id, count(*), avg(value), min(value), max(value) FROM measurements
		WHERE user_id = $1 AND metric_id = $2 AND start_at >= $3 AND start_at < $4 AND superseded_at IS NULL AND deleted_at IS NULL
		GROUP BY local_date, provider_id, device_id`
	stepsDaily = `SELECT local_date, provider_id, value FROM measurements
		WHERE user_id = $1 AND metric_id = $2 AND kind = 'daily_value' AND local_date >= $3::date AND local_date < $4::date
		AND superseded_at IS NULL AND deleted_at IS NULL`
	hrByLocalDate = `SELECT start_at, value FROM measurements
		WHERE user_id = $1 AND metric_id = $2 AND local_date = $3::date AND superseded_at IS NULL AND deleted_at IS NULL`
)

type dataset struct {
	dir        string
	start      time.Time // first local date, UTC midnight
	days       int
	loc        *time.Location
	owner, app *pgxpool.Pool
	stats      fixtureload.Stats
	loadTime   time.Duration
	hrID       int16
	stepsID    int16
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// generate runs tools/fixturegen into a temp dir.
func generate(t *testing.T, args ...string) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.CommandContext(t.Context(), "go", append([]string{"run", "./tools/fixturegen", "-out", dir}, args...)...) //nolint:gosec // fixed tool path
	cmd.Dir = repoRoot()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixturegen: %v\n%s", err, out)
	}
	return dir
}

func loadDataset(t *testing.T, dir string) *dataset {
	t.Helper()
	url, app := dbtest.Migrated(t)
	ds := &dataset{dir: dir, app: app, owner: dbtest.Pool(t, url, db.OwnerRole)}
	var manifest struct {
		Start string
		Days  int
	}
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json")) //nolint:gosec // dataset dir
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatal(err)
	}
	ds.start, _ = time.Parse("2006-01-02", manifest.Start)
	ds.days = manifest.Days
	ds.loc, _ = time.LoadLocation("Europe/Berlin")

	began := time.Now()
	ds.stats, err = fixtureload.Load(t.Context(), app, dir)
	if err != nil {
		t.Fatal(err)
	}
	ds.loadTime = time.Since(began)
	for code, id := range map[string]*int16{"heart_rate": &ds.hrID, "steps": &ds.stepsID} {
		if err := app.QueryRow(t.Context(), `SELECT id FROM metric_catalog WHERE code = $1`, code).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	// Autovacuum would do this on a real instance; do it now so plans and the visibility map are settled.
	if _, err := ds.owner.Exec(t.Context(), `VACUUM (ANALYZE) measurements`); err != nil {
		t.Fatal(err)
	}
	return ds
}

// localDay returns the instants bounding the n-th local day of the dataset.
func (ds *dataset) localDay(n int) (time.Time, time.Time) {
	y, m, d := ds.start.AddDate(0, 0, n).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, ds.loc), time.Date(y, m, d+1, 0, 0, 0, 0, ds.loc)
}

type timing struct {
	Name     string
	Runs     int
	Rows     int
	P50, P95 time.Duration
}

func (t timing) String() string {
	return fmt.Sprintf("%-34s runs=%-3d rows/run=%-8d p50=%-10v p95=%v", t.Name, t.Runs, t.Rows, t.P50.Round(10*time.Microsecond), t.P95.Round(10*time.Microsecond))
}

// measure runs the query for each argument set (after one discarded warm-up) and drains every row.
func measure(t *testing.T, pool *pgxpool.Pool, name, sql string, args [][]any) timing {
	t.Helper()
	run := func(a []any) (n int, d time.Duration) {
		began := time.Now()
		rows, err := pool.Query(t.Context(), sql, a...)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			n++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return n, time.Since(began)
	}
	run(args[0])
	var ds []time.Duration
	var rowsTotal int
	for _, a := range args {
		n, d := run(a)
		ds = append(ds, d)
		rowsTotal += n
	}
	slices.Sort(ds)
	return timing{name, len(ds), rowsTotal / len(ds), ds[len(ds)/2], ds[min(len(ds)-1, len(ds)*95/100)]}
}

// benchmarks times the three read patterns of the resolution engine.
func (ds *dataset) benchmarks(t *testing.T, oneDayRuns, aggRuns int) (oneDay, agg, daily timing) {
	t.Helper()
	var day, win, steps [][]any
	for i := range oneDayRuns {
		from, to := ds.localDay((i * 37) % ds.days)
		day = append(day, []any{ds.stats.UserID, ds.hrID, from, to})
	}
	span := min(90, ds.days)
	for i := range aggRuns {
		first := (i * 41) % (ds.days - span + 1)
		from, _ := ds.localDay(first)
		_, to := ds.localDay(first + span - 1)
		win = append(win, []any{ds.stats.UserID, ds.hrID, from, to})
		steps = append(steps, []any{ds.stats.UserID, ds.stepsID, ds.start.AddDate(0, 0, first), ds.start.AddDate(0, 0, first+span)})
	}
	oneDay = measure(t, ds.app, "HR one local day (all sources)", hrOneDay, day)
	agg = measure(t, ds.app, fmt.Sprintf("HR %d-day daily aggregate", span), hrDaily, win)
	daily = measure(t, ds.app, fmt.Sprintf("steps %d daily_value rows", span), stepsDaily, steps)
	return oneDay, agg, daily
}

func explain(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) string {
	t.Helper()
	rows, err := pool.Query(t.Context(), "EXPLAIN "+sql, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, line)
	}
	return strings.Join(plan, "\n")
}

func TestVolumeSmoke(t *testing.T) {
	ds := loadDataset(t, generate(t, "-start", "2025-03-01", "-days", "30"))
	t.Logf("loaded %d measurements (%d HR), %d groups, %d sleep sessions in %v", ds.stats.Measurements, ds.stats.HeartRate, ds.stats.Groups, ds.stats.Sleep, ds.loadTime.Round(time.Millisecond))
	if ds.stats.HeartRate < 400_000 {
		t.Fatalf("only %d heart rate rows in 30 days, want a realistic volume", ds.stats.HeartRate)
	}

	from, to := ds.localDay(10)
	if plan := explain(t, ds.app, hrOneDay, ds.stats.UserID, ds.hrID, from, to); !strings.Contains(plan, "measurements_metric_start_idx") {
		t.Errorf("one-day HR scan does not use measurements_metric_start_idx:\n%s", plan)
	}
	if plan := explain(t, ds.app, stepsDaily, ds.stats.UserID, ds.stepsID, ds.start, ds.start.AddDate(0, 0, 30)); !strings.Contains(plan, "measurements_daily_value_idx") {
		t.Errorf("daily_value query does not use measurements_daily_value_idx:\n%s", plan)
	}

	oneDay, agg, daily := ds.benchmarks(t, 15, 3)
	for _, tm := range []timing{oneDay, agg, daily} {
		t.Log(tm)
	}
	// Generous: the full-year targets live in docs/benchmarks/baseline.md; these only catch a lost index or a 10x regression.
	for _, c := range []struct {
		tm    timing
		limit time.Duration
	}{{oneDay, 500 * time.Millisecond}, {agg, 5 * time.Second}, {daily, 500 * time.Millisecond}} {
		if c.tm.P95 > c.limit {
			t.Errorf("%s: p95 %v exceeds %v", c.tm.Name, c.tm.P95, c.limit)
		}
	}
	var rows, total int64
	if err := ds.app.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM measurements), pg_total_relation_size('measurements')`).Scan(&rows, &total); err != nil {
		t.Fatal(err)
	}
	t.Logf("measurements: %d rows, %d bytes/row with indexes", rows, total/rows)
	if per := total / rows; per > 600 {
		t.Errorf("measurements take %d bytes/row with indexes, want <= 600", per)
	}
}

func TestVolumeBaseline(t *testing.T) {
	if os.Getenv("VITAMUX_VOLUME_BASELINE") == "" {
		t.Skip("set VITAMUX_VOLUME_BASELINE=1 to load the full year (about 10 minutes, 2.5 GB of disk)")
	}
	dir := os.Getenv("VITAMUX_VOLUME_DATASET")
	if dir == "" {
		dir = generate(t)
	}
	ds := loadDataset(t, dir)
	ctx := context.Background()
	t.Logf("loaded %d measurements (%d HR), %d groups, %d sleep sessions (%d stages), %d raw payloads in %v (%.0f rows/s)",
		ds.stats.Measurements, ds.stats.HeartRate, ds.stats.Groups, ds.stats.Sleep, ds.stats.Stages, ds.stats.Raw, ds.loadTime.Round(time.Second),
		float64(ds.stats.Measurements)/ds.loadTime.Seconds())

	var version, buffers string
	_ = ds.app.QueryRow(ctx, `SELECT version()`).Scan(&version)
	_ = ds.app.QueryRow(ctx, `SHOW shared_buffers`).Scan(&buffers)
	t.Logf("server: %s; shared_buffers=%s; %d CPUs; %s/%s", version, buffers, runtime.NumCPU(), runtime.GOOS, runtime.GOARCH)

	var rows, heap, toast, idx, total int64
	if err := ds.app.QueryRow(ctx, `SELECT (SELECT count(*) FROM measurements), pg_relation_size('measurements'),
		pg_table_size('measurements') - pg_relation_size('measurements'), pg_indexes_size('measurements'), pg_total_relation_size('measurements')`).
		Scan(&rows, &heap, &toast, &idx, &total); err != nil {
		t.Fatal(err)
	}
	t.Logf("measurements: rows=%d heap=%d other=%d indexes=%d total=%d | bytes/row heap=%.1f indexes=%.1f total=%.1f", rows, heap, toast, idx, total,
		float64(heap)/float64(rows), float64(idx)/float64(rows), float64(total)/float64(rows))

	ir, err := ds.app.Query(ctx, `SELECT c.relname, pg_relation_size(c.oid) FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
		WHERE i.indrelid = 'measurements'::regclass ORDER BY 2 DESC`)
	if err != nil {
		t.Fatal(err)
	}
	for ir.Next() {
		var name string
		var size int64
		if err := ir.Scan(&name, &size); err != nil {
			t.Fatal(err)
		}
		t.Logf("index %-34s %12d bytes %6.1f bytes/row", name, size, float64(size)/float64(rows))
	}
	ir.Close()
	for _, tbl := range []string{"measurement_groups", "sleep_sessions", "sleep_stages", "raw_payloads", "blobs"} {
		var n, size int64
		if err := ds.app.QueryRow(ctx, fmt.Sprintf(`SELECT (SELECT count(*) FROM %[1]s), pg_total_relation_size('%[1]s')`, tbl)).Scan(&n, &size); err != nil {
			t.Fatal(err)
		}
		t.Logf("table %-20s rows=%-8d total=%d", tbl, n, size)
	}

	from, to := ds.localDay(180)
	t.Logf("plan one-day HR:\n%s", explain(t, ds.app, hrOneDay, ds.stats.UserID, ds.hrID, from, to))
	t.Logf("plan HR by local_date (no start_at bound):\n%s", explain(t, ds.app, hrByLocalDate, ds.stats.UserID, ds.hrID, ds.start.AddDate(0, 0, 180)))

	oneDay, agg, daily := ds.benchmarks(t, 100, 12)
	for _, tm := range []timing{oneDay, agg, daily} {
		t.Log(tm)
	}
	byDate := make([][]any, 20)
	for i := range byDate {
		byDate[i] = []any{ds.stats.UserID, ds.hrID, ds.start.AddDate(0, 0, (i*17)%ds.days)}
	}
	t.Log(measure(t, ds.app, "HR by local_date only", hrByLocalDate, byDate))
}
