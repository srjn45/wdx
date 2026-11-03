package daemon

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/waittest"
)

// TestViews: tide's view as a whole and each project's are read back as written,
// {} until then, and a request the spec does not allow changes nothing.
func TestViews(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.create(t, "app")
	td.create(t, "web")

	got, err := td.client.GetTideViewWithResponse(ctx)
	if err != nil || got.JSON200 == nil || len(got.JSON200.View) != 0 {
		t.Fatalf("none saved: %v %s", err, got.Body)
	}
	whole := map[string]any{"project": "proj-x", "focus": "agent", "terminal_open": true}
	put, err := td.client.ReplaceTideViewWithResponse(ctx, api.View{View: whole})
	if err != nil || put.JSON200 == nil || put.JSON200.Result != api.ResultUpdated || !reflect.DeepEqual(put.JSON200.View, whole) {
		t.Fatalf("replace: %v %s", err, put.Body)
	}
	if got, err := td.client.GetTideViewWithResponse(ctx); err != nil || got.JSON200 == nil || !reflect.DeepEqual(got.JSON200.View, whole) {
		t.Fatalf("read back: %v %s", err, got.Body)
	}

	app := map[string]any{"tab": "settings", "cursor": map[string]any{"section": "docs", "doc": "doc-a"}, "folded": []any{"agent-a"}}
	if r, err := td.client.ReplaceProjectViewWithResponse(ctx, "app", api.View{View: app}); err != nil || r.JSON200 == nil {
		t.Fatalf("replace app's: %v %s", err, r.Body)
	}
	if r, err := td.client.GetProjectViewWithResponse(ctx, "app"); err != nil || r.JSON200 == nil || !reflect.DeepEqual(r.JSON200.View, app) {
		t.Fatalf("app's: %v %s", err, r.Body)
	}
	if r, err := td.client.GetProjectViewWithResponse(ctx, "web"); err != nil || r.JSON200 == nil || len(r.JSON200.View) != 0 {
		t.Fatalf("web's: %v %s", err, r.Body)
	}

	missing, err := td.client.GetProjectViewWithResponse(ctx, "nope")
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, missing.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
	for _, body := range []string{`{"view": []}`, `{"view": "x"}`, `{}`} {
		resp, problem := td.do(t, http.MethodPut, "/v1/projects/app/view", body)
		if resp.StatusCode != http.StatusBadRequest || problem["code"] != string(api.ErrorCodeUsage) {
			t.Errorf("%s: %d %v", body, resp.StatusCode, problem)
		}
	}
	if r, err := td.client.GetProjectViewWithResponse(ctx, "app"); err != nil || r.JSON200 == nil || !reflect.DeepEqual(r.JSON200.View, app) {
		t.Errorf("after refusals: %v %s", err, r.Body)
	}
}

// TestViewsPassChanges: a view is saved while another change holds the line.
func TestViewsPassChanges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.create(t, "app")
	td.daemon.changes.Lock()
	defer td.daemon.changes.Unlock()
	done := make(chan error, 2)
	go func() {
		_, err := td.client.ReplaceTideViewWithResponse(ctx, api.View{View: map[string]any{"focus": "agent"}})
		done <- err
	}()
	go func() {
		_, err := td.client.ReplaceProjectViewWithResponse(ctx, "app", api.View{View: map[string]any{"tab": "settings"}})
		done <- err
	}()
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-waittest.After(t):
			t.Fatal("a view waited for the change holding the line")
		}
	}
}

func TestIsViewPath(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]bool{
		"/v1/tide/view":           true,
		"/v1/projects/app/view":   true,
		"/v1/projects/view":       false, // a project called view
		"/v1/projects/app/tabs":   false,
		"/v1/projects//view":      false,
		"/v1/projects/a/b/view":   false,
		"/v1/projects/app/viewer": false,
	} {
		if got := isViewPath(path); got != want {
			t.Errorf("%s: %v", path, got)
		}
	}
}
