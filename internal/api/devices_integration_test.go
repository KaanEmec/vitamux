//go:build integration

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/ingest"
)

// Synthetic device names and codes only.

type deviceEnv struct {
	*authEnv
	cookie, csrf string
}

func newDeviceEnv(t *testing.T) *deviceEnv {
	e := &deviceEnv{authEnv: newAuthEnv(t, false)}
	e.cookie, e.csrf = e.login()
	return e
}

// call sends one request, checks the response against the spec operation pattern and
// returns it decoded. bearer authenticates as a device; otherwise owner requests use the session.
func (e *deviceEnv) call(pattern, path, body, bearer string) (int, map[string]any) {
	e.t.Helper()
	method, _, _ := strings.Cut(pattern, " ")
	c := call{bearer: bearer}
	if bearer == "" && strings.HasPrefix(path, "/api/v1/") {
		c.cookie, c.csrf = e.cookie, e.csrf
	}
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := request(e.t, method, path, r)
	if c.cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.cookie})
		req.Header.Set(csrfHeader, c.csrf)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	res := serve(e.t, e.h, req)
	raw := checkResponse(e.t, pattern, res)
	var out map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			e.t.Fatalf("%s: %d %s", pattern, res.StatusCode, raw)
		}
	}
	return res.StatusCode, out
}

func (e *deviceEnv) want(status int, body map[string]any, wantStatus int, code Code) {
	e.t.Helper()
	if status != wantStatus || (code != "" && body["code"] != string(code)) {
		e.t.Fatalf("got %d %v, want %d %s", status, body, wantStatus, code)
	}
}

func (e *deviceEnv) pairingCode() string {
	e.t.Helper()
	st, body := e.call("POST /api/v1/devices/pairing-codes", "/api/v1/devices/pairing-codes", "", "")
	e.want(st, body, http.StatusCreated, "")
	code := body["code"].(string)
	e.secrets = append(e.secrets, code, strings.ReplaceAll(code, "-", ""))
	return code
}

// pair redeems code and returns the device id, connection id and token.
func (e *deviceEnv) pair(code, name string) (string, string, string) {
	e.t.Helper()
	b, _ := json.Marshal(map[string]string{"code": code, "name": name})
	st, body := e.call("POST /api/ingest/v1/devices/pair", "/api/ingest/v1/devices/pair", string(b), "")
	e.want(st, body, http.StatusCreated, "")
	tok := body["token"].(string)
	e.secrets = append(e.secrets, tok)
	return body["device_id"].(string), body["connection_id"].(string), tok
}

