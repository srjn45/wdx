package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/plan"
	"github.com/srjn45/wdx/internal/waittest"
)

// answer does what the person's answer to a plan does, as a request would, holding
// the changes' lock, and wakes the plan.
func (td *testDaemon) answer(t *testing.T, planID string, do func(ctx context.Context, s *plan.Store) error) {
	t.Helper()
	td.daemon.changes.Lock()
	err := do(context.Background(), td.daemon.plans)
	td.daemon.changes.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	td.daemon.runner.Wake(planID)
}

// messagesTo returns what the agent with id was told, oldest first.
func (td *testDaemon) messagesTo(t *testing.T, id string) []string {
	t.Helper()
	rows, err := td.daemon.db.Query(`SELECT m.text FROM messages m JOIN agents a ON a.terminal_id = m.terminal_id WHERE a.id = ? ORDER BY m.created_at, m.rowid`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var list []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatal(err)
		}
		list = append(list, text)
	}
	return list
}

func (td *testDaemon) agentLive(t *testing.T, id string) bool {
	t.Helper()
	a, err := td.daemon.agents.Store().Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return a.Terminal.State.Live()
}

func (td *testDaemon) setCheck(t *testing.T, command string) {
	t.Helper()
	r, err := td.client.SetProjectLandingWithResponse(context.Background(), "app", api.ProjectLandingUpdate{Check: &command})
	if err != nil || r.JSON200 == nil {
		t.Fatalf("landing check: %v %s", err, r.Body)
	}
}

// gitDir is where a check finds the files a test makes, outside every worktree.
const gitDir = `g=$(git rev-parse --path-format=absolute --git-common-dir)`

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// TestPlanCheckFailsThenLands: a landing whose check fails keeps its terminal; the
// task retried resumes its agent, told of the check, and lands once it passes.
func TestPlanCheckFailsThenLands(t *testing.T) {
	t.Parallel()
	td := start(t)
	repo := td.planProject(t, "")
	td.setCheck(t, gitDir+`; test -e "$g/pass"`)
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")
	td.runPlan(t, "auth", 0)
	c := td.waitPlan(t, "auth", "a running", stateIs("a:running"))
	agent := taskIn(c, "a").Agent.Id
	td.finishTask(t, repo, "auth", "a")
	c = td.waitPlan(t, "auth", "a land failed", stateIs("a:land_failed"))
	a := taskIn(c, "a")
	committed := gitOut(t, *a.SlotPath, "rev-parse", "HEAD")
	if a.Failure == nil || *a.Failure != api.PlanTaskFailureCheck || gitOut(t, repo, "rev-parse", "plan/auth") != gitOut(t, repo, "rev-parse", "main") {
		t.Errorf("a: %+v, or plan/auth moved", a)
	}
	check := plan.CheckTerminal(*a.SlotPath)
	if term, err := td.daemon.terminals.Store().GetByName(context.Background(), c.Plan.ProjectId, check); err != nil || term.State.Live() {
		t.Errorf("the check's terminal %s: %+v, %v", check, term, err)
	}

	td.answer(t, c.Plan.Id, func(ctx context.Context, s *plan.Store) error {
		_, err := s.RetryTask(ctx, c.Plan.Id, "a", false)
		return err
	})
	c = td.waitPlan(t, "auth", "a running again", stateIs("a:running"))
	if got := taskIn(c, "a").Agent.Id; got != agent || !td.agentLive(t, agent) {
		t.Errorf("a's agent: %s, want %s resumed", got, agent)
	}
	if got := gitOut(t, *a.SlotPath, "rev-parse", "HEAD"); got != committed {
		t.Errorf("the slot at %s after the retry, want %s, with a's commit", got, committed)
	}
	told := td.messagesTo(t, agent)
	if last := told[len(told)-1]; !strings.Contains(last, "the check `"+gitDir) || !strings.Contains(last, check) {
		t.Errorf("told: %q", last)
	}
	touch(t, filepath.Join(repo, ".git", "pass"))
	td.finishTask(t, repo, "auth", "a")
	td.waitPlan(t, "auth", "a done", stateIs("a:done"))
	if _, err := td.daemon.terminals.Store().GetByName(context.Background(), c.Plan.ProjectId, check); err == nil {
		t.Error("a check that passed kept its terminal")
	}
}

