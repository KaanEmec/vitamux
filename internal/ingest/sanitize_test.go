package ingest

import (
	"encoding/json"
	"strings"
	"testing"
)

const sentinel = "SENTINELnotarealsecret"

// sentinelRequest carries the sentinel in every credential position a connector or client
// could plausibly put it.
func sentinelRequest() Request {
	return Request{
		Endpoint: "https://user:" + sentinel + "@wbsapi.example.test/measure?action=getmeas&access_token=" + sentinel + "&oauth_signature=" + sentinel + "#" + sentinel,
		Params: map[string]any{
			"meastype":      "1,9,10",
			"access_token":  sentinel,
			"refresh_token": sentinel,
			"client_secret": sentinel,
			"code":          sentinel,
			"signature":     sentinel,
			"nonce":         sentinel,
			"state":         sentinel,
			"sessionid":     sentinel,
			"Authorization": "Bearer " + sentinel,
			"nested":        map[string]any{"password": sentinel, "startdate": 1726300000},
			"list":          []any{map[string]any{"api_key": sentinel}},
			"callback":      "https://cb.example.test/hook?token=" + sentinel,
			"typed":         map[string]string{"id_token": sentinel, "unit": "kg"},
		},
		Headers: map[string]string{
			"Authorization": "Bearer " + sentinel,
			"cookie":        "sid=" + sentinel,
			"X-Api-Key":     sentinel,
			"X-Auth":        sentinel,
			"accept":        "application/json",
		},
	}
}

func TestSanitizeRequestStripsCredentials(t *testing.T) {
	out := SanitizeRequest(sentinelRequest())
	if strings.Contains(string(out), sentinel) {
		t.Fatalf("sentinel leaked into request_meta: %s", out)
	}
	var meta struct {
		Endpoint string            `json:"endpoint"`
		Params   map[string]any    `json:"params"`
		Headers  map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(out, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Endpoint != "https://wbsapi.example.test/measure" {
		t.Errorf("endpoint = %q", meta.Endpoint)
	}
	if meta.Params["action"] != "getmeas" || meta.Params["meastype"] != "1,9,10" {
		t.Errorf("non-secret params lost: %v", meta.Params)
	}
	if n, ok := meta.Params["nested"].(map[string]any); !ok || n["startdate"] == nil {
		t.Errorf("nested non-secret param lost: %v", meta.Params["nested"])
	}
	if meta.Headers["Accept"] != "application/json" || len(meta.Headers) != 1 {
		t.Errorf("headers = %v", meta.Headers)
	}
}

func TestSanitizeEmptyRequest(t *testing.T) {
	if got := string(SanitizeRequest(Request{})); got != "{}" {
		t.Fatalf("got %s", got)
	}
}
