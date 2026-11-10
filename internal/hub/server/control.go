package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/srjn45/wdx/internal/hub/ca"
	"github.com/srjn45/wdx/internal/hub/node"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/logging"
)

// The control connection's timings, unless Options say otherwise.
const (
	// DefaultRenewInterval is how often the hub checks whether a connected
	// node's certificate is due for renewal, besides at its welcome.
	DefaultRenewInterval = 24 * time.Hour
	// seenEvery is how often a connected node's last_seen_at is written,
	// at most, besides at its connect and close.
	seenEvery = time.Minute
	// controlReadLimit is the largest message a node may send.
	controlReadLimit = 64 << 10
	// controlQueue is how many messages may wait to be sent a node, such as
	// streams asked for it; more are refused.
	controlQueue = 32
)

// controlResponse upgrades the request to the node's control WebSocket and
// serves it until it closes.
type controlResponse struct {
	server *Server
	r      *http.Request
	node   node.Node
}

func (c controlResponse) VisitOpenControlResponse(w http.ResponseWriter) error {
	ctx := c.r.Context()
	log := logging.FromContext(ctx)
	if !c.server.beginControl() {
		log.Info("control refused", "node_id", c.node.ID, "reason", "the hub is stopping")
		writeProblem(w, problemOf(ctx, errStopping))
		return nil
	}
	defer c.server.controls.Done()
	conn, err := websocket.Accept(w, c.r, nil)
	if err != nil {
		// Accept answered the request itself.
		log.Info("control refused", "node_id", c.node.ID, "err", err)
		return nil
	}
	conn.SetReadLimit(controlReadLimit)
	// The request's context ends with the handler; the connection outlives
	// nothing but the hub.
	c.server.control(logging.NewContext(c.server.stopping, log), conn, c.node)
	return nil
}

var errStopping = errors.New("the hub is stopping")

// beginControl counts a control connection or a stream in, unless the hub
// is stopping.
func (s *Server) beginControl() bool {
	s.controlsMu.Lock()
	defer s.controlsMu.Unlock()
	if s.stopping.Err() != nil {
		return false
	}
	s.controls.Add(1)
	return true
}

// stopControls says goodbye to every control connection, closes every
// stream, and waits for them to end, as the hub stops.
func (s *Server) stopControls() {
	s.controlsMu.Lock()
	s.stopNow()
	s.controlsMu.Unlock()
	s.controls.Wait()
}

// byeCause is why a control connection is closed: the reason its bye says.
type byeCause struct{ reason node.Reason }

func (b byeCause) Error() string { return "bye: " + string(b.reason) }