func (e *deviceEnv) upload(conn uuid.UUID, tok string) (int, map[string]any) {
	e.t.Helper()
	req := request(e.t, http.MethodPost, "/api/ingest/v1/batches",
		strings.NewReader(string(batchBody(conn, testItem{key: uuid.NewString(), body: `{"v": 1}`}))))
	req.Header.Set("Idempotency-Key", uuid.NewString())
	req.Header.Set("Authorization", "Bearer "+tok)
	res := serve(e.t, e.h, req)
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

// TestDevicePairing: pair → upload → anchor resets → rotate → revoke, with each old token
// refused at once (J15.2 done-when: a revoked token gets 401 immediately).
func TestDevicePairing(t *testing.T) {
	e := newDeviceEnv(t)

	st, body := e.call("POST /api/v1/devices/pairing-codes", "/api/v1/devices/pairing-codes", "", "")
	e.want(st, body, http.StatusCreated, "")
	code := body["code"].(string)
	e.secrets = append(e.secrets, code)
	var qr map[string]string
	if err := json.Unmarshal([]byte(body["qr_payload"].(string)), &qr); err != nil || len(qr) != 2 ||
		qr["code"] != code || qr["url"] != "https://vitamux.example.test" || body["url"] != qr["url"] {
		t.Fatalf("qr payload must hold exactly the public URL and the code: %v", body)
	}

	// Typed by hand: lower case, no dash.
	devID, connID, tok := e.pair(strings.ToLower(strings.ReplaceAll(code, "-", "")), "Synthetic iPhone")
	if _, err := uuid.Parse(devID); err != nil || !regexp.MustCompile(`^conn_[0-9a-f]{32}$`).MatchString(connID) {
		t.Fatalf("device_id %q, connection_id %q", devID, connID)
	}
	conn, _ := ingest.ParseConnectionID(connID)
	if n := e.count(`SELECT count(*) FROM connections c JOIN providers p ON p.id = c.provider_id
		WHERE c.id = $1 AND c.user_id = $2 AND p.code = 'apple_health' AND c.mode = 'push'`, conn, e.userID); n != 1 {
		t.Fatal("pairing did not create the owner's apple_health push connection")
	}
	if n := e.count(`SELECT count(*) FROM pairing_codes WHERE code_hash = sha256($1::bytea)`, []byte(code)); n != 0 {
		t.Fatal("the code must be stored hashed, without its dash")
	}

	// Single use.
	b, _ := json.Marshal(map[string]string{"code": code, "name": "Synthetic iPad"})
	st, body = e.call("POST /api/ingest/v1/devices/pair", "/api/ingest/v1/devices/pair", string(b), "")
	e.want(st, body, http.StatusUnprocessableEntity, CodeValidationFailed)

	if st, body := e.upload(conn, tok); st != http.StatusAccepted {
		t.Fatalf("upload with the device token: %d %v", st, body)
	}

	st, body = e.call("GET /api/ingest/v1/devices/self", "/api/ingest/v1/devices/self", "", tok)
	e.want(st, body, http.StatusOK, "")
	if body["device_id"] != devID || body["connection_id"] != connID || body["name"] != "Synthetic iPhone" || len(body["anchor_resets"].([]any)) != 0 {
		t.Fatalf("self: %v", body)
	}

	resetPath := "/api/v1/devices/" + devID + "/request-anchor-reset"
	st, body = e.call("POST /api/v1/devices/{id}/request-anchor-reset", resetPath, `{"types":["HKQuantityTypeIdentifierHeartRate"]}`, "")
	e.want(st, body, http.StatusNoContent, "")
	st, body = e.call("POST /api/v1/devices/{id}/request-anchor-reset", resetPath, "", "")
	e.want(st, body, http.StatusNoContent, "")
	st, body = e.call("POST /api/v1/devices/{id}/request-anchor-reset", resetPath, `{"types":["heart rate"]}`, "")
	e.want(st, body, http.StatusUnprocessableEntity, CodeValidationFailed)
	st, body = e.call("POST /api/v1/devices/{id}/request-anchor-reset", "/api/v1/devices/"+uuid.NewString()+"/request-anchor-reset", "", "")
	e.want(st, body, http.StatusNotFound, CodeNotFound)

	_, body = e.call("GET /api/ingest/v1/devices/self", "/api/ingest/v1/devices/self", "", tok)
	resets := body["anchor_resets"].([]any)
	if len(resets) != 2 || resets[0].(map[string]any)["type"] != "*" || resets[1].(map[string]any)["type"] != "HKQuantityTypeIdentifierHeartRate" {
		t.Fatalf("anchor resets: %v", resets)
	}

	st, body = e.call("GET /api/v1/devices", "/api/v1/devices", "", "")
	e.want(st, body, http.StatusOK, "")
	devs := body["devices"].([]any)
	d := devs[0].(map[string]any)
	if len(devs) != 1 || d["id"] != devID || d["last_sync_at"] == nil || d["revoked_at"] != nil || len(d["anchor_resets"].([]any)) != 2 {
		t.Fatalf("devices: %v", devs)
	}

	// Rotate: the old token fails at once, the new one works.
	st, body = e.call("POST /api/ingest/v1/devices/self/rotate-token", "/api/ingest/v1/devices/self/rotate-token", "", tok)
	e.want(st, body, http.StatusOK, "")
	newTok := body["token"].(string)
	e.secrets = append(e.secrets, newTok)
	if newTok == tok || !strings.HasPrefix(newTok, "vmx_cli_"+strings.ReplaceAll(devID, "-", "")+"_") {
		t.Fatalf("rotated token %q keeps the device id and changes the secret", newTok)
	}
	if st, body := e.upload(conn, tok); st != http.StatusUnauthorized {
		t.Fatalf("old token after rotation: %d %v", st, body)
	}
	if st, body := e.upload(conn, newTok); st != http.StatusAccepted {
		t.Fatalf("new token: %d %v", st, body)
	}

	// Revoke: the next ingest call is 401.
	st, body = e.call("POST /api/v1/devices/{id}/revoke", "/api/v1/devices/"+devID+"/revoke", "", "")
	e.want(st, body, http.StatusNoContent, "")
	if st, body := e.upload(conn, newTok); st != http.StatusUnauthorized || body["code"] != string(CodeUnauthenticated) {
		t.Fatalf("revoked token: %d %v", st, body)
	}
	st, body = e.call("GET /api/ingest/v1/devices/self", "/api/ingest/v1/devices/self", "", newTok)
	e.want(st, body, http.StatusUnauthorized, CodeUnauthenticated)
	st, body = e.call("POST /api/v1/devices/{id}/revoke", "/api/v1/devices/"+devID+"/revoke", "", "")
	e.want(st, body, http.StatusNotFound, CodeNotFound)

	// A second device joins the same connection.
	_, conn2, _ := e.pair(e.pairingCode(), "Synthetic iPad")
	if conn2 != connID {
		t.Fatalf("second device on %s, want the existing %s", conn2, connID)
	}
	_, body = e.call("GET /api/v1/devices", "/api/v1/devices", "", "")
	if devs := body["devices"].([]any); len(devs) != 2 || devs[1].(map[string]any)["revoked_at"] == nil {
		t.Fatalf("devices after revoke and re-pair: %v", devs)
	}

	for action, want := range map[string]int{"pairing_code.create": 2, "device.pair": 2, "connection.create": 1,
		"device.anchor_reset": 2, "client.rotate_token": 1, "device.revoke": 1} {
		if n := e.count(`SELECT count(*) FROM audit_events WHERE action = $1`, action); n != want {
			t.Errorf("audit %s: %d events, want %d", action, n, want)
		}
	}
}

// TestPairingCodeRejections: expired and malformed codes fail, wrong guesses are throttled
// per address, and creating codes is rate-limited per owner.
func TestPairingCodeRejections(t *testing.T) {
	e := newDeviceEnv(t)
	pair := func(code string) (int, map[string]any) {
		b, _ := json.Marshal(map[string]string{"code": code, "name": "Synthetic iPhone"})
		return e.call("POST /api/ingest/v1/devices/pair", "/api/ingest/v1/devices/pair", string(b), "")
	}

	expired := e.pairingCode()
	e.exec(`UPDATE pairing_codes SET expires_at = now() - interval '1 second'`)
	st, body := pair(expired)
	e.want(st, body, http.StatusUnprocessableEntity, CodeValidationFailed)
	st, body = e.call("POST /api/ingest/v1/devices/pair", "/api/ingest/v1/devices/pair", `{"code":"ABCD-EFGH"}`, "")
	e.want(st, body, http.StatusUnprocessableEntity, CodeValidationFailed) // no name

	// 5 codes within the window are fine (the expired one is gone); the 6th is refused.
	codes := []string{}
	for range 5 {
		codes = append(codes, e.pairingCode())
	}
	st, body = e.call("POST /api/v1/devices/pairing-codes", "/api/v1/devices/pairing-codes", "", "")
	e.want(st, body, http.StatusTooManyRequests, CodeRateLimited)

	// Wrong guesses (expired and malformed ones count) lock the address out, even for a valid code.
	for _, c := range []string{"0000-0000", "0000-0001", "not a code", "ZZZZ-ZZZZ"} {
		st, body = pair(c)
		e.want(st, body, http.StatusUnprocessableEntity, CodeValidationFailed)
	}
	st, body = pair(codes[0])
	e.want(st, body, http.StatusTooManyRequests, CodeRateLimited)
	if n := e.count(`SELECT count(*) FROM clients`); n != 0 {
		t.Fatalf("%d clients after rejected pairings", n)
	}
}

// TestPossiblyDenied: a requested type with no payload for 7 days is flagged once the device is
// that old and still in contact; a type with a recent payload, a young device and a revoked one are not.
func TestPossiblyDenied(t *testing.T) {
	const heart, steps = "HKQuantityTypeIdentifierHeartRate", "HKQuantityTypeIdentifierStepCount"
	e := newDeviceEnv(t)
	devID, connID, tok := e.pair(e.pairingCode(), "Synthetic iPhone")
	conn, _ := ingest.ParseConnectionID(connID)
	if st, body := e.upload(conn, tok); st != http.StatusAccepted {
		t.Fatalf("upload: %d %v", st, body)
	}
	e.exec(`INSERT INTO sync_cursors (connection_id, stream, cursor) VALUES ($1, 'healthkit.samples.v1', $2::jsonb)`,
		conn, `{"`+heart+`": "a1", "`+steps+`": "b1"}`)
	e.exec(`INSERT INTO raw_payloads (user_id, connection_id, batch_id, stream, external_key, content_sha256, content_type, fetched_at)
		SELECT user_id, connection_id, batch_id, 'healthkit.samples.v1', $2, content_sha256, content_type, fetched_at
		FROM raw_payloads WHERE connection_id = $1 LIMIT 1`, conn, heart+":k1")

	denied := func() []any {
		e.t.Helper()
		st, body := e.call("GET /api/v1/devices", "/api/v1/devices", "", "")
		e.want(st, body, http.StatusOK, "")
		for _, d := range body["devices"].([]any) {
			if d.(map[string]any)["id"] == devID {
				return d.(map[string]any)["possibly_denied"].([]any)
			}
		}
		e.t.Fatalf("device %s not listed", devID)
		return nil
	}
	if got := denied(); len(got) != 0 {
		t.Fatalf("a device paired minutes ago has no flags: %v", got)
	}
	e.exec(`UPDATE clients SET created_at = now() - interval '8 days', last_seen_at = now() WHERE id = $1`, uuid.MustParse(devID))
	if got := denied(); len(got) != 1 || got[0] != steps {
		t.Fatalf("only the silent type is flagged: %v", got)
	}
	e.exec(`UPDATE clients SET last_seen_at = now() - interval '8 days' WHERE id = $1`, uuid.MustParse(devID))
	if got := denied(); len(got) != 0 {
		t.Fatalf("a device out of contact flags nothing: %v", got)
	}
}

// TestOriginClassification: the owner lists origins, sets and clears the vendor one relays, and
// the choice is audited; unknown vendors and origins are refused.
func TestOriginClassification(t *testing.T) {
	e := newDeviceEnv(t)
	e.exec(`INSERT INTO data_origins (id, user_id, provider_id, origin_key, name, is_native, relayed_provider_id)
		SELECT gen_random_uuid(), $1, p.id, v.key, v.name, v.native, NULL
		FROM (VALUES ('com.example.synthetic.relay', 'Synthetic Relay', false), ('com.apple.health.synthetic', 'Synthetic Native', true)) AS v (key, name, native),
		     providers p WHERE p.code = 'apple_health'`, e.userID)
	e.exec(`INSERT INTO devices (id, user_id, provider_id, fingerprint, device_type, manufacturer, model)
		SELECT gen_random_uuid(), $1, p.id, 'synthetic-watch', 'watch', 'Synthetic', 'Watch 1' FROM providers p WHERE p.code = 'apple_health'`, e.userID)

	list := func() map[string]map[string]any {
		e.t.Helper()
		st, body := e.call("GET /api/v1/origins", "/api/v1/origins", "", "")
		e.want(st, body, http.StatusOK, "")
		out := map[string]map[string]any{}
		for _, o := range body["origins"].([]any) {
			out[o.(map[string]any)["origin_key"].(string)] = o.(map[string]any)
		}
		return out
	}
	_, body := e.call("GET /api/v1/origins", "/api/v1/origins", "", "")
	var codes []string
	for _, t := range body["relay_targets"].([]any) {
		codes = append(codes, t.(map[string]any)["code"].(string))
	}
	if !slices.Contains(codes, "garmin") {
		t.Fatalf("relay targets %v lack the seeded garmin", codes)
	}
	relay := list()["com.example.synthetic.relay"]
	if relay["relayed_provider"] != nil || relay["is_native"] != false || list()["com.apple.health.synthetic"]["is_native"] != true {
		t.Fatalf("origins: %v", list())
	}

	path := "/api/v1/origins/" + relay["id"].(string)
	st, body := e.call("PATCH /api/v1/origins/{id}", path, `{"relayed_provider":"garmin"}`, "")
	e.want(st, body, http.StatusNoContent, "")
	if got := list()["com.example.synthetic.relay"]["relayed_provider"]; got != "garmin" {
		t.Fatalf("relayed_provider %v", got)
	}
	st, body = e.call("PATCH /api/v1/origins/{id}", path, `{"relayed_provider":"nope"}`, "")
	e.want(st, body, http.StatusUnprocessableEntity, CodeValidationFailed)
	st, body = e.call("PATCH /api/v1/origins/{id}", "/api/v1/origins/"+uuid.NewString(), `{"relayed_provider":null}`, "")
	e.want(st, body, http.StatusNotFound, CodeNotFound)
	st, body = e.call("PATCH /api/v1/origins/{id}", path, `{"relayed_provider":null}`, "")
	e.want(st, body, http.StatusNoContent, "")
	if got := list()["com.example.synthetic.relay"]["relayed_provider"]; got != nil {
		t.Fatalf("cleared relayed_provider %v", got)
	}
	if n := e.count(`SELECT count(*) FROM audit_events WHERE action = 'origin.classify'`); n != 2 {
		t.Errorf("%d origin.classify audit events, want 2", n)
	}

	st, body = e.call("GET /api/v1/source-devices", "/api/v1/source-devices", "", "")
	e.want(st, body, http.StatusOK, "")
	devs := body["devices"].([]any)
	if d := devs[0].(map[string]any); len(devs) != 1 || d["device_type"] != "watch" || d["model"] != "Watch 1" || !regexp.MustCompile(`^dev_[0-9a-f]{32}$`).MatchString(d["id"].(string)) {
		t.Fatalf("source devices: %v", devs)
	}

	st, body = e.call("GET /api/v1/coverage", "/api/v1/coverage?start_date=2026-09-01&end_date=2026-09-07&origin=com.example.synthetic.relay", "", "")
	e.want(st, body, http.StatusOK, "")
}
