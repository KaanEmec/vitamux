// Package crypto implements the master key, HKDF purpose keys and AES-GCM sealing (ADR-0011).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Purpose names what a derived key protects. Each purpose gets an independent key.
type Purpose string

const (
	Credentials    Purpose = "credentials"
	Documents      Purpose = "documents"
	BlobNames      Purpose = "blob-names"
	SessionSigning Purpose = "session-signing"
)

var purposes = []Purpose{Credentials, Documents, BlobNames, SessionSigning}

const (
	keySize     = 32
	keyIDSize   = 8
	nonceSize   = 12
	version     = 1
	headerSize  = 1 + keyIDSize
	maxKeyFile  = 1024
	keyIDLabel  = "vitamux/key-id/v1\x00"
	hkdfInfoFmt = "vitamux/v1/%s"
)

var (
	// ErrOpen is returned for any sealed value that is malformed or fails authentication.
	ErrOpen = errors.New("crypto: cannot open sealed value")
	// ErrUnknownKey means the sealed value names a key the keyring does not hold.
	ErrUnknownKey = errors.New("crypto: sealed value uses an unknown master key")
	// ErrPurpose means the purpose is not one of the defined constants.
	ErrPurpose = errors.New("crypto: unknown purpose")
)

type masterKey struct {
	id    [keyIDSize]byte
	aeads map[Purpose]cipher.AEAD
	raw   map[Purpose][]byte
}

// Keyring seals with the current master key and opens with the current or any previous one.
type Keyring struct {
	current *masterKey
	byID    map[[keyIDSize]byte]*masterKey
}

// Load reads the current master key file and optional previous key files (used to open
// values sealed before a rotation). Key files are 64 hex characters, mode 0600 or stricter.
func Load(path string, previous ...string) (*Keyring, error) {
	kr := &Keyring{byID: map[[keyIDSize]byte]*masterKey{}}
	for i, p := range append([]string{path}, previous...) {
		raw, err := readKeyFile(p)
		if err != nil {
			return nil, err
		}
		k, err := newMasterKey(raw)
		clear(raw)
		if err != nil {
			return nil, fmt.Errorf("master key %q: %w", p, err)
		}
		if _, dup := kr.byID[k.id]; dup {
			continue
		}
		kr.byID[k.id] = k
		if i == 0 {
			kr.current = k
		}
	}
	return kr, nil
}

// Generate returns a new random master key in the file format Load expects.
func Generate() ([]byte, error) {
	raw := make([]byte, keySize)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	defer clear(raw)
	return append(hex.AppendEncode(nil, raw), '\n'), nil
}

// WriteKeyFile generates a master key at path with mode 0600, refusing to overwrite an
// existing file, and returns its key id.
func WriteKeyFile(path string) (string, error) {
	text, err := Generate()
	if err != nil {
		return "", err
	}
	defer clear(text)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // path is operator-supplied configuration
	if err != nil {
		return "", err
	}
	if _, err = f.Write(text); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	kr, err := Load(path)
	if err != nil {
		return "", err
	}
	return kr.KeyID(), nil
}

func readKeyFile(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // path is operator-supplied configuration
	if err != nil {
		return nil, fmt.Errorf("master key: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("master key %q: %w", path, err)
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("master key %q: not a regular file", path)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("master key %q: mode %04o is readable by group or others; run chmod 600", path, st.Mode().Perm())
	}
	b, err := io.ReadAll(io.LimitReader(f, maxKeyFile))
	if err != nil {
		return nil, fmt.Errorf("master key %q: %w", path, err)
	}
	defer clear(b)
	// hex.Decode errors quote offending bytes, so report format problems ourselves.
	text := strings.TrimSpace(string(b))
	if len(text) != 2*keySize {
		return nil, fmt.Errorf("master key %q: expected %d hex characters", path, 2*keySize)
	}
	raw, err := hex.DecodeString(text)
	if err != nil {
		return nil, fmt.Errorf("master key %q: not valid hex", path)
	}
	return raw, nil
}

func newMasterKey(raw []byte) (*masterKey, error) {
	if len(raw) != keySize {
		return nil, errors.New("key must be 32 bytes")
	}
	k := &masterKey{aeads: map[Purpose]cipher.AEAD{}, raw: map[Purpose][]byte{}}
	h := sha256.New()
	h.Write([]byte(keyIDLabel))
	h.Write(raw)
	copy(k.id[:], h.Sum(nil))
	for _, p := range purposes {
		pk, err := hkdf.Key(sha256.New, raw, nil, fmt.Sprintf(hkdfInfoFmt, p), keySize)
		if err != nil {
			return nil, err
		}
		block, err := aes.NewCipher(pk)
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		k.aeads[p], k.raw[p] = aead, pk
	}
	return k, nil
}

// KeyID is the current key's non-secret identifier, stored next to each sealed value.
func (kr *Keyring) KeyID() string { return hex.EncodeToString(kr.current.id[:]) }

// PurposeKey returns a copy of the current 32-byte key for p, for uses such as HMAC
// signing that need the raw key. Previous keys are never used for this.
func (kr *Keyring) PurposeKey(p Purpose) ([]byte, error) {
	k, ok := kr.current.raw[p]
	if !ok {
		return nil, ErrPurpose
	}
	return append([]byte(nil), k...), nil
}

// Seal encrypts plaintext under the current key. The result is self-describing:
// version | key id | nonce | ciphertext+tag. aad binds the value to its row and must be
// passed again to Open.
func (kr *Keyring) Seal(p Purpose, plaintext, aad []byte) ([]byte, error) {
	aead, ok := kr.current.aeads[p]
	if !ok {
		return nil, ErrPurpose
	}
	out := make([]byte, headerSize+nonceSize, headerSize+nonceSize+len(plaintext)+aead.Overhead())
	out[0] = version
	copy(out[1:], kr.current.id[:])
	nonce := out[headerSize:]
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(out, nonce, plaintext, fullAAD(out[:headerSize], p, aad)), nil
}

// Open decrypts a value from Seal. Any change to the value, purpose or aad yields ErrOpen.
func (kr *Keyring) Open(p Purpose, sealed, aad []byte) ([]byte, error) {
	if len(sealed) < headerSize+nonceSize || sealed[0] != version {
		return nil, ErrOpen
	}
	var id [keyIDSize]byte
	copy(id[:], sealed[1:headerSize])
	k, ok := kr.byID[id]
	if !ok {
		return nil, ErrUnknownKey
	}
	aead, ok := k.aeads[p]
	if !ok {
		return nil, ErrPurpose
	}
	nonce, ct := sealed[headerSize:headerSize+nonceSize], sealed[headerSize+nonceSize:]
	pt, err := aead.Open(nil, nonce, ct, fullAAD(sealed[:headerSize], p, aad))
	if err != nil {
		return nil, ErrOpen
	}
	return pt, nil
}

// fullAAD authenticates the header and purpose along with the caller's aad. The header has
// a fixed length and the purpose is NUL-terminated, so the concatenation is unambiguous.
func fullAAD(header []byte, p Purpose, aad []byte) []byte {
	out := make([]byte, 0, len(header)+len(p)+1+len(aad))
	out = append(out, header...)
	out = append(out, p...)
	out = append(out, 0)
	return append(out, aad...)
}
