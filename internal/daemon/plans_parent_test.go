package daemon

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/gh/ghtest"
	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/telemetry"
)

const ssoPlan = "goal: g\ntasks:\n  - id: s\n    prompt: p\n"
const uiPlan = "goal: g\ntasks:\n  - id: u\n    prompt: p\n"
const bigPlan = "goal: g\nplans:\n  - id: sso\n  - id: ui\n    after: [sso]\n"

// children says what each plan a plan of plans lists is in, as "sso:done ui:draft",
// a draft waiting for the person as "ui:waiting".
func children(c api.PlanContent) string {
	var list []string
	for _, p := range *c.Definition.Plans {
		state := string(p.State)
		if p.Waiting {
			state = "waiting"
		}
		list = append(list, p.Id+":"+state)
	}
	return strings.Join(list, " ")
}

func childrenAre(want string) func(api.PlanContent) bool {
	return func(c api.PlanContent) bool { return children(c) == want }
}

// TestPlanOfPlansRuns: a plan of plans starts what waits for nothing; the plan
// after waits for the person until the one before has landed through wdx, then
// starts by itself; pausing, resuming and cancelling it reach its plans.
func TestPlanOfPlansRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	repo := td.planProject(t, "")
	td.createPlan(t, "app", "sso", ssoPlan)
	td.createPlan(t, "app", "ui", uiPlan)
	td.createPlan(t, "app", "big", bigPlan)
	if r, err := td.client.SetProjectLandingWithResponse(ctx, "app", api.ProjectLandingUpdate{}); err != nil || r.JSON200 == nil {
		t.Fatalf("landing settings: %v, %s", err, r.Body)
	}
	if c := td.waitPlan(t, "big", "a draft", func(api.PlanContent) bool { return true }); children(c) != "sso:draft ui:draft" || c.Plan.PlansDone != nil {
		t.Errorf("a draft's plans: %s, %+v", children(c), c.Plan)
	}

	p := td.runPlan(t, "big", 0)
	if p.State != api.PlanStateRunning || p.PlansDone == nil || *p.PlansDone != 0 || p.TasksDone != nil {
		t.Errorf("started: %+v", p)
	}
	c := td.waitPlan(t, "big", "sso running", childrenAre("sso:running ui:draft"))
	if c.Run == nil || c.Run.Branch != "" || c.Run.Base != "main" {
		t.Errorf("its run: %+v", c.Run)
	}
	two := 2
	set, err := td.client.UpdatePlanWithResponse(ctx, "big", &api.UpdatePlanParams{Project: optional("app")}, api.UpdatePlan{Parallel: &two})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, set.ApplicationproblemJSONDefault, http.StatusUnprocessableEntity, api.ErrorCodeInvalidArgument)
	td.finishTask(t, repo, "sso", "s")
	c = td.waitPlan(t, "big", "ui waiting for the person", childrenAre("sso:done ui:waiting"))
	if c.Plan.PlansWaiting == nil || !slices.Equal(*c.Plan.PlansWaiting, []string{"ui"}) {
		t.Errorf("waiting, as read: %v", c.Plan.PlansWaiting)
	}
	td.waitProject(t, "app", "the stream saying ui waits", func(e event) bool { return slices.Equal(planWaiting(e, "big"), []string{"ui"}) })
	if *c.Plan.PlansDone != 1 || c.Plan.State != api.PlanStateRunning {
		t.Errorf("sso done: %+v", c.Plan)
	}

	on := true
	events, leave := td.followWith(t, "app", &api.StreamProjectParams{Worktrees: &on})
	waitEvent(t, events, "sso's worktree", func(e event) bool { _, ok := worktreeStatus(e, "sso-1"); return ok })
	land, err := td.client.LandWorktreeWithResponse(ctx, "sso-1", &api.LandWorktreeParams{Project: optional("app")})
	if err != nil || land.JSON202 == nil {
		t.Fatalf("land sso: %v, %s", err, land.Body)
	}
	td.waitPlan(t, "big", "ui running", childrenAre("sso:done ui:running"))
	td.waitProject(t, "app", "the stream saying none waits", func(e event) bool { w := planWaiting(e, "big"); return w != nil && len(w) == 0 })
	if got := gitOut(t, repo, "show", "main:s.txt"); got != "s" {
		t.Errorf("s.txt on main: %q", got)
	}

	pause, err := td.client.PausePlanWithResponse(ctx, "big", &api.PausePlanParams{Project: optional("app")})
	if err != nil || pause.JSON200 == nil || pause.JSON200.Result != api.ResultPaused {
		t.Fatalf("pause: %v, %s", err, pause.Body)
	}
	td.waitPlan(t, "big", "ui paused", childrenAre("sso:done ui:paused"))
	resume, err := td.client.ResumePlanWithResponse(ctx, "big", &api.ResumePlanParams{Project: optional("app")})
	if err != nil || resume.JSON200 == nil || resume.JSON200.Result != api.ResultResumed {
		t.Fatalf("resume: %v, %s", err, resume.Body)
	}
	td.waitPlan(t, "big", "ui running again", childrenAre("sso:done ui:running"))

	cancel, err := td.client.CancelPlanWithResponse(ctx, "big", &api.CancelPlanParams{Project: optional("app")})
	if err != nil || cancel.JSON200 == nil || cancel.JSON200.Result != api.ResultCancelled {
		t.Fatalf("cancel: %v, %s", err, cancel.Body)
	}
	td.waitPlan(t, "big", "closed, ui cancelled", func(c api.PlanContent) bool {
		return c.Run.FinishedAt != nil && children(c) == "sso:done ui:cancelled"
	})
	td.waitPlan(t, "ui", "ui closed", func(c api.PlanContent) bool { return c.Run.FinishedAt != nil })
	leave()
	wantRequestsLogged(t, td.home)
}

