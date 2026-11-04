package cli

import (
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

// failTask removes the agent of the plan's running task, which fails it: running
// and failed are the plan's tasks before and after, as waitTasks takes them.
func failTask(t *testing.T, home, repo, plan, task, running, failed string) {
	t.Helper()
	s := waitTasks(t, home, repo, plan, running)
	for _, tk := range *s.Definition.Tasks {
		if tk.Id == task {
			if v, exit := wdxJSONWith(t, home, in(repo), "agent", "remove", "--id", tk.Agent.Id, "--stop-running"); exit != api.ExitOK {
				t.Fatalf("remove %s's agent: exit %d, %+v", task, exit, v)
			}
		}
	}
	waitTasks(t, home, repo, plan, failed)
}

// TestPlanRunAnswered: each outcome of pause, resume, set and cancel.
func TestPlanRunAnswered(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	repo := planRepo(t, home, "a manual\nb manual\n")
	newPlan(t, home, repo, "sso", "goal: g\ntasks:\n  - id: a\n    prompt: p\n  - id: b\n    prompt: p\n")
	run := func(args ...string) (jsonOutput, int) {
		t.Helper()
		return wdxJSONWith(t, home, in(repo), append([]string{"plan"}, args...)...)
	}
	want := func(v jsonOutput, exit int, result string, state api.PlanState) {
		t.Helper()
		if exit != api.ExitOK || v.Result != result || v.Plan == nil || v.Plan.State != state {
			t.Errorf("want %s, %s: exit %d, %+v", result, state, exit, v)
		}
	}
	v, exit := run("pause", "--name", "sso")
	wantError(t, v, exit, api.ErrorCodePlanNotRunning, api.ExitRejected)
	wantHint(t, v, "only a running or paused plan takes this: wdx plan status says what it is")
	v, exit = run("set", "--name", "sso", "--parallel", "2")
	wantError(t, v, exit, api.ErrorCodePlanNotRunning, api.ExitRejected)
	v, exit = run("set", "--name", "sso", "--parallel", "21")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	if !strings.Contains(v.Detail, "--parallel 21 is not from 1 to 20") {
		t.Errorf("parallel 21: %q", v.Detail)
	}
	v, exit = run("cancel", "--name", "sso")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)

	if _, exit := run("run", "--name", "sso", "--parallel", "1"); exit != api.ExitOK {
		t.Fatal("not started")
	}
	waitTasks(t, home, repo, "sso", "a:running b:pending")
	v, exit = run("pause", "--name", "sso")
	want(v, exit, "paused", api.PlanStatePaused)
	v, exit = run("pause", "--name", "sso")
	want(v, exit, "unchanged", api.PlanStatePaused)
	v, exit = run("resume", "--name", "sso")
	want(v, exit, "resumed", api.PlanStateRunning)
	v, exit = run("resume", "--name", "sso")
	want(v, exit, "unchanged", api.PlanStateRunning)
	v, exit = run("set", "--name", "sso", "--parallel", "2")
	want(v, exit, "updated", api.PlanStateRunning)
	v, exit = run("set", "--name", "sso", "--parallel", "2")
	want(v, exit, "unchanged", api.PlanStateRunning)
	waitTasks(t, home, repo, "sso", "a:running b:running")

	if v, exit := wdxJSON(t, home, "project", "close", "--name", "app", "--stop-running"); exit != api.ExitOK {
		t.Fatalf("close: exit %d, %+v", exit, v)
	}
	v, exit = run("resume", "--name", "sso")
	wantError(t, v, exit, api.ErrorCodeProjectClosed, api.ExitRejected)
	wantHint(t, v, "open it first with wdx project open; its plans go on as they were")
	v, exit = run("status", "--name", "sso")
	if exit != api.ExitOK || v.Plan.State != api.PlanStatePaused || v.Run == nil || v.Run.PausedReason == nil || *v.Run.PausedReason != api.PlanRunPausedReasonProjectClosed {
		t.Errorf("status of a closed project's plan: exit %d, %+v, %+v", exit, v.Plan, v.Run)
	}
	if out := wdxWith(t, home, in(repo), "plan", "status", "--name", "sso"); !strings.HasPrefix(out.stdout, "sso: paused (project closed), 0/2 tasks done, on plan/sso\n") {
		t.Errorf("status text of a closed project's plan: %+v", out)
	}
	if v, exit := wdxJSON(t, home, "project", "open", "--local", repo, "--name", "app"); exit != api.ExitOK {
		t.Fatalf("open: exit %d, %+v", exit, v)
	}

	v, exit = run("cancel", "--name", "sso", "--yes")
	want(v, exit, "cancelled", api.PlanStateCancelled)
	v, exit = run("cancel", "--name", "sso", "--yes")
	wantError(t, v, exit, api.ErrorCodePlanNotRunning, api.ExitRejected)
}

