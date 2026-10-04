package extract

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/KaanEmec/vitamux/internal/httpx"
	"github.com/KaanEmec/vitamux/prompts"
	"github.com/KaanEmec/vitamux/schemas"
)

// sentinelKey is a synthetic API key that must never appear in logs, errors or stored rows.
const sentinelKey = "SENTINEL-AI-KEY-5b1e-synthetic"

var testPDF = []byte("%PDF-1.4\n% synthetic test document\n%%EOF\n")

func truth(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../../fixtures/lab/lab-01.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func request() Request {
	return Request{PDF: testPDF, Pages: 1, Prompt: prompts.LabExtractionV1, Schema: schemas.LabExtractionV1}
}

// capture is an httptest provider that records the last request and answers with respond.
type capture struct {
	req  *http.Request
	body map[string]any
}

func provider(t *testing.T, respond func(w http.ResponseWriter)) (*capture, *httptest.Server) {
	t.Helper()
	c := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.req = r
		c.body = nil
		if err := json.Unmarshal(b, &c.body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		respond(w)
	}))
	t.Cleanup(srv.Close)
	return c, srv
}

func jsonReply(status int, v any, header ...string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		for i := 0; i+1 < len(header); i += 2 {
			w.Header().Set(header[i], header[i+1])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
}

func dig(v any, path ...string) any {
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[p]
	}
	return v
}

func geminiOK(text string) any {
	return map[string]any{
		"candidates":    []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": text}}}, "finishReason": "STOP"}},
		"usageMetadata": map[string]any{"promptTokenCount": 1200, "candidatesTokenCount": 800, "totalTokenCount": 2000},
		"modelVersion":  "gemini-test-001", "responseId": "gem-req-1",
	}
}

func TestGeminiRequestShape(t *testing.T) {
	c, srv := provider(t, jsonReply(200, geminiOK(truth(t))))
	g := NewGemini(GeminiConfig{APIKey: sentinelKey, Model: "gemini-test", BaseURL: srv.URL, Client: httpx.New(httpx.Options{})})
	res, err := g.Extract(t.Context(), request())
	if err != nil {
		t.Fatal(err)
	}
	if c.req.URL.Path != "/v1beta/models/gemini-test:generateContent" || c.req.URL.RawQuery != "" {
		t.Errorf("target %s?%s", c.req.URL.Path, c.req.URL.RawQuery)
	}
	if c.req.Header.Get("x-goog-api-key") != sentinelKey {
		t.Error("API key not sent in x-goog-api-key")
	}
	if dig(c.body, "systemInstruction", "parts").([]any)[0].(map[string]any)["text"] != prompts.LabExtractionV1 {
		t.Error("system instruction is not the v1 prompt")
	}
	parts := dig(c.body, "contents").([]any)[0].(map[string]any)["parts"].([]any)
	inline := parts[0].(map[string]any)["inlineData"].(map[string]any)
	if inline["mimeType"] != "application/pdf" || inline["data"] != base64.StdEncoding.EncodeToString(testPDF) {
		t.Errorf("inline PDF part: %v", inline["mimeType"])
	}
	gc := dig(c.body, "generationConfig").(map[string]any)
	if gc["responseMimeType"] != "application/json" || gc["temperature"] != float64(0) {
		t.Errorf("generationConfig: %v", gc)
	}
	schema, _ := json.Marshal(gc["responseJsonSchema"])
	for _, banned := range []string{`"synthetic"`, `"uniqueItems"`, `"pattern"`, `"minLength"`, `"$schema"`, `"const"`, `null]`} {
		if bytes.Contains(schema, []byte(banned)) {
			t.Errorf("Gemini schema contains %s", banned)
		}
	}
	if !bytes.Contains(schema, []byte(`"anyOf"`)) {
		t.Error("nullable comparator enum not rewritten as anyOf")
	}
	if res.ModelID != "gemini-test-001" || res.RequestID != "gem-req-1" || res.Usage.TotalTokens != 2000 {
		t.Errorf("response meta: %+v", res.Usage)
	}
	if len(res.Extraction.Rows) != 13 || !bytes.Contains(res.Raw, []byte("candidates")) {
		t.Errorf("rows %d", len(res.Extraction.Rows))
	}
}

