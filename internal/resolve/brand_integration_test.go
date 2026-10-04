//go:build integration

package resolve

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

// Brand and device selectors (J09.11) on fixturegen data, whose Apple sources carry HealthKit's
// "Apple Inc." with model Watch or iPhone and whose Garmin watch also arrives relayed.

// builtinRule is a first_available rule from the built-in groups with these ids, in this order.
func builtinRule(t *testing.T, metric string, kind catalog.Window, ids ...string) *Rule {
	t.Helper()
	all := map[string]Group{}
	for _, b := range Builtins() {
		for _, g := range b.Rule.Groups {
			all[g.ID] = g
		}
	}
	r := Rule{Schema: SchemaV1, Metric: metric, Window: RuleWindow{Kind: kind}, Strategy: Strategy{Op: OpFirstAvailable}}
	for _, id := range ids {
		g, ok := all[id]
		if !ok {
			t.Fatalf("no built-in group %s", id)
		}
		r.Groups = append(r.Groups, g)
	}
	spec, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return parseRule(t, string(spec))
}

func TestBrandSelectors(t *testing.T) {
	s := loadSlice(t, "2025-02-15", 5)
	const hour = "2025-02-16T09:00:00Z"

	// The Apple Watch group takes only Watch rows; the relayed Garmin watch stays outside.
	r := byKey(s.run(t, "heart_rate", catalog.WindowHour, "2025-02-16", "2025-02-16",
		builtinRule(t, "heart_rate", catalog.WindowHour, "apple_watch"), sliceANow))[hour]
	if r.Selected != "apple_watch" {
		t.Fatalf("apple_watch rule selected %q: %s", r.Selected, r.Explanation)
	}
	for _, src := range input(r, "apple_watch").Sources {
		if src.DeviceManufacturer != "Apple Inc." || src.DeviceModel != "Watch" {
			t.Errorf("apple_watch took %+v", src)
		}
	}
	if !slices.ContainsFunc(r.Inputs, func(in ResultInput) bool {
		return in.Status == StatusNotInRule && in.Sources[0].OriginKey == "com.garmin.connect.mobile"
	}) {
		t.Errorf("the relayed Garmin watch is not outside the rule: %+v", r.Inputs)
	}

	// A brand, spelled in any case, takes the direct and the relayed Garmin watch.
	brand := parseRule(t, `{"schema":"vitamux.rule/1","metric":"heart_rate","window":{"kind":"hour"},
		"groups":[{"id":"garmin_any","match":[{"device_manufacturer":"garmin"}]}],"strategy":{"op":"first_available"}}`)
	r = byKey(s.run(t, "heart_rate", catalog.WindowHour, "2025-02-16", "2025-02-16", brand, sliceANow))[hour]
	var providers []string
	for _, src := range input(r, "garmin_any").Sources {
		providers = append(providers, src.Provider)
	}
	slices.Sort(providers)
	if !slices.Equal(providers, []string{"apple_health", "garmin"}) {
		t.Errorf("garmin_any sources %v, want apple_health and garmin", providers)
	}

	// A manufacturer change clears the owner's cache; a change selectors never read does not.
	s.run(t, "heart_rate", "", "2025-02-15", "2025-02-19", nil, sliceANow)
	if len(s.cached(t, "heart_rate")) == 0 {
		t.Fatal("heart_rate not cached")
	}
	if err := s.owner(`UPDATE devices SET hardware_version = 'Watch0,0' WHERE user_id = $1 AND fingerprint = 'synthetic-apple-watch-01'`, s.user); err != nil {
		t.Fatal(err)
	}
	if len(s.cached(t, "heart_rate")) == 0 {
		t.Error("a hardware version change cleared the cache")
	}
	if err := s.owner(`UPDATE devices SET manufacturer = 'Apple' WHERE user_id = $1 AND fingerprint = 'synthetic-apple-watch-01'`, s.user); err != nil {
		t.Fatal(err)
	}
	if got := s.cached(t, "heart_rate"); got != nil {
		t.Errorf("a manufacturer change left heart_rate cached on %v", got)
	}
}

