package tide

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
)

func aPlan(id, name string, revision int64, tasks int) api.Plan {
	n := int64(tasks)
	return api.Plan{Id: id, Name: name, State: api.PlanStateDraft, Tasks: &n, Revision: revision}
}

// aParent is a plan of plans listing children, whose Parent it sets.
func aParent(id, name string, children ...*api.Plan) api.Plan {
	var names []string
	for _, c := range children {
		names = append(names, c.Name)
		c.Parent = &name
	}
	return api.Plan{Id: id, Name: name, State: api.PlanStateDraft, Plans: &names, Revision: 1}
}

// setPlans makes the stream bring the project shown with plans.
func setPlans(m *Model, plans ...api.Plan) {
	m.following.project.Plans = &plans
	m.plansChanged()
}

// plansModel is a model whose project has the plan of plans auth, with auth-a and
// auth-b under it, and the plan sso, with the Plans section first and the cursor
// on auth.
func plansModel(t *testing.T) *Model {
	t.Helper()
	m := worktreesModel(api.WorktreeStatus{Name: "main", Path: "/r"})
	m.sections = sectionOrder("pawc")
	a, b := aPlan("doc-a", "auth-a", 1, 2), aPlan("doc-b", "auth-b", 1, 1)
	auth := aParent("doc-auth", "auth", &b, &a) // in its DAG order: b first
	setPlans(m, auth, a, b, aPlan("doc-sso", "sso", 2, 4))
	m.moveToPlan(0)
	if l := m.list(); l.section != sectionPlans || l.planCursor != "doc-auth" {
		t.Fatalf("on %v %q", l.section, l.planCursor)
	}
	return m
}

func loadPlan(m *Model, id string, c api.PlanContent) {
	m.Update(planLoaded{projectID: "proj-demo", tab: planTab(id), content: c})
}

func TestPlansListed(t *testing.T) {
	t.Parallel()
	m := plansModel(t)
	wantText(t, m, "Plans", "− auth draft 2 plans", "  auth-b draft 1 task", "  auth-a draft 2 tasks", "sso draft 4 tasks")
	if got := headingRows(m); !slices.Equal(got, []string{"Plans", "Agents", "Worktrees", "Code"}) {
		t.Errorf("headings %q", got)
	}
	if heading := paneLines(m)[1]; !strings.Contains(heading, "Plans") || !strings.HasSuffix(strings.TrimRight(heading, "│ "), "4") {
		t.Errorf("the heading has no count: %q", heading)
	}
	if !strings.Contains(statusRow(m), "←→ fold") {
		t.Errorf("hint on a plan of plans %q", statusRow(m))
	}
	if got := m.planRows(); len(got) != 4 || got[1].plan.Name != "auth-b" || !got[1].child || got[3].child {
		t.Fatalf("rows %+v", got)
	}

	keys(m, "left")
	wantText(t, m, "+ auth draft 2 plans")
	if strings.Contains(text(m), "auth-a") || len(m.planRows()) != 2 {
		t.Errorf("folded, its plans still show:\n%s", text(m))
	}
	keys(m, "down")
	if l := m.list(); l.planCursor != "doc-sso" || strings.Contains(statusRow(m), "fold") {
		t.Errorf("down from the folded parent: %q, hint %q", l.planCursor, statusRow(m))
	}
	keys(m, "up", "right")
	if len(m.planRows()) != 4 {
		t.Errorf("unfolded: %+v", m.planRows())
	}
	keys(m, "down", "left") // on auth-b, its parent folds
	if l := m.list(); len(m.planRows()) != 2 || l.planCursor != "doc-auth" {
		t.Errorf("← on a child: %+v, cursor %q", m.planRows(), l.planCursor)
	}
	keys(m, "right")

	// With none, the row says how to add one, and the cursor leaves the section.
	setPlans(m)
	wantText(t, m, "no plans: see wdx help plans")
	if l := m.list(); l.section != sectionAgents {
		t.Errorf("the cursor stayed in an empty section: %v", l.section)
	}
}

// TestPlanRowFits: a narrow pane drops the state first, then the size whole.
func TestPlanRowFits(t *testing.T) {
	t.Parallel()
	st := plansModel(t).st
	r := planRow{plan: aPlan("doc-sso", "sso", 1, 4)}
	for width, want := range map[int]string{
		30: "  sso draft 4 tasks",
		16: "  sso 4 tasks",
		10: "  sso",
	} {
		if got := strings.TrimRight(planLine(st, r, false, false, false, width), " "); got != want {
			t.Errorf("%d: %q, want %q", width, got, want)
		}
	}
	child := planRow{plan: aPlan("doc-a", "auth-a", 1, 2), child: true}
	if got := strings.TrimRight(planLine(st, child, false, false, false, 30), " "); got != "    auth-a draft 2 tasks" {
		t.Errorf("a plan under its plan of plans: %q", got)
	}
}

