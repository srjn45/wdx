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

// newProject creates a project named name in h, in state, and returns the
// directory its checkout is in.
func (h testHome) newProject(t *testing.T, name string, state api.ProjectState) string {
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
	return dir
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

// keys sends m each of names, a key as Bubble Tea names it, and runs what
// follows from each.
func keys(m *Model, names ...string) {
	byName := map[string]tea.KeyPressMsg{
		"up": key(tea.KeyUp, 0), "down": key(tea.KeyDown, 0), "enter": key(tea.KeyEnter, 0), "esc": key(tea.KeyEscape, 0),
		"C-q": key('q', tea.ModCtrl), `C-\`: key('\\', tea.ModCtrl), "alt+right": key(tea.KeyRight, tea.ModAlt),
		"C-]": key(']', tea.ModCtrl),
	}
	for _, name := range names {
		k, ok := byName[name]
		if !ok {
			k = key([]rune(name)[0], 0)
		}
		_, cmd := m.Update(k)
		run(m, cmd)
	}
}

// wantText fails the test unless m shows each of want.
func wantText(t *testing.T, m *Model, want ...string) {
	t.Helper()
	got := text(m)
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Fatalf("no %q on:\n%s", w, got)
		}
	}
}

func TestStartWithoutProjects(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	m := h.start(t)
	if m.switcher || m.layout.focus != project {
		t.Fatalf("switcher %v, focus %s", m.switcher, m.layout.focus)
	}
	wantText(t, m, "[Open Project]", "▸ Open a local repository", "Create a new project", "no project open")
	if strings.Contains(text(m), "closed projects") {
		t.Fatalf("closed projects listed with none:\n%s", text(m))
	}
	keys(m, `C-\`)
	if m.layout.shown(terminal) {
		t.Fatal("C-\\ showed terminals with no project")
	}
	keys(m, "esc", "down", "enter")
	wantText(t, m, "[Open Project]", "▸ Clone a git repository", "not built yet")
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
	// The box lists the open projects only.
	wantText(t, m, "switch project", "│ beta ", "│ gamma ")
	keys(m, "down", "enter") // the switcher's, not [Open Project]'s below it
	if !m.switcher {
		t.Fatal("enter closed the switcher")
	}
	keys(m, "esc")
	if m.switcher {
		t.Fatal("Esc left the switcher open")
	}
	wantText(t, m, "[Open Project]", "▸ Open a local repository", "closed projects", "  alpha")
}

func TestReopen(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	h.newProject(t, "alpha", api.ProjectStateClosed)
	h.newProject(t, "beta", api.ProjectStateClosed)
	m := h.start(t)
	keys(m, "down", "j", "j", "down", "down") // past beta, the last: it stays
	wantText(t, m, "▸ beta")
	keys(m, "k", "enter")
	if m.shown != "alpha" || !m.layout.shown(terminal) {
		t.Fatalf("shown %q, terminal shown %v", m.shown, m.layout.shown(terminal))
	}
	wantText(t, m, "[Project: alpha]", " alpha · project")
	list, err := h.client().ListProjects(context.Background(), api.ProjectStateOpen)
	if err != nil || len(list.Projects) != 1 || list.Projects[0].Name != "alpha" {
		t.Fatalf("open projects: %+v, %v", list.Projects, err)
	}

	// C-q o opens [Open Project] over it, from any pane; Esc goes back.
	keys(m, "alt+right", "C-]", "C-q", "o")
	if m.layout.focus != project || m.layout.zoomed != project {
		t.Fatalf("C-q o: focus %s, zoomed %s", m.layout.focus, m.layout.zoomed)
	}
	wantText(t, m, "[Open Project]", "closed projects", "  beta")
	if strings.Contains(text(m), "  alpha") {
		t.Fatalf("alpha still listed closed:\n%s", text(m))
	}
	keys(m, "C-q", "o") // once is enough
	keys(m, "esc")
	wantText(t, m, "[Project: alpha]")
	keys(m, "esc") // the root stays
	wantText(t, m, "[Project: alpha]")
}

func TestReopenFails(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "alpha", api.ProjectStateClosed)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	m := h.start(t)
	keys(m, "down", "down", "down", "enter")
	if m.shown != "" {
		t.Fatalf("shown %q", m.shown)
	}
	got := text(m)
	if !strings.Contains(got, "[Open Project]") || !strings.Contains(got, "▸ alpha") || !strings.Contains(got, "no checkout") {
		t.Fatalf("after a failed reopen:\n%s", got)
	}
}

func TestDaemonUnavailable(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	m := h.start(t)
	wantText(t, m, "the daemon is not running", "wdx daemon start", " project ")

	h.serve(t)
	m.Update(key(tea.KeyRight, tea.ModAlt))
	if _, cmd := m.Update(key('r', 0)); cmd != nil {
		t.Fatal("r retried outside the project pane")
	}
	m.Update(key(tea.KeyLeft, tea.ModAlt))
	_, cmd := m.Update(key('r', 0))
	wantText(t, m, "loading…")
	run(m, cmd)
	wantText(t, m, "[Open Project]")
}
