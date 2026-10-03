package crypto

import "github.com/google/uuid"

// CredentialsAAD is the AAD that binds a sealed credentials.ciphertext to its connection.
// Seal, Open and key rotation must all use it (ADR-0011).
func CredentialsAAD(connectionID uuid.UUID) []byte {
	return []byte("credentials:" + connectionID.String())
}

// TOTPAAD binds a sealed users.totp_ciphertext to its user (purpose Credentials).
func TOTPAAD(userID uuid.UUID) []byte { return []byte("users.totp:" + userID.String()) }
