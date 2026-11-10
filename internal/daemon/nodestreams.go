package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/getkin/kin-openapi/routers"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/remote"
)

// The client's side: connectHubNode turns a connection of the CLI into a
// stream of the session open into a node, over which the CLI speaks HTTP to
// that node's API.

func (s *server) ConnectHubNode(ctx context.Context, req api.ConnectHubNodeRequestObject) (api.ConnectHubNodeResponseObject, error) {
	log := logging.FromContext(ctx)
	session, err := s.outgoingSession(ctx, req.Node)
	if err != nil {
		return nil, err
	}
	conn, _, err := s.remote.Stream(ctx, session.Node.ID)
	if e, ok := errors.AsType[*remote.Error](err); ok && (e.Code == remote.CodeSessionClosed || e.Code == remote.CodeSessionNotFound) {
		// The host closed it since: as good as none, so a code opens another.
		return nil, &hubError{code: api.ErrorCodeSessionRequired, err: fmt.Errorf("the session into node %s closed: %w", session.Node.Name, e), details: e.Details}
	}
	if err != nil {
		return nil, sessionFailure(err)
	}
	log.Info("node stream opened", "session_id", session.ID, "node_id", session.Node.ID, "node_name", session.Node.Name)
	return connectResponse{streams: s.nodeStreams, stream: conn, log: log}, nil
}

// outgoingSession is the session open from this node into the node ref
// names, by its name whatever its case, or "id:" and its id: it never asks
// the hub, as a session names its node.
func (s *server) outgoingSession(ctx context.Context, ref string) (remote.Session, error) {
	list, err := s.remote.Sessions(ctx)
	if err != nil {
		return remote.Session{}, err
	}
	id, byID := strings.CutPrefix(ref, "id:")
	for _, session := range list {
		if session.Direction == remote.Outgoing && (byID && session.Node.ID == id || !byID && strings.EqualFold(session.Node.Name, ref)) {
			return session, nil
		}
	}
	return remote.Session{}, &hubError{code: api.ErrorCodeSessionRequired, err: fmt.Errorf("no session into node %s is open", ref), details: map[string]any{"node": ref}}
}

// connectResponse switches the CLI's connection to stream, and copies bytes
// both ways until either closes.
type connectResponse struct {
	streams *nodeStreams
	stream  net.Conn
	log     *slog.Logger
}

func (c connectResponse) VisitConnectHubNodeResponse(w http.ResponseWriter) error {
	conn, in, ok := switchToStream(w, c.log)
	if !ok {
		c.stream.Close()
		return nil
	}
	c.streams.copy(c.log, conn, in, c.stream)
	return nil
}

// switchToStream answers 101 and takes the connection over, which what the
// client sends next is read from through in. false is a client gone before it
// switched, which is logged.
func switchToStream(w http.ResponseWriter, log *slog.Logger) (net.Conn, io.Reader, bool) {
	header := w.Header().Clone()
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		log.Info("stream closed", "reason", "the connection could not be taken over", "err", err)
		return nil, nil, false
	}
	// The server's deadlines were for the request; the stream has none.
	_ = conn.SetDeadline(time.Time{})
	header.Set("Connection", "Upgrade")
	header.Set("Upgrade", api.StreamUpgrade)
	_, err = fmt.Fprintf(rw, "HTTP/1.1 %d %s\r\n", http.StatusSwitchingProtocols, http.StatusText(http.StatusSwitchingProtocols))
	if err == nil {
		err = header.Write(rw)
	}
	if err == nil {
		_, err = rw.WriteString("\r\n")
	}
	if err == nil {
		err = rw.Flush()
	}
	if err != nil {
		// The client is gone: the 101 is logged, and the stream closed.
		log.Info("stream closed", "reason", "the client left before it switched", "err", err)
		conn.Close()
		return nil, nil, false
	}
	return conn, rw.Reader, true
}

// nodeStreams are the connections switched to a stream to another node,
// which the daemon closes when it stops.
type nodeStreams struct {
	mu      sync.Mutex
	open    map[net.Conn]net.Conn // the CLI's connection, the stream it is switched to
	closed  bool
	copying sync.WaitGroup
}

