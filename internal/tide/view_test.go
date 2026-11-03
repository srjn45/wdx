package tide

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/waittest"
)

// viewWaits is tea.Tick for every wait but the view's, which settles as settle
// says: at once, or never.
func viewWaits(settle func(viewSettled) tea.Cmd) func(time.Duration, func(time.Time) tea.Msg) tea.Cmd {
	return func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
		if msg, ok := fn(time.Time{}).(viewSettled); ok {
			return settle(msg)
		}
		return tea.Tick(d, fn)
	}
}

// atOnce ends a view's wait as soon as its message is taken: of keys pressed
// together, only the last change's wait still counts then.
var atOnce = viewWaits(func(msg viewSettled) tea.Cmd { return func() tea.Msg { return msg } })

// neverSettles leaves only quitting or reloading to send a view.
var neverSettles = viewWaits(func(viewSettled) tea.Cmd { return nil })

// viewSaved reports whether the daemon has what tide shows now.
func viewSaved(m *Model) bool {
	if m.view.saving || !m.view.read {
		return false
	}
	whole, _ := json.Marshal(m.currentTideView())
	id, v, ok := m.currentProjectView()
	project, _ := json.Marshal(v)
	return string(whole) == string(m.view.sentTide) && (!ok || string(project) == string(m.view.sentProject[id]))
}

// TestViewKept: what one tide showed, a tide started later on the same home shows:
// the project, the zoom, the current tab, the cursor and the folds.
func TestViewKept(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	one := h.newAgent(t, "api", dir, "a-one")
	two := h.newAgent(t, "api", dir, "b-two")
	p := h.followTicking(t, atOnce)
	p.until(t, "switcher", func(m *Model) bool { return m.switcher != nil })
	p.keys("enter")
	p.until(t, "a-one attached", agentAttached("a-one"))
	p.keys("down", "enter", "alt+right", "C-]")
	p.until(t, "b-two zoomed", func(m *Model) bool { return m.list().active == two.Id && m.layout.zoomed == agent })
	l := p.m.list()
	l.closed["agent-folded"] = true // a fold is kept by its id, whatever it names
	l.code.worktree = "main"
	l.code.files = map[string]*codeFiles{"main": {open: map[string]bool{"src": true}}}
	p.update(nil) // what changes the view outside a key is taken as the next update
	// The tabs go apart from the view: the next tide starts on what both say.
	p.until(t, "the view and the tabs saved", func(m *Model) bool {
		return viewSaved(m) && tabsSavedAs(one.Id, two.Id)(m)
	})

	next := h.followTicking(t, atOnce)
	next.until(t, "the view placed", func(m *Model) bool {
		l := m.list()
		return m.shown == "api" && m.switcher == nil && l != nil && l.viewRead && l.active == two.Id && l.cursor == two.Id
	})
	got := next.m.list()
	if next.m.layout.zoomed != agent || !got.closed["agent-folded"] || got.code.worktree != "main" ||
		got.code.files["main"] == nil || !got.code.files["main"].open["src"] {
		t.Fatalf("zoomed %s, folded %v, code %q %+v", next.m.layout.zoomed, got.closed, got.code.worktree, got.code.files["main"])
	}
}

// TestViewSettles: a change made during the wait starts it again, so the view goes
// once the keys rest, not half a second after the first.
func TestViewSettles(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	for _, name := range []string{"a-one", "b-two", "c-three"} {
		h.newAgent(t, "api", dir, name)
	}
	var waits []viewSettled // the waits started, which the test ends
	p := h.followTicking(t, viewWaits(func(msg viewSettled) tea.Cmd {
		waits = append(waits, msg)
		return nil
	}))
	// settled ends the latest wait until the view is saved: events go on changing
	// it, as the agents' states come, each starting a wait of its own.
	settled := func() {
		t.Helper()
		timeout := waittest.After(t)
		ended := map[int]bool{}
		for !viewSaved(p.m) {
			if n := len(waits); n > 0 && waits[n-1].change == p.m.view.changes && !ended[waits[n-1].change] {
				ended[waits[n-1].change] = true
				p.update(waits[n-1])
				continue
			}
			select {
			case msg := <-p.msgs:
				p.update(msg)
			case <-timeout:
				t.Fatalf("the view never saved:\n%s", text(p.m))
			}
		}
	}
	p.until(t, "switcher", func(m *Model) bool { return m.switcher != nil })
	p.keys("enter")
	p.until(t, "a-one attached", agentAttached("a-one"))
	settled()

	waits = nil
	p.keys("down")
	p.keys("down")
	if len(waits) != 2 {
		t.Fatalf("waits %v", waits)
	}
	p.update(waits[0]) // the first wait ends: a change came since
	if p.m.view.saving {
		t.Fatal("a view went while the keys were still pressed")
	}
	p.update(waits[1])
	if !p.m.view.saving {
		t.Fatal("no view went once the keys rested")
	}
	settled()
}

// TestViewFlushedOnQuit: what changed less than the wait before quitting is kept.
func TestViewFlushedOnQuit(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	h.newAgent(t, "api", dir, "a-one")
	p := h.followTicking(t, neverSettles)
	p.until(t, "switcher", func(m *Model) bool { return m.switcher != nil })
	p.keys("enter")
	p.until(t, "a-one attached", agentAttached("a-one"))
	p.keys("alt+right", "C-]")
	p.until(t, "the agent zoomed", func(m *Model) bool { return m.layout.zoomed == agent })
	p.keys("C-q", "q")
	timeout := waittest.After(t)
	for quit := false; !quit; {
		select {
		case msg := <-p.msgs:
			_, quit = msg.(tea.QuitMsg)
		case <-timeout:
			t.Fatalf("tide did not quit:\n%s", text(p.m))
		}
	}

	v, err := h.client().TideView(context.Background())
	var whole tideView
	if err != nil || json.Unmarshal(v, &whole) != nil || whole.Zoomed != "agent" {
		t.Fatalf("kept %s, %v", v, err)
	}
}

// TestViewOfAClosedProject: a view naming a project closed since starts on the
// switcher, as tide does with none.
func TestViewOfAClosedProject(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	h.newProject(t, "api", api.ProjectStateOpen)
	h.newProject(t, "web", api.ProjectStateClosed)
	web, err := h.client().GetProject(context.Background(), "web")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.client().ReplaceTideView(context.Background(), json.RawMessage(`{"project_id":"`+web.Project.Id+`"}`)); err != nil {
		t.Fatal(err)
	}
	p := h.followTicking(t, atOnce)
	p.until(t, "switcher", func(m *Model) bool { return m.switcher != nil && m.shown == "" })
}
