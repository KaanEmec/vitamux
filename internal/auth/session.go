package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

const (
	// SessionCookie holds the raw session token; the sessions row keeps only its SHA-256.
	SessionCookie = "vitamux_session"
	// SessionIdle ends a session that has not been used for this long.
	SessionIdle = 12 * time.Hour
	// SessionLifetime ends every session this long after login, however active.
	SessionLifetime = 7 * 24 * time.Hour
	// AppSessionIdle and AppSessionLifetime are the defaults for app sessions
	// (VITAMUX_APP_SESSION_IDLE and VITAMUX_APP_SESSION_MAX).
	AppSessionIdle     = 30 * 24 * time.Hour
	AppSessionLifetime = 90 * 24 * time.Hour
	// MaxDeviceNameLen bounds the device name an app session is listed under.
	MaxDeviceNameLen = 100

	sessionTokenLen = 32
	csrfLabel       = "vitamux/csrf/v1\x00"
)

var (
	// ErrInvalidCredentials is the one answer to a bad username, password or code.
	ErrInvalidCredentials = errors.New("auth: invalid username, password or code")
	// ErrTOTPRequired means the password was right but TOTP is enabled and no code was sent.
	ErrTOTPRequired = errors.New("auth: TOTP code required")
)

// ThrottledError refuses a login attempt while its username or address is locked out.
type ThrottledError struct{ RetryAfter time.Duration }

func (e *ThrottledError) Error() string {
	return fmt.Sprintf("auth: too many failed logins; retry in %s", e.RetryAfter.Round(time.Second))
}

// Service holds owner sessions, TOTP and machine tokens. It needs the master keyring for
// the CSRF key and TOTP secrets, so without a key there is no Service and auth fails closed.
type Service struct {
	db       *db.DB
	keys     *crypto.Keyring
	csrfKey  []byte
	throttle *throttle
	now      func() time.Time
	// appIdle and appLifetime are the app session timeouts.
	appIdle, appLifetime time.Duration
}

// New returns a Service over d using keys for CSRF tokens and sealed TOTP secrets.
func New(d *db.DB, keys *crypto.Keyring) (*Service, error) {
	k, err := keys.PurposeKey(crypto.SessionSigning)
	if err != nil {
		return nil, err
	}
	return &Service{db: d, keys: keys, csrfKey: k, throttle: newThrottle(), now: time.Now,
		appIdle: AppSessionIdle, appLifetime: AppSessionLifetime}, nil
}

// SetAppSessionLifetimes replaces the app session idle and absolute timeouts; call it before
// serving. Non-positive values keep the current ones.
func (s *Service) SetAppSessionLifetimes(idle, lifetime time.Duration) {
	if idle > 0 {
		s.appIdle = idle
	}
	if lifetime > 0 {
		s.appLifetime = lifetime
	}
}

// Login is one login attempt.
type Login struct {
	Username, Password string
	// TOTPCode or RecoveryCode is required when the owner has TOTP enabled.
	TOTPCode, RecoveryCode string
	ClientIP               netip.Addr
	// PreviousToken is the session cookie the request carried, if any; it is deleted so
	// a login always starts a fresh session. Ignored for app logins.
	PreviousToken string
	// DeviceName, when set, makes this an app login: the session is kind app, listed under
	// this name, and its token is a vmx_ses_ bearer token instead of a cookie value.
	DeviceName string
}

// NewSession is a session just created by Login.
type NewSession struct {
	// Token is the cookie value, or for an app session the vmx_ses_ bearer token.
	Token     string
	UserID    uuid.UUID
	ExpiresAt time.Time
}

// Login checks the credentials and creates a session. It returns ErrInvalidCredentials,
// ErrTOTPRequired or a *ThrottledError for refused attempts.
func (s *Service) Login(ctx context.Context, in Login) (*NewSession, error) {
	keys := throttleKeys(in.Username, in.ClientIP)
	if wait := s.throttle.Wait(keys...); wait > 0 {
		return nil, &ThrottledError{RetryAfter: wait}
	}
	user, err := s.db.Q().GetUserByUsername(ctx, in.Username)
	if err != nil {
		if err = db.MapErr(err); !errors.Is(err, db.ErrNotFound) {
			return nil, err
		}
		_, _ = VerifyPassword(dummyHash(), in.Password) // same cost as a known user
		return nil, s.loginFailed(ctx, keys, nil, "bad_credentials", in.ClientIP)
	}
	ok, err := VerifyPassword(user.PasswordHash, in.Password)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, s.loginFailed(ctx, keys, &user.ID, "bad_credentials", in.ClientIP)
	}
	method := "password"
	if user.TotpEnabledAt != nil {
		switch {
		case in.TOTPCode != "":
			method = "totp"
			ok, err = s.useTOTP(ctx, user, in.TOTPCode)
		case in.RecoveryCode != "":
			method = "recovery_code"
			ok, err = s.useRecoveryCode(ctx, user.ID, in.RecoveryCode)
		default:
			return nil, ErrTOTPRequired // not counted: the password was right
		}
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, s.loginFailed(ctx, keys, &user.ID, "bad_code", in.ClientIP)
		}
	}
	s.throttle.Reset(keys...)

	now := s.now()
	sid, token, hash, err := newSessionToken(in.DeviceName != "")
	if err != nil {
		return nil, err
	}
	kind, name, lifetime := "browser", (*string)(nil), SessionLifetime
	if in.DeviceName != "" {
		kind, name, lifetime = "app", &in.DeviceName, s.appLifetime
	}
	out := &NewSession{Token: token, UserID: user.ID, ExpiresAt: now.Add(lifetime)}
	err = s.db.Tx(ctx, func(q *dbq.Queries) error {
		if in.PreviousToken != "" && kind == "browser" {
			if err := q.DeleteSessionByTokenHash(ctx, hashToken(in.PreviousToken)); err != nil {
				return err
			}
		}
		err := q.DeleteStaleSessions(ctx, dbq.DeleteStaleSessionsParams{UserID: user.ID, Now: now,
			IdleSince: now.Add(-SessionIdle), AppIdleSince: now.Add(-s.appIdle)})
		if err != nil {
			return err
		}
		err = q.InsertSession(ctx, dbq.InsertSessionParams{ID: sid, UserID: user.ID, TokenHash: hash, Kind: kind, Name: name, Now: now, ExpiresAt: out.ExpiresAt})
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &user.ID, Actor: audit.Owner, Action: "auth.login",
			TargetType: "session", TargetID: sid.String(), Detail: map[string]any{"method": method, "client_ip": in.ClientIP.String(), "kind": kind}})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// newSessionToken returns a new session id, its token and the hash the row keeps. A browser
