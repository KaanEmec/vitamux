// Package remote runs connectors served by sidecars over protocol vitamux-connector/1
// (api/connector-sidecar.v1.yaml, schemas/connector-sidecar.v1.json, ADR-0017).
package remote

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/ingest"
)

// Protocol is the version a sidecar reports in describe and in ProtocolHeader on every response.
const (
	Protocol       = "vitamux-connector/1"
	ProtocolHeader = "Vitamux-Protocol"

	MaxBinaryBytes  = 25 << 20 // decoded body_base64 of one raw line, as for push ingest blobs
	MaxSessionBytes = 64 << 10 // decoded auth session
)

// errProtocol is a sidecar response that breaks the protocol. Messages never quote the
// response: it may carry health data or credentials.
var errProtocol = fmt.Errorf("sidecar protocol violation: %w", connectors.ErrTransient)

func violation(reason string) error { return fmt.Errorf("%w: %s", errProtocol, reason) }

// Describe is the response of GET /v1/describe.
type Describe struct {
	Protocol     string       `json:"protocol"`
	Provider     string       `json:"provider"`
	Name         string       `json:"name"`
	Version      string       `json:"version"`
	Official     bool         `json:"official"`
	AuthKind     string       `json:"auth_kind"`
	Streams      []Stream     `json:"streams"`
	RateLimits   []RateLimit  `json:"rate_limits,omitempty"`
	Capabilities Capabilities `json:"capabilities"`
	Upstream     *Upstream    `json:"upstream,omitempty"`
}

// Stream is connectors.StreamSpec with durations in whole seconds.
type Stream struct {
	Name             string `json:"name"`
	IntervalS        int64  `json:"interval_s,omitempty"`
	LookbackS        int64  `json:"lookback_s,omitempty"`
	CorrectionEveryS int64  `json:"correction_every_s,omitempty"`
	MaxBackfillS     int64  `json:"max_backfill_s,omitempty"`
	UnitSizeS        int64  `json:"unit_size_s,omitempty"`
}

// RateLimit is connectors.RateLimitSpec.
type RateLimit struct {
	Requests int   `json:"requests"`
	PerS     int64 `json:"per_s"`
}

// Capabilities is connectors.Capabilities.
type Capabilities struct {
	Incremental bool `json:"incremental,omitempty"`
	Backfill    bool `json:"backfill,omitempty"`
	Webhooks    bool `json:"webhooks,omitempty"`
	ManualSync  bool `json:"manual_sync,omitempty"`
}

// Upstream is connectors.Upstream.
type Upstream struct {
	Package   string `json:"package"`
	Version   string `json:"version"`
	SourceURL string `json:"source_url,omitempty"`
}

// AuthBeginRequest is the body of POST /v1/auth/begin.
type AuthBeginRequest struct {
	RedirectURL string          `json:"redirect_url"`
	State       string          `json:"state"`
	Config      json.RawMessage `json:"config,omitempty"`
}

// AuthContinueRequest is the body of POST /v1/auth/continue.
type AuthContinueRequest struct {
	RedirectURL string              `json:"redirect_url"`
	Callback    map[string][]string `json:"callback,omitempty"`
	Values      map[string]string   `json:"values,omitempty"`
	Session     []byte              `json:"session,omitempty"`
	Config      json.RawMessage     `json:"config,omitempty"`
}

// AuthResponse is the response of begin (Step) and continue (Step or Authorized).
type AuthResponse struct {
	Step       *Step       `json:"step,omitempty"`
	Authorized *Authorized `json:"authorized,omitempty"`
}

// Step is connectors.AuthStep: RedirectURL or Prompt.
type Step struct {
	RedirectURL string  `json:"redirect_url,omitempty"`
	Prompt      *Prompt `json:"prompt,omitempty"`
	Session     []byte  `json:"session,omitempty"`
}

// Prompt is connectors.AuthPrompt.
type Prompt struct {
	Message string  `json:"message"`
	Fields  []Field `json:"fields"`
}

// Field is connectors.AuthField.
type Field struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

// Authorized is a completed authorization.
type Authorized struct {
	AccountID   string                 `json:"account_id"`
	Credentials connectors.Credentials `json:"credentials"`
}

// RefreshRequest is the body of POST /v1/auth/refresh.
type RefreshRequest struct {
	Credentials connectors.Credentials `json:"credentials"`
	Config      json.RawMessage        `json:"config,omitempty"`
}

// RefreshResponse is the response of POST /v1/auth/refresh.
type RefreshResponse struct {
	Credentials connectors.Credentials `json:"credentials"`
}

// FetchRequest is the body of POST /v1/fetch: one page of one work unit.
type FetchRequest struct {
	Stream      string                 `json:"stream"`
	Mode        string                 `json:"mode,omitempty"`
	Cursor      json.RawMessage        `json:"cursor,omitempty"`
	From        time.Time              `json:"from,omitzero"`
	To          time.Time              `json:"to,omitzero"`
	Credentials connectors.Credentials `json:"credentials"`
	Config      json.RawMessage        `json:"config,omitempty"`
}

