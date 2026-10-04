package db

import (
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrNotFound means a query that must return a row returned none (pgx.ErrNoRows).
	ErrNotFound = errors.New("db: not found")
	// ErrConflict means a unique (23505) or exclusion (23P01) constraint rejected the write.
	ErrConflict = errors.New("db: conflict")
)

// PostgreSQL SQLSTATE codes the layer reacts to.
const (
	codeSerializationFailure = "40001"
	codeDeadlockDetected     = "40P01"
	codeUniqueViolation      = "23505"
	codeExclusionViolation   = "23P01"
)

// MapErr turns pgx errors into ErrNotFound or ErrConflict. The original error stays in the
// chain, so errors.Is works for both the sentinel and the cause, and errors.As can still
// reach the *pgconn.PgError for the constraint name. Tx applies it; call it on errors from
// direct Q() calls.
func MapErr(err error) error {
	switch {
	case err == nil, errors.Is(err, ErrNotFound), errors.Is(err, ErrConflict):
		return err
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	case sqlState(err, codeUniqueViolation, codeExclusionViolation):
		return fmt.Errorf("%w: %w", ErrConflict, err)
	}
	return err
}

func sqlState(err error, codes ...string) bool {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return false
	}
	return slices.Contains(codes, pg.Code)
}
