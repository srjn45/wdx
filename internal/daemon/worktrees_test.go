package daemon

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/gittest"
)

func worktreeStatus(e event, name string) (api.WorktreeStatus, bool) {
	if e.project.WorktreeStatuses == nil {
		return api.WorktreeStatus{}, false
	}
	list := *e.project.WorktreeStatuses
	i := slices.IndexFunc(list, func(w api.WorktreeStatus) bool { return w.Name == name })
	if i < 0 {
		return api.WorktreeStatus{}, false
	}
	return list[i], true
}

func writeIn(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasFile(w api.WorktreeStatus, path string) bool {
	return slices.ContainsFunc(w.Files, func(f api.WorktreeFile) bool { return f.Path == path })
}

// TestStreamWorktrees follows a project asking for its worktrees: they come after
// the first event, which waits for no git, and an event shows what git changed.
func TestStreamWorktrees(t *testing.T) {
	t.Parallel()
	td := start(t)
	p := td.create(t, "api")
	dir := p.Worktrees[0].Path
	on := true
	events, _ := td.followWith(t, "api", &api.StreamProjectParams{Worktrees: &on})
	waitEvent(t, events, "the clone", func(e event) bool { _, ok := worktreeStatus(e, "main"); return ok })

	writeIn(t, dir, "staged", "s\n")
	gittest.Git(t, dir, "add", "staged")
	waitEvent(t, events, "the file staged", func(e event) bool {
		w, ok := worktreeStatus(e, "main")
		return ok && hasFile(w, "staged")
	})

	// An edit git's files say nothing of, until asked.
	writeIn(t, dir, "edited", "e\n")
	if r, err := td.client.RefreshWorktreesWithResponse(t.Context(), "api", &api.RefreshWorktreesParams{}); err != nil || r.StatusCode() != http.StatusAccepted {
		t.Fatalf("refresh: %v, %s", err, r.Body)
	}
	waitEvent(t, events, "the file edited", func(e event) bool {
		w, ok := worktreeStatus(e, "main")
		return ok && hasFile(w, "edited")
	})

	// A worktree git made, which wdx never heard of.
	other := filepath.Join(t.TempDir(), "other")
	gittest.Git(t, dir, "worktree", "add", "-q", other, "-b", "other")
	waitEvent(t, events, "the worktree", func(e event) bool { _, ok := worktreeStatus(e, "other"); return ok })
}

// TestStreamWithoutWorktrees is the promise to clients that do not ask.
func TestStreamWithoutWorktrees(t *testing.T) {
	t.Parallel()
	td := start(t)
	p := td.create(t, "api")
	events, _ := td.follow(t, "api")
	first := waitEvent(t, events, "the project", func(e event) bool { return e.name == "project" })
	if first.project.WorktreeStatuses != nil {
		t.Errorf("sent unasked: %+v", first.project.WorktreeStatuses)
	}
	if got := td.daemon.tracker.Statuses(p.Id); got != nil {
		t.Errorf("kept unasked: %+v", got)
	}
}

// TestRefreshDoesNotWaitForChanges holds the daemon's one change at a time, as a
// long clone does: the refresh is answered all the same.
func TestRefreshDoesNotWaitForChanges(t *testing.T) {
	t.Parallel()
	td := start(t)
	td.create(t, "api")
	td.daemon.changes.Lock()
	defer td.daemon.changes.Unlock()
	if r, err := td.client.RefreshWorktreesWithResponse(t.Context(), "api", &api.RefreshWorktreesParams{}); err != nil || r.StatusCode() != http.StatusAccepted {
		t.Fatalf("refresh: %v, %s", err, r.Body)
	}
}

func TestWorktreeRequests(t *testing.T) {
	t.Parallel()
	td := start(t)
	p := td.create(t, "api")
	dir := p.Worktrees[0].Path
	gittest.Git(t, dir, "branch", "-M", "main")
	writeIn(t, dir, "file", "one\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-qm", "base")
	work := filepath.Join(t.TempDir(), "work")
	gittest.Git(t, dir, "worktree", "add", "-q", work, "-b", "work")
	writeIn(t, work, "landed", "committed\n")
	gittest.Git(t, work, "add", ".")
	gittest.Git(t, work, "commit", "-qm", "work")
	writeIn(t, work, "file", "two\n")

	ctx := context.Background()
	project := "api"
	list, err := td.client.ListWorktreesWithResponse(ctx, &api.ListWorktreesParams{Project: &project})
	if err != nil || list.JSON200 == nil || len(list.JSON200.Worktrees) != 2 {
		t.Fatalf("list: %v, %s", err, list.Body)
	}
	byDir, err := td.client.ListWorktreesWithResponse(ctx, &api.ListWorktreesParams{Dir: &work})
	if err != nil || byDir.JSON200 == nil || len(byDir.JSON200.Worktrees) != 2 {
		t.Fatalf("list by a dir git knows and wdx does not: %v, %s", err, byDir.Body)
	}

	d, err := td.client.GetWorktreeDiffWithResponse(ctx, "work", &api.GetWorktreeDiffParams{Project: &project})
	if err != nil || d.JSON200 == nil || !strings.Contains(d.JSON200.Patch, "+committed") || strings.Contains(d.JSON200.Patch, "+two") || d.JSON200.Base == nil || *d.JSON200.Base != "main" {
		t.Fatalf("committed: %v, %s", err, d.Body)
	}
	f, err := td.client.GetWorktreeFileDiffWithResponse(ctx, "work", &api.GetWorktreeFileDiffParams{Project: &project, Path: "file"})
	if err != nil || f.JSON200 == nil || !strings.Contains(f.JSON200.Patch, "+two") {
		t.Fatalf("file: %v, %s", err, f.Body)
	}

	for _, tc := range []struct {
		what string
		get  func() (*http.Response, []byte, error)
		want int
		code api.ErrorCode
	}{
		{"no project", func() (*http.Response, []byte, error) {
			r, err := td.client.ListWorktreesWithResponse(ctx, &api.ListWorktreesParams{})
			return r.HTTPResponse, r.Body, err
		}, http.StatusBadRequest, api.ErrorCodeUsage},
		{"an unknown worktree", func() (*http.Response, []byte, error) {
			r, err := td.client.GetWorktreeDiffWithResponse(ctx, "nope", &api.GetWorktreeDiffParams{Project: &project})
			return r.HTTPResponse, r.Body, err
		}, http.StatusNotFound, api.ErrorCodeNotFound},
		{"a path outside", func() (*http.Response, []byte, error) {
			r, err := td.client.GetWorktreeFileDiffWithResponse(ctx, "work", &api.GetWorktreeFileDiffParams{Project: &project, Path: "../x"})
			return r.HTTPResponse, r.Body, err
		}, http.StatusUnprocessableEntity, api.ErrorCodeInvalidPath},
	} {
		resp, body, err := tc.get()
		if err != nil || resp.StatusCode != tc.want || !strings.Contains(string(body), string(tc.code)) {
			t.Errorf("%s: %v, %s", tc.what, err, body)
		}
	}
}
