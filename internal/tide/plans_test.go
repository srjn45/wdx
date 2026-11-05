package tide

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/runtest"
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
	child := planRow{plan: aPlan("doc-a", "auth-a", 1, 2), child: true, place: treePlace{depth: 1, guide: "  "}}
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
	wantText(t, m, "sso", "○ draft", "rev 2", "2 tasks, not run yet", "goal and 1 constraint", "schema", "api", "r run  y copy  ↑↓ select",
		"─ schema ", "Add the table.")

	// Enter on a task of a draft shows its whole prompt, Esc goes back.
	keys(m, "alt+right", "down", "enter")
	if l.active != planTab("doc-sso") || m.taskBox != nil {
		t.Errorf("Enter on a draft's task: tab %q, box %+v", l.active, m.taskBox)
	}
	wantText(t, m, "Prompt", "Use it.", "Esc back")
	if strings.Contains(text(m), "r run") {
		t.Errorf("the list shows under the prompt:\n%s", text(m))
	}
	keys(m, "esc")
	wantText(t, m, "r run  y copy  ↑↓ select  Enter read it")
	keys(m, "right")
	wantText(t, m, "Keep sessions.", "No new API.", "← constraints")

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
	wantText(t, m, "runs with auth  y copy")
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
	children := []api.PlanChild{{Id: "auth-b", After: []string{}, State: api.PlanStateDraft, Tasks: 1}, {Id: "auth-a", After: []string{"auth-b"}, State: api.PlanStateDraft, Tasks: 2}}
	loadPlan(m, "doc-auth", api.PlanContent{Plan: m.plans()[0], Revision: 1, Definition: api.PlanDefinition{Goal: "Rewrite auth.", Constraints: []string{}, Plans: &children}})
	wantText(t, m, "2 plans, not run yet", "─ Plans ", "auth-b ready   1 task", "auth-a waiting 2 tasks", "Unblocks", "○ auth-a")
	keys(m, "alt+right")
	wantText(t, m, "Enter open it")
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

// TestPlanReadsAgain: a plan's tab is read again when the plan changes, which moves
// its updated_at on: a new revision, or a step of its run.
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
	plans[0].Revision, plans[0].UpdatedAt = 2, plans[0].UpdatedAt.Add(time.Millisecond)
	setPlans(m, plans...)
	m.Update(nil)
	if !v.loading || !strings.Contains(titleRow(m), "[plan: auth r2") {
		t.Errorf("not read again on a new revision: title %q", titleRow(m))
	}
	loadPlan(m, "doc-auth", api.PlanContent{Plan: plans[0], Revision: 2, Definition: api.PlanDefinition{Constraints: []string{}, Plans: &[]api.PlanChild{}}})
	plans = slices.Clone(plans)
	plans[0].UpdatedAt = plans[0].UpdatedAt.Add(time.Millisecond)
	setPlans(m, plans...)
	m.Update(nil)
	if !v.loading {
		t.Error("not read again on a step of its run")
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
	p.until(t, "the plan's tasks", func(m *Model) bool { return strings.Contains(text(m), "1 task, not run yet") })

	if _, err := c.UpdatePlanContent(ctx, "sso", app, "", 1, "goal: Two.\ntasks:\n  - id: a\n    prompt: p\n  - id: b\n    prompt: p\n"); err != nil {
		t.Fatal(err)
	}
	p.until(t, "revision 2", func(m *Model) bool { return strings.Contains(text(m), "2 tasks, not run yet") })
}

