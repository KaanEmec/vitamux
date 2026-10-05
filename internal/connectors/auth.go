package connectors

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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

// TicketTTL bounds the start ticket of an app redirect step: the app opens it at once.
const TicketTTL = 2 * time.Minute

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

// Interactive is the authorization bootstrap of a connector whose AuthKind needs one: an
// OAuth authorization-code flow, prompts the owner answers in the UI (credentials, then an MFA
// code), or a mix. Refresh stays in Authenticator.
type Interactive interface {
	// Begin returns the first step. It must not call the provider and returns
	// ErrAuthUnavailable when the connector is not configured.
	Begin(ctx context.Context, in AuthInput) (AuthStep, error)
	// Continue takes the provider's callback or the owner's answers to a prompt, with the
	// Session of the step that led here. It returns the credentials and the provider account
	// id, or Next for one more step. A callback without a grant is ErrAuthDenied.
	Continue(ctx context.Context, c Conn, in AuthInput) (Authorized, error)
}

// AuthInput is one step's input.
type AuthInput struct {
	RedirectURL string            // the provider callback, ${VITAMUX_PUBLIC_URL}/oauth/<provider>/callback
	State       string            // signed state of the step to return; a redirect must round-trip it
	Callback    url.Values        // Continue after a redirect: the callback's query
	Values      map[string]string // Continue after a prompt: the owner's answers; never stored or logged
	Session     []byte            // Continue: the Session of the previous step
}

// AuthStep is what the owner does next: open RedirectURL, or answer Prompt.
type AuthStep struct {
	RedirectURL string
	Prompt      *AuthPrompt
	// Session is the connector's opaque continuation (e.g. a PKCE verifier or a login
	// session). The runtime seals it into the state row; it never reaches the browser.
	Session []byte
}

// AuthPrompt asks the owner for values, e.g. a username and password, then an MFA code.
type AuthPrompt struct {
	Message string
	Fields  []AuthField
}

// AuthField is one prompted value; Kind is FieldText, FieldPassword or FieldCode.
type AuthField struct {
	Name, Label, Kind string
}

// Prompt field kinds.
const (
	FieldText     = "text"
	FieldPassword = "password"
	FieldCode     = "code"
)

const maxAuthSession = 64 << 10

// check refuses a step the UI cannot show or the state row cannot hold.
func (s AuthStep) check() error {
	bad := func(msg string) error { return fmt.Errorf("%w: auth step: %s", ErrPermanent, msg) }
	switch {
	case (s.RedirectURL == "") == (s.Prompt == nil):
		return bad("needs a redirect URL or a prompt, not both")
	case len(s.Session) > maxAuthSession:
		return bad("session is too large")
	case s.Prompt != nil && len(s.Prompt.Fields) == 0:
		return bad("prompt has no fields")
	}
	if s.Prompt != nil {
		seen := map[string]bool{}
		for _, f := range s.Prompt.Fields {
			if !providerRe.MatchString(f.Name) || seen[f.Name] || f.Label == "" ||
				(f.Kind != FieldText && f.Kind != FieldPassword && f.Kind != FieldCode) {
				return bad(fmt.Sprintf("invalid prompt field %q", f.Name))
			}
			seen[f.Name] = true
		}
	}
	return nil
}

// Authorized is a completed authorization, or, with Next, one more step.
type Authorized struct {
	AccountID   string // provider account id; only its SHA-256 is stored (connections.account_key)
	Credentials Credentials
	Next        *AuthStep // set: not done yet, show this step; AccountID and Credentials are ignored
}

