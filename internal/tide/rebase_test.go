package tide

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/gittest"
)

func TestRebaseBarKey(t *testing.T) {
	t.Parallel()
	main := "main"
	m := worktreesModel(
		api.WorktreeStatus{Name: "main", Path: "/r", Ahead: aheadBy(0), Behind: aheadBy(2), Base: &main},
		api.WorktreeStatus{Name: "behind", Path: "/r/a", Ahead: aheadBy(1), Behind: aheadBy(2), Base: &main},
		api.WorktreeStatus{Name: "on", Path: "/r/b", Ahead: aheadBy(1), Behind: aheadBy(0), Base: &main},
		api.WorktreeStatus{Name: "gone", Path: "/r/c", Missing: true},
	)
	for name, want := range map[string]bool{"main": false, "behind": true, "on": false, "gone": false, "nope": false} {
		if key, ok := m.rebaseBarKey(name); ok != want || (ok && (key.keys != rebaseKey || key.label != "rebase")) {
			t.Errorf("%s: %+v, %v, want %v", name, key, ok, want)
		}
	}
	if !slices.ContainsFunc(m.worktreeBarKeys(committedTab("behind")), func(k barKey) bool { return k.keys == rebaseKey }) {
		t.Errorf("no R: %+v", m.worktreeBarKeys(committedTab("behind")))
	}
	if slices.ContainsFunc(m.worktreeBarKeys(fileDiffTab("behind", "f")), func(k barKey) bool { return k.keys == rebaseKey }) {
		t.Errorf("a file's diff offers R")
	}

	// The row offers it on any worktree but the clone.
	for _, tc := range []struct {
		name string
		want bool
	}{{"main", false}, {"behind", true}, {"on", true}} {
		m.moveToWorktree(slices.IndexFunc(m.worktreeRows(), func(r worktreeRow) bool { return r.worktree.Name == tc.name }))
		if m.menuOffers(rebaseKey) != tc.want {
			t.Errorf("row %s: offered %v, want %v", tc.name, !tc.want, tc.want)
		}
	}
}

// moveMain puts a commit on main, and waits for the worktree to say it is behind.
func moveMain(t *testing.T, p *program, dir, file, content string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, file), content)
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-qm", file)
	p.until(t, "main moved on", func(m *Model) bool {
		w, ok := m.worktreeNamed("work")
		return ok && baseMovedOn(w)
	})
}

func TestRebaseInATide(t *testing.T) {
	t.Parallel()
	p, dir, work := landInTide(t)
	m := p.m
	if strings.Contains(text(m), "R rebase") {
		t.Errorf("R offered with main where it was:\n%s", text(m))
	}
	moveMain(t, p, dir, "later", "b\n")
	p.until(t, "the bar", func(m *Model) bool { return strings.Contains(text(m), "R rebase") })
	p.keys(rebaseKey)
	p.until(t, "rebased", func(m *Model) bool { return m.statusNotice == "rebased work onto main" })
	if got := revParse(t, work, "HEAD^"); got != revParse(t, dir, "main") {
		t.Errorf("work is on %s, not main", got)
	}
	p.until(t, "on main", func(m *Model) bool { w, _ := m.worktreeNamed("work"); return !baseMovedOn(w) })

	// On its row too, where nothing is left to do.
	m.layout.setFocus(project)
	m.moveToWorktree(slices.IndexFunc(m.worktreeRows(), func(r worktreeRow) bool { return r.worktree.Name == "work" }))
	p.keys(rebaseKey)
	p.until(t, "up to date", func(m *Model) bool { return m.statusNotice == "work is up to date with main" })
}

