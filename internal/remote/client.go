package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"filippo.io/cpace"

	"github.com/srjn45/wdx/internal/authcode"
	"github.com/srjn45/wdx/internal/hublink"
	"github.com/srjn45/wdx/internal/logging"
)

// Open opens a session from this node into node with the host's code, as
// its authenticator shows it now, unless one is open already, as a stream
// to the host checks: opened is false then. It fails with the hub's errors from hublink.Dial, or an
// *Error: remote_access_off, code_not_set_up, code_wrong,
// node_version_unsupported, node_key_mismatch.
func (r *Remote) Open(ctx context.Context, node hublink.NodeRef, code string) (s Session, opened bool, err error) {
	log := logging.FromContext(ctx).With("host_node_id", node.ID)
	ctx = logging.NewContext(ctx, log)
	r.clientOpening.Lock()
	defer r.clientOpening.Unlock()
	if s, ok, err := r.stillOpen(ctx, node.ID); err != nil || ok {
		return s, false, err
	}
	// The code is of the step it arrived in, not of the step the dial
	// through the hub ends in.
	now := r.opts.Clock.Now()
	conn, me, hostKey, _, err := r.dial(ctx, node)
	if err != nil {
		return Session{}, false, err
	}
	defer conn.Close()
	ex, err := r.opts.Exporter(conn.ConnectionState())
	if err != nil {
		return Session{}, false, err
	}
	b := binding{exporter: ex, client: me.key, host: hostKey, step: authcode.Step(now), version: protocolVersion}
	msgA, state, err := cpace.Start(code, b.context())
	if err != nil {
		return Session{}, false, err
	}
	if err := write(conn, message{Type: msgOpen, Version: b.version, Step: b.step, CPace: msgA}); err != nil {
		return Session{}, false, ended(node, err)
	}
	exchange, err := read(conn, msgExchange)
	if err != nil {
		return Session{}, false, ended(node, err)
	}
	key, err := state.Finish(exchange.CPace)
	if err != nil {
		return Session{}, false, refusal(CodeCodeWrong, "node %s answered with an unreadable message", node.Name)
	}
	if err := write(conn, message{Type: msgConfirm, MAC: b.mac(key, "client")}); err != nil {
		return Session{}, false, ended(node, err)
	}
	answer, err := read(conn, msgOpened)
	if err != nil {
		return Session{}, false, ended(node, err)
	}
	if !hmac.Equal(answer.MAC, b.mac(key, "host")) {
		log.Warn("session not opened", "reason", "the host did not prove it holds the code")
		return Session{}, false, refusal(CodeCodeWrong, "node %s did not prove it holds the code", node.Name)
	}
	timeout := time.Duration(answer.IdleTimeoutMS) * time.Millisecond
	s = Session{ID: answer.Session, Direction: Outgoing, Node: node, OpenedAt: now, LastUsedAt: now, IdleExpiresAt: now.Add(timeout), idleTimeout: timeout}
	if s, err = r.opened(ctx, s, hostKey); err != nil {
		return Session{}, false, err
	}
	log.Info("session opened", "session_id", s.ID, "direction", Outgoing, "node_id", node.ID, "node_name", node.Name, "idle_timeout_ms", timeout.Milliseconds())
	return s, true, nil
}