// AuthRequest starts an authorization for the owner session SessionID.
type AuthRequest struct {
	UserID, SessionID uuid.UUID
	Provider          string     // provider to connect; ignored with ConnectionID
	ConnectionID      *uuid.UUID // reauthorize this connection; nil connects (or reconnects by account)
	Binding           string     // random per-browser value every later step must present again
	// App returns to the app: a redirect step's URL becomes a single-use start ticket on this
	// server (StartAuth), and the callback reports the state as app-originated.
	App bool
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

func (rt *Runtime) startURL(provider, ticket string) string {
	u := rt.publicURL.JoinPath("oauth", provider, "start")
	u.RawQuery = url.Values{"ticket": {ticket}}.Encode()
	return u.String()
}

func (rt *Runtime) signer() (*StateSigner, error) {
	k, err := rt.creds.keys.PurposeKey(crypto.SessionSigning)
	if err != nil {
		return nil, err
	}
	return NewStateSigner(k), nil
}

// pending is who a state row authorizes what for, and where the callback returns.
type pending struct {
	user, session uuid.UUID
	conn          *uuid.UUID
	provider      string
	app           bool
	binding       string // sealed into an app redirect step's row for the start route
}

const (
	returnBrowser = "browser"
	returnApp     = "app"
)

// saveStep checks step, seals its Session into a new state row id and clears it from step.
// An app redirect step keeps the provider URL and the sealed binding on the row and answers a
// start URL with a single-use ticket (only its SHA-256 is stored) instead.
func (rt *Runtime) saveStep(ctx context.Context, p pending, id uuid.UUID, step *AuthStep) error {
	if err := step.check(); err != nil {
		return err
	}
	var sealed []byte
	if step.Session != nil {
		var err error
		if sealed, err = rt.creds.keys.Seal(crypto.Credentials, step.Session, crypto.AuthSessionAAD(id)); err != nil {
			return err
		}
		step.Session = nil
	}
	row := dbq.InsertOAuthStateParams{
		ID: id, UserID: p.user, SessionID: p.session, ConnectionID: p.conn,
		ExpiresAt: time.Now().Add(StateTTL), Session: sealed, Provider: p.provider, ReturnTo: returnBrowser,
	}
	if p.app {
		row.ReturnTo = returnApp
	}
	if p.app && step.RedirectURL != "" {
		var b [32]byte
		_, _ = rand.Read(b[:])
		ticket := base64.RawURLEncoding.EncodeToString(b[:])
		sum := sha256.Sum256([]byte(ticket))
		binding, err := rt.creds.keys.Seal(crypto.Credentials, []byte(p.binding), crypto.AuthBindingAAD(id))
		if err != nil {
			return err
		}
		consent := step.RedirectURL
		row.TicketHash, row.StartUrl, row.Binding = sum[:], &consent, binding
		step.RedirectURL = rt.startURL(p.provider, ticket)
	}
	q := rt.db.Q()
	if err := q.DeleteExpiredOAuthStates(ctx); err != nil {
		return db.MapErr(err)
	}
	return db.MapErr(q.InsertOAuthState(ctx, row))
}

// BeginAuth records a pending authorization and returns its first step (a provider URL to
// send the browser to, or a prompt) and the state that continues it. A ConnectionID that is not
// the user's is db.ErrNotFound.
func (rt *Runtime) BeginAuth(ctx context.Context, in AuthRequest) (AuthStep, string, error) {
	if in.Binding == "" {
		return AuthStep{}, "", errors.New("connectors: begin auth without a browser binding")
	}
	if in.ConnectionID != nil {
		row, err := rt.db.Q().GetSyncConnection(ctx, *in.ConnectionID)
		if err = db.MapErr(err); err != nil {
			return AuthStep{}, "", err
		}
		if row.UserID != in.UserID {
			return AuthStep{}, "", db.ErrNotFound
		}
		in.Provider = row.Provider
	}
	ia, _, err := rt.interactive(in.Provider)
	if err != nil {
		return AuthStep{}, "", err
	}
	s, err := rt.signer()
	if err != nil {
		return AuthStep{}, "", err
	}
	id := uuid.New()
	state := s.Sign(id, in.Binding)
	step, err := ia.Begin(ctx, AuthInput{RedirectURL: rt.callbackURL(in.Provider), State: state})
	if err != nil {
		return AuthStep{}, "", err
	}
	p := pending{user: in.UserID, session: in.SessionID, conn: in.ConnectionID, provider: in.Provider, app: in.App, binding: in.Binding}
	if err := rt.saveStep(ctx, p, id, &step); err != nil {
		return AuthStep{}, "", err
	}
	return step, state, nil
}

// StartAuth uses the start ticket of an app redirect step (single use, TicketTTL). It returns
// the provider URL to send the auth browser to and the binding to set there as cookie; any
// unknown, used, expired or foreign ticket is ErrAuthState.
func (rt *Runtime) StartAuth(ctx context.Context, provider, ticket string) (redirectURL, binding string, err error) {
	if _, _, err := rt.interactive(provider); err != nil {
		return "", "", err
	}
	if ticket == "" {
		return "", "", ErrAuthState
	}
	sum := sha256.Sum256([]byte(ticket))
	row, err := rt.db.Q().UseOAuthTicket(ctx, dbq.UseOAuthTicketParams{TicketHash: sum[:], Provider: provider, IssuedAfter: time.Now().Add(-TicketTTL)})
	if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
		return "", "", ErrAuthState
	} else if err != nil {
		return "", "", err
	}
	b, err := rt.creds.keys.Open(crypto.Credentials, row.Binding, crypto.AuthBindingAAD(row.ID))
	if err != nil {
		return "", "", ErrAuthState // e.g. the master key changed: start over
	}
	return row.StartUrl, string(b), nil
}

// ContinueAuth answers the prompt the state names with the owner's values. It returns the next
// step and its state, or, when the authorization is complete, the connection (as CompleteAuth).
func (rt *Runtime) ContinueAuth(ctx context.Context, provider, state, binding string, values map[string]string) (AuthStep, string, uuid.UUID, error) {
	step, next, id, _, err := rt.advance(ctx, provider, state, binding, AuthInput{Values: values}, true)
	return step, next, id, err
}

// CompleteAuth finishes the authorization the callback's state names. A new account gets a
// connection; a known account (reconnect) or the reauthorized connection gets fresh
// credentials and becomes active again, so one account never has two connections. The
// connection's default schedules are ensured and a first sync is queued, except for a new
// connection of an unofficial connector: it starts paused until the owner resumes it. app
// reports a state begun with AuthRequest.App, also with an error once the state was found.
func (rt *Runtime) CompleteAuth(ctx context.Context, provider, state, binding string, callback url.Values) (id uuid.UUID, app bool, err error) {
	_, _, id, app, err = rt.advance(ctx, provider, state, binding, AuthInput{Callback: callback}, false)
	return id, app, err
}

