package remote

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/httpx"
	"github.com/KaanEmec/vitamux/internal/ingest"
)

const (
	describeEvery   = time.Minute     // a cached descriptor is re-described on use after this
	describeTimeout = 5 * time.Second // a lazy describe on use; startup passes its own context
)

// Options configures one sidecar.
type Options struct {
	Name   string   // provider code; the sidecar's describe must report it
	URL    *url.URL // base URL; it must resolve to a private address
	Secret string   // shared bearer secret
	Log    *slog.Logger
	// OnDescribe runs after a successful describe whose name or upstream differs from the last
	// one it accepted (always after the first): register the provider, record the upstream.
	// An error makes the next describe call it again.
	OnDescribe func(ctx context.Context, d connectors.Descriptor) error
}

// Connector is a connector served by a sidecar. Its descriptor is the sidecar's describe,
// cached and refreshed on use at most once a minute; until a describe succeeds it is a
// placeholder without streams, which the runtime treats as unavailable (transient failures).
// Plan is core-side, as for in-process connectors; Fetch, Refresh, Begin and Continue call
// the sidecar.
type Connector struct {
	name       string
	cl         *client
	log        *slog.Logger
	onDescribe func(context.Context, connectors.Descriptor) error

	refreshing sync.Mutex // one describe at a time
	mu         sync.Mutex
	d          connectors.Descriptor
	checked    time.Time // last describe attempt
	reported   string    // name and upstream last accepted by onDescribe
}

var (
	_ connectors.Connector     = (*Connector)(nil)
	_ connectors.Authenticator = (*Connector)(nil)
	_ connectors.Interactive   = (*Connector)(nil)
)

// New returns the sidecar's connector with a placeholder descriptor; it does not call the
// sidecar (Discover or the first Describe does).
func New(o Options) *Connector { return newConnector(o, defaultLimits) }

func newConnector(o Options, l limits) *Connector {
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Connector{
		name: o.Name, cl: newClient(o.URL, o.Secret, l), log: log.With("provider", o.Name), onDescribe: o.OnDescribe,
		d: placeholder(o.Name),
	}
}

func placeholder(name string) connectors.Descriptor {
	return connectors.Descriptor{Provider: name, Remote: true}
}

// Describe returns the cached descriptor, describing the sidecar again first when the last
// attempt is a minute old (other callers meanwhile get the cached one).
func (c *Connector) Describe() connectors.Descriptor {
	if c.stale() && c.refreshing.TryLock() {
		if !c.stale() { // another caller just described it
			c.refreshing.Unlock()
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), describeTimeout)
			_ = c.refresh(ctx)
			cancel()
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.d
}

func (c *Connector) stale() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Since(c.checked) >= describeEvery
}

// Discover describes the sidecar now (startup). An error leaves the placeholder; the
// connector stays registered and retries on use.
func (c *Connector) Discover(ctx context.Context) error {
	c.refreshing.Lock()
	return c.refresh(ctx)
}

// refresh describes the sidecar; the caller holds refreshing.
func (c *Connector) refresh(ctx context.Context) error {
	defer c.refreshing.Unlock()
	d, err := c.describe(ctx)
	c.mu.Lock()
	c.checked, c.d = time.Now(), d
	reported := c.reported
	c.mu.Unlock()
	if err != nil {
		c.log.Warn("sidecar unavailable", "err", err)
		return err
	}
	key, _ := json.Marshal([]any{d.Name, d.Upstream})
	if c.onDescribe == nil || string(key) == reported {
		return nil
	}
	if err := c.onDescribe(ctx, d); err != nil {
		c.log.Warn("sidecar registration", "err", err)
		return err
	}
	c.mu.Lock()
	c.reported = string(key)
	c.mu.Unlock()
	return nil
}

// describe fetches and checks the descriptor; on error it returns the placeholder.
func (c *Connector) describe(ctx context.Context) (connectors.Descriptor, error) {
	b, err := c.cl.message(ctx, "/v1/describe", nil)
	if err != nil {
		return placeholder(c.name), err
	}
	d, err := DecodeDescribe(b)
	switch {
	case err != nil:
	case d.Provider != c.name:
		err = violation("describe reports another provider than configured")
	case len(d.Streams) == 0:
		err = violation("describe without streams")
	default:
		err = connectors.Validate(c, d)
	}
	if err != nil {
		return placeholder(c.name), err
	}
	return d, nil
}

func (c *Connector) current() connectors.Descriptor {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.d
}

// Plan returns one unit: the stored cursor for incremental and manual runs, the window for
// correction and backfill. The sidecar pages within it.
func (c *Connector) Plan(_ context.Context, _ connectors.Conn, req connectors.PlanRequest) ([]connectors.WorkUnit, error) {
	switch req.Mode {
	case connectors.ModeIncremental, connectors.ModeManual:
		return []connectors.WorkUnit{{Cursor: req.Cursor}}, nil
	case connectors.ModeCorrection, connectors.ModeBackfill:
		if req.From.IsZero() || !req.To.After(req.From) {
			return nil, fmt.Errorf("%w: %s: %s needs a window", connectors.ErrPermanent, c.name, req.Mode)
		}
		return []connectors.WorkUnit{{From: req.From, To: req.To}}, nil
	}
	return nil, fmt.Errorf("%w: %s: unsupported mode %q", connectors.ErrPermanent, c.name, req.Mode)
}

