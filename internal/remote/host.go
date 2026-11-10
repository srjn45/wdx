package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/tls"
	"errors"
	"io"
	"time"

	"filippo.io/cpace"

	"github.com/srjn45/wdx/internal/authcode"
	"github.com/srjn45/wdx/internal/hublink"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/nodeaudit"
)

// Serve answers s, a stream another node asked for, as the host: TLS with
// this node's key, then what the client's first message asks for. It
// closes s when done.
func (r *Remote) Serve(ctx context.Context, s hublink.Stream) {
	defer s.Close()
	log := logging.FromContext(ctx).With("client_node_id", s.Node.ID)
	ctx = logging.NewContext(ctx, log)
	host, cert, err := r.identity()
	if err != nil {
		log.Info("stream refused", "err", err)
		return
	}
	_ = s.SetDeadline(time.Now().Add(exchangeTimeout))
	conn := tls.Server(s, config(cert, true))
	defer conn.Close()
	if err := conn.HandshakeContext(ctx); err != nil {
		log.Info("stream refused", "reason", "tls handshake failed", "err", err)
		return
	}
	cs := conn.ConnectionState()
	clientKey, _ := peerKey(cs) // VerifyConnection checked it
	first, err := read(conn, msgOpen, msgStream, msgClose)
	if err != nil {
		log.Info("stream refused", "reason", "first message unreadable", "err", err)
		return
	}
	if first.Version < oldestProtocolVersion || first.Version > protocolVersion {
		r.refuseStream(ctx, conn, versionRefusal(first.Version), "")
		return
	}
	pinned, err := r.checkPin(ctx, s.Node, clientKey)
	if e, ok := asError(err); ok {
		r.refuseStream(ctx, conn, e, "")
		return
	}
	if err != nil {
		log.Error("node key not read", "err", err)
		return
	}
	switch first.Type {
	case msgOpen:
		r.hostOpen(ctx, conn, host, s.Node, clientKey, cs, first)
	case msgStream, msgClose:
		if !pinned {
			r.refuseStream(ctx, conn, refusal(CodeSessionClosed, "no session %s is open", first.Session), "")
			return
		}
		r.hostSession(ctx, conn, s.Node, first)
	}
}

// versionRefusal refuses a client of a protocol version outside this
// host's range, which the refusal carries.
func versionRefusal(v int) *Error {
	return refusal(CodeNodeVersionUnsupported, "the client speaks version %d between nodes, the host versions %d to %d", v, oldestProtocolVersion, protocolVersion)
}

// refuseStream sends e and logs it.
func (r *Remote) refuseStream(ctx context.Context, w io.Writer, e *Error, reason Reason) {
	logging.FromContext(ctx).Info("stream refused", "code", e.Code, "err", e)
	if err := refuse(w, e, reason); err != nil {
		logging.FromContext(ctx).Info("refusal not sent", "err", err)
	}
}

// hostSession serves a stream of a session open: a stream, which moves
// the idle timer on as it opens and as bytes pass, and is handed to
// Options.ServeStream, or a close.
func (r *Remote) hostSession(ctx context.Context, conn *tls.Conn, client hublink.NodeRef, m message) {
	log := logging.FromContext(ctx).With("session_id", m.Session)
	ctx = logging.NewContext(ctx, log)
	s, err := r.Use(ctx, m.Session, client)
	if e, ok := asError(err); ok {
		r.refuseStream(ctx, conn, e, ReasonOf(e))
		return
	}
	if err != nil {
		log.Error("session not read", "err", err)
		return
	}
	if m.Type == msgClose {
		if _, err := r.closeWhere(ctx, ReasonClosed, "id = ?", s.ID); err != nil {
			log.Error("session not closed", "err", err)
			return
		}
	}
	if err := write(conn, message{Type: msgOK}); err != nil {
		log.Info("stream ended", "err", err)
		return
	}
	log.Info("session stream served", "kind", m.Type)
	if m.Type != msgStream || r.opts.ServeStream == nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	if !r.carry(s.ID, conn) {
		log.Info("stream ended", "reason", "the session closed")
		return
	}
	defer r.dropped(s.ID, conn)
	used := &usedConn{Conn: conn, now: r.opts.Clock.Now, timeout: r.opts.IdleTimeout, used: func() {
		// A session closed closes the stream too.
		if _, err := r.Use(ctx, s.ID, client); err != nil {
			log.Info("session idle time not moved on", "err", err)
		}
	}}
	r.opts.ServeStream(ctx, used, s)
}

