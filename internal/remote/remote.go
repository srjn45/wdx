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
	"net"
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
	// ReasonNodeRemoved is the other node removed from the account.
	ReasonNodeRemoved Reason = "node_removed"
	// ReasonCutOff is this node cut off by the hub for good: removed from
	// the account, or the account disabled.
	ReasonCutOff Reason = "cut_off"
)

// The waits after a wrong code: the first, doubled at each wrong code after
// it, up to the last. A client node's own wait goes on doubling past the
// last after wrongInARow wrong codes in a row, up to lastNodeWait: a thief
// holding one node then gets about 24 guesses a day. The host's total,
// against a hub that invents node ids, stops at the last.
const (
	firstWrongWait = time.Second
	lastWrongWait  = 5 * time.Minute
	wrongInARow    = 10
	lastNodeWait   = time.Hour
)

// confirmTimeout bounds the wait for a client's confirm, which holds the
// host's open lock: a confirm that does not come counts as a wrong code.
const confirmTimeout = 10 * time.Second

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
	// ServeStream, when not nil, serves conn, a stream of the session s
	// into this node, until it is done with it; the Remote closes conn when
	// the session closes. Without it, a stream ends once opened.
	ServeStream func(ctx context.Context, conn net.Conn, s Session)
	// Changed, when not nil, is called after a session opened or closed,
	// either way; it must not block.
	Changed func()
	// idleChecked, when not nil, is called each time the idle timer has
	// looked, for tests to wait on.
	idleChecked func()
	// linkCut, when not nil, is called in place of CutOff once the link
	// is cut off, for tests.
	linkCut func(ctx context.Context)
	// confirmTimeout, when not 0, replaces confirmTimeout, for tests.
	confirmTimeout time.Duration
}

// Remote is the node's sessions, both ways.
type Remote struct {
	opts    Options
	ctx     context.Context // its life, with the daemon's logger
	stop    context.CancelFunc
	done    chan struct{} // the idle timer ended
	watched chan struct{} // the link's watch ended

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

	// streams are the streams of each incoming session being served, by
	// its id, which close with it.
	streams map[string]map[net.Conn]struct{}

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
	if opts.confirmTimeout == 0 {
		opts.confirmTimeout = confirmTimeout
	}
	ctx, stop := context.WithCancel(ctx)
	r := &Remote{opts: opts, ctx: ctx, stop: stop, done: make(chan struct{}), watched: make(chan struct{}), slowed: map[string]*slowdown{}, streams: map[string]map[net.Conn]struct{}{}, kicked: make(chan struct{}, 1)}
	if _, err := r.closeWhere(ctx, ReasonRestart, "direction = 'incoming'"); err != nil {
		stop()
		return nil, err
	}
	go r.keepIdle()
	go r.watchLink()
	return r, nil
}

// Close stops the idle timer and the link's watch, and waits for them.
func (r *Remote) Close() {
	r.stop()
	<-r.done
	<-r.watched
}

// cutOff reports whether the hub cut this node off for good.
func cutOff(c hublink.Connection) bool {
	return c == hublink.Revoked || c == hublink.Disabled
}

// watchLink calls CutOff each time the link is cut off, until the Remote
// closes.
func (r *Remote) watchLink() {
	defer close(r.watched)
	log := logging.FromContext(r.ctx)
	cut := r.opts.linkCut
	if cut == nil {
		cut = func(ctx context.Context) {
			if err := r.CutOff(ctx); err != nil && ctx.Err() == nil {
				log.Error("sessions not closed", "reason", ReasonCutOff, "err", err)
			}
		}
	}
	was := false
	for {
		s, changed := r.opts.Link.Watch()
		now := cutOff(s.Connection)
		if now && !was {
			log.Info("node cut off by the hub", "state", string(s.Connection))
			cut(r.ctx)
		}
		was = now
		select {
		case <-r.ctx.Done():
			return
		case <-changed:
		}
	}
}

// CutOff closes every session, both ways, for ReasonCutOff, and so cuts
// their streams: the hub removed this node from its account or disabled
// the account, so no other node can be told, and none may go on.
func (r *Remote) CutOff(ctx context.Context) error {
	_, err := r.closeWhere(ctx, ReasonCutOff, "1")
	return err
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

// LoggingOut tells the host of every session open from this node that it
// closed, best effort, while the hub still carries the streams to them:
// once logged out, a host could not be told, and would keep the session
// open until it went idle.
func (r *Remote) LoggingOut(ctx context.Context) {
	list, err := r.Sessions(ctx)
	if err != nil {
		logging.FromContext(ctx).Error("hosts not told of the logout", "err", err)
		return
	}
	for _, s := range list {
		if s.Direction == Outgoing {
			r.tellClosed(ctx, s)
		}
	}
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
	wait := s.add(now, lastNodeWait)
	return max(wait, r.total.add(now, lastWrongWait))
}

// add counts a wrong code at now and returns the wait it sets: doubled at
// each, up to lastWrongWait, then past it after wrongInARow, up to last.
func (s *slowdown) add(now time.Time, last time.Duration) time.Duration {
	s.wrong++
	wait := min(firstWrongWait<<min(s.wrong-1, 16), lastWrongWait)
	if s.wrong > wrongInARow {
		wait = min(lastWrongWait<<min(s.wrong-wrongInARow, 16), last)
	}
	s.tryAfter = now.Add(wait)
	return wait
}

// rightCode ends the node's wait and the host's total; another node's wait
// stays.
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

// carry keeps conn among the streams of the session id, either way, which
// closing it closes, and reports whether the session is open still: it may have
// closed since the stream was checked.
func (r *Remote) carry(id string, conn net.Conn) bool {
	r.mu.Lock()
	if r.streams[id] == nil {
		r.streams[id] = map[net.Conn]struct{}{}
	}
	r.streams[id][conn] = struct{}{}
	r.mu.Unlock()
	var open bool
	err := r.opts.DB.QueryRowContext(r.ctx, "SELECT closed_at IS NULL FROM hub_sessions WHERE id = ?", id).Scan(&open)
	if err != nil || !open {
		r.dropped(id, conn)
		return false
	}
	return true
}

// dropped forgets conn, a stream of the session id that ended.
func (r *Remote) dropped(id string, conn net.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.streams[id], conn)
	if len(r.streams[id]) == 0 {
		delete(r.streams, id)
	}
}

// cut closes the streams of the sessions closed, whatever they carry, and
// forgets them.
func (r *Remote) cut(ctx context.Context, closed []Session) {
	var conns []net.Conn
	r.mu.Lock()
	for _, s := range closed {
		for conn := range r.streams[s.ID] {
			conns = append(conns, conn)
		}
		delete(r.streams, s.ID)
	}
	r.mu.Unlock()
	// Outside the lock: closing a TLS connection may wait on its peer.
	for _, conn := range conns {
		conn.Close()
	}
	if len(conns) > 0 {
		logging.FromContext(ctx).Info("session streams cut", "streams", len(conns))
	}
}

// changed says sessions opened or closed.
func (r *Remote) changed() {
	if r.opts.Changed != nil {
		r.opts.Changed()
	}
}

// asError is errors.AsType for *Error.
func asError(err error) (*Error, bool) { return errors.AsType[*Error](err) }