func openAIOK(text string) any {
	return map[string]any{
		"id": "resp_1", "status": "completed", "model": "gpt-test-2025",
		"output": []any{
			map[string]any{"type": "reasoning", "summary": []any{}},
			map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": text}}},
		},
		"usage": map[string]any{"input_tokens": 1500, "output_tokens": 900, "total_tokens": 2400},
	}
}

func TestOpenAIRequestShape(t *testing.T) {
	c, srv := provider(t, jsonReply(200, openAIOK(truth(t)), "x-request-id", "req_abc"))
	o := NewOpenAI(OpenAIConfig{APIKey: sentinelKey, Model: "gpt-test", BaseURL: srv.URL + "/v1", Client: httpx.New(httpx.Options{})})
	res, err := o.Extract(t.Context(), request())
	if err != nil {
		t.Fatal(err)
	}
	if o.ID() != OpenAI || c.req.URL.Path != "/v1/responses" || c.req.Header.Get("Authorization") != "Bearer "+sentinelKey {
		t.Errorf("target %s, id %s", c.req.URL.Path, o.ID())
	}
	if c.body["model"] != "gpt-test" || c.body["instructions"] != prompts.LabExtractionV1 || c.body["store"] != false {
		t.Errorf("model/instructions/store: %v %v", c.body["model"], c.body["store"])
	}
	content := dig(c.body, "input").([]any)[0].(map[string]any)["content"].([]any)
	file := content[0].(map[string]any)
	if file["type"] != "input_file" || file["file_data"] != "data:application/pdf;base64,"+base64.StdEncoding.EncodeToString(testPDF) {
		t.Errorf("file part: %v", file["type"])
	}
	format := dig(c.body, "text", "format").(map[string]any)
	if format["type"] != "json_schema" || format["strict"] != true || format["name"] != "lab_extraction_v1" {
		t.Errorf("format: %v", format)
	}
	schema, _ := json.Marshal(format["schema"])
	for _, banned := range []string{`"synthetic"`, `"uniqueItems"`, `"minLength"`, `"maxLength"`, `"$schema"`, `"const"`} {
		if bytes.Contains(schema, []byte(banned)) {
			t.Errorf("OpenAI schema contains %s", banned)
		}
	}
	if !bytes.Contains(schema, []byte(`"pattern"`)) {
		t.Error("OpenAI schema lost the date pattern")
	}
	if res.ModelID != "gpt-test-2025" || res.RequestID != "req_abc" || res.Usage.InputTokens != 1500 || len(res.Extraction.Rows) != 13 {
		t.Errorf("response: %s %s %+v", res.ModelID, res.RequestID, res.Usage)
	}
}

func TestOpenAICompatibleWithoutKey(t *testing.T) {
	c, srv := provider(t, jsonReply(200, openAIOK(truth(t))))
	o := NewOpenAI(OpenAIConfig{Compatible: true, Model: "local-model", BaseURL: srv.URL + "/v1/", Client: httpx.New(httpx.Options{})})
	res, err := o.Extract(t.Context(), request())
	if err != nil {
		t.Fatal(err)
	}
	if o.ID() != OpenAICompatible || !o.External() || c.req.URL.Path != "/v1/responses" || c.req.Header.Get("Authorization") != "" {
		t.Errorf("id %s, path %s, auth %q", o.ID(), c.req.URL.Path, c.req.Header.Get("Authorization"))
	}
	if res.RequestID != "resp_1" {
		t.Errorf("request id %q", res.RequestID)
	}
}

