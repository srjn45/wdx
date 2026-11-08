package account

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"errors"
	"regexp"
	"strings"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
	"github.com/pquerna/otp/totp"

	"github.com/srjn45/wdx/internal/hub/seal"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/sqlite"
)

// The TOTP the hub takes, as every authenticator app has it: SHA-1, 6 digits,
// 30-second steps, and the step before and after the current one accepted.
const (
	totpPeriod = 30
	totpDigits = otp.DigitsSix
	totpSkew   = 1
)

// RecoveryCodes is how many recovery codes a user has.
const RecoveryCodes = 10

// newKey returns a new TOTP key for username and its secret sealed for
// userID, so that a sealed secret copied to another user's row does not
// open.
func (s *Store) newKey(userID, username string) (*otp.Key, []byte, error) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: s.issuer, AccountName: username, Period: totpPeriod, Digits: totpDigits, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		return nil, nil, err
	}
	sealed, err := seal.Seal(s.key, []byte(key.Secret()), []byte(userID))
	if err != nil {
		return nil, nil, err
	}
	return key, sealed, nil
}

// usernameOf returns the username of userID, who must be neither removed
// nor disabled.
func usernameOf(ctx context.Context, q sqlite.Querier, userID string) (string, error) {
	var username string
	err := q.QueryRowContext(ctx, "SELECT username FROM users WHERE id = ? AND removed_at IS NULL AND disabled_at IS NULL", userID).Scan(&username)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return username, err
}

// EnrolTOTP starts enrolling an authenticator for userID, who has none
// confirmed: the key's QR code and secret are shown to the person, and
// ConfirmTOTP takes a code from it. Enrolling again replaces the key not
// confirmed.
func (s *Store) EnrolTOTP(ctx context.Context, userID string) (*otp.Key, error) {
	var key *otp.Key
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		username, err := usernameOf(ctx, tx, userID)
		if err != nil {
			return err
		}
		var confirmed int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM totp_factors WHERE user_id = ? AND confirmed_at IS NOT NULL", userID).Scan(&confirmed); err != nil {
			return err
		}
		if confirmed > 0 {
			return ErrFactorConfirmed
		}
		var sealed []byte
		if key, sealed, err = s.newKey(userID, username); err != nil {
			return err
		}
		if _, err := sqlite.Exec(ctx, tx, "INSERT INTO totp_factors (user_id, secret, created_at) VALUES (?, ?, ?) "+
			"ON CONFLICT (user_id) DO UPDATE SET secret = excluded.secret, pending_secret = NULL, last_step = 0, created_at = excluded.created_at",
			userID, secret(sealed), ms(s.now())); err != nil {
			return err
		}
		logging.FromContext(ctx).Info("authenticator enrolling", "user_id", userID)
		return nil
	})
	return key, err
}

// Confirmed is an authenticator just confirmed at enrolment: the session's
// new token, its second factor passed, and the recovery codes, shown once.
type Confirmed struct {
	Started
	RecoveryCodes []string
}

// ConfirmTOTP confirms the authenticator userID is enrolling with a code from
// it, gives them their recovery codes and gives the session sessionID its
// second factor.
func (s *Store) ConfirmTOTP(ctx context.Context, by By, userID, sessionID, code string) (Confirmed, error) {
	var c Confirmed
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var sealed []byte
		err := tx.QueryRowContext(ctx, "SELECT secret FROM totp_factors WHERE user_id = ? AND confirmed_at IS NULL", userID).Scan(&sealed)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoEnrolment
		}
		if err != nil {
			return err
		}
		step, err := s.check(ctx, userID, sealed, code, 0)
		if err != nil {
			return err
		}
		if _, err := sqlite.Exec(ctx, tx, "UPDATE totp_factors SET confirmed_at = ?, last_step = ? WHERE user_id = ?", ms(s.now()), step, userID); err != nil {
			return err
		}
		if c.RecoveryCodes, err = s.newRecoveryCodes(ctx, tx, userID); err != nil {
			return err
		}
		if err := s.record(ctx, tx, by, userID, ActionFactorEnrolled, map[string]any{"factor": FactorTOTP}); err != nil {
			return err
		}
		logging.FromContext(ctx).Info("authenticator enrolled", "user_id", userID)
		c.Started, err = s.passSecondFactor(ctx, tx, by, userID, sessionID, map[string]any{"recovery_code": false, "enrolled": true})
		return err
	})
	return c, err
}

