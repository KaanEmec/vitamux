package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

const (
	refreshSkew    = 2 * time.Minute  // refresh this long before the access token expires
	refreshTimeout = 30 * time.Second // the provider call runs while the row is locked
)

// credStore seals credentials into the credentials row and refreshes them single-flight.
type credStore struct {
	db   *db.DB
	keys *crypto.Keyring
}

func (s *credStore) save(ctx context.Context, q *dbq.Queries, connectionID uuid.UUID, c Credentials) error {
	plain, err := json.Marshal(c) //nolint:gosec // sealed right below; never stored or logged in plain
	if err != nil {
		return err
	}
	sealed, err := s.keys.Seal(crypto.Credentials, plain, crypto.CredentialsAAD(connectionID))
	if err != nil {
		return err
	}
	var exp *time.Time
	if !c.ExpiresAt.IsZero() {
		exp = &c.ExpiresAt
	}
	return q.UpsertCredentials(ctx, dbq.UpsertCredentialsParams{
		ConnectionID: connectionID, Ciphertext: sealed, KeyID: s.keys.KeyID(), AccessExpiresAt: exp,
	})
}

func (s *credStore) open(connectionID uuid.UUID, sealed []byte) (Credentials, error) {
	plain, err := s.keys.Open(crypto.Credentials, sealed, crypto.CredentialsAAD(connectionID))
	if err != nil {
		// Lost or rotated-away master key: reconnecting seals fresh credentials.
		return Credentials{}, fmt.Errorf("open credentials: %w: %w", ErrReauthRequired, err)
	}
	var c Credentials
	if err := json.Unmarshal(plain, &c); err != nil {
		return Credentials{}, fmt.Errorf("decode credentials: %w", ErrReauthRequired)
	}
	return c, nil
}

func expiring(c Credentials, now time.Time) bool {
	return !c.ExpiresAt.IsZero() && c.ExpiresAt.Before(now.Add(refreshSkew))
}

// current returns usable credentials and the row version they came from, refreshing them
// first when they are about to expire.
func (s *credStore) current(ctx context.Context, c Conn, auth Authenticator) (Credentials, int32, error) {
	row, err := s.db.Q().GetCredentials(ctx, c.ID)
	if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
		return Credentials{}, 0, fmt.Errorf("no credentials stored: %w", ErrReauthRequired)
	} else if err != nil {
		return Credentials{}, 0, err
	}
	cred, err := s.open(c.ID, row.Ciphertext)
	if err != nil || !expiring(cred, time.Now()) {
		return cred, row.Version, err
	}
	return s.refresh(ctx, c, auth, row.Version, false)
}

// refresh renews the credentials under a row lock, so one caller across all processes
// refreshes and the rest reuse its result. seen is the version the caller holds: if the row
// moved on, someone else refreshed already. force refreshes even unexpired credentials
// (the provider rejected the access token). The rotated tokens are committed before use.
func (s *credStore) refresh(ctx context.Context, c Conn, auth Authenticator, seen int32, force bool) (Credentials, int32, error) {
	var cred Credentials
	version := seen
	err := s.db.Tx(ctx, func(q *dbq.Queries) error {
		row, err := q.LockCredentials(ctx, c.ID)
		if err != nil {
			return err
		}
		version = row.Version
		if cred, err = s.open(c.ID, row.Ciphertext); err != nil {
			return err
		}
		if row.Version != seen || (!force && !expiring(cred, time.Now())) {
			return nil
		}
		rctx, cancel := context.WithTimeout(ctx, refreshTimeout)
		defer cancel()
		if cred, err = auth.Refresh(rctx, c, cred); err != nil {
			return err
		}
		version = row.Version + 1
		return s.save(ctx, q, c.ID, cred)
	})
	if errors.Is(err, db.ErrNotFound) {
		err = fmt.Errorf("credentials removed: %w", ErrReauthRequired)
	}
	if err != nil {
		return Credentials{}, 0, err
	}
	return cred, version, nil
}
