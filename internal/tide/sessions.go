package tide

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
)

// A local TIDE shows in its bar the sessions other nodes have open into
// this one, following them with streamHubSessions (the wdx doc
// HUB_REMOTE_TIDE, "The host's indicator"): read at start and at every
// event, never on a timer.

// sessionStream is what TIDE reads the sessions into this node from: a
// *client.HubSessionStream, or a test's.
type sessionStream interface {
	Next() ([]api.HubSession, error)
	Close() error
}

// incoming is the sessions into this node, as their stream last said.
type incoming struct {
	gen    int // which stream this is: the messages of an older one are dropped
	cancel context.CancelFunc
	stream sessionStream
	nodes  []string      // the nodes of the sessions open, by when they opened
	wait   time.Duration // before a broken stream is opened again
}

// Messages of the sessions' stream, each of the stream gen.
type (
	sessionsOpened struct {
		gen    int
		stream sessionStream
		err    error
	}
	sessionsEvent struct {
		gen      int
		sessions []api.HubSession
		err      error
	}
	reopenSessions struct{ gen int }
)

// openHubSessions is the client's stream as a sessionStream.
func openHubSessions(c *client.Client) func(ctx context.Context) (sessionStream, error) {
	return func(ctx context.Context) (sessionStream, error) {
		s, err := c.HubSessionEvents(ctx)
		if err != nil {
			return nil, err
		}
		return s, nil
	}
}

// followSessions follows the sessions into this node, leaving the stream
// followed before.
func (m *Model) followSessions() tea.Cmd {
	m.unfollowSessions()
	if m.openSessions == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.incoming.gen++
	m.incoming.cancel = cancel
	return m.openIncoming(ctx)
}

func (m *Model) openIncoming(ctx context.Context) tea.Cmd {
	gen := m.incoming.gen
	return func() tea.Msg {
		s, err := m.openSessions(ctx)
		return sessionsOpened{gen: gen, stream: s, err: err}
	}
}

// unfollowSessions stops following the sessions.
func (m *Model) unfollowSessions() {
	in := &m.incoming
	if in.cancel != nil {
		in.cancel() // which ends the read waiting on it
		in.cancel = nil
	}
	if in.stream != nil {
		in.stream.Close()
		in.stream = nil
	}
	in.gen++
}

// nextSessions waits for the stream's next event.
func (m *Model) nextSessions() tea.Cmd {
	in := m.incoming
	return func() tea.Msg {
		list, err := in.stream.Next()
		return sessionsEvent{gen: in.gen, sessions: list, err: err}
	}
}

func (m *Model) sessionsOpened(msg sessionsOpened) tea.Cmd {
	in := &m.incoming
	switch {
	case msg.gen != in.gen:
		if msg.stream != nil {
			msg.stream.Close()
		}
		return nil
	case msg.err != nil:
		return m.sessionsBroke(msg.err)
	}
	in.stream = msg.stream
	return m.nextSessions()
}

func (m *Model) sessionsEvent(msg sessionsEvent) tea.Cmd {
	in := &m.incoming
	switch {
	case msg.gen != in.gen:
		return nil
	case msg.err != nil:
		return m.sessionsBroke(msg.err)
	}
	var nodes []string
	for _, s := range msg.sessions {
		if !slices.Contains(nodes, s.Node.Name) {
			nodes = append(nodes, s.Node.Name)
		}
	}
	if !slices.Equal(nodes, in.nodes) {
		m.log.Info("sessions into this node changed", "nodes", nodes)
	}
	in.nodes, in.wait = nodes, 0
	return m.nextSessions()
}

// sessionsBroke opens the stream again after a wait, as the project's, but
// for a daemon that refused it, which would refuse it again: one older
// than the stream. No session outlives the daemon, so none is shown
// meanwhile.
func (m *Model) sessionsBroke(err error) tea.Cmd {
	in := &m.incoming
	if in.stream != nil {
		in.stream.Close()
		in.stream = nil
	}
	in.nodes = nil
	if _, ok := errors.AsType[*client.ProblemError](err); ok {
		m.log.Info("sessions not followed", "err", err)
		return nil
	}
	in.wait = min(max(2*in.wait, firstReopenWait), maxReopenWait)
	m.log.Info("session stream broke", "err", err, "wait_ms", in.wait.Milliseconds())
	gen := in.gen
	return m.tick(in.wait, func(time.Time) tea.Msg { return reopenSessions{gen: gen} })
}

func (m *Model) reopenSessions(msg reopenSessions) tea.Cmd {
	if msg.gen != m.incoming.gen || m.incoming.cancel == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.incoming.cancel()
	m.incoming.cancel = cancel
	return m.openIncoming(ctx)
}

// remoteSessions is what the bar says of the sessions into this node, ""
// with none open.
func (m *Model) remoteSessions() string {
	if len(m.incoming.nodes) == 0 {
		return ""
	}
	return "remote: " + m.st.BarName(strings.Join(m.incoming.nodes, ", "))
}
