package auth

import (
	"context"
	"slices"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
)

// Scope is an API key permission (docs/architecture/api.md#conventions).
type Scope string

const (
	ReadHealth     Scope = "read:health"
	ReadConfig     Scope = "read:config"
	WriteConfig    Scope = "write:config"
	WriteDocuments Scope = "write:documents"
	// Admin implies every other owner scope.
	Admin Scope = "admin"
)

// Scopes lists every API key scope, matching the api_keys.scopes check constraint.
var Scopes = []Scope{ReadHealth, ReadConfig, WriteConfig, WriteDocuments, Admin}

// Kind says how a request authenticated.
type Kind string

const (
	OwnerSession Kind = "session"
	APIKey       Kind = "api_key"
	Client       Kind = "client"
)

// Principal is the authenticated caller of one request.
type Principal struct {
	Kind   Kind
	UserID uuid.UUID
	// ID is the session, API key or client row id.
	ID uuid.UUID
	// Scopes is set for API keys only; a session holds every owner scope.
	Scopes []Scope
	// ConnectionID is the only connection a client token may ingest into.
	ConnectionID uuid.UUID
}

// Can reports whether p may use an owner (/api/v1) endpoint guarded by s. Client tokens
// never can: they only ingest.
func (p *Principal) Can(s Scope) bool {
	switch p.Kind {
	case OwnerSession:
		return true
	case APIKey:
		return slices.Contains(p.Scopes, s) || slices.Contains(p.Scopes, Admin)
	default:
		return false
	}
}

// CanIngest reports whether p may push data into the connection.
func (p *Principal) CanIngest(connection uuid.UUID) bool {
	return p.Kind == Client && p.ConnectionID == connection
}

// Actor names p in audit events: "owner", "api_key:<id>" or "client:<id>".
func (p *Principal) Actor() string {
	if p.Kind == OwnerSession {
		return audit.Owner
	}
	return string(p.Kind) + ":" + p.ID.String()
}

type principalKey struct{}

// WithPrincipal returns ctx carrying p.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the caller set by the HTTP authentication middleware, or nil.
func PrincipalFrom(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}
