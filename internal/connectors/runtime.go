package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/httpx"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/jobs"
	"github.com/KaanEmec/vitamux/internal/metrics"
)

// Config wires a Runtime.
type Config struct {
	DB       *db.DB
	Blobs    *blob.Store
	Keys     *crypto.Keyring
	Registry *Registry
	HTTP     *httpx.Client // base provider client; nil = httpx defaults
	Log      *slog.Logger
	// PublicURL is VITAMUX_PUBLIC_URL, the base of OAuth callbacks; nil disables interactive auth.
	PublicURL *url.URL
}

// Runtime runs sync jobs for registered connectors: credentials, rate limits, raw-first
// commits with cursor advance, and the typed-error state machine.
type Runtime struct {
	db      *db.DB
	blobs   *blob.Store
	reg     *Registry
	creds   credStore
	clients providerClients
	log     *slog.Logger

	publicURL *url.URL
	afterAuth func(ctx context.Context, connectionID uuid.UUID, provider string) // OnAuthorized

	beforeCommit func() error // tests: fail a commit after its raw rows and cursor were written
}

// New returns a Runtime; register Handle for jobs.KindSync.
func New(cfg Config) *Runtime {
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	base := cfg.HTTP
	if base == nil {
		base = httpx.New(httpx.Options{Logger: log})
	}
	reg := cfg.Registry
	if reg == nil {
		reg = &Registry{}
	}
	return &Runtime{
		db: cfg.DB, blobs: cfg.Blobs, reg: reg, log: log, publicURL: cfg.PublicURL,
		creds:   credStore{db: cfg.DB, keys: cfg.Keys},
		clients: providerClients{base: base, m: map[string]*HTTPClient{}},
	}
}

// SaveCredentials seals c and stores it as the connection's credentials (auth bootstrap,
// reconnect). Pass the Queries of the transaction that creates or updates the connection.
func (rt *Runtime) SaveCredentials(ctx context.Context, q *dbq.Queries, connectionID uuid.UUID, c Credentials) error {
	return rt.creds.save(ctx, q, connectionID, c)
}

// syncRun is one job's connection and connector.
type syncRun struct {
	conn Conn
	c    Connector
	auth Authenticator // nil when the auth kind keeps no provider tokens
}

// checkpoint is the progress of a run that leaves the stream cursor alone (correction).
type checkpoint struct {
	Unit   int             `json:"unit"`
	Cursor json.RawMessage `json:"cursor,omitempty"`
}

// Handle is the jobs.KindSync handler for jobs.SyncPayload.
func (rt *Runtime) Handle(ctx context.Context, j jobs.Job) error {
	var p jobs.SyncPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil || j.ConnectionID == nil {
		return jobs.Permanent(errors.New("sync job: invalid payload or no connection"))
	}
	switch p.Mode {
	case ModeIncremental, ModeCorrection, ModeManual:
	default:
		return jobs.Permanent(fmt.Errorf("sync job: unsupported mode %q", p.Mode))
	}
	r, err := rt.prepare(ctx, *j.ConnectionID, p.Stream)
	if errors.Is(err, errStreamGone) && p.ScheduleID != uuid.Nil {
		// The connector dropped the stream (e.g. a sidecar update): retire it instead of
		// failing the connection.
		rt.log.Info("stream retired: no longer declared", "connection_id", *j.ConnectionID, "stream", p.Stream)
		return rt.db.Q().RetireStream(ctx, dbq.RetireStreamParams{ConnectionID: *j.ConnectionID, Stream: p.Stream})
	}
	if err != nil {
		if r.c != nil { // an unreachable sidecar counts as a failed sync
			return rt.settle(ctx, r, p.Stream, err)
		}
		return err
	}
	// A block set before a restart or by another process: no provider call until it ends.
	if until, err := rt.blockedUntil(ctx, r); err != nil {
		return err
	} else if !until.IsZero() {
		return jobs.RescheduleAt(until, &RateLimitedError{RetryAfter: time.Until(until)})
	}
	req := PlanRequest{Mode: p.Mode, Stream: p.Stream, To: p.Slot}
	if p.From != nil {
		req.From = *p.From
	}
	if req.To.IsZero() {
		req.To = time.Now()
	}
	resume, err := rt.run(ctx, j, r, req, nil)
	if err == nil && !resume.IsZero() {
		return jobs.RescheduleAt(resume, nil)
	}
	return rt.settle(ctx, r, p.Stream, err)
}