// Use checks the session id, incoming from client, is open and not idle,
// with remote access on and this node not cut off by the hub, and moves
// its idle time on: at each stream, and at each request a stream carries.
// A session that is not is an *Error with
// CodeSessionClosed, whose ReasonOf says why when it is known.
func (r *Remote) Use(ctx context.Context, id string, client hublink.NodeRef) (Session, error) {
	if cutOff(r.opts.Link.Status().Connection) {
		// The link's watch closes them all, which this may come before;
		// without the open lock, which an open may hold for a while, as an
		// open checks the link again under it.
		if _, err := r.closeWhere(ctx, ReasonCutOff, "1"); err != nil {
			return Session{}, err
		}
	}
	if !r.opts.RemoteAccess() {
		// Apply closes them all, which this may come before.
		if _, err := r.closeWhere(ctx, ReasonRemoteAccessOff, "direction = 'incoming'"); err != nil {
			return Session{}, err
		}
	}
	s, err := r.openSession(ctx, id, Incoming)
	if err == nil && s.Node.ID != client.ID {
		err = refusal(CodeSessionNotFound, "the session is another node's")
	}
	if e, ok := asError(err); ok && e.Code == CodeSessionNotFound {
		closed := refusal(CodeSessionClosed, "session %s is not open on this node: open one again with a code", id)
		if reason := r.closedReason(ctx, id, client.ID); reason != "" {
			closed.Details = map[string]any{"reason": string(reason)}
		}
		return Session{}, closed
	}
	if err != nil {
		return Session{}, err
	}
	now := r.opts.Clock.Now()
	if now.After(s.IdleExpiresAt) {
		// Before the idle timer got to it.
		if _, err := r.closeWhere(ctx, ReasonIdle, "id = ?", s.ID); err != nil {
			return Session{}, err
		}
		closed := refusal(CodeSessionClosed, "session %s went idle: open one again with a code", s.ID)
		closed.Details = map[string]any{"reason": string(ReasonIdle)}
		return Session{}, closed
	}
	if err := r.touch(ctx, s.ID, now); err != nil {
		return Session{}, err
	}
	s.LastUsedAt, s.IdleExpiresAt = now, now.Add(s.idleTimeout)
	return s, nil
}

// ReasonOf is why the session of e, session_closed, closed; empty when it
// is not known.
func ReasonOf(e *Error) Reason {
	reason, _ := e.Details["reason"].(string)
	return Reason(reason)
}

// closedReason is why the incoming session id of client closed, empty when
// it does not know.
func (r *Remote) closedReason(ctx context.Context, id, clientID string) Reason {
	var reason Reason
	_ = r.opts.DB.QueryRowContext(ctx, "SELECT closed_reason FROM hub_sessions WHERE id = ? AND direction = 'incoming' AND node_id = ? AND closed_reason IS NOT NULL", id, clientID).Scan(&reason)
	return reason
}

// hostOpen opens a session for client, who sent m, open, on conn: the
// refusals that need no work first, then CPace with the code of the step
// the client names, then each side's MAC.
func (r *Remote) hostOpen(ctx context.Context, conn *tls.Conn, host self, client hublink.NodeRef, clientKey ed25519.PublicKey, cs tls.ConnectionState, m message) {
	log := logging.FromContext(ctx)
	r.opening.Lock()
	defer r.opening.Unlock()
	now := r.opts.Clock.Now()
	if !r.opts.RemoteAccess() {
		r.refuseStream(ctx, conn, refusal(CodeRemoteAccessOff, "remote access is off on this node: turn hub.remote_access on there"), "")
		return
	}
	if wait := r.tooSoon(client.ID, now); wait > 0 {
		log.Info("code refused unread", "reason", "too soon after a wrong code", "wait_ms", wait.Milliseconds())
		r.refuseStream(ctx, conn, refusal(CodeCodeWrong, "too soon after a wrong code: try again in %s", wait.Round(time.Second)), "")
		return
	}
	password, err := r.opts.Code.CodeAt(ctx, m.Step, now)
	switch {
	case errors.Is(err, authcode.ErrNotSetUp):
		r.refuseStream(ctx, conn, refusal(CodeCodeNotSetUp, "no code is set up on this node: run wdx hub code setup there"), "")
		return
	case errors.Is(err, authcode.ErrStepRefused):
		r.refuseStream(ctx, conn, refusal(CodeCodeWrong, "the code is of a time step too old or too new, or used already: check both clocks, and type a new code"), "")
		return
	case err != nil:
		log.Error("code not read", "err", err)
		return
	}
	ex, err := r.opts.Exporter(cs)
	if err != nil {
		log.Error("tls exporter not read", "err", err)
		return
	}
	b := binding{exporter: ex, client: clientKey, host: host.key, step: m.Step, version: m.Version}
	msgB, key, err := cpace.Exchange(password, b.context(), m.CPace)
	if err != nil {
		log.Info("stream refused", "reason", "cpace message unreadable", "err", err)
		r.refuseStream(ctx, conn, refusal(CodeCodeWrong, "the session's opening message is unreadable"), "")
		return
	}
	if err := write(conn, message{Type: msgExchange, CPace: msgB}); err != nil {
		log.Info("stream ended", "err", err)
		return
	}
	// The open lock is held: a confirm that does not come in time is a
	// wrong code, so that a stall cannot hold it again at once.
	confirm, err := r.readConfirm(conn)
	if err != nil {
		log.Info("no confirmation", "err", err)
		r.codeWrong(ctx, conn, client, r.opts.Clock.Now(), "no confirmation came in time")
		return
	}
	if !hmac.Equal(confirm.MAC, b.mac(key, "client")) {
		r.codeWrong(ctx, conn, client, now, "the code is wrong")
		return
	}
	if err := r.opts.Code.Use(ctx, m.Step); err != nil {
		if errors.Is(err, authcode.ErrStepRefused) {
			r.refuseStream(ctx, conn, refusal(CodeCodeWrong, "the code was used already: type a new one"), "")
		} else {
			log.Error("code step not recorded", "err", err)
		}
		return
	}
	r.rightCode(client.ID)
	if r.opts.recording != nil {
		r.opts.recording(conn)
	}
	if e := r.closingAll(); e != nil {
		r.refuseStream(ctx, conn, e, ReasonOf(e))
		return
	}
	// One session per node: an older one is the client's no more, as it
	// opens one only with none open.
	if _, err := r.closeWhere(ctx, ReasonClosed, "direction = 'incoming' AND node_id = ?", client.ID); err != nil {
		log.Error("older sessions not closed", "err", err)
		return
	}
	s, err := r.opened(ctx, Session{ID: newSessionID(), Direction: Incoming, Node: client, OpenedAt: now, LastUsedAt: now}, clientKey)
	if err != nil {
		log.Error("session not recorded", "err", err)
		return
	}
	timeout := r.opts.IdleTimeout()
	if err := write(conn, message{Type: msgOpened, MAC: b.mac(key, "host"), Session: s.ID, IdleTimeoutMS: timeout.Milliseconds()}); err != nil {
		// The client records its session only once it read this.
		log.Info("stream ended", "err", err)
		if _, err := r.closeWhere(ctx, ReasonClosed, "id = ?", s.ID); err != nil {
			log.Error("session not closed", "err", err)
		}
		return
	}
	log.Info("session opened", "session_id", s.ID, "direction", Incoming, "node_id", client.ID, "node_name", client.Name, "host_node_id", host.node.ID, "idle_timeout_ms", timeout.Milliseconds())
	r.Kick()
}

