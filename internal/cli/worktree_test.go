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
