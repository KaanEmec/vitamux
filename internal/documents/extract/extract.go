// Package extract runs blood-test extraction through pluggable providers behind one interface
// (ADR-0013, docs/architecture/lab-documents.md#extraction-provider-interface): a deterministic
// fake, Gemini, OpenAI and OpenAI-compatible servers. External providers need server-side
// enablement and per-request consent. Raw responses are sealed with the document key before
// they are stored, and neither keys, PDFs nor responses are ever logged.
package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/KaanEmec/vitamux/internal/config"
	"github.com/KaanEmec/vitamux/internal/documents"
	"github.com/KaanEmec/vitamux/internal/httpx"
)

// Provider ids (extraction_runs.provider).
const (
	Fake             = "fake"
	Gemini           = "gemini"
	OpenAI           = "openai"
	OpenAICompatible = "openai_compatible"
)

// Providers lists every provider id the API accepts.
var Providers = []string{Fake, Gemini, OpenAI, OpenAICompatible}

// Extractor turns one PDF into an extraction. Implementations hold their key and model; they
// never log the request, the PDF or the response.
type Extractor interface {
	ID() string
	External() bool // true: the PDF leaves the host, so enablement and consent are required
	Model() string  // configured model, which consent must name; empty for fake
	// Extract returns Raw whenever the provider answered, also with an error, so an invalid
	// response is kept (sealed) for diagnosis and reprocessing. Errors are *Error.
	Extract(ctx context.Context, req Request) (Response, error)
}

// Request is what every provider receives. Prompt and Schema are the embedded versions named
// by documents.PromptVersion and documents.ExtractionSchema.
type Request struct {
	PDF    []byte
	Pages  int
	Prompt string
	Schema []byte
}

// Response is a provider answer.
type Response struct {
	Raw        []byte // the whole response body
	Extraction *documents.Extraction
	ModelID    string // as reported by the provider
	RequestID  string
	Usage      Usage
}

// Usage holds the token counts a provider reported.
type Usage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
}

// Error classes (extraction_runs.error_class, job_runs.error_class).
const (
	ClassTransient           = "transient"      // network, timeout, 5xx: retried
	ClassRateLimited         = "rate_limited"   // 429: retried after Retry-After
	ClassAuth                = "auth"           // 401/403: check the key
	ClassRejected            = "rejected"       // other 4xx
	ClassTooLarge            = "too_large"      // the PDF exceeds what the provider accepts
	ClassRefused             = "refused"        // safety block or model refusal
	ClassInvalidOutput       = "invalid_output" // not the extraction contract, or truncated
	ClassUnknownDocument     = "unknown_document"
	ClassProviderUnavailable = "provider_unavailable" // no longer configured
	ClassProviderDisabled    = "provider_disabled"    // disabled after the run was queued
	ClassConsentMismatch     = "consent_mismatch"     // the configured model changed after consent
	ClassAbandoned           = "abandoned"            // its job ended without recording an outcome
)

// Error is a classified extraction failure. Its text never holds keys, PDF content or
// provider message bodies.
type Error struct {
	Class      string
	RetryAfter time.Duration // rate_limited only; zero when the provider gave none
	msg        string
}

func (e *Error) Error() string      { return e.msg }
func (e *Error) ErrorClass() string { return e.Class }

// Retryable reports whether another attempt may succeed.
func (e *Error) Retryable() bool { return e.Class == ClassTransient || e.Class == ClassRateLimited }

func errorf(class, format string, args ...any) *Error {
	return &Error{Class: class, msg: fmt.Sprintf(format, args...)}
}