// TestPlanFilter: a plan of plans shows with a plan of it that matches, and all of
// them when it matches itself, folded or not.
func TestPlanFilter(t *testing.T) {
	t.Parallel()
	m := plansModel(t)
	keys(m, "left") // folded
	l := m.list()
	m.moveToPlan(1) // sso
	l.filter.text = "auth-a"
	m.keepCursors()
	if l.planCursor != "doc-a" { // the row that took sso's
		t.Errorf("the cursor stayed on a plan not shown: %q", l.planCursor)
	}
	rows := m.planRows()
	if len(rows) != 2 || rows[0].plan.Name != "auth" || rows[0].match || rows[1].plan.Name != "auth-a" || !rows[1].match {
		t.Fatalf("a child matching: %+v", rows)
	}
	if m.filterMatches() != 1 {
		t.Errorf("matches %d", m.filterMatches())
	}
	a, b := aPlan("doc-a", "auth-a", 1, 2), aPlan("doc-b", "auth-b", 1, 1)
	setPlans(m, aParent("doc-auth", "login", &b, &a), a, b, aPlan("doc-sso", "sso", 2, 4))
	l.filter.text = "login"
	if rows := m.planRows(); len(rows) != 3 || !rows[0].match || rows[1].match {
		t.Errorf("only the parent matching shows its plans: %+v", rows)
	}
	// On a plan of plans shown only for its plan that matches, the cursor goes to
	// that plan.
	l.filter.text = ""
	m.keepCursors()
	m.moveToPlan(0)
	l.filter.text = "auth-a"
	m.filterChanged()
	if l.planCursor != "doc-a" {
		t.Errorf("left on a row shown as a parent: %q", l.planCursor)
	}
	m.moveCursor(0) // an agent, which does not match
	l.filter.text = "sso"
	m.filterChanged()
	if l.section != sectionPlans || l.planCursor != "doc-sso" {
		t.Errorf("not put on the match: %v %q", l.section, l.planCursor)
	}
	if rows := m.planRows(); len(rows) != 1 || rows[0].plan.Name != "sso" || !m.cursorOnMatch() {
		t.Errorf("a plan alone: %+v", rows)
	}
}

func TestPlanTab(t *testing.T) {
	t.Parallel()
	m := plansModel(t)
	keys(m, "down", "down", "down") // sso
	keys(m, "enter")
	l := m.list()
	if l.active != planTab("doc-sso") || !m.planShown() {
		t.Fatalf("tab %q", l.active)
	}
	if m.layout.focus != project {
		t.Errorf("the focus left the list: %v", m.layout.focus)
	}
	if !strings.Contains(titleRow(m), "[plan: sso r2") {
		t.Errorf("title %q", titleRow(m))
	}
	wantText(t, m, "loading…", "◂")
	tasks := []api.PlanTask{
		{Id: "schema", After: []string{}, Prompt: "Add the table.\nWith an index.\n"},
		{Id: "api", After: []string{"schema"}, Prompt: "Use it."},
	}
	loadPlan(m, "doc-sso", api.PlanContent{Plan: aPlan("doc-sso", "sso", 2, 2), Revision: 2,
		Definition: api.PlanDefinition{Goal: "Keep sessions.", Constraints: []string{"No new API."}, Tasks: &tasks}})
	wantText(t, m, "goal:", "Keep sessions.", "No new API.", "schema:", "waits for nothing", "api:", "after schema")
	if strings.Contains(text(m), "Add the table") {
		t.Errorf("a prompt shows before it is asked for:\n%s", text(m))
	}

	keys(m, "alt+right") // to the tab
	wantText(t, m, "Enter show the prompt")
	keys(m, "enter")
	wantText(t, m, "Add the table.", "With an index.", "Enter hide the prompt")
	keys(m, "enter")
	if strings.Contains(text(m), "Add the table") {
		t.Errorf("the prompt still shows:\n%s", text(m))
	}
	keys(m, "down", "enter")
	wantText(t, m, "Use it.")

	// A click on a task does as Enter: lines are goal, constraints, state,
	// revision, a gap, then schema.
	m.values.click(m.planTabRows(), 6, 5, 80, 40, m.st)
	wantText(t, m, "Add the table.")

	// The pane starts again on another plan's tab.
	m.layout.setFocus(project)
	m.moveToPlan(0)
	keys(m, "enter")
	if m.values.owner != planTab("doc-auth") {
		t.Errorf("the pane's owner is %q", m.values.owner)
	}
}

func TestPlanTabParentAndError(t *testing.T) {
	t.Parallel()
	m := plansModel(t)
	m.moveToPlan(2) // auth-a
	keys(m, "enter")
	a := m.plans()[1]
	loadPlan(m, "doc-a", api.PlanContent{Plan: a, Revision: 1, Definition: api.PlanDefinition{Goal: "g", Constraints: []string{}, Tasks: &[]api.PlanTask{}}})
	wantText(t, m, "parent:", "auth")
	m.moveToPlan(3) // sso
	keys(m, "enter")
	m.Update(planLoaded{projectID: "proj-demo", tab: planTab("doc-sso"), err: errors.New("daemon gone")})
	wantText(t, m, "error:", "daemon gone")
}