// control serves the control connection of n until it closes: the hello and
// its version, the welcome, the renewals of n's certificate, the pings, and
// the bye when n is cut off or the hub stops.
func (s *Server) control(parent context.Context, conn *websocket.Conn, n node.Node) {
	log := logging.FromContext(parent).With("node_id", n.ID, "user_id", n.UserID)
	parent = logging.NewContext(parent, log)
	// ctx ends when the connection must close: cut off, the hub stopping,
	// or the connection failing, its cause saying which. life carries the
	// connection's reads and writes, which this websocket closes the
	// connection on when their context ends: it lasts until the bye is said.
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	life, end := context.WithCancel(context.WithoutCancel(parent))
	defer end()
	// Closed already, unless it failed before: nothing is left to do.
	defer func() { _ = conn.CloseNow() }()

	hello, err := s.hello(parent, conn)
	if err != nil {
		log.Info("control closed", "reason", "hello", "err", err)
		return
	}
	if !hubapi.Served(hello.ApiVersion) {
		log.Info("control refused", "reason", "hub_version_unsupported", "api_version", hello.ApiVersion, "behind", hubapi.BehindNode)
		if err := conn.Close(hubapi.CloseVersionUnsupported, hubapi.BehindNode); err != nil {
			log.Info("close not answered", "err", err)
		}
		return
	}
	if err := s.nodes.Connected(life, n.ID, hello.WdxVersion, hello.Os); err != nil {
		log.Error("node not marked connected", "err", err)
	}
	out := make(chan any, controlQueue)
	send := func(v any) bool {
		select {
		case out <- v:
			return true
		default:
			return false
		}
	}
	c := s.nodes.Registry().Add(n.ID, n.UserID, func(r node.Reason) { cancel(byeCause{r}) }, send)
	defer s.nodes.Registry().Remove(c)
	// Run after end() cancelled life, it writes with a context of its own.
	defer func() {
		if err := s.nodes.Seen(context.WithoutCancel(parent), n.ID); err != nil {
			log.Error("last seen not written", "err", err)
		}
	}()
	// A revocation committed before the connection was registered missed
	// it: the database says so now.
	if _, err := s.nodes.Authenticate(life, n.ID, n.PublicKey); err != nil {
		reason := node.ReasonRevoked
		if errors.Is(err, node.ErrDisabled) {
			reason = node.ReasonDisabled
		}
		s.closed(life, conn, log, byeCause{reason})
		return
	}
	log.Info("control connected", "api_version", hello.ApiVersion, "wdx_version", hello.WdxVersion, "os", hello.Os)

	if err := wsjson.Write(life, conn, hubapi.ControlWelcome{
		Type: hubapi.ControlWelcomeTypeWelcome, HubVersion: s.version(), ApiVersion: s.apiVersion,
		Node: hubapi.NodeRef{Id: n.ID, Name: n.Name}, NodesUrl: &s.nodesURL,
	}); err != nil {
		log.Info("control closed", "reason", "connection", "err", err)
		return
	}
	// The expiries of the certificates sent and not yet saved: the hub sends
	// another at each check until one is, so a save may come for one sent
	// before the last.
	var sent []time.Time
	renew := func() {
		if exp, ok := s.renew(life, conn, n); ok {
			sent = append(sent, exp)
		}
	}
	renew()

	saved := make(chan time.Time)
	var running sync.WaitGroup
	// Ended before the connection's life is, they end with it.
	defer running.Wait()
	defer end()
	// A copy: the loop below writes n's certificate expiry as they read theirs.
	held := n
	running.Go(func() { s.readControl(life, cancel, conn, held, saved) })
	running.Go(func() { s.ping(life, cancel, conn, held) })
	renewals := time.NewTicker(s.opts.RenewInterval)
	defer renewals.Stop()
	for {
		select {
		case <-ctx.Done():
			s.closed(life, conn, log, context.Cause(ctx))
			return
		case exp := <-saved:
			if !slices.ContainsFunc(sent, exp.Equal) {
				log.Info("certificate saved ignored", "expires_at", exp, "reason", "not one sent")
				continue
			}
			if err := s.nodes.CertificateRenewed(life, n, exp); err != nil {
				log.Error("certificate renewal not recorded", "err", err)
				continue
			}
			n.CertExpiresAt, sent = exp, nil
		case <-renewals.C:
			renew()
		case v := <-out:
			if err := wsjson.Write(life, conn, v); err != nil {
				log.Info("control message not sent", "err", err)
			}
		}
	}
}

// hello reads the node's hello, which it must send first and in time.
func (s *Server) hello(ctx context.Context, conn *websocket.Conn) (hubapi.ControlHello, error) {
	ctx, cancel := context.WithTimeout(ctx, s.opts.Limits.Hello)
	defer cancel()
	var hello hubapi.ControlHello
	if err := wsjson.Read(ctx, conn, &hello); err != nil {
		return hello, err
	}
	if hello.Type != hubapi.ControlHelloTypeHello {
		_ = conn.Close(websocket.StatusPolicyViolation, "hello first") // the error returned says why
		return hello, errors.New("the first message is " + string(hello.Type) + ", not hello")
	}
	return hello, nil
}

