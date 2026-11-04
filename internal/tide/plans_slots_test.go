package tide

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
)

// slotsModel is a model whose worktrees are main, sso-1 and sso-2, slots of the plan
// sso, the first holding its task schema, done by kit, with a file changed,
// the second free, and other, after main and before the slots; 200 wide. An agent
// of schema before was removed.
func slotsModel(t *testing.T) *Model {
	t.Helper()
	m := worktreesModel(
		api.WorktreeStatus{Name: "main", Path: "/r"},
		api.WorktreeStatus{Name: "sso-2", Path: "/r/.worktrees/sso-2"},
		api.WorktreeStatus{Name: "other", Path: "/r/other"},
		api.WorktreeStatus{Name: "sso-1", Path: "/r/.worktrees/sso-1", Files: []api.WorktreeFile{changed("auth.go", api.WorktreeFileStatusM)}},
	)
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 41})
	gone := onTask(running("sso-schema-0"), "sso", "schema")
	gone.State = api.AgentStateRemoved
	agents := []api.Agent{onTask(running("kit"), "SSO", "schema"), gone}
	identify(agents, nil)
	agents[0].Dir = "/r/.worktrees/sso-1"
	m.following.project.Agents = &agents
	sso := aPlan("doc-sso", "sso", 1, 2)
	schema := "schema"
	sso.State = api.PlanStateRunning
	sso.Slots = []api.PlanSlot{{Slot: 1, Path: "/r/.worktrees/sso-1", Task: &schema}, {Slot: 2, Path: "/r/.worktrees/sso-2"}}
	setPlans(m, sso)
	m.moveToWorktree(0)
	m.list().viewRead = true
	return m
}

func worktreeRowKeys(m *Model) []string {
	var keys []string
	for _, r := range m.worktreeRows() {
		keys = append(keys, strings.ReplaceAll(r.key(), rowSeparator, "/"))
	}
	return keys
}

// TestPlanSlotsGrouped: a plan's slots are under its row, at the place of its first,
// in their order, each saying its task and agent or that it is free; no slot is
// removed nor landed.
func TestPlanSlotsGrouped(t *testing.T) {
	t.Parallel()
	m := slotsModel(t)
	want := []string{"main", "plan:doc-sso", "sso-1", "sso-2", "other"}
	if got := worktreeRowKeys(m); !slices.Equal(got, want) {
		t.Fatalf("rows %q, want %q", got, want)
	}
	wantRow(t, m, "plan/sso", "− plan sso")
	wantRow(t, m, "sso-1", "    + sso-1 schema · kit")
	wantRow(t, m, "sso-2", "      sso-2 free")

	m.moveToWorktree(2) // sso-1
	keys(m, "right")
	if got := worktreeRowKeys(m); !slices.Equal(got, []string{"main", "plan:doc-sso", "sso-1", "sso-1/auth.go", "sso-2", "other"}) {
		t.Errorf("a slot unfolded: %q", got)
	}
	wantRow(t, m, "auth.go", "      M auth.go")
	if r := row(t, m, "sso-1"); strings.Contains(r, "●") {
		t.Errorf("a slot names its agent twice: %q", r)
	}
	(&projectScreen{m: m}).clickWorktree(2, 4) // on sso-1's mark
	if m.list().worktreesOpen["sso-1"] {
		t.Error("a click on a slot's mark left it open")
	}

	m.list().worktreesOpen = nil
	for i := range 4 {
		m.moveToWorktree(i)
		if _, ok := m.removable(); ok && i > 0 {
			t.Errorf("row %d of a plan's slots can be removed", i)
		}
	}
	m.moveToWorktree(3)
	base := "main"
	(*m.following.project.WorktreeStatuses)[1].Ahead, (*m.following.project.WorktreeStatuses)[1].Base = aheadBy(0), &base // landed, as would be removable
	if _, ok := m.removable(); ok {
		t.Error("a landed slot can be removed")
	}
	if _, ok := m.landBarKey("sso-1"); ok {
		t.Error("a slot offers L")
	}
	v := &diffView{}
	if cmd := m.askLand(committedTab("sso-1"), v); cmd != nil || v.notice != "sso-1 is a slot of plan sso, which lands it on plan/sso" {
		t.Errorf("L on a slot: %q", v.notice)
	}
	m.moveToWorktree(1)
	if hint := (&projectScreen{m: m}).sectionHint(); hint != planGroupHint {
		t.Errorf("the hint on a plan's row: %q", hint)
	}
	if title := m.listSelectionTitle(); title != "sso (plan)" {
		t.Errorf("the menu's title: %q", title)
	}
}

