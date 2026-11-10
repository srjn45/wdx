// Package relay is the hub's streams between two nodes of one user (the wdx
// doc HUB_TUNNEL): a stream asked for by one node to another, joined once
// both arrived, each at most once, and the binary messages of their two
// WebSockets copied both ways until either closes. The relay knows which two
// nodes and when, and counts the bytes each way; it never reads them, and
// never logs them. Which nodes may ask, and which are online, is the
// server's to check, and when a node or a user is cut off, its to say.
package relay

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/srjn45/wdx/internal/logging"
)

// The relay's limits, unless Options say otherwise.
const (
	// DefaultJoinTimeout is how long both nodes have to join a stream, from
	// when it was asked for.
	DefaultJoinTimeout = 10 * time.Second
	// DefaultMaxPerUser is how many streams a user may have open or waiting
	// at once.
	DefaultMaxPerUser = 32
)

var (
	// ErrNotFound is a stream unknown or expired, or one the node joining is
	// not of, or joined already.
	ErrNotFound = errors.New("no such stream")
	// ErrPeerAbsent is a stream the other node did not join in time.
	ErrPeerAbsent = errors.New("the other node did not join the stream in time")
	// ErrTooMany is a user with as many streams as the relay allows.
	ErrTooMany = errors.New("the account has as many streams open as the hub allows")
)

// Options configure a Relay.
type Options struct {
	// JoinTimeout is DefaultJoinTimeout when 0; MaxPerUser DefaultMaxPerUser.
	JoinTimeout time.Duration
	MaxPerUser  int
	// After sends once d has passed: the clock of the join timeouts;
	// time.After when nil.
	After func(d time.Duration) <-chan time.Time
}

// Relay is the streams, in memory.
type Relay struct {
	opts Options

	mu      sync.Mutex
	streams map[string]*stream   // by id, until both joined or it failed
	open    map[*stream]struct{} // both joined, until both copies ended
	perUser map[string]int       // open or waiting, by user id
}

// New returns a relay with no streams.
func New(opts Options) *Relay {
	opts.JoinTimeout = cmp.Or(opts.JoinTimeout, DefaultJoinTimeout)
	opts.MaxPerUser = cmp.Or(opts.MaxPerUser, DefaultMaxPerUser)
	if opts.After == nil {
		opts.After = time.After
	}
	return &Relay{opts: opts, streams: map[string]*stream{}, open: map[*stream]struct{}{}, perUser: map[string]int{}}
}

// Nodes are the two nodes of a stream: the client, which asked for it, and
// the host, which it was asked to.
type Nodes struct {
	UserID, ClientID, HostID string
}

// stream is a stream from when it is asked for to its end.
type stream struct {
	id    string
	nodes Nodes
	// joined has a node's id once it joined, so that it joins once.
	joined map[string]bool
	// both is closed once both joined, failed once the stream did before.
	both   chan struct{}
	failed error
	// conns has each side's WebSocket once it switched to one, nil when it
	// could not, which the other side reads to copy into it.
	conns map[string]chan *websocket.Conn
	// bytes copied from the client and from the host.
	fromClient, fromHost atomic.Int64
	// copies counts the sides still running; the last logs the close.
	copies  atomic.Int32
	started time.Time

	// ws is each side's WebSocket while it runs, and cut whether the
	// stream was cut, which closes them; both under the relay's mu.
	ws  []*websocket.Conn
	cut bool
}

// Open asks for a stream between nodes, failing with ErrTooMany when their
// user has as many as the relay allows. Neither joined yet: the stream fails
// when both did not within the join timeout.
func (r *Relay) Open(ctx context.Context, nodes Nodes) (string, error) {
	log := logging.FromContext(ctx)
	r.mu.Lock()
	if r.perUser[nodes.UserID] >= r.opts.MaxPerUser {
		r.mu.Unlock()
		log.Info("stream refused", "reason", "too_many_streams", "user_id", nodes.UserID, "max", r.opts.MaxPerUser)
		return "", ErrTooMany
	}
	s := &stream{
		id: newID(), nodes: nodes, joined: map[string]bool{}, both: make(chan struct{}),
		conns: map[string]chan *websocket.Conn{nodes.ClientID: make(chan *websocket.Conn, 1), nodes.HostID: make(chan *websocket.Conn, 1)},
	}
	r.streams[s.id] = s
	r.perUser[nodes.UserID]++
	r.mu.Unlock()
	log.Info("stream asked", "stream_id", s.id, "user_id", nodes.UserID, "client_node_id", nodes.ClientID, "host_node_id", nodes.HostID)
	expires := r.opts.After(r.opts.JoinTimeout)
	go func() {
		<-expires
		if r.fail(s, ErrPeerAbsent) {
			log.Info("stream expired", "stream_id", s.id, "user_id", s.nodes.UserID, "joined", len(s.joined))
		}
	}()
	return s.id, nil
}

