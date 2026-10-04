// Package example is a toy connector for a fictional heart-rate API, built from docs/adapters.md
// alone to show the walkthrough end to end. The connector is registered only in its own tests,
// never in cmd/vitamux, and has no providers row outside them. Only its Normalizer ships, for the
// Python example sidecar's stream (examples/sidecar-python).
package example

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/ingest"
)

const (
	Provider = "example"
	Stream   = "example.heart_rate"

	endpoint = "GET /v1/heart-rate"
	maxBody  = 8 << 20
)

// epoch is updated_since of the first sync.
var epoch = time.Unix(0, 0).UTC()

// Config is the connector's setup.
type Config struct {
	BaseURL string // e.g. "https://api.example.com", no trailing slash
}

// Connector syncs the heart-rate feed.
type Connector struct{ base string }

// New returns a Connector.
func New(cfg Config) *Connector {
	return &Connector{base: strings.TrimRight(cfg.BaseURL, "/")}
}

// Describe declares one hourly incremental stream without correction or backfill.
func (*Connector) Describe() connectors.Descriptor {
	return connectors.Descriptor{
		Provider: Provider, Version: "1", Official: true, AuthKind: connectors.AuthNone,
		Streams:      []connectors.StreamSpec{{Name: Stream, Interval: time.Hour}},
		RateLimits:   []connectors.RateLimitSpec{{Requests: 60, Per: time.Minute}},
		Capabilities: connectors.Capabilities{Incremental: true, ManualSync: true},
	}
}

// cursor is both the stream cursor and the page cursor. Since is what the stream resumes from;
// Page and Next (the first page's server time) exist only while paging.
type cursor struct {
	Since time.Time  `json:"since"`
	Page  string     `json:"page,omitempty"`
	Next  *time.Time `json:"next,omitempty"`
}

// Plan returns one unit that pages from the stored cursor.
func (*Connector) Plan(_ context.Context, _ connectors.Conn, req connectors.PlanRequest) ([]connectors.WorkUnit, error) {
	if req.Mode != connectors.ModeIncremental && req.Mode != connectors.ModeManual {
		return nil, fmt.Errorf("%w: example: mode %s not supported", connectors.ErrPermanent, req.Mode)
	}
	if _, err := readCursor(req.Cursor); err != nil {
		return nil, err
	}
	return []connectors.WorkUnit{{Stream: req.Stream, Cursor: req.Cursor}}, nil
}

func readCursor(raw json.RawMessage) (cursor, error) {
	c := cursor{Since: epoch}
	if raw == nil {
		return c, nil
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return cursor{}, fmt.Errorf("%w: example: unreadable cursor", connectors.ErrPermanent)
	}
	return c, nil
}

// Fetch fetches one page.
func (cn *Connector) Fetch(ctx context.Context, conn connectors.Conn, _ connectors.Credentials, u connectors.WorkUnit, out *connectors.RawSink) (connectors.FetchResult, error) {
	c, err := readCursor(u.Cursor)
	if err != nil {
		return connectors.FetchResult{}, err
	}
	params := url.Values{"updated_since": {c.Since.UTC().Format(time.RFC3339)}}
	if c.Page != "" {
		params.Set("page", c.Page)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cn.base+"/v1/heart-rate?"+params.Encode(), nil)
	if err != nil {
		return connectors.FetchResult{}, fmt.Errorf("%w: example: %w", connectors.ErrPermanent, err)
	}
	res, err := conn.HTTP.Do(req)
	if err != nil {
		return connectors.FetchResult{}, err // RateLimitedError, or a network error (transient)
	}
	defer func() { _ = res.Body.Close() }()
	switch {
	case res.StatusCode >= 500:
		return connectors.FetchResult{}, fmt.Errorf("%w: example: HTTP %d", connectors.ErrTransient, res.StatusCode)
	case res.StatusCode != http.StatusOK:
		return connectors.FetchResult{}, fmt.Errorf("%w: example: HTTP %d", connectors.ErrPermanent, res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1)) // one byte past the limit detects oversize
	if err != nil {
		return connectors.FetchResult{}, fmt.Errorf("%w: example: reading body: %w", connectors.ErrTransient, err)
	}
	if len(body) > maxBody {
		return connectors.FetchResult{}, fmt.Errorf("%w: example: body over %d bytes", connectors.ErrPermanent, maxBody)
	}
	request := ingest.Request{Endpoint: endpoint, Params: map[string]any{}}
	for k := range params {
		request.Params[k] = params.Get(k)
	}

	p, err := decodePage(body)
	if err != nil {
		out.Put(ingest.RawItem{
			ExternalKey: "page:" + params.Encode(), ContentType: "application/json", Body: body, Request: request,
		})
		fp, _ := ingest.ShapeFingerprint(body)
		return connectors.FetchResult{}, &connectors.SchemaDriftError{Endpoint: endpoint, Fingerprint: fp}
	}
	var hw time.Time
	for _, s := range p.samples {
		out.Put(ingest.RawItem{ExternalKey: "sample:" + s.id, ContentType: "application/json", Body: s.body, Request: request})
		if s.time.After(hw) {
			hw = s.time
		}
	}

	next := c.Next
	if next == nil {
		next = &p.serverTime // first page of this run
	}
	if p.nextPage != "" {
		nc, err := json.Marshal(cursor{Since: c.Since, Page: p.nextPage, Next: next})
		return connectors.FetchResult{NextCursor: nc, HighWatermark: hw}, err
	}
	nc, err := json.Marshal(cursor{Since: *next})
	return connectors.FetchResult{NextCursor: nc, HighWatermark: hw, Done: true}, err
}

// page is a decoded response; samples keep their bytes as received.
type page struct {
	serverTime time.Time
	samples    []sample
	nextPage   string
}

type sample struct {
	id   string
	time time.Time
	body json.RawMessage
}

// errDrift means the response no longer has the shape decodePage knows.
var errDrift = errors.New("example: unexpected response shape")

// decodePage checks the fields the connector relies on: server_time, samples, next_page, and
// each sample's id and time. bpm and device are the normalizer's business.
func decodePage(body []byte) (page, error) {
	var raw struct {
		ServerTime *time.Time         `json:"server_time"`
		Samples    *[]json.RawMessage `json:"samples"`
		NextPage   *string            `json:"next_page"`
	}
	if err := json.Unmarshal(body, &raw); err != nil || raw.ServerTime == nil || raw.Samples == nil {
		return page{}, errDrift
	}
	p := page{serverTime: *raw.ServerTime}
	if raw.NextPage != nil {
		p.nextPage = *raw.NextPage
	}
	for _, b := range *raw.Samples {
		var s struct {
			ID   *string    `json:"id"`
			Time *time.Time `json:"time"`
		}
		if err := json.Unmarshal(b, &s); err != nil || s.ID == nil || *s.ID == "" || s.Time == nil {
			return page{}, errDrift
		}
		p.samples = append(p.samples, sample{id: *s.ID, time: *s.Time, body: b})
	}
	return p, nil
}
