package tide

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/style"
)

// newModel returns tide's window at width × height, uncolored, with a clock
// the test moves and no daemon, showing the project demo with the running
// terminals shell, tests and server.
func newModel(width, height int) (*Model, *time.Time) {
	m := New(context.Background(), Options{Style: style.New(false)})
	m.tick = noTick
	m.projects.loaded = true
	m.show("demo") // its commands, listing the projects again and opening the stream, never run
	sent(m, "shell", "tests", "server")
	now := time.UnixMilli(0)
	m.now = func() time.Time { return now }
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return m, &now
}

// sent makes m get the project demo from its stream, with running
// terminals named names, their ids term-<name>.
func sent(m *Model, names ...string) {
	terms := []api.Terminal{}
	for _, name := range names {
		terms = append(terms, api.Terminal{Id: "term-" + name, Name: name, State: api.TerminalStateRunning, Dir: "/work/demo"})
	}
	m.Update(projectEvent{gen: m.following.gen, event: client.ProjectEvent{Project: api.Project{Id: "proj-demo", Name: "demo", Terminals: &terms}}})
}

func key(code rune, mod tea.KeyMod) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Mod: mod} }

func clickAt(x, y int) tea.MouseClickMsg { return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft} }

// text is what m shows, a line per row, without trailing spaces.
func text(m *Model) string {
	lines := strings.Split(ansi.Strip(m.render()), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

func TestKeys(t *testing.T) {
	t.Parallel()
	m, _ := newModel(80, 24)
	if m.layout.focus != project {
		t.Fatalf("tide starts focused on %s", m.layout.focus)
	}
	m.Update(key(tea.KeyRight, tea.ModAlt))
	m.Update(key('a', 0))
	m.Update(key(tea.KeyLeft, tea.ModAlt))
	m.Update(key(tea.KeyDown, tea.ModAlt))
	m.Update(key('c', tea.ModCtrl))
	m.Update(tea.PasteMsg{Content: "hello"})
	// In the terminal pane, left and right switch tabs, going round.
	m.Update(key(tea.KeyLeft, tea.ModAlt))
	if m.tab != 2 || m.layout.focus != terminal {
		t.Fatalf("tab %d, focus %s after ←", m.tab, m.layout.focus)
	}
	m.Update(key(tea.KeyRight, tea.ModAlt))
	m.Update(key(tea.KeyRight, tea.ModAlt))
	if m.tab != 1 {
		t.Fatalf("tab %d after → →", m.tab)
	}

	m.Update(key(']', tea.ModCtrl))
	if m.layout.zoomed != terminal {
		t.Fatalf("C-] zoomed %s", m.layout.zoomed)
	}
	m.Update(key('\\', tea.ModCtrl))
	if m.layout.shown(terminal) || m.layout.focus != project {
		t.Fatalf("C-\\ left %+v", m.layout)
	}
}

func TestMenu(t *testing.T) {
	t.Parallel()
	m, _ := newModel(80, 24)
	m.Update(key('q', tea.ModCtrl))
	if got := text(m); !strings.Contains(got, "q  quit") || !strings.Contains(got, "o  open project") || !strings.Contains(got, "p  switch project") {
		t.Fatalf("no menu:\n%s", text(m))
	}
	m.Update(key(tea.KeyEscape, 0))
	if m.menu || strings.Contains(text(m), "quit") {
		t.Fatalf("Esc left the menu open:\n%s", text(m))
	}
	m.Update(key('q', tea.ModCtrl))
	_, cmd := m.Update(key('q', 0))
	if cmd == nil {
		t.Fatal("C-q q did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("C-q q returned %T", cmd())
	}
}

func TestClicks(t *testing.T) {
	t.Parallel()
	m, now := newModel(100, 41) // panes in 100 × 40: project 0-25, agent 25-99, terminal from row 30
	m.Update(clickAt(60, 5))
	if m.layout.focus != agent {
		t.Fatalf("focus %s", m.layout.focus)
	}
	m.Update(tea.MouseClickMsg{X: 5, Y: 3, Button: tea.MouseRight})
	if m.layout.focus != agent {
		t.Fatal("a right click moved the focus")
	}
	m.Update(clickAt(5, 3))
	m.Update(tea.MouseWheelMsg{X: 60, Y: 5, Button: tea.MouseWheelUp})
	if m.layout.focus != project {
		t.Fatalf("the wheel moved the focus to %s", m.layout.focus)
	}

	// The tabs: " [● shell] [● tests] [● server] " from column 1 of row 30.
	m.Update(clickAt(12, 30))
	if m.layout.focus != terminal || m.tab != 1 {
		t.Fatalf("a click on [tests]: focus %s, tab %d", m.layout.focus, m.tab)
	}

	// The zoom button, 4 columns before the corner.
	m.Update(clickAt(95, 0))
	if m.layout.zoomed != agent {
		t.Fatalf("the agent's zoom button zoomed %s", m.layout.zoomed)
	}
	m.Update(clickAt(95, 0))
	if m.layout.zoomed != none {
		t.Fatalf("the zoom button again left %s zoomed", m.layout.zoomed)
	}

	// A double-click on a title zooms; two slow clicks do not.
	m.Update(clickAt(20, 0))
	*now = now.Add(doubleClick + time.Millisecond)
	m.Update(clickAt(20, 0))
	if m.layout.zoomed != none {
		t.Fatal("two slow clicks zoomed")
	}
	*now = now.Add(doubleClick)
	m.Update(clickAt(20, 0))
	if m.layout.zoomed != project {
		t.Fatalf("a double-click zoomed %s", m.layout.zoomed)
	}
}

func TestRender(t *testing.T) {
	t.Parallel()
	m, _ := newModel(40, 11)
	m.Update(key(tea.KeyRight, tea.ModAlt))
	m.Update(key('x', 0))
	want := `┌ [Pr─[⤢]─┬ agent ─────────────────[⤢]─┐
│ Agents0 │no agents: C-q a creates one│
│ no agent│                            │
│         │                            │
│         │                            │
│         │                            │
│         │                            │
├ [● shell] [● tests] [● server] ──[⤢]─┤
│● shell running                       │
└──────────────────────────────────────┘
 demo · agent Alt+arrows move  C-] zoom`
	if got := text(m); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTooSmall(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{4, 24}, {80, 5}, {1, 1}, {0, 0}} {
		m, _ := newModel(size[0], size[1])
		got := text(m)
		if want := ansi.Truncate(tooSmall, size[0], ""); got != want {
			t.Errorf("%v: got %q, want %q", size, got, want)
		}
		m.Update(clickAt(0, 0))
		m.Update(key(tea.KeyDown, tea.ModAlt))
	}
	// Zoomed, a pane fits where the three do not.
	m, _ := newModel(4, 5)
	m.Update(key(']', tea.ModCtrl))
	if got := text(m); got == tooSmall || !strings.HasPrefix(got, "┌") {
		t.Fatalf("zoomed at 4×5:\n%s", got)
	}
}

// TestCreatedTerminalSelected selects a terminal created here whichever comes
// first: the answer to the request, or its tab on the stream.
func TestCreatedTerminalSelected(t *testing.T) {
	t.Parallel()
	created := terminalCreated{projectID: "proj-demo", terminal: api.Terminal{Id: "term-api", Name: "api"}}
	for name, steps := range map[string][]func(*Model){
		"answer first": {
			func(m *Model) { m.Update(created) },
			func(m *Model) { sent(m, "api", "shell", "tests", "server") },
		},
		"stream first": {
			func(m *Model) { sent(m, "api", "shell", "tests", "server") },
			func(m *Model) { m.Update(created) },
		},
	} {
		m, _ := newModel(80, 24)
		m.selectTab(1)
		for _, step := range steps {
			step(m)
		}
		if sel, ok := m.selectedTerminal(); !ok || sel.Id != "term-api" {
			t.Fatalf("%s: selected %+v", name, sel)
		}
	}
}