func newNodeStreams() *nodeStreams {
	return &nodeStreams{open: map[net.Conn]net.Conn{}}
}

// copy copies bytes between the CLI's connection, read through in, and
// stream until either ends, then closes both and logs the bytes counted.
func (n *nodeStreams) copy(log *slog.Logger, conn net.Conn, in io.Reader, stream net.Conn) {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		conn.Close()
		stream.Close()
		return
	}
	n.open[conn] = stream
	n.copying.Add(1)
	n.mu.Unlock()
	start := time.Now()
	go func() {
		defer n.copying.Done()
		var sent, received int64 // read once both copies ended
		var once sync.Once
		end := func() {
			once.Do(func() {
				conn.Close()
				stream.Close()
			})
		}
		var both sync.WaitGroup
		both.Go(func() {
			sent, _ = io.Copy(stream, in)
			end()
		})
		both.Go(func() {
			received, _ = io.Copy(conn, stream)
			end()
		})
		both.Wait()
		n.mu.Lock()
		delete(n.open, conn)
		n.mu.Unlock()
		log.Info("stream closed", "bytes_sent", sent, "bytes_received", received, logging.Duration(start))
	}()
}

// close closes every stream and waits for their copies to end.
func (n *nodeStreams) close() {
	n.mu.Lock()
	n.closed = true
	for conn, stream := range n.open {
		conn.Close()
		stream.Close()
	}
	n.mu.Unlock()
	n.copying.Wait()
}

// The host's side: each stream of a session into this node is a connection
// of an HTTP server of its own, which serves the API through remoteRules.

// sessionStreams hands the streams of the sessions into this node to the
// session server, as its listener.
type sessionStreams struct {
	conns  chan *sessionConn
	done   chan struct{}
	closed sync.Once
}

func newSessionStreams() *sessionStreams {
	return &sessionStreams{conns: make(chan *sessionConn), done: make(chan struct{})}
}

func (l *sessionStreams) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *sessionStreams) Close() error {
	l.closed.Do(func() { close(l.done) })
	return nil
}

func (l *sessionStreams) Addr() net.Addr { return sessionAddr{} }

type sessionAddr struct{}

func (sessionAddr) Network() string { return "wdx-session" }
func (sessionAddr) String() string  { return "wdx-session" }

// serve serves conn, a stream of session, until the session server is done
// with it: remote.Options.ServeStream. The server reads one end of a pipe
// copied to conn rather than conn: it interrupts its reads with a deadline
// past, which would close the WebSocket under conn rather than interrupt it.
//
// Only the copy to conn closes conn, once it has passed on all the server
// wrote: the pipe hands bytes over before they reach conn, so closing conn as
// the server closes its end would lose its last answer, such as the
// session_closed it closes the stream after.
func (l *sessionStreams) serve(ctx context.Context, conn net.Conn, session remote.Session) {
	served, pipe := net.Pipe()
	c := &sessionConn{Conn: served, session: session, done: make(chan struct{})}
	var copying sync.WaitGroup
	var once sync.Once
	end := func() {
		once.Do(func() {
			pipe.Close()
			conn.Close()
		})
	}
	copying.Go(func() {
		_, _ = io.Copy(pipe, conn)
		// The client left, or the server: the server reads the end, and
		// the copy to conn ends with it.
		pipe.Close()
	})
	copying.Go(func() {
		_, _ = io.Copy(conn, pipe)
		end()
	})
	defer copying.Wait()
	select {
	case l.conns <- c:
	case <-l.done:
		end()
		return
	case <-ctx.Done():
		end()
		return
	}
	<-c.done
	logging.FromContext(ctx).Info("session stream ended", "reason", "the session server closed it")
}

// sessionConn is a stream of a session, which the session server's
// requests know it by.
type sessionConn struct {
	net.Conn
	session remote.Session
	done    chan struct{}
	once    sync.Once
}

func (c *sessionConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { close(c.done) })
	return err
}

type sessionKey struct{}