// TestPlanCancelAsks: plan cancel asks first, and no stops it.
func TestPlanCancelAsks(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	repo := planRepo(t, home, "a manual\n")
	newPlan(t, home, repo, "sso", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")
	if _, exit := wdxJSONWith(t, home, in(repo), "plan", "run", "--name", "sso"); exit != api.ExitOK {
		t.Fatal("not started")
	}
	out := wdxWith(t, home, in(repo), "plan", "cancel", "--name", "sso")
	if out.exit != api.ExitUsage || !strings.Contains(out.stderr, "pass --yes") {
		t.Errorf("without a terminal: %+v", out)
	}
	out = wdxWith(t, home, both(in(repo), answering(t, "n")), "plan", "cancel", "--name", "sso")
	if out.exit != api.ExitRejected || !strings.Contains(out.stderr, "cancel plan sso? its running agents stop and it cannot run again [y/N]") || !strings.Contains(out.stderr, "[cancelled]") {
		t.Errorf("answered no: %+v", out)
	}
	out = wdxWith(t, home, both(in(repo), answering(t, "y")), "plan", "cancel", "--name", "sso")
	if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "plan sso cancelled: its agents stop, its worktrees holding work stay, and so does plan/sso\n") {
		t.Errorf("answered yes: %+v", out)
	}
}

// TestPlanTaskAnswered: each outcome of retry, land and skip, and what status, list
// and task get show of a task that waits for the person.
func TestPlanTaskAnswered(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	repo := planRepo(t, home, "a manual\nb manual\n")
	newPlan(t, home, repo, "sso", "goal: g\ntasks:\n  - id: a\n    prompt: p\n  - id: b\n    after: [a]\n    prompt: p\n")
	task := func(verb string, args ...string) (jsonOutput, int) {
		t.Helper()
		return wdxJSONWith(t, home, in(repo), append([]string{"plan", "task", verb, "--plan", "sso"}, args...)...)
	}
	v, exit := task("retry", "--task", "a")
	wantError(t, v, exit, api.ErrorCodePlanNotRunning, api.ExitRejected)
	if v, exit := wdxJSON(t, home, "project", "landing", "--name", "app", "--check", "false"); exit != api.ExitOK {
		t.Fatalf("landing check: exit %d, %+v", exit, v)
	}
	if _, exit := wdxJSONWith(t, home, in(repo), "plan", "run", "--name", "sso"); exit != api.ExitOK {
		t.Fatal("not started")
	}
	waitTasks(t, home, repo, "sso", "a:running b:pending")
	v, exit = task("retry", "--task", "a")
	wantError(t, v, exit, api.ErrorCodeTaskState, api.ExitRejected)
	wantHint(t, v, "wdx plan task get says what it is")
	v, exit = task("skip", "--task", "a")
	wantError(t, v, exit, api.ErrorCodeTaskState, api.ExitRejected)
	v, exit = task("land", "--task", "a")
	wantError(t, v, exit, api.ErrorCodeTaskState, api.ExitRejected)

	waitCommitted(t, repo, "a")
	if _, exit := task("done", "--task", "a", "--summary", "did a"); exit != api.ExitOK {
		t.Fatal("a not done")
	}
	waitTasks(t, home, repo, "sso", "a:land_failed b:pending")
	out := wdxWith(t, home, in(repo), "plan", "status", "--name", "sso")
	for _, want := range []string{"sso: running, 0/2 tasks done, 1 failed, on plan/sso\n", "land failed: check (terminal land-sso-1)"} {
		if !strings.Contains(out.stdout, want) {
			t.Errorf("status: want %q in\n%s", want, out.stdout)
		}
	}
	out = wdxWith(t, home, in(repo), "plan", "task", "get", "--plan", "sso", "--task", "a")
	if !strings.Contains(out.stdout, "its output in the terminal land-sso-1") {
		t.Errorf("task get: %s", out.stdout)
	}
	out = wdxWith(t, home, in(repo), "plan", "list")
	if !strings.Contains(out.stdout, "0/2, 1 failed") {
		t.Errorf("list: %s", out.stdout)
	}

	v, exit = task("retry", "--task", "a", "--fresh")
	if exit != api.ExitOK || v.Result != "retried" || v.Task.Agent != nil {
		t.Fatalf("retry fresh: exit %d, %+v", exit, v)
	}
	s := waitTasks(t, home, repo, "sso", "a:running b:pending")
	if a := (*s.Definition.Tasks)[0]; a.Agent == nil || a.Agent.Name != "sso-a-2" {
		t.Errorf("a retried fresh: %+v", a.Agent)
	}
	if _, exit := task("done", "--task", "a", "--summary", "did a"); exit != api.ExitOK {
		t.Fatal("a not done again")
	}
	waitTasks(t, home, repo, "sso", "a:land_failed b:pending")
	if v, exit := wdxJSON(t, home, "project", "landing", "--name", "app", "--check", "true"); exit != api.ExitOK {
		t.Fatalf("landing check: exit %d, %+v", exit, v)
	}
	v, exit = task("land", "--task", "a")
	if exit != api.ExitOK || v.Result != "landing" || v.Task.State != api.PlanTaskStateLanding {
		t.Fatalf("land: exit %d, %+v", exit, v)
	}
	failTask(t, home, repo, "sso", "b", "a:done b:running", "a:done b:failed")
	v, exit = task("retry", "--task", "b")
	if exit != api.ExitOK || v.Result != "retried" || v.Task.State != api.PlanTaskStatePending || v.Task.Agent != nil {
		t.Fatalf("retry of a task whose agent was removed: exit %d, %+v", exit, v)
	}
	s = waitTasks(t, home, repo, "sso", "a:done b:running")
	if b := (*s.Definition.Tasks)[1]; b.Agent == nil || b.Agent.Name != "sso-b-2" {
		t.Errorf("b retried with its agent removed: %+v", b.Agent)
	}
	failTask(t, home, repo, "sso", "b", "a:done b:running", "a:done b:failed")
	v, exit = task("skip", "--task", "b")
	if exit != api.ExitOK || v.Result != "skipped" || v.Task.State != api.PlanTaskStateSkipped {
		t.Fatalf("skip: exit %d, %+v", exit, v)
	}
}

