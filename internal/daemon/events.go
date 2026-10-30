package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/project"
)

// notices tells the open streams that something changed. A notice says
// only that: each stream reads its project again and sends it when it
// differs from what it sent last, so changes need not say what they touched.
type notices struct {
	mu      sync.Mutex
	streams map[chan struct{}]struct{}
}

func newNotices() *notices {
	return &notices{streams: map[chan struct{}]struct{}{}}
}

// subscribe returns a channel that gets a value after changes, and the
// function that stops it. The channel holds one value: changes that come
// while the stream is busy sending collapse into one.
func (n *notices) subscribe() (<-chan struct{}, func()) {
	c := make(chan struct{}, 1)
	n.mu.Lock()
	n.streams[c] = struct{}{}
	n.mu.Unlock()
	return c, func() {
		n.mu.Lock()
		delete(n.streams, c)
		n.mu.Unlock()
	}
}

// notify tells every stream that something changed. It never blocks.
func (n *notices) notify() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for c := range n.streams {
		select {
		case c <- struct{}{}:
		default: // one is already pending
		}
	}
}

func (s *server) StreamProject(ctx context.Context, req api.StreamProjectRequestObject) (api.StreamProjectResponseObject, error) {
	p, err := s.find(ctx, req.Project)
	if err != nil {
		return nil, err
	}
	return &projectStream{ctx: ctx, s: s, id: p.ID}, nil
}

// projectStream sends the project with id whole, then again after every
// change to it, until the client leaves, the project is removed or the
// daemon stops.
type projectStream struct {
	ctx context.Context
	s   *server
	id  string
}

func (st *projectStream) VisitStreamProjectResponse(w http.ResponseWriter) error {
	log := logging.FromContext(st.ctx)
	changed, stop := st.s.notices.subscribe() // before the first read, so no change is missed
	defer stop()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flush := http.NewResponseController(w).Flush

	var last []byte
	sent := 0
	reason := "client left"
	defer func() { log.Info("project stream ended", "project_id", st.id, "events", sent, "reason", reason) }()
	for {
		event, data, err := st.next()
		if err != nil {
			// The status is sent: the stream can only end.
			log.Error("project not read for its stream", "project_id", st.id, "err", err)
			reason = "project not read"
			return nil
		}
		if !bytes.Equal(data, last) {
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
				return nil // the client is gone
			}
			if err := flush(); err != nil {
				return nil
			}
			last = data
			sent++
			log.Debug("project event sent", "project_id", st.id, "event", event, "bytes", len(data))
		}
		if event == "removed" {
			reason = "project removed"
			return nil
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

// next returns the stream's next event: the project as getProject returns
// it, or removed once it is gone.
func (st *projectStream) next() (event string, data []byte, err error) {
	p, err := st.s.projects.GetByID(st.ctx, st.id)
	if errors.Is(err, project.ErrNotFound) {
		data, err := json.Marshal(api.ProjectRemoved{ProjectId: st.id})
		return "removed", data, err
	}
	if err != nil {
		return "", nil, err
	}
	v, err := st.s.detail(st.ctx, p)
	if err != nil {
		return "", nil, err
	}
	data, err = json.Marshal(api.ProjectEnvelope{Project: v})
	return "project", data, err
}

// streamOperation is the operation whose response is a stream, which the
// log describes rather than holds.
const streamOperation = "streamProject"