// safeToken matches provider error codes that are safe to record (e.g. INVALID_ARGUMENT).
var safeToken = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// statusError classifies a non-2xx answer. Only the status and the provider's error code or
// type are kept: messages may echo parts of the key or the request.
func statusError(provider string, res *http.Response, body []byte) *Error {
	var b struct {
		Error struct {
			Status string `json:"status"` // Gemini
			Type   string `json:"type"`   // OpenAI
			Code   any    `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &b)
	code := ""
	for _, c := range []any{b.Error.Status, b.Error.Code, b.Error.Type} {
		if s, ok := c.(string); ok && safeToken.MatchString(s) {
			code = " (" + s + ")"
			break
		}
	}
	e := errorf(ClassRejected, "%s: HTTP %d%s", provider, res.StatusCode, code)
	switch s := res.StatusCode; {
	case s == http.StatusTooManyRequests:
		e.Class, e.RetryAfter = ClassRateLimited, retryAfter(res.Header.Get("Retry-After"), time.Now())
	case s == http.StatusRequestTimeout || s >= 500:
		e.Class = ClassTransient
	case s == http.StatusUnauthorized || s == http.StatusForbidden:
		e.Class = ClassAuth
	case s == http.StatusRequestEntityTooLarge:
		e.Class = ClassTooLarge
	}
	return e
}

func retryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

// post sends a JSON body and returns the response headers and body. Transport failures are
// transient unless the context ended.
func post(ctx context.Context, client *httpx.Client, provider, url string, header http.Header, body any) (http.Header, []byte, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, nil, errorf(ClassRejected, "%s: bad request URL", provider)
	}
	req.Header = header
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, errorf(ClassTransient, "%s: %v", provider, err) // httpx errors carry no query, headers or bodies
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		if errors.Is(err, httpx.ErrResponseTooLarge) {
			return nil, nil, errorf(ClassInvalidOutput, "%s: response too large", provider)
		}
		return nil, nil, errorf(ClassTransient, "%s: reading the response failed", provider)
	}
	if res.StatusCode/100 != 2 {
		return nil, nil, statusError(provider, res, raw)
	}
	return res.Header, raw, nil
}

// decode parses the model's JSON text against the extraction contract. Problems name fields,
// never values.
func decode(provider, text string) (*documents.Extraction, error) {
	x, err := documents.DecodeExtraction([]byte(text))
	if err != nil {
		return nil, errorf(ClassInvalidOutput, "%s: %v", provider, err)
	}
	return x, nil
}

// userText accompanies the PDF; the instructions are the system prompt.
const userText = "Transcribe the attached laboratory report into the JSON schema, following the instructions."

// providerSchema adapts the extraction schema to what structured-output APIs accept: no
// optional properties (synthetic is fixture-only), no keywords they reject, const as enum.
// DecodeExtraction still enforces the full contract on the answer.
func providerSchema(schema []byte, gemini bool) (map[string]any, error) {
	var s map[string]any
	if err := json.Unmarshal(schema, &s); err != nil {
		return nil, err
	}
	if props, ok := s["properties"].(map[string]any); ok {
		delete(props, "synthetic")
	}
	delete(s, "$schema")
	return adapt(s, gemini).(map[string]any), nil
}

func adapt(v any, gemini bool) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range t {
			switch k {
			case "uniqueItems", "minLength", "maxLength":
				continue
			case "pattern":
				if gemini {
					continue
				}
			case "const":
				out["enum"] = []any{x}
				continue
			case "properties", "$defs": // maps of names, not schemas
				m := map[string]any{}
				for name, sub := range x.(map[string]any) {
					m[name] = adapt(sub, gemini)
				}
				out[k] = m
				continue
			}
			out[k] = adapt(x, gemini)
		}
		// Gemini enums hold strings only: a nullable enum becomes anyOf.
		if enum, ok := out["enum"].([]any); ok && gemini && out["type"] == nil {
			var strs []any
			for _, e := range enum {
				if e != nil {
					strs = append(strs, e)
				}
			}
			if len(strs) < len(enum) {
				delete(out, "enum")
				out["anyOf"] = []any{map[string]any{"type": "string", "enum": strs}, map[string]any{"type": "null"}}
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = adapt(x, gemini)
		}
		return out
	}
	return v
}

// Timeouts and size caps for provider calls. One call may take minutes for a long report.
const (
	callTimeout      = 5 * time.Minute
	maxResponseBytes = 8 << 20
)

// Configured returns the external extractors cfg configures, sharing one safe HTTP client.
// The fake extractor is added by New.
func Configured(cfg config.Config) []Extractor {
	client := httpx.New(httpx.Options{Timeout: callTimeout, MaxResponseBytes: maxResponseBytes})
	var out []Extractor
	if cfg.GeminiAPIKey.IsSet() {
		out = append(out, NewGemini(GeminiConfig{APIKey: cfg.GeminiAPIKey.Value(), Model: cfg.GeminiModel, Client: client}))
	}
	if cfg.OpenAIAPIKey.IsSet() {
		out = append(out, NewOpenAI(OpenAIConfig{APIKey: cfg.OpenAIAPIKey.Value(), Model: cfg.OpenAIModel, Client: client}))
	}
	if cfg.OpenAICompatibleBaseURL != nil {
		out = append(out, NewOpenAI(OpenAIConfig{Compatible: true, BaseURL: cfg.OpenAICompatibleBaseURL.String(),
			APIKey: cfg.OpenAICompatibleAPIKey.Value(), Model: cfg.OpenAICompatibleModel, Client: client}))
	}
	return out
}