// closingAll is the refusal of a session opened now, nil when it may open:
// with remote access off, or this node cut off by the hub, every incoming
// one closes, which one opened while the code was checked would outlive.
// It is checked under the open lock, which Apply and CutOff take too.
func (r *Remote) closingAll() *Error {
	if cutOff(r.opts.Link.Status().Connection) {
		e := refusal(CodeSessionClosed, "this node was cut off by the hub while the session opened")
		e.Details = map[string]any{"reason": string(ReasonCutOff)}
		return e
	}
	if !r.opts.RemoteAccess() {
		e := refusal(CodeRemoteAccessOff, "remote access was turned off on this node while the session opened: turn hub.remote_access on there")
		e.Details = map[string]any{"reason": string(ReasonRemoteAccessOff)}
		return e
	}
	return nil
}

// readConfirm reads the client's confirm, giving up after confirmTimeout.
// Not with a read deadline: the stream through the hub closes when one
// passes, and the refusal could not be sent. Once it gives up, the read
// ends when conn closes.
func (r *Remote) readConfirm(conn *tls.Conn) (message, error) {
	type result struct {
		m   message
		err error
	}
	got := make(chan result, 1)
	go func() {
		m, err := read(conn, msgConfirm)
		got <- result{m, err}
	}()
	timer := time.NewTimer(r.opts.confirmTimeout)
	defer timer.Stop()
	select {
	case res := <-got:
		return res.m, res.err
	case <-timer.C:
		return message{}, errors.New("no confirm in time")
	}
}

// codeWrong counts a wrong code from client at now, records it in the audit
// chain and refuses it on conn, saying why and how long the next try waits.
func (r *Remote) codeWrong(ctx context.Context, conn *tls.Conn, client hublink.NodeRef, now time.Time, why string) {
	log := logging.FromContext(ctx)
	wait := r.wrongCode(client.ID, now)
	log.Info("code wrong", "client_node_name", client.Name, "wait_ms", wait.Milliseconds())
	if err := nodeaudit.AppendOne(ctx, r.opts.DB, nodeaudit.Entry{At: now, Action: nodeaudit.ActionCodeWrong, NodeID: client.ID, Details: map[string]any{"wait_ms": wait.Milliseconds()}}); err != nil {
		log.Error("wrong code not in the audit chain", "err", err)
	}
	r.changed()
	r.refuseStream(ctx, conn, refusal(CodeCodeWrong, "%s; the next try waits %s", why, wait), "")
}

// opened records s, pinning key for its node if none is, with its audit row
// for an incoming one, all in one transaction.
func (r *Remote) opened(ctx context.Context, s Session, key ed25519.PublicKey) (Session, error) {
	tx, err := r.opts.DB.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback() // a no-op once committed
	if err := pin(ctx, tx, s.Node, key, s.OpenedAt); err != nil {
		return Session{}, err
	}
	if err := insertSession(ctx, tx, s); err != nil {
		return Session{}, err
	}
	if s.Direction == Incoming {
		if err := nodeaudit.Append(ctx, tx, nodeaudit.Entry{At: s.OpenedAt, Action: nodeaudit.ActionSessionOpened, NodeID: s.Node.ID, Details: map[string]any{"session_id": s.ID}}); err != nil {
			return Session{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	r.changed()
	return s, nil
}
