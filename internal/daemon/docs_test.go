package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/telemetry"
)

// createDoc creates a doc in the project named project.
func (td *testDaemon) createDoc(t *testing.T, project string, body api.CreateDoc) api.Doc {
	t.Helper()
	body.Project = &project
	r, err := td.client.CreateDocWithResponse(context.Background(), body)
	if err != nil || r.JSON201 == nil || r.JSON201.Result != api.ResultCreated {
		t.Fatalf("create doc: %v %s", err, r.Body)
	}
	return r.JSON201.Doc
}

func TestDocContent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.create(t, "app")
	in := &api.UpdateDocContentParams{Project: optional("app")}

	d := td.createDoc(t, "app", api.CreateDoc{Name: "auth", Labels: &[]string{"rfc"}, Status: optional(api.DocStatusApproved), Content: optional("one\n")})
	if d.Format != "md" || d.Revision != 1 || d.Size != 4 || d.ProjectName != "app" || !slices.Equal(d.Labels, []string{"rfc"}) {
		t.Errorf("created: %+v", d)
	}

	up, err := td.client.UpdateDocContentWithResponse(ctx, "auth", in, api.DocContentChange{Base: 1, Content: "two\n"})
	if err != nil || up.JSON200 == nil {
		t.Fatalf("update: %v %s", err, up.Body)
	}
	if r := up.JSON200; r.Result != api.ResultUpdated || r.Doc.Revision != 2 || r.Doc.Status != api.DocStatusWip || r.PreviousStatus == nil || *r.PreviousStatus != api.DocStatusApproved {
		t.Errorf("updated: %+v", r)
	}

	same, err := td.client.UpdateDocContentWithResponse(ctx, "auth", in, api.DocContentChange{Base: 2, Content: "two\n"})
	if err != nil || same.JSON200 == nil || same.JSON200.Result != api.ResultUnchanged || same.JSON200.PreviousStatus != nil || same.JSON200.Doc.Revision != 2 {
		t.Errorf("the same content: %v %s", err, same.Body)
	}

	stale, err := td.client.UpdateDocContentWithResponse(ctx, "auth", in, api.DocContentChange{Base: 1, Content: "three\n"})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, stale.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeDocChanged)
	if details := *stale.ApplicationproblemJSONDefault.Details; details["base"] != float64(1) || details["doc"].(map[string]any)["revision"] != float64(2) {
		t.Errorf("doc_changed details: %v", details)
	}

	got, err := td.client.GetDocWithResponse(ctx, "auth", &api.GetDocParams{Project: optional("app")})
	if err != nil || got.JSON200 == nil || got.JSON200.Content != "two\n" || got.JSON200.Revision != 2 {
		t.Errorf("the latest: %v %s", err, got.Body)
	}
	first, err := td.client.GetDocWithResponse(ctx, "id:"+d.Id, &api.GetDocParams{Revision: optional(int64(1))})
	if err != nil || first.JSON200 == nil || first.JSON200.Content != "one\n" || first.JSON200.Revision != 1 || first.JSON200.Doc.Revision != 2 {
		t.Errorf("revision 1: %v %s", err, first.Body)
	}
	missing, err := td.client.GetDocWithResponse(ctx, "auth", &api.GetDocParams{Project: optional("app"), Revision: optional(int64(3))})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, missing.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)

	revs, err := td.client.ListDocRevisionsWithResponse(ctx, "auth", &api.ListDocRevisionsParams{Project: optional("app")})
	if err != nil || revs.JSON200 == nil || len(revs.JSON200.Revisions) != 2 || revs.JSON200.Revisions[0].Number != 2 || revs.JSON200.Revisions[1].Size != 4 {
		t.Errorf("revisions: %v %s", err, revs.Body)
	}

	nul, err := td.client.UpdateDocContentWithResponse(ctx, "auth", in, api.DocContentChange{Base: 2, Content: "a\x00b"})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, nul.ApplicationproblemJSONDefault, http.StatusUnprocessableEntity, api.ErrorCodeInvalidArgument)
}

func TestDocCreateRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.create(t, "app")
	d := td.createDoc(t, "app", api.CreateDoc{Name: "auth", Format: optional("txt")})
	if d.Format != "txt" || d.Size != 0 || d.Status != api.DocStatusWip {
		t.Errorf("an empty txt doc: %+v", d)
	}

	for _, c := range []struct {
		what   string
		body   api.CreateDoc
		status int
		code   api.ErrorCode
	}{
		{"a name taken", api.CreateDoc{Project: optional("app"), Name: "AUTH"}, http.StatusConflict, api.ErrorCodeNameTaken},
		{"a slash", api.CreateDoc{Project: optional("app"), Name: "a/b"}, http.StatusUnprocessableEntity, api.ErrorCodeInvalidName},
		{"a capital label", api.CreateDoc{Project: optional("app"), Name: "b", Labels: &[]string{"RFC"}}, http.StatusUnprocessableEntity, api.ErrorCodeInvalidArgument},
		{"no project", api.CreateDoc{Name: "b"}, http.StatusBadRequest, api.ErrorCodeUsage},
		{"a project missing", api.CreateDoc{Project: optional("nope"), Name: "b"}, http.StatusNotFound, api.ErrorCodeNotFound},
		{"an unknown status", api.CreateDoc{Project: optional("app"), Name: "b", Status: optional(api.DocStatus("done"))}, http.StatusBadRequest, api.ErrorCodeUsage},
	} {
		r, err := td.client.CreateDocWithResponse(ctx, c.body)
		if err != nil {
			t.Fatal(err)
		}
		if r.ApplicationproblemJSONDefault == nil || r.ApplicationproblemJSONDefault.Code != c.code || r.ApplicationproblemJSONDefault.Status != c.status {
			t.Errorf("%s: want %d %s, got %s", c.what, c.status, c.code, r.Body)
		}
		if c.code == api.ErrorCodeNameTaken {
			if taken := (*r.ApplicationproblemJSONDefault.Details)["doc"].(map[string]any); taken["id"] != d.Id {
				t.Errorf("name_taken details: %v", taken)
			}
		}
	}
}

func TestDocList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app")
	td.create(t, "other")
	td.createDoc(t, "app", api.CreateDoc{Name: "zeta", Labels: &[]string{"auth", "rfc"}})
	td.createDoc(t, "app", api.CreateDoc{Name: "alpha", Labels: &[]string{"auth"}, Status: optional(api.DocStatusApproved)})
	td.createDoc(t, "other", api.CreateDoc{Name: "beta", Labels: &[]string{"auth"}})

	names := func(params api.ListDocsParams) []string {
		t.Helper()
		r, err := td.client.ListDocsWithResponse(ctx, &params)
		if err != nil || r.JSON200 == nil {
			t.Fatalf("list %+v: %v %s", params, err, r.Body)
		}
		var names []string
		for _, d := range r.JSON200.Docs {
			names = append(names, d.Name)
		}
		return names
	}
	if got := names(api.ListDocsParams{Project: optional("app")}); !slices.Equal(got, []string{"alpha", "zeta"}) {
		t.Errorf("all: %q", got)
	}
	if got := names(api.ListDocsParams{Dir: &app.Worktrees[0].Path, Labels: &[]string{"auth", "rfc"}}); !slices.Equal(got, []string{"zeta"}) {
		t.Errorf("both labels, by dir: %q", got)
	}
	if got := names(api.ListDocsParams{Project: optional("app"), Status: optional(api.DocStatusApproved)}); !slices.Equal(got, []string{"alpha"}) {
		t.Errorf("approved: %q", got)
	}

	for _, c := range []struct {
		what   string
		params api.ListDocsParams
		code   api.ErrorCode
	}{
		{"no project", api.ListDocsParams{}, api.ErrorCodeUsage},
		{"project and dir", api.ListDocsParams{Project: optional("app"), Dir: &app.Worktrees[0].Path}, api.ErrorCodeUsage},
		{"a capital label", api.ListDocsParams{Project: optional("app"), Labels: &[]string{"Auth"}}, api.ErrorCodeInvalidArgument},
	} {
		r, err := td.client.ListDocsWithResponse(ctx, &c.params)
		if err != nil {
			t.Fatal(err)
		}
		if r.ApplicationproblemJSONDefault == nil || r.ApplicationproblemJSONDefault.Code != c.code {
			t.Errorf("%s: want %s, got %s", c.what, c.code, r.Body)
		}
	}
}

func TestDocUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.create(t, "app")
	in := &api.UpdateDocParams{Project: optional("app")}
	d := td.createDoc(t, "app", api.CreateDoc{Name: "auth", Labels: &[]string{"draft"}})
	td.createDoc(t, "app", api.CreateDoc{Name: "design"})

	set, err := td.client.UpdateDocWithResponse(ctx, "auth", in, api.UpdateDoc{Status: optional(api.DocStatusReview), AddLabels: &[]string{"rfc"}, RemoveLabels: &[]string{"draft"}})
	if err != nil || set.JSON200 == nil {
		t.Fatalf("set: %v %s", err, set.Body)
	}
	if r := set.JSON200; r.Result != api.ResultUpdated || r.Doc.Status != api.DocStatusReview || !slices.Equal(r.Doc.Labels, []string{"rfc"}) || r.Doc.Revision != 1 || r.PreviousStatus != nil {
		t.Errorf("set: %+v", r)
	}
	again, err := td.client.UpdateDocWithResponse(ctx, "auth", in, api.UpdateDoc{Status: optional(api.DocStatusReview)})
	if err != nil || again.JSON200 == nil || again.JSON200.Result != api.ResultUnchanged {
		t.Errorf("setting what it has: %v %s", err, again.Body)
	}

	renamed, err := td.client.UpdateDocWithResponse(ctx, "auth", in, api.UpdateDoc{Name: optional("auth-rfc")})
	if err != nil || renamed.JSON200 == nil || renamed.JSON200.Result != api.ResultUpdated || renamed.JSON200.Doc.Name != "auth-rfc" || renamed.JSON200.Doc.Id != d.Id {
		t.Errorf("rename: %v %s", err, renamed.Body)
	}
	same, err := td.client.UpdateDocWithResponse(ctx, "id:"+d.Id, nil, api.UpdateDoc{Name: optional("auth-rfc")})
	if err != nil || same.JSON200 == nil || same.JSON200.Result != api.ResultUnchanged {
		t.Errorf("renaming to its name: %v %s", err, same.Body)
	}

	for _, c := range []struct {
		what   string
		ref    string
		body   api.UpdateDoc
		status int
		code   api.ErrorCode
	}{
		{"a name and a status", "auth-rfc", api.UpdateDoc{Name: optional("x"), Status: optional(api.DocStatusWip)}, http.StatusBadRequest, api.ErrorCodeUsage},
		{"nothing", "auth-rfc", api.UpdateDoc{}, http.StatusBadRequest, api.ErrorCodeUsage},
		{"another doc's name", "auth-rfc", api.UpdateDoc{Name: optional("Design")}, http.StatusConflict, api.ErrorCodeNameTaken},
		{"a label added and removed", "auth-rfc", api.UpdateDoc{AddLabels: &[]string{"x"}, RemoveLabels: &[]string{"x"}}, http.StatusUnprocessableEntity, api.ErrorCodeInvalidArgument},
		{"the old name", "auth", api.UpdateDoc{Status: optional(api.DocStatusWip)}, http.StatusNotFound, api.ErrorCodeNotFound},
	} {
		r, err := td.client.UpdateDocWithResponse(ctx, c.ref, in, c.body)
		if err != nil {
			t.Fatal(err)
		}
		if r.ApplicationproblemJSONDefault == nil || r.ApplicationproblemJSONDefault.Code != c.code || r.ApplicationproblemJSONDefault.Status != c.status {
			t.Errorf("%s: want %d %s, got %s", c.what, c.status, c.code, r.Body)
		}
	}
}

func TestDocReferences(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.create(t, "app")
	td.create(t, "other")
	d := td.createDoc(t, "app", api.CreateDoc{Name: "auth"})

	for _, c := range []struct {
		what   string
		ref    string
		params api.GetDocParams
		code   api.ErrorCode
	}{
		{"a name without a project", "auth", api.GetDocParams{}, api.ErrorCodeUsage},
		{"an id in another project", "id:" + d.Id, api.GetDocParams{Project: optional("other")}, api.ErrorCodeNotFound},
		{"a name in another project", "auth", api.GetDocParams{Project: optional("other")}, api.ErrorCodeNotFound},
	} {
		r, err := td.client.GetDocWithResponse(ctx, c.ref, &c.params)
		if err != nil {
			t.Fatal(err)
		}
		if r.ApplicationproblemJSONDefault == nil || r.ApplicationproblemJSONDefault.Code != c.code {
			t.Errorf("%s: want %s, got %s", c.what, c.code, r.Body)
		}
	}
	if r, err := td.client.GetDocWithResponse(ctx, "id:"+d.Id, &api.GetDocParams{Project: optional("app")}); err != nil || r.JSON200 == nil {
		t.Errorf("an id with its project: %v %s", err, r.Body)
	}
}