// fail ends s with err when both have not joined yet, reporting whether it
// did.
func (r *Relay) fail(s *stream, err error) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.streams[s.id] != s {
		return false
	}
	delete(r.streams, s.id)
	r.release(s.nodes.UserID)
	s.failed = err
	close(s.both)
	return true
}

// release counts a stream of userID out; r.mu is held.
func (r *Relay) release(userID string) {
	if r.perUser[userID]--; r.perUser[userID] <= 0 {
		delete(r.perUser, userID)
	}
}

// Joined is one node's side of a stream both nodes joined.
type Joined struct {
	relay  *Relay
	s      *stream
	nodeID string
}

// Join joins the stream id as the node nodeID, and waits for the other node
// to join it, until the join timeout or ctx ends. It fails with ErrNotFound
// for a stream unknown, expired, of other nodes or joined by this node
// already, and with ErrPeerAbsent when the other did not come in time. Once
// it returns, the side must Run, whether it switched to a WebSocket or not.
func (r *Relay) Join(ctx context.Context, id, nodeID string) (*Joined, error) {
	log := logging.FromContext(ctx)
	r.mu.Lock()
	s := r.streams[id]
	if s == nil || (nodeID != s.nodes.ClientID && nodeID != s.nodes.HostID) || s.joined[nodeID] {
		r.mu.Unlock()
		log.Info("stream join refused", "stream_id", id, "node_id", nodeID, "reason", "no such stream for this node")
		return nil, ErrNotFound
	}
	s.joined[nodeID] = true
	if len(s.joined) == 2 {
		// Both are in: the stream is no longer waiting, but open.
		delete(r.streams, id)
		r.open[s] = struct{}{}
		s.started = time.Now()
		s.copies.Store(2)
		close(s.both)
	} else {
		log.Info("stream join waiting", "stream_id", id, "node_id", nodeID)
	}
	r.mu.Unlock()
	select {
	case <-s.both:
	case <-ctx.Done():
		if r.fail(s, ctx.Err()) {
			log.Info("stream join given up", "stream_id", id, "node_id", nodeID, "err", ctx.Err())
			return nil, ctx.Err()
		}
		<-s.both // both joined meanwhile, or it failed
	}
	if s.failed != nil {
		log.Info("stream join failed", "stream_id", id, "node_id", nodeID, "err", s.failed)
		return nil, s.failed
	}
	log.Info("stream joined", "stream_id", id, "node_id", nodeID, "client_node_id", s.nodes.ClientID, "host_node_id", s.nodes.HostID)
	return &Joined{relay: r, s: s, nodeID: nodeID}, nil
}

// other is the id of the other node of the stream.
func (j *Joined) other() string {
	if j.nodeID == j.s.nodes.ClientID {
		return j.s.nodes.HostID
	}
	return j.s.nodes.ClientID
}

// Run copies the binary messages of conn, this side's WebSocket, to the
// other side's until either closes or ctx ends, then closes the other's as
// this one's closing said, which ends the other's copy. A nil conn, a side
// that could not switch to a WebSocket, ends the stream.
func (j *Joined) Run(ctx context.Context, conn *websocket.Conn) {
	s := j.s
	defer j.done(ctx)
	s.conns[j.nodeID] <- conn
	if conn == nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	if !j.relay.running(s, conn) {
		return
	}
	conn.SetReadLimit(-1) // copied as it comes, never held whole
	var peer *websocket.Conn
	select {
	case peer = <-s.conns[j.other()]:
	case <-ctx.Done():
	}
	if peer == nil {
		closeWith(conn, websocket.StatusGoingAway, "the other node left")
		return
	}
	// The hub stopping closes this side as it should be; the copy then
	// ends as for any close.
	stop := context.AfterFunc(ctx, func() { closeWith(conn, websocket.StatusGoingAway, "the hub is stopping") })
	defer stop()
	counted := &s.fromHost
	if j.nodeID == s.nodes.ClientID {
		counted = &s.fromClient
	}
	status, reason := copyMessages(context.WithoutCancel(ctx), conn, peer, counted)
	closeWith(peer, status, reason)
}

