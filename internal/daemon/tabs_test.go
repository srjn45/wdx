package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/gittest"

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

// tabsAre reports whether an event has exactly the tabs want, in their order,
// whether missing or not.
func tabsAre(e event, want ...api.Tab) bool {
	tabs := make([]api.Tab, len(e.tabs))
	for i, t := range e.tabs {
		t.Missing = nil
		tabs[i] = t
	}
	got, _ := json.Marshal(tabs)
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
	tabs := []api.Tab{docTab(d.Id), {Kind: api.TabKindPr, Worktree: &pr}, settingsTab(), {Kind: api.TabKindStats}, fileTab("main", "a/b.go")}
	r := td.replaceTabs(t, "app", tabs...)
	if r.JSON200 == nil || r.JSON200.Result != api.ResultUpdated || r.JSON200.TabsRevision <= 0 {
		t.Fatalf("replace: %s", r.Body)
	}
	saved := r.JSON200.TabsRevision
	for i := range r.JSON200.Tabs {
		r.JSON200.Tabs[i].Missing = nil
	}
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

// missingIn returns the missing of each of an event's tabs.
func missingIn(e event) []bool {
	var list []bool
	for _, t := range e.tabs {
		list = append(list, t.Missing != nil && *t.Missing)
	}
	return list
}

// TestTabsMissing: a file tab is missing while its file is gone from the disk, a
// file git ignores included, and a diff or pr tab while its worktree is; a diff of
// a deleted file is not. Each is removed outside wdx and git once the worktrees
// were read, so that only a watch on what the tabs show can tell of it.
func TestTabsMissing(t *testing.T) {
	t.Parallel()
	td, p := startEditing(t, "")
	dir := p.Worktrees[0].Path
	work := filepath.Join(t.TempDir(), "work")
	gittest.Git(t, dir, "worktree", "add", "-q", work, "-b", "work")
	writeIn(t, work, "b.go", "package b\n")
	writeIn(t, work, ".gitignore", "*.log\n")
	writeIn(t, work, "x.log", "x\n")
	diff, workName := "a.go", "work"
	td.replaceTabs(t, "api", fileTab("main", "a.go"), api.Tab{Kind: api.TabKindDiff, Worktree: optional("main"), Path: &diff},
		fileTab("work", "b.go"), api.Tab{Kind: api.TabKindPr, Worktree: &workName}, fileTab("work", "x.log"))
	events, _ := td.followWith(t, "api", &api.StreamProjectParams{Worktrees: optional(true)})
	missing := func(what string, want ...bool) {
		t.Helper()
		waitEvent(t, events, what, func(e event) bool { return e.name == "project" && slices.Equal(missingIn(e), want) })
	}
	waitEvent(t, events, "the worktrees read, all there", func(e event) bool {
		_, main := worktreeStatus(e, "main")
		_, work := worktreeStatus(e, "work")
		return main && work && slices.Equal(missingIn(e), []bool{false, false, false, false, false})
	})

	if err := os.Remove(filepath.Join(dir, "a.go")); err != nil {
		t.Fatal(err)
	}
	missing("a.go gone, its diff still showing the deletion", true, false, false, false, false)
	writeIn(t, dir, "a.go", "package a\n")
	missing("a.go back", false, false, false, false, false)

	if err := os.Remove(filepath.Join(work, "x.log")); err != nil {
		t.Fatal(err)
	}
	missing("the ignored file gone", false, false, false, false, true)

	if err := os.RemoveAll(work); err != nil {
		t.Fatal(err)
	}
	missing("the worktree's directory gone", false, false, true, true, true)
}