// TestPlanRunShown: a running plan's row says its state and progress, and its tab
// each task's state and agent; Enter on a running task goes to its agent, on a
// done one shows its whole prompt and summary.
func TestPlanRunShown(t *testing.T) {
	t.Parallel()
	m := plansModel(t)
	sso := aPlan("doc-sso", "sso", 2, 4)
	sso.State, sso.TasksDone = api.PlanStateRunning, optionalInt(1)
	plans := slices.Clone(m.plans())
	plans[3] = sso
	setPlans(m, plans...)
	wantText(t, m, "sso running 1/4")

	m.moveToPlan(3)
	keys(m, "enter")
	summary, failure := "Added it.", api.PlanTaskFailureRebase
	tasks := []api.PlanTask{
		{Id: "schema", After: []string{}, Prompt: "Add the table.", State: api.PlanTaskStateDone, WaitingFor: []string{}, Summary: &summary, Agent: &api.PlanTaskAgent{Id: "agent-s", Name: "sso-schema"}},
		{Id: "api", After: []string{"schema"}, Prompt: "Use it.", State: api.PlanTaskStateRunning, WaitingFor: []string{}, Agent: &api.PlanTaskAgent{Id: "agent-a", Name: "sso-api"}},
		{Id: "cli", After: []string{"schema"}, Prompt: "p", State: api.PlanTaskStateLandFailed, WaitingFor: []string{}, Failure: &failure, Conflicts: &[]string{"x.go"}},
		{Id: "docs", After: []string{"api", "cli"}, Prompt: "p", State: api.PlanTaskStatePending, WaitingFor: []string{"api", "cli"}},
	}
	loadPlan(m, "doc-sso", api.PlanContent{Plan: sso, Revision: 2, Run: &api.PlanRun{State: api.PlanStateRunning, Branch: "plan/sso"},
		Definition: api.PlanDefinition{Goal: "g", Constraints: []string{}, Tasks: &tasks}})
	wantText(t, m, "● running", "1/4 tasks · 1 failed · on plan/sso", "done", "running", "land failed: rebase", "waiting", "→ goal")
	if strings.Contains(text(m), "2562047h") {
		t.Errorf("a run with no start says how long it ran:\n%s", text(m))
	}

	keys(m, "alt+right", "r")
	if m.confirm != nil {
		t.Error("r asks to run a plan that runs")
	}
	m.Update(planRan{r: removal{kind: runPlanKind, id: "doc-sso", name: "sso"}, err: errors.New("plan sso has run")})
	if m.statusNotice != "plan sso has run" {
		t.Errorf("a refusal: notice %q", m.statusNotice)
	}
	keys(m, "enter")
	if l := m.list(); l.active != planTab("doc-sso") {
		t.Errorf("Enter on a done task: tab %q", l.active)
	}
	wantText(t, m, "schema  ✓ done · sso-schema", "Prompt", "Add the table.", "Summary", "Added it.")
	keys(m, "esc", "down")
	wantText(t, m, "Enter go to its agent")
	keys(m, "enter")
	if l := m.list(); l.active != "agent-a" {
		t.Errorf("Enter on a running task: tab %q", l.active)
	}
}

func optionalInt(n int64) *int64 { return &n }

// TestPlanNeedsYou: a plan with a task that failed, or whose agent waits for
// input, says it needs the person.
func TestPlanNeedsYou(t *testing.T) {
	t.Parallel()
	m := plansModel(t)
	sso := aPlan("doc-sso", "sso", 2, 4)
	sso.State, sso.TasksDone, sso.TasksFailed = api.PlanStateRunning, optionalInt(1), optionalInt(0)
	plans := slices.Clone(m.plans())
	plans[3] = sso
	setPlans(m, plans...)
	wantText(t, m, "sso running 1/4 ")
	if strings.Contains(text(m), "needs you") {
		t.Error("a plan going well needs the person")
	}
	working, waiting := api.AgentActivityWorking, api.AgentActivityWaiting
	agents := []api.Agent{{Id: "agent-a", Name: "sso-api", Activity: &working, Task: &api.AgentTask{Plan: "SSO", Task: "api"}}}
	m.following.project.Agents = &agents
	if strings.Contains(text(m), "needs you") {
		t.Error("a plan whose agent works needs the person")
	}
	agents[0].Activity = &waiting
	wantText(t, m, "sso running 1/4 needs you")
	m.following.project.Agents = nil
	sso.TasksFailed = optionalInt(1)
	plans[3] = sso
	setPlans(m, plans...)
	wantText(t, m, "sso running 1/4 needs you")
}

