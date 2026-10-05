package auth

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// ChangePassword sets a new password after re-checking the current one and ends every
// other session of the owner; the caller's session stays. Wrong current passwords are
// throttled like logins (per account, a separate counter), so a stolen session cannot
// guess the password at full speed. It returns how many sessions ended, or
// ErrWeakPassword, ErrInvalidCredentials or a *ThrottledError.
func (s *Service) ChangePassword(ctx context.Context, by *Principal, current, next string) (int64, error) {
	if !acceptablePassword(next) {
		return 0, ErrWeakPassword
	}
	key := "password:" + by.UserID.String()
	if wait := s.throttle.Wait(key); wait > 0 {
		return 0, &ThrottledError{RetryAfter: wait}
	}
	u, err := s.db.Q().GetUserByID(ctx, by.UserID)
	if err != nil {
		return 0, db.MapErr(err)
	}
	ok, err := VerifyPassword(u.PasswordHash, current)
	if err != nil {
		return 0, err
	}
	if !ok {
		s.throttle.Fail(key)
		if err := audit.Record(ctx, s.db.Q(), audit.Event{UserID: &u.ID, Actor: by.Actor(), Action: "auth.password_change_failed",
			TargetType: "user", TargetID: u.ID.String()}); err != nil {
			return 0, err
		}
		return 0, ErrInvalidCredentials
	}
	s.throttle.Reset(key)
	hash, err := hashPassword(next)
	if err != nil {
		return 0, err
	}
	var ended int64
	err = s.db.Tx(ctx, func(q *dbq.Queries) error {
		if err := q.SetPasswordHash(ctx, dbq.SetPasswordHashParams{PasswordHash: hash, ID: u.ID}); err != nil {
			return err
		}
		if ended, err = q.DeleteOtherSessions(ctx, dbq.DeleteOtherSessionsParams{UserID: u.ID, Keep: by.ID}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &u.ID, Actor: by.Actor(), Action: "auth.password_change",
			TargetType: "user", TargetID: u.ID.String(), Detail: map[string]any{"sessions_ended": ended}})
	})
	return ended, err
}

// SessionInfo is one live session as listed to its owner. ExpiresAt is the absolute end;
// a session also ends after its kind's idle timeout. Name is the app's device name.
type SessionInfo struct {
	ID         uuid.UUID `json:"id"`
	Kind       string    `json:"kind"`
	Name       *string   `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Current    bool      `json:"current"`
}

// ListSessions returns the live sessions of by's owner, most recently used first, marking
// by's own.
func (s *Service) ListSessions(ctx context.Context, by *Principal) ([]SessionInfo, error) {
	now := s.now()
	rows, err := s.db.Q().ListLiveSessions(ctx, dbq.ListLiveSessionsParams{UserID: by.UserID, Now: now,
		IdleSince: now.Add(-SessionIdle), AppIdleSince: now.Add(-s.appIdle)})
	if err != nil {
		return nil, db.MapErr(err)
	}
	out := make([]SessionInfo, len(rows))
	for i, r := range rows {
		out[i] = SessionInfo{ID: r.ID, Kind: r.Kind, Name: r.Name, CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt, ExpiresAt: r.ExpiresAt,
			Current: by.Kind == OwnerSession && r.ID == by.ID}
	}
	return out, nil
}

// RevokeSession ends one session of by's owner (possibly by's own). db.ErrNotFound if the
// owner has no session with this id.
func (s *Service) RevokeSession(ctx context.Context, by *Principal, id uuid.UUID) error {
	return s.db.Tx(ctx, func(q *dbq.Queries) error {
		n, err := q.DeleteUserSession(ctx, dbq.DeleteUserSessionParams{ID: id, UserID: by.UserID})
		if err != nil {
			return err
		}
		if n == 0 {
			return db.ErrNotFound
		}
		return audit.Record(ctx, q, audit.Event{UserID: &by.UserID, Actor: by.Actor(), Action: "auth.session_revoke",
			TargetType: "session", TargetID: id.String(), Detail: map[string]any{"current": id == by.ID}})
	})
}