// renew sends n a new certificate when two thirds of its current one's life
// have passed, returning when the new one ends.
func (s *Server) renew(ctx context.Context, conn *websocket.Conn, n node.Node) (time.Time, bool) {
	log := logging.FromContext(ctx)
	due := n.CertExpiresAt.Add(-ca.NodeValidity / 3)
	if s.now().Before(due) {
		return time.Time{}, false
	}
	cert, notAfter, err := s.issue(n)
	if err != nil {
		log.Error("certificate not issued", "err", err)
		return time.Time{}, false
	}
	if err := wsjson.Write(ctx, conn, hubapi.ControlCertificate{Type: hubapi.ControlCertificateTypeCertificate, Certificate: string(cert), ExpiresAt: notAfter}); err != nil {
		log.Info("certificate not sent", "err", err)
		return time.Time{}, false
	}
	log.Info("certificate sent", "cert_expires_at", n.CertExpiresAt, "expires_at", notAfter)
	return notAfter, true
}

// readControl reads the node's messages, handing on the expiry of each
// certificate it saved and saving the state it says, until the connection
// fails, which it cancels the connection with.
func (s *Server) readControl(ctx context.Context, cancel context.CancelCauseFunc, conn *websocket.Conn, n node.Node, saved chan<- time.Time) {
	log := logging.FromContext(ctx)
	for {
		_, b, err := conn.Read(ctx)
		if err != nil {
			cancel(err)
			return
		}
		var m hubapi.ControlMessage
		if err := json.Unmarshal(b, &m); err != nil {
			log.Info("control message ignored", "reason", "not JSON", "bytes", len(b))
			continue
		}
		switch m.Type {
		case string(hubapi.ControlCertificateSavedTypeCertificateSaved):
			var c hubapi.ControlCertificateSaved
			if err := json.Unmarshal(b, &c); err != nil {
				log.Info("control message ignored", "type", m.Type, "err", err)
				continue
			}
			select {
			case saved <- c.ExpiresAt:
			case <-ctx.Done():
				return
			}
		case string(hubapi.ControlStateTypeState):
			var st hubapi.ControlState
			if err := json.Unmarshal(b, &st); err != nil {
				log.Info("control message ignored", "type", m.Type, "err", err)
				continue
			}
			if err := s.nodes.SetState(ctx, n.ID, st.RemoteAccess, st.CodeSetup); err != nil {
				log.Error("node state not saved", "err", err)
			}
		default:
			// A later node may say what this hub does not know.
			log.Info("control message ignored", "type", m.Type)
		}
	}
}

// ping pings the node every Limits.Ping, closing the connection when no
// pong comes in time, and writes its last_seen_at at most once a minute.
func (s *Server) ping(ctx context.Context, cancel context.CancelCauseFunc, conn *websocket.Conn, n node.Node) {
	tick := time.NewTicker(s.opts.Limits.Ping)
	defer tick.Stop()
	seen := s.now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		pinged, stop := context.WithTimeout(ctx, s.opts.Limits.Ping)
		err := conn.Ping(pinged)
		stop()
		if err != nil {
			cancel(errors.Join(errors.New("no pong"), err))
			return
		}
		if now := s.now(); now.Sub(seen) >= seenEvery {
			if err := s.nodes.Seen(ctx, n.ID); err != nil {
				logging.FromContext(ctx).Error("last seen not written", "err", err)
			}
			seen = now
		}
	}
}

// closed ends the connection for cause: with a bye when the hub closes it,
// else as the connection itself ended.
func (s *Server) closed(ctx context.Context, conn *websocket.Conn, log *slog.Logger, cause error) {
	var bye byeCause
	switch {
	case errors.As(cause, &bye):
	case s.stopping.Err() != nil:
		bye.reason = node.ReasonShuttingDown
	default:
		log.Info("control closed", "reason", "connection", "err", cause)
		return
	}
	sending, cancel := context.WithTimeout(ctx, s.opts.Limits.Bye)
	defer cancel()
	if err := wsjson.Write(sending, conn, hubapi.ControlBye{Type: hubapi.ControlByeTypeBye, Reason: hubapi.ControlByeReason(bye.reason)}); err != nil {
		log.Info("bye not sent", "err", err)
	}
	status := websocket.StatusPolicyViolation
	if bye.reason == node.ReasonShuttingDown {
		status = websocket.StatusGoingAway
	}
	// Close waits for the node's close in reply, but no longer than its own
	// limit.
	if err := conn.Close(status, string(bye.reason)); err != nil {
		log.Info("close not answered", "err", err)
	}
	log.Info("control closed", "reason", string(bye.reason))
}
