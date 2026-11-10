package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/hublink"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/remote"
)

// sendHubState tells the hub, through the link, whether this node allows
// sessions and has a code for them: at start and at every change of either.
// The hub only shows it; this node decides every session itself.
func sendHubState(ctx context.Context, link *hublink.Link, access *hubAccess) {
	link.SendState(hublink.State{RemoteAccess: access.remoteAccess.Load(), CodeSetup: access.codeSetUp(ctx)})
}

// nodesGone hands the remote sessions what the link hears of nodes removed
// from the account, once the daemon made them: the link is opened first,
// and may hear it before.
type nodesGone struct {
	ready  chan struct{}
	remote *remote.Remote // set before ready closes
}

func newNodesGone() *nodesGone { return &nodesGone{ready: make(chan struct{})} }

// start hands r what is heard from now on, and what waited for it.
func (g *nodesGone) start(r *remote.Remote) {
	g.remote = r
	close(g.ready)
}

// wait is the remote sessions once started, nil when ctx ends first.
func (g *nodesGone) wait(ctx context.Context) *remote.Remote {
	select {
	case <-g.ready:
		return g.remote
	case <-ctx.Done():
		return nil
	}
}

// removed closes the sessions with n, which the hub says was removed.
func (g *nodesGone) removed(ctx context.Context, n hublink.NodeRef) {
	if r := g.wait(ctx); r != nil {
		if err := r.NodeGone(ctx, n.ID); err != nil {
			logging.FromContext(ctx).Error("sessions with a removed node not closed", "node_id", n.ID, "err", err)
		}
	}
}

// listed closes the sessions with every node not among the account's.
func (g *nodesGone) listed(ctx context.Context, nodes []hublink.NodeRef) {
	if r := g.wait(ctx); r != nil {
		if err := r.AccountNodes(ctx, nodes); err != nil {
			logging.FromContext(ctx).Error("sessions with removed nodes not closed", "err", err)
		}
	}
}

// hubStreams answers the streams other nodes ask the hub for, each with the
// remote sessions, until stop; wait waits for those being answered.
type hubStreams struct {
	stop    context.CancelFunc
	serving sync.WaitGroup
}

// serveHubStreams starts answering the link's streams with r, each logged
// with base and a debug id of its own: base is the daemon's logger before
// its run's debug id, which a stream's line would carry besides its own.
func serveHubStreams(ctx context.Context, base *slog.Logger, link *hublink.Link, r *remote.Remote) *hubStreams {
	ctx, stop := context.WithCancel(ctx)
	h := &hubStreams{stop: stop}
	h.serving.Go(func() {
		for {
			select {
			case s := <-link.Streams():
				h.serving.Go(func() {
					r.Serve(logging.NewContext(ctx, base.With("debug_id", logging.NewDebugID())), s)
				})
			case <-ctx.Done():
				return
			}
		}
	})
	return h
}

// close stops answering streams and waits for those being answered, which
// end with the link closed before.
func (h *hubStreams) close() {
	h.stop()
	h.serving.Wait()
}

func (s *server) ListHubSessions(ctx context.Context, _ api.ListHubSessionsRequestObject) (api.ListHubSessionsResponseObject, error) {
	list, err := s.remote.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	return api.ListHubSessions200JSONResponse{Sessions: sessionViews(list)}, nil
}

func (s *server) StreamHubSessions(ctx context.Context, _ api.StreamHubSessionsRequestObject) (api.StreamHubSessionsResponseObject, error) {
	return &hubSessionStream{ctx: ctx, s: s}, nil
}

// hubSessionStream sends the incoming sessions, then again after every
// session opened or closed and every wrong code, until the client leaves or the daemon stops.
type hubSessionStream struct {
	ctx context.Context
	s   *server
}

