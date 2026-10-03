package connectors

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db"
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
