package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/gh/ghtest"
	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/waittest"
)

// worktreeOutput is what the commands of wdx worktree print with --json.
type worktreeOutput struct {
	api.WorktreeList
	api.Problem
	Patch string `json:"patch"`
}

func wdxWorktree(t *testing.T, home string, dir string, args ...string) (worktreeOutput, int) {
	t.Helper()
	out := wdxWith(t, home, in(dir), append(append([]string{"worktree"}, args...), "--json")...)
	var v worktreeOutput
	if err := json.Unmarshal([]byte(out.stdout), &v); err != nil {
		t.Fatalf("%v: %v in %q", args, err, out.stdout)
	}
	return v, out.exit
}

// worktreeProject is a project with a worktree with a commit ahead and a file
// changed.
func worktreeProject(t *testing.T, home string) (app, work string) {
	t.Helper()
	app = newProjectDir(t, home, "app")
	gittest.Git(t, app, "branch", "-M", "main")
	if err := os.WriteFile(filepath.Join(app, "file"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, app, "add", ".")
	gittest.Git(t, app, "commit", "-qm", "base")
	work = filepath.Join(filepath.Dir(app), "work")
	gittest.Git(t, app, "worktree", "add", "-q", work, "-b", "work") // not by wdx: git's list is what counts
	if err := os.WriteFile(filepath.Join(work, "landing"), []byte("committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, work, "add", ".")
	gittest.Git(t, work, "commit", "-qm", "work")
	if err := os.WriteFile(filepath.Join(work, "file"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return app, work
}

func TestWorktreeList(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app, _ := worktreeProject(t, home)

	for _, tc := range []struct {
		dir  string
		args []string
	}{{app, nil}, {t.TempDir(), []string{"--project-name", "app"}}} {
		v, exit := wdxWorktree(t, home, tc.dir, append([]string{"list"}, tc.args...)...)
		if exit != clikit.ExitOK || len(v.Worktrees) != 2 {
			t.Fatalf("%v: exit %d, %+v", tc.args, exit, v)
		}
		w := v.Worktrees[slices.IndexFunc(v.Worktrees, func(s api.WorktreeStatus) bool { return s.Name == "work" })]
		if w.Ahead == nil || *w.Ahead != 1 || len(w.Files) != 1 || w.Files[0].Path != "file" || w.Files[0].Status != api.WorktreeFileStatusM {
			t.Errorf("work: %+v", w)
		}
	}

	out := wdxWith(t, home, in(app), "worktree", "list")
	if out.exit != clikit.ExitOK || !strings.Contains(out.stdout, "NAME") || !strings.Contains(out.stdout, "work") {
		t.Errorf("text: %+v", out)
	}
}

func TestWorktreeListOutsideAProject(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	v, exit := wdxJSONWith(t, home, in(t.TempDir()), "worktree", "list")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	wantHint(t, v, "run it in a project's worktree, or pass --project-name or --project-id")
}

func TestWorktreeDiff(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app, _ := worktreeProject(t, home)

	v, exit := wdxWorktree(t, home, app, "diff", "--name", "work")
	if exit != clikit.ExitOK || !strings.Contains(v.Patch, "+committed") || strings.Contains(v.Patch, "+two") {
		t.Errorf("committed: exit %d, %q", exit, v.Patch)
	}
	v, exit = wdxWorktree(t, home, app, "diff", "--name", "work", "--path", "file")
	if exit != clikit.ExitOK || !strings.Contains(v.Patch, "+two") || strings.Contains(v.Patch, "+committed") {
		t.Errorf("uncommitted: exit %d, %q", exit, v.Patch)
	}
	v, exit = wdxWorktree(t, home, app, "diff", "--name", "work", "--uncommitted")
	if exit != clikit.ExitOK || !strings.Contains(v.Patch, "+two") || strings.Contains(v.Patch, "+committed") {
		t.Errorf("all uncommitted: exit %d, %q", exit, v.Patch)
	}
	out := wdxWith(t, home, in(app), "worktree", "diff", "--name", "work")
	if out.exit != clikit.ExitOK || !strings.Contains(out.stdout, "+committed") {
		t.Errorf("text: %+v", out)
	}
}

func TestWorktreeDiffRefused(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app, _ := worktreeProject(t, home)

	v, exit := wdxJSONWith(t, home, in(app), "worktree", "diff", "--name", "nope")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "worktree", "diff", "--name", "work", "--path", "../outside")
	wantError(t, v, exit, api.ErrorCodeInvalidPath, clikit.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "worktree", "diff", "--name", "work")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
}

func TestWorktreeComments(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app, _ := worktreeProject(t, home)
	v, exit := wdxWorktree(t, home, app, "comments", "--name", "work")
	if exit != clikit.ExitOK || !strings.Contains(wdxWith(t, home, in(app), "worktree", "comments", "--name", "work").stdout, "no comments") {
		t.Errorf("none: exit %d, %+v", exit, v)
	}
	e, exit := wdxJSONWith(t, home, in(app), "worktree", "comments", "--name", "nope")
	wantError(t, e, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
}

func TestMessageCommands(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	send := func() {
		t.Helper()
		if out := wdxWith(t, home, in(app), "agent", "send", "--name", "helper", "--prompt", "a secret text"); out.exit != clikit.ExitOK {
			t.Fatalf("send: %+v", out)
		}
	}
	if out := wdxWith(t, home, in(app), "agent", "create", "--name", "helper"); out.exit != clikit.ExitOK {
		t.Fatalf("create: %+v", out)
	}
	if out := wdxWith(t, home, in(app), "message", "list"); out.exit != clikit.ExitOK || !strings.Contains(out.stdout, "no messages waiting") {
		t.Errorf("none: %+v", out)
	}
	send()
	out := wdxWith(t, home, in(app), "message", "list", "--json")
	var list api.MessageList
	if err := json.Unmarshal([]byte(out.stdout), &list); err != nil || len(list.Messages) != 1 || list.Messages[0].Text != "a secret text" {
		t.Fatalf("list: %v, %s", err, out.stdout)
	}
	id := list.Messages[0].Id
	if out := wdxWith(t, home, in(app), "message", "list"); !strings.Contains(out.stdout, "a secret text") || !strings.Contains(out.stdout, "pending") {
		t.Errorf("text: %+v", out)
	}
	v, exit := wdxJSONWith(t, home, in(app), "message", "resend", "--id", id)
	if exit != clikit.ExitOK || v.Result != "queued" {
		t.Errorf("resend: %d, %+v", exit, v)
	}
	if out := wdxWith(t, home, in(app), "message", "resend", "--id", id); out.exit != clikit.ExitOK || !strings.Contains(out.stdout, "queued for helper") {
		t.Errorf("resend text: %+v", out)
	}
	v, exit = wdxJSONWith(t, home, in(app), "message", "discard", "--id", id)
	if exit != clikit.ExitOK || v.Result != "removed" {
		t.Errorf("discard: %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(app), "message", "discard", "--id", id)
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "message", "resend", "--id", "msg-nope")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "message", "list")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	send()
	if out := wdxWith(t, home, in(app), "message", "discard", "--id", "msg-nope"); out.exit != clikit.ExitRejected {
		t.Errorf("text failure: %+v", out)
	}
}

func TestWorktreeRemove(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app, work := worktreeProject(t, home)

	// With a file changed and a commit ahead, nothing goes.
	v, exit := wdxWorktree(t, home, app, "remove", "--name", "work")
	if exit != clikit.ExitRejected || v.Code != api.ErrorCodeWorktreeNotLanded {
		t.Fatalf("not landed: exit %d, %+v", exit, v)
	}
	if _, err := os.Stat(work); err != nil {
		t.Fatal(err)
	}
	v, exit = wdxWorktree(t, home, app, "remove", "--name", "main")
	if exit != clikit.ExitRejected || v.Code != api.ErrorCodeWorktreeIsClone {
		t.Fatalf("the clone: exit %d, %+v", exit, v)
	}
	v, exit = wdxWorktree(t, home, app, "remove", "--name", "nope")
	if exit != clikit.ExitRejected || v.Code != api.ErrorCodeNotFound {
		t.Fatalf("unknown: exit %d, %+v", exit, v)
	}

	gittest.Git(t, work, "checkout", "-q", "--", "file")
	gittest.Git(t, app, "merge", "-q", "--ff-only", "work")
	out := wdxWith(t, home, in(app), "worktree", "remove", "--name", "work")
	if out.exit != clikit.ExitOK || !strings.Contains(out.stdout, "removed worktree work") || !strings.Contains(out.stdout, "branch work deleted") {
		t.Fatalf("removed: %+v", out)
	}
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Errorf("the directory: %v", err)
	}
}

// A worktree wdx made is in the project's records too, which must not bring it back
// as missing.
func TestWorktreeRemoveForgetsWhatWdxMade(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	repo := gittest.Repo(t, "app")
	gittest.Git(t, repo, "branch", "-M", "main")
	if v, exit := wdxJSON(t, home, "project", "open", "--local", repo, "--name", "app"); exit != clikit.ExitOK {
		t.Fatalf("open: exit %d, %+v", exit, v)
	}
	if v, exit := wdxJSONWith(t, home, in(repo), "agent", "create", "--name", "fixer", "--worktree", "fix"); exit != clikit.ExitOK {
		t.Fatalf("agent: exit %d, %+v", exit, v)
	}
	out := wdxWith(t, home, in(repo), "worktree", "remove", "--name", "fix", "--json")
	var r api.WorktreeRemoved
	if err := json.Unmarshal([]byte(out.stdout), &r); err != nil || out.exit != clikit.ExitOK {
		t.Fatalf("remove: %+v, %v", out, err)
	}
	if r.Result != api.ResultRemoved || len(r.Notified) != 1 || r.Notified[0] != "fixer" {
		t.Errorf("answer: %+v", r)
	}
	v, exit := wdxWorktree(t, home, repo, "list")
	if exit != clikit.ExitOK || len(v.Worktrees) != 1 || v.Worktrees[0].Name != "main" {
		t.Errorf("list: exit %d, %+v", exit, v.Worktrees)
	}
}

func TestWorktreeLand(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app, work := worktreeProject(t, home)

	// Nothing is said yet of how it lands.
	v, exit := wdxWorktree(t, home, app, "land", "--name", "work")
	if exit != clikit.ExitRejected || v.Code != api.ErrorCodeLandNotConfigured || v.Hint == nil || !strings.Contains(*v.Hint, "project landing") {
		t.Fatalf("not configured: exit %d, %+v", exit, v)
	}
	shown := wdxWith(t, home, in(app), "project", "landing", "--name", "app", "--json")
	var s api.ProjectLanding
	if err := json.Unmarshal([]byte(shown.stdout), &s); err != nil || shown.exit != clikit.ExitOK || s.Configured {
		t.Fatalf("shown: %+v, %v", shown, err)
	}
	if out := wdxWith(t, home, in(app), "project", "landing", "--name", "app", "--push"); out.exit != clikit.ExitUsage {
		t.Fatalf("--push alone: %+v", out)
	}
	if out := wdxWith(t, home, in(app), "project", "landing", "--name", "app", "--no-check"); out.exit != clikit.ExitOK || !strings.Contains(out.stdout, "check: none") {
		t.Fatalf("set: %+v", out)
	}

	// With a file changed, it does not start.
	v, exit = wdxWorktree(t, home, app, "land", "--name", "work")
	if exit != clikit.ExitRejected || v.Code != api.ErrorCodeNotCommitted {
		t.Fatalf("not committed: exit %d, %+v", exit, v)
	}
	gittest.Git(t, work, "checkout", "-q", "--", "file")

	// A check that fails stops it, with the code and what to read.
	if out := wdxWith(t, home, in(app), "project", "landing", "--name", "app", "--check", "exit 4", "--json"); out.exit != clikit.ExitOK || !strings.Contains(out.stdout, "exit 4") {
		t.Fatalf("set: %+v", out)
	}
	v, exit = wdxWorktree(t, home, app, "land", "--name", "work")
	if exit != clikit.ExitRejected || v.Code != api.ErrorCodeCheckFailed || v.Hint == nil || !strings.Contains(*v.Hint, "land-work") {
		t.Fatalf("check failed: exit %d, %+v", exit, v)
	}

	// It lands, and says so.
	if out := wdxWith(t, home, in(app), "project", "landing", "--name", "app", "--check", "true"); out.exit != clikit.ExitOK {
		t.Fatalf("set: %+v", out)
	}
	out := wdxWith(t, home, in(app), "worktree", "land", "--name", "work")
	if out.exit != clikit.ExitOK || !strings.Contains(out.stdout, "landed worktree work on main") {
		t.Fatalf("land: %+v", out)
	}
	if _, err := os.Stat(filepath.Join(app, "landing")); err != nil {
		t.Errorf("not in the clone: %v", err)
	}

	// Nothing is left, and the JSON of landing was a result.
	v, exit = wdxWorktree(t, home, app, "land", "--name", "work")
	if exit != clikit.ExitRejected || v.Code != api.ErrorCodeNothingToLand {
		t.Fatalf("again: exit %d, %+v", exit, v)
	}
}

func TestWorktreeLandByPR(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app, work := worktreeProject(t, home)
	gittest.Git(t, work, "checkout", "-q", "--", "file")
	gittest.Git(t, app, "init", "-q", "--bare", filepath.Join(home, "origin.git"))
	gittest.Git(t, app, "remote", "add", "origin", filepath.Join(home, "origin.git"))

	if out := wdxWith(t, home, in(app), "project", "landing", "--name", "app", "--method", "pr"); out.exit != clikit.ExitUsage {
		t.Fatalf("--method alone: %+v", out)
	}
	if out := wdxWith(t, home, in(app), "project", "landing", "--name", "app", "--no-check", "--method", "squash"); out.exit != clikit.ExitUsage {
		t.Fatalf("an unknown method: %+v", out)
	}
	if out := wdxWith(t, home, in(app), "project", "landing", "--name", "app", "--no-check", "--method", "pr"); out.exit != clikit.ExitOK || !strings.Contains(out.stdout, "method: pr") {
		t.Fatalf("set: %+v", out)
	}

	out := wdxWith(t, home, in(app), "worktree", "land", "--name", "work", "--json")
	var r worktreeLanded
	if err := json.Unmarshal([]byte(out.stdout), &r); err != nil || out.exit != clikit.ExitOK {
		t.Fatalf("land: %+v, %v", out, err)
	}
	if r.Result != api.ResultPrOpened || r.PullRequest == nil || r.PullRequest.Number != 12 || r.PullRequest.Url == "" {
		t.Errorf("answer: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(app, "landing")); !os.IsNotExist(err) {
		t.Errorf("it reached the clone: %v", err)
	}

	// The PR at this commit merged: there is no more to land, and it can go.
	headOf, err := exec.Command("git", "-C", work, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(headOf))
	fake := ghtest.In(home)
	fake.Set(t, "MERGED", head)
	v, exit := wdxWorktree(t, home, app, "land", "--name", "work")
	if exit != clikit.ExitRejected || v.Code != api.ErrorCodePrFailed {
		t.Fatalf("merged: exit %d, %+v", exit, v)
	}
	removed := wdxWith(t, home, in(app), "worktree", "remove", "--name", "work")
	if removed.exit != clikit.ExitOK {
		t.Fatalf("remove: %+v", removed)
	}
}

// --method overrides the project's for one landing, and text says what opened.
func TestWorktreeLandMethodOnce(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app, work := worktreeProject(t, home)
	gittest.Git(t, work, "checkout", "-q", "--", "file")
	gittest.Git(t, app, "init", "-q", "--bare", filepath.Join(home, "origin.git"))
	gittest.Git(t, app, "remote", "add", "origin", filepath.Join(home, "origin.git"))
	if out := wdxWith(t, home, in(app), "project", "landing", "--name", "app", "--no-check"); out.exit != clikit.ExitOK {
		t.Fatalf("set: %+v", out)
	}
	if out := wdxWith(t, home, in(app), "worktree", "land", "--name", "work", "--method", "nope"); out.exit != clikit.ExitUsage {
		t.Fatalf("an unknown method: %+v", out)
	}
	out := wdxWith(t, home, in(app), "worktree", "land", "--name", "work", "--method", "pr")
	if out.exit != clikit.ExitOK || !strings.Contains(out.stdout, "PR #12 open: worktree work onto main") || !strings.Contains(out.stdout, "/pull/12") {
		t.Fatalf("land: %+v", out)
	}
	shown := wdxWith(t, home, in(app), "project", "landing", "--name", "app")
	if !strings.Contains(shown.stdout, "method: rebase") {
		t.Errorf("saved: %+v", shown)
	}
}

func TestWorktreeLandJSON(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app, work := worktreeProject(t, home)
	gittest.Git(t, work, "checkout", "-q", "--", "file")
	if out := wdxWith(t, home, in(app), "project", "landing", "--name", "app", "--no-check"); out.exit != clikit.ExitOK {
		t.Fatalf("set: %+v", out)
	}
	out := wdxWith(t, home, in(app), "worktree", "land", "--name", "work", "--json")
	var r worktreeLanded
	if err := json.Unmarshal([]byte(out.stdout), &r); err != nil || out.exit != clikit.ExitOK {
		t.Fatalf("land: %+v, %v", out, err)
	}
	if r.Result != api.ResultLanded || r.Worktree != "work" || r.Base != "main" || r.Commit == nil {
		t.Errorf("answer: %+v", r)
	}
}

// worktreeRebaseOutput is what wdx worktree rebase prints with --json.
type worktreeRebaseOutput struct {
	api.WorktreeRebased
	api.Problem
}

func wdxRebase(t *testing.T, home, dir, name string, args ...string) (worktreeRebaseOutput, int) {
	t.Helper()
	out := wdxWith(t, home, in(dir), append([]string{"worktree", "rebase", "--name", name, "--json"}, args...)...)
	var v worktreeRebaseOutput
	if err := json.Unmarshal([]byte(out.stdout), &v); err != nil {
		t.Fatalf("rebase %s: %v in %q", name, err, out.stdout)
	}
	return v, out.exit
}

func revParse(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func detailFiles(t *testing.T, p api.Problem, key string) []string {
	t.Helper()
	if p.Details == nil {
		t.Fatalf("no details: %+v", p)
	}
	var files []string
	for _, f := range (*p.Details)[key].([]any) {
		files = append(files, f.(string))
	}
	return files
}

func TestWorktreeRebase(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app, work := worktreeProject(t, home)
	commitIn := func(dir, file, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		gittest.Git(t, dir, "add", ".")
		gittest.Git(t, dir, "commit", "-qm", file)
	}

	// Nothing is stashed: an agent is to commit first.
	v, exit := wdxRebase(t, home, app, "work")
	if exit != clikit.ExitRejected || v.Code != api.ErrorCodeNotCommitted || v.Hint == nil || !strings.Contains(*v.Hint, "agent send") {
		t.Fatalf("not committed: exit %d, %+v", exit, v)
	}
	if files := detailFiles(t, v.Problem, "files"); !slices.Equal(files, []string{"file"}) {
		t.Errorf("not committed: files %v", files)
	}
	gittest.Git(t, work, "checkout", "-q", "--", "file")

	v, exit = wdxRebase(t, home, app, "work")
	if exit != clikit.ExitOK || v.Result != api.ResultUpToDate || v.Base != "main" || v.HeadBefore != v.HeadAfter || v.Pushed {
		t.Fatalf("up to date: exit %d, %+v", exit, v)
	}

	// On origin, it goes up again once rebased.
	origin := filepath.Join(t.TempDir(), "origin.git")
	gittest.Git(t, app, "init", "-q", "--bare", origin)
	gittest.Git(t, app, "remote", "add", "origin", origin)
	gittest.Git(t, app, "push", "-q", "origin", "main")
	gittest.Git(t, work, "push", "-q", "origin", "work")
	commitIn(app, "later", "on main\n")
	v, exit = wdxRebase(t, home, app, "work")
	if exit != clikit.ExitOK || v.Result != api.ResultRebased || v.HeadBefore == v.HeadAfter || !v.Pushed {
		t.Fatalf("rebased: exit %d, %+v", exit, v)
	}
	if got := revParse(t, origin, "work"); got != v.HeadAfter {
		t.Errorf("origin has %s, want %s", got, v.HeadAfter)
	}

	// Pushed to by another since: the rebase stays, origin's commit too.
	other := filepath.Join(t.TempDir(), "other")
	gittest.Git(t, filepath.Dir(other), "clone", "-q", "-b", "work", origin, other)
	commitIn(other, "theirs", "pushed elsewhere\n")
	gittest.Git(t, other, "push", "-q", "origin", "work")
	commitIn(app, "again", "on main\n")
	v, exit = wdxRebase(t, home, app, "work")
	if exit != clikit.ExitRejected || v.Code != api.ErrorCodePushFailed || v.Hint == nil || !strings.Contains(*v.Hint, "rebase is kept") {
		t.Fatalf("push refused: exit %d, %+v", exit, v)
	}

	// A conflict is undone, its files listed.
	commitIn(work, "file", "theirs\n")
	commitIn(app, "file", "ours\n")
	before := revParse(t, work, "HEAD")
	out := wdxWith(t, home, in(app), "worktree", "rebase", "--name", "work")
	if out.exit != clikit.ExitRejected || !strings.Contains(out.stderr, "rebase_failed") || !strings.Contains(out.stderr, "agent send") {
		t.Fatalf("conflict: %+v", out)
	}
	v, _ = wdxRebase(t, home, app, "work")
	if v.Code != api.ErrorCodeRebaseFailed || !slices.Equal(detailFiles(t, v.Problem, "conflicts"), []string{"file"}) {
		t.Errorf("conflict: %+v", v)
	}
	if got := revParse(t, work, "HEAD"); got != before {
		t.Error("the conflict was not undone")
	}

	// One stopped by hand is the person's to finish.
	if err := exec.Command("git", "-C", work, "rebase", "main").Run(); err == nil {
		t.Fatal("want the rebase to stop")
	}
	if v, exit = wdxRebase(t, home, app, "work"); exit != clikit.ExitRejected || v.Code != api.ErrorCodeWorktreeBusy {
		t.Errorf("busy: exit %d, %+v", exit, v)
	}
	gittest.Git(t, work, "rebase", "--abort")
	gittest.Git(t, work, "switch", "-q", "--detach")
	if v, exit = wdxRebase(t, home, app, "work"); exit != clikit.ExitRejected || v.Code != api.ErrorCodeDetachedHead {
		t.Errorf("detached: exit %d, %+v", exit, v)
	}
	if v, exit = wdxRebase(t, home, app, "main"); exit != clikit.ExitRejected || v.Code != api.ErrorCodeWorktreeIsClone {
		t.Errorf("the clone: exit %d, %+v", exit, v)
	}
	if v, exit = wdxRebase(t, home, app, "nope"); exit != clikit.ExitRejected || v.Code != api.ErrorCodeNotFound {
		t.Errorf("unknown: exit %d, %+v", exit, v)
	}
}

func TestWorktreeRebaseText(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app, work := worktreeProject(t, home)
	gittest.Git(t, work, "checkout", "-q", "--", "file")
	out := wdxWith(t, home, in(app), "worktree", "rebase", "--name", "work")
	if out.exit != clikit.ExitOK || !strings.Contains(out.stdout, "up to date: worktree work already sits on main") {
		t.Errorf("up to date: %+v", out)
	}
	gittest.Git(t, app, "commit", "-q", "--allow-empty", "-m", "later")
	out = wdxWith(t, home, in(app), "worktree", "rebase", "--name", "work")
	if out.exit != clikit.ExitOK || !strings.Contains(out.stdout, "rebased worktree work onto main") {
		t.Errorf("rebased: %+v", out)
	}
}

// claudeWorking is a run file for the test ACA: a program drawing Claude Code's
// screen mid-turn, which the holder reads as working.
const claudeWorking = `stty raw -echo
printf '────────────\r\n❯ typed\r\n────────────\r\n  ⏸ manual mode on · esc to interrupt'
`

func waitWorking(t *testing.T, home, id string) {
	t.Helper()
	waittest.For(t, "agent "+id+" working", func() bool {
		v, exit := wdxJSON(t, home, "agent", "get", "--id", id)
		return exit == clikit.ExitOK && v.Agent.Activity != nil && *v.Agent.Activity == api.AgentActivityWorking
	})
}

// askingAgentHome is a home whose worktree "work" of project app holds one
// commit to land, with an agent working in it for each of names, by their ids.
func askingAgentHome(t *testing.T, names ...string) (home, app string, ids []string) {
	t.Helper()
	home = newHome(t)
	app, work := worktreeProject(t, home)
	gittest.Git(t, work, "checkout", "-q", "--", "file")
	if err := os.WriteFile(filepath.Join(app, ".git", "info", "exclude"), []byte("run\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "run"), []byte(claudeWorking), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := wdxWith(t, home, in(app), "project", "landing", "--name", "app", "--no-check"); out.exit != clikit.ExitOK {
		t.Fatalf("set: %+v", out)
	}
	if v, exit := wdxJSON(t, home, "project", "open", "--local", work); exit != clikit.ExitOK {
		t.Fatalf("open the worktree: exit %d, %+v", exit, v)
	}
	for _, name := range names {
		ids = append(ids, newAgent(t, home, work, name).Id)
	}
	for _, id := range ids {
		waitWorking(t, home, id)
	}
	return home, app, ids
}

// TestWorktreeAskingAgentAnotherWorking: the agent asking is not counted as
// working in its worktree, but another agent working there still is.
func TestWorktreeAskingAgentAnotherWorking(t *testing.T) {
	t.Parallel()
	home, app, ids := askingAgentHome(t, "self", "other")
	for _, command := range []string{"rebase", "land"} {
		v, exit := wdxWorktree(t, home, app, command, "--name", "work", "--agent-id", ids[0])
		if exit != clikit.ExitRejected || v.Code != api.ErrorCodeAgentWorking {
			t.Errorf("%s with another working: exit %d, %+v", command, exit, v)
		}
	}
}

// TestWorktreeAskingAgentUnknown: an --agent-id that is no agent of the project
// is not_found, so that a mistyped id never skips the check.
func TestWorktreeAskingAgentUnknown(t *testing.T) {
	t.Parallel()
	home, app, _ := askingAgentHome(t)
	for _, command := range []string{"rebase", "land"} {
		v, exit := wdxWorktree(t, home, app, command, "--name", "work", "--agent-id", "agent-nope")
		if exit != clikit.ExitRejected || v.Code != api.ErrorCodeNotFound {
			t.Errorf("%s by an unknown agent: exit %d, %+v", command, exit, v)
		}
	}
}

// TestWorktreeAskingAgentItself: the agent working in a worktree, always
// mid-turn as it runs the command, rebases and lands it when it says who it is.
func TestWorktreeAskingAgentItself(t *testing.T) {
	t.Parallel()
	home, app, ids := askingAgentHome(t, "self")
	v, exit := wdxWorktree(t, home, app, "rebase", "--name", "work")
	if exit != clikit.ExitRejected || v.Code != api.ErrorCodeAgentWorking {
		t.Errorf("rebase not saying who asks: exit %d, %+v", exit, v)
	}
	r, exit := wdxRebase(t, home, app, "work", "--agent-id", ids[0])
	if exit != clikit.ExitOK || r.Result != api.ResultUpToDate {
		t.Errorf("rebase by the agent working there: exit %d, %+v", exit, r)
	}
	out := wdxWith(t, home, in(app), "worktree", "land", "--name", "work", "--agent-id", ids[0], "--json")
	var landed worktreeLanded
	if err := json.Unmarshal([]byte(out.stdout), &landed); err != nil || out.exit != clikit.ExitOK || landed.Result != api.ResultLanded {
		t.Errorf("land by the agent working there: %+v, %v", out, err)
	}
}
