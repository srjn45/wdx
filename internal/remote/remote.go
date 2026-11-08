// Package remote is remote access between two nodes of one account (the wdx
// doc HUB_TUNNEL): TLS 1.3 between the nodes' own keys over a stream the hub
// relays and cannot read, each node pinning the other's key; the session the
// host opens to a client that proves, through CPace keyed with the host's
// authenticator code and bound to that TLS channel, that it holds the code
// now; the sessions both ways, the host's idle timer, and the slowing of
// wrong codes.
//
// Nothing here is ever logged of a code, CPace's messages or key, the MACs,
// or a stream's bytes.
package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/srjn45/wdx/internal/authcode"
	"github.com/srjn45/wdx/internal/hublink"
	"github.com/srjn45/wdx/internal/logging"
)

// Code is why a node refused, or a session failed, as the API names it.
type Code string

const (
	CodeRemoteAccessOff        Code = "remote_access_off"
	CodeCodeNotSetUp           Code = "code_not_set_up"
	CodeCodeWrong              Code = "code_wrong"
	CodeNodeKeyMismatch        Code = "node_key_mismatch"
	CodeNodeVersionUnsupported Code = "node_version_unsupported"
	CodeSessionClosed          Code = "session_closed"
	CodeSessionNotFound        Code = "session_not_found"
)

// Error is a refusal, with its code.
type Error struct {
	Code Code
	Err  error
	// Details are what the API gives with it; nil for none.
	Details map[string]any
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

func refusal(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Err: fmt.Errorf(format, args...)}
}

// Direction is which way a session goes: Incoming is another node's into
// this one, Outgoing this node's into another.
type Direction string

const (
	Incoming Direction = "incoming"
	Outgoing Direction = "outgoing"
)

// Reason is why a session closed.
type Reason string

const (
	ReasonClosed          Reason = "closed"
	ReasonIdle            Reason = "idle"
	ReasonRestart         Reason = "restart"
	ReasonRemoteAccessOff Reason = "remote_access_off"
	ReasonLoggedOut       Reason = "logged_out"
)

// The waits after a wrong code: the first, doubled at each wrong code after
// it, up to the last.
const (
	firstWrongWait = time.Second
	lastWrongWait  = 5 * time.Minute
)

// exchangeTimeout bounds a stream's TLS handshake and the messages that
// follow it, so that a stream nobody speaks on ends.
const exchangeTimeout = 30 * time.Second

// Options are what a Remote works with; tests change Exporter.
type Options struct {
	DB   *sql.DB
	Link *hublink.Link
	// Code is the host's authenticator code, in <home>/hub/.
	Code  *authcode.Files
	Clock hublink.Clock
	// RemoteAccess is hub.remote_access now, IdleTimeout
	// hub.session_idle_timeout now.
	RemoteAccess func() bool
	IdleTimeout  func() time.Duration
	// Exporter is the TLS exporter value both sides bind CPace to; nil is
	// the connection's own. Tests give a side another connection's.
	Exporter func(tls.ConnectionState) ([]byte, error)
	// idleChecked, when not nil, is called each time the idle timer has
	// looked, for tests to wait on.
	idleChecked func()
}

// Remote is the node's sessions, both ways.
type Remote struct {
	opts Options
	ctx  context.Context // its life, with the daemon's logger
	stop context.CancelFunc
	done chan struct{} // the idle timer ended

	// opening is one session opened into this node at a time, so that the
	// waits after wrong codes count every one: a code checked at once
	// beside another would not wait.
	opening sync.Mutex
	// clientOpening is one session opened from this node at a time, so
	// that it keeps one per host.
	clientOpening sync.Mutex

	mu sync.Mutex
	// slowed is the waits after wrong codes per client node, total the
	// host's own across them all.
	slowed map[string]*slowdown
	total  slowdown

	// kicked says the idle timer should look again: a session opened, or
	// the timeout changed.
	kicked chan struct{}
}

