package tide

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/style"
)

// sessionsModel is a local TIDE following a sessions stream the test feeds
// with feed, whose client asks a daemon of the test's: it closes sessions and
// removes nodes, but refuses to remove the node down, and asked says what it
// was asked, in order.
func sessionsModel(t *testing.T) (m *Model, feed func(nodes ...string), asked func() []string) {
	t.Helper()
	m, _ = newModel(100, 30)
	stream := newFakeSessions()
	m.openSessions = func(context.Context) (sessionStream, error) { return stream, nil }
	next := m.followSessions()
	_, next = m.Update(next())
	feed = func(nodes ...string) {
		t.Helper()
		stream.events <- incomingFrom(nodes...)
		_, next = m.Update(next())
	}

	socket := filepath.Join(runtest.Dir(t), "wdx.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var requests []string
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch {
		case r.Method != http.MethodDelete:
			http.NotFound(w, r)
		case r.URL.Path == "/v1/hub/nodes/id:node-down":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(api.NewProblem(api.ErrorCodeHubUnreachable, "the hub cannot be reached"))
		case strings.HasPrefix(r.URL.Path, "/v1/hub/nodes/"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(api.HubNodeResult{Result: api.ResultRemoved})
		case strings.HasPrefix(r.URL.Path, "/v1/hub/sessions/"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(api.HubSessionResult{Result: api.ResultClosed})
		default:
			http.NotFound(w, r)
		}
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { srv.Close() })
	m.client = client.New(client.Options{Socket: socket, DebugID: "dbg-0123456789ab"})
	asked = func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(requests)
	}
	return m, feed, asked
}

// TestSessionsOffered: C-q H is on the menu of a local TIDE only while a
// session is open into this node, and opens the Sessions dialog; a remote
// TIDE never offers it, the host's sessions not being its own.
func TestSessionsOffered(t *testing.T) {
	t.Parallel()
	m, feed, _ := sessionsModel(t)
	keys(m, "C-q")
	if strings.Contains(text(m), "H  sessions") {
		t.Fatalf("offered with no session:\n%s", text(m))
	}
	keys(m, "esc")
	feed("laptop")
	keys(m, "C-q")
	wantText(t, m, "H  sessions into this node")
	keys(m, "H")
	wantText(t, m, "Sessions into this node", "▸ laptop", "x close  X close all  D remove node  Esc back")

	remote, _ := remoteModel(style.New(false), noSession)
	remote.incoming.sessions = incomingFrom("laptop")
	if remote.sessionsOffered() {
		t.Fatal("a remote TIDE offers the host's sessions")
	}
}

// TestSessionsFollowStream: the dialog's rows are the stream's, and it closes
// itself when the last session goes.
func TestSessionsFollowStream(t *testing.T) {
	t.Parallel()
	m, feed, _ := sessionsModel(t)
	feed("laptop")
	keys(m, "H") // the left pane takes the menu's letters without C-q
	feed("laptop", "work")
	wantText(t, m, "▸ laptop", "  work  ")
	keys(m, "down")
	feed("work")
	if got := text(m); strings.Contains(got, "laptop") || !strings.Contains(got, "▸ work") {
		t.Fatalf("laptop's session closed, still shown:\n%s", got)
	}
	feed()
	if m.sessionsBox != nil {
		t.Fatalf("the dialog stayed open with no session:\n%s", text(m))
	}
}

// TestSessionsClose: x then y closes the session under the cursor, saying so
// on the status line; n closes nothing.
func TestSessionsClose(t *testing.T) {
	t.Parallel()
	m, feed, asked := sessionsModel(t)
	feed("laptop", "work")
	keys(m, "H", "x")
	wantText(t, m, "close laptop's session? y/n")
	keys(m, "n", "down", "x", "y")
	if got := asked(); !slices.Equal(got, []string{"DELETE /v1/hub/sessions/sess-work"}) {
		t.Fatalf("asked %q, want work's session closed only", got)
	}
	if got := lastLine(m); !strings.Contains(got, "closed the session with work") {
		t.Errorf("status line %q", got)
	}
	if m.sessionsBox == nil {
		t.Error("the dialog closed with a session left")
	}
}

// TestSessionsCloseAll: X then y closes each session into this node, one by
// one, never this node's own into others, which closing all at once would.
func TestSessionsCloseAll(t *testing.T) {
	t.Parallel()
	m, feed, asked := sessionsModel(t)
	feed("laptop", "work")
	keys(m, "H", "X")
	wantText(t, m, "close all 2 sessions? y/n")
	keys(m, "y")
	if got := asked(); !slices.Equal(got, []string{"DELETE /v1/hub/sessions/sess-laptop", "DELETE /v1/hub/sessions/sess-work"}) {
		t.Fatalf("asked %q, want each incoming session closed", got)
	}
	if got := lastLine(m); !strings.Contains(got, "closed 2 sessions") {
		t.Errorf("status line %q", got)
	}
}

// TestSessionsRemoveNode: D then y removes the node under the cursor from the
// account.
func TestSessionsRemoveNode(t *testing.T) {
	t.Parallel()
	m, feed, asked := sessionsModel(t)
	feed("laptop")
	keys(m, "H", "D")
	wantText(t, m, "remove laptop from your account?")
	keys(m, "y")
	if got := asked(); !slices.Equal(got, []string{"DELETE /v1/hub/nodes/id:node-laptop"}) {
		t.Fatalf("asked %q, want laptop removed", got)
	}
	if got := lastLine(m); !strings.Contains(got, "removed laptop from the account") {
		t.Errorf("status line %q", got)
	}
}

// TestSessionsFailure: a refusal stays in the dialog, which stays open.
func TestSessionsFailure(t *testing.T) {
	t.Parallel()
	m, feed, _ := sessionsModel(t)
	feed("down")
	keys(m, "H", "D", "y")
	if m.sessionsBox == nil {
		t.Fatal("the dialog closed on a failure")
	}
	wantText(t, m, "the hub cannot be reached", "▸ down")
}

// TestSessionsBarClick: a click on the bar's remote: opens the dialog; one
// elsewhere on the bar does not.
func TestSessionsBarClick(t *testing.T) {
	t.Parallel()
	m, feed, _ := sessionsModel(t)
	feed("laptop")
	m.Update(clickAt(90, 29))
	if m.sessionsBox != nil {
		t.Fatal("a click on the bar's keys opened the dialog")
	}
	m.Update(clickAt(3, 29))
	if m.sessionsBox == nil {
		t.Fatalf("a click on remote: opened no dialog:\n%s", text(m))
	}
}