// TestPlanBranchMovedDuringTheCheck: the plan's branch moved by someone while the
// check ran lands the task again, rebased onto it and checked again.
func TestPlanBranchMovedDuringTheCheck(t *testing.T) {
	t.Parallel()
	td := start(t)
	repo := td.planProject(t, "")
	td.setCheck(t, gitDir+`; echo >> "$g/checks"; touch "$g/checking"; while [ ! -e "$g/go" ]; do sleep 0.05; done`)
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")
	td.runPlan(t, "auth", 0)
	td.waitPlan(t, "auth", "a running", stateIs("a:running"))
	td.finishTask(t, repo, "auth", "a")
	git := filepath.Join(repo, ".git")
	waittest.For(t, "the check", func() bool { return exists(filepath.Join(git, "checking")) })

	other := filepath.Join(t.TempDir(), "other")
	gittest.Git(t, repo, "worktree", "add", "-q", "--detach", other, "plan/auth")
	if err := os.WriteFile(filepath.Join(other, "moved.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, other, "add", "moved.txt")
	gittest.Git(t, other, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "moved")
	gittest.Git(t, repo, "update-ref", "refs/heads/plan/auth", gitOut(t, other, "rev-parse", "HEAD"))
	touch(t, filepath.Join(git, "go"))

	td.waitPlan(t, "auth", "a done", stateIs("a:done"))
	gitOut(t, repo, "show", "plan/auth:moved.txt")
	gitOut(t, repo, "show", "plan/auth:a.txt")
	checks, err := os.ReadFile(filepath.Join(git, "checks"))
	if err != nil || strings.Count(string(checks), "\n") != 2 {
		t.Errorf("checks run: %q, %v; want 2", checks, err)
	}
}

// TestPlanConflictLandedByThePerson: a conflict the person resolves in the slot
// lands when they say so.
func TestPlanConflictLandedByThePerson(t *testing.T) {
	t.Parallel()
	td := start(t)
	repo := td.planProject(t, "a shared\nb shared\n")
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n  - id: b\n    prompt: p\n")
	td.runPlan(t, "auth", 2)
	td.waitPlan(t, "auth", "both running", stateIs("a:running b:running"))
	td.finishTask(t, repo, "auth", "a")
	td.waitPlan(t, "auth", "a done", stateIs("a:done b:running"))
	td.finishTask(t, repo, "auth", "b")
	c := td.waitPlan(t, "auth", "b land failed", stateIs("a:done b:land_failed"))
	slot := *taskIn(c, "b").SlotPath

	rebase := exec.Command("git", "-C", slot, "rebase", "plan/auth")
	if out, err := rebase.CombinedOutput(); err == nil {
		t.Fatalf("the rebase did not stop: %s", out)
	}
	if err := os.WriteFile(filepath.Join(slot, "shared.txt"), []byte("a\nb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, slot, "add", "shared.txt")
	gittest.Git(t, slot, "-c", "core.editor=true", "-c", "user.name=t", "-c", "user.email=t@t", "rebase", "--continue")

	td.answer(t, c.Plan.Id, func(ctx context.Context, s *plan.Store) error {
		_, err := s.LandTask(ctx, c.Plan.Id, "b")
		return err
	})
	td.waitPlan(t, "auth", "b done", stateIs("a:done b:done"))
	if got := gitOut(t, repo, "show", "plan/auth:shared.txt"); got != "a\nb" {
		t.Errorf("shared.txt on plan/auth: %q", got)
	}
}

// TestPlanTaskRetried: a task whose agent ended resumes it, then, retried fresh,
// starts a new one named after the attempt, told of the one before.
func TestPlanTaskRetried(t *testing.T) {
	t.Parallel()
	td := start(t)
	repo := td.planProject(t, "a exit\n")
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")
	td.runPlan(t, "auth", 0)
	c := td.waitPlan(t, "auth", "a running", stateIs("a:running"))
	first := *taskIn(c, "a").Agent
	end := filepath.Join(repo, ".git", "end-a")
	touch(t, end)
	c = td.waitPlan(t, "auth", "a failed", stateIs("a:failed"))
	if err := os.Remove(end); err != nil {
		t.Fatal(err)
	}

	td.answer(t, c.Plan.Id, func(ctx context.Context, s *plan.Store) error {
		_, err := s.RetryTask(ctx, c.Plan.Id, "a", false)
		return err
	})
	c = td.waitPlan(t, "auth", "a running again", stateIs("a:running"))
	if got := *taskIn(c, "a").Agent; got != first || !td.agentLive(t, first.Id) {
		t.Errorf("a's agent: %+v, want %+v resumed", got, first)
	}
	told := td.messagesTo(t, first.Id)
	if last := told[len(told)-1]; !strings.HasPrefix(last, "You ended before saying task a of the plan auth was done.") {
		t.Errorf("told: %q", last)
	}

	touch(t, end)
	c = td.waitPlan(t, "auth", "a failed again", stateIs("a:failed"))
	if err := os.Remove(end); err != nil {
		t.Fatal(err)
	}
	td.answer(t, c.Plan.Id, func(ctx context.Context, s *plan.Store) error {
		_, err := s.RetryTask(ctx, c.Plan.Id, "a", true)
		return err
	})
	c = td.waitPlan(t, "auth", "a running fresh", stateIs("a:running"))
	fresh := *taskIn(c, "a").Agent
	if fresh.Name != "auth-a-2" || fresh.Id == first.Id || *taskIn(c, "a").SlotPath != filepath.Join(filepath.Dir(*taskIn(c, "a").SlotPath), "auth-1") {
		t.Errorf("a: %+v", taskIn(c, "a"))
	}
	if told := td.messagesTo(t, fresh.Id); len(told) != 1 || !strings.HasPrefix(told[0], "A previous attempt at this task left its commits on this branch, task/auth/a") {
		t.Errorf("told: %q", told)
	}
	if _, err := td.daemon.agents.Store().Get(context.Background(), first.Id); err != nil {
		t.Errorf("the first agent: %v", err)
	}
}

// TestPlanSkippedTaskFreesItsSlot: a skipped task with no work frees its slot for
// what waits for it.
func TestPlanSkippedTaskFreesItsSlot(t *testing.T) {
	t.Parallel()
	td := start(t)
	repo := td.planProject(t, "a exit\n")
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n  - id: b\n    after: [a]\n    prompt: p\n")
	td.runPlan(t, "auth", 0)
	c := td.waitPlan(t, "auth", "a running", stateIs("a:running b:pending"))
	slot := *taskIn(c, "a").SlotPath
	touch(t, filepath.Join(repo, ".git", "end-a"))
	c = td.waitPlan(t, "auth", "a failed", stateIs("a:failed b:pending"))
	td.answer(t, c.Plan.Id, func(ctx context.Context, s *plan.Store) error {
		_, err := s.SkipTask(ctx, c.Plan.Id, "a")
		return err
	})
	c = td.waitPlan(t, "auth", "b running", stateIs("a:skipped b:running"))
	if got := *taskIn(c, "b").SlotPath; got != slot {
		t.Errorf("b in %s, want %s", got, slot)
	}
	if taskIn(c, "a").SlotPath != nil || strings.Contains(gitOut(t, repo, "branch", "--list", "task/auth/a"), "task/auth/a") {
		t.Errorf("a kept its slot or branch: %+v", taskIn(c, "a"))
	}
}

// TestPlanSkippedTaskKeepsItsWork: a skipped task's slot holding work leaves the
// plan as an ordinary worktree, and what waits for it starts in a new slot.
func TestPlanSkippedTaskKeepsItsWork(t *testing.T) {
	t.Parallel()
	td := start(t)
	repo := td.planProject(t, "a exit\n")
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n  - id: b\n    after: [a]\n    prompt: p\n")
	td.runPlan(t, "auth", 0)
	c := td.waitPlan(t, "auth", "a running", stateIs("a:running b:pending"))
	slot := *taskIn(c, "a").SlotPath
	touch(t, filepath.Join(repo, ".git", "end-a"))
	c = td.waitPlan(t, "auth", "a failed", stateIs("a:failed b:pending"))
	touch(t, filepath.Join(slot, "loose.txt"))
	td.answer(t, c.Plan.Id, func(ctx context.Context, s *plan.Store) error {
		_, err := s.SkipTask(ctx, c.Plan.Id, "a")
		return err
	})
	c = td.waitPlan(t, "auth", "b running", stateIs("a:skipped b:running"))
	if got := *taskIn(c, "b").SlotPath; got != filepath.Join(filepath.Dir(slot), "auth-2") {
		t.Errorf("b in %s", got)
	}
	if !exists(filepath.Join(slot, "loose.txt")) || gitOut(t, slot, "branch", "--show-current") != "task/auth/a" || taskIn(c, "a").SlotPath != nil {
		t.Errorf("a's work was not kept in %s: %+v", slot, taskIn(c, "a"))
	}
}

// TestPlanCancelled: cancelling stops the agents, removes the slots holding no work,
// keeps those that do, and closes the run, its tasks as they were.
func TestPlanCancelled(t *testing.T) {
	t.Parallel()
	td := start(t)
	td.planProject(t, "a exit\nb exit\n")
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n  - id: b\n    prompt: p\n")
	td.runPlan(t, "auth", 2)
	c := td.waitPlan(t, "auth", "both running", stateIs("a:running b:running"))
	a, b := taskIn(c, "a"), taskIn(c, "b")
	touch(t, filepath.Join(*b.SlotPath, "loose.txt"))
	td.answer(t, c.Plan.Id, func(ctx context.Context, s *plan.Store) error { return s.Cancel(ctx, c.Plan.Id) })
	c = td.waitPlan(t, "auth", "closed", func(c api.PlanContent) bool { return c.Run.FinishedAt != nil })
	if states(c) != "a:running b:running" || c.Plan.State != api.PlanStateCancelled {
		t.Errorf("cancelled: %s, %s", states(c), c.Plan.State)
	}
	if td.agentLive(t, a.Agent.Id) || td.agentLive(t, b.Agent.Id) {
		t.Error("an agent still runs")
	}
	if exists(*a.SlotPath) || !exists(filepath.Join(*b.SlotPath, "loose.txt")) {
		t.Errorf("a's slot kept, or b's work lost")
	}
}

// TestPlanCancelledDuringACheck: a landing going is cut short, its check stopped,
// and its task left landing, its work kept.
func TestPlanCancelledDuringACheck(t *testing.T) {
	t.Parallel()
	td := start(t)
	repo := td.planProject(t, "")
	td.setCheck(t, gitDir+`; touch "$g/checking"; while :; do sleep 0.05; done`)
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")
	td.runPlan(t, "auth", 0)
	c := td.waitPlan(t, "auth", "a running", stateIs("a:running"))
	slot := *taskIn(c, "a").SlotPath
	td.finishTask(t, repo, "auth", "a")
	waittest.For(t, "the check", func() bool { return exists(filepath.Join(repo, ".git", "checking")) })
	td.answer(t, c.Plan.Id, func(ctx context.Context, s *plan.Store) error { return s.Cancel(ctx, c.Plan.Id) })
	c = td.waitPlan(t, "auth", "closed", func(c api.PlanContent) bool { return c.Run.FinishedAt != nil })
	if states(c) != "a:landing" || !exists(slot) {
		t.Errorf("cancelled: %s, slot kept %v", states(c), exists(slot))
	}
	term, err := td.daemon.terminals.Store().GetByName(context.Background(), c.Plan.ProjectId, plan.CheckTerminal(slot))
	if err == nil && term.State.Live() {
		t.Errorf("the check still runs: %+v", term)
	}
}

// TestPlanCheckLeftRunningIsStopped: a check of the slot's still running, as a
// daemon that died during it leaves, is stopped before the slot's next check.
func TestPlanCheckLeftRunningIsStopped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	repo := td.planProject(t, "")
	td.setCheck(t, "true")
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")
	td.runPlan(t, "auth", 0)
	c := td.waitPlan(t, "auth", "a running", stateIs("a:running"))
	slot := *taskIn(c, "a").SlotPath
	left, err := td.client.CreateTerminalWithResponse(ctx, api.CreateTerminal{Project: optional("app"), Dir: slot, Name: optional(plan.CheckTerminal(slot))})
	if err != nil || left.JSON201 == nil {
		t.Fatalf("terminal: %v %s", err, left.Body)
	}
	td.finishTask(t, repo, "auth", "a")
	td.waitPlan(t, "auth", "a done", stateIs("a:done"))
	if _, err := td.daemon.terminals.Store().GetByName(ctx, c.Plan.ProjectId, plan.CheckTerminal(slot)); err == nil {
		t.Error("the check's terminal is still there")
	}
}
