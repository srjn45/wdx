package daemon

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/agent"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/plan"
	"github.com/srjn45/wdx/internal/waittest"
)

func taskParams[P api.RetryPlanTaskParams | api.LandPlanTaskParams | api.SkipPlanTaskParams]() *P {
	return &P{Project: optional("app")}
}

// retry, land and skip answer a task of the plan auth as the person does, and fail
// the test when the daemon refuses.
func (td *testDaemon) retry(t *testing.T, task string, fresh bool) {
	t.Helper()
	r, err := td.client.RetryPlanTaskWithResponse(context.Background(), "auth", task, taskParams[api.RetryPlanTaskParams](), api.RetryPlanTask{Fresh: &fresh})
	if err != nil || r.JSON200 == nil || r.JSON200.Result != api.ResultRetried || r.JSON200.Task.State != api.PlanTaskStatePending {
		t.Fatalf("retry %s: %v %s", task, err, r.Body)
	}
}

func (td *testDaemon) land(t *testing.T, task string) {
	t.Helper()
	r, err := td.client.LandPlanTaskWithResponse(context.Background(), "auth", task, taskParams[api.LandPlanTaskParams]())
	if err != nil || r.JSON200 == nil || r.JSON200.Result != api.ResultLanding {
		t.Fatalf("land %s: %v %s", task, err, r.Body)
	}
}

func (td *testDaemon) skip(t *testing.T, task string) {
	t.Helper()
	r, err := td.client.SkipPlanTaskWithResponse(context.Background(), "auth", task, taskParams[api.SkipPlanTaskParams]())
	if err != nil || r.JSON200 == nil || r.JSON200.Result != api.ResultSkipped || r.JSON200.Task.State != api.PlanTaskStateSkipped {
		t.Fatalf("skip %s: %v %s", task, err, r.Body)
	}
}

