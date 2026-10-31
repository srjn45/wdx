package tide

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/aca/acatest"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/daemon"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/holder/holdertest"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/logtest"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/style"
)

func TestMain(m *testing.M) {
	holdertest.RunIfHolder()
	os.Exit(runtest.Main(m))
}

// testShell is the shell of the test terminals, interactive with the
// prompt "$ ": it stays until it is hung up on or told to exit.
const testShell = "#!/bin/sh\nPS1='$ ' exec /bin/sh\n"

// testHome is a home with the socket a daemon serving it listens on.
type testHome struct {
	home, socket string
}

// removeAgents removes agents and all under them, children first: a list holds
// the top level only, and a parent with children would stay as a tombstone.
func removeAgents(ctx context.Context, c *client.Client, agents []api.Agent) {
	for _, a := range agents {
		if a.Children != nil {
			removeAgents(ctx, c, *a.Children)
		}
		_, _ = c.DeleteAgent(ctx, "id:"+a.Id, "", "", true)
	}
}

// newTestHome returns a home no daemon serves yet.
func newTestHome(t *testing.T) testHome {
	t.Helper()
	// A socket under t.TempDir() can be longer than macOS allows.
	dir := runtest.Dir(t)
	home := t.TempDir()
	logtest.OnFailure(t, home)
	return testHome{home: home, socket: filepath.Join(dir, "wdx.sock")}
}

// logger writes to h's log files, as wdx's processes do, labelled process.
func (h testHome) logger(t *testing.T, process string) *slog.Logger {
	t.Helper()
	logs := logging.NewDailyFile(identity.LogsDir(h.home), 7, time.Now)
	t.Cleanup(func() { logs.Close() })
	return logging.New(logs, slog.LevelDebug).With("process", process)
}

