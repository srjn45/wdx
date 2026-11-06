package daemon

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/gh/ghtest"
	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/telemetry"
)

const prPlan = "goal: g\npr: true\ntasks:\n  - id: a\n    prompt: p\n"

// prProject is planProject with an origin to push to, served by the gh fake, and
// following the project's worktrees.
func prProject(t *testing.T) (*testDaemon, *ghtest.Fake, string, <-chan event, func()) {
	t.Helper()
	fake := ghtest.New(t, t.TempDir())
	td := startOn(t, shortDir(t), telemetry.Options{}, func(o *Options) { o.GH = fake.Program })
	repo := td.planProject(t, "")
	origin := filepath.Join(t.TempDir(), "origin.git")
	gittest.Git(t, repo, "init", "-q", "--bare", origin)
	gittest.Git(t, repo, "remote", "add", "origin", origin)
	gittest.Git(t, repo, "push", "-q", "origin", "main")
	on := true
	events, leave := td.followWith(t, "app", &api.StreamProjectParams{Worktrees: &on})
	t.Cleanup(leave)
	return td, fake, repo, events, leave
}

// planLanding follows the stream until the landing of the worktree called name has
// ended.
func planLanding(t *testing.T, events <-chan event, name string) api.WorktreeLanding {
	t.Helper()
	var last api.WorktreeLanding
	waitEvent(t, events, name+"'s landing to end", func(e event) bool {
		w, ok := worktreeStatus(e, name)
		if !ok || w.Landing == nil {
			return false
		}
		last = *w.Landing
		return last.State == api.WorktreeLandingStateOpened || last.State == api.WorktreeLandingStateFailed || last.State == api.WorktreeLandingStateLanded
	})
	return last
}

// TestPlanOpensItsPR: a plan with pr: true, once done, lands its worktree by PR,
// though the project lands by rebase: the branch pushed and the PR opened against
// the default branch.
func TestPlanOpensItsPR(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td, fake, repo, events, leave := prProject(t)
	if r, err := td.client.SetProjectLandingWithResponse(ctx, "app", api.ProjectLandingUpdate{}); err != nil || r.JSON200 == nil || r.JSON200.Method != api.ProjectLandingMethodRebase {
		t.Fatalf("landing settings: %v, %s", err, r.Body)
	}
	td.createPlan(t, "app", "auth", prPlan)
	td.runPlan(t, "auth", 0)
	td.finishTask(t, repo, "auth", "a")

	l := planLanding(t, events, "auth-1")
	if l.State != api.WorktreeLandingStateOpened || l.PullRequest == nil || l.PullRequest.Number != 12 || l.Base != "main" {
		t.Fatalf("the landing: %+v", l)
	}
	if calls := fake.Calls(t); !slices.Contains(calls, "pr create --fill --base main --head plan/auth") {
		t.Errorf("gh was asked %q", calls)
	}
	origin := gitOut(t, repo, "remote", "get-url", "origin")
	if pushed, tip := gitOut(t, origin, "rev-parse", "plan/auth"), gitOut(t, repo, "rev-parse", "plan/auth"); pushed != tip {
		t.Errorf("origin has plan/auth at %s, not %s", pushed, tip)
	}
	if c := td.waitPlan(t, "auth", "the plan", func(api.PlanContent) bool { return true }); c.Plan.State != api.PlanStateDone {
		t.Errorf("the plan: %+v", c.Plan)
	}
	leave()
	wantRequestsLogged(t, td.home)
}

// TestPlanPRFails: a PR that gh refuses leaves the plan done, the worktree's
// landing saying why.
func TestPlanPRFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td, fake, repo, events, _ := prProject(t)
	if r, err := td.client.SetProjectLandingWithResponse(ctx, "app", api.ProjectLandingUpdate{}); err != nil || r.JSON200 == nil {
		t.Fatalf("landing settings: %v, %s", err, r.Body)
	}
	td.createPlan(t, "app", "auth", prPlan)
	td.runPlan(t, "auth", 0)
	fake.Refuse(t, "HTTP 401: Bad credentials")
	td.finishTask(t, repo, "auth", "a")

	l := planLanding(t, events, "auth-1")
	if l.State != api.WorktreeLandingStateFailed || l.Code == nil || *l.Code != string(api.ErrorCodePrFailed) {
		t.Fatalf("the landing: %+v", l)
	}
	if c := td.waitPlan(t, "auth", "the plan", func(api.PlanContent) bool { return true }); c.Plan.State != api.PlanStateDone {
		t.Errorf("the plan: %+v", c.Plan)
	}
}

// TestPlanPRNotStarted: a project that has not said how it lands cannot land the
// plan's worktree: the plan is done all the same, and the log says why.
func TestPlanPRNotStarted(t *testing.T) {
	t.Parallel()
	td, fake, repo, _, _ := prProject(t)
	td.createPlan(t, "app", "auth", prPlan)
	td.runPlan(t, "auth", 0)
	td.finishTask(t, repo, "auth", "a")
	td.waitPlan(t, "auth", "the plan done, its PR not started", func(c api.PlanContent) bool {
		return c.Plan.State == api.PlanStateDone && td.logged(t, "plan's PR landing not started: the plan stays done")
	})
	if calls := fake.Calls(t); len(calls) != 0 {
		t.Errorf("gh was asked %q", calls)
	}
}

// TestPlanOfPlansOpensItsPlansPRs: pr: true on a plan of plans has each plan it
// lists open its PR once done, as no plan it lists can set pr itself.
func TestPlanOfPlansOpensItsPlansPRs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td, fake, repo, events, _ := prProject(t)
	if r, err := td.client.SetProjectLandingWithResponse(ctx, "app", api.ProjectLandingUpdate{}); err != nil || r.JSON200 == nil {
		t.Fatalf("landing settings: %v, %s", err, r.Body)
	}
	td.createPlan(t, "app", "sso", ssoPlan)
	td.createPlan(t, "app", "ui", uiPlan)
	td.createPlan(t, "app", "big", "goal: g\npr: true\nplans:\n  - id: sso\n  - id: ui\n    after: [sso]\n")
	td.runPlan(t, "big", 0)
	td.waitPlan(t, "big", "sso running", childrenAre("sso:running ui:draft"))
	td.finishTask(t, repo, "sso", "s")

	if l := planLanding(t, events, "sso-1"); l.State != api.WorktreeLandingStateOpened {
		t.Fatalf("sso's landing: %+v", l)
	}
	if calls := fake.Calls(t); !slices.Contains(calls, "pr create --fill --base main --head plan/sso") {
		t.Errorf("gh was asked %q", calls)
	}
	// Its PR open, not merged, sso's work is not in main: ui waits for the person.
	td.waitPlan(t, "big", "ui waiting", childrenAre("sso:done ui:waiting"))
}
