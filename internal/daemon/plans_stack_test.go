package daemon

import (
	"context"
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

// TestStackedPlanStartsOnTheOneBefore: a stacked plan of plans starts ui as soon as
// sso, which it waits for alone, is done, without waiting for its PR: on plan/sso's
// tip, which its run keeps, its slot measured against plan/sso, and its PR opened
// against plan/sso.
func TestStackedPlanStartsOnTheOneBefore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td, fake, repo, events, leave := prProject(t)
	if r, err := td.client.SetProjectLandingWithResponse(ctx, "app", api.ProjectLandingUpdate{}); err != nil || r.JSON200 == nil {
		t.Fatalf("landing settings: %v, %s", err, r.Body)
	}
	td.createPlan(t, "app", "sso", ssoPlan)
	td.createPlan(t, "app", "ui", uiPlan)
	td.createPlan(t, "app", "big", "goal: g\npr: true\nstacked: true\nplans:\n  - id: sso\n  - id: ui\n    after: [sso]\n")
	td.runPlan(t, "big", 0)
	td.waitPlan(t, "big", "sso running", childrenAre("sso:running ui:draft"))
	td.finishTask(t, repo, "sso", "s")

	c := td.waitPlan(t, "big", "ui running", childrenAre("sso:done ui:running"))
	if ui := (*c.Definition.Plans)[1]; ui.StackedOn == nil || *ui.StackedOn != "sso" {
		t.Errorf("ui: %+v", ui)
	}
	tip := gitOut(t, repo, "rev-parse", "plan/sso")
	p, err := td.daemon.plans.GetByName(ctx, c.Plan.ProjectId, "ui")
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := td.daemon.plans.Run(ctx, p.ID)
	if err != nil || run.Base != "plan/sso" || run.BaseCommit != tip {
		t.Fatalf("ui's run: base %q at %q, not plan/sso at %s: %v", run.Base, run.BaseCommit, tip, err)
	}
	waitEvent(t, events, "ui's slot measured against plan/sso", func(e event) bool {
		w, ok := worktreeStatus(e, "ui-1")
		return ok && w.Base != nil && *w.Base == "plan/sso"
	})

	td.finishTask(t, repo, "ui", "u")
	if l := planLanding(t, events, "ui-1"); l.State != api.WorktreeLandingStateOpened || l.Base != "plan/sso" {
		t.Fatalf("ui's landing: %+v", l)
	}
	if calls := fake.Calls(t); !slices.Contains(calls, "pr create --fill --base plan/sso --head plan/ui") {
		t.Errorf("gh was asked %q", calls)
	}
	leave()
	wantRequestsLogged(t, td.home)
}
