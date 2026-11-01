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
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/waittest"
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
	on := true
	u, err := td.client.GetWorktreeDiffWithResponse(ctx, "work", &api.GetWorktreeDiffParams{Project: &project, Uncommitted: &on})
	if err != nil || u.JSON200 == nil || !strings.Contains(u.JSON200.Patch, "+two") || strings.Contains(u.JSON200.Patch, "+committed") || u.JSON200.Base != nil {
		t.Fatalf("uncommitted: %v, %s", err, u.Body)
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

// TestVerifiedMark follows a mark from the API to the stream, through a commit, and
// away when the file is edited.
func TestVerifiedMark(t *testing.T) {
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
	writeIn(t, work, "file", "two\n")

	on := true
	events, _ := td.followWith(t, "api", &api.StreamProjectParams{Worktrees: &on})
	file := func(e event) (api.WorktreeFile, bool) {
		w, ok := worktreeStatus(e, "work")
		if !ok || len(w.Files) != 1 {
			return api.WorktreeFile{}, false
		}
		return w.Files[0], true
	}
	waitEvent(t, events, "the file", func(e event) bool { _, ok := file(e); return ok })

	ctx := t.Context()
	project := "api"
	params := &api.VerifyWorktreeFileParams{Project: &project, Path: "file"}
	r, err := td.client.VerifyWorktreeFileWithResponse(ctx, "work", params, api.WorktreeFileVerify{})
	if err != nil || r.JSON200 == nil || !r.JSON200.Verified {
		t.Fatalf("verify: %v, %s", err, r.Body)
	}
	waitEvent(t, events, "the mark", func(e event) bool { f, ok := file(e); return ok && f.Verified })

	// A commit of what was verified keeps it.
	gittest.Git(t, work, "add", "file")
	gittest.Git(t, work, "commit", "-qm", "two")
	waitEvent(t, events, "the mark on the commit", func(e event) bool {
		w, ok := worktreeStatus(e, "work")
		return ok && len(w.Files) == 0 && len(w.CommittedFiles) == 1 && w.CommittedFiles[0].Verified
	})

	// An edit takes it away, and going back does not bring it.
	writeIn(t, work, "file", "three\n")
	gittest.Git(t, work, "add", "file")
	waitEvent(t, events, "the mark gone", func(e event) bool { f, ok := file(e); return ok && !f.Verified })
	writeIn(t, work, "file", "two\n")
	gittest.Git(t, work, "add", "file")
	waitEvent(t, events, "the file as committed", func(e event) bool {
		w, ok := worktreeStatus(e, "work")
		return ok && len(w.Files) == 0
	})
	list, err := td.client.ListWorktreesWithResponse(ctx, &api.ListWorktreesParams{Project: &project})
	if err != nil || list.JSON200 == nil {
		t.Fatalf("list: %v", err)
	}
	for _, w := range list.JSON200.Worktrees {
		for _, f := range w.CommittedFiles {
			if f.Verified {
				t.Errorf("the mark came back: %+v", f)
			}
		}
	}

	// What was on the screen is what it holds: an edit since makes it stale at once.
	stale := "0123456789abcdef0123456789abcdef01234567"
	writeIn(t, work, "file", "four\n")
	if r, err = td.client.VerifyWorktreeFileWithResponse(ctx, "work", params, api.WorktreeFileVerify{ContentId: &stale}); err != nil || r.JSON200 == nil {
		t.Fatalf("verify: %v, %s", err, r.Body)
	}
	if u, err := td.client.RefreshWorktreesWithResponse(ctx, "api", &api.RefreshWorktreesParams{}); err != nil || u.StatusCode() != http.StatusAccepted {
		t.Fatalf("refresh: %v", err)
	}
	waitEvent(t, events, "the edit", func(e event) bool { f, ok := file(e); return ok && !f.Verified && f.Size == 5 })

	// Taken away by hand, and refused for what is not a file of the worktree.
	if r, err = td.client.VerifyWorktreeFileWithResponse(ctx, "work", params, api.WorktreeFileVerify{}); err != nil || r.JSON200 == nil {
		t.Fatalf("verify: %v, %s", err, r.Body)
	}
	waitEvent(t, events, "the mark again", func(e event) bool { f, ok := file(e); return ok && f.Verified })
	d, err := td.client.UnverifyWorktreeFileWithResponse(ctx, "work", &api.UnverifyWorktreeFileParams{Project: &project, Path: "file"})
	if err != nil || d.JSON200 == nil || d.JSON200.Verified {
		t.Fatalf("unverify: %v, %s", err, d.Body)
	}
	waitEvent(t, events, "no mark", func(e event) bool { f, ok := file(e); return ok && !f.Verified })
	outside := &api.VerifyWorktreeFileParams{Project: &project, Path: "../x"}
	if r, err := td.client.VerifyWorktreeFileWithResponse(ctx, "work", outside, api.WorktreeFileVerify{}); err != nil || r.StatusCode() != http.StatusUnprocessableEntity {
		t.Errorf("outside: %v, %s", err, r.Body)
	}
}

// TestWorktreeReview follows comments from a draft through a review: nothing is sent
// with no agent to take it, and they go together when one does.
func TestWorktreeReview(t *testing.T) {
	t.Parallel()
	td := start(t)
	p := td.create(t, "api")
	dir := p.Worktrees[0].Path
	writeIn(t, dir, "file", "one\n")
	on := true
	events, _ := td.followWith(t, "api", &api.StreamProjectParams{Worktrees: &on})
	waitEvent(t, events, "the clone", func(e event) bool { _, ok := worktreeStatus(e, "main"); return ok })

	ctx := t.Context()
	project := "api"
	quote := "one"
	create := &api.CreateWorktreeCommentParams{Project: &project}
	add := func(body string, line int) api.WorktreeComment {
		t.Helper()
		r, err := td.client.CreateWorktreeCommentWithResponse(ctx, "main", create, api.WorktreeCommentCreate{
			File: "file", Line: line, Side: api.WorktreeCommentCreateSideNew, View: api.WorktreeCommentCreateViewUncommitted, Quote: &quote, Body: body,
		})
		if err != nil || r.JSON201 == nil {
			t.Fatalf("add: %v, %s", err, r.Body)
		}
		return r.JSON201.Comment
	}
	if r, err := td.client.CreateWorktreeCommentWithResponse(ctx, "main", create, api.WorktreeCommentCreate{File: "file", Line: 1, Side: api.WorktreeCommentCreateSideNew, View: api.WorktreeCommentCreateViewUncommitted, Body: "  "}); err != nil || r.StatusCode() != http.StatusUnprocessableEntity {
		t.Errorf("empty: %v, %s", err, r.Body)
	}
	if r, err := td.client.CreateWorktreeCommentWithResponse(ctx, "main", create, api.WorktreeCommentCreate{File: "../x", Line: 1, Side: api.WorktreeCommentCreateSideNew, View: api.WorktreeCommentCreateViewUncommitted, Body: "x"}); err != nil || r.StatusCode() != http.StatusUnprocessableEntity {
		t.Errorf("outside: %v, %s", err, r.Body)
	}
	first := add("keep it", 1)
	second := add("to delete", 2)
	waitEvent(t, events, "both", func(e event) bool {
		w, ok := worktreeStatus(e, "main")
		return ok && len(w.Comments) == 2
	})

	// Edited and deleted before it goes.
	if r, err := td.client.EditWorktreeCommentWithResponse(ctx, "main", first.Id, &api.EditWorktreeCommentParams{Project: &project}, api.WorktreeCommentEdit{Body: "keep the check"}); err != nil || r.StatusCode() != http.StatusOK {
		t.Fatalf("edit: %v, %s", err, r.Body)
	}
	if r, err := td.client.DeleteWorktreeCommentWithResponse(ctx, "main", second.Id, &api.DeleteWorktreeCommentParams{Project: &project}); err != nil || r.StatusCode() != http.StatusOK {
		t.Fatalf("delete: %v, %s", err, r.Body)
	}
	waitEvent(t, events, "one, edited", func(e event) bool {
		w, ok := worktreeStatus(e, "main")
		return ok && len(w.Comments) == 1 && w.Comments[0].Body == "keep the check"
	})

	// An edit to the file says so.
	writeIn(t, dir, "file", "two\n")
	if r, err := td.client.RefreshWorktreesWithResponse(ctx, "api", &api.RefreshWorktreesParams{}); err != nil || r.StatusCode() != http.StatusAccepted {
		t.Fatalf("refresh: %v", err)
	}
	waitEvent(t, events, "the file changed", func(e event) bool {
		w, ok := worktreeStatus(e, "main")
		return ok && len(w.Comments) == 1 && w.Comments[0].Changed
	})

	// With no agent to take it, it stays a draft.
	review := &api.SendWorktreeReviewParams{Project: &project}
	if r, err := td.client.SendWorktreeReviewWithResponse(ctx, "main", review, api.WorktreeReview{Agent: "nobody"}); err != nil || r.StatusCode() != http.StatusNotFound {
		t.Fatalf("no agent: %v, %s", err, r.Body)
	}
	list, err := td.client.ListWorktreeCommentsWithResponse(ctx, "main", &api.ListWorktreeCommentsParams{Project: &project})
	if err != nil || list.JSON200 == nil || len(list.JSON200.Comments) != 1 || list.JSON200.Comments[0].State != api.WorktreeCommentStateDraft {
		t.Fatalf("after a refusal: %v, %s", err, list.Body)
	}

	// Sent, it leaves the drafts, and there is nothing left to send.
	a := td.createAgent(t, "api", dir, "reviewer")
	r, err := td.client.SendWorktreeReviewWithResponse(ctx, "main", review, api.WorktreeReview{Agent: "id:" + a.Id})
	if err != nil || r.JSON200 == nil || r.JSON200.Comments != 1 || r.JSON200.Result != api.ResultQueued {
		t.Fatalf("send: %v, %s", err, r.Body)
	}
	waitEvent(t, events, "no drafts", func(e event) bool {
		w, ok := worktreeStatus(e, "main")
		return ok && len(w.Comments) == 0
	})
	list, err = td.client.ListWorktreeCommentsWithResponse(ctx, "main", &api.ListWorktreeCommentsParams{Project: &project})
	if err != nil || list.JSON200 == nil || len(list.JSON200.Comments) != 1 || list.JSON200.Comments[0].State != api.WorktreeCommentStateSent {
		t.Fatalf("after sending: %v, %s", err, list.Body)
	}
	if r, err := td.client.SendWorktreeReviewWithResponse(ctx, "main", review, api.WorktreeReview{Agent: "id:" + a.Id}); err != nil || r.StatusCode() != http.StatusUnprocessableEntity {
		t.Errorf("nothing to send: %v, %s", err, r.Body)
	}
}

// A file's contents and what is said of them reach the logs no more than a terminal's
// output does, at the debug level too, which logs what SQL is given.
func TestWorktreeContentsAreNotLogged(t *testing.T) {
	t.Parallel()
	const inFile, inComment, inNote = "the-secret-in-a-file", "the-secret-in-a-comment", "the-secret-in-a-note"
	td := start(t)
	p := td.create(t, "api")
	dir := p.Worktrees[0].Path
	writeIn(t, dir, "file", inFile+"\n")
	ctx := t.Context()
	project := "api"
	quote := inFile
	if r, err := td.client.CreateWorktreeCommentWithResponse(ctx, "main", &api.CreateWorktreeCommentParams{Project: &project}, api.WorktreeCommentCreate{
		File: "file", Line: 1, Side: api.WorktreeCommentCreateSideNew, View: api.WorktreeCommentCreateViewUncommitted, Quote: &quote, Body: inComment,
	}); err != nil || r.JSON201 == nil {
		t.Fatalf("comment: %v, %s", err, r.Body)
	}
	if r, err := td.client.GetWorktreeFileDiffWithResponse(ctx, "main", &api.GetWorktreeFileDiffParams{Project: &project, Path: "file"}); err != nil || r.JSON200 == nil || !strings.Contains(r.JSON200.Patch, inFile) {
		t.Fatalf("file diff: %v, %s", err, r.Body)
	}
	on := true
	if r, err := td.client.GetWorktreeDiffWithResponse(ctx, "main", &api.GetWorktreeDiffParams{Project: &project, Uncommitted: &on}); err != nil || r.JSON200 == nil || !strings.Contains(r.JSON200.Patch, inFile) {
		t.Fatalf("diff: %v, %s", err, r.Body)
	}
	if r, err := td.client.ListWorktreeCommentsWithResponse(ctx, "main", &api.ListWorktreeCommentsParams{Project: &project}); err != nil || r.JSON200 == nil || r.JSON200.Comments[0].Body != inComment {
		t.Fatalf("comments: %v, %s", err, r.Body)
	}
	if r, err := td.client.ListWorktreesWithResponse(ctx, &api.ListWorktreesParams{Project: &project}); err != nil || r.JSON200 == nil || len(r.JSON200.Worktrees[0].Comments) != 1 {
		t.Fatalf("list: %v, %s", err, r.Body)
	}
	a := td.createAgent(t, "api", dir, "reviewer")
	if r, err := td.client.SendWorktreeReviewWithResponse(ctx, "main", &api.SendWorktreeReviewParams{Project: &project}, api.WorktreeReview{Agent: "id:" + a.Id, Note: optional(inNote)}); err != nil || r.JSON200 == nil {
		t.Fatalf("review: %v, %s", err, r.Body)
	}
	logs, err := filepath.Glob(filepath.Join(identity.LogsDir(td.home), "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	read := func() string {
		var all strings.Builder
		for _, name := range logs {
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			all.Write(data)
		}
		return all.String()
	}
	waittest.For(t, "the review's answer to be logged", func() bool {
		var ok bool
		logs, _ = filepath.Glob(filepath.Join(identity.LogsDir(td.home), "*.log"))
		for line := range strings.Lines(read()) {
			ok = ok || (strings.Contains(line, "request returned") && strings.Contains(line, "/reviews"))
		}
		return ok
	})
	got := read()
	for _, secret := range []string{inFile, inComment, inNote} {
		if strings.Contains(got, secret) {
			t.Errorf("%s is in the logs", secret)
		}
	}
}

func TestRemoveWorktree(t *testing.T) {
	t.Parallel()
	td := start(t)
	p := td.create(t, "api")
	dir := p.Worktrees[0].Path
	gittest.Git(t, dir, "branch", "-M", "main")
	writeIn(t, dir, "file", "one\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-qm", "base")
	ctx := t.Context()
	project := "api"
	created, err := td.client.CreateAgentWithResponse(ctx, api.CreateAgent{Project: &project, Dir: dir, Name: optional("worker"), Worktree: optional("work")})
	if err != nil || created.JSON201 == nil {
		t.Fatalf("create: %v, %s", err, created.Body)
	}
	a := created.JSON201.Agent
	work := a.Dir
	writeIn(t, work, "more", "committed\n")
	gittest.Git(t, work, "add", ".")
	gittest.Git(t, work, "commit", "-qm", "work")
	elsewhere := td.createAgent(t, "api", dir, "elsewhere")

	params := &api.RemoveWorktreeParams{Project: &project}
	refused := func(name string, code api.ErrorCode) {
		t.Helper()
		r, err := td.client.RemoveWorktreeWithResponse(ctx, name, params)
		if err != nil || r.ApplicationproblemJSONDefault == nil || r.ApplicationproblemJSONDefault.Code != code {
			t.Fatalf("%s: want %s: %v, %s", name, code, err, r.Body)
		}
	}
	refused("work", api.ErrorCodeWorktreeNotLanded)
	refused("main", api.ErrorCodeWorktreeIsClone)
	refused("nope", api.ErrorCodeNotFound)
	if _, err := os.Stat(work); err != nil {
		t.Fatalf("a refusal removed it: %v", err)
	}

	gittest.Git(t, dir, "merge", "-q", "--ff-only", "work")
	r, err := td.client.RemoveWorktreeWithResponse(ctx, "work", params)
	if err != nil || r.JSON200 == nil {
		t.Fatalf("remove: %v, %s", err, r.Body)
	}
	got := r.JSON200
	if got.Result != api.ResultRemoved || got.Worktree != "work" || got.Path != work || got.BranchDeleted == nil || *got.BranchDeleted != "work" || got.BranchKept != nil {
		t.Errorf("answer: %s", r.Body)
	}
	if len(got.Notified) != 1 || got.Notified[0] != a.Name {
		t.Errorf("told %v, want only %s and not %s", got.Notified, a.Name, elsewhere.Name)
	}
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Errorf("the directory: %v", err)
	}
	list, err := td.client.ListWorktreesWithResponse(ctx, &api.ListWorktreesParams{Project: &project})
	if err != nil || list.JSON200 == nil || len(list.JSON200.Worktrees) != 1 {
		t.Errorf("list: %v, %s", err, list.Body)
	}
}