// stillOpen is the session open from this node into nodeID, false when
// there is none: the row is only this node's guess, so a stream checks it
// with the host, and one the host closed is closed here too.
func (r *Remote) stillOpen(ctx context.Context, nodeID string) (Session, bool, error) {
	log := logging.FromContext(ctx)
	if _, ok, err := r.outgoingTo(ctx, nodeID); err != nil || !ok {
		return Session{}, false, err
	}
	conn, s, err := r.Stream(ctx, nodeID)
	if e, ok := asError(err); ok && (e.Code == CodeSessionClosed || e.Code == CodeSessionNotFound) {
		log.Info("session open here closed at the host", "code", e.Code, "reason", ReasonOf(e))
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	conn.Close()
	log.Info("session open already", "session_id", s.ID)
	return s, true, nil
}

// Stream opens a stream in the session open from this node into nodeID,
// which moves its idle timer on at the host, and returns it, for what the
// session carries; closing the session here closes it too. It fails with
// CodeSessionNotFound with none open, and CodeSessionClosed when the host
// closed it, which closes it here too.
func (r *Remote) Stream(ctx context.Context, nodeID string) (net.Conn, Session, error) {
	s, ok, err := r.outgoingTo(ctx, nodeID)
	if err != nil {
		return nil, Session{}, err
	}
	if !ok {
		return nil, Session{}, refusal(CodeSessionNotFound, "no session into node %s is open", nodeID)
	}
	if r.opts.dialing != nil {
		r.opts.dialing()
	}
	conn, err := r.sessionStream(ctx, s, msgStream)
	if err != nil {
		return nil, Session{}, err
	}
	now := r.opts.Clock.Now()
	if err := r.touch(ctx, s.ID, now); err != nil {
		conn.Close()
		return nil, Session{}, err
	}
	s.LastUsedAt, s.IdleExpiresAt = now, now.Add(s.idleTimeout)
	_ = conn.SetDeadline(time.Time{})
	if !r.carry(ctx, s.ID, conn) {
		conn.Close()
		return nil, Session{}, refusal(CodeSessionNotFound, "the session into node %s closed meanwhile", s.Node.Name)
	}
	// The host moves its row on as bytes pass, and this node its own at the
	// same pace, so that both show the same times.
	log := logging.FromContext(ctx).With("session_id", s.ID)
	used := &usedConn{Conn: conn, now: r.opts.Clock.Now, timeout: func() time.Duration { return s.idleTimeout }, used: func() {
		err := r.touch(context.WithoutCancel(ctx), s.ID, r.opts.Clock.Now())
		if _, closed := asError(err); closed {
			log.Info("session idle time not moved on", "err", err) // the stream is cut with it
		} else if err != nil {
			log.Error("session idle time not moved on", "err", err)
		}
	}}
	return &carried{Conn: used, stream: conn, r: r, id: s.ID}, s, nil
}

// carried is a stream of the outgoing session id, carried until closed.
type carried struct {
	net.Conn
	stream net.Conn // the one carried, under Conn
	r      *Remote
	id     string
}

func (c *carried) Close() error {
	c.r.dropped(c.id, c.stream)
	return c.Conn.Close()
}

// sessionStream opens a stream of kind, stream or close, in s, an
// outgoing session, and waits for the host's ok. A host that refuses it as
// session_closed has the session closed here too, for the reason it gave.
func (r *Remote) sessionStream(ctx context.Context, s Session, kind string) (*tls.Conn, error) {
	log := logging.FromContext(ctx).With("session_id", s.ID, "host_node_id", s.Node.ID)
	ctx = logging.NewContext(ctx, log)
	conn, _, _, pinned, err := r.dial(ctx, s.Node)
	if err != nil {
		return nil, err
	}
	if !pinned {
		// Only a logout forgets the keys of nodes a session is open into,
		// and it closed the session after it was read.
		conn.Close()
		reason := r.reasonClosed(ctx, s.ID)
		if reason == "" {
			reason = ReasonLoggedOut
		}
		log.Info("session closed meanwhile", "reason", reason)
		closed := refusal(CodeSessionClosed, "session %s into node %s closed meanwhile, its key forgotten (%s)", s.ID, s.Node.Name, reason)
		closed.Details = map[string]any{"reason": string(reason)}
		return nil, closed
	}
	if err := write(conn, message{Type: kind, Version: protocolVersion, Session: s.ID}); err != nil {
		conn.Close()
		return nil, ended(s.Node, err)
	}
	_, err = read(conn, msgOK)
	if e, ok := asError(err); ok && e.Code == CodeSessionClosed {
		conn.Close()
		reason := ReasonClosed
		if why, ok := e.Details["reason"].(string); ok && why != "" {
			reason = Reason(why)
		}
		log.Info("session closed by the host", "reason", reason)
		if _, err := r.closeWhere(ctx, reason, "id = ?", s.ID); err != nil {
			log.Error("session not closed", "err", err)
		}
		return nil, e
	}
	if err != nil {
		conn.Close()
		if _, ok := asError(err); ok {
			return nil, err
		}
		return nil, ended(s.Node, err)
	}
	return conn, nil
}

// tellClosed tells the host of s, outgoing, that it closed: best effort,
// as the host finds out anyway, at the latest when it goes idle.
func (r *Remote) tellClosed(ctx context.Context, s Session) {
	ctx, cancel := context.WithTimeout(ctx, r.opts.Limits.Close)
	defer cancel()
	conn, err := r.sessionStream(ctx, s, msgClose)
	if err != nil {
		logging.FromContext(ctx).Info("host not told the session closed", "session_id", s.ID, "err", err)
		return
	}
	conn.Close()
}

// dial opens a stream to node and TLS in it, as the client, and checks the
// host's key against the one pinned for it: it returns this node, the
// host's key and whether one is pinned.
func (r *Remote) dial(ctx context.Context, node hublink.NodeRef) (*tls.Conn, self, ed25519.PublicKey, bool, error) {
	me, cert, err := r.identity()
	if err != nil {
		return nil, self{}, nil, false, err
	}
	raw, err := r.opts.Link.Dial(ctx, node.ID)
	if err != nil {
		return nil, self{}, nil, false, err
	}
	_ = raw.SetDeadline(time.Now().Add(r.opts.Limits.Exchange))
	conn := tls.Client(raw, config(cert, false))
	if err := conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, self{}, nil, false, fmt.Errorf("no TLS with node %s: %w", node.Name, err)
	}
	hostKey, _ := peerKey(conn.ConnectionState()) // VerifyConnection checked it
	pinned, err := r.checkPin(ctx, node, hostKey)
	if err != nil {
		conn.Close()
		return nil, self{}, nil, false, err
	}
	return conn, me, hostKey, pinned, nil
}

// StreamEndedError is a stream to a node that ended before it answered.
type StreamEndedError struct {
	Node string
	Err  error
}

func (e *StreamEndedError) Error() string {
	return fmt.Sprintf("the stream to node %s ended before it answered: %v", e.Node, e.Err)
}
func (e *StreamEndedError) Unwrap() error { return e.Err }

// ended is err, a stream's, as the error it makes.
func ended(node hublink.NodeRef, err error) error {
	if _, ok := asError(err); ok {
		return err
	}
	return &StreamEndedError{Node: node.Name, Err: err}
}
