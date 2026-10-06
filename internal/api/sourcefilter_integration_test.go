//go:build integration

package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/connectors/applehealth"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// Apple Health source filter (J22.25). Synthetic bundle ids, names and values only;
// com.whoop.iphone is the seeded relay origin of the WHOOP connector.

const (
	hkHR       = "HKQuantityTypeIdentifierHeartRate"
	hkRHR      = "HKQuantityTypeIdentifierRestingHeartRate"
	hkMass     = "HKQuantityTypeIdentifierBodyMass"
	whoopApp   = "com.whoop.iphone"
	appleWatch = "com.apple.health.00000000-0000-4000-8000-0000000000aa"
	scaleApp   = "com.example.synthetic.scale"
)

// sourceFilterEnv is a paired device and its owner.
type sourceFilterEnv struct {
	*deviceEnv
	dev, tok string
	conn     uuid.UUID
}

func newSourceFilterEnv(t *testing.T) *sourceFilterEnv {
	e := &sourceFilterEnv{deviceEnv: newDeviceEnv(t)}
	var connID string
	e.dev, connID, e.tok = e.pair(e.pairingCode(), "Synthetic iPhone")
	e.conn, _ = ingest.ParseConnectionID(connID)
	return e
}

func (e *sourceFilterEnv) filter() map[string]any {
	e.t.Helper()
	st, body := e.call("GET /api/v1/devices/{id}/source-filter", "/api/v1/devices/"+e.dev+"/source-filter", "", "")
	e.want(st, body, http.StatusOK, "")
	return body
}

func (e *sourceFilterEnv) put(body string) (int, map[string]any) {
	e.t.Helper()
	return e.call("PUT /api/v1/devices/{id}/source-filter", "/api/v1/devices/"+e.dev+"/source-filter", body, "")
}

func (e *sourceFilterEnv) self() map[string]any {
	e.t.Helper()
	st, body := e.call("GET /api/ingest/v1/devices/self", "/api/ingest/v1/devices/self", "", e.tok)
	e.want(st, body, http.StatusOK, "")
	return body
}

// connectWhoop adds a direct WHOOP connection with status and returns its id.
func (e *sourceFilterEnv) connectWhoop(status string) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	e.exec(`INSERT INTO connections (id, user_id, provider_id, mode, status)
		SELECT $1, $2, id, 'remote', $3 FROM providers WHERE code = 'whoop'`, id, e.userID, status)
	return id
}

func origin(t *testing.T, view map[string]any, bundle string) map[string]any {
	t.Helper()
	for _, o := range view["origins"].([]any) {
		if m := o.(map[string]any); m["bundle_id"] == bundle {
			return m
		}
	}
	t.Fatalf("%s not listed in %v", bundle, view["origins"])
	return nil
}

func resetTypes(self map[string]any) []string {
	var out []string
	for _, r := range self["anchor_resets"].([]any) {
		out = append(out, r.(map[string]any)["type"].(string))
	}
	slices.Sort(out)
	return out
}

