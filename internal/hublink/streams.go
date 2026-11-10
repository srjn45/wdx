package hublink

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/logging"
)

// handoffTimeout is how long a stream another node asked for waits, once
// joined, for whoever reads Streams to take it; it is closed after.
const handoffTimeout = 10 * time.Second

// Stream is a stream another node of the account asked for to this one,
// joined: the bytes it carries are the two nodes' own, which the hub copies
// and cannot read.
type Stream struct {
	net.Conn
	// Node is the node that asked for it, as the hub names it: what TLS
	// between the two nodes proves is for the reader to check.
	Node NodeRef
}

// Streams delivers each stream another node asks for, once joined.
func (l *Link) Streams() <-chan Stream { return l.streams }

// Dial asks the hub for a stream to the node nodeID of the account and joins
// it, returning once the other node joined it too. It fails with
// ErrNotLoggedIn, an *UnreachableError, or a *HubError: node_not_found,
// node_offline, too_many_streams, hub_version_unsupported. Asking and joining run out after the
// link's SetupTimeout, as an *UnreachableError. The stream ends with the
// link.
func (l *Link) Dial(ctx context.Context, nodeID string) (net.Conn, error) {
	log := logging.FromContext(ctx)
	if err := l.hubLacks(featureStreams); err != nil {
		return nil, err
	}
	client, c, done, err := l.nodesClient()
	if err != nil {
		return nil, err
	}
	ctx, endSetup := l.setup(ctx)
	defer endSetup()
	r, err := client.OpenStreamWithResponse(ctx, hubapi.OpenStream{NodeId: nodeID})
	done()
	if err != nil {
		return nil, &UnreachableError{URL: c.NodesURL, Err: setupErr(ctx, err)}
	}
	if r.JSON201 == nil {
		err := refused(c.NodesURL, r.StatusCode(), r.ApplicationproblemJSONDefault)
		log.Info("stream not opened", "node_id", nodeID, "err", err)
		return nil, err
	}
	id := r.JSON201.Stream.Id
	log.Info("stream asked", "stream_id", id, "node_id", nodeID)
	conn, err := l.join(ctx, c, id)
	if err != nil {
		log.Info("stream not joined", "stream_id", id, "node_id", nodeID, "err", err)
		return nil, err
	}
	log.Info("stream joined", "stream_id", id, "node_id", nodeID)
	return conn, nil
}

// join joins the stream id, waiting for the other node to join it.
func (l *Link) join(ctx context.Context, c *credentials, id string) (net.Conn, error) {
	transport := nodeTransport(c)
	defer transport.CloseIdleConnections() // the stream's own connection is the WebSocket's
	var rt http.RoundTripper = transport
	if l.opts.WrapNodes != nil {
		rt = l.opts.WrapNodes(rt)
	}
	//nolint:bodyclose // the websocket closes it itself: "You never need to close resp.Body yourself."
	conn, resp, err := websocket.Dial(ctx, c.NodesURL+"/v1/streams/"+url.PathEscape(id), &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: rt},
		HTTPHeader: versionHeaders(),
	})
	if err != nil {
		why := dialRefused(resp, err)
		if hubErr, ok := errors.AsType[*HubError](why); ok {
			return nil, hubErr
		}
		if cut, ok := errors.AsType[*cutOff](why); ok {
			code := hubapi.ErrorCodeNodeRevoked
			if cut.state == Disabled {
				code = hubapi.ErrorCodeAccountDisabled
			}
			return nil, &HubError{Problem: hubapi.NewProblem(code, cut.Error())}
		}
		return nil, &UnreachableError{URL: c.NodesURL, Err: setupErr(ctx, err)}
	}
	return l.keptAlive(conn, id), nil
}

// liveConn is a joined stream, which notes when it last heard from the hub.
type liveConn struct {
	net.Conn
	clock Clock
	// heard is when a read last returned bytes or a pong came, in Unix
	// nanoseconds.
	heard  atomic.Int64
	closed context.CancelFunc
}

func (c *liveConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.heard.Store(c.clock.Now().UnixNano())
	}
	return n, err
}

func (c *liveConn) Close() error {
	c.closed()
	return c.Conn.Close()
}

// keptAlive is ws, the stream id joined, as a net.Conn that pings the hub
// each time it received nothing for StreamQuiet, and closes once a pong
// does not come within StreamPongWait: the hub's library answers, so a
// network gone is noticed though neither node writes. A stream receiving
// bytes sends no pings. It ends with the link.
func (l *Link) keptAlive(ws *websocket.Conn, id string) net.Conn {
	ctx, closed := context.WithCancel(l.ctx)
	c := &liveConn{Conn: websocket.NetConn(ctx, ws, websocket.MessageBinary), clock: l.opts.Clock, closed: closed}
	c.heard.Store(l.opts.Clock.Now().UnixNano())
	go l.keepAlive(ctx, ws, c, id)
	return c
}

