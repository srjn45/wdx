package tide

import (
	"context"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

func (h testHome) projectState(t *testing.T, name string) api.ProjectState {
	t.Helper()
	p, err := h.client().GetProject(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return p.Project.State
}

func projectKnown(m *Model) bool { return m.following != nil && m.following.project != nil }

// TestCloseProjectIdle: with nothing running it closes without asking, and tide
// goes to the switcher, as at start, when another project is open.
func TestCloseProjectIdle(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	h.newProject(t, "api", api.ProjectStateOpen)
	h.newProject(t, "web", api.ProjectStateOpen)
	p := h.openShown(t)
	p.until(t, "the project", projectKnown)

	p.keys("C-q")
	wantText(t, p.m, "w  close api", "w close")
	p.keys("w")
	if p.m.confirm != nil {
		t.Fatalf("asked with nothing running:\n%s", text(p.m))
	}
	p.until(t, "the switcher", func(m *Model) bool { return m.shown == "" && m.switcher != nil })
	wantText(t, p.m, "┃ Switch Project", "web")
	if s := h.projectState(t, "api"); s != api.ProjectStateClosed {
		t.Fatalf("state %s", s)
	}
	if p.m.switcher.selected != "web" {
		t.Fatalf("selected %q", p.m.switcher.selected)
	}
}

// TestCloseProjectRunning: with something running it asks first, says what
// stops, and n keeps it; the last project closed leaves Open Project, where it
// can be opened again.
func TestCloseProjectRunning(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	h.newAgent(t, "api", dir, "a-one")
	h.newTerminal(t, "api", dir, "shell")
	p := h.openShown(t)
	p.until(t, "a-one attached", agentAttached("a-one"))

	p.keys("C-q", "w")
	wantText(t, p.m, "┃ Close project", "Close api?", "Its running agents and terminals stop.", "y close  n or Esc keep", "confirm: y close")
	p.keys("n")
	if p.m.confirm != nil || h.projectState(t, "api") != api.ProjectStateOpen {
		t.Fatalf("n: confirm %v", p.m.confirm)
	}

	// Without the menu in the project pane, as its other letters.
	p.keys("w", "y")
	wantText(t, p.m, "closing…")
	p.until(t, "Open Project", func(m *Model) bool { return m.shown == "" && m.confirm == nil && len(m.opening) == 1 })
	wantText(t, p.m, "┃ Open Project", "closed api")
	if p.m.agentTerm != nil || p.m.term != nil {
		t.Fatalf("still attached: agent %+v, terminal %+v", p.m.agentTerm, p.m.term)
	}
	agents, err := h.client().ListAgents(context.Background(), "api", "")
	if err != nil || agents.Agents[0].State != api.AgentStateStopped {
		t.Fatalf("agents: %+v, %v", agents.Agents, err)
	}

	p.until(t, "api listed closed", func(m *Model) bool { return strings.Contains(text(m), "api") })
	p.keys("a", "p", "i", "enter")
	p.until(t, "api again", func(m *Model) bool { return m.shown == "api" && agentAttached("a-one")(m) })
}

// TestCloseProjectRefused: something that started after the stream last said
// is not stopped without asking; the status line tells, and the project stays.
func TestCloseProjectRefused(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	p := h.openShown(t)
	p.until(t, "the project", projectKnown)
	r, ok := p.m.closableProject()
	if !ok || r.running {
		t.Fatalf("closable %v, %+v", ok, r)
	}
	h.newTerminal(t, "api", dir, "shell")

	p.do(p.m.closeProject(r, false))
	p.until(t, "the refusal", func(m *Model) bool { return m.statusNotice != "" })
	if p.m.shown != "api" || h.projectState(t, "api") != api.ProjectStateOpen {
		t.Fatalf("shown %q:\n%s", p.m.shown, text(p.m))
	}
}

// TestCloseProjectNotOffered: there is nothing to close with no project shown.
func TestCloseProjectNotOffered(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	p := h.follow(t)
	p.until(t, "Open Project", func(m *Model) bool { return len(m.opening) == 1 })
	if p.m.menuOffers(closeProjectKey) {
		t.Fatal("offered with no project")
	}
	p.keys("C-q", "w")
	if p.m.confirm != nil {
		t.Fatalf("w with no project:\n%s", text(p.m))
	}
}
