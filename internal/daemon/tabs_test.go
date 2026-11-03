package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

func settingsTab() api.Tab { return api.Tab{Kind: api.TabKindSettings} }

func fileTab(worktree, path string) api.Tab {
	return api.Tab{Kind: api.TabKindFile, Worktree: &worktree, Path: &path}
}

func docTab(id string) api.Tab { return api.Tab{Kind: api.TabKindDoc, DocId: &id} }

// replaceTabs replaces the project's tabs and returns the answer.
func (td *testDaemon) replaceTabs(t *testing.T, project string, tabs ...api.Tab) *api.ReplaceTabsResponse {
	t.Helper()
	r, err := td.client.ReplaceTabsWithResponse(context.Background(), project, api.TabsUpdate{Tabs: tabs})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// tabsAre reports whether an event has exactly the tabs want, in their order.
func tabsAre(e event, want ...api.Tab) bool {
	got, _ := json.Marshal(e.tabs)
	w, _ := json.Marshal(append([]api.Tab{}, want...))
	return e.name == "project" && e.tabs != nil && string(got) == string(w)
}

// TestReplaceTabs: the list given is answered and streamed in its order, a closed
// project keeps one, and a removal through wdx takes its tabs from the stream.
func TestReplaceTabs(t *testing.T) {
	t.Parallel()
	td := start(t)
	td.create(t, "app")
	events, _ := td.follow(t, "app")
	waitEvent(t, events, "no tabs", func(e event) bool { return tabsAre(e) })
	d := td.createDoc(t, "app", api.CreateDoc{Name: "auth", Content: optional("text")})
	pr := "main"
	tabs := []api.Tab{docTab(d.Id), {Kind: api.TabKindPr, Worktree: &pr}, settingsTab(), fileTab("main", "a/b.go")}
	r := td.replaceTabs(t, "app", tabs...)
	if r.JSON200 == nil || r.JSON200.Result != api.ResultUpdated || r.JSON200.TabsRevision <= 0 {
		t.Fatalf("replace: %s", r.Body)
	}
	saved := r.JSON200.TabsRevision
	answered, _ := json.Marshal(r.JSON200.Tabs)
	if given, _ := json.Marshal(tabs); string(answered) != string(given) {
		t.Errorf("answered %s, want %s", answered, given)
	}
	waitEvent(t, events, "the tabs at their revision", func(e event) bool { return tabsAre(e, tabs...) && e.tabsRevision == saved })

	if del, err := td.client.DeleteDocWithResponse(context.Background(), "auth", &api.DeleteDocParams{Project: optional("app")}); err != nil || del.JSON200 == nil {
		t.Fatalf("remove the doc: %v", err)
	}
	waitEvent(t, events, "the doc's tab gone, at a later revision", func(e event) bool { return tabsAre(e, tabs[1:]...) && e.tabsRevision > saved })

	if c, err := td.client.UpdateProjectWithResponse(context.Background(), "app", api.UpdateProject{State: api.ProjectStateClosed}); err != nil || c.JSON200 == nil {
		t.Fatalf("close: %v", err)
	}
	if r := td.replaceTabs(t, "app", settingsTab()); r.JSON200 == nil {
		t.Fatalf("a closed project's tabs: %s", r.Body)
	}
	waitEvent(t, events, "the closed project's tabs", func(e event) bool { return tabsAre(e, settingsTab()) })
}

// TestReplaceTabsRefused: a list the store refuses, or a request the spec does not
// allow, changes nothing.
func TestReplaceTabsRefused(t *testing.T) {
	t.Parallel()
	td := start(t)
	td.create(t, "app")
	td.replaceTabs(t, "app", settingsTab())
	agent := "agent-nowhere"
	for what, tabs := range map[string][]api.Tab{
		"a kind with the wrong fields": {settingsTab(), {Kind: api.TabKindFile}},
		"an agent not the project's":   {settingsTab(), {Kind: api.TabKindAgent, AgentId: &agent}},
		"the same tab twice":           {settingsTab(), settingsTab()},
		"a path outside":               {settingsTab(), fileTab("main", "../x")},
	} {
		r := td.replaceTabs(t, "app", tabs...)
		wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusUnprocessableEntity, api.ErrorCodeInvalidArgument)
		if p := r.ApplicationproblemJSONDefault; p == nil || p.Details == nil || (*p.Details)["index"] != float64(1) {
			t.Errorf("%s: %s", what, r.Body)
		}
	}
	wantProblem(t, td.replaceTabs(t, "nope", settingsTab()).ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
	resp, body := td.do(t, http.MethodPut, "/v1/projects/app/tabs", `{"tabs": [{"kind": "terminal", "agent_id": null, "doc_id": null, "worktree": null, "path": null}]}`)
	if resp.StatusCode != http.StatusBadRequest || body["code"] != string(api.ErrorCodeUsage) {
		t.Errorf("an unknown kind: %d %v", resp.StatusCode, body)
	}
	td.waitProject(t, "app", "the tabs unchanged", func(e event) bool { return tabsAre(e, settingsTab()) })
}

// TestTabsFollowFiles: a file or a directory renamed through wdx takes its tabs and
// those under it along, and one deleted takes them away.
func TestTabsFollowFiles(t *testing.T) {
	t.Parallel()
	td, _ := startEditing(t, "")
	ctx := context.Background()
	project := "api"
	yes := true
	for _, path := range []string{"src", "src/lib"} {
		if r, err := td.client.CreateWorktreeFileWithResponse(ctx, "main", &api.CreateWorktreeFileParams{Project: &project}, api.WorktreeFileCreate{Path: path, Directory: &yes}); err != nil || r.JSON201 == nil {
			t.Fatalf("%s: %v %s", path, err, r.Body)
		}
	}
	if r, err := td.client.CreateWorktreeFileWithResponse(ctx, "main", &api.CreateWorktreeFileParams{Project: &project}, api.WorktreeFileCreate{Path: "src/lib/b.go"}); err != nil || r.JSON201 == nil {
		t.Fatalf("b.go: %v %s", err, r.Body)
	}
	diff := "src/lib/b.go"
	td.replaceTabs(t, "api", fileTab("main", "src/lib/b.go"), api.Tab{Kind: api.TabKindDiff, Worktree: optional("main"), Path: &diff}, fileTab("main", "a.go"))

	if r, err := td.client.RenameWorktreeFileWithResponse(ctx, "main", &api.RenameWorktreeFileParams{Project: &project}, api.WorktreeFileRename{Path: "src/lib", Name: "pkg"}); err != nil || r.JSON200 == nil {
		t.Fatalf("rename: %v %s", err, r.Body)
	}
	moved := "src/pkg/b.go"
	td.waitProject(t, "api", "the tabs renamed", func(e event) bool {
		return tabsAre(e, fileTab("main", moved), api.Tab{Kind: api.TabKindDiff, Worktree: optional("main"), Path: &moved}, fileTab("main", "a.go"))
	})
	if r, err := td.client.RemoveWorktreeFileWithResponse(ctx, "main", &api.RemoveWorktreeFileParams{Project: &project, Path: "src"}); err != nil || r.JSON200 == nil {
		t.Fatalf("remove: %v %s", err, r.Body)
	}
	td.waitProject(t, "api", "the tabs removed", func(e event) bool { return tabsAre(e, fileTab("main", "a.go")) })
}
