package tide

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/daemon"
	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/style"
)

func TestMain(m *testing.M) {
	gittest.Isolate()
	m.Run()
}

// testHome is a home with the socket a daemon serving it listens on.
type testHome struct {
	home, socket string
}

// newTestHome returns a home no daemon serves yet.
func newTestHome(t *testing.T) testHome {
	t.Helper()
	// A socket under t.TempDir() can be longer than macOS allows.
	dir, err := os.MkdirTemp("/tmp", "tide")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return testHome{home: t.TempDir(), socket: filepath.Join(dir, "wdx.sock")}
}

// serve runs a daemon on h until the test ends.
func (h testHome) serve(t *testing.T) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	d, err := daemon.Listen(ctx, daemon.Options{Home: h.home, Socket: h.socket, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- d.Serve(ctx) }()
	t.Cleanup(func() {
		stop()
		if err := <-served; err != nil {
			t.Errorf("daemon: %v", err)
		}
	})
}

func (h testHome) client() *client.Client {
	return client.New(client.Options{Socket: h.socket, DebugID: logging.NewDebugID()})
}

// newProject creates a project named name in h, in state.
func (h testHome) newProject(t *testing.T, name string, state api.ProjectState) {
	t.Helper()
	ctx, c := context.Background(), h.client()
	var source api.Source
	dir := t.TempDir()
	_ = source.FromNewSource(api.NewSource{Type: api.NewSourceTypeNew, Path: &dir})
	if _, err := c.CreateProject(ctx, api.CreateProject{Source: source, Name: &name}); err != nil {
		t.Fatal(err)
	}
	if state == api.ProjectStateClosed {
		if _, err := c.UpdateProject(ctx, name, state, false); err != nil {
			t.Fatal(err)
		}
	}
}

// start returns tide's window on h at 80 × 24, started: the projects
// listed.
func (h testHome) start(t *testing.T) *Model {
	t.Helper()
	m := New(context.Background(), Options{Client: h.client(), Style: style.New(false)})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	run(m, m.Init())
	return m
}

// run runs cmd and what follows from it, as Bubble Tea would.
func run(m *Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			run(m, c)
		}
	default:
		_, next := m.Update(msg)
		run(m, next)
	}
}

func TestStartWithoutProjects(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	m := h.start(t)
	if m.switcher {
		t.Fatal("the switcher opened with no project")
	}
	if got := text(m); !strings.Contains(got, "no projects yet") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestStartWithOpenProjects(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	h.newProject(t, "beta", api.ProjectStateOpen)
	h.newProject(t, "alpha", api.ProjectStateClosed)
	h.newProject(t, "gamma", api.ProjectStateOpen)
	m := h.start(t)
	if !m.switcher {
		t.Fatal("the switcher did not open")
	}
	// The box lists the open projects only; the pane behind it lists all.
	if got := text(m); !strings.Contains(got, "│ beta ") || !strings.Contains(got, "│ gamma ") || strings.Contains(got, "│ alpha ") {
		t.Fatalf("the switcher shows:\n%s", got)
	}
	m.Update(key(tea.KeyEscape, 0))
	got := text(m)
	if m.switcher || !strings.Contains(got, "open: beta, gamma") || !strings.Contains(got, "closed: alpha") {
		t.Fatalf("after Esc:\n%s", got)
	}
}

func TestDaemonUnavailable(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	m := h.start(t)
	got := text(m)
	if !strings.Contains(got, "the daemon is not running") || !strings.Contains(got, "wdx daemon start") {
		t.Fatalf("with no daemon:\n%s", got)
	}

	h.serve(t)
	m.Update(key(tea.KeyRight, tea.ModAlt))
	if _, cmd := m.Update(key('r', 0)); cmd != nil {
		t.Fatal("r retried outside the project pane")
	}
	m.Update(key(tea.KeyLeft, tea.ModAlt))
	_, cmd := m.Update(key('r', 0))
	if !strings.Contains(text(m), "loading…") {
		t.Fatalf("while retrying:\n%s", text(m))
	}
	run(m, cmd)
	if got := text(m); !strings.Contains(got, "no projects yet") {
		t.Fatalf("after r:\n%s", got)
	}
}