// TestDocRemove also checks the stream sends a project's docs, and removing the
// doc deletes its copy in the home.
func TestDocRemove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.create(t, "app")
	events, _ := td.follow(t, "app")
	d := td.createDoc(t, "app", api.CreateDoc{Name: "auth", Content: optional("text")})
	waitEvent(t, events, "the doc", func(e event) bool {
		return e.name == "project" && e.project.Docs != nil && len(*e.project.Docs) == 1 && (*e.project.Docs)[0].Id == d.Id
	})
	copy := filepath.Join(identity.DocsDir(td.home), "app", "auth.md")
	if _, err := os.Stat(copy); err != nil {
		t.Fatalf("the copy: %v", err)
	}

	r, err := td.client.DeleteDocWithResponse(ctx, "auth", &api.DeleteDocParams{Project: optional("app")})
	if err != nil || r.JSON200 == nil || r.JSON200.Result != api.ResultRemoved || r.JSON200.Doc.Id != d.Id {
		t.Fatalf("remove: %v %s", err, r.Body)
	}
	waitEvent(t, events, "no doc", func(e event) bool {
		return e.name == "project" && e.project.Docs != nil && len(*e.project.Docs) == 0
	})
	if _, err := os.Stat(copy); !os.IsNotExist(err) {
		t.Errorf("the copy is left: %v", err)
	}
	again, err := td.client.DeleteDocWithResponse(ctx, "auth", &api.DeleteDocParams{Project: optional("app")})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, again.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
}

func TestDeleteProjectRemovesDocCopies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.create(t, "app")
	td.create(t, "other")
	td.createDoc(t, "app", api.CreateDoc{Name: "auth"})
	td.createDoc(t, "other", api.CreateDoc{Name: "auth"})

	if r, err := td.client.DeleteProjectWithResponse(ctx, "app", nil); err != nil || r.JSON200 == nil {
		t.Fatalf("remove the project: %v %s", err, r.Body)
	}
	docs := identity.DocsDir(td.home)
	if _, err := os.Stat(filepath.Join(docs, "app")); !os.IsNotExist(err) {
		t.Errorf("the project's copies are left: %v", err)
	}
	if _, err := os.Stat(filepath.Join(docs, "other", "auth.md")); err != nil {
		t.Errorf("another project's copy: %v", err)
	}
}

func TestDocCopiesCaughtUpOnStart(t *testing.T) {
	t.Parallel()
	td := start(t)
	td.create(t, "app")
	td.createDoc(t, "app", api.CreateDoc{Name: "auth", Content: optional("text")})
	td.stop()
	copy := filepath.Join(identity.DocsDir(td.home), "app", "auth.md")
	if err := os.Remove(copy); err != nil {
		t.Fatal(err)
	}

	startOn(t, td.home, telemetry.Options{}, nil)
	got, err := os.ReadFile(copy)
	if err != nil || string(got) != "text" {
		t.Errorf("the copy after a start: %q, %v", got, err)
	}
}

// TestDocContentNotLogged checks a doc's text, which can hold secrets, is measured
// in the requests and responses logged, never logged.
func TestDocContentNotLogged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.create(t, "app")
	td.createDoc(t, "app", api.CreateDoc{Name: "auth", Content: optional("created-secret")})
	if r, err := td.client.UpdateDocContentWithResponse(ctx, "auth", &api.UpdateDocContentParams{Project: optional("app")}, api.DocContentChange{Base: 1, Content: "updated-secret"}); err != nil || r.JSON200 == nil {
		t.Fatalf("update: %v %s", err, r.Body)
	}
	if r, err := td.client.GetDocWithResponse(ctx, "auth", &api.GetDocParams{Project: optional("app")}); err != nil || r.JSON200 == nil {
		t.Fatalf("get: %v %s", err, r.Body)
	}

	measured := map[string]bool{}
	for _, line := range logLines(t, td.home) {
		b, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "-secret") {
			t.Errorf("a doc's text is logged: %s", b)
		}
		if body, ok := line["body"].(map[string]any); ok && body["content_bytes"] != nil {
			measured[line["msg"].(string)] = true
		}
		if response, ok := line["response"].(map[string]any); ok && response["content_bytes"] == float64(len("updated-secret")) {
			measured[line["msg"].(string)] = true
		}
	}
	if !measured["request received"] || !measured["request returned"] {
		t.Errorf("want the text measured in requests and responses, got %v", measured)
	}
}

