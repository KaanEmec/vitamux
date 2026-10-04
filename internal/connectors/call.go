package connectors

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

// HasProvider reports whether a connector is registered for provider.
func (rt *Runtime) HasProvider(provider string) bool {
	_, ok := rt.reg.Get(provider)
	return ok
}

// OnAuthorized registers fn to run after every completed authorization (connect, reconnect,
// reauthorize), once its transaction has committed; e.g. to subscribe provider notifications.
// fn handles its own errors. Call it before serving.
func (rt *Runtime) OnAuthorized(fn func(ctx context.Context, connectionID uuid.UUID, provider string)) {
	rt.afterAuth = fn
}

// WithCredentials runs fn with an active connection's rate-limited client and current
// credentials, for provider calls outside a sync (notification subscriptions). A refused access
// token is refreshed once (single-flight) and fn retried. Unlike a sync it records nothing on
// the connection: a lasting refusal comes back as ErrReauthRequired for the next sync to settle.
func (rt *Runtime) WithCredentials(ctx context.Context, connectionID uuid.UUID, fn func(Conn, Credentials) error) error {
	row, err := rt.db.Q().GetSyncConnection(ctx, connectionID)
	if err = db.MapErr(err); err != nil {
		return err
	}
	if row.Status != "active" && row.Status != "degraded" {
		return &classError{"connection_inactive", "connection is " + row.Status}
	}
	c, ok := rt.reg.Get(row.Provider)
	if !ok {
		return fmt.Errorf("%w: no connector registered for %s", ErrPermanent, row.Provider)
	}
	d := c.Describe()
	conn := Conn{ID: row.ID, UserID: row.UserID, Provider: row.Provider, Config: row.Config, HTTP: rt.clients.get(d)}
	if !d.AuthKind.needsRefresh() {
		return fn(conn, Credentials{})
	}
	auth := c.(Authenticator) // checked by NewRegistry
	cred, version, err := rt.creds.current(ctx, conn, auth)
	if err != nil {
		return err
	}
	if err = fn(conn, cred); !errors.Is(err, ErrReauthRequired) {
		return err
	}
	if cred, _, err = rt.creds.refresh(ctx, conn, auth, version, true); err != nil {
		return err
	}
	return fn(conn, cred)
}

// Describe returns the descriptor of provider's connector; false when none is registered.
func (rt *Runtime) Describe(provider string) (Descriptor, bool) {
	c, ok := rt.reg.Get(provider)
	if !ok {
		return Descriptor{}, false
	}
	return c.Describe(), true
}

// Providers returns every registered connector's descriptor, by provider code.
func (rt *Runtime) Providers() []Descriptor { return rt.reg.Descriptors() }

// ErrNotSyncable means the server cannot sync the connection now: it is paused, disabled or in
// error, or no connector runs its provider (push connections). The text is safe to show.
var ErrNotSyncable = errors.New("connection cannot be synced")

// SyncNow queues a manual sync of every stream of the connection and returns the job ids. A
// stream whose manual sync is still queued or running gets that job back, so repeated requests
// queue nothing new. Errors: db.ErrNotFound, ErrReauthRequired, ErrNotSyncable.
func (rt *Runtime) SyncNow(ctx context.Context, connectionID uuid.UUID) ([]uuid.UUID, error) {
	row, err := rt.db.Q().GetSyncConnection(ctx, connectionID)
	if err = db.MapErr(err); err != nil {
		return nil, err
	}
	switch row.Status {
	case "active", "degraded":
	case "needs_reauth":
		return nil, ErrReauthRequired
	default:
		return nil, fmt.Errorf("%w: it is %s", ErrNotSyncable, row.Status)
	}
	c, ok := rt.reg.Get(row.Provider)
	if !ok {
		return nil, fmt.Errorf("%w: its client pushes the data", ErrNotSyncable)
	}
	var ids []uuid.UUID
	now := time.Now()
	err = rt.db.Tx(ctx, func(q *dbq.Queries) error {
		ids = ids[:0]
		for _, s := range c.Describe().Streams {
			id, _, err := jobs.Enqueue(ctx, q, jobs.NewJob{
				Kind: jobs.KindSync, ConnectionID: &row.ID, Exclusive: true, Priority: jobs.PriorityHigh,
				DedupeKey: "manual:" + row.ID.String() + ":" + s.Name,
				Payload:   jobs.SyncPayload{Stream: s.Name, Mode: ModeManual, Slot: now},
			})
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	return ids, err
}
