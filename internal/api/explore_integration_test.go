//go:build integration

package api

import (
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/db/fixtureload"
	"github.com/KaanEmec/vitamux/internal/normalize"
	"github.com/KaanEmec/vitamux/internal/resolve"
)

// healthKitGolden reads the normalized output of a synthetic HealthKit batch.
func healthKitGolden(t *testing.T, name string) normalize.Output {
	t.Helper()
	b, err := os.ReadFile("../connectors/applehealth/testdata/healthkit.samples/" + name + ".golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Output normalize.Output `json:"output"`
	}
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	return g.Output
}

// rebuild consumes every dirty mark into the hourly aggregates, as the rebuild job does.
func rebuild(t *testing.T, d *db.DB) {
	t.Helper()
	for {
		n, err := resolve.RebuildAggregates(t.Context(), d, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
}

func findItem(inv oapi.Inventory, kind oapi.InventoryItemKind, code string) *oapi.InventoryItem {
	i := slices.IndexFunc(inv.Items, func(it oapi.InventoryItem) bool { return it.Kind == kind && it.Code == code })
	if i < 0 {
		return nil
	}
	return &inv.Items[i]
}

func TestInventoryAndEvents(t *testing.T) {
	e := newSourceEnv(t)
	e.write(e.conn, sourceFixture(61, 121, "left", "deep", "running"))
	e.write(e.conn, sourceFixture(62, 124, "right", "rem", "cycling")) // corrects r1, b1, s1, w1
	e.write(e.conn, normalize.Output{Tombstones: []normalize.Key{{RecordType: "reading", ExternalID: "r2"}}})
	e.write(e.conn, healthKitGolden(t, "hr_alert_event"))
	e.write(e.conn, healthKitGolden(t, "steadiness_event"))
	e.write(e.oconn, sourceFixture(90, 140, "left", "deep", "running"))
	e.write(e.oconn, healthKitGolden(t, "hr_alert_event"))
	// A confirmed lab result whose document was deleted with derived=keep.
	doc, report := uuid.New(), uuid.New()
	e.exec(`INSERT INTO documents (id, user_id, status, size_bytes, page_count, uploaded_by, deleted_at) VALUES ($1, $2, 'deleted', 1, 1, 'owner', now())`, doc, e.user)
	e.exec(`INSERT INTO lab_reports (id, user_id, document_id, provider, schema_version, prompt_version, confirmed_by)
		VALUES ($1, $2, $3, 'fake', 'vitamux.lab.extraction/1', 'lab-extraction/v1', 'owner')`, report, e.user, doc)
	e.exec(`INSERT INTO lab_results (id, user_id, report_id, analyte_id, original_label, value_text, value_numeric, unit_text,
		canonical_value, canonical_unit, conversion_factor, conversion_offset, catalog_version, collected_date)
		VALUES ($1, $2, $3, (SELECT id FROM analytes WHERE code = 'glucose'), 'Glucose', '90', 90, 'mg/dL', 4.9959, 'mmol/L', 0.05551, 0, 1, '2026-09-01')`,
		uuid.New(), e.user, report)

	const inventory = "GET /api/v1/inventory"
	var inv oapi.Inventory
	e.get(e.user, inventory, "/api/v1/inventory", http.StatusOK, &inv)
	hr := findItem(inv, oapi.InventoryItemKindMetric, "heart_rate")
	if !inv.AggregatesPending || hr == nil || hr.Count != 0 || hr.Latest == nil || *hr.Latest.Value != 62 || hr.Metric == nil || hr.Metric.Aggregation != "intensive" {
		t.Fatalf("before the rebuild: pending %v, heart_rate %+v", inv.AggregatesPending, hr)
	}
	rebuild(t, e.d)
	e.get(e.user, inventory, "/api/v1/inventory", http.StatusOK, &inv)
	hr = findItem(inv, oapi.InventoryItemKindMetric, "heart_rate")
	if inv.AggregatesPending || hr.Count != 1 || hr.Days != 1 || hr.FirstDate.String() != "2026-06-15" ||
		!slices.Equal(hr.Providers, []string{"withings"}) || len(hr.Devices) != 1 || len(hr.Origins) != 1 || *hr.Origins[0].Key != "com.example.app" {
		t.Fatalf("heart_rate: %+v", hr)
	}
	if it := findItem(inv, oapi.InventoryItemKindGroup, "bp_reading"); it == nil || it.Count != 1 || it.Latest == nil ||
		(*it.Latest.Components)["bp_systolic"] != 124 || !slices.Contains(*it.Components, "bp_pulse") {
		t.Errorf("bp_reading: %+v", it)
	}
	if it := findItem(inv, oapi.InventoryItemKindMetric, "bp_systolic"); it == nil || *it.Latest.Value != 124 {
		t.Errorf("bp_systolic: %+v", it)
	}
	if it := findItem(inv, oapi.InventoryItemKindSleep, "sleep"); it == nil || it.Count != 1 || it.LastDate.String() != "2026-06-15" || *it.Latest.Unit != "s" {
		t.Errorf("sleep: %+v", it)
	}
	if it := findItem(inv, oapi.InventoryItemKindWorkouts, "workouts"); it == nil || it.Count != 1 || *it.Latest.Text != "cycling" {
		t.Errorf("workouts: %+v", it)
	}
	if it := findItem(inv, oapi.InventoryItemKindEvent, "walking_steadiness_alert"); it == nil || it.Count != 1 || *it.Latest.Level != "repeat_low" ||
		it.Event == nil || len(it.Event.Levels) != 4 || it.Devices[0].Type == nil || *it.Devices[0].Type != "phone" {
		t.Errorf("steadiness event: %+v", it)
	}
	if it := findItem(inv, oapi.InventoryItemKindAnalyte, "glucose"); it == nil || it.Count != 1 || *it.Latest.Value != 4.9959 ||
		*it.Latest.Unit != "mmol/L" || *it.Latest.Text != "90" || it.FirstAt != nil || it.Analyte.Name == "" {
		t.Errorf("glucose: %+v", it)
	}
	for _, it := range inv.Items {
		if it.Kind == oapi.InventoryItemKindMetric && it.Code == "heart_rate" && it.Count != 1 {
			t.Errorf("another owner's rows counted: %+v", it)
		}
	}
	var other oapi.Inventory
	e.get(e.other, inventory, "/api/v1/inventory", http.StatusOK, &other)
	if findItem(other, oapi.InventoryItemKindAnalyte, "glucose") != nil || findItem(other, oapi.InventoryItemKindEvent, "walking_steadiness_alert") != nil {
		t.Errorf("other owner sees the first owner's data: %+v", other.Items)
	}

	// Event types and events: the HealthKit batches round-trip.
	var types struct {
		EventTypes []oapi.EventType `json:"event_types"`
	}
	e.get(e.user, "GET /api/v1/event-types", "/api/v1/event-types", http.StatusOK, &types)
	if len(types.EventTypes) < 8 || types.EventTypes[0].Levels == nil {
		t.Fatalf("event types: %+v", types)
	}
	const events = "GET /api/v1/events"
	var page oapi.HealthEventPage
	e.get(e.user, events, "/api/v1/events?include=provenance", http.StatusOK, &page)
	if len(page.Events) != 2 || page.HasMore || page.Events[0].Code != "walking_steadiness_alert" || *page.Events[0].Level != "repeat_low" ||
		page.Events[1].Code != "high_heart_rate_alert" || page.Events[1].EndAt == nil || page.Events[1].LocalDate.String() != "2026-09-14" ||
		page.Events[1].Provenance.Raw == nil || !strings.Contains(string(page.Events[1].Context), "HKMetadataKeyHeartRateEventThreshold") {
		t.Fatalf("events: %+v", page.Events)
	}
	e.get(e.user, events, "/api/v1/events?code=high_heart_rate_alert", http.StatusOK, &page)
	if len(page.Events) != 1 || page.Events[0].Code != "high_heart_rate_alert" {
		t.Fatalf("code filter: %+v", page.Events)
	}
	e.get(e.user, events, "/api/v1/events?limit=1", http.StatusOK, &page)
	if len(page.Events) != 1 || !page.HasMore || page.NextCursor == nil {
		t.Fatalf("first page: %+v", page)
	}
	first := page.Events[0].ID
	e.get(e.user, events, "/api/v1/events?limit=1&cursor="+url.QueryEscape(*page.NextCursor), http.StatusOK, &page)
	if len(page.Events) != 1 || page.HasMore || page.Events[0].ID == first {
		t.Fatalf("second page: %+v", page)
	}
	e.get(e.other, events, "/api/v1/events", http.StatusOK, &page)
	if len(page.Events) != 1 {
		t.Fatalf("other owner's events: %+v", page.Events)
	}
	e.get(e.user, events, "/api/v1/events?connection=nope", http.StatusUnprocessableEntity, nil)
	e.get(e.user, events, "/api/v1/events?limit=1&cursor=tampered", http.StatusUnprocessableEntity, nil)
}

// TestInventoryScale loads a synthetic slice (30 days; the full year with VITAMUX_COVERAGE_YEAR=1)
// and times the inventory: the done-when is a p95 under 300 ms on three years.
func TestInventoryScale(t *testing.T) {
	days := 30
	if os.Getenv("VITAMUX_COVERAGE_YEAR") != "" {
		days = 365
	}
	dir := t.TempDir()
	generateYear(t, dir, "2025-03-01", days)
	u, app := dbtest.Migrated(t)
	stats, err := fixtureload.Load(t.Context(), app, dir)
	if err != nil {
		t.Fatal(err)
	}
	d := db.New(app)
	if _, err := app.Exec(t.Context(), `INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES ($1, $2, 'Europe/Berlin', '2024-01-01Z')`,
		uuid.New(), stats.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := dbtest.Pool(t, u, db.OwnerRole).Exec(t.Context(), `INSERT INTO resolution_dirty (user_id, metric_id, local_date)
		SELECT DISTINCT user_id, metric_id, local_date FROM measurements ON CONFLICT DO NOTHING; ANALYZE`); err != nil {
		t.Fatal(err)
	}
	rebuild(t, d)
	rt, err := newRouter(slog.New(slog.DiscardHandler), newUITestFS(), Options{DB: d})
	if err != nil {
		t.Fatal(err)
	}
	e := &statusEnv{t: t, h: rt.mux, d: d}
	var inv oapi.Inventory
	var took []time.Duration
	for range 5 {
		t0 := time.Now()
		e.get(stats.UserID, "/api/v1/inventory", http.StatusOK, &inv)
		took = append(took, time.Since(t0))
	}
	slices.Sort(took)
	t.Logf("inventory of %d days: %d items, fastest %v, slowest %v", days, len(inv.Items), took[0], took[len(took)-1])
	if took[len(took)/2] > 300*time.Millisecond {
		t.Fatalf("inventory median %v, limit 300ms", took[len(took)/2])
	}
	steps := findItem(inv, oapi.InventoryItemKindMetric, "steps")
	if steps == nil || steps.Days < days-1 || len(steps.Providers) < 2 || steps.Count == 0 || findItem(inv, oapi.InventoryItemKindSleep, "sleep") == nil {
		t.Fatalf("steps: %+v", steps)
	}
}

const (
	summary   = "GET /api/v1/resolved/summary"
	trend     = "GET /api/v1/resolved/trend"
	srcSeries = "GET /api/v1/sources/series"
)

// TestResolvedSummaryAndTrend checks the rollups against the daily values they summarize, over
// the end of daylight saving time in Europe/Berlin (2025-10-26).
func TestResolvedSummaryAndTrend(t *testing.T) {
	e := newResolvedEnv(t, "2025-10-24", 4)
	var d oapi.ResolvedDaily
	e.call(daily, "/api/v1/resolved/daily?start_date=2025-10-24&end_date=2025-10-27&metrics=steps,blood_pressure", "", http.StatusOK, &d)
	var steps []float64
	for _, day := range d.Days {
		if v, ok := day.Metrics["steps"].Value.(float64); ok {
			steps = append(steps, v)
		}
	}
	if len(steps) != 4 {
		t.Fatalf("daily steps: %v", steps)
	}
	sum := func(xs []float64) float64 {
		var s float64
		for _, x := range xs {
			s += x
		}
		return s
	}
	near := func(a float64, b *float64) bool { return b != nil && math.Abs(a-*b) < 1e-6 }

	// Weekly: Friday to Sunday, then Monday; monthly: one bucket.
	var tr oapi.ResolvedTrend
	e.call(trend, "/api/v1/resolved/trend?metric=steps&start_date=2025-10-24&end_date=2025-10-27", "", http.StatusOK, &tr)
	if len(tr.Buckets) != 2 || tr.Rule == nil || tr.Timezone != "Europe/Berlin" || *tr.Unit != "count" {
		t.Fatalf("weekly: %+v", tr)
	}
	b0, b1 := tr.Buckets[0], tr.Buckets[1]
	if b0.StartDate.String() != "2025-10-24" || b0.EndDate.String() != "2025-10-26" || b0.Days != 3 || b0.N != 3 || b0.Coverage != 1 ||
		!near(sum(steps[:3]), b0.Sum) || !near(sum(steps[:3])/3, b0.Mean) || !near(slices.Min(steps[:3]), b0.Min) || !near(slices.Max(steps[:3]), b0.Max) {
		t.Errorf("week 1: %+v, daily %v", b0, steps[:3])
	}
	if b1.StartDate.String() != "2025-10-27" || b1.Days != 1 || !near(steps[3], b1.Sum) {
		t.Errorf("week 2: %+v", b1)
	}
	tr = oapi.ResolvedTrend{} // decoding into a used value keeps fields the response omits
	e.call(trend, "/api/v1/resolved/trend?metric=steps&start_date=2025-10-01&end_date=2025-11-30&grain=month", "", http.StatusOK, &tr)
	if len(tr.Buckets) != 2 || tr.Buckets[0].Days != 31 || tr.Buckets[0].N != 4 || !near(sum(steps), tr.Buckets[0].Sum) || tr.Buckets[1].N != 0 || tr.Buckets[1].Mean != nil {
		t.Errorf("monthly: %+v", tr.Buckets)
	}
	// Past the 366-date cap of the daily endpoints; a family has per-code statistics.
	tr = oapi.ResolvedTrend{}
	e.call(trend, "/api/v1/resolved/trend?metric=blood_pressure&start_date=2024-01-01&end_date=2025-12-31&grain=month", "", http.StatusOK, &tr)
	if len(tr.Buckets) != 24 || tr.Unit != nil {
		t.Fatalf("two years: %d buckets", len(tr.Buckets))
	}
	oct := tr.Buckets[21]
	if oct.N == 0 || oct.Components == nil || (*oct.Components)["bp_systolic"].N == 0 || oct.Mean != nil {
		t.Errorf("blood pressure October: %+v", oct)
	}
	for _, target := range []string{
		"/api/v1/resolved/trend?metric=nope&start_date=2025-10-24&end_date=2025-10-27",
		"/api/v1/resolved/trend?metric=steps&start_date=2025-10-27&end_date=2025-10-24",
		"/api/v1/resolved/trend?metric=steps&start_date=2010-01-01&end_date=2025-10-27",
		"/api/v1/resolved/trend?metric=steps&start_date=2025-10-24&end_date=2025-10-27&grain=day",
	} {
		e.call(trend, target, "", http.StatusUnprocessableEntity, nil)
	}

	// Summary: the value of the date, 30 sparkline dates, and rollups of 7, 30 and 90 dates.
	var s oapi.ResolvedSummary
	e.call(summary, "/api/v1/resolved/summary?date=2025-10-27&metrics=steps,blood_pressure&metrics=skin_temperature", "", http.StatusOK, &s)
	st := s.Metrics["steps"]
	if s.Timezone != "Europe/Berlin" || len(s.Metrics) != 3 || st.Value.Status == "no_data" || len(st.Sparkline) != 30 || len(st.Stats) != 3 {
		t.Fatalf("summary steps: %+v", st)
	}
	if v, _ := st.Value.Value.(float64); v != steps[3] || st.Sparkline[29].LocalDate.String() != "2025-10-27" || st.Sparkline[0].Status != "no_data" {
		t.Errorf("steps value %v, sparkline end %+v", st.Value.Value, st.Sparkline[29])
	}
	if w := st.Stats[0]; w.Days != 7 || w.N != 4 || w.StartDate.String() != "2025-10-21" || !near(sum(steps)/4, w.Mean) || !near(sum(steps), w.Sum) {
		t.Errorf("7 days: %+v", w)
	}
	if q := st.Stats[2]; q.Days != 90 || q.N != 4 || *round3(4.0 / 90) != q.Coverage {
		t.Errorf("90 days: %+v", q)
	}
	if bp := s.Metrics["blood_pressure"]; bp.Unit != nil || bp.Stats[0].Components == nil {
		t.Errorf("blood pressure: %+v", bp)
	}
	if sk := s.Metrics["skin_temperature"]; sk.Rule != nil || sk.Value.Status != "no_data" || sk.Stats[1].N != 0 {
		t.Errorf("metric without a rule: %+v", sk)
	}
	for _, target := range []string{
		"/api/v1/resolved/summary?metrics=nope",
		"/api/v1/resolved/summary?metrics=steps,heart_rate,spo2,weight,vo2max,sleep,hrv_sdnn,bp_systolic,bp_diastolic,height,active_energy," +
			"basal_energy,distance_walk_run,floors_climbed,respiratory_rate,resting_heart_rate,body_fat_ratio,fat_mass,bmi,sleep_total,sleep_deep",
	} {
		e.call(summary, target, "", http.StatusUnprocessableEntity, nil)
	}
}

func TestSourceSeries(t *testing.T) {
	e := newResolvedEnv(t, "2025-09-13", 3)
	e.exec(`INSERT INTO resolution_dirty (user_id, metric_id, local_date)
		SELECT DISTINCT user_id, metric_id, local_date FROM measurements ON CONFLICT DO NOTHING`)
	dayRange := "&start=" + url.QueryEscape("2025-09-13T00:00:00+02:00") + "&end=" + url.QueryEscape("2025-09-16T00:00:00+02:00")
	var s oapi.SourceSeries
	e.call(srcSeries, "/api/v1/sources/series?metric=steps"+dayRange, "", http.StatusOK, &s)
	if !s.Behind {
		t.Fatalf("pending marks not reported: %+v", s)
	}
	rebuild(t, e.d)

	s = oapi.SourceSeries{}
	e.call(srcSeries, "/api/v1/sources/series?metric=steps"+dayRange, "", http.StatusOK, &s)
	if s.Behind || s.Grain != "day" || s.Aggregation != "additive" || s.Unit != "count" || s.Rule == nil || len(s.Sources) < 2 || s.Timezone != "Europe/Berlin" {
		t.Fatalf("steps by day: %+v", s)
	}
	var daily, grouped bool
	for _, src := range s.Sources {
		if len(src.Points) == 0 || len(src.Points) > 3 {
			t.Errorf("%s: %d points", src.Provider, len(src.Points))
		}
		for _, p := range src.Points {
			daily = daily || p.DailyValue != nil
			if p.N > 0 && (p.Sum == nil || p.Mean != nil) {
				t.Errorf("%s %s: additive point %+v", src.Provider, p.LocalDate, p)
			}
		}
		grouped = grouped || src.RuleStatus == "used" && src.Group != nil
	}
	if !daily || !grouped {
		t.Errorf("daily values %v, grouped source %v", daily, grouped)
	}

	// Hours of one local day add up to the day.
	hourRange := "&start=" + url.QueryEscape("2025-09-14T00:00:00+02:00") + "&end=" + url.QueryEscape("2025-09-15T00:00:00+02:00")
	var h oapi.SourceSeries
	e.call(srcSeries, "/api/v1/sources/series?grain=hour&metric=steps"+hourRange, "", http.StatusOK, &h)
	for _, src := range h.Sources {
		var total float64
		for _, p := range src.Points {
			if p.Start == nil || p.LocalDate.String() != "2025-09-14" {
				t.Fatalf("hour point %+v", p)
			}
			total += *p.Sum
		}
		i := slices.IndexFunc(s.Sources, func(x oapi.SourceSeriesSource) bool { return sourceKey(t, x) == sourceKey(t, src) })
		j := -1
		if i >= 0 {
			j = slices.IndexFunc(s.Sources[i].Points, func(p oapi.SourcePoint) bool { return p.LocalDate.String() == "2025-09-14" })
		}
		if j < 0 {
			t.Fatalf("%s: no day point for its hours", src.Provider)
		}
		if day := s.Sources[i].Points[j]; math.Abs(total-*day.Sum) > 1e-3 {
			t.Errorf("%s: hours %v, day %v", src.Provider, total, *day.Sum)
		}
	}
	h = oapi.SourceSeries{}
	e.call(srcSeries, "/api/v1/sources/series?grain=hour&metric=heart_rate"+hourRange, "", http.StatusOK, &h)
	i := slices.IndexFunc(h.Sources, func(x oapi.SourceSeriesSource) bool { return len(x.Points) == 24 })
	if h.Aggregation != "intensive" || i < 0 {
		t.Fatalf("heart rate by hour: %+v", h)
	}
	if p := h.Sources[i].Points[0]; p.Mean == nil || *p.Min > *p.Mean || *p.Max < *p.Mean || p.Sum != nil {
		t.Errorf("intensive point: %+v", p)
	}
	for _, target := range []string{
		"/api/v1/sources/series?metric=sleep_total" + dayRange,
		"/api/v1/sources/series?metric=nope" + dayRange,
		"/api/v1/sources/series?metric=steps&grain=hour&start=2025-01-01T00:00:00Z&end=2025-09-01T00:00:00Z",
		"/api/v1/sources/series?metric=steps&start=2025-09-15T00:00:00Z&end=2025-09-14T00:00:00Z",
	} {
		e.call(srcSeries, target, "", http.StatusUnprocessableEntity, nil)
	}
}

// sourceKey identifies a series source by its connection, device and origin.
func sourceKey(t *testing.T, s oapi.SourceSeriesSource) string {
	b, err := json.Marshal([]any{s.ConnectionID, s.Device, s.Origin})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDashboardLayout(t *testing.T) {
	e := newCfgEnv(t)
	const get, put = "GET /api/v1/settings/dashboard", "PUT /api/v1/settings/dashboard"
	var l oapi.DashboardLayout
	e.call(e.user, get, "/api/v1/settings/dashboard", "", http.StatusOK, &l)
	if !l.IsDefault || l.Version != 1 || len(l.Cards) < 8 || l.Cards[0].Metric != "sleep" || l.Cards[0].Size != oapi.L {
		t.Fatalf("default: %+v", l)
	}
	for _, c := range l.Cards {
		if !resolvable(c.Metric) {
			t.Errorf("default card %s is not a catalogue code or family", c.Metric)
		}
	}
	body := `{"version": 1, "cards": [{"metric": "steps", "size": "L", "hidden": false}, {"metric": "blood_pressure", "size": "S", "hidden": true}]}`
	e.audited(1, func() { e.call(e.user, put, "/api/v1/settings/dashboard", body, http.StatusOK, &l) })
	if l.IsDefault || len(l.Cards) != 2 || l.Cards[1].Metric != "blood_pressure" || !l.Cards[1].Hidden {
		t.Fatalf("stored: %+v", l)
	}
	e.call(e.user, get, "/api/v1/settings/dashboard", "", http.StatusOK, &l)
	if l.IsDefault || len(l.Cards) != 2 || l.Cards[0].Size != oapi.L {
		t.Fatalf("read back: %+v", l)
	}
	e.call(e.other, get, "/api/v1/settings/dashboard", "", http.StatusOK, &l)
	if !l.IsDefault {
		t.Fatalf("another owner's layout: %+v", l)
	}
	// A code that left the catalogue is dropped on read.
	e.exec(`UPDATE settings SET value = jsonb_set(value, '{cards,0,metric}', '"retired_code"') WHERE user_id = $1 AND key = 'dashboard.layout'`, e.user)
	e.call(e.user, get, "/api/v1/settings/dashboard", "", http.StatusOK, &l)
	if len(l.Cards) != 1 || l.Cards[0].Metric != "blood_pressure" {
		t.Fatalf("unknown metric kept: %+v", l)
	}
	for _, bad := range []string{
		`{"version": 2, "cards": []}`,
		`{"version": 1, "cards": [{"metric": "nope", "size": "M", "hidden": false}]}`,
		`{"version": 1, "cards": [{"metric": "steps", "size": "XL", "hidden": false}]}`,
		`{"version": 1, "cards": [{"metric": "steps", "size": "M", "hidden": false}, {"metric": "steps", "size": "S", "hidden": false}]}`,
	} {
		e.call(e.user, put, "/api/v1/settings/dashboard", bad, http.StatusUnprocessableEntity, nil)
	}
	// An empty layout is a stored layout, not the default.
	e.call(e.user, put, "/api/v1/settings/dashboard", `{"version": 1, "cards": []}`, http.StatusOK, &l)
	e.call(e.user, get, "/api/v1/settings/dashboard", "", http.StatusOK, &l)
	if l.IsDefault || len(l.Cards) != 0 {
		t.Fatalf("empty layout: %+v", l)
	}
}
