package daemon

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/logging"
)

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
	version := holder.Version
	if req.Params.HolderVersion != nil {
		version = *req.Params.HolderVersion
	}
	relay, _, err := s.terminals.Relay(ctx, id, node, version)
	if err != nil {
		return nil, err
	}
	return attachResponse{ctx: context.WithoutCancel(ctx), streams: s.nodeStreams, relay: relay, log: log}, nil
}

// attachResponse switches the client's connection to a stream, answers its
// hello with the holder's, and copies bytes both ways until either closes.
type attachResponse struct {
	ctx     context.Context
	streams *nodeStreams
	relay   *holder.Relay
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
	a.streams.copy(a.log, conn, in, a.relay.Conn)
	return nil
}
