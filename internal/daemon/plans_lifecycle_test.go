package daemon

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

// TestPlanLifecycle: a plan is archived only once it is over, which leaves it out of
// listPlans but not the project's stream, and copied in any state.
func TestPlanLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.planProject(t, "a manual\n")
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    aca: claude_code\n    model: opus\n    prompt: p\n")
	archive := func() (*api.PlanResult, *api.Problem) {
		t.Helper()
		r, err := td.client.ArchivePlanWithResponse(ctx, "auth", &api.ArchivePlanParams{Project: optional("app")})
		if err != nil {
			t.Fatal(err)
		}
		return r.JSON200, r.ApplicationproblemJSONDefault
	}
	unarchive := func() (*api.PlanResult, *api.Problem) {
		t.Helper()
		r, err := td.client.UnarchivePlanWithResponse(ctx, "auth", &api.UnarchivePlanParams{Project: optional("app")})
		if err != nil {
			t.Fatal(err)
		}
		return r.JSON200, r.ApplicationproblemJSONDefault
	}
	listed := func(archived bool) int {
		t.Helper()
		r, err := td.client.ListPlansWithResponse(ctx, &api.ListPlansParams{Project: optional("app"), Archived: &archived})
		if err != nil || r.JSON200 == nil {
			t.Fatalf("list: %v %s", err, r.Body)
		}
		return len(r.JSON200.Plans)
	}
	want := func(r *api.PlanResult, problem *api.Problem, result api.Result, archived bool) {
		t.Helper()
		if r == nil || r.Result != result || (r.Plan.ArchivedAt != nil) != archived || r.Plan.State != api.PlanStateCancelled {
			t.Errorf("got %+v, %+v; want %s, archived %v", r, problem, result, archived)
		}
	}

	_, problem := archive()
	wantProblem(t, problem, http.StatusConflict, api.ErrorCodePlanNotFinished)
	td.runPlan(t, "auth", 0)
	td.waitPlan(t, "auth", "a running", stateIs("a:running"))
	_, problem = unarchive()
	wantProblem(t, problem, http.StatusConflict, api.ErrorCodePlanNotFinished)
	td.cancel(t)
	td.waitPlan(t, "auth", "closed", func(c api.PlanContent) bool { return c.Run.FinishedAt != nil })

	r, problem := archive()
	want(r, problem, api.ResultArchived, true)
	r, problem = archive()
	want(r, problem, api.ResultUnchanged, true)
	if n, all := listed(false), listed(true); n != 0 || all != 1 {
		t.Errorf("listed %d, %d with the archived", n, all)
	}
	td.waitPlan(t, "auth", "archived in the stream", func(c api.PlanContent) bool { return c.Plan.ArchivedAt != nil })

	copied, err := td.client.CopyPlanWithResponse(ctx, "auth", &api.CopyPlanParams{Project: optional("app")}, api.CopyPlan{Name: "auth-2", Folder: optional("/again")})
	if err != nil || copied.JSON201 == nil {
		t.Fatalf("copy: %v %s", err, copied.Body)
	}
	if c := copied.JSON201; c.Result != api.ResultCreated || c.Plan.Name != "auth-2" || c.Plan.State != api.PlanStateDraft || c.Plan.Folder != "/again" || c.Plan.ArchivedAt != nil {
		t.Errorf("an archived plan's copy: %s", copied.Body)
	}

	r, problem = unarchive()
	want(r, problem, api.ResultUnarchived, false)
	r, problem = unarchive()
	want(r, problem, api.ResultUnchanged, false)

	td.createPlan(t, "app", "big", "goal: g\nplans:\n  - id: auth-2\n")
	for _, c := range []struct {
		what   string
		ref    string
		name   string
		status int
		code   api.ErrorCode
	}{
		{"a name taken", "auth", "AUTH-2", http.StatusConflict, api.ErrorCodeNameTaken},
		{"a name it cannot have", "auth", "auth_3", http.StatusUnprocessableEntity, api.ErrorCodeInvalidName},
		{"no plan", "nowhere", "x", http.StatusNotFound, api.ErrorCodeNotFound},
	} {
		r, err := td.client.CopyPlanWithResponse(ctx, c.ref, &api.CopyPlanParams{Project: optional("app")}, api.CopyPlan{Name: c.name})
		if err != nil {
			t.Fatal(err)
		}
		if p := r.ApplicationproblemJSONDefault; p == nil || p.Code != c.code || p.Status != c.status {
			t.Errorf("%s: want %d %s, got %s", c.what, c.status, c.code, r.Body)
		}
	}
	parent, err := td.client.CopyPlanWithResponse(ctx, "big", &api.CopyPlanParams{Project: optional("app")}, api.CopyPlan{Name: "big-2"})
	if err != nil || parent.JSON201 == nil || parent.JSON201.Plan.Plans == nil || !slices.Equal(*parent.JSON201.Plan.Plans, []string{"auth-2-2"}) {
		t.Errorf("a plan of plans' copy: %v, %s", err, parent.Body)
	}
	wantRequestsLogged(t, td.home)
}