// rebaseWithAnAgent is landInTide with an agent running, so that a box can ask it.
func rebaseWithAnAgent(t *testing.T) (*program, string, string) {
	t.Helper()
	p, dir, work := landInTide(t)
	if _, err := p.m.client.CreateAgent(t.Context(), api.CreateAgent{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	p.until(t, "the agent", func(m *Model) bool {
		return len(m.agents()) == 1 && m.agents()[0].State == api.AgentStateRunning
	})
	return p, dir, work
}

func TestRebaseConflictAsksAnAgent(t *testing.T) {
	t.Parallel()
	p, dir, work := rebaseWithAnAgent(t)
	m := p.m
	before := revParse(t, work, "HEAD")
	moveMain(t, p, dir, "more", "ours\n")
	p.keys(rebaseKey)
	p.until(t, "the box", func(m *Model) bool { return m.commit != nil })
	if want := "Rebase work onto main in " + work + ", resolving the conflicts"; m.commit.text != want {
		t.Errorf("text: %q, want %q", m.commit.text, want)
	}
	wantText(t, m, "Ask to rebase", "Stopped on: more")
	if revParse(t, work, "HEAD") != before {
		t.Error("the conflict was not undone")
	}
}

func TestRebaseUncommittedAsksToCommit(t *testing.T) {
	t.Parallel()
	p, _, work := rebaseWithAnAgent(t)
	m := p.m
	writeFile(t, filepath.Join(work, "more"), "changed\n")
	gittest.Git(t, work, "add", "more") // an edit not staged fires no event (docs/TIDE_WORKTREES.md)
	p.until(t, "the change", func(m *Model) bool { w, _ := m.worktreeNamed("work"); return len(w.Files) == 1 })
	m.layout.setFocus(project)
	m.moveToWorktree(slices.IndexFunc(m.worktreeRows(), func(r worktreeRow) bool { return r.worktree.Name == "work" }))
	p.keys(rebaseKey)
	p.until(t, "the box", func(m *Model) bool { return m.commit != nil })
	if want := "Commit what is not committed in " + work + ", then rebase work onto main"; m.commit.text != want {
		t.Errorf("text: %q, want %q", m.commit.text, want)
	}
	if m.commit.rebase != nil {
		t.Errorf("asks to rebase: %+v", m.commit)
	}
	wantText(t, m, "Ask to commit")
}

func revParse(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// TestStackedInTheParentTab: the parent's tab says what each stacked plan is
// stacked on, and R once its plan below has moved on, which rebases its worktree.
func TestStackedInTheParentTab(t *testing.T) {
	t.Parallel()
	sso, ui := "sso", "plan/ui"
	below, on := "plan/sso", "sso"
	m := worktreesModel(
		api.WorktreeStatus{Name: "main", Path: "/r", Ahead: aheadBy(0), Base: &sso},
		api.WorktreeStatus{Name: "ui-1", Path: "/r/ui-1", Branch: &ui, Ahead: aheadBy(1), Behind: aheadBy(0), Base: &below},
	)
	plans := []api.PlanChild{
		{Id: "sso", After: []string{}, State: api.PlanStateDone, Tasks: 1},
		{Id: "ui", After: []string{"sso"}, State: api.PlanStateDone, Tasks: 1, StackedOn: &on},
	}
	c := &api.PlanContent{Plan: api.Plan{Id: "doc-big", Name: "big", State: api.PlanStateDone, Revision: 1}, Revision: 1,
		Run:        &api.PlanRun{State: api.PlanStateDone},
		Definition: api.PlanDefinition{Goal: "g", Constraints: []string{}, Plans: &plans}}
	v := &planView{content: c, cursor: "ui", goals: map[string]*childGoal{}}
	draw := func() string {
		v.movedOn = m.stackedMovedOn(c)
		return planTabText(v, 120, 20, time.Unix(0, 0))
	}
	if got := draw(); !strings.Contains(got, "done · stacked on sso") || strings.Contains(got, "rebase: R") {
		t.Errorf("not moved on:\n%s", got)
	}
	(*m.following.project.WorktreeStatuses)[1].Behind = aheadBy(1)
	if got := draw(); !strings.Contains(got, "done · stacked on sso · rebase: R") {
		t.Errorf("moved on:\n%s", got)
	}
	if cmd := m.planTabKey(v, keyMsgs(rebaseKey)[0]); cmd == nil || m.statusNotice != "rebasing ui-1…" {
		t.Errorf("R: %q", m.statusNotice)
	}
	m.statusNotice = ""
	v.cursor = "sso"
	if cmd := m.planTabKey(v, keyMsgs(rebaseKey)[0]); cmd != nil || m.statusNotice != "" {
		t.Errorf("R on a plan not stacked: %q", m.statusNotice)
	}
}