// TestPlanOfPlansFinishes: once every plan it lists is done, a plan of plans is
// done, the last one's work not yet landed.
func TestPlanOfPlansFinishes(t *testing.T) {
	t.Parallel()
	td := start(t)
	repo := td.planProject(t, "")
	td.createPlan(t, "app", "sso", ssoPlan)
	td.createPlan(t, "app", "big", "goal: g\nplans:\n  - id: sso\n")
	td.runPlan(t, "big", 0)
	td.waitPlan(t, "big", "sso running", childrenAre("sso:running"))
	td.finishTask(t, repo, "sso", "s")
	c := td.waitPlan(t, "big", "done", func(c api.PlanContent) bool { return c.Plan.State == api.PlanStateDone })
	if *c.Plan.PlansDone != 1 || c.Run.FinishedAt == nil {
		t.Errorf("done: %+v", c.Plan)
	}
	wantRequestsLogged(t, td.home)
}

// TestPlanOfPlansRefused: a plan of plans takes no parallel, runs only with every
// branch of its plans free, and a plan it lists does not run alone.
func TestPlanOfPlansRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	repo := td.planProject(t, "")
	td.createPlan(t, "app", "sso", ssoPlan)
	td.createPlan(t, "app", "ui", uiPlan)
	td.createPlan(t, "app", "big", bigPlan)
	run := func(name string, parallel *int) *api.Problem {
		t.Helper()
		r, err := td.client.RunPlanWithResponse(ctx, name, &api.RunPlanParams{Project: optional("app")}, api.RunPlan{Parallel: parallel})
		if err != nil {
			t.Fatal(err)
		}
		return r.ApplicationproblemJSONDefault
	}
	two := 2
	wantProblem(t, run("big", &two), http.StatusUnprocessableEntity, api.ErrorCodeInvalidArgument)
	wantProblem(t, run("ui", nil), http.StatusConflict, api.ErrorCodePlanInUse)
	gittest.Git(t, repo, "branch", "plan/ui")
	p := run("big", nil)
	wantProblem(t, p, http.StatusConflict, api.ErrorCodeBranchExists)
	if p != nil && !strings.Contains(p.Detail, "plan/ui") {
		t.Errorf("the branch in the way is not named: %s", p.Detail)
	}
	got, err := td.client.GetPlanWithResponse(ctx, "big", &api.GetPlanParams{Project: optional("app")})
	if err != nil || got.JSON200 == nil {
		t.Fatalf("get: %v, %s", err, got.Body)
	}
	if c := *got.JSON200; c.Plan.State != api.PlanStateDraft || children(c) != "sso:draft ui:draft" {
		t.Errorf("refused, it ran: %+v, %s", c.Plan, children(c))
	}
	wantRequestsLogged(t, td.home)
}

