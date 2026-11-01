package tide

import (
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

// TestStartAgentAgain: an agent that ended starts again from its pane, the menu
// offering it only then, and goes live in the pane; from the list, g does the
// same.
func TestStartAgentAgain(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	h.newAgent(t, "api", dir, "a-one")
	p := h.openShown(t)
	p.until(t, "a-one attached", agentAttached("a-one"))
	if p.m.menuOffers(startKey) {
		t.Fatal("offered on a running agent")
	}

	exit := func() {
		t.Helper()
		p.typed("exit 3\n")
		p.until(t, "it exited", func(m *Model) bool {
			a, ok := m.activeAgent()
			return ok && a.State == api.AgentStateExited && m.agentTerm != nil && !m.agentTerm.live
		})
	}
	live := func() {
		t.Helper()
		p.until(t, "it live again", func(m *Model) bool {
			a, ok := m.activeAgent()
			return ok && a.State == api.AgentStateRunning && agentAttached("a-one")(m) && m.agentTerm.live
		})
	}

	p.keys("alt+right")
	exit()
	p.keys("C-q")
	wantText(t, p.m, "g  start a-one again", "g start")
	p.keys("g")
	live()
	p.typed("echo ag''ain\n")
	p.until(t, "it takes keys", agentScreenHas("again"))

	exit()
	p.keys("alt+left", "g")
	live()
}

// TestStartTerminalAgain: the terminal of the tab in focus starts again and goes
// live in its tab, without asking.
func TestStartTerminalAgain(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	h.newTerminal(t, "api", dir, "shell")
	p := h.openShown(t)
	p.until(t, "attached", attachedTo("shell"))
	p.keys("alt+down")
	if p.m.menuOffers(startKey) {
		t.Fatal("offered on a running terminal")
	}

	p.typed("exit 3\n")
	p.until(t, "it exited", func(m *Model) bool {
		s, ok := m.startable()
		return ok && s.name == "shell"
	})
	p.keys("C-q")
	wantText(t, p.m, "g  start shell again")
	p.keys("g")
	p.until(t, "it live again", func(m *Model) bool {
		s, ok := m.selectedTerminal()
		return ok && s.State == api.TerminalStateRunning && m.term != nil && m.term.live && m.term.pane != nil
	})
	if p.m.confirm != nil {
		t.Fatal("asked")
	}
	p.typed("echo ag''ain\n")
	p.until(t, "it takes keys", screenHas("again"))
}

// TestStartAgainRefused: what started elsewhere meanwhile is left as it is, and
// the status line says so.
func TestStartAgainRefused(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	a := h.newAgent(t, "api", dir, "a-one")
	p := h.openShown(t)
	p.until(t, "a-one attached", agentAttached("a-one"))

	p.do(p.m.startAgain(startTarget{kind: "agent", id: a.Id, name: a.Name}))
	p.until(t, "the refusal", func(m *Model) bool { return m.statusNotice != "" })
	if !agentAttached("a-one")(p.m) {
		t.Fatalf("want it left attached:\n%s", text(p.m))
	}
}
