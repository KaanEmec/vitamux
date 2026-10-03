package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// TOTP is RFC 6238 with the authenticator-app defaults (SHA-1, 6 digits, 30 s), accepting
// one step of clock skew either way. A step is accepted at most once.
const (
	totpIssuer    = "Vitamux"
	totpPeriod    = 30
	recoveryCount = 10
	recoveryBytes = 10 // 80 bits, so a plain SHA-256 at rest is enough
)

var (
	ErrTOTPEnabled    = errors.New("auth: TOTP is already enabled")
	ErrTOTPNotPending = errors.New("auth: no pending TOTP enrolment")
	ErrTOTPNotEnabled = errors.New("auth: TOTP is not enabled")
)

var totpOpts = totp.ValidateOpts{Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}

// Enrollment is a new, unconfirmed TOTP secret; it is shown once.
type Enrollment struct {
	Secret string `json:"secret"`
	URI    string `json:"otpauth_uri"`
}

// EnrollTOTP stores a new pending secret, replacing any earlier unconfirmed one. TOTP stays
// off until ConfirmTOTP proves the authenticator works.
func (s *Service) EnrollTOTP(ctx context.Context, userID uuid.UUID) (Enrollment, error) {
	u, err := s.db.Q().GetUserByID(ctx, userID)
	if err != nil {
		return Enrollment{}, db.MapErr(err)
	}
	if u.TotpEnabledAt != nil {
		return Enrollment{}, ErrTOTPEnabled
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: totpIssuer, AccountName: u.Username, Period: totpPeriod})
	if err != nil {
		return Enrollment{}, err
	}
	sealed, err := s.keys.Seal(crypto.Credentials, []byte(key.Secret()), crypto.TOTPAAD(userID))
	if err != nil {
		return Enrollment{}, err
	}
	n, err := s.db.Q().SetPendingTOTP(ctx, dbq.SetPendingTOTPParams{TotpCiphertext: sealed, TotpKeyID: s.keys.KeyID(), ID: userID})
	if err != nil {
		return Enrollment{}, db.MapErr(err)
	}
	if n == 0 {
		return Enrollment{}, ErrTOTPEnabled // enabled concurrently
	}
	return Enrollment{Secret: key.Secret(), URI: key.URL()}, nil
}

// ConfirmTOTP enables the pending secret if code matches it, and returns fresh recovery
// codes, shown once.
func (s *Service) ConfirmTOTP(ctx context.Context, by *Principal, code string) ([]string, error) {
	u, err := s.db.Q().GetUserByID(ctx, by.UserID)
	if err != nil {
		return nil, db.MapErr(err)
	}
	if u.TotpEnabledAt != nil {
		return nil, ErrTOTPEnabled
	}
	if u.TotpCiphertext == nil {
		return nil, ErrTOTPNotPending
	}
	step, ok, err := s.matchTOTP(u, code)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrInvalidCredentials
	}
	codes := make([]string, recoveryCount)
	for i := range codes {
		if codes[i], err = newRecoveryCode(); err != nil {
			return nil, err
		}
	}
	err = s.db.Tx(ctx, func(q *dbq.Queries) error {
		n, err := q.EnableTOTP(ctx, dbq.EnableTOTPParams{Now: s.now(), Step: step, ID: u.ID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrTOTPNotPending
		}
		if err := q.DeleteRecoveryCodes(ctx, u.ID); err != nil {
			return err
		}
		for _, c := range codes {
			if err := q.InsertRecoveryCode(ctx, dbq.InsertRecoveryCodeParams{UserID: u.ID, CodeHash: recoveryHash(c)}); err != nil {
				return err
			}
		}
		return audit.Record(ctx, q, audit.Event{UserID: &u.ID, Actor: by.Actor(), Action: "auth.totp_enable", TargetType: "user", TargetID: u.ID.String()})
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// DisableTOTP turns TOTP off after re-checking the password and a current TOTP or recovery
// code, and deletes the recovery codes.
func (s *Service) DisableTOTP(ctx context.Context, by *Principal, password, totpCode, recoveryCode string) error {
	u, err := s.db.Q().GetUserByID(ctx, by.UserID)
	if err != nil {
		return db.MapErr(err)
	}
	if u.TotpEnabledAt == nil {
		return ErrTOTPNotEnabled
	}
	ok, err := VerifyPassword(u.PasswordHash, password)
	if err == nil && ok {
		switch {
		case totpCode != "":
			ok, err = s.useTOTP(ctx, u, totpCode)
		case recoveryCode != "":
			ok, err = s.useRecoveryCode(ctx, u.ID, recoveryCode)
		default:
			ok = false
		}
	}
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidCredentials
	}
	return s.db.Tx(ctx, func(q *dbq.Queries) error {
		if err := q.DisableTOTP(ctx, u.ID); err != nil {
			return err
		}
		if err := q.DeleteRecoveryCodes(ctx, u.ID); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &u.ID, Actor: by.Actor(), Action: "auth.totp_disable", TargetType: "user", TargetID: u.ID.String()})
	})
}

// matchTOTP returns the time step whose code equals code, within one step of now.
func (s *Service) matchTOTP(u dbq.User, code string) (int64, bool, error) {
	secret, err := s.keys.Open(crypto.Credentials, u.TotpCiphertext, crypto.TOTPAAD(u.ID))
	if err != nil {
		return 0, false, err
	}
	defer clear(secret)
	code = strings.TrimSpace(code)
	now := s.now().Unix() / totpPeriod
	for step := now - 1; step <= now+1; step++ {
		want, err := totp.GenerateCodeCustom(string(secret), timeOfStep(step), totpOpts)
		if err != nil {
			return 0, false, err
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true, nil
		}
	}
	return 0, false, nil
}

// useTOTP accepts code only for a step later than the last accepted one, atomically.
func (s *Service) useTOTP(ctx context.Context, u dbq.User, code string) (bool, error) {
	step, ok, err := s.matchTOTP(u, code)
	if err != nil || !ok {
		return false, err
	}
	n, err := s.db.Q().UseTOTPStep(ctx, dbq.UseTOTPStepParams{Step: step, ID: u.ID})
	return n == 1, db.MapErr(err)
}

func (s *Service) useRecoveryCode(ctx context.Context, userID uuid.UUID, code string) (bool, error) {
	n, err := s.db.Q().UseRecoveryCode(ctx, dbq.UseRecoveryCodeParams{Now: s.now(), UserID: userID, CodeHash: recoveryHash(code)})
	if err != nil {
		return false, db.MapErr(err)
	}
	if n == 1 {
		err = audit.Record(ctx, s.db.Q(), audit.Event{UserID: &userID, Actor: audit.Owner, Action: "auth.recovery_code_used", TargetType: "user", TargetID: userID.String()})
	}
	return n == 1, err
}

// newRecoveryCode returns a code such as "abcd-efgh-ijkl-mnop" (base32, 80 bits).
func newRecoveryCode() (string, error) {
	b := make([]byte, recoveryBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	s := strings.ToLower(base32.StdEncoding.EncodeToString(b))
	return s[0:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:16], nil
}

// recoveryHash ignores case, spaces and dashes so codes can be typed loosely.
func recoveryHash(code string) []byte {
	norm := strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		return r
	}, strings.ToLower(code))
	sum := sha256.Sum256([]byte(norm))
	return sum[:]
}

func timeOfStep(step int64) time.Time { return time.Unix(step*totpPeriod, 0) }