// advance runs one Continue: the state is consumed first (single use, whatever happens next)
// and its sealed session handed back to the connector. A further step gets a new state row;
// allowNext false (the provider callback, which can only redirect to the UI) refuses one.
// app is the consumed row's return target (false until a row is found).
func (rt *Runtime) advance(ctx context.Context, provider, state, binding string, in AuthInput, allowNext bool) (AuthStep, string, uuid.UUID, bool, error) {
	ia, d, err := rt.interactive(provider)
	if err != nil {
		return AuthStep{}, "", uuid.Nil, false, err
	}
	s, err := rt.signer()
	if err != nil {
		return AuthStep{}, "", uuid.Nil, false, err
	}
	sid, err := s.Verify(state, binding)
	if err != nil {
		return AuthStep{}, "", uuid.Nil, false, err
	}
	st, err := rt.db.Q().ConsumeOAuthState(ctx, dbq.ConsumeOAuthStateParams{ID: sid, Provider: provider})
	if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
		return AuthStep{}, "", uuid.Nil, false, ErrAuthState
	} else if err != nil {
		return AuthStep{}, "", uuid.Nil, false, err
	}
	app := st.ReturnTo == returnApp
	if st.Session != nil {
		if in.Session, err = rt.creds.keys.Open(crypto.Credentials, st.Session, crypto.AuthSessionAAD(sid)); err != nil {
			return AuthStep{}, "", uuid.Nil, app, ErrAuthState // e.g. the master key changed: start over
		}
	}
	conn := Conn{UserID: st.UserID, Provider: provider, HTTP: rt.clients.get(d)}
	if st.ConnectionID != nil {
		conn.ID = *st.ConnectionID
	}
	next := uuid.New()
	in.RedirectURL, in.State = rt.callbackURL(provider), s.Sign(next, binding)
	res, err := ia.Continue(ctx, conn, in)
	switch {
	case err != nil:
		return AuthStep{}, "", uuid.Nil, app, err
	case res.Next != nil && !allowNext:
		return AuthStep{}, "", uuid.Nil, app, fmt.Errorf("%w: another step after the provider callback", ErrPermanent)
	case res.Next != nil:
		p := pending{user: st.UserID, session: st.SessionID, conn: st.ConnectionID, provider: provider, app: app, binding: binding}
		if err := rt.saveStep(ctx, p, next, res.Next); err != nil {
			return AuthStep{}, "", uuid.Nil, app, err
		}
		return *res.Next, in.State, uuid.Nil, app, nil
	}
	id, err := rt.finalize(ctx, d, conn, st.ConnectionID != nil, res)
	return AuthStep{}, "", id, app, err
}

// finalize stores a completed authorization: see CompleteAuth.
func (rt *Runtime) finalize(ctx context.Context, d Descriptor, conn Conn, reauth bool, res Authorized) (uuid.UUID, error) {
	if res.AccountID == "" {
		return uuid.Nil, fmt.Errorf("%w: provider returned no account id", ErrPermanent)
	}
	sum := sha256.Sum256([]byte(res.AccountID))
	key := sum[:]
	var upstream []byte
	if d.Upstream != nil {
		var err error
		if upstream, err = json.Marshal(d.Upstream); err != nil {
			return uuid.Nil, err
		}
	}
	mode, status := "in_process", "active"
	if d.Remote {
		mode = "remote"
	}
	if !d.Official {
		status = "paused"
	}
	user, provider, action := conn.UserID, conn.Provider, "connection.reauthorized"
	err := rt.db.Tx(ctx, func(q *dbq.Queries) error {
		firstSync := true
		if reauth {
			old, err := q.GetConnectionAccount(ctx, dbq.GetConnectionAccountParams{ID: conn.ID, UserID: user, Provider: provider})
			if err != nil {
				return err
			}
			if old != nil && !bytes.Equal(old, key) {
				return ErrAccountMismatch
			}
			if err := q.ReauthorizeConnection(ctx, dbq.ReauthorizeConnectionParams{AccountKey: key, Upstream: upstream, ID: conn.ID}); err != nil {
				return err
			}
		} else {
			row, err := q.UpsertOAuthConnection(ctx, dbq.UpsertOAuthConnectionParams{
				ID: uuid.New(), UserID: user, AccountKey: key, Mode: mode, Status: status, Upstream: upstream, Provider: provider,
			})
			if err != nil {
				return err
			}
			conn.ID = row.ID
			if row.Created {
				action, firstSync = "connection.connected", status == "active"
			}
		}
		if err := rt.creds.save(ctx, q, conn.ID, res.Credentials); err != nil {
			return err
		}
		if err := EnsureSchedules(ctx, q, conn.ID, d); err != nil {
			return err
		}
		if firstSync {
			if err := rt.enqueueFirstSync(ctx, q, conn.ID, d); err != nil {
				return err
			}
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