// Fetch asks the sidecar for one page. The page counts only when its result line arrives: a
// malformed or oversized line, a stream cut short, or a timeout discards every raw line read
// so far (a transient protocol violation). Raw lines before a schema_drift error line go to
// out, which the runtime stores quarantined; before any other error line they are dropped.
func (c *Connector) Fetch(ctx context.Context, conn connectors.Conn, cred connectors.Credentials, u connectors.WorkUnit, out *connectors.RawSink) (connectors.FetchResult, error) {
	return c.fetch(ctx, conn, cred, u, out.Put)
}

// fetch is Fetch with the sink's Put (tests capture it).
func (c *Connector) fetch(ctx context.Context, conn connectors.Conn, cred connectors.Credentials, u connectors.WorkUnit, put func(ingest.RawItem)) (connectors.FetchResult, error) {
	mode := "" // a window unit does not know whether a correction or a backfill planned it
	if u.From.IsZero() && u.To.IsZero() {
		mode = connectors.ModeIncremental
	}
	res, err := c.cl.do(ctx, c.cl.fetch, "/v1/fetch", FetchRequestOf(u, mode, cred, conn.Config))
	if err != nil {
		return connectors.FetchResult{}, err
	}
	defer func() { _ = res.Body.Close() }()
	streams := map[string]bool{}
	for _, s := range c.current().Streams {
		streams[s.Name] = true
	}
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 0, min(64<<10, c.cl.line)), int(c.cl.line)+1) // a larger initial buffer would raise the cap
	var items []ingest.RawItem
	for sc.Scan() {
		l, err := DecodeLine(sc.Bytes())
		switch {
		case err != nil:
			return connectors.FetchResult{}, err
		case l.Raw != nil:
			if l.Raw.Stream != "" && !streams[l.Raw.Stream] {
				return connectors.FetchResult{}, violation("raw line of an undeclared stream")
			}
			items = append(items, *l.Raw)
			continue
		case l.Result != nil && sc.Scan():
			return connectors.FetchResult{}, violation("data after the result line")
		}
		if err := scanErr(sc.Err()); err != nil {
			return connectors.FetchResult{}, err
		}
		var drift *connectors.SchemaDriftError
		if l.Err != nil && !errors.As(l.Err, &drift) {
			return connectors.FetchResult{}, withRetryAfter(l.Err, "")
		}
		for _, it := range items {
			put(it)
		}
		if l.Err != nil {
			return connectors.FetchResult{}, l.Err
		}
		return *l.Result, nil
	}
	if err := scanErr(sc.Err()); err != nil {
		return connectors.FetchResult{}, err
	}
	return connectors.FetchResult{}, violation("page without a result or error line")
}

func scanErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, bufio.ErrTooLong):
		return violation("fetch line over the size limit")
	case errors.Is(err, httpx.ErrResponseTooLarge):
		return violation("fetch page over the size limit")
	}
	return fmt.Errorf("%w: sidecar: reading page: %w", connectors.ErrTransient, err)
}

// Refresh asks the sidecar to refresh the credentials.
func (c *Connector) Refresh(ctx context.Context, conn connectors.Conn, cred connectors.Credentials) (connectors.Credentials, error) {
	b, err := c.cl.message(ctx, "/v1/auth/refresh", RefreshRequest{Credentials: cred, Config: conn.Config})
	if err != nil {
		return connectors.Credentials{}, err
	}
	var w RefreshResponse
	if err := json.Unmarshal(b, &w); err != nil {
		return connectors.Credentials{}, violation("malformed refresh response")
	}
	return w.Credentials, nil
}

// Begin asks the sidecar for the first auth step.
func (c *Connector) Begin(ctx context.Context, in connectors.AuthInput) (connectors.AuthStep, error) {
	b, err := c.cl.message(ctx, "/v1/auth/begin", BeginRequest(in, nil))
	if err != nil {
		return connectors.AuthStep{}, err
	}
	a, err := DecodeAuth(b)
	if err != nil {
		return connectors.AuthStep{}, err
	}
	if a.Next == nil {
		return connectors.AuthStep{}, violation("auth begin without a step")
	}
	return *a.Next, nil
}

// Continue sends the owner's answers or the provider callback with the previous session.
func (c *Connector) Continue(ctx context.Context, conn connectors.Conn, in connectors.AuthInput) (connectors.Authorized, error) {
	b, err := c.cl.message(ctx, "/v1/auth/continue", ContinueRequest(in, conn.Config))
	if err != nil {
		return connectors.Authorized{}, err
	}
	return DecodeAuth(b)
}
