package hublink

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/coder/websocket"

	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/identity"
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
// node_offline, too_many_streams. The stream ends with the link.
func (l *Link) Dial(ctx context.Context, nodeID string) (net.Conn, error) {
	log := logging.FromContext(ctx)
	client, c, done, err := l.nodesClient()
	if err != nil {
		return nil, err
	}
	r, err := client.OpenStreamWithResponse(ctx, hubapi.OpenStream{NodeId: nodeID})
	done()
	if err != nil {
		return nil, &UnreachableError{URL: c.NodesURL, Err: err}
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
		HTTPHeader: http.Header{hubapi.VersionHeader: {identity.Version}},
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
		return nil, &UnreachableError{URL: c.NodesURL, Err: err}
	}
	return websocket.NetConn(l.ctx, conn, websocket.MessageBinary), nil
}

// answer joins the stream m names, which another node asked for, and hands
// it to whoever reads Streams.
func (l *Link) answer(ctx context.Context, c *credentials, m hubapi.ControlStreamWaiting) {
	log := logging.FromContext(ctx).With("stream_id", m.Stream, "client_node_id", m.Node.Id)
	log.Info("stream waiting", "client_node_name", m.Node.Name)
	conn, err := l.join(ctx, c, m.Stream)
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
