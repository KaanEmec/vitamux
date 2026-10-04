package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net/netip"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// Device pairing (J15.2; docs/architecture/apple-health.md#pairing-and-security): the owner
// creates a short-lived, single-use code, and a device exchanges it for a client token of kind
// device on the owner's apple_health push connection. Only the code's SHA-256 is stored.
const (
	// PairingCodeTTL is how long a pairing code works.
	PairingCodeTTL = 10 * time.Minute
	// maxPairingCodes is how many codes an owner may create per PairingCodeTTL.
	maxPairingCodes = 5
	// Codes are 8 Crockford base32 symbols (40 bits), shown as XXXX-XXXX.
	pairingAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	pairingCodeLen  = 8
	maxDeviceName   = 100
	pairingProvider = "apple_health"
)

var (
	// ErrInvalidPairingCode covers malformed, unknown, used and expired codes alike.
	ErrInvalidPairingCode = errors.New("auth: unknown, used or expired pairing code")
	// ErrBadDeviceName is a device name that is empty, too long or has control characters.
	ErrBadDeviceName = errors.New("auth: device name must be 1 to 100 characters without control characters")
)

// PairingCode is a new code, shown to the owner once.
type PairingCode struct {
	Code      string // XXXX-XXXX
	ExpiresAt time.Time
}

// CreatePairingCode stores a new code for the owner, or returns a *ThrottledError once they
// created maxPairingCodes within PairingCodeTTL.
func (s *Service) CreatePairingCode(ctx context.Context, by *Principal) (PairingCode, error) {
	raw := make([]byte, pairingCodeLen)
	if _, err := rand.Read(raw); err != nil {
		return PairingCode{}, err
	}
	for i, b := range raw {
		raw[i] = pairingAlphabet[b&31] // 32 symbols: no modulo bias
	}
	code := string(raw)
	id, err := uuid.NewV7()
	if err != nil {
		return PairingCode{}, err
	}
	now := s.now()
	sum := sha256.Sum256(raw)
	out := PairingCode{Code: code[:4] + "-" + code[4:], ExpiresAt: now.Add(PairingCodeTTL)}
	err = s.db.Tx(ctx, func(q *dbq.Queries) error {
		if err := q.DeleteOldPairingCodes(ctx, dbq.DeleteOldPairingCodesParams{UserID: by.UserID, Before: now}); err != nil {
			return err
		}
		n, err := q.InsertPairingCode(ctx, dbq.InsertPairingCodeParams{ID: id, UserID: by.UserID, CodeHash: sum[:],
			Now: now, ExpiresAt: out.ExpiresAt, Since: now.Add(-PairingCodeTTL), MaxRecent: maxPairingCodes})
		if err != nil {
			return err
		}
		if n == 0 {
			return &ThrottledError{RetryAfter: PairingCodeTTL}
		}
		return audit.Record(ctx, q, audit.Event{UserID: &by.UserID, Actor: by.Actor(), Action: "pairing_code.create",
			TargetType: "pairing_code", TargetID: id.String(), Detail: map[string]any{"expires_at": out.ExpiresAt}})
	})
	if err != nil {
		return PairingCode{}, err
	}
	return out, nil
}

// PairedDevice is what a device receives for a pairing code. The token is not retrievable later.
type PairedDevice struct {
	DeviceID, ConnectionID uuid.UUID
	Token                  string
}

