package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/style"
)

// planTaskScript is what a task's agent runs, from the repository: it commits a
// file named after its task, then says the task is done through the wdx binary,
// unless tasks.conf names the task manual, which leaves that to the test.
const planTaskScript = `task=$(git symbolic-ref --short HEAD); task=${task##*/}
printf '%s\n' "$task" > "$task.txt"
git add "$task.txt" && git -c user.name=t -c user.email=t@t commit -qm "$task"
touch "$(git rev-parse --path-format=absolute --git-common-dir)/committed-$task"
if [ "$(sed -n "s/^$task //p" tasks.conf)" != manual ]; then
  "$WDX" plan task done --agent-id "$WDX_AGENT_ID" --summary "did $task" > /dev/null
fi
`

// planRepo registers, as the project app, a repository whose tasks' agents run
// planTaskScript with conf as tasks.conf, and returns it.
func planRepo(t *testing.T, home, conf string) string {
	t.Helper()
	repo := gittest.Repo(t, "app")
	gittest.Git(t, repo, "branch", "-M", "main")
	script := "WDX=" + wdxBinary + "\n" + planTaskScript
	for name, content := range map[string]string{"run": script, "tasks.conf": conf} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gittest.Git(t, repo, "add", ".")
	gittest.Git(t, repo, "commit", "-qm", "the tasks' script")
	if v, exit := wdxJSON(t, home, "project", "open", "--local", repo, "--name", "app"); exit != api.ExitOK {
		t.Fatalf("open: exit %d, %+v", exit, v)
	}
	return repo
}

// waitTasks waits until plan status says the plan's tasks are as want, as
// "a:done b:running", and returns what it said.
func waitTasks(t *testing.T, home, dir, plan, want string) jsonOutput {
	t.Helper()
	var v jsonOutput
	var got string
	if !eventually(t, func() bool {
		v, _ = wdxJSONWith(t, home, in(dir), "plan", "status", "--name", plan)
		var list []string
		for _, task := range *v.Definition.Tasks {
			list = append(list, task.Id+":"+string(task.State))
		}
		got = strings.Join(list, " ")
		return got == want
	}) {
		t.Fatalf("tasks of %s: %s, want %s", plan, got, want)
	}
	return v
}

func waitCommitted(t *testing.T, repo, task string) {
	t.Helper()
	if !eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(repo, ".git", "committed-"+task))
		return err == nil
	}) {
		t.Fatalf("%s did not commit", task)
	}
}

// TestPlanRunsThroughItsAgents: each task's agent says it is done through wdx,
// with --agent-id, and the plan runs to done.
func TestPlanRunsThroughItsAgents(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	repo := planRepo(t, home, "")
	newPlan(t, home, repo, "sso", "goal: g\ntasks:\n  - id: a\n    prompt: p\n  - id: b\n    after: [a]\n    prompt: p\n")
	v, exit := wdxJSONWith(t, home, in(repo), "plan", "run", "--name", "sso", "--parallel", "1")
	if exit != api.ExitOK || v.Result != "started" || v.Plan.State != api.PlanStateRunning {
		t.Fatalf("run: exit %d, %+v", exit, v)
	}
	v = waitTasks(t, home, repo, "sso", "a:done b:done")
	if !eventually(t, func() bool {
		v, _ = wdxJSONWith(t, home, in(repo), "plan", "status", "--name", "sso")
		return v.Plan.State == api.PlanStateDone
	}) {
		t.Fatalf("the plan: %+v", v.Plan)
	}
	if v.Run == nil || v.Run.Branch != "plan/sso" || *v.Plan.TasksDone != 2 {
		t.Errorf("status: %+v, %+v", v.Run, v.Plan)
	}
	v, exit = wdxJSONWith(t, home, in(repo), "plan", "task", "get", "--plan", "sso", "--task", "b")
	if exit != api.ExitOK || v.Task == nil || v.Task.Summary == nil || *v.Task.Summary != "did b" || v.Task.Prompt != "p" || v.Plan.Name != "sso" {
		t.Errorf("task get: exit %d, %+v", exit, v.Task)
	}
	v, exit = wdxJSONWith(t, home, in(repo), "plan", "task", "get", "--plan", "sso", "--task", "nowhere")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
}

