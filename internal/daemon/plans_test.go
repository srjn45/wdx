package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/telemetry"
)

// createPlan creates a plan in the project named project.
func (td *testDaemon) createPlan(t *testing.T, project, name, content string) api.PlanResult {
	t.Helper()
	r, err := td.client.CreatePlanWithResponse(context.Background(), api.CreatePlan{Project: &project, Name: name, Content: content})
	if err != nil || r.JSON201 == nil || r.JSON201.Result != api.ResultCreated {
		t.Fatalf("create plan: %v %s", err, r.Body)
	}
	return *r.JSON201
}

const planOfTasks = `goal: Keep sessions in the database.
tasks:
  - id: schema
    prompt: Add the table.
  - id: docs
    after: [api, schema]
    prompt: Document it.
  - id: api
    after: [schema]
    prompt: Use it.
`

func TestPlanCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.create(t, "app")
	td.createDoc(t, "app", api.CreateDoc{Name: "auth"})

	created := td.createPlan(t, "app", "auth", planOfTasks)
	p := created.Plan
	if p.Name != "auth" || p.State != api.PlanStateDraft || p.Tasks == nil || *p.Tasks != 3 || p.Plans != nil || p.Parent != nil || p.Revision != 1 || p.ProjectName != "app" {
		t.Errorf("created: %+v", p)
	}
	if want := []api.PlanDropped{{Entry: "docs", After: "schema", Via: "api"}}; created.Dropped == nil || !reflect.DeepEqual(*created.Dropped, want) {
		t.Errorf("dropped: %+v", created.Dropped)
	}
	if _, err := os.Stat(filepath.Join(identity.PlansDir(td.home), "app", "auth.yaml")); err != nil {
		t.Errorf("no copy: %v", err)
	}

	got, err := td.client.GetPlanWithResponse(ctx, "auth", &api.GetPlanParams{Project: optional("app")})
	if err != nil || got.JSON200 == nil {
		t.Fatalf("get: %v %s", err, got.Body)
	}
	def := got.JSON200.Definition
	want := api.PlanDefinition{
		Goal:        "Keep sessions in the database.",
		Constraints: []string{},
		Tasks: &[]api.PlanTask{
			{Id: "schema", After: []string{}, Prompt: "Add the table.", State: api.PlanTaskStatePending, WaitingFor: []string{}},
			{Id: "api", After: []string{"schema"}, Prompt: "Use it.", State: api.PlanTaskStatePending, WaitingFor: []string{"schema"}},
			{Id: "docs", After: []string{"api"}, Prompt: "Document it.", State: api.PlanTaskStatePending, WaitingFor: []string{"api"}},
		},
	}
	if !reflect.DeepEqual(def, want) || got.JSON200.Revision != 1 {
		t.Errorf("definition: %+v", def)
	}

	invalid, err := td.client.CreatePlanWithResponse(ctx, api.CreatePlan{Project: optional("app"), Name: "bad", Content: "goal: g\ntasks:\n  - id: a_b\n"})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, invalid.ApplicationproblemJSONDefault, http.StatusUnprocessableEntity, api.ErrorCodeInvalidPlan)
	problems := (*invalid.ApplicationproblemJSONDefault.Details)["problems"].([]any)
	if len(problems) != 2 || problems[0].(map[string]any)["line"] != float64(3) {
		t.Errorf("problems: %v", problems)
	}

	taken, err := td.client.CreatePlanWithResponse(ctx, api.CreatePlan{Project: optional("app"), Name: "AUTH", Content: planOfTasks})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, taken.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeNameTaken)
	if details := *taken.ApplicationproblemJSONDefault.Details; details["plan"].(map[string]any)["id"] != p.Id {
		t.Errorf("name_taken details: %v", details)
	}

	project, err := td.client.GetProjectWithResponse(ctx, "app")
	if err != nil || project.JSON200 == nil || project.JSON200.Project.Plans == nil || len(*project.JSON200.Project.Plans) != 1 || len(*project.JSON200.Project.Docs) != 1 {
		t.Errorf("the project: %v %s", err, project.Body)
	}
	list, err := td.client.ListPlansWithResponse(ctx, &api.ListPlansParams{Project: optional("app")})
	if err != nil || list.JSON200 == nil || len(list.JSON200.Plans) != 1 || list.JSON200.Plans[0].Id != p.Id {
		t.Errorf("list: %v %s", err, list.Body)
	}
	docs, err := td.client.ListDocsWithResponse(ctx, &api.ListDocsParams{Project: optional("app")})
	if err != nil || docs.JSON200 == nil || len(docs.JSON200.Docs) != 1 || docs.JSON200.Docs[0].Id == p.Id {
		t.Errorf("docs list a plan: %v %s", err, docs.Body)
	}
	asDoc, err := td.client.GetDocWithResponse(ctx, "id:"+p.Id, &api.GetDocParams{})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, asDoc.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
	wantRequestsLogged(t, td.home)
}

