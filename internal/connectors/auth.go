package connectors

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

// StateTTL bounds how long the owner may take at the provider's consent page.
const StateTTL = 10 * time.Minute

var (
	// ErrAuthUnavailable means the provider has no interactive authorization here: unknown,
	// not interactive, or not configured (no client id and secret, no public URL).
	ErrAuthUnavailable = errors.New("connectors: authorization is not available for this provider")
	// ErrAuthDenied means the owner declined at the provider; the state is used up.
	ErrAuthDenied = errors.New("connectors: authorization was declined")
	// ErrAccountMismatch means a reauthorization signed in to another provider account than
	// the connection's; nothing was changed.
	ErrAccountMismatch = errors.New("connectors: reauthorized with a different provider account")
)

// Interactive is the authorization bootstrap of a connector whose AuthKind needs one; for
// AuthOAuth2 it is the authorization-code flow. Refresh stays in Authenticator.
type Interactive interface {
	// Begin returns where to send the owner's browser. It must not call the provider and
	// returns ErrAuthUnavailable when the connector is not configured.
	Begin(ctx context.Context, in AuthInput) (AuthStep, error)
	// Continue completes the flow from the provider's callback: exchange the code, return the
	// credentials and the provider account id. A callback without a grant is ErrAuthDenied.
	Continue(ctx context.Context, c Conn, in AuthInput) (Authorized, error)
}

// AuthInput is one step's input.
type AuthInput struct {
	RedirectURL string            // the provider callback, ${VITAMUX_PUBLIC_URL}/oauth/<provider>/callback
	State       string            // Begin: the signed state to round-trip
	Callback    url.Values        // Continue: the callback's query
	Values      map[string]string // Continue: the owner's answers to a prompt; never stored or logged
	Session     []byte            // Continue: the previous step's Session
}

// AuthStep is what the owner does next: follow RedirectURL or answer Prompt. Session is the
// connector's opaque continuation; the core seals it and never shows it to the browser.
type AuthStep struct {
	RedirectURL string
	Prompt      *AuthPrompt
	Session     []byte
}

// AuthPrompt asks the owner for input, e.g. credentials or an MFA code.
type AuthPrompt struct {
	Message string
	Fields  []AuthField
}

// AuthField is one prompt input. Kind is text, password or code.
type AuthField struct {
	Name, Label string
	Kind        string
}

// Authorized is a completed authorization, or the next step when Next is set (AccountID and
// Credentials are then ignored).
type Authorized struct {
	AccountID   string // provider account id; only its SHA-256 is stored (connections.account_key)
	Credentials Credentials
	Next        *AuthStep
}

// AuthRequest starts an authorization for the owner session SessionID.
type AuthRequest struct {
	UserID, SessionID uuid.UUID
	Provider          string     // provider to connect; ignored with ConnectionID
	ConnectionID      *uuid.UUID // reauthorize this connection; nil connects (or reconnects by account)
	Binding           string     // random per-browser value the callback must present again
}

func (rt *Runtime) interactive(provider string) (Interactive, Descriptor, error) {
	c, ok := rt.reg.Get(provider)
	if !ok {
		return nil, Descriptor{}, ErrAuthUnavailable
	}
	ia, ok := c.(Interactive)
	if !ok || rt.publicURL == nil || rt.creds.keys == nil {
		return nil, Descriptor{}, ErrAuthUnavailable
	}
	return ia, c.Describe(), nil
}

func (rt *Runtime) callbackURL(provider string) string {
	return rt.publicURL.JoinPath("oauth", provider, "callback").String()
}

func (rt *Runtime) signer() (*StateSigner, error) {
	k, err := rt.creds.keys.PurposeKey(crypto.SessionSigning)
	if err != nil {
		return nil, err
	}
	return NewStateSigner(k), nil
}

// BeginAuth records a pending authorization and returns the provider URL to send the browser
// to. A ConnectionID that is not the user's is db.ErrNotFound.
func (rt *Runtime) BeginAuth(ctx context.Context, in AuthRequest) (string, error) {
	if in.Binding == "" {
		return "", errors.New("connectors: begin auth without a browser binding")
	}
	if in.ConnectionID != nil {
		row, err := rt.db.Q().GetSyncConnection(ctx, *in.ConnectionID)
		if err = db.MapErr(err); err != nil {
			return "", err
		}
		if row.UserID != in.UserID {
			return "", db.ErrNotFound
		}
		in.Provider = row.Provider
	}
	ia, _, err := rt.interactive(in.Provider)
	if err != nil {
		return "", err
	}
	s, err := rt.signer()
	if err != nil {
		return "", err
	}
	id := uuid.New()
	step, err := ia.Begin(ctx, AuthInput{RedirectURL: rt.callbackURL(in.Provider), State: s.Sign(id, in.Binding)})
	if err != nil {
		return "", err
	}
	q := rt.db.Q()
	if err := q.DeleteExpiredOAuthStates(ctx); err != nil {
		return "", db.MapErr(err)
	}
	err = q.InsertOAuthState(ctx, dbq.InsertOAuthStateParams{
		ID: id, UserID: in.UserID, SessionID: in.SessionID, ConnectionID: in.ConnectionID,
		ExpiresAt: time.Now().Add(StateTTL), Provider: in.Provider,
	})
	if err != nil {
		return "", db.MapErr(err)
	}
	return step.RedirectURL, nil
}

