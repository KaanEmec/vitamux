package connectors

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// AppCredentials are the owner's own application at a provider: an OAuth client id and
// secret (ADR-0021). They print as [redacted].
type AppCredentials struct {
	ClientID, ClientSecret string
}

// IsSet reports whether both values are present.
func (a AppCredentials) IsSet() bool { return a.ClientID != "" && a.ClientSecret != "" }

func (AppCredentials) String() string       { return "[redacted]" }
func (AppCredentials) GoString() string     { return "[redacted]" }
func (AppCredentials) LogValue() slog.Value { return slog.StringValue("[redacted]") }

// AppConnector is a connector that runs on the owner's own provider application. It reads
// the application on every call (AppSource), so a change applies without a restart.
type AppConnector interface {
	Connector
	// VerifyApp checks app against the provider without a user grant: nil when it is accepted,
	// ErrAppRejected when refused, ErrAppUnverifiable when the provider has no such check.
	// Other errors are typed as for any provider call.
	VerifyApp(ctx context.Context, h *HTTPClient, app AppCredentials) error
}

var (
	// ErrAppRejected means the provider refused the client id and secret.
	ErrAppRejected = errors.New("connectors: the provider refused the app credentials")
	// ErrAppUnverifiable means the provider cannot check app credentials without a user grant;
	// the first connect checks them.
	ErrAppUnverifiable = errors.New("connectors: the provider cannot check app credentials without a user grant")
)

// AppSource returns a provider's current app credentials; zero when none are set.
type AppSource func(ctx context.Context) (AppCredentials, error)

// StaticApp is an AppSource with fixed values (tests, tools).
func StaticApp(clientID, clientSecret string) AppSource {
	return func(context.Context) (AppCredentials, error) { return AppCredentials{clientID, clientSecret}, nil }
}

// AppStatus is what the panel may know about a provider's app credentials: never the secret.
type AppStatus struct {
	Set                  bool
	ManagedByEnvironment bool
	ClientID             string
	UpdatedAt            *time.Time // nil for environment values
}

// Apps resolves provider app credentials: the environment value (VITAMUX_<PROVIDER>_CLIENT_ID
// and _SECRET) first, then the sealed provider_app_credentials row.
type Apps struct {
	db   *db.DB
	keys *crypto.Keyring // nil: stored values are unavailable
	env  map[string]AppCredentials
}

// NewApps returns the resolver. env holds the complete environment values by provider code.
func NewApps(d *db.DB, keys *crypto.Keyring, env map[string]AppCredentials) *Apps {
	return &Apps{db: d, keys: keys, env: env}
}

// ErrManagedByEnvironment refuses to change a setup value the environment sets (ADR-0021).
var ErrManagedByEnvironment = errors.New("connectors: this value is set by the environment")

// Source returns the AppSource of provider, for its connector.
func (a *Apps) Source(provider string) AppSource {
	return func(ctx context.Context) (AppCredentials, error) { return a.Get(ctx, provider) }
}

// Get returns the provider's app credentials, zero when none are set.
func (a *Apps) Get(ctx context.Context, provider string) (AppCredentials, error) {
	if e, ok := a.env[provider]; ok {
		return e, nil
	}
	row, err := a.db.Q().GetProviderApp(ctx, provider)
	if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
		return AppCredentials{}, nil
	} else if err != nil {
		return AppCredentials{}, err
	}
	if a.keys == nil {
		return AppCredentials{}, errors.New("app credentials: the master key is not loaded")
	}
	secret, err := a.keys.Open(crypto.Credentials, row.Ciphertext, crypto.ProviderAppAAD(provider))
	if err != nil {
		return AppCredentials{}, fmt.Errorf("app credentials of %s: %w", provider, err)
	}
	return AppCredentials{ClientID: row.ClientID, ClientSecret: string(secret)}, nil
}

