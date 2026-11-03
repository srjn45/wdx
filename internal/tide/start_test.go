package tide

import (
	"context"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/gittest"
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
	wantText(t, p.m, "a-one (agent)", "g  start again", "g start")
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
	wantText(t, p.m, "shell (terminal)", "g  start again")
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

// TestRestartAgent: C-q n asks first, saying what the agent is doing, and starts it in a
// new session, live in its pane.
func TestRestartAgent(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	h.newAgent(t, "api", dir, "a-one")
	p := h.openShown(t)
	p.until(t, "a-one attached", agentAttached("a-one"))
	before, ok := p.m.activeAgent()
	if !ok {
		t.Fatal("no agent")
	}
	p.keys("alt+right", "C-q")
	wantText(t, p.m, "a-one (agent)", "R  new session", "R restart")
	p.keys("R")
	wantText(t, p.m, "Restart agent", "again in a new session?", "It is in the middle of something (")
	p.keys("n")
	if p.m.confirm != nil {
		t.Fatal("still asking")
	}
	if a, _ := p.m.activeAgent(); a.SessionId != before.SessionId {
		t.Fatalf("declined, and yet a new session")
	}
	p.keys("C-q", "R", "y")
	p.until(t, "a new session, live", func(m *Model) bool {
		a, ok := m.activeAgent()
		return ok && a.SessionId != before.SessionId && a.State == api.AgentStateRunning && agentAttached("a-one")(m) && m.agentTerm.live
	})
	// Directly: whether a project with no terminal has its pane open depends on what the
	// stream said first, and Alt+Down goes nowhere when it is closed.
	p.m.layout.setFocus(terminal)
	if p.m.menuOffers(restartKey) {
		t.Error("offered on a terminal's pane")
	}
}

// TestRunPlan: r in a draft's tab asks first, then runs it, and its first task's
// agent shows in the tab, which Enter goes to.
func TestRunPlan(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := newTestHome(t)
	h.serve(t)
	h.newProject(t, "api", api.ProjectStateOpen)
	c := h.client()
	got, err := c.GetProject(ctx, "api")
	if err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, got.Project.Worktrees[0].Path, "commit", "-q", "--allow-empty", "-m", "init")
	created, err := c.CreatePlan(ctx, api.CreatePlan{Project: &got.Project.Name, Name: "auth", Content: "goal: g\ntasks:\n  - id: a\n    prompt: p\n"})
	if err != nil {
		t.Fatal(err)
	}
	p := h.openShown(t)
	p.until(t, "the plan listed", func(m *Model) bool { _, ok := m.planOf(created.Plan.Id); return ok })
	p.m.openPlan(created.Plan, true)
	p.m.layout.setFocus(agent)
	p.until(t, "the tab read", func(m *Model) bool { return strings.Contains(text(m), "draft: r runs it") })
	p.keys("r")
	wantText(t, p.m, "Run plan", "Run auth?", "y run")
	p.keys("n")
	if p.m.confirm != nil {
		t.Fatal("still asking")
	}
	p.keys("r", "y")
	p.until(t, "a running, its agent shown", func(m *Model) bool { return strings.Contains(text(m), "running · auth-a") })
	p.keys("enter")
	p.until(t, "its agent's tab", func(m *Model) bool {
		a, ok := m.activeAgent()
		return ok && a.Name == "auth-a"
	})
}