// token is 32 random bytes for the cookie, stored as SHA-256(token); an app token is
// vmx_ses_<id>_<secret>, stored as SHA-256(secret) like the other bearer tokens.
func newSessionToken(app bool) (id uuid.UUID, token string, hash []byte, err error) {
	if app {
		return newToken(SessionPrefix)
	}
	raw := make([]byte, sessionTokenLen)
	if _, err := rand.Read(raw); err != nil {
		return uuid.Nil, "", nil, err
	}
	if id, err = uuid.NewV7(); err != nil {
		return uuid.Nil, "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return id, token, hashToken(token), nil
}

// loginFailed counts the failure and audits it. The attempted username is not recorded,
// since people sometimes type their password into that field.
func (s *Service) loginFailed(ctx context.Context, keys []string, userID *uuid.UUID, reason string, ip netip.Addr) error {
	s.throttle.Fail(keys...)
	err := audit.Record(ctx, s.db.Q(), audit.Event{UserID: userID, Actor: "anonymous", Action: "auth.login_failed",
		Detail: map[string]any{"reason": reason, "client_ip": ip.String()}})
	if err != nil {
		return err
	}
	return ErrInvalidCredentials
}

// Session returns the owner principal for a browser session token, sliding its idle expiry.
// Unknown, idle and expired sessions are ErrInvalidToken; app sessions go through Bearer.
func (s *Service) Session(ctx context.Context, token string) (*Principal, error) {
	now := s.now()
	q := s.db.Q()
	row, err := q.GetLiveSession(ctx, dbq.GetLiveSessionParams{TokenHash: hashToken(token), Now: now, IdleSince: now.Add(-SessionIdle)})
	if err != nil {
		return nil, notFoundAs(err, ErrInvalidToken)
	}
	if err := q.TouchSession(ctx, dbq.TouchSessionParams{ID: row.ID, Now: now, Before: now.Add(-touchEvery)}); err != nil {
		return nil, err
	}
	return &Principal{Kind: OwnerSession, UserID: row.UserID, ID: row.ID}, nil
}

// appSession returns the principal of an app session token, sliding its idle expiry.
func (s *Service) appSession(ctx context.Context, id uuid.UUID, hash []byte) (*Principal, error) {
	now := s.now()
	q := s.db.Q()
	row, err := q.GetLiveAppSession(ctx, dbq.GetLiveAppSessionParams{ID: id, Now: now, IdleSince: now.Add(-s.appIdle)})
	if err != nil {
		return nil, notFoundAs(err, ErrInvalidToken)
	}
	if subtle.ConstantTimeCompare(row.TokenHash, hash) != 1 {
		return nil, ErrInvalidToken
	}
	if err := q.TouchSession(ctx, dbq.TouchSessionParams{ID: row.ID, Now: now, Before: now.Add(-touchEvery)}); err != nil {
		return nil, err
	}
	return &Principal{Kind: OwnerSession, App: true, UserID: row.UserID, ID: row.ID}, nil
}

// Logout deletes the session.
func (s *Service) Logout(ctx context.Context, p *Principal) error {
	return db.MapErr(s.db.Q().DeleteSession(ctx, p.ID))
}

// CSRFToken is the value the UI must echo in X-CSRF-Token on mutating requests. It is an
// HMAC of the session token, so it needs no storage and dies with the session.
func (s *Service) CSRFToken(sessionToken string) string {
	m := hmac.New(sha256.New, s.csrfKey)
	m.Write([]byte(csrfLabel))
	m.Write([]byte(sessionToken))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// CheckCSRF reports whether header is the CSRF token for the session token.
func (s *Service) CheckCSRF(sessionToken, header string) bool {
	return sessionToken != "" && hmac.Equal([]byte(header), []byte(s.CSRFToken(sessionToken)))
}

// User is the owner as shown to the UI.
type User struct {
	ID          uuid.UUID `json:"id"`
	Username    string    `json:"username"`
	TOTPEnabled bool      `json:"totp_enabled"`
}

// User returns the account behind a principal.
func (s *Service) User(ctx context.Context, id uuid.UUID) (User, error) {
	u, err := s.db.Q().GetUserByID(ctx, id)
	if err != nil {
		return User{}, db.MapErr(err)
	}
	return User{ID: u.ID, Username: u.Username, TOTPEnabled: u.TotpEnabledAt != nil}, nil
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