// ReplaceTOTP starts replacing the confirmed authenticator of userID, the
// password given: the one in use keeps working until ConfirmReplacement
// takes a code from the new.
func (s *Store) ReplaceTOTP(ctx context.Context, userID, password string) (*otp.Key, error) {
	if err := s.checkPasswordOf(ctx, userID, password); err != nil {
		return nil, err
	}
	var key *otp.Key
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		username, err := usernameOf(ctx, tx, userID)
		if err != nil {
			return err
		}
		var sealed []byte
		if key, sealed, err = s.newKey(userID, username); err != nil {
			return err
		}
		n, err := sqlite.Exec(ctx, tx, "UPDATE totp_factors SET pending_secret = ? WHERE user_id = ? AND confirmed_at IS NOT NULL", secret(sealed), userID)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNoFactor
		}
		logging.FromContext(ctx).Info("authenticator replacing", "user_id", userID)
		return nil
	})
	return key, err
}

// ConfirmReplacement replaces the authenticator of userID with the one
// ReplaceTOTP started, given a code from it.
func (s *Store) ConfirmReplacement(ctx context.Context, by By, userID, code string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var sealed []byte
		err := tx.QueryRowContext(ctx, "SELECT pending_secret FROM totp_factors WHERE user_id = ? AND confirmed_at IS NOT NULL AND pending_secret IS NOT NULL", userID).Scan(&sealed)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoEnrolment
		}
		if err != nil {
			return err
		}
		// The new secret has had no code used: the old one's last step
		// says nothing of it.
		step, err := s.check(ctx, userID, sealed, code, 0)
		if err != nil {
			return err
		}
		if _, err := sqlite.Exec(ctx, tx, "UPDATE totp_factors SET secret = pending_secret, pending_secret = NULL, confirmed_at = ?, last_step = ? WHERE user_id = ?",
			ms(s.now()), step, userID); err != nil {
			return err
		}
		logging.FromContext(ctx).Info("authenticator replaced", "user_id", userID)
		return s.record(ctx, tx, by, userID, ActionFactorReplaced, map[string]any{"factor": FactorTOTP})
	})
}

// PassTOTP passes the second factor of the session sessionID of userID with
// a code from their authenticator, giving the session a new token.
func (s *Store) PassTOTP(ctx context.Context, by By, userID, sessionID, code string) (Started, error) {
	var started Started
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var sealed []byte
		var last int64
		err := tx.QueryRowContext(ctx, "SELECT secret, last_step FROM totp_factors WHERE user_id = ? AND confirmed_at IS NOT NULL", userID).Scan(&sealed, &last)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoFactor
		}
		if err != nil {
			return err
		}
		// A session that cannot take the code rolls its use back.
		step, err := s.check(ctx, userID, sealed, code, last)
		if err != nil {
			return err
		}
		if _, err := sqlite.Exec(ctx, tx, "UPDATE totp_factors SET last_step = ? WHERE user_id = ?", step, userID); err != nil {
			return err
		}
		started, err = s.passSecondFactor(ctx, tx, by, userID, sessionID, map[string]any{"recovery_code": false})
		return err
	})
	return started, err
}

var sixDigits = regexp.MustCompile(`^[0-9]{6}$`)

// check returns the 30-second step code is the code of, for the secret
// sealed for userID: the current step, the one before or the one after, and
// later than last, so that a code is used once. It fails with ErrWrongCode.
func (s *Store) check(ctx context.Context, userID string, sealed []byte, code string, last int64) (int64, error) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if !sixDigits.MatchString(code) {
		logging.FromContext(ctx).Info("code refused", "user_id", userID, "reason", "form")
		return 0, ErrWrongCode
	}
	plain, err := seal.Open(s.key, sealed, []byte(userID))
	if err != nil {
		return 0, err
	}
	now := s.now().Unix() / totpPeriod
	for step := now - totpSkew; step <= now+totpSkew; step++ {
		if step <= last {
			continue
		}
		want, err := hotp.GenerateCodeCustom(string(plain), uint64(step), hotp.ValidateOpts{Digits: totpDigits, Algorithm: otp.AlgorithmSHA1}) //nolint:gosec // a step after 1970 is positive
		if err != nil {
			return 0, err
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, nil
		}
	}
	logging.FromContext(ctx).Info("code refused", "user_id", userID, "reason", "code")
	return 0, ErrWrongCode
}

// crockford is the alphabet of recovery codes: base32 without I, L, O and U,
// which are read as 1, 1, 0 and V.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// newRecoveryCode returns a code of 10 characters shown as ABCDE-FGHJK.
func newRecoveryCode() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b) // never fails
	for i := range b {
		b[i] = crockford[b[i]&31]
	}
	return string(b[:5]) + "-" + string(b[5:])
}

// normalCode is a recovery code as typed, without its dash or spaces, in
// capitals, its letters read as the alphabet has them.
func normalCode(code string) string {
	code = strings.ToUpper(code)
	code = strings.NewReplacer("-", "", " ", "", "I", "1", "L", "1", "O", "0").Replace(code)
	return code
}