// Steps fall back from the Apple Watch to the iPhone on the days the watch is not worn
// (2025-08-04..10, fixtures/README.md).
func TestBrandStepsFallBackToIPhone(t *testing.T) {
	s := loadSlice(t, "2025-08-01", 5)
	days := byKey(s.run(t, "steps", catalog.WindowLocalDay, "2025-08-01", "2025-08-05",
		builtinRule(t, "steps", catalog.WindowLocalDay, "apple_watch", "iphone"), "2025-08-10T00:00:00Z"))
	for _, d := range []string{"2025-08-04", "2025-08-05"} {
		if r := days[d]; r.Selected != "iphone" || input(r, "apple_watch").Status != StatusNoData {
			t.Errorf("%s: selected %q: %s", d, r.Selected, r.Explanation)
		}
	}
	if !slices.ContainsFunc([]string{"2025-08-01", "2025-08-02", "2025-08-03"}, func(d string) bool { return days[d].Selected == "apple_watch" }) {
		t.Error("the Apple Watch never wins on the days it is worn")
	}
}

// WHOOP relayed through Apple Health fills the hours the WHOOP connector has no data for.
func TestBrandWhoopRelay(t *testing.T) {
	s := loadSlice(t, "2025-02-15", 5)
	conn, dev, origin := uuid.New(), uuid.New(), uuid.New()
	setup := []struct {
		stmt string
		args []any
	}{
		{`INSERT INTO connections (id, user_id, provider_id, account_key, mode, status)
			VALUES ($1, $2, (SELECT id FROM providers WHERE code = 'whoop'), sha256('synthetic-whoop'), 'push', 'active')`, []any{conn, s.user}},
		{`INSERT INTO devices (id, user_id, provider_id, fingerprint, device_type, manufacturer)
			VALUES ($1, $2, (SELECT id FROM providers WHERE code = 'whoop'), 'whoop:strap', 'band', 'WHOOP')`, []any{dev, s.user}},
		{`INSERT INTO data_origins (id, user_id, provider_id, origin_key, name, relayed_provider_id)
			VALUES ($1, $2, (SELECT id FROM providers WHERE code = 'apple_health'), 'com.whoop.iphone', 'WHOOP',
				(SELECT id FROM providers WHERE code = 'whoop'))`, []any{origin, s.user}},
	}
	for _, st := range setup {
		if err := s.owner(st.stmt, st.args...); err != nil {
			t.Fatal(err)
		}
	}
	// Direct WHOOP heart rate from 08:00 to 08:59; the relayed copy from 08:00 to 09:59.
	hr := func(conn uuid.UUID, device, origin *uuid.UUID, tag, from, to string, value float64) {
		t.Helper()
		if err := s.owner(`INSERT INTO measurements (user_id, metric_id, kind, start_at, local_date, value, provider_id,
			connection_id, device_id, origin_id, dedupe_key, normalizer_version_id)
			SELECT $1::uuid, (SELECT id FROM metric_catalog WHERE code = 'heart_rate'), 'sample', at, '2025-02-16', $2::float8, c.provider_id, c.id,
				$3::uuid, $4::uuid, substring(sha256(convert_to($5::text || at::text, 'UTF8')) for 16),
				(SELECT normalizer_version_id FROM measurements WHERE user_id = $1 LIMIT 1)
			FROM connections c, generate_series($6::timestamptz, $7::timestamptz, interval '1 minute') AS at
			WHERE c.id = $8::uuid`, s.user, value, device, origin, tag, from, to, conn); err != nil {
			t.Fatal(err)
		}
	}
	var apple uuid.UUID
	if err := s.scan([]any{&apple}, `SELECT c.id FROM connections c JOIN providers p ON p.id = c.provider_id
		WHERE c.user_id = $1 AND p.code = 'apple_health'`, s.user); err != nil {
		t.Fatal(err)
	}
	hr(conn, &dev, nil, "synthetic-whoop-direct", "2025-02-16T08:00:00Z", "2025-02-16T08:59:00Z", 60)
	hr(apple, nil, &origin, "synthetic-whoop-relay", "2025-02-16T08:00:00Z", "2025-02-16T09:59:00Z", 65)

	rule := builtinRule(t, "heart_rate", catalog.WindowHour, "whoop", "whoop_apple", "garmin", "garmin_apple", "apple_watch")
	hours := byKey(s.run(t, "heart_rate", catalog.WindowHour, "2025-02-16", "2025-02-16", rule, sliceANow))
	if r := hours["2025-02-16T08:00:00Z"]; r.Selected != "whoop" || input(r, "whoop_apple").Status != StatusUnused {
		t.Errorf("08:00: selected %q: %s", r.Selected, r.Explanation)
	}
	if r := hours["2025-02-16T09:00:00Z"]; r.Selected != "whoop_apple" || input(r, "whoop").Status != StatusNoData {
		t.Errorf("09:00: selected %q: %s", r.Selected, r.Explanation)
	}
}