// TestPlanSlotsFold: ← folds a plan's row and → unfolds it, apart from its agents';
// ← on a slot goes to it; the fold is kept in the view; a folded plan carries the
// mark of a slot's tab; Enter opens the plan's tab.
func TestPlanSlotsFold(t *testing.T) {
	t.Parallel()
	m := slotsModel(t)
	m.moveToWorktree(3) // sso-2
	keys(m, "left")
	if m.list().worktreeKey != "plan:doc-sso" {
		t.Fatalf("← on a slot: cursor %q", m.list().worktreeKey)
	}
	keys(m, "left")
	if got := worktreeRowKeys(m); !slices.Equal(got, []string{"main", "plan:doc-sso", "other"}) {
		t.Errorf("folded rows %q", got)
	}
	wantRow(t, m, "plan/sso", "+ plan sso")
	if m.list().closed[planGroupKey("sso")] {
		t.Error("folding the slots folded the agents")
	}
	_, v, ok := m.currentProjectView()
	if !ok || !slices.Equal(v.SlotsFolded, []string{"doc-sso"}) {
		t.Errorf("the view kept %v", v.SlotsFolded)
	}
	l := m.list()
	m.applyProjectView("proj-demo", l, projectView{SlotsFolded: []string{"doc-x"}})
	if !l.slotsFolded["doc-x"] {
		t.Errorf("the view put back %v", l.slotsFolded)
	}
	l.slotsFolded["doc-sso"] = true
	l.openTab(committedTab("sso-1"))
	if r := row(t, m, "plan/sso"); !strings.Contains(r, "◂") {
		t.Errorf("a folded plan's row, a slot's tab shown: %q", r)
	}
	keys(m, "right")
	if l.slotsFolded["doc-sso"] {
		t.Error("→ left the slots folded")
	}
	if r := row(t, m, "plan/sso"); strings.Contains(r, "◂") {
		t.Errorf("an unfolded plan's row, a slot's tab shown: %q", r)
	}
	(&projectScreen{m: m}).clickWorktree(1, 2)
	if !l.slotsFolded["doc-sso"] {
		t.Error("a click on the mark did not fold")
	}
	keys(m, "enter")
	if l.active != planTab("doc-sso") {
		t.Errorf("Enter on a plan's row opened %q", l.active)
	}
	if r := row(t, m, "plan/sso"); !strings.Contains(r, "◂") {
		t.Errorf("the plan's row, its tab shown: %q", r)
	}
}

// TestPlanSlotsFilter: a slot matching shows under its plan's row, and the plan's
// name matching shows all its slots; once the plan has no slots, they are worktrees
// as any.
func TestPlanSlotsFilter(t *testing.T) {
	t.Parallel()
	m := slotsModel(t)
	l := m.list()
	l.slotsFolded = map[string]bool{"doc-sso": true}
	l.filter.text = "auth"
	if got := worktreeRowKeys(m); !slices.Equal(got, []string{"plan:doc-sso", "sso-1", "sso-1/auth.go"}) {
		t.Errorf("filtered rows %q", got)
	}
	l.filter.text = "sso"
	if got := worktreeRowKeys(m); !slices.Equal(got, []string{"plan:doc-sso", "sso-1", "sso-2"}) {
		t.Errorf("rows for the plan's name %q", got)
	}
	if rows := m.worktreeRows(); !rows[0].match {
		t.Error("the plan's row does not match its name")
	}
	l.filter.text = "other"
	if got := worktreeRowKeys(m); !slices.Equal(got, []string{"other"}) {
		t.Errorf("rows for another %q", got)
	}
	l.filter.text, l.slotsFolded = "", nil
	m.moveToWorktree(3) // sso-2
	done := m.plans()[0]
	done.State, done.Slots = api.PlanStateDone, []api.PlanSlot{}
	setPlans(m, done)
	if got := worktreeRowKeys(m); !slices.Equal(got, []string{"main", "sso-2", "other", "sso-1"}) {
		t.Errorf("rows once done %q", got)
	}
	if l.worktreeRow != 1 {
		t.Errorf("the cursor on sso-2 is on row %d once done", l.worktreeRow)
	}
}