func TestProviderFailures(t *testing.T) {
	echo := map[string]any{"error": map[string]any{"code": 401, "message": "Incorrect API key provided: " + sentinelKey, "status": "UNAUTHENTICATED"}}
	cases := []struct {
		name    string
		openai  bool
		reply   func(http.ResponseWriter)
		class   string
		raw     bool
		after   time.Duration
		message string
	}{
		{"rate limited", false, jsonReply(429, map[string]any{"error": map[string]any{"status": "RESOURCE_EXHAUSTED"}}, "Retry-After", "7"), ClassRateLimited, false, 7 * time.Second, "HTTP 429 (RESOURCE_EXHAUSTED)"},
		{"server error", true, jsonReply(503, map[string]any{}), ClassTransient, false, 0, "HTTP 503"},
		{"bad key echoed", false, jsonReply(401, echo), ClassAuth, false, 0, "UNAUTHENTICATED"},
		{"rejected", true, jsonReply(400, map[string]any{"error": map[string]any{"type": "invalid_request_error", "message": "bad " + sentinelKey}}), ClassRejected, false, 0, "invalid_request_error"},
		{"invalid output", false, jsonReply(200, geminiOK(`{"schema":"vitamux.lab.extraction/1"}`)), ClassInvalidOutput, true, 0, "/document"},
		{"truncated", false, jsonReply(200, map[string]any{"candidates": []any{map[string]any{"finishReason": "MAX_TOKENS"}}}), ClassInvalidOutput, true, 0, "MAX_TOKENS"},
		{"blocked", false, jsonReply(200, map[string]any{"promptFeedback": map[string]any{"blockReason": "SAFETY"}}), ClassRefused, true, 0, "SAFETY"},
		{"refusal", true, jsonReply(200, map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "refusal", "refusal": "no"}}}}}), ClassRefused, true, 0, "refused"},
		{"incomplete", true, jsonReply(200, map[string]any{"status": "incomplete", "incomplete_details": map[string]any{"reason": "max_output_tokens"}}), ClassInvalidOutput, true, 0, "max_output_tokens"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			client := httpx.New(httpx.Options{Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
			_, srv := provider(t, tc.reply)
			ex := NewGemini(GeminiConfig{APIKey: sentinelKey, Model: "m", BaseURL: srv.URL, Client: client})
			if tc.openai {
				ex = NewOpenAI(OpenAIConfig{APIKey: sentinelKey, Model: "m", BaseURL: srv.URL, Client: client})
			}
			res, err := ex.Extract(t.Context(), request())
			var e *Error
			if !errors.As(err, &e) || e.Class != tc.class || e.RetryAfter != tc.after {
				t.Fatalf("err %v, want class %s", err, tc.class)
			}
			if !strings.Contains(e.Error(), tc.message) {
				t.Errorf("error %q does not mention %q", e.Error(), tc.message)
			}
			if (len(res.Raw) > 0) != tc.raw {
				t.Errorf("raw kept: %v, want %v", len(res.Raw) > 0, tc.raw)
			}
			for what, s := range map[string]string{"error": e.Error(), "logs": logs.String()} {
				if strings.Contains(s, sentinelKey) || strings.Contains(s, "Incorrect API key") {
					t.Errorf("%s leaks the key or the provider message: %s", what, s)
				}
			}
		})
	}
}

func TestGeminiRefusesOversizedPDF(t *testing.T) {
	g := NewGemini(GeminiConfig{APIKey: sentinelKey, Model: "m", BaseURL: "http://127.0.0.1:1", Client: httpx.New(httpx.Options{})})
	req := request()
	req.PDF = make([]byte, geminiInlineMax+1)
	var e *Error
	if _, err := g.Extract(t.Context(), req); !errors.As(err, &e) || e.Class != ClassTooLarge {
		t.Fatalf("err %v", err)
	}
}

func TestFakeAnswersByChecksum(t *testing.T) {
	sum := "4d4fd2b3e1b8c3c1b6f4a0a0f0e9c6d0a4d95bb0f0b3fe0a1e1ef1d1f0d1f0d1" // not testPDF's
	f, err := newFake(fstest.MapFS{
		"manifest.json": {Data: []byte(`{"synthetic": true, "reports": [{"id": "lab-01", "sha256": "` + sum + `"}]}`)},
		"lab-01.json":   {Data: []byte(truth(t))},
	})
	if err != nil {
		t.Fatal(err)
	}
	var e *Error
	if _, err := f.Extract(t.Context(), request()); !errors.As(err, &e) || e.Class != ClassUnknownDocument {
		t.Fatalf("unknown PDF: %v", err)
	}
	real, err := NewFake()
	if err != nil {
		t.Fatal(err)
	}
	if real.External() || real.ID() != Fake || len(real.(*fake).truth) != 12 {
		t.Fatalf("embedded fixtures: %d", len(real.(*fake).truth))
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for v, want := range map[string]time.Duration{"": 0, "30": 30 * time.Second, "-1": 0, "Sat, 03 Oct 2026 12:01:00 GMT": time.Minute, "soon": 0} {
		if got := retryAfter(v, now); got != want {
			t.Errorf("%q: %v, want %v", v, got, want)
		}
	}
}