func (td *testDaemon) cancel(t *testing.T) {
	t.Helper()
	r, err := td.client.CancelPlanWithResponse(context.Background(), "auth", &api.CancelPlanParams{Project: optional("app")})
	if err != nil || r.JSON200 == nil || r.JSON200.Result != api.ResultCancelled || r.JSON200.Plan.State != api.PlanStateCancelled {
		t.Fatalf("cancel: %v %s", err, r.Body)
	}
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
	if a.CheckTerminal == nil || *a.CheckTerminal != check || c.Plan.TasksFailed == nil || *c.Plan.TasksFailed != 1 {
		t.Errorf("a's check terminal %v, the plan's failed tasks %v", a.CheckTerminal, c.Plan.TasksFailed)
	}

	ctx := context.Background()
	helper, err := td.client.CreateAgentWithResponse(ctx, api.CreateAgent{Project: optional("app"), Dir: *a.SlotPath, Name: optional("helper")})
	if err != nil || helper.JSON201 == nil {
		t.Fatalf("helper: %v %s", err, helper.Body)
	}
	r, err := td.client.LandPlanTaskWithResponse(ctx, "auth", "a", taskParams[api.LandPlanTaskParams]())
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeAgentWorking)
	stop := true
	if removed, err := td.client.DeleteAgentWithResponse(ctx, "helper", &api.DeleteAgentParams{Project: optional("app"), StopRunning: &stop}); err != nil || removed.JSON200 == nil {
		t.Fatalf("remove the helper: %v %s", err, removed.Body)
	}

	td.retry(t, "a", false)
	// The task runs once its agent is told why, which is before the agent starts.
	c = td.waitPlan(t, "auth", "a running again, its agent resumed", func(c api.PlanContent) bool {
		return states(c) == "a:running" && td.agentLive(t, agent)
	})
	if got := taskIn(c, "a").Agent.Id; got != agent {
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
	if taskIn(c, "b").CheckTerminal != nil {
		t.Errorf("a rebase that stopped names a check: %+v", taskIn(c, "b"))
	}

	rebase := exec.Command("git", "-C", slot, "rebase", "plan/auth")
	if out, err := rebase.CombinedOutput(); err == nil {
		t.Fatalf("the rebase did not stop: %s", out)
	}
	if err := os.WriteFile(filepath.Join(slot, "shared.txt"), []byte("a\nb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, slot, "add", "shared.txt")
	gittest.Git(t, slot, "-c", "core.editor=true", "-c", "user.name=t", "-c", "user.email=t@t", "rebase", "--continue")

	td.land(t, "b")
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
	td.waitPlan(t, "auth", "a failed", stateIs("a:failed"))
	if err := os.Remove(end); err != nil {
		t.Fatal(err)
	}

	td.retry(t, "a", false)
	c = td.waitPlan(t, "auth", "a running again, its agent resumed", func(c api.PlanContent) bool {
		return states(c) == "a:running" && td.agentLive(t, first.Id)
	})
	if got := *taskIn(c, "a").Agent; got != first {
		t.Errorf("a's agent: %+v, want %+v resumed", got, first)
	}
	told := td.messagesTo(t, first.Id)
	if last := told[len(told)-1]; !strings.HasPrefix(last, "You ended before saying task a of the plan auth was done.") {
		t.Errorf("told: %q", last)
	}

	touch(t, end)
	td.waitPlan(t, "auth", "a failed again", stateIs("a:failed"))
	if err := os.Remove(end); err != nil {
		t.Fatal(err)
	}
	td.retry(t, "a", true)
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
	td.waitPlan(t, "auth", "a failed", stateIs("a:failed b:pending"))
	td.skip(t, "a")
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
	td.waitPlan(t, "auth", "a failed", stateIs("a:failed b:pending"))
	touch(t, filepath.Join(slot, "loose.txt"))
	td.skip(t, "a")
	c = td.waitPlan(t, "auth", "b running", stateIs("a:skipped b:running"))
	if got := *taskIn(c, "b").SlotPath; got != filepath.Join(filepath.Dir(slot), "auth-2") {
		t.Errorf("b in %s", got)
	}
	if !exists(filepath.Join(slot, "loose.txt")) || gitOut(t, slot, "branch", "--show-current") != "task/auth/a" || taskIn(c, "a").SlotPath != nil {
		t.Errorf("a's work was not kept in %s: %+v", slot, taskIn(c, "a"))
	}
}

// TestPlanCancelled: cancelling stops the agents, removes the slots holding no work,
// keeps those that do, and closes the run, its tasks as they were; then removes the
// agents, of the task running and of the one done.
func TestPlanCancelled(t *testing.T) {
	t.Parallel()
	td := start(t)
	repo := td.planProject(t, "b exit\n")
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n  - id: b\n    prompt: p\n")
	td.runPlan(t, "auth", 2)
	c := td.waitPlan(t, "auth", "both running", stateIs("a:running b:running"))
	a, b := taskIn(c, "a"), taskIn(c, "b")
	td.finishTask(t, repo, "auth", "a")
	td.waitPlan(t, "auth", "a done", stateIs("a:done b:running"))
	touch(t, filepath.Join(*b.SlotPath, "loose.txt"))
	td.cancel(t)
	c = td.waitPlan(t, "auth", "closed, its agents removed", func(c api.PlanContent) bool {
		return c.Run.FinishedAt != nil && taskIn(c, "a").Agent == nil && taskIn(c, "b").Agent == nil
	})
	if states(c) != "a:done b:running" || c.Plan.State != api.PlanStateCancelled || taskIn(c, "a").Summary == nil {
		t.Errorf("cancelled: %s, %s, a %+v", states(c), c.Plan.State, taskIn(c, "a"))
	}
	for _, id := range []string{a.Agent.Id, b.Agent.Id} {
		if _, err := td.daemon.agents.Store().Get(context.Background(), id); !errors.Is(err, agent.ErrNotFound) {
			t.Errorf("agent %s not removed: %v", id, err)
		}
	}
	if exists(*a.SlotPath) || !exists(filepath.Join(*b.SlotPath, "loose.txt")) {
		t.Errorf("a's slot kept, or b's work lost")
	}
}

// TestPlanAgentNotRemoved: a task's agent live when its plan is done, as the person
// restarted it, is not removed, and the plan is done all the same.
func TestPlanAgentNotRemoved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	repo := td.planProject(t, "a exit\n")
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")
	td.runPlan(t, "auth", 0)
	c := td.waitPlan(t, "auth", "a running", stateIs("a:running"))
	a := taskIn(c, "a").Agent
	end := filepath.Join(repo, ".git", "end-a")
	touch(t, end)
	td.waitPlan(t, "auth", "a failed", stateIs("a:failed"))
	if err := os.Remove(end); err != nil {
		t.Fatal(err)
	}
	if r, err := td.client.RestartAgentWithResponse(ctx, "id:"+a.Id, nil, api.RestartAgent{Force: optional(true)}); err != nil || r.JSON200 == nil {
		t.Fatalf("restart: %v %s", err, r.Body)
	}
	td.skip(t, "a")
	c = td.waitPlan(t, "auth", "done, its agent not removed", func(c api.PlanContent) bool {
		return c.Plan.State == api.PlanStateDone && td.logged(t, "plan agent not removed")
	})
	if got := taskIn(c, "a"); got.State != api.PlanTaskStateSkipped || got.Agent == nil || got.Agent.Id != a.Id || !td.agentLive(t, a.Id) {
		t.Errorf("a: %+v", got)
	}
	touch(t, end)
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
	td.cancel(t)
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

// TestPlanRunAnswers: pause, resume and parallel, and what they and a task's answers
// refuse; a plan shows paused while its project is closed, and goes on as it was
// when it opens.
func TestPlanRunAnswers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.planProject(t, "a exit\n")
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n  - id: b\n    after: [a]\n    prompt: p\n")
	params := &api.PausePlanParams{Project: optional("app")}
	pause := func() (*api.PlanResult, *api.Problem) {
		r, err := td.client.PausePlanWithResponse(ctx, "auth", params)
		if err != nil {
			t.Fatal(err)
		}
		return r.JSON200, r.ApplicationproblemJSONDefault
	}
	resume := func() (*api.PlanResult, *api.Problem) {
		r, err := td.client.ResumePlanWithResponse(ctx, "auth", &api.ResumePlanParams{Project: optional("app")})
		if err != nil {
			t.Fatal(err)
		}
		return r.JSON200, r.ApplicationproblemJSONDefault
	}
	update := func(body api.UpdatePlan) (*api.PlanResult, *api.Problem) {
		r, err := td.client.UpdatePlanWithResponse(ctx, "auth", &api.UpdatePlanParams{Project: optional("app")}, body)
		if err != nil {
			t.Fatal(err)
		}
		return r.JSON200, r.ApplicationproblemJSONDefault
	}
	retry := func(task string) *api.Problem {
		r, err := td.client.RetryPlanTaskWithResponse(ctx, "auth", task, taskParams[api.RetryPlanTaskParams](), api.RetryPlanTask{})
		if err != nil {
			t.Fatal(err)
		}
		return r.ApplicationproblemJSONDefault
	}
	skip := func(task string) *api.Problem {
		r, err := td.client.SkipPlanTaskWithResponse(ctx, "auth", task, taskParams[api.SkipPlanTaskParams]())
		if err != nil {
			t.Fatal(err)
		}
		return r.ApplicationproblemJSONDefault
	}
	want := func(r *api.PlanResult, problem *api.Problem, result api.Result, state api.PlanState) {
		t.Helper()
		if r == nil || r.Result != result || r.Plan.State != state {
			t.Errorf("got %+v, %+v; want %s, %s", r, problem, result, state)
		}
	}
	parallel := func(n int) api.UpdatePlan { return api.UpdatePlan{Parallel: &n} }

	_, problem := pause()
	wantProblem(t, problem, http.StatusConflict, api.ErrorCodePlanNotRunning)
	_, problem = update(parallel(2))
	wantProblem(t, problem, http.StatusConflict, api.ErrorCodePlanNotRunning)
	wantProblem(t, retry("a"), http.StatusConflict, api.ErrorCodePlanNotRunning)
	_, problem = update(api.UpdatePlan{})
	wantProblem(t, problem, http.StatusBadRequest, api.ErrorCodeUsage)
	two := 2
	_, problem = update(api.UpdatePlan{Name: optional("x"), Parallel: &two})
	wantProblem(t, problem, http.StatusBadRequest, api.ErrorCodeUsage)

	td.runPlan(t, "auth", 0)
	td.waitPlan(t, "auth", "a running", stateIs("a:running b:pending"))
	wantProblem(t, retry("a"), http.StatusConflict, api.ErrorCodeTaskState)
	wantProblem(t, skip("a"), http.StatusConflict, api.ErrorCodeTaskState)
	r, problem := update(parallel(4))
	want(r, problem, api.ResultUpdated, api.PlanStateRunning)
	r, problem = update(parallel(4))
	want(r, problem, api.ResultUnchanged, api.PlanStateRunning)
	three := 3
	_, problem = update(api.UpdatePlan{Parallel: &three, Folder: optional("a.b")})
	wantProblem(t, problem, http.StatusUnprocessableEntity, api.ErrorCodeInvalidArgument)
	r, problem = update(parallel(4))
	want(r, problem, api.ResultUnchanged, api.PlanStateRunning) // the folder refused, parallel was not changed
	r, problem = update(api.UpdatePlan{Folder: optional("/run")})
	want(r, problem, api.ResultUpdated, api.PlanStateRunning)
	if r != nil && r.Plan.Folder != "/run" {
		t.Errorf("a running plan moved to %s", r.Plan.Folder)
	}
	five := 5
	r, problem = update(api.UpdatePlan{Parallel: &five, Folder: optional("/run")})
	want(r, problem, api.ResultUpdated, api.PlanStateRunning) // parallel changed, the folder did not
	r, problem = resume()
	want(r, problem, api.ResultUnchanged, api.PlanStateRunning)
	r, problem = pause()
	want(r, problem, api.ResultPaused, api.PlanStatePaused)
	r, problem = pause()
	want(r, problem, api.ResultUnchanged, api.PlanStatePaused)
	r, problem = resume()
	want(r, problem, api.ResultResumed, api.PlanStateRunning)

	closed, err := td.client.UpdateProjectWithResponse(ctx, "app", api.UpdateProject{State: api.ProjectStateClosed, StopRunning: optional(true)})
	if err != nil || closed.JSON200 == nil {
		t.Fatalf("close: %v %s", err, closed.Body)
	}
	c := td.waitPlan(t, "auth", "paused with its project", func(c api.PlanContent) bool { return c.Plan.State == api.PlanStatePaused })
	if c.Run.State != api.PlanStatePaused || c.Run.PausedReason == nil || *c.Run.PausedReason != api.PlanRunPausedReasonProjectClosed {
		t.Errorf("run: %+v", c.Run)
	}
	_, problem = resume()
	wantProblem(t, problem, http.StatusConflict, api.ErrorCodeProjectClosed)
	r, problem = pause()
	want(r, problem, api.ResultPaused, api.PlanStatePaused)
	opened, err := td.client.UpdateProjectWithResponse(ctx, "app", api.UpdateProject{State: api.ProjectStateOpen})
	if err != nil || opened.JSON200 == nil {
		t.Fatalf("open: %v %s", err, opened.Body)
	}
	c = td.waitPlan(t, "auth", "paused by the person", func(c api.PlanContent) bool {
		return c.Run.PausedReason != nil && *c.Run.PausedReason == api.PlanRunPausedReasonPerson
	})
	if c.Plan.State != api.PlanStatePaused {
		t.Errorf("plan: %+v", c.Plan)
	}
	r, problem = resume()
	want(r, problem, api.ResultResumed, api.PlanStateRunning)
	td.waitPlan(t, "auth", "running again", func(c api.PlanContent) bool { return c.Run.PausedReason == nil && c.Plan.State == api.PlanStateRunning })

	td.cancel(t)
	_, problem = pause()
	wantProblem(t, problem, http.StatusConflict, api.ErrorCodePlanNotRunning)
	cancelled, err := td.client.CancelPlanWithResponse(ctx, "auth", &api.CancelPlanParams{Project: optional("app")})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, cancelled.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodePlanNotRunning)
}