func TestPlanRun(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	repo := planRepo(t, home, "a manual\n")
	newPlan(t, home, repo, "child", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")
	newPlan(t, home, repo, "parent", "goal: g\nplans:\n  - id: child\n")
	newPlan(t, home, repo, "sso", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")

	v, exit := wdxJSONWith(t, home, in(repo), "plan", "run", "--name", "sso", "--parallel", "0")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	v, exit = wdxJSONWith(t, home, in(repo), "plan", "run", "--name", "parent", "--parallel", "2")
	wantError(t, v, exit, api.ErrorCodeInvalidArgument, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(repo), "plan", "run", "--name", "child")
	wantError(t, v, exit, api.ErrorCodePlanInUse, api.ExitRejected)
	wantHint(t, v, "a plan a plan of plans lists runs with it: run the plan of plans")
	v, exit = wdxJSONWith(t, home, in(repo), "plan", "run", "--name", "nowhere")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	gittest.Git(t, repo, "branch", "plan/taken")
	newPlan(t, home, repo, "taken", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")
	v, exit = wdxJSONWith(t, home, in(repo), "plan", "run", "--name", "taken")
	wantError(t, v, exit, api.ErrorCodeBranchExists, api.ExitRejected)
	wantHint(t, v, "remove or rename the branch, or rename the plan")

	v, exit = wdxJSONWith(t, home, in(repo), "plan", "run", "--name", "sso")
	if exit != api.ExitOK || v.Result != "started" {
		t.Fatalf("run: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(repo), "plan", "run", "--name", "sso")
	wantError(t, v, exit, api.ErrorCodePlanStarted, api.ExitRejected)
	wantHint(t, v, "a plan runs once: copy it to run it again")
	v, exit = wdxJSONWith(t, home, in(repo), "plan", "update", "--name", "sso", "--path", writeDocFile(t, "x.yaml", planYAML), "--base", "1")
	wantError(t, v, exit, api.ErrorCodePlanStarted, api.ExitRejected)

	if v, exit := wdxJSON(t, home, "project", "close", "--name", "app", "--stop-running"); exit != api.ExitOK {
		t.Fatalf("close: exit %d, %+v", exit, v)
	}
	newPlan(t, home, repo, "later", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")
	v, exit = wdxJSONWith(t, home, in(repo), "plan", "run", "--name", "later")
	wantError(t, v, exit, api.ErrorCodeProjectClosed, api.ExitRejected)
}

func TestPlanTaskDone(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	repo := planRepo(t, home, "a manual\nb manual\n")
	newPlan(t, home, repo, "sso", "goal: g\ntasks:\n  - id: a\n    prompt: p\n  - id: b\n    after: [a]\n    prompt: p\n")
	done := func(args ...string) (jsonOutput, int) {
		t.Helper()
		return wdxJSONWith(t, home, in(repo), append([]string{"plan", "task", "done"}, args...)...)
	}
	v, exit := done("--plan", "sso", "--task", "a", "--summary", "s")
	wantError(t, v, exit, api.ErrorCodePlanNotRunning, api.ExitRejected)
	v, exit = done("--plan", "sso", "--summary", "s")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	v, exit = done("--plan", "sso", "--task", "a", "--agent-id", "agent-x", "--summary", "s")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	v, exit = done("--plan", "sso", "--agent-id", "agent-x", "--summary", "s")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	v, exit = done("--plan", "sso", "--task", "a", "--summary", " ")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	if !strings.Contains(v.Detail, "--summary is blank") {
		t.Errorf("a blank summary: %q", v.Detail)
	}
	helper := newAgent(t, home, repo, "helper")
	v, exit = done("--agent-id", helper.Id, "--summary", "s")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)

	if _, exit := wdxJSONWith(t, home, in(repo), "plan", "run", "--name", "sso"); exit != api.ExitOK {
		t.Fatal("not started")
	}
	s := waitTasks(t, home, repo, "sso", "a:running b:pending")
	slot := *(*s.Definition.Tasks)[0].SlotPath
	waitCommitted(t, repo, "a")
	v, exit = done("--plan", "sso", "--task", "b", "--summary", "s")
	wantError(t, v, exit, api.ErrorCodeTaskState, api.ExitRejected)
	if err := os.WriteFile(filepath.Join(slot, "loose.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, exit = done("--plan", "sso", "--task", "a", "--summary", "s")
	wantError(t, v, exit, api.ErrorCodeNotCommitted, api.ExitRejected)
	wantHint(t, v, "commit your work on the task's branch first")
	if err := os.Remove(filepath.Join(slot, "loose.txt")); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, slot, "switch", "-q", "--detach")
	v, exit = done("--plan", "sso", "--task", "a", "--summary", "s")
	wantError(t, v, exit, api.ErrorCodeWrongBranch, api.ExitRejected)
	wantHint(t, v, "switch the worktree back to the task's branch, with its commits")
	gittest.Git(t, slot, "switch", "-q", "task/sso/a")

	v, exit = done("--agent-id", (*s.Definition.Tasks)[0].Agent.Id, "--summary", "did a")
	if exit != api.ExitOK || v.Result != "landing" || v.Task.Id != "a" || v.Task.State != api.PlanTaskStateLanding {
		t.Fatalf("by its agent: exit %d, %+v", exit, v)
	}
	waitTasks(t, home, repo, "sso", "a:done b:running")
	waitCommitted(t, repo, "b")
	v, exit = done("--plan", "sso", "--task", "b", "--summary", "did b")
	if exit != api.ExitOK || v.Result != "landing" || v.Task.Id != "b" {
		t.Fatalf("by a person: exit %d, %+v", exit, v)
	}
	waitTasks(t, home, repo, "sso", "a:done b:done")
}

func TestPlanRunText(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	repo := planRepo(t, home, "a manual\n")
	newPlan(t, home, repo, "sso", "goal: g\ntasks:\n  - id: a\n    prompt: Add the table.\n  - id: b\n    after: [a]\n    prompt: p\n")
	out := wdxWith(t, home, in(repo), "plan", "run", "--name", "sso", "--parallel", "2")
	if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "plan sso running on plan/sso, up to 2 tasks at once\n") {
		t.Errorf("run: %+v", out)
	}
	waitTasks(t, home, repo, "sso", "a:running b:pending")
	waitCommitted(t, repo, "a")
	out = wdxWith(t, home, in(repo), "plan", "status", "--name", "sso")
	for _, want := range []string{"sso: running, 0/2 tasks done, on plan/sso\n", "TASK", "AGENT", "sso-a", "sso-1", "waiting for a"} {
		if !strings.Contains(out.stdout, want) {
			t.Errorf("status: want %q in\n%s", want, out.stdout)
		}
	}
	out = wdxWith(t, home, in(repo), "plan", "task", "get", "--plan", "sso", "--task", "a")
	if !strings.HasPrefix(out.stdout, "a of sso: running\n") || !strings.Contains(out.stdout, "PROMPT\nAdd the table.\n") {
		t.Errorf("task get: %+v", out)
	}
	out = wdxWith(t, home, in(repo), "plan", "task", "done", "--plan", "sso", "--task", "a", "--summary", "did a")
	if out.exit != api.ExitOK || out.stdout != "task a of sso done: landing on plan/sso\n" {
		t.Errorf("task done: %+v", out)
	}
	waitTasks(t, home, repo, "sso", "a:done b:running")
	out = wdxWith(t, home, in(repo), "plan", "task", "get", "--plan", "sso", "--task", "a")
	if !strings.HasPrefix(out.stdout, "a of sso: done\n") || !strings.HasSuffix(out.stdout, "\nSUMMARY\ndid a\n") {
		t.Errorf("task get once done: %+v", out)
	}
	out = wdxWith(t, home, in(repo), "plan", "list")
	if !strings.Contains(out.stdout, "running") || !strings.Contains(out.stdout, "1/2") {
		t.Errorf("list: %+v", out)
	}
}

// TestPlanOfPlansRun: a plan of plans starts the plan that waits for nothing, and
// says, as its plans do, how far it is.
func TestPlanOfPlansRun(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	repo := planRepo(t, home, "a manual\n")
	newPlan(t, home, repo, "sso", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")
	newPlan(t, home, repo, "ui", "goal: g\ntasks:\n  - id: b\n    prompt: p\n")
	newPlan(t, home, repo, "big", "goal: g\nplans:\n  - id: sso\n  - id: ui\n    after: [sso]\n")
	v, exit := wdxJSONWith(t, home, in(repo), "plan", "run", "--name", "big")
	if exit != api.ExitOK || v.Result != "started" || v.Plan.PlansDone == nil || *v.Plan.PlansDone != 0 || v.Plan.TasksDone != nil {
		t.Fatalf("run: exit %d, %+v", exit, v)
	}
	waitTasks(t, home, repo, "sso", "a:running")
	out := wdxWith(t, home, in(repo), "plan", "status", "--name", "big")
	for _, want := range []string{"big: running, 0/2 plans done\n", "PLAN", "TASKS", "0/1", "waiting for sso", "1 task"} {
		if !strings.Contains(out.stdout, want) {
			t.Errorf("status: want %q in\n%s", want, out.stdout)
		}
	}
	out = wdxWith(t, home, in(repo), "plan", "list")
	if !strings.Contains(out.stdout, "0/2 plans") {
		t.Errorf("list: %+v", out)
	}
	waitCommitted(t, repo, "a")
	if v, exit := wdxJSONWith(t, home, in(repo), "plan", "task", "done", "--plan", "sso", "--task", "a", "--summary", "s"); exit != api.ExitOK {
		t.Fatalf("task done: exit %d, %+v", exit, v)
	}
	if !eventually(t, func() bool {
		out = wdxWith(t, home, in(repo), "plan", "status", "--name", "big")
		return strings.Contains(out.stdout, "1/2 plans done") && strings.Contains(out.stdout, "waiting for you")
	}) {
		t.Errorf("ui waiting for the person:\n%s", out.stdout)
	}
}

func TestPlanOfPlansRunText(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	repo := planRepo(t, home, "a manual\n")
	newPlan(t, home, repo, "sso", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")
	newPlan(t, home, repo, "big", "goal: g\nplans:\n  - id: sso\n")
	out := wdxWith(t, home, in(repo), "plan", "run", "--name", "big")
	if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "plan big running, each of its plans once those before it have landed\n") || !strings.Contains(out.stdout, "sso") {
		t.Errorf("run: %+v", out)
	}
	waitTasks(t, home, repo, "sso", "a:running")
}

// TestChildState: a draft a plan of plans lists says what it waits for while its
// parent runs: the person, or the plans before it not done.
func TestChildState(t *testing.T) {
	t.Parallel()
	var st style.Style
	done := api.PlanChild{Id: "a", State: api.PlanStateDone}
	running := api.PlanChild{Id: "b", State: api.PlanStateRunning}
	draft := api.PlanChild{Id: "c", State: api.PlanStateDraft, After: []string{"a", "b"}}
	all := []api.PlanChild{done, running, draft}
	for _, tt := range []struct {
		name      string
		c         api.PlanChild
		all       []api.PlanChild
		parentRan bool
		want      string
	}{
		{"one before it not done", draft, all, true, "waiting for b"},
		{"every one before it done", draft, []api.PlanChild{done, {Id: "b", State: api.PlanStateDone}, draft}, true, "draft"},
		{"waiting for the person", api.PlanChild{Id: "c", State: api.PlanStateDraft, Waiting: true}, all, true, "waiting for you"},
		{"its parent a draft", draft, all, false, "draft"},
		{"started", running, all, true, "running"},
	} {
		if got := childState(st, tt.c, tt.all, tt.parentRan); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}
