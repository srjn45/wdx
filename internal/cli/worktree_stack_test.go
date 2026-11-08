package cli

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/gh/ghtest"
	"github.com/srjn45/wdx/internal/gittest"
)

// stackedOn returns what plan status of the plan of plans parent says the plan it
// lists called name is stacked on, "" for nothing.
func stackedOn(t *testing.T, home, dir, parent, name string) string {
	t.Helper()
	v, exit := wdxJSONWith(t, home, in(dir), "plan", "status", "--name", parent)
	if exit != clikit.ExitOK || v.Definition == nil || v.Definition.Plans == nil {
		t.Fatalf("status of %s: exit %d, %+v", parent, exit, v)
	}
	for _, c := range *v.Definition.Plans {
		if c.Id == name && c.StackedOn != nil {
			return *c.StackedOn
		}
	}
	return ""
}

// TestWorktreeRebaseStacked: a stacked plan of plans starts ui on sso's branch once
// sso is done, and opens ui's PR against it. R on ui's worktree then asks gh about
// sso's PR: not merged, ui goes onto sso's review fix; merged but not pulled, it is
// refused; merged and pulled, only ui's own commit goes onto main, though sso was
// squashed, and ui's PR goes on main too.
func TestWorktreeRebaseStacked(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	fake := ghtest.In(home)
	repo := planRepo(t, home, "")
	origin := filepath.Join(t.TempDir(), "origin.git")
	gittest.Git(t, repo, "init", "-q", "--bare", origin)
	gittest.Git(t, repo, "remote", "add", "origin", origin)
	gittest.Git(t, repo, "push", "-q", "origin", "main")
	if v, exit := wdxJSON(t, home, "project", "landing", "--name", "app", "--no-check"); exit != clikit.ExitOK {
		t.Fatalf("landing: exit %d, %+v", exit, v)
	}
	newPlan(t, home, repo, "sso", "goal: g\ntasks:\n  - id: s\n    prompt: p\n")
	newPlan(t, home, repo, "ui", "goal: g\ntasks:\n  - id: u\n    prompt: p\n")
	newPlan(t, home, repo, "big", "goal: g\npr: true\nstacked: true\nplans:\n  - id: sso\n  - id: ui\n    after: [sso]\n")
	if v, exit := wdxJSONWith(t, home, in(repo), "plan", "run", "--name", "big"); exit != clikit.ExitOK {
		t.Fatalf("run: exit %d, %+v", exit, v)
	}
	if !eventually(t, func() bool {
		v, _ := wdxJSONWith(t, home, in(repo), "plan", "status", "--name", "big")
		return v.Plan != nil && v.Plan.State == api.PlanStateDone && slices.Contains(fake.Calls(t), "pr create --fill --base plan/sso --head plan/ui")
	}) {
		t.Fatalf("big not done with ui's PR on plan/sso: gh was asked %q", fake.Calls(t))
	}
	if on := stackedOn(t, home, repo, "big", "ui"); on != "sso" {
		t.Fatalf("ui stacked on %q", on)
	}
	w := worktreeOf(t, home, repo, "ui-1")
	if w.Base == nil || *w.Base != "plan/sso" || w.Ahead == nil || *w.Ahead != 1 {
		t.Fatalf("ui's worktree is not measured against plan/sso: %+v", w)
	}
	sso, ui := worktreeOf(t, home, repo, "sso-1").Path, w.Path
	if revParse(t, ui, "HEAD^") != revParse(t, repo, "plan/sso") {
		t.Fatal("ui does not start at sso's tip")
	}

	// Not merged: onto sso's review fix, only ui's commit moving.
	commitFile(t, sso, "fix", "review\n")
	gittest.Git(t, sso, "push", "-q", "origin", "plan/sso")
	v, exit := wdxRebase(t, home, repo, "ui-1")
	if exit != clikit.ExitOK || v.Result != api.ResultRebased || v.Base != "plan/sso" || !v.Pushed {
		t.Fatalf("not merged: exit %d, %+v", exit, v)
	}
	if revParse(t, ui, "HEAD^") != revParse(t, repo, "plan/sso") {
		t.Error("not merged: ui does not sit on sso's fix")
	}
	if v, exit := wdxRebase(t, home, repo, "ui-1"); exit != clikit.ExitOK || v.Result != api.ResultUpToDate {
		t.Errorf("again: exit %d, %+v", exit, v)
	}

	// sso's PR squashed into main on GitHub, not pulled yet.
	other := filepath.Join(t.TempDir(), "other")
	gittest.Git(t, filepath.Dir(other), "clone", "-q", origin, other)
	gittest.Git(t, other, "merge", "-q", "--squash", "origin/plan/sso")
	gittest.Git(t, other, "commit", "-qm", "sso, squashed")
	gittest.Git(t, other, "push", "-q", "origin", "main")
	fake.SetMergedOf(t, "plan/sso", revParse(t, repo, "plan/sso"), revParse(t, other, "HEAD"))
	v, exit = wdxRebase(t, home, repo, "ui-1")
	if exit != clikit.ExitRejected || v.Code != api.ErrorCodeBaseNotPulled || v.Hint == nil || *v.Hint != "pull main: sso's PR is merged on GitHub" {
		t.Fatalf("not pulled: exit %d, %+v", exit, v)
	}

	// Pulled: only ui's commit goes onto main, and its PR with it.
	gittest.Git(t, repo, "pull", "-q", "--ff-only", "origin", "main")
	v, exit = wdxRebase(t, home, repo, "ui-1")
	if exit != clikit.ExitOK || v.Result != api.ResultRebased || v.Base != "main" || !v.Pushed {
		t.Fatalf("merged: exit %d, %+v", exit, v)
	}
	if revParse(t, ui, "HEAD^") != revParse(t, repo, "main") {
		t.Error("merged: ui's own commit does not sit on main")
	}
	if calls := fake.Calls(t); !slices.Contains(calls, "pr edit plan/ui --base main") {
		t.Errorf("gh was asked %q", calls)
	}
	if on := stackedOn(t, home, repo, "big", "ui"); on != "" {
		t.Errorf("ui still stacked on %q", on)
	}
	if w := worktreeOf(t, home, repo, "ui-1"); w.Base == nil || *w.Base != "main" || w.Ahead == nil || *w.Ahead != 1 {
		t.Errorf("ui's worktree is not measured against main: %+v", w)
	}
	if v, exit := wdxRebase(t, home, repo, "ui-1"); exit != clikit.ExitOK || v.Result != api.ResultUpToDate || v.Base != "main" {
		t.Errorf("again on main: exit %d, %+v", exit, v)
	}
}

// worktreeOf returns what worktree list says of the worktree called name.
func worktreeOf(t *testing.T, home, dir, name string) api.WorktreeStatus {
	t.Helper()
	v, exit := wdxWorktree(t, home, dir, "list")
	i := slices.IndexFunc(v.Worktrees, func(s api.WorktreeStatus) bool { return s.Name == name })
	if exit != clikit.ExitOK || i < 0 {
		t.Fatalf("no worktree %s: exit %d, %+v", name, exit, v)
	}
	return v.Worktrees[i]
}

// commitFile commits a file of content in the checkout at dir.
func commitFile(t *testing.T, dir, file, content string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, file), content)
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-qm", strings.TrimSpace(file))
}
