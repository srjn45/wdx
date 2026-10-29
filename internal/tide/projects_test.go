package tide

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
	m.tick = noTick
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	run(m, m.Init())
	return m
}

// noTick drops the strip's timer: the tests send its message themselves.
func noTick(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }

// keys sends m each of names, a key as Bubble Tea names it, and runs what
// follows from each.
func keys(m *Model, names ...string) {
	byName := map[string]tea.KeyPressMsg{
		"up": key(tea.KeyUp, 0), "down": key(tea.KeyDown, 0), "enter": key(tea.KeyEnter, 0), "esc": key(tea.KeyEscape, 0),
		"C-q": key('q', tea.ModCtrl), `C-\`: key('\\', tea.ModCtrl), "alt+right": key(tea.KeyRight, tea.ModAlt),
		"C-]": key(']', tea.ModCtrl), "backspace": key(tea.KeyBackspace, 0),
		"left": key(tea.KeyLeft, 0), "right": key(tea.KeyRight, 0), "home": key(tea.KeyHome, 0), "end": key(tea.KeyEnd, 0),
		"pgup": key(tea.KeyPgUp, 0), "pgdown": key(tea.KeyPgDown, 0),
		"alt+shift+left": key(tea.KeyLeft, tea.ModAlt|tea.ModShift), "alt+shift+right": key(tea.KeyRight, tea.ModAlt|tea.ModShift),
	}
	for _, name := range names {
		if k, ok := byName[name]; ok {
			_, cmd := m.Update(k)
			run(m, cmd)
			continue
		}
		for _, r := range name { // typed
			_, cmd := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
			run(m, cmd)
		}
	}
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
	if m.switcher != nil || m.layout.focus != project {
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
	if m.switcher == nil {
		t.Fatal("the switcher did not open")
	}
	// Every project, the open ones first, the first selected.
	wantText(t, m, "┃ Switch project", "▸ beta", "  gamma", "  alpha")
	var order []string
	for _, p := range m.switcher.matches() {
		order = append(order, p.Name)
	}
	if !slices.Equal(order, []string{"beta", "gamma", "alpha"}) {
		t.Fatalf("listed %v", order)
	}
	keys(m, "esc")
	if m.switcher != nil || m.shown != "" {
		t.Fatalf("Esc: switcher %v, shown %q", m.switcher, m.shown)
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

func TestSwitcher(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	h.newProject(t, "beta", api.ProjectStateOpen)
	h.newProject(t, "gamma", api.ProjectStateOpen)
	h.newProject(t, "alpha", api.ProjectStateClosed)
	m := h.start(t)

	keys(m, "zzz")
	wantText(t, m, "> zzz_", "no project matches")
	keys(m, "enter", "backspace", "backspace", "backspace", "gm")
	wantText(t, m, "> gm_", "▸ gamma")
	if strings.Contains(text(m), "beta") {
		t.Fatalf("beta matches gm:\n%s", text(m))
	}
	keys(m, "enter")
	if m.shown != "gamma" || m.switcher != nil {
		t.Fatalf("shown %q, switcher %v", m.shown, m.switcher)
	}

	keys(m, "C-q", "p", "down", "up", "enter") // the first: beta
	keys(m, "C-q", "p")
	wantText(t, m, "▸ gamma") // the one shown before
	keys(m, "enter")
	if m.shown != "gamma" || m.previous != "beta" {
		t.Fatalf("C-q p enter: shown %q, previous %q", m.shown, m.previous)
	}

	// A closed project reopens.
	keys(m, "C-q", "p", "down", "down", "enter")
	if m.shown != "alpha" {
		t.Fatalf("shown %q", m.shown)
	}
	list, err := h.client().ListProjects(context.Background(), api.ProjectStateOpen)
	if err != nil || len(list.Projects) != 3 {
		t.Fatalf("open projects: %+v, %v", list.Projects, err)
	}
	keys(m, "C-q", "p", "esc")
	if m.switcher != nil || m.shown != "alpha" {
		t.Fatalf("Esc: switcher %v, shown %q", m.switcher, m.shown)
	}
}

func TestSwitcherReopenFails(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	h.newProject(t, "beta", api.ProjectStateOpen)
	dir := h.newProject(t, "alpha", api.ProjectStateClosed)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	m := h.start(t)
	keys(m, "alp", "enter")
	if m.switcher == nil || m.shown != "" {
		t.Fatalf("switcher %v, shown %q", m.switcher, m.shown)
	}
	wantText(t, m, "▸ alpha", "no checkout")
}

func TestStrip(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	for _, name := range []string{"b", "c", "a"} {
		h.newProject(t, name, api.ProjectStateOpen)
	}
	h.newProject(t, "d", api.ProjectStateClosed)
	m := h.start(t)
	keys(m, "esc", "alt+shift+right") // from no project: the first
	if m.shown != "a" || !m.stripShown {
		t.Fatalf("shown %q, strip %v", m.shown, m.stripShown)
	}
	for _, step := range []struct{ key, want string }{
		{"alt+shift+right", "b"}, {"alt+shift+left", "a"}, {"alt+shift+left", "c"}, {"alt+shift+right", "a"},
	} {
		keys(m, step.key)
		if m.shown != step.want {
			t.Fatalf("%s: shown %q, want %q", step.key, m.shown, step.want)
		}
	}
	wantText(t, m, "▸ a ◂   b     c")
	if strings.Contains(text(m), " d ") {
		t.Fatalf("a closed project on the strip:\n%s", text(m))
	}

	// It hides a while after the last switch, not an earlier one's while.
	m.Update(hideStrip{shown: m.strip - 1})
	if !m.stripShown {
		t.Fatal("an earlier switch's tick hid the strip")
	}
	m.Update(hideStrip{shown: m.strip})
	if m.stripShown {
		t.Fatal("the strip stayed")
	}
	// And at once on any other key.
	keys(m, "alt+shift+right", "x")
	if m.stripShown || m.shown != "b" {
		t.Fatalf("strip %v, shown %q", m.stripShown, m.shown)
	}
}

func TestStripScrolls(t *testing.T) {
	t.Parallel()
	m, _ := newModel(30, 24)
	m.projects.projects = nil
	for _, name := range []string{"one", "two", "three", "four", "five", "six"} {
		m.projects.projects = append(m.projects.projects, api.ProjectSummary{Name: name, State: api.ProjectStateOpen})
	}
	m.show("six")
	m.stripShown = true
	strip := strings.Split(text(m), "\n")[22]
	if !strings.Contains(strip, "▸ six ◂") || strings.Contains(strip, "one") {
		t.Fatalf("strip %q", strip)
	}
}
