package crypto

import "github.com/google/uuid"

// CredentialsAAD is the AAD that binds a sealed credentials.ciphertext to its connection.
// Seal, Open and key rotation must all use it (ADR-0011).
func CredentialsAAD(connectionID uuid.UUID) []byte {
	return []byte("credentials:" + connectionID.String())
}

// TOTPAAD binds a sealed users.totp_ciphertext to its user (purpose Credentials).
func TOTPAAD(userID uuid.UUID) []byte { return []byte("users.totp:" + userID.String()) }

// AuthSessionAAD binds a sealed oauth_states.session to its pending authorization step
// (purpose Credentials).
func AuthSessionAAD(stateID uuid.UUID) []byte { return []byte("auth-session:" + stateID.String()) }

// ProviderAppAAD binds a sealed provider_app_credentials.ciphertext to its provider (purpose
// Credentials, ADR-0021).
func ProviderAppAAD(provider string) []byte { return []byte("provider_app:" + provider) }

// SidecarAAD binds a sealed sidecars.ciphertext to its sidecar (purpose Credentials).
func SidecarAAD(name string) []byte { return []byte("sidecar:" + name) }

// AuthBindingAAD binds a sealed oauth_states.binding to its pending authorization step
// (purpose Credentials).
func AuthBindingAAD(stateID uuid.UUID) []byte { return []byte("auth-binding:" + stateID.String()) }
