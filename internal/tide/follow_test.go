package tide

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
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
	m := New(context.Background(), Options{Client: h.client(), Style: style.New(false)})
	t.Cleanup(m.unfollow)
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
	timeout := time.After(10 * time.Second)
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
