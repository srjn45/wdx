package daemon

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/remote"
)

// idleMoveEvery is how often bytes through an attach relayed for a session
// move its idle time on, at most.
const idleMoveEvery = time.Minute

// AttachTerminal relays a client that cannot dial the holder's socket, one
// on another node, to the holder: the connection switched to a stream, which
// speaks the holder's protocol from the client's hello on.
func (s *server) AttachTerminal(ctx context.Context, req api.AttachTerminalRequestObject) (api.AttachTerminalResponseObject, error) {
	log := logging.FromContext(ctx)
	// An agent's or an editor's terminal is named by its id, which the
	// terminals of the API alone do not find.
	id, byID := strings.CutPrefix(req.Terminal, idPrefix)
	if !byID || req.Params.Project != nil || req.Params.Dir != nil {
		t, err := s.findTerminal(ctx, req.Terminal, req.Params.Project, req.Params.Dir)
		if err != nil {
			return nil, err
		}
		id = t.ID
	}
	session, throughSession := sessionOf(ctx)
	node := ""
	if throughSession {
		node = session.Node.Name
	}
	relay, _, err := s.terminals.Relay(ctx, id, node)
	if err != nil {
		return nil, err
	}
	resp := attachResponse{ctx: context.WithoutCancel(ctx), streams: s.nodeStreams, relay: relay, log: log}
	if throughSession {
		resp.idle = &idleMover{remote: s.remote, session: session, now: s.clock.Now, last: s.clock.Now()}
	}
	return resp, nil
}

// attachResponse switches the client's connection to a stream, answers its
// hello with the holder's, and copies bytes both ways until either closes.
type attachResponse struct {
	ctx     context.Context
	streams *nodeStreams
	relay   *holder.Relay
	idle    *idleMover // nil for a client on this node
	log     *slog.Logger
}

func (a attachResponse) VisitAttachTerminalResponse(w http.ResponseWriter) error {
	conn, in, ok := switchToStream(w, a.log)
	if !ok {
		a.relay.Conn.Close()
		return nil
	}
	if err := a.relay.Greet(a.ctx, in, conn); err != nil {
		a.log.Info("stream closed", "reason", "the client did not say hello", "err", err)
		conn.Close()
		a.relay.Conn.Close()
		return nil
	}
	var active func()
	if a.idle != nil {
		active = func() { a.idle.moved(a.ctx) }
	}
	a.streams.copy(a.log, conn, in, a.relay.Conn, active)
	return nil
}

// idleMover moves a session's idle time on while an attach through it passes
// bytes, which a person typing sends with no request, at most every
// idleMoveEvery.
type idleMover struct {
	remote  *remote.Remote
	session remote.Session
	now     func() time.Time

	mu   sync.Mutex
	last time.Time // when it was moved last
}

func (m *idleMover) moved(ctx context.Context) {
	now := m.now()
	m.mu.Lock()
	due := now.Sub(m.last) >= idleMoveEvery
	if due {
		m.last = now
	}
	m.mu.Unlock()
	if !due {
		return
	}
	// A session closed closes the stream too.
	if _, err := m.remote.Use(ctx, m.session.ID, m.session.Node); err != nil {
		logging.FromContext(ctx).Info("session idle time not moved on", "session_id", m.session.ID, "err", err)
	}
}
