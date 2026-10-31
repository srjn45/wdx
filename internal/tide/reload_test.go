package tide

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/style"
)

// statusRow is the status line as text.
func statusRow(m *Model) string {
	lines := strings.Split(text(m), "\n")
	return lines[len(lines)-1]
}

func TestStatusLineVersion(t *testing.T) {
	t.Parallel()
	running := "[wdx " + identity.Version + "]"
	differing := "[wdx " + identity.Version + " · daemon 9.9.9]"
	m, _ := newModel(120, 24)
	if row := statusRow(m); !strings.Contains(row, running) || !strings.Contains(row, "C-q menu") {
		t.Fatalf("before the daemon answers: %q", row)
	}
	m.Update(daemonVersionMsg{identity.Version})
	if row := statusRow(m); !strings.Contains(row, running) || strings.Contains(row, "daemon") {
		t.Fatalf("the same version: %q", row)
	}

	// Versions that differ take the place of the keys, even when narrow.
	m.Update(daemonVersionMsg{"9.9.9"})
	if row := statusRow(m); !strings.Contains(row, differing) || strings.Contains(row, "C-q menu") {
		t.Fatalf("another version: %q", row)
	}
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	if row := statusRow(m); !strings.Contains(row, differing) {
		t.Fatalf("another version, narrow: %q", row)
	}

	// But not of what the bar is saying now.
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m.statusNotice = "a notice"
	if row := statusRow(m); !strings.Contains(row, "a notice") {
		t.Fatalf("a notice: %q", row)
	}
	m.statusNotice = ""
	m.Update(key('q', tea.ModCtrl))
	if row := statusRow(m); !strings.Contains(row, "menu:") {
		t.Fatalf("the menu: %q", row)
	}
	m.Update(key(tea.KeyEscape, 0))

	// A daemon that stops answering leaves only the version running here.
	m.Update(daemonVersionMsg{""})
	if row := statusRow(m); !strings.Contains(row, running) || strings.Contains(row, "daemon") {
		t.Fatalf("no answer: %q", row)
	}
}

// The running version goes before the keys do: at 80 columns the bar is full.
func TestStatusLineVersionGoesFirst(t *testing.T) {
	t.Parallel()
	m, _ := newModel(40, 24)
	if row := statusRow(m); strings.Contains(row, "[wdx") {
		t.Fatalf("no room for it: %q", row)
	}
}

func TestDaemonVersionAskedAgain(t *testing.T) {
	t.Parallel()
	m, _ := newModel(120, 24)
	answers := []string{"1.0.0", "2.0.0"}
	m.versionOf = func(context.Context) (string, error) {
		if len(answers) == 0 {
			return "", errors.New("no answer")
		}
		v := answers[0]
		answers = answers[1:]
		return v, nil
	}
	var waited []time.Duration
	var fired func(time.Time) tea.Msg
	m.tick = func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
		waited, fired = append(waited, d), fn
		return nil
	}
	for _, want := range []string{"1.0.0", "2.0.0", ""} {
		m.Update(m.checkDaemonVersion()())
		if m.DaemonVersion() != want {
			t.Fatalf("daemon version %q, want %q", m.DaemonVersion(), want)
		}
		if _, ok := fired(time.Time{}).(checkDaemonVersion); !ok {
			t.Fatal("the timer asks nothing")
		}
	}
	if len(waited) != 3 || waited[0] != VersionCheckEvery {
		t.Fatalf("waited %v", waited)
	}
}

func executableFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wdx")
	if err := runtest.WriteExecutable(path, ""); err != nil {
		t.Fatal(err)
	}
	return path
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestReload(t *testing.T) {
	t.Parallel()
	m, _ := newModel(120, 24)
	agents := []api.Agent{{Id: "agent-a", Name: "a", State: api.AgentStateRunning}, {Id: "agent-b", Name: "b", State: api.AgentStateRunning}}
	terms := []api.Terminal{{Id: "term-x", Name: "x", State: api.TerminalStateRunning, Dir: "/work/demo"}, {Id: "term-y", Name: "y", State: api.TerminalStateRunning, Dir: "/work/demo"}}
	m.Update(projectEvent{gen: m.following.gen, event: client.ProjectEvent{Project: api.Project{Id: "proj-demo", Name: "demo", Terminals: &terms, Agents: &agents}}})
	m.list().active = "agent-b"
	m.selectTab(1)
	m.layout.setFocus(terminal)
	m.layout.toggleZoom()

	// Not offered without a binary to run.
	m.executable = ""
	m.Update(key('q', tea.ModCtrl))
	_, cmd := m.Update(key('r', 0))
	if m.Reload() != nil || isQuit(cmd) {
		t.Fatal("reloaded without a binary")
	}

	// A binary that cannot run keeps tide open, and says so.
	m.executable = filepath.Join(t.TempDir(), "gone")
	m.Update(key('q', tea.ModCtrl))
	if !strings.Contains(statusRow(m), "r reload") {
		t.Fatalf("not in the menu's keys: %q", statusRow(m))
	}
	_, cmd = m.Update(key('r', 0))
	if m.Reload() != nil || isQuit(cmd) || !strings.Contains(statusRow(m), "cannot reload") {
		t.Fatalf("with no binary: %q", statusRow(m))
	}

	m.executable = executableFile(t)
	m.Update(key('q', tea.ModCtrl))
	m.update(key('r', 0))
	r := m.Reload()
	want := Resume{Project: "demo", ProjectID: "proj-demo", Focus: "terminal", Zoomed: "terminal", LastTop: "project", TerminalOpen: true, Terminal: "term-y", Agent: "agent-b"}
	if r == nil || *r != want {
		t.Fatalf("resume %+v, want %+v", r, want)
	}
	if m.following != nil {
		t.Fatal("still following the project")
	}

	// The next run, which gets it through the flag, shows the same.
	state, err := r.String()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseResume(state)
	if err != nil {
		t.Fatal(err)
	}
	next := New(context.Background(), Options{Style: style.New(false), Resume: parsed})
	next.tick = noTick
	next.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	next.Update(projectsListed{projects: []api.ProjectSummary{{Name: "demo", State: api.ProjectStateOpen}}})
	if next.shown != "demo" || next.switcher != nil || len(next.opening) != 0 {
		t.Fatalf("shows %q, switcher %v, opening %d", next.shown, next.switcher != nil, len(next.opening))
	}
	if l := next.layout; l.focus != terminal || l.zoomed != terminal || l.lastTop != project || !l.terminalOpen {
		t.Fatalf("layout %+v", l)
	}
	next.Update(projectEvent{gen: next.following.gen, event: client.ProjectEvent{Project: api.Project{Id: "proj-demo", Name: "demo", Terminals: &terms, Agents: &agents}}})
	if next.tab != 1 {
		t.Fatalf("tab %d", next.tab)
	}
	if a, ok := next.activeAgent(); !ok || a.Id != "agent-b" {
		t.Fatalf("active agent %+v, %v", a, ok)
	}
}

// With no agent active before, none is made so; the first running one becomes
// active only in a project seen for the first time.
func TestResumeNoActiveAgent(t *testing.T) {
	t.Parallel()
	agents := []api.Agent{{Id: "agent-a", Name: "a", State: api.AgentStateRunning}}
	m := New(context.Background(), Options{Style: style.New(false), Resume: &Resume{Project: "demo", ProjectID: "proj-demo", Focus: "project", Zoomed: "none", LastTop: "project", TerminalOpen: true}})
	m.tick = noTick
	m.Update(projectsListed{projects: []api.ProjectSummary{{Name: "demo", State: api.ProjectStateOpen}}})
	m.Update(projectEvent{gen: m.following.gen, event: client.ProjectEvent{Project: api.Project{Id: "proj-demo", Name: "demo", Agents: &agents}}})
	if a, ok := m.activeAgent(); ok {
		t.Fatalf("active agent %+v", a)
	}
}

// A project closed meanwhile, or a state not understood, starts as without one.
func TestResumeNotPossible(t *testing.T) {
	t.Parallel()
	m := New(context.Background(), Options{Style: style.New(false), Resume: &Resume{Project: "gone"}})
	m.tick = noTick
	m.Update(projectsListed{projects: []api.ProjectSummary{{Name: "demo", State: api.ProjectStateOpen}}})
	if m.shown != "" || m.switcher == nil {
		t.Fatalf("shows %q, switcher %v", m.shown, m.switcher != nil)
	}
	if _, err := ParseResume("not json"); err == nil {
		t.Fatal("a state not understood parsed")
	}
}

// r lists the projects again after they could not be, rather than reloading.
func TestRetryKeyBeforeProjectsListed(t *testing.T) {
	t.Parallel()
	m := New(context.Background(), Options{Style: style.New(false), Executable: executableFile(t)})
	m.tick = noTick
	m.Update(projectsListed{err: errors.New("no daemon")})
	m.client = nil
	cmd := m.key(key('r', 0))
	if m.Reload() != nil || cmd == nil || !m.projects.listing {
		t.Fatalf("reload %+v, listing %v", m.Reload(), m.projects.listing)
	}
}
