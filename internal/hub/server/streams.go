package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/coder/websocket"

	"github.com/srjn45/wdx/internal/hub/node"
	"github.com/srjn45/wdx/internal/hub/relay"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/logging"
)

var (
	// errNodeOffline is a stream asked for a node without a control
	// connection, or that did not join it.
	errNodeOffline = errors.New("the node is not connected to the hub")
	// errNodeNotFound is a stream asked for a node the account does not
	// have, or the caller itself.
	errNodeNotFound = errors.New("the account has no such other node")
)

func (a *api) OpenStream(ctx context.Context, req hubapi.OpenStreamRequestObject) (hubapi.OpenStreamResponseObject, error) {
	s := a.server
	log := logging.FromContext(ctx)
	this := caller(ctx)
	target := req.Body.NodeId
	host, err := s.nodes.Node(ctx, this.UserID, "id:"+target)
	if errors.Is(err, node.ErrNotFound) || (err == nil && host.ID == this.ID) {
		log.Info("stream refused", "reason", "node_not_found", "node_id", target)
		return nil, &nodeError{ref: "id:" + target, err: errNodeNotFound}
	}
	if err != nil {
		return nil, err
	}
	if !s.nodes.Registry().Online(host.ID) {
		log.Info("stream refused", "reason", "node_offline", "node_id", host.ID)
		return nil, &nodeError{ref: "id:" + host.ID, err: errNodeOffline}
	}
	id, err := s.relay.Open(ctx, relay.Nodes{UserID: this.UserID, ClientID: this.ID, HostID: host.ID})
	if err != nil {
		return nil, err
	}
	waiting := hubapi.ControlStreamWaiting{Type: hubapi.ControlStreamWaitingTypeStreamWaiting, Stream: id, Node: hubapi.NodeRef{Id: this.ID, Name: this.Name}}
	if !s.nodes.Registry().Send(host.ID, waiting) {
		// The stream expires unjoined: the client's join is told
		// node_offline.
		log.Info("stream waiting not sent", "stream_id", id, "node_id", host.ID)
	}
	return hubapi.OpenStream201JSONResponse{Stream: hubapi.Stream{Id: id, Node: hubapi.NodeRef{Id: host.ID, Name: host.Name}}}, nil
}

func (a *api) JoinStream(ctx context.Context, req hubapi.JoinStreamRequestObject) (hubapi.JoinStreamResponseObject, error) {
	s := a.server
	if !s.beginControl() {
		logging.FromContext(ctx).Info("stream join refused", "stream_id", req.Id, "reason", "the hub is stopping")
		return nil, errStopping
	}
	// The hub stopping ends the wait for the other node too.
	waiting, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.stopping, cancel)
	j, err := s.relay.Join(waiting, req.Id, caller(ctx).ID)
	stop()
	cancel()
	if err != nil {
		s.controls.Done()
		if errors.Is(err, context.Canceled) && s.stopping.Err() != nil {
			return nil, errStopping
		}
		return nil, err
	}
	r, _ := ctx.Value(requestKey{}).(*http.Request)
	return streamResponse{server: s, r: r, joined: j}, nil
}

// streamResponse switches a stream's side to its WebSocket and copies it to
// the other's until the stream ends.
type streamResponse struct {
	server *Server
	r      *http.Request
	joined *relay.Joined
}

func (sr streamResponse) VisitJoinStreamResponse(w http.ResponseWriter) error {
	s := sr.server
	defer s.controls.Done()
	log := logging.FromContext(sr.r.Context())
	conn, err := websocket.Accept(w, sr.r, nil)
	if err != nil {
		// Accept answered the request itself; the other side is closed.
		log.Info("stream join failed", "err", err)
		conn = nil
	}
	// The stream outlives the request's context: only the hub stopping
	// ends it besides the nodes.
	sr.joined.Run(logging.NewContext(s.stopping, log), conn)
	return nil
}
