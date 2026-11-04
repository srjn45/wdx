package tide

import (
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

func onTask(a api.Agent, plan, task string) api.Agent {
	a.Task = &api.AgentTask{Plan: plan, Task: task}
	return a
}

// groupedModel is a model whose agents are alpha, beta and the agents of the plan
// sso's tasks a and b, the second with a child, and whose plan sso runs with a task
// failed.
func groupedModel(t *testing.T) *Model {
	t.Helper()
	child := running("helper")
	b := onTask(running("sso-b-1"), "sso", "b")
	b.Children = &[]api.Agent{child}
	m := newAgentsModel(200, 41, running("alpha"), onTask(running("sso-a-1"), "SSO", "a"), running("beta"), b)
	sso := aPlan("doc-sso", "sso", 1, 2)
	done, failed := int64(0), int64(1)
	sso.State, sso.TasksDone, sso.TasksFailed = api.PlanStateRunning, &done, &failed
	setPlans(m, sso)
	m.layout.setFocus(project)
	m.moveCursor(0)
	m.list().viewRead = true
	return m
}

func agentRowKeys(m *Model) []string {
	var keys []string
	for _, r := range m.listRows() {
		keys = append(keys, r.key())
	}
	return keys
}

// TestPlanAgentsGrouped: a plan's agents are under its row, at the place of its
// first, their children under them.
func TestPlanAgentsGrouped(t *testing.T) {
	t.Parallel()
	m := groupedModel(t)
	want := []string{"agent-alpha", "plan:sso", "agent-sso-a-1", "agent-sso-b-1", "agent-helper", "agent-beta"}
	if got := agentRowKeys(m); !slices.Equal(got, want) {
		t.Fatalf("rows %q, want %q", got, want)
	}
	lines := paneLines(m)
	for _, want := range []string{"− plan sso running 0/2", "  ├ ● sso-a-1", "  └ − ● sso-b-1", "      └ ● helper"} {
		if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, want) }) {
			t.Errorf("no line holding %q in:\n%s", want, strings.Join(lines, "\n"))
		}
	}
	if _, ok := m.cursorAgent(); !ok {
		t.Error("no agent under the cursor on alpha")
	}
	m.moveCursor(1)
	if _, ok := m.cursorAgent(); ok {
		t.Error("the plan's row is taken for an agent")
	}
	if hint := (&projectScreen{m: m}).sectionHint(); hint != planGroupHint {
		t.Errorf("the hint on a plan's row: %q", hint)
	}
	if d, p := m.movable(); d != nil || p != nil {
		t.Error("the plan's row offers a move")
	}
	if _, ok := m.removable(); ok {
		t.Error("the plan's row offers a removal")
	}
}

// TestPlanAgentsFold: ← folds a plan's row, which then says what it hides and that
// it needs the person; ← on one of its agents goes to it; → unfolds; the fold is
// kept in the view; Enter opens the plan's tab.
func TestPlanAgentsFold(t *testing.T) {
	t.Parallel()
	m := groupedModel(t)
	m.moveCursor(2) // sso-a-1
	keys(m, "left")
	if m.list().cursor != "plan:sso" {
		t.Fatalf("← on a plan's agent: cursor %q", m.list().cursor)
	}
	keys(m, "left")
	if got := agentRowKeys(m); !slices.Equal(got, []string{"agent-alpha", "plan:sso", "agent-beta"}) {
		t.Errorf("folded rows %q", got)
	}
	wantText(t, m, "+ plan sso running 0/2 3 below needs you")
	_, v, ok := m.currentProjectView()
	if !ok || !slices.Contains(v.Folded, "plan:sso") {
		t.Errorf("the view kept %v", v.Folded)
	}
	m.list().active = "agent-sso-b-1"
	if i := shownAs(m.listRows(), m.agents(), "agent-helper"); i != 1 {
		t.Errorf("a folded plan's agent's child shows as row %d", i)
	}
	m.list().reveal(m.agents(), "agent-helper")
	if m.list().closed["plan:sso"] {
		t.Error("revealing a plan's agent left its plan folded")
	}
	m.list().closed["plan:sso"] = true
	keys(m, "right")
	if m.list().closed["plan:sso"] {
		t.Error("→ left the plan folded")
	}
	m.toggleFold(1)
	if !m.list().closed["plan:sso"] || m.list().cursor != "plan:sso" {
		t.Errorf("a click on the mark: closed %v, cursor %q", m.list().closed, m.list().cursor)
	}
	keys(m, "enter")
	if m.list().active != planTab("doc-sso") {
		t.Errorf("Enter on a plan's row opened %q", m.list().active)
	}
	if r := row(t, m, "plan sso"); !strings.Contains(r, "◂") {
		t.Errorf("the plan's row, its tab shown: %q", r)
	}
}

// TestPlanAgentsFilter: an agent matching shows under its plan's row, and the
// plan's name matching shows all its agents.
func TestPlanAgentsFilter(t *testing.T) {
	t.Parallel()
	m := groupedModel(t)
	m.list().closed["plan:sso"] = true
	m.list().filter.text = "helper"
	if got := agentRowKeys(m); !slices.Equal(got, []string{"plan:sso", "agent-sso-b-1", "agent-helper"}) {
		t.Errorf("filtered rows %q", got)
	}
	m.list().filter.text = "sso"
	if got := agentRowKeys(m); !slices.Equal(got, []string{"plan:sso", "agent-sso-a-1", "agent-sso-b-1", "agent-helper"}) {
		t.Errorf("rows for the plan's name %q", got)
	}
	if rows := m.listRows(); !rows[0].match {
		t.Error("the plan's row does not match its name")
	}
}