// newRecoveryCodes replaces the recovery codes of userID with RecoveryCodes
// new ones, and returns them.
func (s *Store) newRecoveryCodes(ctx context.Context, tx *sql.Tx, userID string) ([]string, error) {
	if _, err := sqlite.Exec(ctx, tx, "DELETE FROM recovery_codes WHERE user_id = ?", userID); err != nil {
		return nil, err
	}
	codes := make([]string, RecoveryCodes)
	for i := range codes {
		codes[i] = newRecoveryCode()
		if _, err := sqlite.Exec(ctx, tx, "INSERT INTO recovery_codes (user_id, code_hash) VALUES (?, ?)", userID, secret(hashOf(normalCode(codes[i])))); err != nil {
			return nil, err
		}
	}
	return codes, nil
}

// RenewRecoveryCodes gives userID, the password given, RecoveryCodes new
// recovery codes; the old ones stop working.
func (s *Store) RenewRecoveryCodes(ctx context.Context, by By, userID, password string) ([]string, error) {
	if err := s.checkPasswordOf(ctx, userID, password); err != nil {
		return nil, err
	}
	var codes []string
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM totp_factors WHERE user_id = ? AND confirmed_at IS NOT NULL", userID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNoFactor
		}
		var err error
		if codes, err = s.newRecoveryCodes(ctx, tx, userID); err != nil {
			return err
		}
		logging.FromContext(ctx).Info("recovery codes renewed", "user_id", userID)
		return s.record(ctx, tx, by, userID, ActionRecoveryCodesRenewed, nil)
	})
	return codes, err
}

// RecoveryCodesLeft returns how many recovery codes of userID are not used.
func (s *Store) RecoveryCodesLeft(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM recovery_codes WHERE user_id = ? AND used_at IS NULL", userID).Scan(&n)
	return n, err
}

// PassRecoveryCode passes the second factor of the session sessionID of
// userID with one of their recovery codes, which is used up, giving the
// session a new token. left is how many codes remain.
func (s *Store) PassRecoveryCode(ctx context.Context, by By, userID, sessionID, code string) (started Started, left int, err error) {
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		n, err := sqlite.Exec(ctx, tx, "UPDATE recovery_codes SET used_at = ? WHERE user_id = ? AND code_hash = ? AND used_at IS NULL "+
			"AND user_id IN (SELECT user_id FROM totp_factors WHERE confirmed_at IS NOT NULL)", ms(s.now()), userID, secret(hashOf(normalCode(code))))
		if err != nil {
			return err
		}
		if n == 0 {
			logging.FromContext(ctx).Info("code refused", "user_id", userID, "reason", "recovery_code")
			return ErrWrongCode
		}
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM recovery_codes WHERE user_id = ? AND used_at IS NULL", userID).Scan(&left); err != nil {
			return err
		}
		started, err = s.passSecondFactor(ctx, tx, by, userID, sessionID, map[string]any{"recovery_code": true, "recovery_codes_left": left})
		return err
	})
	return started, left, err
}

// EnrolmentKey returns the key userID is enrolling and has not confirmed, so
// that the page asking for its code can show it again; ErrNoEnrolment when
// there is none.
func (s *Store) EnrolmentKey(ctx context.Context, userID string) (*otp.Key, error) {
	return s.pendingKey(ctx, userID, "SELECT secret FROM totp_factors WHERE user_id = ? AND confirmed_at IS NULL")
}

// ReplacementKey returns the key ReplaceTOTP started for userID and
// ConfirmReplacement has not taken; ErrNoEnrolment when there is none.
func (s *Store) ReplacementKey(ctx context.Context, userID string) (*otp.Key, error) {
	return s.pendingKey(ctx, userID, "SELECT pending_secret FROM totp_factors WHERE user_id = ? AND confirmed_at IS NOT NULL AND pending_secret IS NOT NULL")
}

func (s *Store) pendingKey(ctx context.Context, userID, query string) (*otp.Key, error) {
	username, err := usernameOf(ctx, s.db, userID)
	if err != nil {
		return nil, err
	}
	var sealed []byte
	err = s.db.QueryRowContext(ctx, query, userID).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoEnrolment
	}
	if err != nil {
		return nil, err
	}
	plain, err := seal.Open(s.key, sealed, []byte(userID))
	if err != nil {
		return nil, err
	}
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(string(plain))
	if err != nil {
		return nil, err
	}
	return totp.Generate(totp.GenerateOpts{Issuer: s.issuer, AccountName: username, Period: totpPeriod, Digits: totpDigits, Algorithm: otp.AlgorithmSHA1, Secret: raw})
}
