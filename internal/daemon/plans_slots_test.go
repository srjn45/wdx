package daemon

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

// TestPlanSlotsGuarded: a plan shows its slots with the task in each, and a slot
// cannot be landed or removed while the plan goes on; once over, it is a worktree
// as any.
func TestPlanSlotsGuarded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.planProject(t, "a manual\n")
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")
	if p := td.createPlan(t, "app", "other", "goal: g\ntasks:\n  - id: a\n    prompt: p\n").Plan; p.Slots == nil || len(p.Slots) != 0 {
		t.Errorf("a draft's slots: %+v", p.Slots)
	}
	td.runPlan(t, "auth", 0)
	c := td.waitPlan(t, "auth", "a running", stateIs("a:running"))
	if len(c.Plan.Slots) != 1 || c.Plan.Slots[0].Slot != 1 || c.Plan.Slots[0].Task == nil || *c.Plan.Slots[0].Task != "a" {
		t.Fatalf("slots: %+v", c.Plan.Slots)
	}
	slot := filepath.Base(c.Plan.Slots[0].Path)

	land, err := td.client.LandWorktreeWithResponse(ctx, slot, &api.LandWorktreeParams{Project: optional("app")})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, land.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeWorktreeInPlan)
	if details := *land.ApplicationproblemJSONDefault.Details; details["plan"].(map[string]any)["name"] != "auth" {
		t.Errorf("details: %v", details)
	}
	remove, err := td.client.RemoveWorktreeWithResponse(ctx, slot, &api.RemoveWorktreeParams{Project: optional("app")})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, remove.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeWorktreeInPlan)

	td.cancel(t)
	c = td.waitPlan(t, "auth", "closed", func(c api.PlanContent) bool { return c.Run.FinishedAt != nil })
	if len(c.Plan.Slots) != 0 {
		t.Errorf("slots once cancelled: %+v", c.Plan.Slots)
	}
	remove, err = td.client.RemoveWorktreeWithResponse(ctx, slot, &api.RemoveWorktreeParams{Project: optional("app")})
	if err != nil {
		t.Fatal(err)
	}
	if p := remove.ApplicationproblemJSONDefault; p != nil && p.Code == api.ErrorCodeWorktreeInPlan {
		t.Errorf("a worktree of a cancelled plan refused: %s", remove.Body)
	}
	wantRequestsLogged(t, td.home)
}
