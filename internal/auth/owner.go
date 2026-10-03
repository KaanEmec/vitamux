package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

var (
	// ErrOwnerExists refuses a second account: Vitamux has a single owner.
	ErrOwnerExists = errors.New("auth: an owner account already exists")
	// ErrBadUsername is returned for an empty or over-long username.
	ErrBadUsername = errors.New("auth: username must be 1 to 64 characters without spaces")
)

// CreateOwner creates the single owner account. It does not need the master key.
func CreateOwner(ctx context.Context, d *db.DB, username, password string) (uuid.UUID, error) {
	if username == "" || len(username) > 64 || strings.ContainsAny(username, " \t\r\n") {
		return uuid.Nil, ErrBadUsername
	}
	hash, err := HashPassword(password)
	if err != nil {
		return uuid.Nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	err = d.Tx(ctx, func(q *dbq.Queries) error {
		n, err := q.CountUsers(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			return ErrOwnerExists
		}
		if err := q.InsertUser(ctx, dbq.InsertUserParams{ID: id, Username: username, PasswordHash: hash}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &id, Actor: audit.System, Action: "owner.create", TargetType: "user", TargetID: id.String()})
	}, db.Serializable())
	if errors.Is(err, db.ErrConflict) {
		return uuid.Nil, ErrOwnerExists
	}
	return id, err
}

// ResetPassword sets a new password and ends every session of the account. TOTP stays as
// it is. db.ErrNotFound if there is no such user.
func ResetPassword(ctx context.Context, d *db.DB, username, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return d.Tx(ctx, func(q *dbq.Queries) error {
		u, err := q.GetUserByUsername(ctx, username)
		if err != nil {
			return err
		}
		if err := q.SetPasswordHash(ctx, dbq.SetPasswordHashParams{PasswordHash: hash, ID: u.ID}); err != nil {
			return err
		}
		n, err := q.DeleteUserSessions(ctx, u.ID)
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &u.ID, Actor: audit.System, Action: "owner.password_reset",
			TargetType: "user", TargetID: u.ID.String(), Detail: map[string]any{"sessions_ended": n}})
	})
}
