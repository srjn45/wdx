package tide

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/style"
	"github.com/srjn45/wdx/internal/waittest"
)

// program runs tide's commands as Bubble Tea does, each in its own
// goroutine, so a command waiting on the stream waits on its own; the test
// goroutine alone updates the model, with what they return.
type program struct {
	m    *Model
	msgs chan tea.Msg
}

// follow starts tide on h, its stream followed, at 80 × 24.
func (h testHome) follow(t *testing.T) *program {
	t.Helper()
	m := New(logging.NewContext(context.Background(), h.logger(t, "tide")), Options{Client: h.client(), Style: style.New(false), Home: h.home})
	t.Cleanup(func() {
		m.unfollow()
		m.detachAll()
	})
	p := &program{m: m, msgs: make(chan tea.Msg, 1000)}
	p.update(tea.WindowSizeMsg{Width: 80, Height: 24})
	p.do(m.Init())
	return p
}

func (p *program) do(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				p.do(c)
			}
			return
		}
		if msg != nil {
			p.msgs <- msg
		}
	}()
}

func (p *program) update(msg tea.Msg) {
	_, cmd := p.m.Update(msg)
	p.do(cmd)
}

func (p *program) keys(names ...string) {
	for _, k := range keyMsgs(names...) {
		p.update(k)
	}
}

// until takes the commands' messages until want holds.
func (p *program) until(t *testing.T, what string, want func(m *Model) bool) {
	t.Helper()
	timeout := waittest.After(t)
	for !want(p.m) {
		select {
		case msg := <-p.msgs:
			p.update(msg)
		case <-timeout:
			t.Fatalf("no %s:\n%s", what, text(p.m))
		}
	}
}

// tabs returns the names of the terminal pane's tabs.
func tabs(m *Model) []string {
	var names []string
	for _, term := range m.terminals() {
		names = append(names, term.Name)
	}
	return names
}

func tabsAre(names ...string) func(*Model) bool {
	return func(m *Model) bool { return slices.Equal(tabs(m), names) }
}

