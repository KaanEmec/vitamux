package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// Machine tokens are <prefix><id>_<secret>: id is the row's UUID as 32 lowercase hex
// characters (no dashes) and secret is 32 random bytes in unpadded base64url (43
// characters, which may themselves contain '_'; the fixed-width id keeps parsing unambiguous).
// Only SHA-256(secret) is stored.
const (
	PATPrefix    = "vmx_pat_"
	ClientPrefix = "vmx_cli_"
	// SessionPrefix marks an app session token; its id is the sessions row.
	SessionPrefix = "vmx_ses_"

	secretLen        = 32
	encodedSecretLen = 43
	idHexLen         = 32
	// touchEvery limits last-used writes to one per token per minute.
	touchEvery = time.Minute
)

// ErrInvalidToken covers malformed, unknown, revoked and expired tokens alike.
var ErrInvalidToken = errors.New("auth: invalid or expired token")

func newToken(prefix string) (id uuid.UUID, token string, hash []byte, err error) {
	id, err = uuid.NewV7()
	if err != nil {
		return uuid.Nil, "", nil, err
	}
	token, hash, err = tokenFor(prefix, id)
	return id, token, hash, err
}

// tokenFor returns a token with a fresh secret for the row id, and the secret's hash.
func tokenFor(prefix string, id uuid.UUID) (token string, hash []byte, err error) {
	secret := make([]byte, secretLen)
	if _, err := rand.Read(secret); err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(secret)
	return prefix + hex.EncodeToString(id[:]) + "_" + base64.RawURLEncoding.EncodeToString(secret), sum[:], nil
}