// CompleteAuth finishes the authorization the callback's state names: the state is consumed
// first (single use, whatever happens next), then the connector exchanges the grant. A new
// account gets a connection; a known account (reconnect) or the reauthorized connection gets
// fresh credentials and becomes active again, so one account never has two connections. The
// connection's default schedules are ensured and a first sync is queued.
func (rt *Runtime) CompleteAuth(ctx context.Context, provider, state, binding string, callback url.Values) (uuid.UUID, error) {
	ia, d, err := rt.interactive(provider)
	if err != nil {
		return uuid.Nil, err
	}
	s, err := rt.signer()
	if err != nil {
		return uuid.Nil, err
	}
	sid, err := s.Verify(state, binding)
	if err != nil {
		return uuid.Nil, err
	}
	st, err := rt.db.Q().ConsumeOAuthState(ctx, dbq.ConsumeOAuthStateParams{ID: sid, Provider: provider})
	if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
		return uuid.Nil, ErrAuthState
	} else if err != nil {
		return uuid.Nil, err
	}
	conn := Conn{UserID: st.UserID, Provider: provider, HTTP: rt.clients.get(d)}
	if st.ConnectionID != nil {
		conn.ID = *st.ConnectionID
	}
	res, err := ia.Continue(ctx, conn, AuthInput{RedirectURL: rt.callbackURL(provider), Callback: callback})
	if err != nil {
		return uuid.Nil, err
	}
	if res.AccountID == "" {
		return uuid.Nil, fmt.Errorf("%w: provider returned no account id", ErrPermanent)
	}
	sum := sha256.Sum256([]byte(res.AccountID))
	key := sum[:]
	user, action := st.UserID, "connection.reauthorized"
	err = rt.db.Tx(ctx, func(q *dbq.Queries) error {
		if st.ConnectionID != nil {
			old, err := q.GetConnectionAccount(ctx, dbq.GetConnectionAccountParams{ID: conn.ID, UserID: user, Provider: provider})
			if err != nil {
				return err
			}
			if old != nil && !bytes.Equal(old, key) {
				return ErrAccountMismatch
			}
			if err := q.ReauthorizeConnection(ctx, dbq.ReauthorizeConnectionParams{AccountKey: key, ID: conn.ID}); err != nil {
				return err
			}
		} else {
			row, err := q.UpsertOAuthConnection(ctx, dbq.UpsertOAuthConnectionParams{ID: uuid.New(), UserID: user, AccountKey: key, Provider: provider})
			if err != nil {
				return err
			}
			conn.ID = row.ID
			if row.Created {
				action = "connection.connected"
			}
		}
		if err := rt.creds.save(ctx, q, conn.ID, res.Credentials); err != nil {
			return err
		}
		if err := EnsureSchedules(ctx, q, conn.ID, d); err != nil {
			return err
		}
		if err := rt.enqueueFirstSync(ctx, q, conn.ID, d); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &user, Actor: audit.Owner, Action: action,
			TargetType: "connection", TargetID: conn.ID.String(), Detail: map[string]any{"provider": provider}})
	})
	if err != nil {
		return uuid.Nil, err
	}
	rt.log.Info("connection authorized", "connection_id", conn.ID, "provider", provider, "action", action)
	if rt.afterAuth != nil {
		rt.afterAuth(ctx, conn.ID, provider)
	}
	return conn.ID, nil
}

// enqueueFirstSync queues a manual sync per scheduled stream, so data arrives without waiting
// a full schedule interval.
func (rt *Runtime) enqueueFirstSync(ctx context.Context, q *dbq.Queries, id uuid.UUID, d Descriptor) error {
	now := time.Now()
	for _, s := range d.Streams {
		if s.Interval == 0 {
			continue
		}
		if _, _, err := jobs.Enqueue(ctx, q, jobs.NewJob{
			Kind: jobs.KindSync, ConnectionID: &id, Exclusive: true, Priority: jobs.PriorityHigh,
			DedupeKey: "connect:" + id.String() + ":" + s.Name,
			Payload:   jobs.SyncPayload{Stream: s.Name, Mode: ModeManual, Slot: now},
		}); err != nil {
			return err
		}
	}
	return nil
}

// Disconnect disables the user's connection and deletes its credentials. Its data, cursors
// and schedules stay (schedules of a disabled connection do not run); connecting the same
// account again reuses the connection. The provider-side grant is left to the owner.
// actor names the caller in the audit event (auth.Principal.Actor).
func (rt *Runtime) Disconnect(ctx context.Context, userID, connectionID uuid.UUID, actor string) error {
	return rt.db.Tx(ctx, func(q *dbq.Queries) error {
		n, err := q.DisableConnection(ctx, dbq.DisableConnectionParams{ID: connectionID, UserID: userID})
		if err != nil {
			return err
		}
		if n == 0 {
			return db.ErrNotFound
		}
		if err := q.DeleteCredentials(ctx, connectionID); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &userID, Actor: actor, Action: "connection.disconnected",
			TargetType: "connection", TargetID: connectionID.String()})
	})
}
