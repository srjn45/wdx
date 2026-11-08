// Package account is the hub's users and every rule about them: the invites
// and password resets that let them in, their password and TOTP second
// factor, recovery codes and browser sessions. It knows nothing of HTTP. Every
// act it records writes its row of the audit chain in the act's own
// transaction (the wdx doc HUB_ACCOUNTS).
//
// A method that reads or changes one user's rows takes that user's id and
// touches no other's: a session or factor of another user is not found.
package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/srjn45/wdx/internal/hub/audit"
	"github.com/srjn45/wdx/internal/hub/seal"
	"github.com/srjn45/wdx/internal/sqlite"
)

// The errors of the store's rules. Their messages may be shown to the person
// who typed what was refused.
var (
	ErrNotFound           = errors.New("no such user")
	ErrInvalidInvite      = errors.New("the invite is unknown, used or expired")
	ErrRegistrationClosed = errors.New("signing up needs an invite")
	ErrUsernameTaken      = errors.New("the username is taken")
	// ErrWrongPassword is a password that is wrong or a username that does
	// not exist: the two are not told apart.
	ErrWrongPassword = errors.New("username or password is wrong")
	ErrDisabled      = errors.New("this account is disabled")
	ErrWrongCode     = errors.New("the code is wrong")
	ErrInvalidReset  = errors.New("the reset link is unknown, used or expired")
	// ErrNoSession is a browser session that is unknown, has expired or is
	// another user's.
	ErrNoSession       = errors.New("no such browser session")
	ErrFactorConfirmed = errors.New("an authenticator is enrolled already")
	ErrNoFactor        = errors.New("no authenticator is enrolled")
	ErrNoEnrolment     = errors.New("no authenticator is being enrolled")
)

// InvalidError is a value the rules do not allow: a username, a password, an
// invite's life.
type InvalidError struct {
	Field  string // username, password, expires
	Reason string
}

func (e *InvalidError) Error() string { return e.Field + " " + e.Reason }

// The audit actions of the acts.
const (
	ActionInvited              = "user.invited"
	ActionSignedUp             = "user.signed_up"
	ActionLoggedIn             = "user.logged_in"
	ActionFactorEnrolled       = "user.factor_enrolled"
	ActionFactorReplaced       = "user.factor_replaced"
	ActionRecoveryCodesRenewed = "user.recovery_codes_renewed"
	ActionPasswordChanged      = "user.password_changed"
	ActionResetMade            = "user.reset_made"
	ActionResetUsed            = "user.reset_used"
	ActionDisabled             = "user.disabled"
	ActionEnabled              = "user.enabled"
	ActionRemoved              = "user.removed"
	ActionSessionEnded         = "user.session_ended"
	ActionLockedOut            = "user.locked_out"
)

// By is who acts and from where, as the audit chain records them.
type By struct {
	Actor   audit.Actor
	Address string // the client's address; empty for the admin socket
}

// Admin is the operator, on the admin socket.
var Admin = By{Actor: audit.ActorAdmin}

// Options configure a Store.
type Options struct {
	// MasterKey seals the TOTP secrets: seal.KeySize bytes.
	MasterKey []byte
	// Now is the store's clock; time.Now when nil.
	Now func() time.Time
	// Argon2 hashes the passwords; OWASP's when zero. Tests lower it.
	Argon2 Argon2
	// Issuer names the hub in the person's authenticator; wdx-hub when
	// empty.
	Issuer string
}

// Store keeps the accounts in the hub's database.
type Store struct {
	db     *sql.DB
	key    []byte
	now    func() time.Time
	argon  Argon2
	issuer string
	// dummy is the hash a login for a username that does not exist checks
	// against, so that it takes as long as one that does.
	dummy func() string
}

// New returns the store of the accounts in db, the hub's database.
func New(db *sql.DB, opts Options) (*Store, error) {
	if len(opts.MasterKey) != seal.KeySize {
		return nil, fmt.Errorf("the master key is %d bytes, not %d", len(opts.MasterKey), seal.KeySize)
	}
	s := &Store{db: db, key: opts.MasterKey, now: opts.Now, argon: opts.Argon2, issuer: opts.Issuer}
	if s.now == nil {
		s.now = time.Now
	}
	if s.argon == (Argon2{}) {
		s.argon = OWASP
	}
	if s.issuer == "" {
		s.issuer = "wdx-hub"
	}
	s.dummy = sync.OnceValue(func() string {
		hash, err := s.argon.hash(rand.Text())
		if err != nil {
			panic(fmt.Sprintf("account: dummy hash: %v", err))
		}
		return hash
	})
	return s, nil
}

// The lives of what the store hands out.
const (
	// MaxInviteLife is the longest an invite may live.
	MaxInviteLife = 30 * 24 * time.Hour
	// ResetLife is how long a reset link works.
	ResetLife = 24 * time.Hour
	// SessionIdle ends a browser session not used for as long.
	SessionIdle = 12 * time.Hour
	// SessionMax ends a browser session this long after it started.
	SessionMax = 7 * 24 * time.Hour
	// seenEvery is how often a session's last_seen_at is written, at most.
	seenEvery = time.Minute
)

// newToken returns a token, 32 random bytes in base64url, and its hash as it
// is stored.
func newToken() (token string, hash []byte) {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // never fails
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashOf(token)
}

// hashOf is the SHA-256 of a token or a code. They are random, so they need
// no slow hash.
func hashOf(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

// secret passes a hash, a sealed value or a password hash to the database,
// logged as its length.
func secret[T string | []byte](v T) sqlite.Secret[T] { return sqlite.Secret[T]{V: v} }

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMS(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return time.UnixMilli(n.Int64).UTC()
}

// inTx runs fn in a transaction, committed when fn returns nil.
func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// record appends the act to the audit chain in tx.
func (s *Store) record(ctx context.Context, tx *sql.Tx, by By, userID, action string, details map[string]any) error {
	return audit.Append(ctx, tx, audit.Entry{At: s.now(), UserID: userID, Actor: by.Actor, Action: action, Address: by.Address, Details: details})
}