// TestPlanFailedTaskDoneByThePerson: a failed task the person says is done lands, and
// what waits for it starts.
func TestPlanFailedTaskDoneByThePerson(t *testing.T) {
	t.Parallel()
	td := start(t)
	repo := td.planProject(t, "a exit\n")
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n  - id: b\n    after: [a]\n    prompt: p\n")
	td.runPlan(t, "auth", 0)
	td.waitPlan(t, "auth", "a running", stateIs("a:running b:pending"))
	touch(t, filepath.Join(repo, ".git", "end-a"))
	td.waitPlan(t, "auth", "a failed", stateIs("a:failed b:pending"))
	r, err := td.client.FinishPlanTaskWithResponse(context.Background(), "auth", "a", &api.FinishPlanTaskParams{Project: optional("app")}, api.FinishPlanTask{Summary: "by hand"})
	if err != nil || r.JSON200 == nil || r.JSON200.Result != api.ResultLanding {
		t.Fatalf("done: %v %s", err, r.Body)
	}
	c := td.waitPlan(t, "auth", "b running", stateIs("a:done b:running"))
	if s := taskIn(c, "a").Summary; s == nil || *s != "by hand" {
		t.Errorf("a: %+v", taskIn(c, "a"))
	}
}

// TestPlanMoreRoomStartsTasks: a run's parallel raised starts what waits for room,
// and a plan resumed starts what its pause held back.
func TestPlanMoreRoomStartsTasks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	td.planProject(t, "a exit\nb exit\nc exit\n")
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n  - id: b\n    prompt: p\n  - id: c\n    prompt: p\n")
	td.runPlan(t, "auth", 1)
	td.waitPlan(t, "auth", "a running", stateIs("a:running b:pending c:pending"))
	set := func(n int) {
		t.Helper()
		r, err := td.client.UpdatePlanWithResponse(ctx, "auth", &api.UpdatePlanParams{Project: optional("app")}, api.UpdatePlan{Parallel: &n})
		if err != nil || r.JSON200 == nil || r.JSON200.Result != api.ResultUpdated {
			t.Fatalf("parallel %d: %v %s", n, err, r.Body)
		}
	}
	set(2)
	td.waitPlan(t, "auth", "b running", stateIs("a:running b:running c:pending"))
	if r, err := td.client.PausePlanWithResponse(ctx, "auth", &api.PausePlanParams{Project: optional("app")}); err != nil || r.JSON200 == nil {
		t.Fatalf("pause: %v %s", err, r.Body)
	}
	set(3) // wakes the plan, which starts nothing while paused
	if r, err := td.client.ResumePlanWithResponse(ctx, "auth", &api.ResumePlanParams{Project: optional("app")}); err != nil || r.JSON200 == nil {
		t.Fatalf("resume: %v %s", err, r.Body)
	}
	td.waitPlan(t, "auth", "c running", stateIs("a:running b:running c:running"))
}