// errStreamGone: a sync names a stream its connector no longer declares.
var errStreamGone = errors.New("stream no longer declared")

func (rt *Runtime) prepare(ctx context.Context, connectionID uuid.UUID, stream string) (syncRun, error) {
	row, err := rt.db.Q().GetSyncConnection(ctx, connectionID)
	if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
		return syncRun{}, jobs.Permanent(err)
	} else if err != nil {
		return syncRun{}, err
	}
	if row.Status != "active" && row.Status != "degraded" {
		return syncRun{}, jobs.Permanent(&classError{"connection_inactive", "connection is " + row.Status})
	}
	c, ok := rt.reg.Get(row.Provider)
	if !ok {
		return syncRun{}, jobs.Permanent(fmt.Errorf("%w: no connector registered for %s", ErrPermanent, row.Provider))
	}
	d := c.Describe()
	r := syncRun{
		conn: Conn{ID: row.ID, UserID: row.UserID, Provider: row.Provider, Config: row.Config, HTTP: rt.clients.get(d)},
		c:    c,
	}
	if !d.Available() {
		return r, fmt.Errorf("%w: the %s sidecar is unavailable", ErrTransient, row.Provider)
	}
	if _, ok := d.stream(stream); !ok {
		return syncRun{}, jobs.Permanent(fmt.Errorf("%w: %w: %s has no stream %q", ErrPermanent, errStreamGone, row.Provider, stream))
	}
	if d.AuthKind.needsRefresh() {
		r.auth = c.(Authenticator) // checked by NewRegistry
	}
	return r, nil
}

// run plans the sync and fetches every unit page by page. A non-zero resume means a page
// asked for a pause and the rest should run then. final, if set, runs in the transaction that
// commits the last page (a backfill unit marks itself done there).
func (rt *Runtime) run(ctx context.Context, j jobs.Job, r syncRun, req PlanRequest, final func(*dbq.Queries) error) (resume time.Time, err error) {
	cur, err := rt.db.Q().GetSyncCursor(ctx, dbq.GetSyncCursorParams{ConnectionID: r.conn.ID, Stream: req.Stream})
	switch err = db.MapErr(err); {
	case err == nil:
		req.Cursor = cur.Cursor
		if cur.HighWatermark != nil {
			req.HighWatermark = *cur.HighWatermark
		}
	case !errors.Is(err, db.ErrNotFound):
		return time.Time{}, err
	}
	units, err := r.c.Plan(ctx, r.conn, req)
	if err != nil {
		return time.Time{}, err
	}
	if len(units) == 0 && final != nil {
		return time.Time{}, rt.db.Tx(ctx, final)
	}
	advance := advancesCursor(req.Mode)
	var cp checkpoint // an incremental run resumes from the stream cursor instead
	if !advance && j.Checkpoint != nil {
		_ = json.Unmarshal(j.Checkpoint, &cp) // unreadable: start over, re-fetches are no-ops
	}
	for i := cp.Unit; i < len(units); i++ {
		u := units[i]
		if u.Stream == "" {
			u.Stream = req.Stream
		} else if u.Stream != req.Stream {
			return time.Time{}, fmt.Errorf("%w: plan returned a unit of stream %s", ErrPermanent, u.Stream)
		}
		if i == cp.Unit && cp.Cursor != nil {
			u.Cursor = cp.Cursor
		}
		var fin func(*dbq.Queries) error
		if i == len(units)-1 {
			fin = final
		}
		for {
			res, err := rt.page(ctx, r, u, advance, fin)
			if err != nil {
				return time.Time{}, err
			}
			u.Cursor = res.NextCursor
			more := !res.Done || i+1 < len(units)
			if !advance && more {
				next := checkpoint{Unit: i, Cursor: res.NextCursor}
				if res.Done {
					next = checkpoint{Unit: i + 1}
				}
				if err := j.SaveCheckpoint(ctx, next); err != nil {
					return time.Time{}, err
				}
			}
			if res.RetryAfter > 0 {
				until, err := rt.block(ctx, r, res.RetryAfter)
				if err != nil || more {
					return until, err
				}
			}
			if res.Done {
				break
			}
		}
	}
	return time.Time{}, nil
}

