// Package hublink is a node's side of the hub (the wdx doc HUB_LOGIN): the
// login kept in <home>/hub/, the two ways of logging in, the control
// connection the daemon holds to the hub's node endpoint with the renewal of
// the node's certificate, the hub's routes of the account's nodes, and the
// pusher of the docs' outbox while connected (the wdx doc HUB_DOCS_PUSH).
//
// The hub's web endpoint is trusted through the system's store, or reached
// over plain HTTP on localhost and 127.0.0.1 only; the node endpoint through
// the hub's CA alone, as the login gave it. Nothing here waits on the hub
// for the daemon: opening a Link, and every status it gives, return at once
// whether the hub is there or not.
package hublink

import (
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

// The timings of a link, unless its Options say otherwise.
const (
	// DefaultMinBackoff is the wait before the first reconnection, doubled
	// at each failure up to DefaultMaxBackoff.
	DefaultMinBackoff = time.Second
	DefaultMaxBackoff = 5 * time.Minute
	// DefaultLogoutTimeout is how long logging out waits for the hub to
	// hear of it.
	DefaultLogoutTimeout = 5 * time.Second
	// DefaultPingInterval is how often the node pings the hub, which tells
	// it the hub is gone when no pong comes within the interval.
	DefaultPingInterval = 30 * time.Second
	// requestTimeout is how long a request to the web endpoint may take.
	requestTimeout = 30 * time.Second
	// DefaultSetupTimeout is how long reaching another node may take: asking
	// the hub for a stream and joining it, or joining one the hub says waits.
	DefaultSetupTimeout = 30 * time.Second
	// dialTimeout is how long opening the control connection may take.
	dialTimeout = 30 * time.Second
	// nodeDialTimeout and nodeHeaderTimeout bound connecting to the node
	// endpoint and waiting for its answer's headers; nothing bounds a whole
	// request, as a stream outlives it.
	nodeDialTimeout   = 10 * time.Second
	nodeHeaderTimeout = 15 * time.Second
)

// Clock is the time a link goes by: when logins expire and how long it
// waits between polls and reconnections. Tests replace it.
type Clock interface {
	Now() time.Time
	// After sends once d has passed.
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Options are a link's settings, which tests change; the zero value is the
// defaults.
type Options struct {
	Clock Clock
	// HTTPClient calls the hub's web endpoint; by default one trusting the
	// system's store. The node endpoint has a client of its own, trusting
	// the hub's CA alone.
	HTTPClient *http.Client
	// MinBackoff and MaxBackoff bound the wait between two tries at the
	// control connection.
	MinBackoff, MaxBackoff time.Duration
	LogoutTimeout          time.Duration
	PingInterval           time.Duration
	// SetupTimeout bounds reaching another node; see DefaultSetupTimeout.
	SetupTimeout time.Duration
	// Outbox is the docs' changes the link pushes to the hub while
	// connected; nil pushes none.
	Outbox Outbox
	// NodeRemoved, when not nil, is given each node the hub says was
	// removed from the account, and AccountNodes the account's nodes as the
	// hub lists them after each welcome, this one among them, so that the
	// node also learns of those removed while it was not connected. Either
	// may be called again with what it was given before.
	NodeRemoved  func(ctx context.Context, n NodeRef)
	AccountNodes func(ctx context.Context, nodes []NodeRef)
	// WrapNodes, when not nil, wraps the transport to the node endpoint:
	// tests lose the hub's answers with it.
	WrapNodes func(http.RoundTripper) http.RoundTripper
}

func (o Options) withDefaults() Options {
	if o.Clock == nil {
		o.Clock = realClock{}
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: requestTimeout}
	}
	o.MinBackoff = cmp.Or(o.MinBackoff, DefaultMinBackoff)
	o.MaxBackoff = max(cmp.Or(o.MaxBackoff, DefaultMaxBackoff), o.MinBackoff)
	o.LogoutTimeout = cmp.Or(o.LogoutTimeout, DefaultLogoutTimeout)
	o.PingInterval = cmp.Or(o.PingInterval, DefaultPingInterval)
	o.SetupTimeout = cmp.Or(o.SetupTimeout, DefaultSetupTimeout)
	return o
}

// Connection is the state of the node's control connection.
type Connection string

const (
	Connected  Connection = "connected"
	Connecting Connection = "connecting"
	// Offline is a connection lost or refused, which is tried again.
	Offline   Connection = "offline"
	LoggedOut Connection = "logged_out"
	// Revoked and Disabled are for good, until logout: the node was removed
	// from its account, or the account disabled.
	Revoked  Connection = "revoked"
	Disabled Connection = "disabled"
)

// NodeRef names a node.
type NodeRef struct {
	ID, Name string
}

// Status is what the link knows of the hub.
type Status struct {
	// URL is the hub's web endpoint, User the account's username and Node
	// this node; all empty when logged out.
	URL, User  string
	Node       NodeRef
	Connection Connection
	// HubVersion is the hub's version as its last welcome said, empty
	// until one came.
	HubVersion string
	// InRange is whether the hub serves the API this wdx speaks, nil until
	// the hub said; Behind, when it does not, names the side to update:
	// hubapi.BehindNode or hubapi.BehindHub.
	InRange *bool
	Behind  string
}

// LoggedIn reports whether the node is logged in, connected or not.
func (s Status) LoggedIn() bool { return s.Connection != LoggedOut }

var (
	// ErrNotLoggedIn is an act that needs a login, asked with none.
	ErrNotLoggedIn = errors.New("this node is not logged in to a hub")
	// ErrAlreadyLoggedIn is a login started while logged in.
	ErrAlreadyLoggedIn = errors.New("this node is logged in to a hub already")
	// ErrNodeIsThis is removing this node, which is logging out.
	ErrNodeIsThis = errors.New("the node is this one: log out instead")
	// ErrClosed is an act on a link closed.
	ErrClosed = errors.New("the link is closed")
)

// UnreachableError is a hub that could not be reached, or did not answer
// as a hub does.
type UnreachableError struct {
	URL string
	Err error
}

func (e *UnreachableError) Error() string {
	return fmt.Sprintf("the hub at %s cannot be reached: %v", e.URL, e.Err)
}
func (e *UnreachableError) Unwrap() error { return e.Err }

// HubError is a request the hub refused, with its problem.
type HubError struct {
	Problem hubapi.Problem
}

func (e *HubError) Error() string {
	if e.Problem.Detail != "" {
		return fmt.Sprintf("the hub refused: %s [%s]", e.Problem.Detail, e.Problem.Code)
	}
	return fmt.Sprintf("the hub refused: %s [%s]", e.Problem.Title, e.Problem.Code)
}

// Code is the hub's code of err, or empty when err is no HubError.
func Code(err error) hubapi.ErrorCode {
	if e, ok := errors.AsType[*HubError](err); ok {
		return e.Problem.Code
	}
	return ""
}

// URLError is a hub's address that cannot be used.
type URLError struct {
	URL, Reason string
}

func (e *URLError) Error() string { return fmt.Sprintf("hub url %q: %s", e.URL, e.Reason) }

// Link is the node's side of the hub, for the daemon to hold one of.
type Link struct {
	dir  string
	opts Options
	ctx  context.Context // the link's life, with the logger it was opened with
	stop context.CancelFunc

	// starting is one login started at a time.
	starting sync.Mutex

	mu       sync.Mutex
	closed   bool
	creds    *credentials // nil when logged out
	conn     Connection
	hub      string // the hub's version, from its welcome
	inRange  *bool
	behind   string
	changed  chan struct{} // closed at the next change of the status
	control  *loop         // the control connection while logged in
	pending  *Login
	finished sync.WaitGroup // the logins' goroutines
	kicked   chan struct{}  // the outbox has changes; see Kick
	// state is the node's state as last given, nil until it is;
	// stateChanged says it changed. See SendState.
	state        *State
	stateChanged chan struct{}
	streams      chan Stream // see Streams
}

// Open reads <home>/hub/ and, when the node is logged in, starts keeping
// its control connection, which it never waits on. Without a login there,
// what is left of one is removed.
func Open(ctx context.Context, home string, opts Options) (*Link, error) {
	ctx, stop := context.WithCancel(ctx)
	l := &Link{dir: identity.HubDir(home), opts: opts.withDefaults(), ctx: ctx, stop: stop, conn: LoggedOut, changed: make(chan struct{}), kicked: make(chan struct{}, 1),
		stateChanged: make(chan struct{}, 1), streams: make(chan Stream)}
	creds, err := readLogin(ctx, l.dir)
	if err != nil {
		// Kept as it is, for a person to look at; a login writes it anew.
		logging.FromContext(ctx).Error("hub login unreadable, taken as logged out", "path", l.dir, "err", err)
		return l, nil
	}
	if creds == nil {
		return l, nil
	}
	logging.FromContext(ctx).Info("hub login read", "url", creds.URL, "user", creds.User, "node_id", creds.Node.ID)
	l.mu.Lock()
	l.loggedIn(creds)
	l.mu.Unlock()
	return l, nil
}

// loggedIn makes creds the link's login and starts its control connection;
// l.mu is held.
func (l *Link) loggedIn(creds *credentials) {
	l.creds, l.hub, l.inRange, l.behind = creds, "", nil, ""
	l.control = l.startControl(creds)
	l.setConn(Connecting)
}

// setConn changes the connection's state; l.mu is held.
func (l *Link) setConn(c Connection) {
	l.conn = c
	l.notify()
}

// notify wakes those watching the status; l.mu is held.
func (l *Link) notify() {
	close(l.changed)
	l.changed = make(chan struct{})
}

// Status is the link's status now.
func (l *Link) Status() Status {
	s, _ := l.Watch()
	return s
}

// Watch is the status now and a channel closed when it next changes, or
// when the node's certificate is renewed.
func (l *Link) Watch() (Status, <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status(), l.changed
}

// status is the status; l.mu is held.
func (l *Link) status() Status {
	s := Status{Connection: l.conn, HubVersion: l.hub, InRange: l.inRange, Behind: l.behind}
	if c := l.creds; c != nil {
		s.URL, s.User, s.Node = c.URL, c.User, c.Node
	}
	return s
}

// Close ends the control connection and any login pending, and waits for
// them.
func (l *Link) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	control, pending := l.control, l.pending
	l.mu.Unlock()
	if pending != nil {
		pending.end(LoginState{Phase: LoginFailed, Problem: &LoginProblem{Code: ProblemCancelled, Detail: "the daemon stopped"}})
	}
	l.stop()
	if control != nil {
		control.wait()
	}
	l.finished.Wait()
	return nil
}

// osName is this machine's system and architecture, as the hub shows it.
func osName() string { return runtime.GOOS + "/" + runtime.GOARCH }

// nodeTransport is a transport to the node endpoint of creds: it trusts the
// hub's CA alone and shows the node's current certificate.
func nodeTransport(creds *credentials) *http.Transport {
	return &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
			RootCAs:    creds.CA,
			GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
				cert := creds.certificate()
				return &cert, nil
			},
		},
		DialContext:           (&net.Dialer{Timeout: nodeDialTimeout}).DialContext,
		TLSHandshakeTimeout:   dialTimeout,
		ResponseHeaderTimeout: nodeHeaderTimeout,
		IdleConnTimeout:       90 * time.Second,
	}
}