// serve runs a daemon on h until the test ends or the function it
// returns stops it. Its terminals run testShell, and are removed when it
// stops.
func (h testHome) serve(t *testing.T) (stop func()) {
	t.Helper()
	shell := filepath.Join(t.TempDir(), "shell")
	if err := runtest.WriteExecutable(shell, testShell); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	d, err := daemon.Listen(ctx, daemon.Options{
		Home: h.home, Socket: h.socket, Log: h.logger(t, "daemon"),
		Executable: exe, TerminalShell: shell, ShellEnv: []string{"PATH=/usr/bin:/bin"}, TerminalHistoryLines: 1000,
		ACA:        &acatest.ACA{},
		StopReport: holdertest.Patience(t), HolderLimits: holdertest.Limits(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- d.Serve(ctx) }()
	stop = sync.OnceFunc(func() {
		c, bg := h.client(), context.Background()
		if list, err := c.ListTerminals(bg, "", ""); err == nil {
			for _, term := range list.Terminals {
				_, _ = c.DeleteTerminal(bg, "id:"+term.Id, "", "", true)
			}
		}
		if list, err := c.ListAgents(bg, "", ""); err == nil {
			removeAgents(bg, c, list.Agents)
		}
		cancel()
		if err := <-served; err != nil {
			t.Errorf("daemon: %v", err)
		}
	})
	t.Cleanup(stop)
	return stop
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
	m := New(logging.NewContext(holder.WithLimits(context.Background(), holdertest.Limits(t)), h.logger(t, "tide")), Options{Client: h.client(), Style: style.New(false)})
	m.tick = noTick
	// run waits for each command: one waiting for the stream's next event
	// would never end. follow_test.go follows the stream.
	m.openStream = nil
	// Wide enough for the project pane's lines: 31 cells inside its borders.
	m.Update(tea.WindowSizeMsg{Width: 128, Height: 24})
	run(m, m.Init())
	return m
}

// noTick drops the strip's timer: the tests send its message themselves.
func noTick(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }

// keys sends m each of names, a key as Bubble Tea names it, and runs what
// follows from each.
func keys(m *Model, names ...string) {
	for _, k := range keyMsgs(names...) {
		_, cmd := m.Update(k)
		run(m, cmd)
	}
}

// keyMsgs returns the keys names name; any other name types its runes.
func keyMsgs(names ...string) []tea.KeyPressMsg {
	byName := map[string]tea.KeyPressMsg{
		"up": key(tea.KeyUp, 0), "down": key(tea.KeyDown, 0), "enter": key(tea.KeyEnter, 0), "esc": key(tea.KeyEscape, 0),
		"C-q": key('q', tea.ModCtrl), `C-\`: key('\\', tea.ModCtrl), "alt+right": key(tea.KeyRight, tea.ModAlt),
		"alt+down": key(tea.KeyDown, tea.ModAlt), "alt+up": key(tea.KeyUp, tea.ModAlt), "alt+left": key(tea.KeyLeft, tea.ModAlt),
		"C-]": key(']', tea.ModCtrl), "backspace": key(tea.KeyBackspace, 0),
		"left": key(tea.KeyLeft, 0), "right": key(tea.KeyRight, 0), "home": key(tea.KeyHome, 0), "end": key(tea.KeyEnd, 0),
		"pgup": key(tea.KeyPgUp, 0), "pgdown": key(tea.KeyPgDown, 0),
		"alt+shift+left": key(tea.KeyLeft, tea.ModAlt|tea.ModShift), "alt+shift+right": key(tea.KeyRight, tea.ModAlt|tea.ModShift),
	}
	var msgs []tea.KeyPressMsg
	for _, name := range names {
		if k, ok := byName[name]; ok {
			msgs = append(msgs, k)
			continue
		}
		for _, r := range name { // typed
			msgs = append(msgs, tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}
	return msgs
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
	wantText(t, m, "┃ Open Project", "▸ Open a local repository", "Create a new project")
	if strings.Contains(text(m), "closed projects") || strings.Contains(text(m), "> _") {
		t.Fatalf("closed projects listed with none:\n%s", text(m))
	}
	keys(m, "down", "enter")
	wantText(t, m, "┃ Open Project › Clone", "URL of the git repository")
	keys(m, "esc")
	wantText(t, m, "┃ Open Project", "▸ Clone a git repository")

	// Esc closes it, and the project pane says how to get back.
	keys(m, "esc")
	if len(m.opening) != 0 {
		t.Fatalf("Esc left it open:\n%s", text(m))
	}
	wantText(t, m, "[No Project]", "no project open", "C-q p switch project", "C-q o open project")
	keys(m, `C-\`)
	if m.layout.shown(terminal) {
		t.Fatal("C-\\ showed terminals with no project")
	}
	keys(m, "C-q", "p")
	wantText(t, m, "┃ Switch Project", "▸ Open/Create Project")
	keys(m, "enter")
	if m.switcher != nil {
		t.Fatalf("the switcher stayed:\n%s", text(m))
	}
	wantText(t, m, "┃ Open Project", "▸ Open a local repository")
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
	// The open projects only, the first selected.
	wantText(t, m, "┃ Switch Project", "  Open/Create Project", "▸ beta", "  gamma")
	if strings.Contains(text(m), "alpha") {
		t.Fatalf("a closed project in the switcher:\n%s", text(m))
	}
	keys(m, "esc")
	if m.switcher != nil || m.shown != "" {
		t.Fatalf("Esc: switcher %v, shown %q", m.switcher, m.shown)
	}
	wantText(t, m, "[No Project]")
	keys(m, "C-q", "o")
	wantText(t, m, "┃ Open Project", "▸ Open a local repository", "closed projects", "  alpha")
}

func TestReopen(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	h.newProject(t, "alpha", api.ProjectStateClosed)
	h.newProject(t, "beta", api.ProjectStateClosed)
	m := h.start(t)
	keys(m, "down", "down", "down", "down", "down", "down") // past beta, the last: it stays
	wantText(t, m, "▸ beta")
	keys(m, "up", "enter")
	if m.shown != "alpha" || !m.layout.shown(terminal) {
		t.Fatalf("shown %q, terminal shown %v", m.shown, m.layout.shown(terminal))
	}
	wantText(t, m, "[Project: alpha]", " alpha · project")
	list, err := h.client().ListProjects(context.Background(), api.ProjectStateOpen)
	if err != nil || len(list.Projects) != 1 || list.Projects[0].Name != "alpha" {
		t.Fatalf("open projects: %+v, %v", list.Projects, err)
	}

	// C-q o opens the popup from any pane, whose focus and zoom it leaves.
	keys(m, "alt+right", "C-]", "C-q", "o")
	if m.layout.focus != agent || m.layout.zoomed != agent {
		t.Fatalf("C-q o: focus %s, zoomed %s", m.layout.focus, m.layout.zoomed)
	}
	wantText(t, m, "┃ Open Project", "closed projects", "  beta")
	if strings.Contains(text(m), "  alpha") {
		t.Fatalf("alpha still listed closed:\n%s", text(m))
	}
	keys(m, "C-q", "o") // once is enough
	keys(m, "esc")
	if len(m.opening) != 0 || m.layout.focus != agent || m.layout.zoomed != agent {
		t.Fatalf("Esc: popup %d screens, focus %s, zoomed %s", len(m.opening), m.layout.focus, m.layout.zoomed)
	}
	keys(m, "C-]")
	wantText(t, m, "[Project: alpha]")
}

func TestOpenProjectFilter(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	for _, name := range []string{"alpha", "bravo", "jack"} {
		h.newProject(t, name, api.ProjectStateClosed)
	}
	m := h.start(t)
	wantText(t, m, "> _", "▸ Open a local repository", "  alpha", "  bravo", "  jack")

	// Letters type, j and k too; the sources stay.
	keys(m, "jk")
	wantText(t, m, "> jk_", "  Open a local repository", "  Clone a git repository", "  Create a new project", "▸ jack")
	if got := text(m); strings.Contains(got, "alpha") || strings.Contains(got, "bravo") {
		t.Fatalf("listed what does not match:\n%s", got)
	}
	keys(m, "x")
	wantText(t, m, "> jkx_", "▸ Open a local repository", "no closed project matches")
	keys(m, "backspace", "backspace", "backspace")
	wantText(t, m, "> _", "▸ Open a local repository", "  alpha")

	keys(m, "brv", "enter")
	if m.shown != "bravo" {
		t.Fatalf("shown %q:\n%s", m.shown, text(m))
	}
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
	// No origin to clone it again from: it can only be removed.
	wantText(t, m, "┃ Open Project › alpha", "no checkout", "▸ remove it from wdx", "  cancel")
	if strings.Contains(text(m), "clone it again") {
		t.Fatalf("offered to clone with no origin:\n%s", text(m))
	}
	keys(m, "down", "enter") // cancel
	wantText(t, m, "┃ Open Project", "▸ alpha", "no checkout")
	keys(m, "enter", "enter")
	wantText(t, m, "┃ Open Project", "removed alpha")
	if strings.Contains(text(m), "closed projects") {
		t.Fatalf("alpha still listed:\n%s", text(m))
	}
	if _, err := h.client().GetProject(context.Background(), "alpha"); err == nil {
		t.Fatal("alpha was not removed")
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
	wantText(t, m, "┃ Open Project")
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
	wantText(t, m, "> zzz_", "▸ Open/Create Project", "no project matches")
	keys(m, "backspace", "backspace", "backspace", "gm")
	wantText(t, m, "> gm_", "  Open/Create Project", "▸ gamma")
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

	// Above the first project is the way to [Open Project], where the closed
	// ones are; Esc there closes it, rather than going back to the switcher.
	keys(m, "C-q", "p", "up", "up")
	wantText(t, m, "▸ Open/Create Project")
	keys(m, "down")
	wantText(t, m, "▸ beta")
	keys(m, "up", "enter")
	if m.switcher != nil || len(m.opening) != 1 {
		t.Fatalf("switcher %v, popup %d screens", m.switcher, len(m.opening))
	}
	wantText(t, m, "┃ Open Project", "  alpha")
	keys(m, "esc")
	if m.switcher != nil || len(m.opening) != 0 || m.shown != "gamma" {
		t.Fatalf("Esc: switcher %v, popup %d screens, shown %q", m.switcher, len(m.opening), m.shown)
	}

	// The two popups take each other's place.
	keys(m, "C-q", "p", "C-q", "o")
	if m.switcher != nil || len(m.opening) != 1 {
		t.Fatalf("C-q o: switcher %v, popup %d screens", m.switcher, len(m.opening))
	}
	keys(m, "C-q", "p")
	if m.switcher == nil || len(m.opening) != 0 {
		t.Fatalf("C-q p: switcher %v, popup %d screens", m.switcher, len(m.opening))
	}
	keys(m, "esc")
	if m.switcher != nil || m.shown != "gamma" {
		t.Fatalf("Esc: switcher %v, shown %q", m.switcher, m.shown)
	}
}

// TestPopupQuestionKeepsFocus: a question goes on in the popup, and the pane
// that has the focus keeps it.
func TestPopupQuestionKeepsFocus(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	h.newProject(t, "beta", api.ProjectStateOpen)
	dir := h.newProject(t, "alpha", api.ProjectStateClosed)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	m := h.start(t)
	keys(m, "esc", "alt+right", "C-q", "o", "alp", "enter")
	if m.shown != "" || m.layout.focus != agent {
		t.Fatalf("shown %q, focus %s", m.shown, m.layout.focus)
	}
	wantText(t, m, "┃ Open Project › alpha", "no checkout", "▸ remove it from wdx")
	keys(m, "esc")
	wantText(t, m, "┃ Open Project")
}

// TestAnswerAfterPopupClosed: Esc does not wait for the daemon, so the answer
// can find the popup closed: a question opens it again, and a failure with
// nothing to ask goes to the status line.
func TestAnswerAfterPopupClosed(t *testing.T) {
	t.Parallel()
	m, _ := newModel(80, 24)
	m.projects.loaded = true
	req := localRequest("/work/demo")

	m.Update(projectOpened{name: "x", req: &req, err: errors.New("cannot open x")})
	wantText(t, m, "cannot open x")
	if len(m.opening) != 0 {
		t.Fatalf("popup %d screens", len(m.opening))
	}

	problem := &client.ProblemError{Problem: api.Problem{Code: api.ErrorCodeNameTaken, Title: "taken"}}
	m.Update(projectOpened{req: &req, err: problem})
	if _, ok := m.openTop().(*fieldScreen); !ok || len(m.opening) != 2 {
		t.Fatalf("no name asked:\n%s", text(m))
	}
	wantText(t, m, "┃ Open Project", "Another name")
}

func TestPopupTakesTheMouse(t *testing.T) {
	t.Parallel()
	m, _ := newModel(100, 41)
	m.Update(key('q', tea.ModCtrl))
	m.Update(key('o', 0)) // its command, listing the projects again, never runs
	m.Update(clickAt(90, 2))
	if m.layout.focus != project {
		t.Fatalf("a click under the popup focused %s", m.layout.focus)
	}
}

// TestPopupFrame: both frames and the margin, equidistant, over the panes.
func TestPopupFrame(t *testing.T) {
	t.Parallel()
	m, _ := newModel(60, 24)
	m.Update(key('q', tea.ModCtrl))
	want := []string{
		"                               ",
		"  ┏━━━━━━━━━━━━━━━━━━━━━━━━━┓  ",
		"  ┃                         ┃  ",
		"  ┃  ┏━━━━━━━━━━━━━━━━━━━┓  ┃  ",
		"  ┃  ┃ Menu              ┃  ┃  ",
		"  ┃  ┣━━━━━━━━━━━━━━━━━━━┫  ┃  ",
		"  ┃  ┃ o  open project   ┃  ┃  ",
		"  ┃  ┃ p  switch project ┃  ┃  ",
		"  ┃  ┃ q  quit           ┃  ┃  ",
		"  ┃  ┣━━━━━━━━━━━━━━━━━━━┫  ┃  ",
		"  ┃  ┃ Esc close         ┃  ┃  ",
		"  ┃  ┗━━━━━━━━━━━━━━━━━━━┛  ┃  ",
		"  ┃                         ┃  ",
		"  ┗━━━━━━━━━━━━━━━━━━━━━━━━━┛  ",
		"                               ",
	}
	got := strings.Split(ansi.Strip(m.render()), "\n")
	const x, y = 14, 4 // the middle of the 60 × 23 above the status line
	for i, w := range want {
		if row := string([]rune(got[y+i])[x : x+utf8.RuneCountInString(w)]); row != w {
			t.Fatalf("row %d: got %q, want %q:\n%s", i, row, w, text(m))
		}
	}
}

// TestPopupFrameSmallWindow: the margin goes first, then the outer frame.
func TestPopupFrameSmallWindow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		width, height int
		outer, margin bool
	}{
		{80, 24, true, true},
		{80, 15, true, false},
		{80, 13, false, false},
		{24, 24, false, false},
	} {
		m, _ := newModel(tc.width, tc.height)
		m.Update(key('q', tea.ModCtrl))
		rows := strings.Split(ansi.Strip(m.render()), "\n")
		got := strings.Join(rows, "\n")
		if !strings.Contains(got, "┃ Menu") || !strings.Contains(got, "quit") {
			t.Errorf("%d×%d: the popup lost its content:\n%s", tc.width, tc.height, got)
		}
		if strings.Contains(got, "┃  ┏━") != tc.outer {
			t.Errorf("%d×%d: outer frame, want %v:\n%s", tc.width, tc.height, tc.outer, got)
		}
		// The margin is the row above the outer frame, blank over the panes.
		margin := false
		for y, row := range rows {
			if x := strings.Index(row, "┏"); x >= 0 && tc.outer {
				margin = y > 0 && []rune(rows[y-1])[utf8.RuneCountInString(row[:x])] == ' '
				break
			}
		}
		if margin != tc.margin {
			t.Errorf("%d×%d: margin %v, want %v:\n%s", tc.width, tc.height, margin, tc.margin, got)
		}
	}
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
