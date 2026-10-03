package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters for new hashes (docs/architecture/security.md#keys-and-secrets).
// Verification reads the parameters from the stored PHC string, so they can be raised later.
const (
	argonMemory  = 64 * 1024 // KiB
	argonTime    = 3
	argonThreads = 2
	argonSaltLen = 16
	argonKeyLen  = 32

	// MinPasswordLen is checked by the CLI; a long passphrase is the expected input.
	MinPasswordLen = 12
	maxPasswordLen = 1024
)

// ErrWeakPassword is returned for passwords outside the accepted length.
var ErrWeakPassword = fmt.Errorf("auth: password must be %d to %d characters", MinPasswordLen, maxPasswordLen)

// hashSlots bounds concurrent argon2 runs: each one takes argonMemory, and login is public.
var hashSlots = make(chan struct{}, 2)

// dummyHash is verified for unknown usernames so both failures cost the same time.
var dummyHash = sync.OnceValue(func() string { return mustHash("vitamux-dummy-password") })

// HashPassword returns an argon2id PHC string:
// $argon2id$v=19$m=65536,t=3,p=2$<salt>$<key> (unpadded standard base64).
func HashPassword(password string) (string, error) {
	if !acceptablePassword(password) {
		return "", ErrWeakPassword
	}
	return hashPassword(password)
}

func acceptablePassword(password string) bool {
	return utf8.RuneCountInString(password) >= MinPasswordLen && len(password) <= maxPasswordLen
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argonKey(password, salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

func mustHash(password string) string {
	h, err := hashPassword(password)
	if err != nil {
		panic(err)
	}
	return h
}

// VerifyPassword reports whether password matches the PHC string. A malformed hash is an
// error, never a match.
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("auth: unsupported password hash")
	}
	var version int
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("auth: unsupported argon2 version")
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil ||
		memory == 0 || memory > 1<<22 || iterations == 0 || iterations > 64 || threads == 0 {
		return false, errors.New("auth: bad argon2 parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errors.New("auth: bad password salt")
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) < 16 || len(want) > 64 {
		return false, errors.New("auth: bad password hash")
	}
	tooLong := len(password) > maxPasswordLen
	if tooLong {
		password = password[:maxPasswordLen] // bounded cost; the result is discarded below
	}
	got := argonKey(password, salt, iterations, memory, threads, uint32(len(want))) //nolint:gosec // len(want) ≤ 64
	return subtle.ConstantTimeCompare(got, want) == 1 && !tooLong, nil
}

func argonKey(password string, salt []byte, t, m uint32, p uint8, n uint32) []byte {
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	return argon2.IDKey([]byte(password), salt, t, m, p, n)
}
