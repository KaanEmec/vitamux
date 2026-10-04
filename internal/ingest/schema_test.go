package ingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaDir = "../../schemas"

func compile(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	abs, err := filepath.Abs(filepath.Join(schemaDir, name))
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(abs)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func readExample(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(schemaDir, "examples", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func schemaValid(t *testing.T, s *jsonschema.Schema, doc []byte) bool {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return false
	}
	return s.Validate(inst) == nil
}

func TestExamplesValidate(t *testing.T) {
	batch, hb := compile(t, "ingest-batch.v1.json"), compile(t, "heartbeat.v1.json")
	for _, name := range []string{"ingest-batch.v1.healthkit.json", "ingest-batch.v1.import-file.json"} {
		doc := readExample(t, name)
		if !schemaValid(t, batch, doc) {
			t.Errorf("%s: rejected by the JSON Schema", name)
		}
		b, err := DecodeBatch(doc)
		if err != nil {
			t.Errorf("%s: rejected by DecodeBatch: %v", name, err)
			continue
		}
		for i, it := range b.Items {
			if _, err := it.Raw(); err != nil {
				t.Errorf("%s item %d: Raw: %v", name, i, err)
			}
		}
	}
	var hk struct {
		Items []struct{ Body json.RawMessage }
	}
	if err := json.Unmarshal(readExample(t, "ingest-batch.v1.healthkit.json"), &hk); err != nil {
		t.Fatal(err)
	}
	if !schemaValid(t, compile(t, "healthkit-samples.v1.json"), hk.Items[0].Body) {
		t.Error("healthkit example body rejected by healthkit-samples.v1.json")
	}
	doc := readExample(t, "heartbeat.v1.json")
	if !schemaValid(t, hb, doc) {
		t.Error("heartbeat example rejected by the JSON Schema")
	}
	if _, err := DecodeHeartbeat(doc); err != nil {
		t.Errorf("heartbeat example rejected by DecodeHeartbeat: %v", err)
	}
}

// mutate applies f to a decoded copy of doc and re-encodes it.
func mutate(t *testing.T, doc []byte, f func(m map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(doc, &m); err != nil {
		t.Fatal(err)
	}
	f(m)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func item0(m map[string]any) map[string]any { return m["items"].([]any)[0].(map[string]any) }

// The Go validator and the JSON Schema must agree on every case.
func TestBatchValidatorMatchesSchema(t *testing.T) {
	s := compile(t, "ingest-batch.v1.json")
	base := readExample(t, "ingest-batch.v1.healthkit.json")
	cases := map[string]func(m map[string]any){
		"missing schema":       func(m map[string]any) { delete(m, "schema") },
		"wrong schema":         func(m map[string]any) { m["schema"] = "vitamux.ingest.batch/2" },
		"bad connection id":    func(m map[string]any) { m["connection_id"] = "conn_XYZ" },
		"uuid connection id":   func(m map[string]any) { m["connection_id"] = "0190a1b2-c3d4-7e8f-9a0b-1c2d3e4f5a6b" },
		"bad client kind":      func(m map[string]any) { m["client"].(map[string]any)["kind"] = "phone" },
		"missing version":      func(m map[string]any) { delete(m["client"].(map[string]any), "version") },
		"unknown top field":    func(m map[string]any) { m["extra"] = true },
		"no items":             func(m map[string]any) { m["items"] = []any{} },
		"bad stream":           func(m map[string]any) { item0(m)["stream"] = "HealthKit Samples" },
		"empty external key":   func(m map[string]any) { item0(m)["external_key"] = "" },
		"control in key":       func(m map[string]any) { item0(m)["external_key"] = "a\nb" },
		"bad fetched_at":       func(m map[string]any) { item0(m)["fetched_at"] = "yesterday" },
		"no offset":            func(m map[string]any) { item0(m)["fetched_at"] = "2026-09-14T09:02:11" },
		"non-json body type":   func(m map[string]any) { item0(m)["content_type"] = "text/csv" },
		"bad content type":     func(m map[string]any) { item0(m)["content_type"] = "JSON" },
		"uppercase sha256":     func(m map[string]any) { item0(m)["sha256"] = "AB" + string(bytes.Repeat([]byte("0"), 62)) },
		"body and blob":        func(m map[string]any) { item0(m)["blob_sha256"] = string(bytes.Repeat([]byte("a"), 64)) },
		"neither body or blob": func(m map[string]any) { delete(item0(m), "body") },
		"null body":            func(m map[string]any) { item0(m)["body"] = nil },
		"scalar body":          func(m map[string]any) { item0(m)["body"] = 42 },
		"unknown item field":   func(m map[string]any) { item0(m)["status"] = "stored" },
		"unknown request key":  func(m map[string]any) { item0(m)["request"].(map[string]any)["headers"] = map[string]any{} },
		"params not object":    func(m map[string]any) { item0(m)["request"].(map[string]any)["params"] = []any{} },
		"bad migration source": func(m map[string]any) { m["provenance"] = map[string]any{"migration_source": "Open Wearables"} },
		"provenance extra":     func(m map[string]any) { m["provenance"] = map[string]any{"by": "x"} },
	}
	for name, f := range cases {
		doc := mutate(t, base, f)
		if schemaValid(t, s, doc) {
			t.Errorf("%s: JSON Schema accepted it", name)
		}
		_, err := DecodeBatch(doc)
		if _, ok := errors.AsType[*ValidationError](err); !ok {
			t.Errorf("%s: DecodeBatch returned %v, want a ValidationError", name, err)
		}
	}
	// Still valid after these edits, for both.
	ok := map[string]func(m map[string]any){
		"null migration source": func(m map[string]any) { m["provenance"] = map[string]any{"migration_source": nil} },
		"vendor json type":      func(m map[string]any) { item0(m)["content_type"] = "application/vnd.withings+json; charset=utf-8" },
		"array body":            func(m map[string]any) { item0(m)["body"] = []any{map[string]any{"a": 1}} },
		"no request":            func(m map[string]any) { delete(item0(m), "request") },
	}
	for name, f := range ok {
		doc := mutate(t, base, f)
		if !schemaValid(t, s, doc) {
			t.Errorf("%s: JSON Schema rejected it", name)
		}
		if _, err := DecodeBatch(doc); err != nil {
			t.Errorf("%s: DecodeBatch: %v", name, err)
		}
	}
}

func TestBatchChecksums(t *testing.T) {
	body := []byte(`{"synthetic": true,  "value": 61}`)
	sum := sha256.Sum256(body)
	doc := func(sha string) []byte {
		return []byte(`{"schema":"vitamux.ingest.batch/1","connection_id":"conn_0190a1b2c3d47e8f9a0b1c2d3e4f5a6b",
			"client":{"kind":"importer","name":"t","version":"1"},
			"items":[{"stream":"s.v1","external_key":"k","fetched_at":"2026-09-14T10:00:00Z","content_type":"application/json",
			"sha256":"` + sha + `","body":` + string(body) + `}]}`)
	}
	b, err := DecodeBatch(doc(hex.EncodeToString(sum[:])))
	if err != nil {
		t.Fatalf("exact body bytes must hash to sha256: %v", err)
	}
	if !bytes.Equal(b.Items[0].Body, body) {
		t.Fatal("body bytes not preserved verbatim")
	}
	if _, err := DecodeBatch(doc(string(bytes.Repeat([]byte("0"), 64)))); err == nil {
		t.Fatal("sha256 mismatch accepted")
	}
	if _, err := DecodeBatch(append(doc(hex.EncodeToString(sum[:])), []byte(` {}`)...)); err == nil {
		t.Fatal("trailing data accepted")
	}
}

func TestHeartbeatValidatorMatchesSchema(t *testing.T) {
	s := compile(t, "heartbeat.v1.json")
	base := readExample(t, "heartbeat.v1.json")
	stream0 := func(m map[string]any) map[string]any { return m["streams"].([]any)[0].(map[string]any) }
	cases := map[string]func(m map[string]any){
		"wrong schema":         func(m map[string]any) { m["schema"] = "vitamux.ingest.batch/1" },
		"missing sent_at":      func(m map[string]any) { delete(m, "sent_at") },
		"negative units":       func(m map[string]any) { m["pending_failed_units"] = -1 },
		"fractional units":     func(m map[string]any) { m["pending_failed_units"] = 1.5 },
		"bad error class":      func(m map[string]any) { m["last_error_class"] = "Network Error" },
		"checkpoint not obj":   func(m map[string]any) { stream0(m)["checkpoint"] = "7a90" },
		"bad stream":           func(m map[string]any) { stream0(m)["stream"] = "" },
		"bad last_success_at":  func(m map[string]any) { stream0(m)["last_success_at"] = "now" },
		"unknown stream field": func(m map[string]any) { stream0(m)["anchor"] = "x" },
	}
	for name, f := range cases {
		doc := mutate(t, base, f)
		if schemaValid(t, s, doc) {
			t.Errorf("%s: JSON Schema accepted it", name)
		}
		if _, err := DecodeHeartbeat(doc); err == nil {
			t.Errorf("%s: DecodeHeartbeat accepted it", name)
		}
	}
}

func TestConnectionID(t *testing.T) {
	id, err := ParseConnectionID("conn_0190a1b2c3d47e8f9a0b1c2d3e4f5a6b")
	if err != nil || FormatConnectionID(id) != "conn_0190a1b2c3d47e8f9a0b1c2d3e4f5a6b" {
		t.Fatalf("round trip failed: %v", err)
	}
	if _, err := ParseConnectionID("conn_0190a1b2-c3d4-7e8f-9a0b-1c2d3e4f5a6b"); err == nil {
		t.Fatal("hyphenated id accepted")
	}
}