func TestPlanContent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.create(t, "app")
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")
	in := &api.UpdatePlanContentParams{Project: optional("app")}

	up, err := td.client.UpdatePlanContentWithResponse(ctx, "auth", in, api.DocContentChange{Base: 1, Content: planOfTasks})
	if err != nil || up.JSON200 == nil {
		t.Fatalf("update: %v %s", err, up.Body)
	}
	if r := up.JSON200; r.Result != api.ResultUpdated || r.Plan.Revision != 2 || *r.Plan.Tasks != 3 || r.Dropped == nil || len(*r.Dropped) != 1 {
		t.Errorf("updated: %+v", r)
	}
	stored, err := td.client.GetPlanWithResponse(ctx, "auth", &api.GetPlanParams{Project: optional("app")})
	if err != nil || stored.JSON200 == nil {
		t.Fatal(err)
	}
	same, err := td.client.UpdatePlanContentWithResponse(ctx, "auth", in, api.DocContentChange{Base: 2, Content: stored.JSON200.Content})
	if err != nil || same.JSON200 == nil || same.JSON200.Result != api.ResultUnchanged || len(*same.JSON200.Dropped) != 0 {
		t.Errorf("the same content: %v %s", err, same.Body)
	}
	stale, err := td.client.UpdatePlanContentWithResponse(ctx, "auth", in, api.DocContentChange{Base: 1, Content: planOfTasks})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, stale.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodePlanChanged)
	if details := *stale.ApplicationproblemJSONDefault.Details; details["base"] != float64(1) || details["plan"].(map[string]any)["revision"] != float64(2) {
		t.Errorf("plan_changed details: %v", details)
	}
	invalid, err := td.client.UpdatePlanContentWithResponse(ctx, "auth", in, api.DocContentChange{Base: 2, Content: "goal: g\n"})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, invalid.ApplicationproblemJSONDefault, http.StatusUnprocessableEntity, api.ErrorCodeInvalidPlan)

	first, err := td.client.GetPlanWithResponse(ctx, "auth", &api.GetPlanParams{Project: optional("app"), Revision: optional(int64(1))})
	if err != nil || first.JSON200 == nil || first.JSON200.Revision != 1 || len(*first.JSON200.Definition.Tasks) != 1 {
		t.Errorf("revision 1: %v %s", err, first.Body)
	}
	revs, err := td.client.ListPlanRevisionsWithResponse(ctx, "auth", &api.ListPlanRevisionsParams{Project: optional("app")})
	if err != nil || revs.JSON200 == nil || len(revs.JSON200.Revisions) != 2 || revs.JSON200.Revisions[0].Number != 2 {
		t.Errorf("revisions: %v %s", err, revs.Body)
	}
	td.create(t, "other")
	elsewhere, err := td.client.GetPlanWithResponse(ctx, "id:"+first.JSON200.Plan.Id, &api.GetPlanParams{Project: optional("other")})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, elsewhere.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
	missing, err := td.client.GetPlanWithResponse(ctx, "nowhere", &api.GetPlanParams{Project: optional("app")})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, missing.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
	wantRequestsLogged(t, td.home)
}