// page fetches one page of u and commits it, with final when it completes u. A refused access
// token is refreshed once (single-flight) and the page retried; a second refusal stands.
func (rt *Runtime) page(ctx context.Context, r syncRun, u WorkUnit, advance bool, final func(*dbq.Queries) error) (FetchResult, error) {
	var cred Credentials
	var version int32
	if r.auth != nil {
		var err error
		if cred, version, err = rt.creds.current(ctx, r.conn, r.auth); err != nil {
			return FetchResult{}, err
		}
	}
	for refreshed := false; ; refreshed = true {
		sink := &RawSink{stream: u.Stream, fetchedAt: time.Now()}
		res, err := r.c.Fetch(ctx, r.conn, cred, u, sink)
		var drift *SchemaDriftError
		switch {
		case errors.Is(err, ErrReauthRequired) && r.auth != nil && !refreshed:
			if cred, version, err = rt.creds.refresh(ctx, r.conn, r.auth, version, true); err != nil {
				return FetchResult{}, err
			}
			continue
		case errors.As(err, &drift):
			// Keep what arrived, quarantined, and never let it move the cursor.
			for i := range sink.items {
				sink.items[i].Quarantine = true
			}
			if cerr := rt.commit(ctx, r.conn, u.Stream, sink.items, nil, nil, nil); cerr != nil {
				return FetchResult{}, cerr
			}
			return FetchResult{}, err
		case err != nil:
			return FetchResult{}, err
		case !res.Done && res.NextCursor == nil:
			return FetchResult{}, fmt.Errorf("%w: page is not done but has no next cursor", ErrPermanent)
		}
		var adv *FetchResult
		if advance {
			adv = &res
		}
		if !res.Done {
			final = nil
		}
		return res, rt.commit(ctx, r.conn, u.Stream, sink.items, adv, res.Credentials, final)
	}
}

// commit stores items, enqueues their normalization and, given adv, advances the stream
// cursor in the same transaction, so a cursor never moves past records that are not stored.
// rotated (credentials the provider rotated during the fetch) and final, if set, are stored in
// that transaction too. A crash leaves at most orphan blob files for the sweeper.
func (rt *Runtime) commit(ctx context.Context, c Conn, stream string, items []ingest.RawItem, adv *FetchResult, rotated *Credentials, final func(*dbq.Queries) error) error {
	var stored []ingest.Result
	err := rt.db.Tx(ctx, func(q *dbq.Queries) error {
		if rotated != nil {
			if err := rt.creds.save(ctx, q, c.ID, *rotated); err != nil {
				return err
			}
		}
		if len(items) > 0 {
			b, err := ingest.CreateBatch(ctx, q, ingest.BatchInfo{UserID: c.UserID, ConnectionID: c.ID, SourceKind: ingest.SourceSync})
			if err != nil {
				return err
			}
			if stored, err = ingest.StoreRaw(ctx, q, rt.blobs, b, items); err != nil {
				return err
			}
			fresh := false // quarantined rows wait for release instead of normalization
			for i, r := range stored {
				fresh = fresh || (r.Outcome != ingest.Duplicate && !items[i].Quarantine)
			}
			if fresh {
				if _, _, err := jobs.Enqueue(ctx, q, jobs.NewJob{
					Kind: ingest.KindNormalizeBatch, ConnectionID: &c.ID,
					DedupeKey: ingest.KindNormalizeBatch + ":" + b.ID.String(), Payload: ingest.NormalizePayload{BatchID: b.ID},
				}); err != nil {
					return err
				}
			}
		}
		if adv != nil && (adv.NextCursor != nil || !adv.HighWatermark.IsZero()) {
			var hw *time.Time
			if !adv.HighWatermark.IsZero() {
				hw = &adv.HighWatermark
			}
			if err := q.AdvanceSyncCursor(ctx, dbq.AdvanceSyncCursorParams{
				ConnectionID: c.ID, Stream: stream, Cursor: adv.NextCursor, HighWatermark: hw,
			}); err != nil {
				return err
			}
		}
		if final != nil {
			if err := final(q); err != nil {
				return err
			}
		}
		if rt.beforeCommit != nil {
			return rt.beforeCommit()
		}
		return nil
	})
	if err == nil {
		metrics.SyncPages.WithLabelValues(c.Provider, stream).Inc()
		for _, r := range stored {
			metrics.RawItems.WithLabelValues(c.Provider, stream, string(r.Outcome)).Inc()
		}
	}
	return err
}