// TestPlanOfPlansTab: Enter opens a plan of plans' tab, where Enter on a plan opens
// that plan's.
func TestPlanOfPlansTab(t *testing.T) {
	t.Parallel()
	m := plansModel(t)
	keys(m, "enter")
	l := m.list()
	if l.active != planTab("doc-auth") {
		t.Fatalf("tab %q", l.active)
	}
	children := []api.PlanChild{{Id: "auth-b", After: []string{}}, {Id: "auth-a", After: []string{"auth-b"}}}
	loadPlan(m, "doc-auth", api.PlanContent{Plan: m.plans()[0], Revision: 1, Definition: api.PlanDefinition{Goal: "Rewrite auth.", Constraints: []string{}, Plans: &children}})
	wantText(t, m, "auth-b:", "auth-a:", "after auth-b")
	keys(m, "alt+right")
	wantText(t, m, "Enter open the plan")
	keys(m, "down", "enter")
	if l.active != planTab("doc-a") {
		t.Errorf("Enter on auth-a opened %q", l.active)
	}

	// A click on a row opens its tab; on a plan of plans' fold mark, it folds.
	s := &projectScreen{m: m}
	rowOf := func(i int) int {
		shown, _ := m.visibleItems(100)
		return slices.IndexFunc(shown, func(it paneItem) bool { return it.section == sectionPlans && it.row == i }) + 1
	}
	s.click(6, rowOf(3), 31, 100)
	if l.active != planTab("doc-sso") {
		t.Errorf("after a click: tab %q", l.active)
	}
	s.click(2, rowOf(0), 31, 100)
	if !l.plansFolded["doc-auth"] || l.active != planTab("doc-sso") {
		t.Errorf("a click on the fold mark: folded %v, tab %q", l.plansFolded, l.active)
	}
	s.click(6, rowOf(0), 31, 100)
	if l.active != planTab("doc-auth") {
		t.Errorf("a click on a plan of plans: tab %q", l.active)
	}
}

// TestPlanReadsAgain: a plan's tab is read again for a new revision only.
func TestPlanReadsAgain(t *testing.T) {
	t.Parallel()
	m := plansModel(t)
	keys(m, "enter")
	loadPlan(m, "doc-auth", api.PlanContent{Plan: m.plans()[0], Revision: 1, Definition: api.PlanDefinition{Constraints: []string{}, Plans: &[]api.PlanChild{}}})
	l := m.list()
	v := l.planViews[planTab("doc-auth")]
	m.Update(nil)
	if v.loading {
		t.Fatal("read again with nothing changed")
	}
	plans := slices.Clone(m.plans())
	plans[0].Revision = 2
	setPlans(m, plans...)
	m.Update(nil)
	if !v.loading || !strings.Contains(titleRow(m), "[plan: auth r2") {
		t.Errorf("not read again on a new revision: title %q", titleRow(m))
	}
}

func TestPlanRemovedClosesItsTab(t *testing.T) {
	t.Parallel()
	m := plansModel(t)
	keys(m, "enter")
	l := m.list()
	p := *m.following.project
	p.Plans = &[]api.Plan{aPlan("doc-sso", "sso", 2, 4)}
	m.Update(projectEvent{gen: m.following.gen, event: client.ProjectEvent{Project: p}})
	if slices.Contains(l.tabs, planTab("doc-auth")) || l.planViews[planTab("doc-auth")] != nil {
		t.Errorf("tabs %v", l.tabs)
	}
	if m.statusNotice != "plan auth removed" {
		t.Errorf("notice %q", m.statusNotice)
	}
	if l.planCursor != "doc-sso" {
		t.Errorf("the cursor is on %q", l.planCursor)
	}

	// A daemon that sends no plans closes none.
	l.openTab(planTab("doc-sso"))
	m.following.project.Plans = nil
	m.plansChanged()
	if !slices.Contains(l.tabs, planTab("doc-sso")) {
		t.Errorf("closed with no plans sent: %v", l.tabs)
	}
}

func TestPlanLive(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	h.newProject(t, "app", api.ProjectStateOpen)
	ctx, c, app := context.Background(), h.client(), "app"
	if _, err := c.CreatePlan(ctx, api.CreatePlan{Project: &app, Name: "sso", Content: "goal: One.\ntasks:\n  - id: a\n    prompt: p\n"}); err != nil {
		t.Fatal(err)
	}

	p := h.follow(t)
	p.until(t, "the projects listed", func(m *Model) bool { return m.projects.loaded })
	p.keys("enter")
	p.until(t, "the plan listed", func(m *Model) bool { return len(m.planRows()) == 1 })
	m := p.m
	m.layout.setFocus(project)
	m.moveToPlan(0)
	p.keys("enter")
	p.until(t, "the plan's goal", func(m *Model) bool { return strings.Contains(text(m), "One.") })

	if _, err := c.UpdatePlanContent(ctx, "sso", app, "", 1, "goal: Two.\ntasks:\n  - id: a\n    prompt: p\n"); err != nil {
		t.Fatal(err)
	}
	p.until(t, "revision 2", func(m *Model) bool { return strings.Contains(text(m), "Two.") })
}