// TestPlanAnswersText: what each answer prints.
func TestPlanAnswersText(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	repo := planRepo(t, home, "a manual\n")
	newPlan(t, home, repo, "sso", "goal: g\ntasks:\n  - id: a\n    prompt: p\n  - id: b\n    after: [a]\n    prompt: p\n")
	if _, exit := wdxJSONWith(t, home, in(repo), "plan", "run", "--name", "sso"); exit != api.ExitOK {
		t.Fatal("not started")
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"pause", "--name", "sso"}, "plan sso paused: nothing new starts, and its tasks at work go on\n"},
		{[]string{"pause", "--name", "sso"}, "plan sso was already paused\n"},
		{[]string{"resume", "--name", "sso"}, "plan sso running again\n"},
		{[]string{"resume", "--name", "sso"}, "plan sso was already running\n"},
		{[]string{"set", "--name", "sso", "--parallel", "4"}, "plan sso runs up to 4 tasks at once\n"},
		{[]string{"set", "--name", "sso", "--parallel", "4"}, "plan sso already runs up to 4 tasks at once\n"},
	} {
		out := wdxWith(t, home, in(repo), append([]string{"plan"}, c.args...)...)
		if out.exit != api.ExitOK || out.stdout != c.want {
			t.Errorf("%v: %+v, want %q", c.args, out, c.want)
		}
	}
	failTask(t, home, repo, "sso", "a", "a:running b:pending", "a:failed b:pending")
	out := wdxWith(t, home, in(repo), "plan", "task", "retry", "--plan", "sso", "--task", "a")
	if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "task a of sso retried: a new agent starts\n") {
		t.Errorf("retry of a task whose agent was removed: %+v", out)
	}
	failTask(t, home, repo, "sso", "a", "a:running b:pending", "a:failed b:pending")
	out = wdxWith(t, home, in(repo), "plan", "task", "skip", "--plan", "sso", "--task", "a")
	if out.exit != api.ExitOK || out.stdout != "task a of sso skipped: what waits for it goes on without it\n" {
		t.Errorf("skip: %+v", out)
	}
}
