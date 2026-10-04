// Package withings is the official Withings connector: OAuth 2.0 connection, the
// withings.measures stream and its normalizer. Verified API facts: docs/providers/withings.md.
package withings

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/KaanEmec/vitamux/internal/connectors"
)

const (
	Provider       = "withings"
	StreamMeasures = "withings.measures"

	defaultAuthURL = "https://account.withings.com/oauth2_user/authorize2"
	defaultAPIURL  = "https://wbsapi.withings.net"
	scope          = "user.metrics"
	maxBody        = 32 << 20 // a getmeas page of years of groups stays far below this
)

// Config is the self-hoster's Withings application. App is read on every authorization,
// exchange, refresh and verify (connectors.Apps: environment first, then the panel value);
// without it ClientID and ClientSecret are used (tests). AuthURL and APIURL default to
// Withings; tests point them at a fake.
type Config struct {
	App                    connectors.AppSource
	ClientID, ClientSecret string
	AuthURL, APIURL        string
}

// Connector implements connectors.Connector, Authenticator and Interactive for Withings.
type Connector struct {
	cfg Config
}

// New returns the connector. Without a client id and secret it still syncs nothing and
// refuses to start an authorization (connectors.ErrAuthUnavailable).
func New(cfg Config) *Connector {
	if cfg.App == nil {
		cfg.App = connectors.StaticApp(cfg.ClientID, cfg.ClientSecret)
	}
	if cfg.AuthURL == "" {
		cfg.AuthURL = defaultAuthURL
	}
	if cfg.APIURL == "" {
		cfg.APIURL = defaultAPIURL
	}
	cfg.APIURL = strings.TrimRight(cfg.APIURL, "/")
	return &Connector{cfg: cfg}
}

// Describe declares the measures stream: hourly lastupdate polling, a daily 7-day correction
// window, 30-day backfill units, and the 120 requests per minute of the standard plan.
func (*Connector) Describe() connectors.Descriptor {
	return connectors.Descriptor{
		Provider: Provider, Name: "Withings", Version: "1", Official: true, AuthKind: connectors.AuthOAuth2,
		Streams: []connectors.StreamSpec{{
			Name: StreamMeasures, Interval: time.Hour, Lookback: 7 * 24 * time.Hour,
			MaxBackfill: 20 * 365 * 24 * time.Hour, UnitSize: 30 * 24 * time.Hour,
		}},
		RateLimits:   []connectors.RateLimitSpec{{Requests: 120, Per: time.Minute}},
		Capabilities: connectors.Capabilities{Incremental: true, Backfill: true, Webhooks: true, ManualSync: true},
	}
}

// Withings answers HTTP 200 with a status in the body (docs/providers/withings.md#oauth-20).
type envelope struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
	Error  string          `json:"error"`

	http int // HTTP status when it was neither 200 nor typed by post (4xx other than 401 and 429)
}

// statusError maps a non-zero body status to a typed error. Statuses are numbers, never data.
func statusError(endpoint string, status int) error {
	switch status {
	case 401, 342, 343:
		return fmt.Errorf("withings %s: status %d: %w", endpoint, status, connectors.ErrReauthRequired)
	case 601:
		return &connectors.RateLimitedError{RetryAfter: time.Minute}
	case 2554, 2555:
		return fmt.Errorf("withings %s: status %d: %w", endpoint, status, connectors.ErrTransient)
	}
	return fmt.Errorf("withings %s: status %d: %w", endpoint, status, connectors.ErrPermanent)
}

// post sends a form to path and returns the envelope. HTTP-level failures are typed here;
// the body status is left to the caller. bearer may be empty (token endpoint).
func (w *Connector) post(ctx context.Context, h *connectors.HTTPClient, path, bearer string, form url.Values) (envelope, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.cfg.APIURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return envelope{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := h.Do(req)
	if err != nil {
		return envelope{}, err // rate limits are typed by HTTPClient; network errors count as transient
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return envelope{}, fmt.Errorf("withings %s: read: %w", path, connectors.ErrTransient)
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return envelope{}, fmt.Errorf("withings %s: HTTP 401: %w", path, connectors.ErrReauthRequired)
	case res.StatusCode >= 500:
		return envelope{}, fmt.Errorf("withings %s: HTTP %d: %w", path, res.StatusCode, connectors.ErrTransient)
	case res.StatusCode != http.StatusOK:
		return envelope{http: res.StatusCode}, nil // the caller decides (token endpoint: refused grant)
	}
	var env envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return envelope{}, fmt.Errorf("withings %s: undecodable response: %w", path, connectors.ErrTransient)
	}
	return env, nil
}