// ParseToken splits a vmx_pat_/vmx_cli_/vmx_ses_ token into its prefix, row id and secret hash.
func ParseToken(token string) (prefix string, id uuid.UUID, hash []byte, err error) {
	switch {
	case strings.HasPrefix(token, PATPrefix):
		prefix = PATPrefix
	case strings.HasPrefix(token, ClientPrefix):
		prefix = ClientPrefix
	case strings.HasPrefix(token, SessionPrefix):
		prefix = SessionPrefix
	default:
		return "", uuid.Nil, nil, ErrInvalidToken
	}
	rest := token[len(prefix):]
	if len(rest) != idHexLen+1+encodedSecretLen || rest[idHexLen] != '_' {
		return "", uuid.Nil, nil, ErrInvalidToken
	}
	raw, err := hex.DecodeString(rest[:idHexLen])
	if err != nil || strings.ToLower(rest[:idHexLen]) != rest[:idHexLen] {
		return "", uuid.Nil, nil, ErrInvalidToken
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(rest[idHexLen+1:])
	if err != nil || len(secret) != secretLen {
		return "", uuid.Nil, nil, ErrInvalidToken
	}
	copy(id[:], raw)
	sum := sha256.Sum256(secret)
	return prefix, id, sum[:], nil
}

// APIKeyInfo describes an API key without its secret.
type APIKeyInfo struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	Scopes     []Scope    `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

func apiKeyInfo(k dbq.ApiKey) APIKeyInfo {
	scopes := make([]Scope, len(k.Scopes))
	for i, s := range k.Scopes {
		scopes[i] = Scope(s)
	}
	return APIKeyInfo{ID: k.ID, Name: k.Name, Scopes: scopes, CreatedAt: k.CreatedAt,
		LastUsedAt: k.LastUsedAt, ExpiresAt: k.ExpiresAt, RevokedAt: k.RevokedAt}
}

// ErrBadScopes is returned for an empty or unknown scope list.
var ErrBadScopes = errors.New("auth: scopes must be a non-empty subset of " + fmt.Sprint(Scopes))

// CreateAPIKey stores a new personal access token for the user and returns it. The token
// is returned only here; the database keeps its hash.
func (s *Service) CreateAPIKey(ctx context.Context, by *Principal, name string, scopes []Scope, expiresAt *time.Time) (APIKeyInfo, string, error) {
	if len(scopes) == 0 {
		return APIKeyInfo{}, "", ErrBadScopes
	}
	names := make([]string, 0, len(scopes))
	for _, sc := range scopes {
		if !slices.Contains(Scopes, sc) {
			return APIKeyInfo{}, "", ErrBadScopes
		}
		if !slices.Contains(names, string(sc)) {
			names = append(names, string(sc))
		}
	}
	id, token, hash, err := newToken(PATPrefix)
	if err != nil {
		return APIKeyInfo{}, "", err
	}
	var row dbq.ApiKey
	err = s.db.Tx(ctx, func(q *dbq.Queries) error {
		row, err = q.InsertAPIKey(ctx, dbq.InsertAPIKeyParams{
			ID: id, UserID: by.UserID, Name: name, SecretHash: hash, Scopes: names, ExpiresAt: expiresAt,
		})
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &by.UserID, Actor: by.Actor(), Action: "api_key.create",
			TargetType: "api_key", TargetID: id.String(), Detail: map[string]any{"name": name, "scopes": names}})
	})
	if err != nil {
		return APIKeyInfo{}, "", err
	}
	return apiKeyInfo(row), token, nil
}

// ListAPIKeys returns the user's keys, newest first, without secrets.
func (s *Service) ListAPIKeys(ctx context.Context, userID uuid.UUID) ([]APIKeyInfo, error) {
	rows, err := s.db.Q().ListAPIKeys(ctx, userID)
	if err != nil {
		return nil, db.MapErr(err)
	}
	out := make([]APIKeyInfo, len(rows))
	for i, r := range rows {
		out[i] = apiKeyInfo(r)
	}
	return out, nil
}

// RevokeAPIKey revokes one of the user's active keys; db.ErrNotFound if there is none.
func (s *Service) RevokeAPIKey(ctx context.Context, by *Principal, id uuid.UUID) error {
	return s.db.Tx(ctx, func(q *dbq.Queries) error {
		n, err := q.RevokeAPIKey(ctx, dbq.RevokeAPIKeyParams{Now: s.now(), ID: id, UserID: by.UserID})
		if err != nil {
			return err
		}
		if n == 0 {
			return db.ErrNotFound
		}
		return audit.Record(ctx, q, audit.Event{UserID: &by.UserID, Actor: by.Actor(), Action: "api_key.revoke",
			TargetType: "api_key", TargetID: id.String()})
	})
}

// CreateClientToken stores an ingest token for one connection and returns its id and the
// token, which is not retrievable later. kind is collector, device or importer.
func CreateClientToken(ctx context.Context, d *db.DB, userID, connectionID uuid.UUID, kind, name string) (uuid.UUID, string, error) {
	id, token, hash, err := newToken(ClientPrefix)
	if err != nil {
		return uuid.Nil, "", err
	}
	err = d.Q().InsertClient(ctx, dbq.InsertClientParams{
		ID: id, UserID: userID, ConnectionID: connectionID, Kind: kind, Name: name, TokenHash: hash,
	})
	if err != nil {
		return uuid.Nil, "", db.MapErr(err)
	}
	return id, token, nil
}

// Bearer authenticates an API key, client token or app session. Every failure is
// ErrInvalidToken, except database errors.
func (s *Service) Bearer(ctx context.Context, token string) (*Principal, error) {
	prefix, id, hash, err := ParseToken(token)
	if err != nil {
		return nil, err
	}
	if prefix == SessionPrefix {
		return s.appSession(ctx, id, hash)
	}
	now := s.now()
	q := s.db.Q()
	if prefix == PATPrefix {
		k, err := q.GetAPIKey(ctx, id)
		if err != nil {
			return nil, notFoundAs(err, ErrInvalidToken)
		}
		if subtle.ConstantTimeCompare(k.SecretHash, hash) != 1 || k.RevokedAt != nil || (k.ExpiresAt != nil && !now.Before(*k.ExpiresAt)) {
			return nil, ErrInvalidToken
		}
		if err := q.TouchAPIKey(ctx, dbq.TouchAPIKeyParams{Now: now, ID: id, Before: now.Add(-touchEvery)}); err != nil {
			return nil, err
		}
		p := &Principal{Kind: APIKey, UserID: k.UserID, ID: k.ID}
		for _, sc := range k.Scopes {
			p.Scopes = append(p.Scopes, Scope(sc))
		}
		return p, nil
	}
	c, err := q.GetClient(ctx, id)
	if err != nil {
		return nil, notFoundAs(err, ErrInvalidToken)
	}
	if subtle.ConstantTimeCompare(c.TokenHash, hash) != 1 || c.RevokedAt != nil {
		return nil, ErrInvalidToken
	}
	if err := q.TouchClient(ctx, dbq.TouchClientParams{Now: now, ID: id, Before: now.Add(-touchEvery)}); err != nil {
		return nil, err
	}
	return &Principal{Kind: Client, UserID: c.UserID, ID: c.ID, ConnectionID: c.ConnectionID}, nil
}

// notFoundAs maps a missing row to sentinel and passes other errors through.
func notFoundAs(err, sentinel error) error {
	if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
		return sentinel
	}
	return err
}