// settle applies connectors.md#typed-errors to the connection and stream and returns what
// the job runner should do next.
func (rt *Runtime) settle(ctx context.Context, r syncRun, stream string, err error) error {
	if err != nil && (ctx.Err() != nil || errors.Is(err, jobs.ErrLeaseLost)) {
		return err // shutdown or another worker took over; nothing to record
	}
	id := r.conn.ID
	if err == nil {
		return rt.db.Tx(ctx, func(q *dbq.Queries) error {
			if err := q.SetStreamStatus(ctx, dbq.SetStreamStatusParams{ConnectionID: id, Stream: stream, Status: "ok"}); err != nil {
				return err
			}
			return q.RecordSyncSuccess(ctx, id)
		})
	}
	var (
		rl       *RateLimitedError
		drift    *SchemaDriftError
		status   string // "" keeps the connection status
		class    string
		failures int32 = 1
		out      error
	)
	switch {
	case errors.As(err, &rl):
		until, berr := rt.block(ctx, r, rl.RetryAfter)
		if berr != nil {
			return berr
		}
		class, failures, out = ClassRateLimited, 0, jobs.RescheduleAt(until, err)
	case errors.Is(err, ErrReauthRequired):
		status, class, out = "needs_reauth", ClassReauthRequired, jobs.Permanent(err)
	case errors.As(err, &drift):
		status, class, out = "degraded", ClassSchemaDrift, jobs.Permanent(err)
	case errors.Is(err, ErrPermanent):
		status, class, out = "error", ClassPermanent, jobs.Permanent(err)
	default:
		class, out = ClassTransient, err
		if !errors.Is(err, ErrTransient) {
			out = fmt.Errorf("%w: %w", ErrTransient, err)
		}
	}
	terr := rt.db.Tx(ctx, func(q *dbq.Queries) error {
		if drift != nil {
			reason := ClassSchemaDrift
			if err := q.SetStreamStatus(ctx, dbq.SetStreamStatusParams{
				ConnectionID: id, Stream: stream, Status: "degraded", StatusReason: &reason,
			}); err != nil {
				return err
			}
		}
		p := dbq.RecordSyncFailureParams{ErrorClass: &class, Failures: failures, ID: id}
		if status != "" {
			p.Status = &status
		}
		return q.RecordSyncFailure(ctx, p)
	})
	if terr != nil {
		return terr
	}
	if status != "" {
		rt.log.Warn("connection sync failed", "connection_id", id, "provider", r.conn.Provider,
			"stream", stream, "error_class", class, "status", status)
	}
	return out
}

// blockedUntil returns the provider's shared block if it lies in the future.
func (rt *Runtime) blockedUntil(ctx context.Context, r syncRun) (time.Time, error) {
	until, err := rt.db.Q().GetProviderBlock(ctx, r.conn.Provider)
	if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
		return time.Time{}, nil
	} else if err != nil {
		return time.Time{}, err
	}
	if !until.After(time.Now()) {
		return time.Time{}, nil
	}
	r.conn.HTTP.block(until)
	return until, nil
}

// block pauses the provider for d, in this process and, through provider_rate_state, in
// every process and after restarts. It returns the (possibly later) effective end.
func (rt *Runtime) block(ctx context.Context, r syncRun, d time.Duration) (time.Time, error) {
	until, err := rt.db.Q().BlockProvider(ctx, dbq.BlockProviderParams{BlockedUntil: time.Now().Add(d), Provider: r.conn.Provider})
	if err = db.MapErr(err); err != nil {
		return time.Time{}, err
	}
	r.conn.HTTP.block(until)
	metrics.ProviderBlocks.WithLabelValues(r.conn.Provider).Inc()
	return until, nil
}