// TestArchivedPlanOfPlansListed: archiving a cancelled plan of plans archives its
// plans that are over, and listPlans leaves out its draft too, unless asked.
func TestArchivedPlanOfPlansListed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.planProject(t, "")
	td.createPlan(t, "app", "sso", ssoPlan)
	td.createPlan(t, "app", "ui", uiPlan)
	td.createPlan(t, "app", "big", bigPlan)
	td.runPlan(t, "big", 0)
	td.waitPlan(t, "big", "sso running", childrenAre("sso:running ui:draft"))
	if r, err := td.client.CancelPlanWithResponse(ctx, "big", &api.CancelPlanParams{Project: optional("app")}); err != nil || r.JSON200 == nil {
		t.Fatalf("cancel: %v, %s", err, r.Body)
	}
	td.waitPlan(t, "big", "closed", func(c api.PlanContent) bool { return c.Run.FinishedAt != nil })
	td.waitPlan(t, "sso", "sso closed", func(c api.PlanContent) bool { return c.Run.FinishedAt != nil })
	if r, err := td.client.ArchivePlanWithResponse(ctx, "big", &api.ArchivePlanParams{Project: optional("app")}); err != nil || r.JSON200 == nil || r.JSON200.Result != api.ResultArchived {
		t.Fatalf("archive: %v, %s", err, r.Body)
	}
	listed := func(archived bool) string {
		t.Helper()
		r, err := td.client.ListPlansWithResponse(ctx, &api.ListPlansParams{Project: optional("app"), Archived: &archived})
		if err != nil || r.JSON200 == nil {
			t.Fatalf("list: %v, %s", err, r.Body)
		}
		var names []string
		for _, p := range r.JSON200.Plans {
			names = append(names, p.Name+":"+string(p.State)+map[bool]string{true: ",archived"}[p.ArchivedAt != nil])
		}
		return strings.Join(names, " ")
	}
	if got := listed(false); got != "" {
		t.Errorf("listed: %q", got)
	}
	if got := listed(true); got != "big:cancelled,archived sso:cancelled,archived ui:draft" {
		t.Errorf("listed with the archived: %q", got)
	}
	wantRequestsLogged(t, td.home)
}

// TestPlanOfPlansChildNotStarted: a plan its parent fails to start, as a branch was
// made in its way since the run began, waits for the person rather than being
// tried again at every step, and starts on their answer.
func TestPlanOfPlansChildNotStarted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	repo := td.planProject(t, "")
	td.createPlan(t, "app", "sso", ssoPlan)
	td.createPlan(t, "app", "ui", uiPlan)
	td.createPlan(t, "app", "big", bigPlan)
	if r, err := td.client.SetProjectLandingWithResponse(ctx, "app", api.ProjectLandingUpdate{}); err != nil || r.JSON200 == nil {
		t.Fatalf("landing settings: %v, %s", err, r.Body)
	}
	td.runPlan(t, "big", 0)
	td.waitPlan(t, "big", "sso running", childrenAre("sso:running ui:draft"))
	gittest.Git(t, repo, "branch", "plan/ui")
	td.finishTask(t, repo, "sso", "s")
	td.waitPlan(t, "big", "ui waiting", childrenAre("sso:done ui:waiting"))

	on := true
	events, leave := td.followWith(t, "app", &api.StreamProjectParams{Worktrees: &on})
	waitEvent(t, events, "sso's worktree", func(e event) bool { _, ok := worktreeStatus(e, "sso-1"); return ok })
	if land, err := td.client.LandWorktreeWithResponse(ctx, "sso-1", &api.LandWorktreeParams{Project: optional("app")}); err != nil || land.JSON202 == nil {
		t.Fatalf("land sso: %v, %s", err, land.Body)
	}
	waitEvent(t, events, "sso landed", func(e event) bool { w, ok := worktreeStatus(e, "sso-1"); return ok && w.Ahead != nil && *w.Ahead == 0 })
	c := td.waitPlan(t, "big", "ui still waiting, its start failed", func(c api.PlanContent) bool {
		return children(c) == "sso:done ui:waiting" && td.logged(t, "plan not started by its parent: it waits for the person")
	})
	if c.Plan.State != api.PlanStateRunning {
		t.Errorf("the parent: %+v", c.Plan)
	}
	gittest.Git(t, repo, "branch", "-q", "-D", "plan/ui")
	if r, err := td.client.RunPlanWithResponse(ctx, "ui", &api.RunPlanParams{Project: optional("app")}, api.RunPlan{}); err != nil || r.JSON200 == nil {
		t.Fatalf("the answer, the branch gone: %v, %s", err, r.Body)
	}
	td.waitPlan(t, "big", "ui running", childrenAre("sso:done ui:running"))
	leave()
	wantRequestsLogged(t, td.home)
}