// TestSourceFilterDefaultsAndChoices: the defaults follow the direct connection as it is
// connected, disabled and deleted, explicit choices never change with them, every change is
// audited, ignore → take queues an anchor reset for the types the app writes, and a stale
// version is refused.
func TestSourceFilterDefaultsAndChoices(t *testing.T) {
	e := newSourceFilterEnv(t)

	report := fmt.Sprintf(`{"sources": [
		{"bundle_id": %q, "name": "WHOOP", "types": [{"type": %q, "last_sample_at": "2026-10-04T06:00:00Z"}, {"type": %q}]},
		{"bundle_id": %q, "name": "WHOOP", "types": [{"type": %q}]},
		{"bundle_id": %q, "name": "Synthetic Watch", "types": [{"type": %q}]},
		{"bundle_id": %q, "name": "Synthetic Scale", "types": [{"type": %q}]}]}`,
		whoopApp, hkHR, hkRHR, whoopApp+".watchkitapp", hkHR, appleWatch, hkHR, scaleApp, hkMass)
	st, body := e.call("PUT /api/ingest/v1/devices/self/sources", "/api/ingest/v1/devices/self/sources", report, e.tok)
	e.want(st, body, http.StatusNoContent, "")
	st, body = e.call("PUT /api/ingest/v1/devices/self/sources", "/api/ingest/v1/devices/self/sources",
		`{"sources": [{"bundle_id": "com.example.synthetic.scale", "types": [{"type": "body mass"}]}]}`, e.tok)
	e.want(st, body, http.StatusUnprocessableEntity, CodeValidationFailed)

	// No direct connection: everything is taken; the seeded relay origin is classified relayed.
	view := e.filter()
	if view["sources_reported_at"] == nil || len(view["origins"].([]any)) != 4 {
		t.Fatalf("view %v", view)
	}
	w := origin(t, view, whoopApp)
	if w["mode"] != "take" || w["default_mode"] != "take" || w["explicit"] != false || w["default_reason"] != nil ||
		w["classification"] != "relayed" || w["relayed_provider"] != "whoop" || len(w["writes"].([]any)) != 2 {
		t.Fatalf("whoop without a direct connection: %v", w)
	}
	if a := origin(t, view, appleWatch); a["default_reason"] != "native" || a["classification"] != "native" || a["mode"] != "take" {
		t.Fatalf("apple source: %v", a)
	}

	// Connecting WHOOP directly ignores its copy by default, the Watch extension with it.
	conn := e.connectWhoop("active")
	view = e.filter()
	w = origin(t, view, whoopApp)
	if w["mode"] != "ignore" || w["default_mode"] != "ignore" || w["default_reason"] != "direct_connection" ||
		w["reason_provider"] != "whoop" || w["reason_provider_name"] != "WHOOP" || w["explicit"] != false {
		t.Fatalf("whoop with a direct connection: %v", w)
	}
	if x := origin(t, view, whoopApp+".watchkitapp"); x["mode"] != "ignore" || x["default_reason"] != "direct_connection" {
		t.Fatalf("watch extension: %v", x)
	}
	if s := origin(t, view, scaleApp); s["mode"] != "take" {
		t.Fatalf("scale: %v", s)
	}
	sf := e.self()["source_filter"].(map[string]any)
	if sf["version"] != float64(1) || len(sf["origins"].([]any)) != 0 || len(sf["default_ignore"].([]any)) != 1 ||
		sf["default_ignore"].([]any)[0].(map[string]any)["origin_pattern"] != whoopApp {
		t.Fatalf("devices/self source_filter: %v", sf)
	}

	// Ignoring the scale: audited, version bumped, no anchor reset.
	st, body = e.put(`{"version": 1, "origins": [{"bundle_id": "com.example.synthetic.scale", "name": "Synthetic Scale", "mode": "ignore"}]}`)
	e.want(st, body, http.StatusOK, "")
	if body["version"] != float64(2) || origin(t, body, scaleApp)["explicit"] != true || origin(t, body, scaleApp)["mode"] != "ignore" {
		t.Fatalf("after ignore: %v", body)
	}
	if got := resetTypes(e.self()); len(got) != 0 {
		t.Fatalf("ignoring must not reset anchors: %v", got)
	}
	st, body = e.put(`{"version": 1, "origins": []}`)
	e.want(st, body, http.StatusConflict, CodeConflict)

	// Taking WHOOP against its default: the types it writes are pulled again.
	st, body = e.put(`{"version": 2, "origins": [{"bundle_id": "com.example.synthetic.scale", "mode": "ignore"},
		{"bundle_id": "com.whoop.iphone", "mode": "take"}]}`)
	e.want(st, body, http.StatusOK, "")
	if got := resetTypes(e.self()); !slices.Equal(got, []string{hkHR, hkRHR}) {
		t.Fatalf("anchor resets %v, want the types WHOOP writes", got)
	}
	if n := e.count(`SELECT count(*) FROM audit_events WHERE action = 'device.source_filter' AND target_id = $1`, e.dev); n != 2 {
		t.Fatalf("%d audit entries, want 2", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_events WHERE action = 'device.source_filter'
		AND detail -> 'anchor_reset' @> $1::jsonb`, `["`+hkHR+`"]`); n != 1 {
		t.Fatal("the audit entry must name the reset types")
	}

	// Disabling or deleting the direct connection re-evaluates the default, never the choice.
	e.exec(`UPDATE connections SET status = 'disabled' WHERE id = $1`, conn)
	view = e.filter()
	if w := origin(t, view, whoopApp); w["default_mode"] != "take" || w["mode"] != "take" || w["explicit"] != true {
		t.Fatalf("whoop after disconnect: %v", w)
	}
	if x := origin(t, view, whoopApp+".watchkitapp"); x["mode"] != "take" {
		t.Fatalf("watch extension follows the explicit choice of its parent: %v", x)
	}
	e.exec(`UPDATE connections SET status = 'active' WHERE id = $1`, conn)
	st, body = e.put(`{"origins": []}`)
	e.want(st, body, http.StatusOK, "")
	if w := origin(t, body, whoopApp); w["mode"] != "ignore" || w["explicit"] != false {
		t.Fatalf("back to the default: %v", w)
	}
	e.exec(`DELETE FROM connections WHERE id = $1`, conn)
	if w := origin(t, e.filter(), whoopApp); w["mode"] != "take" || w["default_reason"] != nil {
		t.Fatalf("whoop after the connection is deleted: %v", w)
	}

	for _, bad := range []string{
		`{"origins": [{"bundle_id": "com.example.a", "mode": "per_type"}]}`,
		`{"origins": [{"bundle_id": "com.example.a", "mode": "take"}, {"bundle_id": "com.example.a", "mode": "ignore"}]}`,
		`{"origins": [{"bundle_id": "com.example.a", "mode": "per_type", "types": ["steps"]}]}`,
	} {
		st, body = e.put(bad)
		e.want(st, body, http.StatusUnprocessableEntity, CodeValidationFailed)
	}
	st, body = e.call("GET /api/v1/devices/{id}/source-filter", "/api/v1/devices/"+uuid.NewString()+"/source-filter", "", "")
	e.want(st, body, http.StatusNotFound, CodeNotFound)
}

// TestSourceFilterGuard: rows from an origin the device's filter ignores (an old app build, a
// race) are kept raw and marked ignored_by_filter, not normalized; Explore and the all-sources
// view list them only on request; taking the origin normalizes the held payload again.
func TestSourceFilterGuard(t *testing.T) {
	e := newSourceFilterEnv(t)
	ctx := t.Context()
	e.exec(`INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES (gen_random_uuid(), $1, 'Europe/Amsterdam', '2020-01-01Z')`, e.userID)
	e.connectWhoop("active")

	sample := func(id, bundle, at string, bpm int) string {
		return fmt.Sprintf(`{"uuid": %q, "start": %q, "end": %q, "value": %d, "unit": "count/min",
			"source_revision": {"bundle_id": %q, "name": "Synthetic"}, "metadata": {"HKTimeZone": "Europe/Amsterdam"}, "was_user_entered": false}`,
			id, at, at, bpm, bundle)
	}
	page := fmt.Sprintf(`{"type": %q, "anchor": {"before_hash": "none", "after_hash": "0011223344556677", "query_started_at": "2026-10-04T08:00:00Z"},
		"samples": [%s, %s, %s], "deleted": []}`, hkHR,
		sample("00000000-0000-4000-8000-0000000000b1", appleWatch, "2026-10-04T06:00:00Z", 58),
		sample("00000000-0000-4000-8000-0000000000b2", whoopApp, "2026-10-04T06:01:00Z", 59),
		sample("00000000-0000-4000-8000-0000000000b3", whoopApp, "2026-10-04T06:02:00Z", 60))
	batch := `{"schema":"vitamux.ingest.batch/1","connection_id":"` + ingest.FormatConnectionID(e.conn) +
		`","client":{"kind":"device","name":"phone","version":"0.0.0-old"},"items":[{"stream":"` + applehealth.StreamSamples +
		`","external_key":"` + hkHR + `:synthetic-1","fetched_at":"2026-10-04T08:00:01Z","content_type":"application/json","body":` + page + `}]}`
	req := request(t, http.MethodPost, "/api/ingest/v1/batches", strings.NewReader(batch))
	req.Header.Set("Idempotency-Key", "synthetic-guard-1")
	req.Header.Set("Authorization", "Bearer "+e.tok)
	if res := serve(t, e.h, req); res.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("upload: %d %s", res.StatusCode, raw)
	}

	reg, err := normalize.NewRegistry(applehealth.Normalizer{})
	if err != nil {
		t.Fatal(err)
	}
	proc := &normalize.Processor{DB: e.d, Blobs: e.blobs, Registry: reg, Log: slog.New(slog.DiscardHandler)}
	vers, err := normalize.RegisterVersions(ctx, e.d.Q(), reg)
	if err != nil {
		t.Fatal(err)
	}
	rawID := int64(e.count(`SELECT id FROM raw_payloads WHERE connection_id = $1`, e.conn))
	process := func() {
		t.Helper()
		if r, err := proc.Process(ctx, rawID, vers); err != nil || r.Outcome != normalize.Normalized {
			t.Fatalf("process: %+v %v", r, err)
		}
	}
	process()

	measurements := func(bundle string) int {
		return e.count(`SELECT count(*) FROM measurements m JOIN data_origins o ON o.id = m.origin_id
			WHERE m.user_id = $1 AND o.origin_key = $2 AND m.superseded_at IS NULL`, e.userID, bundle)
	}
	if a, w := measurements(appleWatch), measurements(whoopApp); a != 1 || w != 0 {
		t.Fatalf("measurements: apple %d, whoop %d; want 1 and 0", a, w)
	}
	if n := e.count(`SELECT records FROM ignored_records i JOIN data_origins o ON o.id = i.origin_id
		WHERE i.raw_payload_id = $1 AND o.origin_key = $2 AND i.item_kind = 'metric' AND i.item_code = 'heart_rate'
		AND i.reason = 'ignored_by_filter'`, rawID, whoopApp); n != 2 {
		t.Fatalf("%d ignored records, want 2", n)
	}
	if n := e.count(`SELECT count(*) FROM raw_payloads WHERE id = $1 AND status = 'normalized'
		AND warnings @> '[{"code": "ignored_by_filter", "detail": "com.whoop.iphone"}]'`, rawID); n != 1 {
		t.Fatal("the raw payload stays stored, normalized, with an ignored_by_filter warning")
	}

	// Hidden by default, listed on request.
	st, inv := e.call("GET /api/v1/inventory", "/api/v1/inventory", "", "")
	e.want(st, inv, http.StatusOK, "")
	if _, ok := inv["ignored"]; ok {
		t.Fatal("the inventory lists ignored records only with include_ignored")
	}
	st, inv = e.call("GET /api/v1/inventory", "/api/v1/inventory?include_ignored=true", "", "")
	e.want(st, inv, http.StatusOK, "")
	ig := inv["ignored"].([]any)
	if len(ig) != 1 || ig[0].(map[string]any)["code"] != "heart_rate" || ig[0].(map[string]any)["records"] != float64(2) ||
		ig[0].(map[string]any)["origin"].(map[string]any)["key"] != whoopApp {
		t.Fatalf("ignored items %v", ig)
	}
	for _, it := range inv["items"].([]any) {
		for _, o := range it.(map[string]any)["origins"].([]any) {
			if o.(map[string]any)["key"] == whoopApp {
				t.Fatal("ignored records never count in the items")
			}
		}
	}
	series := "/api/v1/sources/series?metric=heart_rate&start=2026-10-03T22:00:00Z&end=2026-10-04T22:00:00Z&grain=day"
	st, s := e.call("GET /api/v1/sources/series", series, "", "")
	e.want(st, s, http.StatusOK, "")
	if _, ok := s["ignored"]; ok {
		t.Fatal("the all-sources view lists ignored records only with include_ignored")
	}
	st, s = e.call("GET /api/v1/sources/series", series+"&include_ignored=true", "", "")
	e.want(st, s, http.StatusOK, "")
	if ig := s["ignored"].([]any); len(ig) != 1 || ig[0].(map[string]any)["records"] != float64(2) {
		t.Fatalf("ignored sources %v", s["ignored"])
	}
	view := e.filter()
	if w := origin(t, view, whoopApp); w["ignored_records"] != float64(2) || w["origin_id"] == nil {
		t.Fatalf("filter view: %v", w)
	}

	// Taking WHOOP queues the held payload again; normalizing it takes the rows.
	st, body := e.put(`{"origins": [{"bundle_id": "com.whoop.iphone", "mode": "take"}]}`)
	e.want(st, body, http.StatusOK, "")
	if n := e.count(`SELECT count(*) FROM raw_payloads WHERE id = $1 AND status = 'stored'`, rawID); n != 1 {
		t.Fatal("the payload holding ignored records is normalized again")
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE kind = 'normalize_batch' AND payload ->> 'batch_id' =
		(SELECT batch_id::text FROM raw_payloads WHERE id = $1)`, rawID); n < 1 {
		t.Fatal("no normalize_batch job queued")
	}
	if raw := e.count(`SELECT (detail ->> 'renormalized_payloads')::int FROM audit_events WHERE action = 'device.source_filter'`); raw != 1 {
		t.Fatalf("audit renormalized_payloads %d", raw)
	}
	process()
	if w := measurements(whoopApp); w != 2 {
		t.Fatalf("whoop measurements after take: %d", w)
	}
	if n := e.count(`SELECT count(*) FROM ignored_records WHERE raw_payload_id = $1`, rawID); n != 0 {
		t.Fatal("normalizing again clears the ignored records")
	}
	b, _ := json.Marshal(e.self()["anchor_resets"])
	if !strings.Contains(string(b), `"*"`) {
		t.Fatalf("WHOOP's types were not reported, so every type is pulled again: %s", b)
	}
}