// syncToGit syncs the docs of the project named project into path.
func (td *testDaemon) syncToGit(t *testing.T, project, path string) *api.SyncDocsToGitResponse {
	t.Helper()
	r, err := td.client.SyncDocsToGitWithResponse(context.Background(), api.SyncDocs{Project: &project, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSyncDocsToGit(t *testing.T) {
	t.Parallel()
	td := start(t)
	app := td.create(t, "app")
	td.createDoc(t, "app", api.CreateDoc{Name: "auth", Content: optional("auth")})
	clone := app.Worktrees[0].Path

	r := td.syncToGit(t, "app", "docs/wdx")
	if r.JSON200 == nil || r.JSON200.Result != api.ResultSynced || r.JSON200.Path != filepath.Join(clone, "docs", "wdx") ||
		!slices.Equal(r.JSON200.Written, []string{"auth.md"}) || r.JSON200.Removed == nil || r.JSON200.Skipped == nil {
		t.Fatalf("sync: %s", r.Body)
	}
	if b, err := os.ReadFile(filepath.Join(clone, "docs", "wdx", "auth.md")); err != nil || string(b) != "auth" {
		t.Errorf("the file: %q, %v", b, err)
	}

	r = td.syncToGit(t, "app", "../out")
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusUnprocessableEntity, api.ErrorCodeInvalidPath)
	r = td.syncToGit(t, "nope", "docs")
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
	if err := os.RemoveAll(clone); err != nil {
		t.Fatal(err)
	}
	r = td.syncToGit(t, "app", "docs")
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeCheckoutMissing)
}

// TestDocsGitPath checks the setting keeps every project's docs written: on start,
// on every change, and when a reload sets it anew.
func TestDocsGitPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	app := td.create(t, "app")
	td.createDoc(t, "app", api.CreateDoc{Name: "before"})
	td.stop()
	clone := app.Worktrees[0].Path
	dir := filepath.Join(clone, "docs")
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}

	path := "docs"
	td = startOn(t, td.home, telemetry.Options{}, func(o *Options) {
		o.DocsGitPath = path
		o.Reload = func() (Settings, error) { return Settings{DocsGitPath: path}, nil }
	})
	if !exists("before.md") {
		t.Fatal("not written on start")
	}
	d := td.createDoc(t, "app", api.CreateDoc{Name: "auth", Content: optional("one")})
	if !exists("auth.md") {
		t.Error("not written on create")
	}
	if r, err := td.client.UpdateDocContentWithResponse(ctx, "id:"+d.Id, nil, api.DocContentChange{Base: 1, Content: "two"}); err != nil || r.JSON200 == nil {
		t.Fatalf("update: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "auth.md")); string(b) != "two" {
		t.Errorf("after an update: %q", b)
	}
	if r, err := td.client.UpdateDocWithResponse(ctx, "id:"+d.Id, nil, api.UpdateDoc{Name: optional("auth-rfc")}); err != nil || r.JSON200 == nil {
		t.Fatalf("rename: %v", err)
	}
	if exists("auth.md") || !exists("auth-rfc.md") {
		t.Error("not moved on rename")
	}
	if r, err := td.client.DeleteDocWithResponse(ctx, "id:"+d.Id, nil); err != nil || r.JSON200 == nil {
		t.Fatalf("remove: %v", err)
	}
	if exists("auth-rfc.md") {
		t.Error("not removed on remove")
	}

	path = "elsewhere"
	if r := reload(t, td); r.JSON200 == nil {
		t.Fatalf("reload: %s", r.Body)
	}
	if _, err := os.Stat(filepath.Join(clone, "elsewhere", "before.md")); err != nil {
		t.Errorf("not written when a reload set it: %v", err)
	}
}