// logged reports whether the daemon logged a line saying msg.
func (td *testDaemon) logged(t *testing.T, msg string) bool {
	t.Helper()
	for _, line := range logLines(t, td.home) {
		if line["msg"] == msg {
			return true
		}
	}
	return false
}

// TestPlanOfPlansAnswered: running a plan its parent lists is the person's answer,
// refused while the plan before it is not done, then while its work is neither in
// main nor merged on GitHub, then while merged but not pulled; once the squash
// merge is in main, it starts. A paused parent takes no answer, and only an
// answer that needs it asks gh.
func TestPlanOfPlansAnswered(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fake := ghtest.New(t, t.TempDir())
	td := startOn(t, shortDir(t), telemetry.Options{}, func(o *Options) { o.GH = fake.Program })
	repo := td.planProject(t, "")
	td.createPlan(t, "app", "sso", ssoPlan)
	td.createPlan(t, "app", "ui", uiPlan)
	td.createPlan(t, "app", "big", bigPlan)
	runUI := func() api.RunPlanResponse {
		t.Helper()
		r, err := td.client.RunPlanWithResponse(ctx, "ui", &api.RunPlanParams{Project: optional("app")}, api.RunPlan{})
		if err != nil {
			t.Fatal(err)
		}
		return *r
	}
	refused := func(code api.ErrorCode, merged []any) {
		t.Helper()
		r := runUI()
		wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusConflict, code)
		if r.ApplicationproblemJSONDefault == nil {
			return
		}
		d := *r.ApplicationproblemJSONDefault.Details
		plans, _ := d["plans"].([]any)
		if len(plans) != 1 || plans[0].(map[string]any)["name"] != "sso" || d["base"] != "main" || !reflect.DeepEqual(d["merged"], merged) {
			t.Errorf("details: %v", d)
		}
	}

	td.runPlan(t, "big", 0)
	td.waitPlan(t, "big", "sso running", childrenAre("sso:running ui:draft"))
	refused(api.ErrorCodePlanNotFinished, []any{})
	td.finishTask(t, repo, "sso", "s")
	td.waitPlan(t, "big", "ui waiting", childrenAre("sso:done ui:waiting"))
	refused(api.ErrorCodePlanNotLanded, []any{})

	head := gitOut(t, repo, "rev-parse", "plan/sso")
	fake.Set(t, "OPEN", head)
	refused(api.ErrorCodePlanNotLanded, []any{})
	fake.SetMerged(t, head, "0123456789abcdef0123456789abcdef01234567")
	refused(api.ErrorCodePlanNotLanded, []any{"sso"})

	gittest.Git(t, repo, "merge", "-q", "--squash", "plan/sso")
	gittest.Git(t, repo, "commit", "-qm", "sso (#12)")
	fake.SetMerged(t, head, gitOut(t, repo, "rev-parse", "HEAD"))
	pause, err := td.client.PausePlanWithResponse(ctx, "big", &api.PausePlanParams{Project: optional("app")})
	if err != nil || pause.JSON200 == nil {
		t.Fatalf("pause: %v, %s", err, pause.Body)
	}
	wantProblem(t, runUI().ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodePlanInUse)
	if r, err := td.client.ResumePlanWithResponse(ctx, "big", &api.ResumePlanParams{Project: optional("app")}); err != nil || r.JSON200 == nil {
		t.Fatalf("resume: %v, %s", err, r.Body)
	}
	if r := runUI(); r.JSON200 == nil || r.JSON200.Result != api.ResultStarted || r.JSON200.Plan.State != api.PlanStateRunning {
		t.Fatalf("the answer: %s", r.Body)
	}
	td.waitPlan(t, "big", "ui running", childrenAre("sso:done ui:running"))
	if calls := fake.Calls(t); len(calls) != 4 || !strings.HasPrefix(calls[0], "pr view plan/sso") {
		t.Errorf("gh was asked %q", calls)
	}
	wantRequestsLogged(t, td.home)
}

// planWaiting returns what the event says of the plans the plan called name lists
// that wait for the person; nil when it says nothing of it.
func planWaiting(e event, name string) []string {
	if e.project.Plans == nil {
		return nil
	}
	for _, p := range *e.project.Plans {
		if p.Name == name && p.PlansWaiting != nil {
			return *p.PlansWaiting
		}
	}
	return nil
}