func (st *hubSessionStream) VisitStreamHubSessionsResponse(w http.ResponseWriter) error {
	log := logging.FromContext(st.ctx)
	changed, stop := st.s.sessionNotices.subscribe() // before the first read, so no change is missed
	defer stop()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flush := http.NewResponseController(w).Flush

	var last []byte
	sent := 0
	reason := "client left"
	defer func() { log.Info("session stream ended", "events", sent, "reason", reason) }()
	for {
		data, err := st.next()
		if err != nil {
			// The status is sent: the stream can only end.
			log.Error("sessions not read for their stream", "err", err)
			reason = "sessions not read"
			return nil
		}
		if !bytes.Equal(data, last) {
			if _, err := fmt.Fprintf(w, "event: sessions\ndata: %s\n\n", data); err != nil {
				return nil // the client is gone
			}
			if err := flush(); err != nil {
				return nil
			}
			last = data
			sent++
			log.Debug("session event sent", "bytes", len(data))
		}
		select {
		case <-st.ctx.Done():
			return nil
		case <-st.s.stopping:
			reason = "daemon stopping"
			return nil
		case <-changed:
		}
	}
}

// next is the stream's next event: the incoming sessions open, none when
// the node is logged out or allows no remote access, and the wrong codes
// since the last opened.
func (st *hubSessionStream) next() ([]byte, error) {
	incoming := []api.HubSession{}
	wrong := api.HubWrongCodes{}
	if st.s.hub.Status().LoggedIn() {
		wrong = st.s.wrongCodes(st.ctx)
	}
	if st.s.hub.Status().LoggedIn() && st.s.access.remoteAccess.Load() {
		list, err := st.s.remote.Sessions(st.ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range list {
			if s.Direction == remote.Incoming {
				incoming = append(incoming, sessionView(s))
			}
		}
	}
	return json.Marshal(api.HubSessionEvent{Sessions: incoming, WrongCodes: wrong})
}

func (s *server) OpenHubSession(ctx context.Context, req api.OpenHubSessionRequestObject) (api.OpenHubSessionResponseObject, error) {
	n, err := s.hub.Node(ctx, req.Body.Node)
	if err != nil {
		return nil, hubFailure(err)
	}
	session, opened, err := s.remote.Open(ctx, hublink.NodeRef{ID: n.Id, Name: n.Name}, req.Body.Code)
	if err != nil {
		return nil, sessionFailure(err)
	}
	if !opened {
		return api.OpenHubSession200JSONResponse{Result: api.ResultUnchanged, Session: sessionView(session)}, nil
	}
	return api.OpenHubSession201JSONResponse{Result: api.ResultOpened, Session: sessionView(session)}, nil
}

func (s *server) CloseHubSession(ctx context.Context, req api.CloseHubSessionRequestObject) (api.CloseHubSessionResponseObject, error) {
	session, err := s.remote.CloseSession(ctx, req.Id)
	if err != nil {
		return nil, sessionFailure(err)
	}
	return api.CloseHubSession200JSONResponse{Result: api.ResultClosed, Session: sessionView(session)}, nil
}

func (s *server) CloseHubSessions(ctx context.Context, _ api.CloseHubSessionsRequestObject) (api.CloseHubSessionsResponseObject, error) {
	closed, err := s.remote.CloseAll(ctx)
	if err != nil {
		return nil, sessionFailure(err)
	}
	return api.CloseHubSessions200JSONResponse{Result: api.ResultClosed, Sessions: sessionViews(closed)}, nil
}

// sessionFailure is err, of a session, with the code the API gives it.
func sessionFailure(err error) error {
	if e, ok := errors.AsType[*remote.Error](err); ok {
		return &hubError{code: api.ErrorCode(e.Code), err: e, details: e.Details}
	}
	if e, ok := errors.AsType[*remote.StreamEndedError](err); ok {
		return &hubError{code: api.ErrorCodeNodeOffline, err: e}
	}
	return hubFailure(err)
}

func sessionView(s remote.Session) api.HubSession {
	return api.HubSession{
		Id: s.ID, Direction: api.HubSessionDirection(s.Direction), Node: api.HubNodeRef{Id: s.Node.ID, Name: s.Node.Name},
		OpenedAt: s.OpenedAt.UTC(), LastUsedAt: s.LastUsedAt.UTC(), IdleExpiresAt: s.IdleExpiresAt.UTC(),
	}
}

func sessionViews(list []remote.Session) []api.HubSession {
	views := make([]api.HubSession, len(list))
	for i, s := range list {
		views[i] = sessionView(s)
	}
	return views
}