// Fetch response lines (application/x-ndjson): RawLine*, then one ResultLine or error line.
const (
	LineRaw    = "raw"
	LineResult = "result"
	LineError  = "error"
)

// RawLine is one verbatim provider record: Body (a JSON value) or BodyBase64 + SHA256.
type RawLine struct {
	Type        string          `json:"type"`
	Stream      string          `json:"stream,omitempty"`
	ExternalKey string          `json:"external_key"`
	ContentType string          `json:"content_type"`
	FetchedAt   time.Time       `json:"fetched_at,omitzero"`
	Body        json.RawMessage `json:"body,omitempty"`
	BodyBase64  []byte          `json:"body_base64,omitempty"`
	SHA256      string          `json:"sha256,omitempty"`
	Request     *Request        `json:"request,omitempty"`
	Quarantine  bool            `json:"quarantine,omitempty"`
}

// Request is ingest.Request; the core sanitizes it before storage.
type Request struct {
	Endpoint string            `json:"endpoint,omitempty"`
	Params   map[string]any    `json:"params,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
}

// ResultLine is connectors.FetchResult, the last line of a successful page.
type ResultLine struct {
	Type          string                  `json:"type"`
	NextCursor    json.RawMessage         `json:"next_cursor,omitempty"`
	HighWatermark time.Time               `json:"high_watermark,omitzero"`
	Done          bool                    `json:"done"`
	RetryAfterS   int64                   `json:"retry_after_s,omitempty"`
	Credentials   *connectors.Credentials `json:"credentials,omitempty"`
}

// Problem is a typed error: the problem+json body of a failed call, or with Type LineError
// the last line of a failed page.
type Problem struct {
	Type        string `json:"type,omitempty"`
	Title       string `json:"title,omitempty"`
	Status      int    `json:"status,omitempty"`
	Detail      string `json:"detail,omitempty"`
	Code        string `json:"code"`
	RetryAfterS int64  `json:"retry_after_s,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

func duration(s int64) time.Duration { return time.Duration(s) * time.Second }

// Descriptor converts a describe response; Remote is always set. The registry validates the rest.
func (w Describe) Descriptor() connectors.Descriptor {
	d := connectors.Descriptor{
		Provider: w.Provider, Name: w.Name, Version: w.Version, Official: w.Official,
		AuthKind: connectors.AuthKind(w.AuthKind), Capabilities: connectors.Capabilities(w.Capabilities), Remote: true,
	}
	for _, s := range w.Streams {
		d.Streams = append(d.Streams, connectors.StreamSpec{
			Name: s.Name, Interval: duration(s.IntervalS), Lookback: duration(s.LookbackS),
			CorrectionEvery: duration(s.CorrectionEveryS), MaxBackfill: duration(s.MaxBackfillS), UnitSize: duration(s.UnitSizeS),
		})
	}
	for _, r := range w.RateLimits {
		d.RateLimits = append(d.RateLimits, connectors.RateLimitSpec{Requests: r.Requests, Per: duration(r.PerS)})
	}
	if w.Upstream != nil {
		d.Upstream = &connectors.Upstream{Package: w.Upstream.Package, Version: w.Upstream.Version, SourceURL: w.Upstream.SourceURL}
	}
	return d
}

// BeginRequest is the begin body for in.
func BeginRequest(in connectors.AuthInput, config json.RawMessage) AuthBeginRequest {
	return AuthBeginRequest{RedirectURL: in.RedirectURL, State: in.State, Config: config}
}

// ContinueRequest is the continue body for in.
func ContinueRequest(in connectors.AuthInput, config json.RawMessage) AuthContinueRequest {
	return AuthContinueRequest{RedirectURL: in.RedirectURL, Callback: in.Callback, Values: in.Values, Session: in.Session, Config: config}
}

// AuthStep converts a step.
func (w Step) AuthStep() connectors.AuthStep {
	s := connectors.AuthStep{RedirectURL: w.RedirectURL, Session: w.Session}
	if w.Prompt != nil {
		s.Prompt = &connectors.AuthPrompt{Message: w.Prompt.Message}
		for _, f := range w.Prompt.Fields {
			s.Prompt.Fields = append(s.Prompt.Fields, connectors.AuthField(f))
		}
	}
	return s
}

// Result converts a begin or continue response.
func (w AuthResponse) Result() connectors.Authorized {
	if w.Step != nil {
		s := w.Step.AuthStep()
		return connectors.Authorized{Next: &s}
	}
	if w.Authorized == nil {
		return connectors.Authorized{}
	}
	return connectors.Authorized{AccountID: w.Authorized.AccountID, Credentials: w.Authorized.Credentials}
}

// FetchRequestOf is the fetch body for one page of u. mode is the planning run's mode.
func FetchRequestOf(u connectors.WorkUnit, mode string, cred connectors.Credentials, config json.RawMessage) FetchRequest {
	return FetchRequest{Stream: u.Stream, Mode: mode, Cursor: u.Cursor, From: u.From, To: u.To, Credentials: cred, Config: config}
}

// FetchResult converts a result line.
func (w ResultLine) FetchResult() connectors.FetchResult {
	return connectors.FetchResult{
		NextCursor: w.NextCursor, HighWatermark: w.HighWatermark, Done: w.Done,
		RetryAfter: duration(w.RetryAfterS), Credentials: w.Credentials,
	}
}

// Item converts a raw line for the RawSink: a JSON body verbatim, or the decoded binary
// after checking its size and SHA-256.
func (w RawLine) Item() (ingest.RawItem, error) {
	it := ingest.RawItem{
		Stream: w.Stream, ExternalKey: w.ExternalKey, ContentType: w.ContentType, FetchedAt: w.FetchedAt, Quarantine: w.Quarantine,
	}
	if w.Request != nil {
		it.Request = ingest.Request(*w.Request)
	}
	switch {
	case w.ExternalKey == "" || w.ContentType == "":
		return it, violation("raw line without external_key or content_type")
	case (w.Body == nil) == (w.BodyBase64 == nil):
		return it, violation("raw line needs exactly one of body and body_base64")
	case w.Body != nil:
		if w.Body[0] != '{' && w.Body[0] != '[' {
			return it, violation("raw line body must be a JSON object or array")
		}
		it.Body = w.Body
		return it, nil
	case len(w.BodyBase64) > MaxBinaryBytes:
		return it, violation("raw line body_base64 over 25 MiB")
	}
	sum := sha256.Sum256(w.BodyBase64)
	if w.SHA256 != hex.EncodeToString(sum[:]) {
		return it, violation("raw line sha256 does not match body_base64")
	}
	it.Body = w.BodyBase64
	return it, nil
}

// Err is the typed error p stands for. Unknown codes (a newer v1 sidecar) are transient.
func (p Problem) Err() error {
	switch p.Code {
	case connectors.ClassRateLimited:
		return &connectors.RateLimitedError{RetryAfter: duration(p.RetryAfterS)}
	case connectors.ClassSchemaDrift:
		return &connectors.SchemaDriftError{Endpoint: p.Endpoint, Fingerprint: p.Fingerprint}
	case connectors.ClassReauthRequired:
		return p.wrap(connectors.ErrReauthRequired)
	case connectors.ClassPermanent:
		return p.wrap(connectors.ErrPermanent)
	}
	return p.wrap(connectors.ErrTransient)
}

func (p Problem) wrap(class error) error {
	if p.Detail == "" {
		return fmt.Errorf("sidecar: %w", class)
	}
	return fmt.Errorf("sidecar: %s: %w", p.Detail, class)
}

// DecodeDescribe parses a describe response. A wrong protocol is a violation.
func DecodeDescribe(b []byte) (connectors.Descriptor, error) {
	var w Describe
	if err := json.Unmarshal(b, &w); err != nil {
		return connectors.Descriptor{}, violation("malformed describe")
	}
	if w.Protocol != Protocol {
		return connectors.Descriptor{}, violation("describe reports another protocol")
	}
	return w.Descriptor(), nil
}

// DecodeAuth parses a begin or continue response. Begin must return a step (Next set).
func DecodeAuth(b []byte) (connectors.Authorized, error) {
	var w AuthResponse
	if err := json.Unmarshal(b, &w); err != nil {
		return connectors.Authorized{}, violation("malformed auth response")
	}
	if (w.Step == nil) == (w.Authorized == nil) {
		return connectors.Authorized{}, violation("auth response needs exactly one of step and authorized")
	}
	if s := w.Step; s != nil {
		switch {
		case (s.RedirectURL == "") == (s.Prompt == nil):
			return connectors.Authorized{}, violation("auth step needs exactly one of redirect_url and prompt")
		case len(s.Session) > MaxSessionBytes:
			return connectors.Authorized{}, violation("auth session over 64 KiB")
		}
	}
	return w.Result(), nil
}

// DecodeProblem is the typed error of a problem+json body; a malformed one is transient.
func DecodeProblem(b []byte) error {
	var p Problem
	if err := json.Unmarshal(b, &p); err != nil || p.Code == "" {
		return violation("malformed problem")
	}
	return p.Err()
}

// Line is one decoded fetch response line; exactly one field is set.
type Line struct {
	Raw    *ingest.RawItem
	Result *connectors.FetchResult
	Err    error // the typed error ending a failed page
}

// DecodeLine parses one NDJSON line of a fetch response.
func DecodeLine(b []byte) (Line, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return Line{}, violation("malformed fetch line")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber() // request params keep their numbers exact
	switch head.Type {
	case LineRaw:
		var w RawLine
		if err := dec.Decode(&w); err != nil {
			return Line{}, violation("malformed raw line")
		}
		it, err := w.Item()
		if err != nil {
			return Line{}, err
		}
		return Line{Raw: &it}, nil
	case LineResult:
		var w ResultLine
		if err := dec.Decode(&w); err != nil || w.RetryAfterS < 0 {
			return Line{}, violation("malformed result line")
		}
		r := w.FetchResult()
		return Line{Result: &r}, nil
	case LineError:
		var p Problem
		if err := dec.Decode(&p); err != nil || p.Code == "" {
			return Line{}, violation("malformed error line")
		}
		return Line{Err: p.Err()}, nil
	}
	return Line{}, violation("unknown fetch line type")
}