// keepAlive pings ws whenever c was quiet for StreamQuiet, closing it when
// no pong comes in time, until ctx ends.
func (l *Link) keepAlive(ctx context.Context, ws *websocket.Conn, c *liveConn, id string) {
	clock := l.opts.Clock
	for {
		quiet := clock.Now().Sub(time.Unix(0, c.heard.Load()))
		if quiet < l.opts.StreamQuiet {
			select {
			case <-clock.After(l.opts.StreamQuiet - quiet):
				continue
			case <-ctx.Done():
				return
			}
		}
		pinged, cancel := context.WithCancel(ctx)
		go func() {
			select {
			case <-clock.After(l.opts.StreamPongWait):
				cancel()
			case <-pinged.Done():
			}
		}()
		err := ws.Ping(pinged)
		cancel()
		if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
			return // closed meanwhile
		}
		if err != nil {
			logging.FromContext(ctx).Info("stream closed", "stream_id", id, "reason", "no pong",
				"quiet_ms", (quiet + l.opts.StreamPongWait).Milliseconds(), "err", err)
			_ = ws.CloseNow() // the network may be gone: nothing waits for the hub
			c.closed()
			return
		}
		c.heard.Store(clock.Now().UnixNano())
	}
}

// setupRanOut is the cause of a setup context that ran out of time.
type setupRanOut struct{ after time.Duration }

func (e *setupRanOut) Error() string {
	return fmt.Sprintf("the hub did not answer within %s", e.after)
}

// setup is ctx, ended as well once the link's SetupTimeout passed on its
// clock; end releases it.
func (l *Link) setup(ctx context.Context) (setupCtx context.Context, end func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	go func() {
		select {
		case <-l.opts.Clock.After(l.opts.SetupTimeout):
			cancel(&setupRanOut{after: l.opts.SetupTimeout})
		case <-ctx.Done():
		}
	}()
	return ctx, func() { cancel(context.Canceled) }
}

// setupErr is err, a step of reaching another node under ctx, or what ended
// ctx when it ran out of time: "context canceled" says nothing of why.
func setupErr(ctx context.Context, err error) error {
	if ranOut, ok := errors.AsType[*setupRanOut](context.Cause(ctx)); ok {
		return ranOut
	}
	return err
}

// answer joins the stream m names, which another node asked for, and hands
// it to whoever reads Streams.
func (l *Link) answer(ctx context.Context, c *credentials, m hubapi.ControlStreamWaiting) {
	log := logging.FromContext(ctx).With("stream_id", m.Stream, "client_node_id", m.Node.Id)
	log.Info("stream waiting", "client_node_name", m.Node.Name)
	joining, endSetup := l.setup(ctx)
	conn, err := l.join(joining, c, m.Stream)
	endSetup()
	if err != nil {
		log.Info("stream not joined", "err", err)
		return
	}
	log.Info("stream joined")
	select {
	case l.streams <- Stream{Conn: conn, Node: NodeRef{ID: m.Node.Id, Name: m.Node.Name}}:
	case <-l.opts.Clock.After(handoffTimeout):
		log.Info("stream closed", "reason", "nobody took it")
		_ = conn.Close() // the client's side sees the stream end
	case <-ctx.Done():
		_ = conn.Close() // the link is closing
	}
}

// State is what the node says of itself to the hub, for display.
type State struct {
	// RemoteAccess is whether it allows sessions, CodeSetup whether it has
	// an authenticator for them.
	RemoteAccess, CodeSetup bool
}

// SendState tells the hub the node's state: now when connected and it
// changed, and at every connect after.
func (l *Link) SendState(s State) {
	l.mu.Lock()
	if l.state != nil && *l.state == s {
		l.mu.Unlock()
		return
	}
	l.state = &s
	l.mu.Unlock()
	select {
	case l.stateChanged <- struct{}{}:
	default:
	}
}

// keepState sends the state on conn, a control connection welcomed: the
// last given at once, then each time it changes, until ctx ends.
func (l *Link) keepState(ctx context.Context, conn *websocket.Conn) {
	log := logging.FromContext(ctx)
	for {
		l.mu.Lock()
		s := l.state
		// The state read is the one a pending notice is for: sent now, it
		// is not to be sent again.
		select {
		case <-l.stateChanged:
		default:
		}
		l.mu.Unlock()
		if s != nil {
			err := write(ctx, conn, hubapi.ControlState{Type: hubapi.ControlStateTypeState, RemoteAccess: s.RemoteAccess, CodeSetup: s.CodeSetup})
			if err != nil {
				log.Info("state not sent", "err", err)
			} else {
				log.Info("state sent", "remote_access", s.RemoteAccess, "code_setup", s.CodeSetup)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-l.stateChanged:
		}
	}
}
