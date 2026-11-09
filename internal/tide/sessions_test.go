package tide

import (
	"context"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// fakeSessions is a stream of the sessions into this node that the test
// sends events on; closing it ends it.
type fakeSessions struct {
	events chan []api.HubSession
	ended  chan struct{}
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{events: make(chan []api.HubSession, 1), ended: make(chan struct{})}
}

func (f *fakeSessions) Next() ([]api.HubSession, error) {
	select {
	case list := <-f.events:
		return list, nil
	case <-f.ended:
		return nil, io.EOF
	}
}

func (f *fakeSessions) Close() error { return nil }

func incomingFrom(nodes ...string) []api.HubSession {
	list := []api.HubSession{}
	for _, n := range nodes {
		list = append(list, api.HubSession{Id: "sess-" + n, Direction: api.HubSessionDirectionIncoming, Node: api.HubNodeRef{Id: "node-" + n, Name: n}})
	}
	return list
}

// TestHostIndicator: a local TIDE's bar says, in the Remote style, which
// nodes have a session into this one while any has, as the stream says at
// its start and at every event, asking for no timer; a stream that breaks
// shows none and is opened again after a wait.
func TestHostIndicator(t *testing.T) {
	t.Parallel()
	st := style.New(true)
	m := New(context.Background(), Options{Style: st})
	tk := &ticks{}
	m.tick = tk.tick
	m.projects.loaded = true
	m.show("demo")
	sent(m, "shell")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	stream, opens := newFakeSessions(), 0
	m.openSessions = func(context.Context) (sessionStream, error) {
		opens++
		return stream, nil
	}
	remote := strings.TrimSuffix(st.Bar("", style.StatusRemote), "\x1b[0m")
	live := strings.TrimSuffix(st.Bar("", style.StatusLive), "\x1b[0m")
	waits := len(tk.waits) // what showing the project asked for
	// step sends the stream's next event and runs what follows from it, up
	// to the wait for the one after.
	next := m.followSessions()
	step := func(list []api.HubSession) {
		t.Helper()
		_, next = m.Update(next())
		stream.events <- list
		_, next = m.Update(next())
	}
	wantBar := func(prefix, style string) {
		t.Helper()
		if got := lastLine(m); !strings.HasPrefix(got, prefix) {
			t.Errorf("bar %q, want it to start with %q", got, prefix)
		}
		if bar := m.statusLine(); !strings.HasPrefix(bar, style) {
			t.Errorf("bar %q, want it in %q", bar, style)
		}
	}

	step(incomingFrom())
	wantBar(" demo · project", live)
	stream.events <- incomingFrom("laptop")
	_, next = m.Update(next())
	wantBar(" remote: laptop · demo · project", remote)
	stream.events <- incomingFrom("laptop", "work", "laptop")
	_, next = m.Update(next())
	wantBar(" remote: laptop, work · demo · project", remote)
	stream.events <- incomingFrom()
	_, next = m.Update(next())
	wantBar(" demo · project", live)
	if opens != 1 || len(tk.waits) != waits {
		t.Fatalf("opened %d times, %d waits asked for: want the stream followed once, on no timer", opens, len(tk.waits)-waits)
	}

	stream.events <- incomingFrom("laptop")
	_, next = m.Update(next())
	close(stream.ended)
	if _, cmd := m.Update(next()); cmd != nil {
		t.Fatal("a broken stream opened again at once")
	}
	wantBar(" demo · project", live)
	reopens := tk.waits[waits:]
	if len(reopens) != 1 || reopens[0].d != firstReopenWait {
		t.Fatalf("waits %v, want one of %s before opening again", reopens, firstReopenWait)
	}
	stream = newFakeSessions()
	_, reopen := m.Update(reopens[0].fn(m.now()))
	m.Update(reopen())
	if opens != 2 {
		t.Fatalf("opened %d times, want again after the wait", opens)
	}
}

// TestRemoteNoIndicator: a remote TIDE follows no sessions: those into the
// host are the host's, which a session cannot read.
func TestRemoteNoIndicator(t *testing.T) {
	t.Parallel()
	m, _ := remoteModel(style.New(false), noSession)
	if m.openSessions != nil || m.followSessions() != nil {
		t.Fatal("a remote TIDE follows the sessions")
	}
}