func (h testHome) newTerminal(t *testing.T, project, dir, name string) api.Terminal {
	t.Helper()
	r, err := h.client().CreateTerminal(context.Background(), api.CreateTerminal{Project: &project, Dir: dir, Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	return r.Terminal
}

func (h testHome) removeTerminal(t *testing.T, id string) {
	t.Helper()
	if _, err := h.client().DeleteTerminal(context.Background(), "id:"+id, "", "", true); err != nil {
		t.Fatal(err)
	}
}

func shown(name string) func(*Model) bool {
	return func(m *Model) bool { return m.shown == name }
}

func TestFollowTerminals(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	h.newTerminal(t, "api", dir, "b-two")
	h.newTerminal(t, "api", dir, "a-one")
	p := h.follow(t)
	p.until(t, "switcher", func(m *Model) bool { return m.switcher != nil })
	p.keys("enter")
	p.until(t, "tabs by name", tabsAre("a-one", "b-two"))
	wantText(t, p.m, "[● a-one] [● b-two]", "● a-one running")

	// A terminal created elsewhere gets a tab at once.
	three := h.newTerminal(t, "api", dir, "c-three")
	p.until(t, "the new tab", tabsAre("a-one", "b-two", "c-three"))
	p.keys("alt+down", "alt+right", "alt+right")
	wantText(t, p.m, " api · terminal · c-three")

	// Removed, the tab beside it is selected.
	h.removeTerminal(t, three.Id)
	p.until(t, "the tab removed", tabsAre("a-one", "b-two"))
	wantText(t, p.m, " api · terminal · b-two")
}

func TestFollowSwitch(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	api1 := h.newProject(t, "api", api.ProjectStateOpen)
	web := h.newProject(t, "web", api.ProjectStateOpen)
	h.newTerminal(t, "api", api1, "x1")
	h.newTerminal(t, "api", api1, "x2")
	h.newTerminal(t, "web", web, "y")
	p := h.follow(t)
	p.until(t, "switcher", func(m *Model) bool { return m.switcher != nil })
	p.keys("enter")
	p.until(t, "api's tabs", tabsAre("x1", "x2"))
	p.keys("alt+down", "alt+right") // x2

	// Each switch swaps the tabs; back on api, x2 is still selected.
	p.keys("alt+shift+right")
	p.until(t, "web's tabs", func(m *Model) bool { return m.shown == "web" && slices.Equal(tabs(m), []string{"y"}) })
	p.keys("alt+shift+left")
	p.until(t, "api's tabs again", func(m *Model) bool { return m.shown == "api" && slices.Equal(tabs(m), []string{"x1", "x2"}) })
	wantText(t, p.m, " api · terminal · x2")
}

func TestFollowRemoved(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	h.newProject(t, "api", api.ProjectStateOpen)
	p := h.follow(t)
	p.until(t, "switcher", func(m *Model) bool { return m.switcher != nil })
	p.keys("enter")
	p.until(t, "no terminals", func(m *Model) bool { return strings.Contains(text(m), "no terminals: C-q t creates one") })

	if _, err := h.client().DeleteProject(context.Background(), "api", true); err != nil {
		t.Fatal(err)
	}
	p.until(t, "[Open Project]", shown(""))
	wantText(t, p.m, "[Open Project]", "api was removed", "no project open")
}

func TestFollowReconnects(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	stop := h.serve(t)
	h.newProject(t, "api", api.ProjectStateOpen)
	p := h.follow(t)
	p.until(t, "switcher", func(m *Model) bool { return m.switcher != nil })
	p.keys("enter")
	p.until(t, "the project", func(m *Model) bool { return m.following != nil && m.following.project != nil })

	stop()
	p.until(t, "the stream broken", func(m *Model) bool { return m.following.err != nil })
	wantText(t, p.m, "could not be followed, trying again…")

	h.serve(t)
	p.until(t, "the stream back", func(m *Model) bool { return m.following.err == nil })
	wantText(t, p.m, "no terminals: C-q t creates one")
}

func TestNewTerminal(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	h.newProject(t, "api", api.ProjectStateOpen)
	p := h.follow(t)
	p.until(t, "switcher", func(m *Model) bool { return m.switcher != nil })
	p.keys("enter")
	p.until(t, "no terminals", func(m *Model) bool { return strings.Contains(text(m), "no terminals") })

	// The menu offers it, and nothing to remove with the project pane focused.
	p.keys("C-q")
	wantText(t, p.m, "t  new terminal", "t terminal")
	if strings.Contains(text(p.m), "x  remove") {
		t.Fatalf("x offered with nothing to remove:\n%s", text(p.m))
	}
	p.keys("t")
	p.until(t, "a tab", func(m *Model) bool { return len(tabs(m)) == 1 })
	first := tabs(p.m)[0]
	if p.m.layout.focus != terminal {
		t.Fatalf("focus %s", p.m.layout.focus)
	}

	// A second one gets selected once it comes.
	p.keys("C-q", "t")
	p.until(t, "the new tab selected", func(m *Model) bool {
		sel, ok := m.selectedTerminal()
		return len(tabs(m)) == 2 && ok && sel.Name != first
	})
	sel, _ := p.m.selectedTerminal()
	wantText(t, p.m, " api · terminal · "+sel.Name)
}

func TestRemoveTerminalConfirmed(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	h.newTerminal(t, "api", dir, "shell")
	p := h.follow(t)
	p.until(t, "switcher", func(m *Model) bool { return m.switcher != nil })
	p.keys("enter")
	p.until(t, "the tab", tabsAre("shell"))

	p.keys("alt+down", "C-q")
	wantText(t, p.m, "x  remove shell")
	p.keys("x")
	wantText(t, p.m, "┃ Remove terminal", "Remove shell?", "It is running: its shell is stopped.", "y remove  n or Esc keep")

	// n keeps it; so does any key but y.
	p.keys("n")
	if p.m.confirm != nil || !slices.Equal(tabs(p.m), []string{"shell"}) {
		t.Fatalf("n: confirm %v, tabs %v", p.m.confirm, tabs(p.m))
	}
	p.keys("C-q", "x", "q", "esc")
	if p.m.confirm != nil || !slices.Equal(tabs(p.m), []string{"shell"}) {
		t.Fatalf("q then Esc: confirm %v, tabs %v", p.m.confirm, tabs(p.m))
	}

	p.keys("C-q", "x", "y")
	p.until(t, "the tab gone", func(m *Model) bool { return m.confirm == nil && len(tabs(m)) == 0 })
	list, err := h.client().ListTerminals(context.Background(), "api", "")
	if err != nil || len(list.Terminals) != 0 {
		t.Fatalf("terminals: %+v, %v", list.Terminals, err)
	}
}

// screenText is what the terminal pane's terminal shows, "" while none is
// attached.
func screenText(m *Model) string {
	if m.term == nil || m.term.pane == nil {
		return ""
	}
	return m.term.pane.Text()
}

// attachedTo reports whether the terminal pane shows the terminal name
// live, its prompt drawn.
func attachedTo(name string) func(*Model) bool {
	return func(m *Model) bool {
		return m.term != nil && m.term.name == name && m.term.live && strings.Contains(screenText(m), "$")
	}
}

func screenHas(s string) func(*Model) bool {
	return func(m *Model) bool { return strings.Contains(screenText(m), s) }
}

// typed types s into the focused pane, a key per character.
func (p *program) typed(s string) {
	for _, r := range s {
		if r == '\n' {
			p.update(tea.KeyPressMsg{Code: tea.KeyEnter})
			continue
		}
		p.update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// openShown starts tide on h and shows the one open project.
func (h testHome) openShown(t *testing.T) *program {
	t.Helper()
	p := h.follow(t)
	p.until(t, "switcher", func(m *Model) bool { return m.switcher != nil })
	p.keys("enter")
	return p
}

// TestTerminalAttached: the selected tab's terminal is live in the pane,
// takes the keys, has the pane's size, docked or zoomed, and stays
// attached while the pane is hidden.
func TestTerminalAttached(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	h.newTerminal(t, "api", dir, "a-one")
	p := h.openShown(t)
	p.until(t, "a-one attached", attachedTo("a-one"))
	p.keys("alt+down")
	p.typed("echo he''llo; stty size\n")
	// 80 × 24: the pane's frame is a quarter of the 23 rows above the
	// status line, 5, and 80 wide.
	p.until(t, "the echo at the pane's size", screenHas("hello\n3 78"))
	if c := p.m.paneCursor(); c == nil || c.Y < 19 {
		t.Fatalf("cursor %v, want it in the terminal pane", c)
	}
	wantText(t, p.m, "hello")

	p.keys("C-]")
	p.typed("clear; stty size\n")
	p.until(t, "the zoomed size", screenHas("21 78"))

	gen := p.m.term.gen
	p.keys(`C-\`)
	if p.m.layout.shown(terminal) || p.m.term == nil || p.m.term.gen != gen {
		t.Fatalf("hidden: %+v, want the terminal still attached", p.m.term)
	}
	p.keys(`C-\`)
	if p.m.term.gen != gen {
		t.Fatal("shown again, want the same attachment")
	}
}

// TestTerminalTabs: switching tabs attaches the terminal selected alone,
// which shows what it showed.
func TestTerminalTabs(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	h.newTerminal(t, "api", dir, "a-one")
	h.newTerminal(t, "api", dir, "b-two")
	p := h.openShown(t)
	p.until(t, "a-one attached", attachedTo("a-one"))
	p.keys("alt+down")
	p.typed("echo fi''rst\n")
	p.until(t, "the first echo", screenHas("first"))
	first := p.m.term

	p.keys("alt+right")
	if first.pane != nil || first.session != nil {
		t.Fatal("want a-one detached when b-two is selected")
	}
	p.until(t, "b-two attached", attachedTo("b-two"))
	p.typed("echo sec''ond\n")
	p.until(t, "the second echo", screenHas("second"))

	p.keys("alt+left")
	p.until(t, "a-one again, as it was", func(m *Model) bool { return attachedTo("a-one")(m) && screenHas("first")(m) })
}

// TestTerminalExits: a terminal whose shell exits shows its last screen,
// read-only, from its history, under its state.
func TestTerminalExits(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	h.newTerminal(t, "api", dir, "a-one")
	p := h.openShown(t)
	p.until(t, "a-one attached", attachedTo("a-one"))
	p.keys("alt+down")
	p.typed("echo by''e; exit 3\n")
	p.until(t, "its last screen", func(m *Model) bool {
		return m.term != nil && !m.term.live && screenHas("bye")(m)
	})
	wantText(t, p.m, "○ a-one exited · exit code 3", "$ echo by''e; exit 3")
	before := screenText(p.m)
	p.typed("x\n")
	if screenText(p.m) != before || p.m.paneCursor() != nil {
		t.Fatal("want keys to do nothing, and no cursor, on a terminal not running")
	}
}

// TestTerminalRests: after a project switch nothing attaches until it
// rests.
func TestTerminalRests(t *testing.T) {
	t.Parallel()
	m, _ := newModel(80, 24)
	if !m.resting || m.term != nil {
		t.Fatalf("resting %v, attached %+v, want nothing attached yet", m.resting, m.term)
	}
	m.Update(rested{rests: m.rests - 1})
	if m.term != nil {
		t.Fatal("an earlier switch's rest attached")
	}
	m.Update(rested{rests: m.rests})
	if m.term == nil || m.term.name != "shell" {
		t.Fatalf("attached %+v, want shell", m.term)
	}
	wantText(t, m, "attaching…")
}