func TestPlansOfPlansThroughTheAPI(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.create(t, "app")
	child := td.createPlan(t, "app", "auth-a", "goal: g\ntasks:\n  - id: a\n    prompt: p\n").Plan
	parent := td.createPlan(t, "app", "auth", "goal: g\nplans:\n  - id: auth-a\n").Plan
	if parent.Tasks != nil || parent.Plans == nil || !reflect.DeepEqual(*parent.Plans, []string{"auth-a"}) {
		t.Errorf("the parent: %+v", parent)
	}
	got, err := td.client.GetPlanWithResponse(ctx, "id:"+child.Id, &api.GetPlanParams{})
	if err != nil || got.JSON200 == nil || got.JSON200.Plan.Parent == nil || *got.JSON200.Plan.Parent != "auth" {
		t.Errorf("the child: %v %s", err, got.Body)
	}
	if def := got.JSON200.Definition; def.Tasks == nil || def.Plans != nil {
		t.Errorf("the child's definition: %+v", def)
	}

	rename, err := td.client.UpdatePlanWithResponse(ctx, "auth-a", &api.UpdatePlanParams{Project: optional("app")}, api.UpdatePlan{Name: optional("auth-one")})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, rename.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodePlanInUse)
	if details := *rename.ApplicationproblemJSONDefault.Details; details["parent"].(map[string]any)["id"] != parent.Id {
		t.Errorf("plan_in_use details: %v", details)
	}
	remove, err := td.client.DeletePlanWithResponse(ctx, "auth-a", &api.DeletePlanParams{Project: optional("app")})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, remove.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodePlanInUse)

	same, err := td.client.UpdatePlanWithResponse(ctx, "auth", &api.UpdatePlanParams{Project: optional("app")}, api.UpdatePlan{Name: optional("auth")})
	if err != nil || same.JSON200 == nil || same.JSON200.Result != api.ResultUnchanged {
		t.Errorf("renamed to its own name: %v %s", err, same.Body)
	}
	renamed, err := td.client.UpdatePlanWithResponse(ctx, "auth", &api.UpdatePlanParams{Project: optional("app")}, api.UpdatePlan{Name: optional("auth-all")})
	if err != nil || renamed.JSON200 == nil || renamed.JSON200.Result != api.ResultUpdated || renamed.JSON200.Plan.Name != "auth-all" {
		t.Errorf("renamed: %v %s", err, renamed.Body)
	}
	removed, err := td.client.DeletePlanWithResponse(ctx, "id:"+parent.Id, &api.DeletePlanParams{})
	if err != nil || removed.JSON200 == nil || removed.JSON200.Result != api.ResultRemoved {
		t.Fatalf("removed: %v %s", err, removed.Body)
	}
	freed, err := td.client.GetPlanWithResponse(ctx, "auth-a", &api.GetPlanParams{Project: optional("app")})
	if err != nil || freed.JSON200 == nil || freed.JSON200.Plan.Parent != nil {
		t.Errorf("the child once its parent is removed: %v %s", err, freed.Body)
	}
	wantRequestsLogged(t, td.home)
}

// TestPlanTextNotLogged checks a plan's text, whose goal, constraints and prompts
// are what agents are told, is measured in the requests and responses logged,
// never logged.
func TestPlanTextNotLogged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.create(t, "app")
	secret := func(word string) string {
		return "goal: goal-secret\nconstraints: [constraint-secret]\ntasks:\n  - id: a\n    prompt: " + word + "-secret\n"
	}
	td.createPlan(t, "app", "auth", secret("created"))
	if r, err := td.client.UpdatePlanContentWithResponse(ctx, "auth", &api.UpdatePlanContentParams{Project: optional("app")}, api.DocContentChange{Base: 1, Content: secret("updated")}); err != nil || r.JSON200 == nil {
		t.Fatalf("update: %v %s", err, r.Body)
	}
	if r, err := td.client.GetPlanWithResponse(ctx, "auth", &api.GetPlanParams{Project: optional("app")}); err != nil || r.JSON200 == nil {
		t.Fatalf("get: %v %s", err, r.Body)
	}

	measured := map[string]bool{}
	for _, line := range logLines(t, td.home) {
		b, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "-secret") {
			t.Errorf("a plan's text is logged: %s", b)
		}
		if body, ok := line["body"].(map[string]any); ok && body["content_bytes"] != nil {
			measured[line["msg"].(string)] = true
		}
		if response, ok := line["response"].(map[string]any); ok && response["content_bytes"] == float64(len(secret("updated"))) {
			measured[line["msg"].(string)] = true
		}
	}
	if !measured["request received"] || !measured["request returned"] {
		t.Errorf("want the text measured in requests and responses, got %v", measured)
	}
}

func TestDeleteProjectRemovesPlanCopies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.create(t, "app")
	td.create(t, "other")
	td.createPlan(t, "app", "auth", planOfTasks)
	td.createPlan(t, "other", "auth", planOfTasks)
	if r, err := td.client.DeleteProjectWithResponse(ctx, "app", nil); err != nil || r.JSON200 == nil {
		t.Fatalf("remove the project: %v %s", err, r.Body)
	}
	plans := identity.PlansDir(td.home)
	if _, err := os.Stat(filepath.Join(plans, "app")); !os.IsNotExist(err) {
		t.Errorf("the project's copies are left: %v", err)
	}
	if _, err := os.Stat(filepath.Join(plans, "other", "auth.yaml")); err != nil {
		t.Errorf("another project's copy: %v", err)
	}
}

func TestPlanCopiesCaughtUpOnStart(t *testing.T) {
	t.Parallel()
	td := start(t)
	td.create(t, "app")
	td.createPlan(t, "app", "auth", planOfTasks)
	td.stop()
	copy := filepath.Join(identity.PlansDir(td.home), "app", "auth.yaml")
	if err := os.Remove(copy); err != nil {
		t.Fatal(err)
	}
	startOn(t, td.home, telemetry.Options{}, nil)
	got, err := os.ReadFile(copy)
	if err != nil || !strings.Contains(string(got), "Keep sessions") {
		t.Errorf("the copy after a start: %q, %v", got, err)
	}
}