// PairDevice redeems a pairing code: it creates a device client named name on the code owner's
// apple_health push connection (creating the connection the first time) and returns its token.
// Wrong codes are throttled per client address like failed logins (*ThrottledError).
func (s *Service) PairDevice(ctx context.Context, code, name string, ip netip.Addr) (PairedDevice, error) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxDeviceName || strings.ContainsFunc(name, unicode.IsControl) {
		return PairedDevice{}, ErrBadDeviceName
	}
	key := "pair:" + addrKey(ip)
	if wait := s.throttle.Wait(key); wait > 0 {
		return PairedDevice{}, &ThrottledError{RetryAfter: wait}
	}
	norm, ok := normalizePairingCode(code)
	if !ok {
		s.throttle.Fail(key)
		return PairedDevice{}, ErrInvalidPairingCode
	}
	sum := sha256.Sum256([]byte(norm))
	id, token, hash, err := newToken(ClientPrefix)
	if err != nil {
		return PairedDevice{}, err
	}
	actor := (&Principal{Kind: Client, ID: id}).Actor()
	out := PairedDevice{DeviceID: id, Token: token}
	err = s.db.Tx(ctx, func(q *dbq.Queries) error {
		userID, err := q.UsePairingCode(ctx, dbq.UsePairingCodeParams{Now: s.now(), CodeHash: sum[:]})
		if err != nil {
			return notFoundAs(err, ErrInvalidPairingCode)
		}
		if out.ConnectionID, err = s.pairingConnection(ctx, q, userID, actor); err != nil {
			return err
		}
		if err := q.InsertClient(ctx, dbq.InsertClientParams{ID: id, UserID: userID, ConnectionID: out.ConnectionID,
			Kind: "device", Name: name, TokenHash: hash}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &userID, Actor: actor, Action: "device.pair",
			TargetType: "client", TargetID: id.String(), Detail: map[string]any{"name": name, "connection_id": out.ConnectionID}})
	})
	if errors.Is(err, ErrInvalidPairingCode) {
		s.throttle.Fail(key)
	}
	if err != nil {
		return PairedDevice{}, err
	}
	return out, nil
}

// pairingConnection returns the owner's apple_health push connection, creating it if needed.
func (s *Service) pairingConnection(ctx context.Context, q *dbq.Queries, userID uuid.UUID, actor string) (uuid.UUID, error) {
	conn, err := q.OwnerApplePushConnection(ctx, userID)
	if err = db.MapErr(err); !errors.Is(err, db.ErrNotFound) {
		return conn, err
	}
	if conn, err = uuid.NewV7(); err != nil {
		return uuid.Nil, err
	}
	if _, err := q.InsertPushConnection(ctx, dbq.InsertPushConnectionParams{ID: conn, UserID: userID, Provider: pairingProvider}); err != nil {
		return uuid.Nil, err
	}
	return conn, audit.Record(ctx, q, audit.Event{UserID: &userID, Actor: actor, Action: "connection.create",
		TargetType: "connection", TargetID: conn.String(), Detail: map[string]any{"provider": pairingProvider, "mode": "push"}})
}

// normalizePairingCode ignores case, spaces and dashes and reads O as 0 and I, L as 1
// (Crockford), so a code typed by hand still matches.
func normalizePairingCode(code string) (string, bool) {
	code = strings.NewReplacer("-", "", " ", "", "O", "0", "I", "1", "L", "1").Replace(strings.ToUpper(code))
	if len(code) != pairingCodeLen || strings.ContainsFunc(code, func(r rune) bool { return !strings.ContainsRune(pairingAlphabet, r) }) {
		return "", false
	}
	return code, true
}

// RotateClientToken gives the calling client a new secret; the old token stops working at once.
func (s *Service) RotateClientToken(ctx context.Context, p *Principal) (string, error) {
	token, hash, err := tokenFor(ClientPrefix, p.ID)
	if err != nil {
		return "", err
	}
	err = s.db.Tx(ctx, func(q *dbq.Queries) error {
		n, err := q.SetClientToken(ctx, dbq.SetClientTokenParams{TokenHash: hash, ID: p.ID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrInvalidToken // revoked meanwhile
		}
		return audit.Record(ctx, q, audit.Event{UserID: &p.UserID, Actor: p.Actor(), Action: "client.rotate_token",
			TargetType: "client", TargetID: p.ID.String()})
	})
	if err != nil {
		return "", err
	}
	return token, nil
}
