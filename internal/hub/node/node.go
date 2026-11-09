// Package node is the hub's nodes, the machines of its accounts, and every
// rule about them: the logins that make one, by the browser flow (an
// authorization code with PKCE, delivered to a loopback callback) or the
// device flow (a code the person types), the nodes themselves, scoped to
// their user, and the registry of their open control connections, which cuts
// a node off when it is revoked or its user disabled or removed. It knows
// nothing of HTTP or of certificates: what issues a node's certificate is
// handed in. Every act it records writes its row of the audit chain in the
// act's own transaction (the wdx doc HUB_LOGIN).
package node

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"regexp"
	"time"

	"github.com/srjn45/wdx/internal/hub/audit"
	"github.com/srjn45/wdx/internal/sqlite"
)

// The errors of the store's rules. Their messages may be shown to the person
// who typed what was refused.
var (
	// ErrLoginNotFound is a login, a code or a device code that is unknown,
	// or was used already.
	ErrLoginNotFound = errors.New("no such login: it is unknown or was used already")
	ErrLoginExpired  = errors.New("the login expired")
	// ErrLoginDone is a login approved or denied already.
	ErrLoginDone     = errors.New("the login was approved or denied already")
	ErrNameTaken     = errors.New("the account has a node of that name already")
	ErrWrongVerifier = errors.New("the PKCE verifier is not the login's")
	// ErrPending, ErrSlowDown and ErrDenied are a device login's states
	// as its polling is told them (RFC 8628).
	ErrPending  = errors.New("the login is not approved yet")
	ErrSlowDown = errors.New("polled faster than the login's interval")
	ErrDenied   = errors.New("the login was denied")
	// ErrNotFound is a node that is not the user's, or was removed.
	ErrNotFound = errors.New("no such node")
	// ErrRevoked is a node removed, or never made; ErrDisabled one whose
	// user is disabled.
	ErrRevoked  = errors.New("the node was removed from its account")
	ErrDisabled = errors.New("the node's account is disabled")
)

// InvalidError is a value the rules do not allow: a node's name, its key, a
// callback.
type InvalidError struct {
	Field  string // name, public_key, callback, pkce_challenge, flow, os, wdx_version
	Reason string
}

func (e *InvalidError) Error() string { return e.Field + " " + e.Reason }

// The audit actions of the acts.
const (
	ActionLoginStarted       = "node.login_started"
	ActionApproved           = "node.approved"
	ActionDenied             = "node.denied"
	ActionRemoved            = "node.removed"
	ActionLoggedOut          = "node.logged_out"
	ActionCertificateRenewed = "node.certificate_renewed"
	ActionCutOff             = "node.cut_off"
)

// By is who acts and from where, as the audit chain records them: a user
// from a browser, a user through one of their nodes, the admin.
type By struct {
	Actor   audit.Actor
	Address string // the client's address; empty for the admin socket
	// NodeID is the node acted through; empty for none.
	NodeID string
}

// Admin is the operator, on the admin socket.
var Admin = By{Actor: audit.ActorAdmin}

// The lives and rates of the logins.
const (
	// LoginLife is how long a login may take, from its start to the node's
	// credentials.
	LoginLife = 10 * time.Minute
	// PollInterval is how often a device login may be polled.
	PollInterval = 5 * time.Second
)

// Options configure a Store.
type Options struct {
	// Now is the store's clock; time.Now when nil.
	Now func() time.Time
	// Removed, when not nil, is the message the other nodes of a node's
	// user are sent once it is removed, which the server writes in the
	// hub's API: they close their sessions with it.
	Removed func(Node) any
}

// Store keeps the nodes and their logins in the hub's database, and the
// registry of their control connections in memory.
type Store struct {
	db       *sql.DB
	now      func() time.Time
	removed  func(Node) any
	registry *Registry
}

// New returns the store of the nodes in db, the hub's database.
func New(db *sql.DB, opts Options) *Store {
	s := &Store{db: db, now: opts.Now, removed: opts.Removed, registry: newRegistry()}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

// Registry is the store's registry of control connections.
func (s *Store) Registry() *Registry { return s.registry }

// namePattern is what a node's name may be: it goes in commands and paths,
// and never holds the colon of id:.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// CheckName fails with an *InvalidError when name cannot be a node's.
func CheckName(name string) error {
	if !namePattern.MatchString(name) {
		return &InvalidError{Field: "name", Reason: "must be 1 to 64 letters, digits, ., _ and -, starting with a letter or digit"}
	}
	return nil
}

// Fingerprint is how a node's key is shown: SHA256: and its SHA-256 in
// base64 without padding.
func Fingerprint(pub []byte) string {
	sum := sha256.Sum256(pub)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// newToken returns a code, 32 random bytes in base64url, and its hash as it
// is stored.
func newToken() (token string, hash []byte) {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // never fails
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashOf(token)
}

// hashOf is the SHA-256 of a code. The device code and the authorization
// code are random; the user code is short, but lives 10 minutes, and its
// tries are limited.
func hashOf(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

// secret passes a hash to the database, logged as its length.
func secret(v []byte) sqlite.Secret[[]byte] { return sqlite.Secret[[]byte]{V: v} }

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
func (s *Store) record(ctx context.Context, tx *sql.Tx, by By, userID, nodeID, action string, details map[string]any) error {
	return audit.Append(ctx, tx, audit.Entry{At: s.now(), UserID: userID, NodeID: nodeID, Actor: by.Actor, Action: action, Address: by.Address, Details: details})
}
