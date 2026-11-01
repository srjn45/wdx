package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/gittest"
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
		if exit != api.ExitOK || len(v.Worktrees) != 2 {
			t.Fatalf("%v: exit %d, %+v", tc.args, exit, v)
		}
		w := v.Worktrees[slices.IndexFunc(v.Worktrees, func(s api.WorktreeStatus) bool { return s.Name == "work" })]
		if w.Ahead == nil || *w.Ahead != 1 || len(w.Files) != 1 || w.Files[0].Path != "file" || w.Files[0].Status != api.WorktreeFileStatusM {
			t.Errorf("work: %+v", w)
		}
	}

	out := wdxWith(t, home, in(app), "worktree", "list")
	if out.exit != api.ExitOK || !strings.Contains(out.stdout, "NAME") || !strings.Contains(out.stdout, "work") {
		t.Errorf("text: %+v", out)
	}
}

func TestWorktreeListOutsideAProject(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	v, exit := wdxJSONWith(t, home, in(t.TempDir()), "worktree", "list")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	wantHint(t, v, "run it in a project's worktree, or pass --project-name or --project-id")
}

func TestWorktreeDiff(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app, _ := worktreeProject(t, home)

	v, exit := wdxWorktree(t, home, app, "diff", "--name", "work")
	if exit != api.ExitOK || !strings.Contains(v.Patch, "+committed") || strings.Contains(v.Patch, "+two") {
		t.Errorf("committed: exit %d, %q", exit, v.Patch)
	}
	v, exit = wdxWorktree(t, home, app, "diff", "--name", "work", "--path", "file")
	if exit != api.ExitOK || !strings.Contains(v.Patch, "+two") || strings.Contains(v.Patch, "+committed") {
		t.Errorf("uncommitted: exit %d, %q", exit, v.Patch)
	}
	v, exit = wdxWorktree(t, home, app, "diff", "--name", "work", "--uncommitted")
	if exit != api.ExitOK || !strings.Contains(v.Patch, "+two") || strings.Contains(v.Patch, "+committed") {
		t.Errorf("all uncommitted: exit %d, %q", exit, v.Patch)
	}
	out := wdxWith(t, home, in(app), "worktree", "diff", "--name", "work")
	if out.exit != api.ExitOK || !strings.Contains(out.stdout, "+committed") {
		t.Errorf("text: %+v", out)
	}
}

func TestWorktreeDiffRefused(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app, _ := worktreeProject(t, home)

	v, exit := wdxJSONWith(t, home, in(app), "worktree", "diff", "--name", "nope")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "worktree", "diff", "--name", "work", "--path", "../outside")
	wantError(t, v, exit, api.ErrorCodeInvalidPath, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "worktree", "diff", "--name", "work")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
}

func TestWorktreeComments(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app, _ := worktreeProject(t, home)
	v, exit := wdxWorktree(t, home, app, "comments", "--name", "work")
	if exit != api.ExitOK || !strings.Contains(wdxWith(t, home, in(app), "worktree", "comments", "--name", "work").stdout, "no comments") {
		t.Errorf("none: exit %d, %+v", exit, v)
	}
	e, exit := wdxJSONWith(t, home, in(app), "worktree", "comments", "--name", "nope")
	wantError(t, e, exit, api.ErrorCodeNotFound, api.ExitRejected)
}

func TestMessageCommands(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	send := func() {
		t.Helper()
		if out := wdxWith(t, home, in(app), "agent", "send", "--name", "helper", "--prompt", "a secret text"); out.exit != api.ExitOK {
			t.Fatalf("send: %+v", out)
		}
	}
	if out := wdxWith(t, home, in(app), "agent", "create", "--name", "helper"); out.exit != api.ExitOK {
		t.Fatalf("create: %+v", out)
	}
	if out := wdxWith(t, home, in(app), "message", "list"); out.exit != api.ExitOK || !strings.Contains(out.stdout, "no messages waiting") {
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
	if exit != api.ExitOK || v.Result != "queued" {
		t.Errorf("resend: %d, %+v", exit, v)
	}
	if out := wdxWith(t, home, in(app), "message", "resend", "--id", id); out.exit != api.ExitOK || !strings.Contains(out.stdout, "queued for helper") {
		t.Errorf("resend text: %+v", out)
	}
	v, exit = wdxJSONWith(t, home, in(app), "message", "discard", "--id", id)
	if exit != api.ExitOK || v.Result != "removed" {
		t.Errorf("discard: %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(app), "message", "discard", "--id", id)
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "message", "resend", "--id", "msg-nope")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "message", "list")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	send()
	if out := wdxWith(t, home, in(app), "message", "discard", "--id", "msg-nope"); out.exit != api.ExitRejected {
		t.Errorf("text failure: %+v", out)
	}
}

func TestWorktreeRemove(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app, work := worktreeProject(t, home)

	// With a file changed and a commit ahead, nothing goes.
	v, exit := wdxWorktree(t, home, app, "remove", "--name", "work")
	if exit != api.ExitRejected || v.Code != api.ErrorCodeWorktreeNotLanded {
		t.Fatalf("not landed: exit %d, %+v", exit, v)
	}
	if _, err := os.Stat(work); err != nil {
		t.Fatal(err)
	}
	v, exit = wdxWorktree(t, home, app, "remove", "--name", "main")
	if exit != api.ExitRejected || v.Code != api.ErrorCodeWorktreeIsClone {
		t.Fatalf("the clone: exit %d, %+v", exit, v)
	}
	v, exit = wdxWorktree(t, home, app, "remove", "--name", "nope")
	if exit != api.ExitRejected || v.Code != api.ErrorCodeNotFound {
		t.Fatalf("unknown: exit %d, %+v", exit, v)
	}

	gittest.Git(t, work, "checkout", "-q", "--", "file")
	gittest.Git(t, app, "merge", "-q", "--ff-only", "work")
	out := wdxWith(t, home, in(app), "worktree", "remove", "--name", "work")
	if out.exit != api.ExitOK || !strings.Contains(out.stdout, "removed worktree work") || !strings.Contains(out.stdout, "branch work deleted") {
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
	if v, exit := wdxJSON(t, home, "project", "open", "--local", repo, "--name", "app"); exit != api.ExitOK {
		t.Fatalf("open: exit %d, %+v", exit, v)
	}
	if v, exit := wdxJSONWith(t, home, in(repo), "agent", "create", "--name", "fixer", "--worktree", "fix"); exit != api.ExitOK {
		t.Fatalf("agent: exit %d, %+v", exit, v)
	}
	out := wdxWith(t, home, in(repo), "worktree", "remove", "--name", "fix", "--json")
	var r api.WorktreeRemoved
	if err := json.Unmarshal([]byte(out.stdout), &r); err != nil || out.exit != api.ExitOK {
		t.Fatalf("remove: %+v, %v", out, err)
	}
	if r.Result != api.ResultRemoved || len(r.Notified) != 1 || r.Notified[0] != "fixer" {
		t.Errorf("answer: %+v", r)
	}
	v, exit := wdxWorktree(t, home, repo, "list")
	if exit != api.ExitOK || len(v.Worktrees) != 1 || v.Worktrees[0].Name != "main" {
		t.Errorf("list: exit %d, %+v", exit, v.Worktrees)
	}
}