// TestPlanTabAnswers: a running plan's tab says its keys, and a failed or
// land_failed task's Enter opens the box of its answers.
func TestPlanTabAnswers(t *testing.T) {
	t.Parallel()
	m := plansModel(t)
	sso := aPlan("doc-sso", "sso", 2, 2)
	sso.State, sso.TasksDone, sso.TasksFailed = api.PlanStateRunning, optionalInt(0), optionalInt(2)
	plans := slices.Clone(m.plans())
	plans[3] = sso
	setPlans(m, plans...)
	m.moveToPlan(3)
	keys(m, "enter")
	ended, check := api.PlanTaskFailureAgentEnded, api.PlanTaskFailureCheck
	checkName := "land-sso-1"
	tasks := []api.PlanTask{
		{Id: "api", After: []string{}, Prompt: "p", State: api.PlanTaskStateFailed, WaitingFor: []string{}, Failure: &ended},
		{Id: "cli", After: []string{}, Prompt: "p", State: api.PlanTaskStateLandFailed, WaitingFor: []string{}, Failure: &check, CheckTerminal: &checkName},
	}
	content := api.PlanContent{Plan: sso, Revision: 2, Run: &api.PlanRun{State: api.PlanStateRunning, Branch: "plan/sso"},
		Definition: api.PlanDefinition{Goal: "g", Constraints: []string{}, Tasks: &tasks}}
	loadPlan(m, "doc-sso", content)
	wantText(t, m, "● running", "0/2 tasks · 2 failed · on plan/sso", "p pause  c cancel  y copy",
		"failed: agent ended", "land failed: check", "needs you")

	keys(m, "alt+right")
	wantText(t, m, "Enter answer it")
	keys(m, "enter")
	wantText(t, m, "Task api", "r retry", "f retry with a new agent", "s skip it")
	if strings.Contains(text(m), "l land it again") || strings.Contains(text(m), "t show the check") {
		t.Errorf("a failed task offers what only a land_failed one has:\n%s", text(m))
	}
	terms := []api.Terminal{{Id: "term-x", Name: "shell"}, {Id: "term-c", Name: checkName}}
	m.following.project.Terminals = &terms
	keys(m, "l", "t")
	if m.taskBox == nil || m.taskBox.busy {
		t.Fatalf("l or t did something for a failed task: %+v", m.taskBox)
	}
	retries := retriesTo(t, m)
	for _, c := range []struct {
		key   string
		fresh bool
	}{{"f", true}, {"r", false}} {
		keys(m, c.key)
		if got := <-retries; got != c.fresh {
			t.Errorf("%s retried with fresh %v", c.key, got)
		}
		if m.taskBox != nil {
			t.Errorf("%s left the box open", c.key)
		}
		keys(m, "enter")
	}
	keys(m, "esc")
	if m.taskBox != nil {
		t.Fatal("Esc kept the box")
	}

	keys(m, "down", "enter")
	wantText(t, m, "Task cli", "l land it again", "t show the check's output")
	keys(m, "t")
	if got, ok := m.selectedTerminal(); m.taskBox != nil || !ok || got.Name != checkName || m.layout.focus != terminal {
		t.Errorf("t: box %+v, terminal %+v, focus %v", m.taskBox, got, m.layout.focus)
	}

	m.Update(planAnswered{what: "retry", planID: "doc-sso", task: "api", err: errors.New("task api is running")})
	if m.statusNotice != "task api is running" {
		t.Errorf("a refusal: notice %q", m.statusNotice)
	}

	paused := sso
	paused.State = api.PlanStatePaused
	closedReason := api.PlanRunPausedReasonProjectClosed
	content.Plan, content.Run = paused, &api.PlanRun{State: api.PlanStatePaused, Branch: "plan/sso", PausedReason: &closedReason}
	loadPlan(m, "doc-sso", content)
	wantText(t, m, "⏸ paused (project closed)", " c cancel  ↑↓ select")
	m.layout.setFocus(agent)
	if _, took := m.planKey(tea.KeyPressMsg{Code: 'p', Text: "p"}); took {
		t.Error("p takes a plan its closed project pauses")
	}
	keys(m, "c")
	wantText(t, m, "Cancel plan", "Cancel sso?", "y cancel it")
	keys(m, "n")
	if m.confirm != nil {
		t.Fatal("still asking")
	}
}

// retriesTo points m's client at a daemon of its own, which answers a task's
// retry and sends whether it was fresh.
func retriesTo(t *testing.T, m *Model) <-chan bool {
	t.Helper()
	socket := filepath.Join(runtest.Dir(t), "wdx.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	retries := make(chan bool, 4)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body api.RetryPlanTask
		_ = json.NewDecoder(r.Body).Decode(&body)
		retries <- body.Fresh != nil && *body.Fresh
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.PlanTaskResult{Result: api.ResultRetried, Task: api.PlanTask{Id: "api", After: []string{}, WaitingFor: []string{}, State: api.PlanTaskStatePending}})
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { srv.Close() })
	m.client = client.New(client.Options{Socket: socket, DebugID: "dbg-0123456789ab"})
	return retries
}