// slowdown is the wrong codes since the last right one and when the next
// may be tried.
type slowdown struct {
	wrong    int
	tryAfter time.Time
}

// New closes every incoming session, which a restart ends, and starts the
// idle timer, until Close.
func New(ctx context.Context, opts Options) (*Remote, error) {
	if opts.Exporter == nil {
		opts.Exporter = exporter
	}
	ctx, stop := context.WithCancel(ctx)
	r := &Remote{opts: opts, ctx: ctx, stop: stop, done: make(chan struct{}), slowed: map[string]*slowdown{}, kicked: make(chan struct{}, 1)}
	if _, err := r.closeWhere(ctx, ReasonRestart, "direction = 'incoming'"); err != nil {
		stop()
		return nil, err
	}
	go r.keepIdle()
	return r, nil
}

// Close stops the idle timer and waits for it.
func (r *Remote) Close() {
	r.stop()
	<-r.done
}

// Kick has the idle timer look again, as after the timeout changed.
func (r *Remote) Kick() {
	select {
	case r.kicked <- struct{}{}:
	default:
	}
}

// Apply takes the hub.* settings as they are now: with remote access off,
// every incoming session closes.
func (r *Remote) Apply(ctx context.Context) {
	if !r.opts.RemoteAccess() {
		if _, err := r.closeWhere(ctx, ReasonRemoteAccessOff, "direction = 'incoming'"); err != nil {
			logging.FromContext(ctx).Error("incoming sessions not closed", "reason", ReasonRemoteAccessOff, "err", err)
		}
	}
	r.Kick()
}

// LoggedOut closes every session both ways and forgets every node's key, so
// that a key pinned under one account never carries over to the next.
func (r *Remote) LoggedOut(ctx context.Context) error {
	if _, err := r.closeWhere(ctx, ReasonLoggedOut, "1"); err != nil {
		return err
	}
	if err := r.forgetKeys(ctx); err != nil {
		return err
	}
	r.mu.Lock()
	r.slowed, r.total = map[string]*slowdown{}, slowdown{}
	r.mu.Unlock()
	return nil
}

// tooSoon is how long the client node must still wait after wrong codes,
// its own or anyone's; 0 when it need not.
func (r *Remote) tooSoon(nodeID string, now time.Time) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	wait := r.total.tryAfter.Sub(now)
	if s := r.slowed[nodeID]; s != nil {
		wait = max(wait, s.tryAfter.Sub(now))
	}
	return max(wait, 0)
}

// wrongCode counts a wrong code from nodeID and returns the wait it sets.
func (r *Remote) wrongCode(nodeID string, now time.Time) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.slowed[nodeID]
	if s == nil {
		s = &slowdown{}
		r.slowed[nodeID] = s
	}
	wait := s.add(now)
	return max(wait, r.total.add(now))
}

func (s *slowdown) add(now time.Time) time.Duration {
	s.wrong++
	wait := min(firstWrongWait<<min(s.wrong-1, 16), lastWrongWait)
	s.tryAfter = now.Add(wait)
	return wait
}

// rightCode ends the waits, the node's and the host's.
func (r *Remote) rightCode(nodeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.slowed, nodeID)
	r.total = slowdown{}
}

// self is this node, its public key, as TLS between nodes presents it.
type self struct {
	node hublink.NodeRef
	key  ed25519.PublicKey
}

// identity is this node and its TLS certificate, of its own key.
func (r *Remote) identity() (self, tls.Certificate, error) {
	node, key, err := r.opts.Link.Identity()
	if err != nil {
		return self{}, tls.Certificate{}, err
	}
	cert, err := certificate(key)
	return self{node: node, key: key.Public().(ed25519.PublicKey)}, cert, err
}

// asError is errors.AsType for *Error.
func asError(err error) (*Error, bool) { return errors.AsType[*Error](err) }