// withSession is ctx of a request that came through conn, when it is a
// stream of a session.
func withSession(ctx context.Context, conn net.Conn) context.Context {
	if c, ok := conn.(*sessionConn); ok {
		return context.WithValue(ctx, sessionKey{}, c.session)
	}
	return ctx
}

// sessionOf is the session the request in ctx came through; false for a
// request on the socket.
func sessionOf(ctx context.Context) (remote.Session, bool) {
	s, ok := ctx.Value(sessionKey{}).(remote.Session)
	return s, ok
}

// remoteRules serves a request that came through a session only when the
// session is open still, which it moves the idle time of, the client speaks
// an API version this node does, and its operation is one remoteOperations
// allows.
func (d *Daemon) remoteRules(router routers.Router, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := logging.FromContext(ctx)
		w.Header().Set(api.APIVersionHeader, strconv.Itoa(api.CurrentVersion))
		session, ok := sessionOf(ctx)
		if !ok {
			failed(w, r, &hubError{code: api.ErrorCodeRouteNotAllowed, err: errors.New("the request came through no session")})
			return
		}
		if _, err := d.remote.Use(ctx, session.ID, session.Node); err != nil {
			// The client sees the stream end after this answer.
			w.Header().Set("Connection", "close")
			failed(w, r, sessionFailure(err))
			return
		}
		version, err := strconv.Atoi(r.Header.Get(api.APIVersionHeader))
		if err != nil || version < api.OldestCompatible {
			failed(w, r, &hubError{code: api.ErrorCodeNodeVersionUnsupported,
				err:     fmt.Errorf("node %s speaks API version %q, older than %d, the oldest this node speaks: update wdx on %s", session.Node.Name, r.Header.Get(api.APIVersionHeader), api.OldestCompatible, session.Node.Name),
				details: map[string]any{"update": "client", "node": session.Node.Name, "client_version": r.Header.Get(api.APIVersionHeader), "oldest_compatible": api.OldestCompatible}})
			return
		}
		route, _, err := router.FindRoute(r)
		if err != nil {
			failed(w, r, &hubError{code: api.ErrorCodeRouteNotAllowed,
				err:     fmt.Errorf("this node has no %s %s: update wdx on this node, the host", r.Method, r.URL.Path),
				details: map[string]any{"update": "host"}})
			return
		}
		operation := route.Operation.OperationID
		rule, known := remoteOperations[operation]
		if !known || !rule.allowed {
			reason := rule.reason
			if !known {
				reason = "it is not classified"
			}
			log.Info("request refused", "reason", "not served through a session", "operation", operation)
			failed(w, r, &hubError{code: api.ErrorCodeRouteNotAllowed,
				err:     fmt.Errorf("%s is not served through a session: %s", operation, reason),
				details: map[string]any{"operation": operation}})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// remoteRule is whether an operation is served through a session, and why
// not when it is not.
type remoteRule struct {
	allowed bool
	reason  string
}

var allowed = remoteRule{allowed: true}

func refused(reason string) remoteRule { return remoteRule{reason: reason} }

// The reasons operations are refused through a session.
var (
	refusedHub      = refused("the hub's routes are this node's own: no session opens another to a third node, sets up a code, logs in or out")
	refusedSettings = refused("the settings are this node's own, its hub.* settings among them")
	refusedDaemon   = refused("the daemon is managed only on its own machine")
	refusedTide     = refused("TIDE's state is the host's own TIDE's, which a remote TIDE would overwrite")
)

// remoteOperations classifies every operation of api/openapi.yaml: served
// through a session, or refused with its reason. One not in it is refused,
// and a test fails on it, so that a new operation is decided on.
var remoteOperations = map[string]remoteRule{
	"getVersion":    allowed,
	"getDaemon":     refusedDaemon,
	"reloadDaemon":  refusedDaemon,
	"listSettings":  refusedSettings,
	"updateSetting": refusedSettings,

	"listProjects":      allowed,
	"createProject":     allowed,
	"getProject":        allowed,
	"updateProject":     allowed,
	"deleteProject":     allowed,
	"streamProject":     allowed,
	"recloneProject":    allowed,
	"refreshWorktrees":  allowed,
	"getProjectLanding": allowed,
	"setProjectLanding": allowed,
	"getProjectSync":    allowed,
	"updateProjectSync": allowed,

	"replaceTabs":        refusedTide,
	"getProjectView":     refusedTide,
	"replaceProjectView": refusedTide,
	"getTideView":        refusedTide,
	"replaceTideView":    refusedTide,

	"listWorktrees":          allowed,
	"removeWorktree":         allowed,
	"landWorktree":           allowed,
	"rebaseWorktree":         allowed,
	"getWorktreeDiff":        allowed,
	"listWorktreeFiles":      allowed,
	"createWorktreeFile":     allowed,
	"renameWorktreeFile":     allowed,
	"removeWorktreeFile":     allowed,
	"getWorktreeFileContent": allowed,
	"getWorktreeFileDiff":    allowed,
	"verifyWorktreeFile":     allowed,
	"unverifyWorktreeFile":   allowed,
	"listWorktreeComments":   allowed,
	"createWorktreeComment":  allowed,
	"editWorktreeComment":    allowed,
	"deleteWorktreeComment":  allowed,
	"sendWorktreeReview":     allowed,

	"listMessages":   allowed,
	"discardMessage": allowed,
	"resendMessage":  allowed,

	"listTerminals":      allowed,
	"createTerminal":     allowed,
	"getTerminal":        allowed,
	"deleteTerminal":     allowed,
	"startTerminal":      allowed,
	"sendTerminal":       allowed,
	"getTerminalHistory": allowed,
	"attachTerminal":     allowed,

	"listAgents":         allowed,
	"createAgent":        allowed,
	"getAgent":           allowed,
	"updateAgent":        allowed,
	"deleteAgent":        allowed,
	"startAgent":         allowed,
	"restartAgent":       allowed,
	"sendAgent":          allowed,
	"getAgentHistory":    allowed,
	"getAgentTranscript": allowed,
	"listAcas":           allowed,
	"listAcaModels":      allowed,
	"listUsage":          allowed,
	"openEditor":         allowed,

	"listDocs":          allowed,
	"createDoc":         allowed,
	"syncDocsToGit":     allowed,
	"getDoc":            allowed,
	"updateDoc":         allowed,
	"deleteDoc":         allowed,
	"updateDocContent":  allowed,
	"listDocRevisions":  allowed,
	"listPlans":         allowed,
	"createPlan":        allowed,
	"getPlan":           allowed,
	"updatePlan":        allowed,
	"deletePlan":        allowed,
	"updatePlanContent": allowed,
	"listPlanRevisions": allowed,
	"runPlan":           allowed,
	"pausePlan":         allowed,
	"copyPlan":          allowed,
	"archivePlan":       allowed,
	"unarchivePlan":     allowed,
	"resumePlan":        allowed,
	"cancelPlan":        allowed,
	"retryPlanTask":     allowed,
	"landPlanTask":      allowed,
	"skipPlanTask":      allowed,
	"finishPlanTask":    allowed,

	"getHub":            refusedHub,
	"startHubLogin":     refusedHub,
	"streamHubLogin":    refusedHub,
	"cancelHubLogin":    refusedHub,
	"sendHubLoginCode":  refusedHub,
	"logoutHub":         refusedHub,
	"listHubNodes":      refusedHub,
	"getHubNode":        refusedHub,
	"removeHubNode":     refusedHub,
	"connectHubNode":    refusedHub,
	"startHubCode":      refusedHub,
	"verifyHubCode":     refusedHub,
	"recoverHubCode":    refusedHub,
	"listHubSessions":   refusedHub,
	"streamHubSessions": refusedHub,
	"openHubSession":    refusedHub,
	"closeHubSessions":  refusedHub,
	"closeHubSession":   refusedHub,
	"verifyHubAudit":    refusedHub,
}

// newSessionServer is the HTTP server of the streams of sessions into this
// node, serving handler.
func newSessionServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ConnContext:       withSession,
	}
}
