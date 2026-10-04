package connectors

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

// AuthKind is how a connection obtains credentials.
type AuthKind string

const (
	AuthNone           AuthKind = "none"
	AuthOAuth2         AuthKind = "oauth2"          // needs an Authenticator for refresh
	AuthInteractiveMFA AuthKind = "interactive_mfa" // needs an Authenticator for refresh
	AuthDevicePairing  AuthKind = "device_pairing"  // push clients; no provider tokens
)

// needsRefresh reports whether the kind keeps refreshable provider tokens.
func (k AuthKind) needsRefresh() bool { return k == AuthOAuth2 || k == AuthInteractiveMFA }

// Plan modes. Only incremental and manual runs advance the stream cursor; correction and
// backfill runs re-fetch a window and leave it alone.
const (
	ModeIncremental = jobs.ModeIncremental
	ModeCorrection  = jobs.ModeCorrection
	ModeManual      = "manual"
	ModeBackfill    = "backfill"
)

func advancesCursor(mode string) bool { return mode == ModeIncremental || mode == ModeManual }

// Descriptor is what a connector declares about itself; the registry validates it. A Remote
// descriptor without streams is the placeholder of a sidecar that has not described itself yet
// (unreachable): it is registered but not Available.
type Descriptor struct {
	Provider     string // providers.code, e.g. "withings"
	Name         string // display name (providers.name of a sidecar); empty shows the code
	Version      string
	Official     bool      // false ⇒ the UI shows an "unofficial API" warning; new connections start paused
	Remote       bool      // served by a sidecar (connection mode 'remote'); set by the core, never by a sidecar
	Upstream     *Upstream // the third-party package a sidecar wraps; nil for first-party connectors
	AuthKind     AuthKind
	Streams      []StreamSpec
	RateLimits   []RateLimitSpec // all apply at once, per provider
	Capabilities Capabilities
}

// Upstream identifies the third-party package behind a sidecar (connections.upstream).
type Upstream struct {
	Package   string `json:"package"`
	Version   string `json:"version"`
	SourceURL string `json:"source_url"`
}

// Available reports whether the connector can serve calls: false for a sidecar placeholder.
func (d Descriptor) Available() bool { return !d.Remote || len(d.Streams) > 0 }

// StreamSpec is one stream's defaults. EnsureSchedules turns them into schedules.
type StreamSpec struct {
	Name            string        // e.g. "withings.measures"
	Interval        time.Duration // incremental schedule; 0 = not scheduled
	Lookback        time.Duration // correction window; 0 = no correction schedule
	CorrectionEvery time.Duration // how often the correction window is re-fetched; default 24h
	MaxBackfill     time.Duration // how far back a backfill may reach
	UnitSize        time.Duration // backfill unit length
}

// RateLimitSpec allows Requests per Per, with bursts up to Requests.
type RateLimitSpec struct {
	Requests int
	Per      time.Duration
}

// Capabilities are the optional features a connector supports.
type Capabilities struct {
	Incremental, Backfill, Webhooks, ManualSync bool
}

// Connector fetches one provider's data. Plan must not call the provider; Fetch fetches one
// page of a unit and puts the verbatim records into out. Errors are the typed errors of
// errors.go; anything else counts as transient.
type Connector interface {
	Describe() Descriptor
	Plan(ctx context.Context, c Conn, req PlanRequest) ([]WorkUnit, error)
	Fetch(ctx context.Context, c Conn, cred Credentials, u WorkUnit, out *RawSink) (FetchResult, error)
}

// Authenticator refreshes provider credentials. Connectors whose AuthKind keeps tokens
// implement it. Refresh maps a refused refresh token to ErrReauthRequired. The interactive
// bootstrap is Interactive (auth.go).
type Authenticator interface {
	Refresh(ctx context.Context, c Conn, cred Credentials) (Credentials, error)
}

// Conn is the connection a connector works for.
type Conn struct {
	ID, UserID uuid.UUID
	Provider   string
	Config     json.RawMessage
	HTTP       *HTTPClient // rate-limited client for this provider; use it for every provider call
}

// PlanRequest asks a connector to split a sync into work units.
type PlanRequest struct {
	Mode          string
	Stream        string
	Cursor        json.RawMessage // stored stream cursor; nil before the first sync
	HighWatermark time.Time       // zero before the first sync
	From, To      time.Time       // window for correction and backfill; To is the run's slot
}

// WorkUnit is one bounded piece of a sync. Between pages the runtime replaces Cursor with
// the NextCursor of the previous FetchResult.
type WorkUnit struct {
	Stream   string
	Cursor   json.RawMessage
	From, To time.Time
}

// FetchResult reports one page. NextCursor nil keeps the cursor. RetryAfter asks the runtime
// to pause the provider (e.g. a soft quota) after storing this page. Credentials, when the
// provider rotated them during the fetch, are stored in the page's transaction, before the next
// page or anything else uses them.
type FetchResult struct {
	NextCursor    json.RawMessage
	HighWatermark time.Time // newest source time seen; zero = unknown
	Done          bool      // the unit is complete
	RetryAfter    time.Duration
	Credentials   *Credentials
}

// Credentials are a connection's decrypted provider tokens. They print as [redacted].
type Credentials struct {
	AccessToken  string          `json:"access_token,omitempty"`
	RefreshToken string          `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time       `json:"expires_at,omitzero"` // zero = does not expire
	Extra        json.RawMessage `json:"extra,omitempty"`     // provider-specific session state
}

func (Credentials) String() string       { return "[redacted]" }
func (Credentials) GoString() string     { return "[redacted]" }
func (Credentials) LogValue() slog.Value { return slog.StringValue("[redacted]") }

// RawSink collects the verbatim records of one page. The runtime stores them together with
// the cursor advance, in one transaction.
type RawSink struct {
	stream    string
	fetchedAt time.Time
	items     []ingest.RawItem
}

// Put adds one record. Stream and FetchedAt default to the unit's stream and the page start.
func (s *RawSink) Put(it ingest.RawItem) {
	if it.Stream == "" {
		it.Stream = s.stream
	}
	if it.FetchedAt.IsZero() {
		it.FetchedAt = s.fetchedAt
	}
	s.items = append(s.items, it)
}