// Status reports the provider's app credentials without the secret.
func (a *Apps) Status(ctx context.Context, provider string) (AppStatus, error) {
	if e, ok := a.env[provider]; ok {
		return AppStatus{Set: true, ManagedByEnvironment: true, ClientID: e.ClientID}, nil
	}
	row, err := a.db.Q().GetProviderApp(ctx, provider)
	if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
		return AppStatus{}, nil
	} else if err != nil {
		return AppStatus{}, err
	}
	return AppStatus{Set: true, ClientID: row.ClientID, UpdatedAt: &row.UpdatedAt}, nil
}

// Put seals and stores the provider's app credentials, audited as provider_app.set (without
// values). It reports whether a stored value was replaced.
func (a *Apps) Put(ctx context.Context, provider string, app AppCredentials, userID uuid.UUID, actor string) (bool, error) {
	if _, ok := a.env[provider]; ok {
		return false, ErrManagedByEnvironment
	}
	if a.keys == nil {
		return false, errors.New("app credentials: the master key is not loaded")
	}
	sealed, err := a.keys.Seal(crypto.Credentials, []byte(app.ClientSecret), crypto.ProviderAppAAD(provider))
	if err != nil {
		return false, err
	}
	var replaced bool
	err = a.db.Tx(ctx, func(q *dbq.Queries) error {
		if replaced, err = q.UpsertProviderApp(ctx, dbq.UpsertProviderAppParams{
			Provider: provider, ClientID: app.ClientID, Ciphertext: sealed, KeyID: a.keys.KeyID(), UpdatedBy: &userID,
		}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &userID, Actor: actor, Action: "provider_app.set",
			TargetType: "provider", TargetID: provider, Detail: map[string]any{"replaced": replaced}})
	})
	return replaced, err
}

// InUseError refuses to remove setup values that connections still use.
type InUseError struct{ Connections int }

func (e *InUseError) Error() string {
	return fmt.Sprintf("%d connections use it", e.Connections)
}

// Delete removes the stored app credentials, audited as provider_app.delete. Unless force,
// it refuses (*InUseError) while connections hold tokens. db.ErrNotFound when none are stored.
func (a *Apps) Delete(ctx context.Context, provider string, force bool, userID uuid.UUID, actor string) error {
	if _, ok := a.env[provider]; ok {
		return ErrManagedByEnvironment
	}
	return a.db.Tx(ctx, func(q *dbq.Queries) error {
		deleted, err := q.DeleteProviderApp(ctx, provider)
		if err != nil {
			return err
		}
		if deleted == 0 {
			return db.ErrNotFound
		}
		n, err := q.CountAuthorizedConnections(ctx, provider)
		if err != nil {
			return err
		}
		if n > 0 && !force {
			return &InUseError{Connections: int(n)} // rolls the delete back
		}
		return audit.Record(ctx, q, audit.Event{UserID: &userID, Actor: actor, Action: "provider_app.delete",
			TargetType: "provider", TargetID: provider, Detail: map[string]any{"connections": n}})
	})
}

// VerifyApp checks the provider's current app credentials (Apps.Get) with its connector and
// audits the outcome as provider_app.verify. The result is "valid", "invalid" or
// "unverifiable"; a provider that could not be reached is an error (typed as for a sync).
func (rt *Runtime) VerifyApp(ctx context.Context, apps *Apps, provider string, userID uuid.UUID, actor string) (string, error) {
	c, ok := rt.reg.Get(provider)
	ac, isApp := c.(AppConnector)
	if !ok || !isApp {
		return "", ErrAuthUnavailable
	}
	app, err := apps.Get(ctx, provider)
	if err != nil {
		return "", err
	}
	if !app.IsSet() {
		return "", db.ErrNotFound
	}
	result := "valid"
	switch err := ac.VerifyApp(ctx, rt.clients.get(c.Describe()), app); {
	case errors.Is(err, ErrAppRejected):
		result = "invalid"
	case errors.Is(err, ErrAppUnverifiable):
		result = "unverifiable"
	case err != nil:
		return "", err
	}
	return result, rt.db.Tx(ctx, func(q *dbq.Queries) error {
		return audit.Record(ctx, q, audit.Event{UserID: &userID, Actor: actor, Action: "provider_app.verify",
			TargetType: "provider", TargetID: provider, Detail: map[string]any{"result": result}})
	})
}