// done counts this side's copy out; the last logs the close and frees the
// user's count.
func (j *Joined) done(ctx context.Context) {
	s := j.s
	if s.copies.Add(-1) != 0 {
		return
	}
	j.relay.mu.Lock()
	j.relay.release(s.nodes.UserID)
	delete(j.relay.open, s)
	j.relay.mu.Unlock()
	logging.FromContext(ctx).Info("stream closed", "stream_id", s.id, "user_id", s.nodes.UserID,
		"client_node_id", s.nodes.ClientID, "host_node_id", s.nodes.HostID,
		"client_bytes", s.fromClient.Load(), "host_bytes", s.fromHost.Load(), logging.Duration(s.started))
}

// running keeps conn, a side's WebSocket, for a cut to close, and reports
// whether the stream is not cut already.
func (r *Relay) running(s *stream, conn *websocket.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.cut {
		return false
	}
	s.ws = append(s.ws, conn)
	return true
}

// CutNode closes every stream open of the node nodeID, either side, as
// once it is removed from its account.
func (r *Relay) CutNode(ctx context.Context, nodeID string) {
	r.cutWhere(ctx, "node_id", nodeID, func(n Nodes) bool { return n.ClientID == nodeID || n.HostID == nodeID })
}

// CutUser closes every stream open of the user userID, as once the user is
// disabled or removed.
func (r *Relay) CutUser(ctx context.Context, userID string) {
	r.cutWhere(ctx, "user_id", userID, func(n Nodes) bool { return n.UserID == userID })
}

// cutWhere fails the streams waiting that match picks, and closes the
// WebSockets of those open, and of their sides still to run.
func (r *Relay) cutWhere(ctx context.Context, key, value string, match func(Nodes) bool) {
	var conns []*websocket.Conn
	cut := 0
	r.mu.Lock()
	for id, s := range r.streams {
		if match(s.nodes) {
			delete(r.streams, id)
			r.release(s.nodes.UserID)
			s.failed = ErrNotFound
			close(s.both)
			cut++
		}
	}
	for s := range r.open {
		if !s.cut && match(s.nodes) {
			s.cut = true
			cut++
			conns = append(conns, s.ws...)
		}
	}
	r.mu.Unlock()
	// Outside the lock, and at once: the stream is over for both sides.
	for _, conn := range conns {
		_ = conn.CloseNow()
	}
	logging.FromContext(ctx).Info("streams cut", key, value, "streams", cut)
}

// copyMessages copies the binary messages read from src to dst, counting
// their bytes, until either fails or ctx ends; it returns how dst is to be
// closed.
func copyMessages(ctx context.Context, src, dst *websocket.Conn, counted *atomic.Int64) (websocket.StatusCode, string) {
	for {
		typ, r, err := src.Reader(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return websocket.StatusNormalClosure, "the other node closed the stream"
			}
			return websocket.StatusGoingAway, "the other node left"
		}
		if typ != websocket.MessageBinary {
			closeWith(src, websocket.StatusUnsupportedData, "binary messages only")
			return websocket.StatusGoingAway, "the other node sent what a stream does not carry"
		}
		w, err := dst.Writer(ctx, websocket.MessageBinary)
		if err != nil {
			return websocket.StatusGoingAway, "the other node left"
		}
		n, err := io.Copy(w, r)
		counted.Add(n)
		if err != nil {
			_ = w.Close()
			return websocket.StatusGoingAway, "the stream failed"
		}
		if err := w.Close(); err != nil {
			return websocket.StatusGoingAway, "the stream failed"
		}
	}
}

// closeWith closes conn with status and reason, without waiting long for
// the other end's answer: the stream is over either way.
func closeWith(conn *websocket.Conn, status websocket.StatusCode, reason string) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = conn.Close(status, reason) // the stream is over, answered or not
	}()
	select {
	case <-done:
	case <-time.After(closeTimeout):
		_ = conn.CloseNow()
	}
}

// closeTimeout is how long a close may wait for the other end's.
const closeTimeout = 5 * time.Second

// newID is a stream's id: str- and 16 random hex digits.
func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // never fails
	return "str-" + hex.EncodeToString(b)
}
