package daemon

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

func TestDocFolder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.create(t, "app")
	in := &api.UpdateDocParams{Project: optional("app")}
	if d := td.createDoc(t, "app", api.CreateDoc{Name: "top"}); d.Folder != "/" {
		t.Errorf("a doc given no folder: %+v", d)
	}
	if d := td.createDoc(t, "app", api.CreateDoc{Name: "spec", Folder: optional("Hub/Initial/")}); d.Folder != "/hub/initial" {
		t.Errorf("a doc given a folder: %+v", d)
	}

	moved, err := td.client.UpdateDocWithResponse(ctx, "top", in, api.UpdateDoc{Folder: optional("/a")})
	if err != nil || moved.JSON200 == nil || moved.JSON200.Result != api.ResultUpdated || moved.JSON200.Doc.Folder != "/a" || moved.JSON200.Doc.Revision != 1 {
		t.Errorf("moved: %v %s", err, moved.Body)
	}
	same, err := td.client.UpdateDocWithResponse(ctx, "top", in, api.UpdateDoc{Folder: optional("a/")})
	if err != nil || same.JSON200 == nil || same.JSON200.Result != api.ResultUnchanged {
		t.Errorf("moved to its folder: %v %s", err, same.Body)
	}

	for _, c := range []struct {
		what   string
		body   api.UpdateDoc
		status int
		code   api.ErrorCode
	}{
		{"a name and a folder", api.UpdateDoc{Name: optional("x"), Folder: optional("/b")}, http.StatusBadRequest, api.ErrorCodeUsage},
		{"a folder with a dot", api.UpdateDoc{Folder: optional("a.b")}, http.StatusUnprocessableEntity, api.ErrorCodeInvalidArgument},
	} {
		r, err := td.client.UpdateDocWithResponse(ctx, "top", in, c.body)
		if err != nil {
			t.Fatal(err)
		}
		wantProblem(t, r.ApplicationproblemJSONDefault, c.status, c.code)
	}
	created, err := td.client.CreateDocWithResponse(ctx, api.CreateDoc{Project: optional("app"), Name: "bad", Folder: optional("a.b")})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, created.ApplicationproblemJSONDefault, http.StatusUnprocessableEntity, api.ErrorCodeInvalidArgument)
	wantRequestsLogged(t, td.home)
}

func TestPlanFolder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.create(t, "app")
	in := &api.UpdatePlanParams{Project: optional("app")}
	tasks := "goal: g\ntasks:\n  - id: a\n    prompt: p\n"
	td.createPlan(t, "app", "auth-a", tasks)
	td.createPlan(t, "app", "auth-b", tasks)
	r, err := td.client.CreatePlanWithResponse(ctx, api.CreatePlan{Project: optional("app"), Name: "auth", Folder: optional("hub"), Content: "goal: g\nplans:\n  - id: auth-a\n"})
	if err != nil || r.JSON201 == nil {
		t.Fatalf("create: %v %s", err, r.Body)
	}
	if got := r.JSON201; got.Plan.Folder != "/hub" || got.Moved == nil || !reflect.DeepEqual(*got.Moved, []string{"auth-a"}) {
		t.Errorf("a plan of plans created in /hub: %s", r.Body)
	}

	update, err := td.client.UpdatePlanContentWithResponse(ctx, "auth", &api.UpdatePlanContentParams{Project: optional("app")}, api.DocContentChange{Base: 1, Content: "goal: g\nplans:\n  - id: auth-a\n  - id: auth-b\n"})
	if err != nil || update.JSON200 == nil || update.JSON200.Moved == nil || !reflect.DeepEqual(*update.JSON200.Moved, []string{"auth-b"}) {
		t.Errorf("an update listing auth-b: %v %s", err, update.Body)
	}

	moved, err := td.client.UpdatePlanWithResponse(ctx, "auth-b", in, api.UpdatePlan{Folder: optional("/x")})
	if err != nil || moved.JSON200 == nil {
		t.Fatalf("move: %v %s", err, moved.Body)
	}
	if got := moved.JSON200; got.Result != api.ResultUpdated || got.Plan.Folder != "/x" || got.Moved == nil || !reflect.DeepEqual(*got.Moved, []string{"auth", "auth-a"}) {
		t.Errorf("moving a plan a plan of plans lists: %s", moved.Body)
	}
	same, err := td.client.UpdatePlanWithResponse(ctx, "auth", in, api.UpdatePlan{Folder: optional("/x/")})
	if err != nil || same.JSON200 == nil || same.JSON200.Result != api.ResultUnchanged || same.JSON200.Moved == nil || len(*same.JSON200.Moved) != 0 {
		t.Errorf("moving to its folder: %v %s", err, same.Body)
	}

	for _, c := range []struct {
		what   string
		body   api.UpdatePlan
		status int
		code   api.ErrorCode
	}{
		{"a name and a folder", api.UpdatePlan{Name: optional("x"), Folder: optional("/b")}, http.StatusBadRequest, api.ErrorCodeUsage},
		{"nothing", api.UpdatePlan{}, http.StatusBadRequest, api.ErrorCodeUsage},
		{"a folder with a dot", api.UpdatePlan{Folder: optional("a.b")}, http.StatusUnprocessableEntity, api.ErrorCodeInvalidArgument},
		{"a folder and parallel on a plan of plans", api.UpdatePlan{Folder: optional("/b"), Parallel: optional(2)}, http.StatusUnprocessableEntity, api.ErrorCodeInvalidArgument},
	} {
		r, err := td.client.UpdatePlanWithResponse(ctx, "auth", in, c.body)
		if err != nil {
			t.Fatal(err)
		}
		wantProblem(t, r.ApplicationproblemJSONDefault, c.status, c.code)
	}
	got, err := td.client.GetPlanWithResponse(ctx, "auth", &api.GetPlanParams{Project: optional("app")})
	if err != nil || got.JSON200 == nil || got.JSON200.Plan.Folder != "/x" {
		t.Errorf("a request refused moved the plan: %v %s", err, got.Body)
	}
	wantRequestsLogged(t, td.home)
}
