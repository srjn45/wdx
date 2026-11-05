package daemon

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/agent"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/telemetry"
	"github.com/srjn45/wdx/internal/waittest"
)

// taskScript is what a task's agent runs, from the repository: it commits a file
// named after its task, or shared.txt for a task tasks.conf names shared, then
// marks the commit made in the repository's git dir, where no status sees it. A
// task tasks.conf names exit ends instead, once the test makes end-<task> there.
const taskScript = `task=$(git symbolic-ref --short HEAD); task=${task##*/}
what=$(sed -n "s/^$task //p" tasks.conf)
gitdir=$(git rev-parse --path-format=absolute --git-common-dir)
if [ "$what" = exit ]; then
  while [ ! -e "$gitdir/end-$task" ]; do sleep 0.05; done
  exit 0
fi
file=$task.txt
if [ "$what" = shared ]; then file=shared.txt; fi
printf '%s\n' "$task" > "$file"
git add "$file" && git -c user.name=t -c user.email=t@t commit -qm "$task"
touch "$gitdir/committed-$task"
`

// planProject registers a repository whose tasks run taskScript with conf as
// tasks.conf, as the project app, and returns its clone.
func (td *testDaemon) planProject(t *testing.T, conf string) string {
	t.Helper()
	repo := gittest.Repo(t, "app")
	for name, content := range map[string]string{"run": taskScript, "tasks.conf": conf} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gittest.Git(t, repo, "add", ".")
	gittest.Git(t, repo, "commit", "-qm", "the tasks' script")
	r, err := td.client.CreateProjectWithResponse(context.Background(), api.CreateProject{Source: localSource(repo), Name: optional("app")})
	if err != nil || r.JSON201 == nil {
		t.Fatalf("project: %v %s", err, r.Body)
	}
	return repo
}

func (td *testDaemon) runPlan(t *testing.T, name string, parallel int) api.Plan {
	t.Helper()
	body := api.RunPlan{}
	if parallel != 0 {
		body.Parallel = &parallel
	}
	r, err := td.client.RunPlanWithResponse(context.Background(), name, &api.RunPlanParams{Project: optional("app")}, body)
	if err != nil || r.JSON200 == nil || r.JSON200.Result != api.ResultStarted {
		t.Fatalf("run plan: %v %s", err, r.Body)
	}
	return r.JSON200.Plan
}

// waitPlan waits until the plan, read on each change to its project, is as want
// says, and returns it.
func (td *testDaemon) waitPlan(t *testing.T, name, what string, want func(api.PlanContent) bool) api.PlanContent {
	t.Helper()
	var got api.PlanContent
	td.waitProject(t, "app", what, func(event) bool {
		r, err := td.client.GetPlanWithResponse(context.Background(), name, &api.GetPlanParams{Project: optional("app")})
		if err != nil || r.JSON200 == nil {
			t.Fatalf("get plan: %v %s", err, r.Body)
		}
		got = *r.JSON200
		return want(got)
	})
	return got
}

func taskIn(c api.PlanContent, id string) api.PlanTask {
	for _, task := range *c.Definition.Tasks {
		if task.Id == id {
			return task
		}
	}
	return api.PlanTask{}
}

// states says what each task is in, in DAG order, as "a:done b:running".
func states(c api.PlanContent) string {
	var list []string
	for _, task := range *c.Definition.Tasks {
		list = append(list, task.Id+":"+string(task.State))
	}
	return strings.Join(list, " ")
}

func stateIs(want string) func(api.PlanContent) bool {
	return func(c api.PlanContent) bool { return states(c) == want }
}

// finishTask waits for the task's agent to commit, then says the task is done.
func (td *testDaemon) finishTask(t *testing.T, repo, plan, task string) api.PlanTask {
	t.Helper()
	waittest.For(t, task+"'s commit", func() bool {
		_, err := os.Stat(filepath.Join(repo, ".git", "committed-"+task))
		return err == nil
	})
	r, err := td.client.FinishPlanTaskWithResponse(context.Background(), plan, task, &api.FinishPlanTaskParams{Project: optional("app")}, api.FinishPlanTask{Summary: "did " + task})
	if err != nil || r.JSON200 == nil || r.JSON200.Result != api.ResultLanding || r.JSON200.Task.State != api.PlanTaskStateLanding {
		t.Fatalf("finish %s: %v %s", task, err, r.Body)
	}
	return r.JSON200.Task
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

const diamondPlan = `goal: Users log in with SSO.
tasks:
  - id: a
    prompt: Add the table.
  - id: b
    after: [a]
    prompt: Use it.
  - id: c
    after: [a]
    prompt: Show it.
  - id: d
    after: [b, c]
    prompt: Document it.
`

// TestPlanRunsToDone: a plan of a, then b and c at once, then d, runs to done in two
// slots, each task landing on the plan's branch, which ends in the one worktree left.
func TestPlanRunsToDone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	repo := td.planProject(t, "")
	td.createPlan(t, "app", "auth", diamondPlan)
	p := td.runPlan(t, "auth", 2)
	if p.State != api.PlanStateRunning || p.TasksDone == nil || *p.TasksDone != 0 {
		t.Errorf("started: %+v", p)
	}

	c := td.waitPlan(t, "auth", "a running", stateIs("a:running b:pending c:pending d:pending"))
	a := taskIn(c, "a")
	if a.Agent == nil || a.Agent.Name != "auth-a" || a.SlotPath == nil || filepath.Base(*a.SlotPath) != "auth-1" || !slices.Equal(taskIn(c, "b").WaitingFor, []string{"a"}) {
		t.Errorf("a: %+v", a)
	}
	if c.Run == nil || c.Run.Branch != "plan/auth" || c.Run.Parallel == nil || *c.Run.Parallel != 2 {
		t.Errorf("run: %+v", c.Run)
	}
	agent, err := td.client.GetAgentWithResponse(ctx, "id:"+a.Agent.Id, nil)
	if err != nil || agent.JSON200 == nil || agent.JSON200.Agent.Task == nil || *agent.JSON200.Agent.Task != (api.AgentTask{Plan: "auth", Task: "a"}) {
		t.Errorf("a's agent: %v %s", err, agent.Body)
	}

	td.finishTask(t, repo, "auth", "a")
	c = td.waitPlan(t, "auth", "b and c running", stateIs("a:done b:running c:running d:pending"))
	if s := taskIn(c, "a").Summary; s == nil || *s != "did a" {
		t.Errorf("a's summary: %v", s)
	}
	slots := map[string]bool{*taskIn(c, "b").SlotPath: true, *taskIn(c, "c").SlotPath: true}
	if len(slots) != 2 || !slots[*a.SlotPath] {
		t.Errorf("b and c do not run in a's slot and one more: %v", slots)
	}
	if agent, err := td.client.GetAgentWithResponse(ctx, "id:"+a.Agent.Id, nil); err != nil || agent.JSON200 == nil || agent.JSON200.Agent.State != api.AgentStateExited {
		t.Errorf("a's agent was not stopped: %v %s", err, agent.Body)
	}

	td.finishTask(t, repo, "auth", "b")
	td.finishTask(t, repo, "auth", "c")
	c = td.waitPlan(t, "auth", "d running", stateIs("a:done b:done c:done d:running"))
	if !slots[*taskIn(c, "d").SlotPath] {
		t.Errorf("d is in a third slot: %s", *taskIn(c, "d").SlotPath)
	}
	td.finishTask(t, repo, "auth", "d")
	c = td.waitPlan(t, "auth", "the plan done", func(c api.PlanContent) bool { return c.Plan.State == api.PlanStateDone })
	if states(c) != "a:done b:done c:done d:done" || *c.Plan.TasksDone != 4 || c.Run.FinishedAt == nil {
		t.Errorf("done: %s, %+v", states(c), c.Plan)
	}

	for _, task := range []string{"a", "b", "c", "d"} {
		if got := gitOut(t, repo, "show", "plan/auth:"+task+".txt"); got != task {
			t.Errorf("%s.txt on plan/auth: %q", task, got)
		}
	}
	if merges := gitOut(t, repo, "rev-list", "--merges", "plan/auth"); merges != "" {
		t.Errorf("merges on plan/auth: %s", merges)
	}
	if branches := gitOut(t, repo, "branch", "--list", "task/*"); branches != "" {
		t.Errorf("task branches left: %s", branches)
	}
	var onPlan []string
	for _, line := range strings.Split(gitOut(t, repo, "worktree", "list", "--porcelain"), "\n") {
		if line == "branch refs/heads/plan/auth" {
			onPlan = append(onPlan, line)
		}
	}
	if worktrees := strings.Count(gitOut(t, repo, "worktree", "list"), "\n") + 1; worktrees != 2 || len(onPlan) != 1 {
		t.Errorf("worktrees: %d, %d on plan/auth", worktrees, len(onPlan))
	}
}

// TestPlanDoneRemovesItsAgents: a plan done removes its tasks' agents, the tasks
// keeping their state and summary with no agent.
func TestPlanDoneRemovesItsAgents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	repo := td.planProject(t, "")
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n  - id: b\n    prompt: p\n")
	td.runPlan(t, "auth", 2)
	c := td.waitPlan(t, "auth", "both running", stateIs("a:running b:running"))
	ids := []string{taskIn(c, "a").Agent.Id, taskIn(c, "b").Agent.Id}
	td.finishTask(t, repo, "auth", "a")
	td.finishTask(t, repo, "auth", "b")
	c = td.waitPlan(t, "auth", "done, its agents removed", func(c api.PlanContent) bool {
		return c.Plan.State == api.PlanStateDone && taskIn(c, "a").Agent == nil && taskIn(c, "b").Agent == nil
	})
	for _, id := range ids {
		if _, err := td.daemon.agents.Store().Get(ctx, id); !errors.Is(err, agent.ErrNotFound) {
			t.Errorf("agent %s not removed: %v", id, err)
		}
	}
	for _, task := range []string{"a", "b"} {
		if got := taskIn(c, task); got.State != api.PlanTaskStateDone || got.Summary == nil || *got.Summary != "did "+task || got.Agent != nil {
			t.Errorf("%s: %+v", task, got)
		}
	}
}

// TestPlanAgentWithAChildStays: a task's agent with a child of its own stays as a
// tombstone once its plan is done.
func TestPlanAgentWithAChildStays(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	repo := td.planProject(t, "a exit\n")
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")
	td.runPlan(t, "auth", 0)
	c := td.waitPlan(t, "auth", "a running", stateIs("a:running"))
	a := taskIn(c, "a").Agent
	// The child runs the task's script in a's slot, and ends at once, as a has.
	touch(t, filepath.Join(repo, ".git", "end-a"))
	td.waitPlan(t, "auth", "a failed", stateIs("a:failed"))
	child, err := td.client.CreateAgentWithResponse(ctx, api.CreateAgent{Dir: repo, Parent: optional("id:" + a.Id)})
	if err != nil || child.JSON201 == nil {
		t.Fatalf("child: %v %s", err, child.Body)
	}
	td.waitAgentState(t, child.JSON201.Agent.Id, api.AgentStateExited)
	if r, err := td.client.FinishPlanTaskWithResponse(ctx, "auth", "a", &api.FinishPlanTaskParams{Project: optional("app")}, api.FinishPlanTask{Summary: "by hand"}); err != nil || r.JSON200 == nil {
		t.Fatalf("done: %v %s", err, r.Body)
	}
	td.waitPlan(t, "auth", "done, a's agent a tombstone", func(c api.PlanContent) bool {
		if c.Plan.State != api.PlanStateDone {
			return false
		}
		got, err := td.daemon.agents.Store().Get(ctx, a.Id)
		return err == nil && got.Tombstone()
	})
	if _, err := td.daemon.agents.Store().Get(ctx, child.JSON201.Agent.Id); err != nil {
		t.Errorf("the child: %v", err)
	}
}

// TestPlanTaskFailsWhenItsAgentEnds: an agent that ends without its task done fails
// it, and what waits for it does not start.
func TestPlanTaskFailsWhenItsAgentEnds(t *testing.T) {
	t.Parallel()
	td := start(t)
	repo := td.planProject(t, "a exit\n")
	td.createPlan(t, "app", "auth", diamondPlan)
	td.runPlan(t, "auth", 0)
	td.waitPlan(t, "auth", "a running", stateIs("a:running b:pending c:pending d:pending"))
	if err := os.WriteFile(filepath.Join(repo, ".git", "end-a"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	c := td.waitPlan(t, "auth", "a failed", stateIs("a:failed b:pending c:pending d:pending"))
	if f := taskIn(c, "a").Failure; f == nil || *f != api.PlanTaskFailureAgentEnded || c.Plan.State != api.PlanStateRunning {
		t.Errorf("a: %+v, plan %s", taskIn(c, "a"), c.Plan.State)
	}
}

// TestPlanLandingConflicts: two tasks changing the same file at once; the second to
// land stops on the conflict, and the plan's branch keeps the first.
func TestPlanLandingConflicts(t *testing.T) {
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
	b := taskIn(c, "b")
	if b.Failure == nil || *b.Failure != api.PlanTaskFailureRebase || b.Conflicts == nil || !slices.Equal(*b.Conflicts, []string{"shared.txt"}) || b.SlotPath == nil {
		t.Errorf("b: %+v", b)
	}
	if got := gitOut(t, repo, "show", "plan/auth:shared.txt"); got != "a" {
		t.Errorf("shared.txt on plan/auth: %q", got)
	}
	if busy := gitOut(t, *b.SlotPath, "status", "--porcelain"); busy != "" {
		t.Errorf("b's slot was left changed: %s", busy)
	}
}

// TestPlanGoesOnAfterARestart: tasks running when the daemon stops go on with the
// next one, which lands them and starts what they unblocked.
func TestPlanGoesOnAfterARestart(t *testing.T) {
	t.Parallel()
	td := start(t)
	repo := td.planProject(t, "")
	td.createPlan(t, "app", "auth", diamondPlan)
	td.runPlan(t, "auth", 0)
	td.waitPlan(t, "auth", "a running", stateIs("a:running b:pending c:pending d:pending"))
	waittest.For(t, "a's commit", func() bool {
		_, err := os.Stat(filepath.Join(repo, ".git", "committed-a"))
		return err == nil
	})
	td.stop()
	// As a daemon that stopped once a was done, before it landed.
	d, err := db.Open(context.Background(), identity.DBPath(td.home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE plan_tasks SET state = 'landing', summary = 'did a' WHERE task_id = 'a'`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	td = startOn(t, td.home, telemetry.Options{}, nil)
	td.waitPlan(t, "auth", "b and c running", stateIs("a:done b:running c:running d:pending"))
	td.finishTask(t, repo, "auth", "b")
	td.waitPlan(t, "auth", "b done", stateIs("a:done b:done c:running d:pending"))
}

// TestPlanRefusals: what running a plan, changing one that has run, and saying a
// task is done refuse.
func TestPlanRefusals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	repo := td.planProject(t, "")
	params := func() *api.RunPlanParams { return &api.RunPlanParams{Project: optional("app")} }

	td.createPlan(t, "app", "child", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")
	td.createPlan(t, "app", "parent", "goal: g\nplans:\n  - id: child\n")
	two := 2
	r, err := td.client.RunPlanWithResponse(ctx, "parent", params(), api.RunPlan{Parallel: &two})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusUnprocessableEntity, api.ErrorCodeInvalidArgument)
	r, err = td.client.RunPlanWithResponse(ctx, "child", params(), api.RunPlan{})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodePlanInUse)

	gittest.Git(t, repo, "branch", "plan/taken")
	td.createPlan(t, "app", "taken", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")
	r, err = td.client.RunPlanWithResponse(ctx, "taken", params(), api.RunPlan{})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeBranchExists)
	if got, err := td.client.GetPlanWithResponse(ctx, "taken", &api.GetPlanParams{Project: optional("app")}); err != nil || got.JSON200 == nil || got.JSON200.Run != nil || got.JSON200.Plan.State != api.PlanStateDraft {
		t.Errorf("a refused run left: %v %s", err, got.Body)
	}

	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n  - id: b\n    after: [a]\n    prompt: p\n  - id: c\n    after: [b]\n    prompt: p\n")
	done := func(task, summary string) *api.Problem {
		t.Helper()
		r, err := td.client.FinishPlanTaskWithResponse(ctx, "auth", task, &api.FinishPlanTaskParams{Project: optional("app")}, api.FinishPlanTask{Summary: summary})
		if err != nil {
			t.Fatal(err)
		}
		return r.ApplicationproblemJSONDefault
	}
	wantProblem(t, done("a", "s"), http.StatusConflict, api.ErrorCodePlanNotRunning)
	td.runPlan(t, "auth", 0)
	c := td.waitPlan(t, "auth", "a running", stateIs("a:running b:pending c:pending"))
	slot := *taskIn(c, "a").SlotPath
	r, err = td.client.RunPlanWithResponse(ctx, "auth", params(), api.RunPlan{})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodePlanStarted)
	u, err := td.client.UpdatePlanContentWithResponse(ctx, "auth", &api.UpdatePlanContentParams{Project: optional("app")}, api.DocContentChange{Base: 1, Content: diamondPlan})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, u.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodePlanStarted)
	wantProblem(t, done("b", "s"), http.StatusConflict, api.ErrorCodeTaskState)
	wantProblem(t, done("nowhere", "s"), http.StatusNotFound, api.ErrorCodeNotFound)
	wantProblem(t, done("a", " "), http.StatusBadRequest, api.ErrorCodeUsage)

	waittest.For(t, "a's commit", func() bool {
		_, err := os.Stat(filepath.Join(repo, ".git", "committed-a"))
		return err == nil
	})
	if err := os.WriteFile(filepath.Join(slot, "loose.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, done("a", "s"), http.StatusConflict, api.ErrorCodeNotCommitted)
	if err := os.Remove(filepath.Join(slot, "loose.txt")); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, slot, "switch", "-q", "--detach")
	wantProblem(t, done("a", "s"), http.StatusConflict, api.ErrorCodeWrongBranch)
	gittest.Git(t, slot, "switch", "-q", "task/auth/a")
	td.finishTask(t, repo, "auth", "a")
	td.waitPlan(t, "auth", "b running", stateIs("a:done b:running c:pending"))
	waittest.For(t, "b's commit", func() bool {
		_, err := os.Stat(filepath.Join(repo, ".git", "committed-b"))
		return err == nil
	})

	closed, err := td.client.UpdateProjectWithResponse(ctx, "app", api.UpdateProject{State: api.ProjectStateClosed, StopRunning: optional(true)})
	if err != nil || closed.JSON200 == nil {
		t.Fatalf("close: %v %s", err, closed.Body)
	}
	// b's agent, stopped with the project, is not failed: b is still running, so the
	// person can say it is done. Its landing, which needs the project open for the
	// check, and what it unblocks wait for the project to open.
	td.finishTask(t, repo, "auth", "b")
	td.waitPlan(t, "auth", "b landing, c waiting", stateIs("a:done b:landing c:pending"))
	td.createPlan(t, "app", "later", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")
	r, err = td.client.RunPlanWithResponse(ctx, "later", params(), api.RunPlan{})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeProjectClosed)

	// Opened again, the plan goes on.
	opened, err := td.client.UpdateProjectWithResponse(ctx, "app", api.UpdateProject{State: api.ProjectStateOpen})
	if err != nil || opened.JSON200 == nil {
		t.Fatalf("open: %v %s", err, opened.Body)
	}
	td.waitPlan(t, "auth", "c running", stateIs("a:done b:done c:running"))
}

// TestPlanSlotsAndBranchesInTheWay: a slot's name a worktree has is passed over, and
// a task's branch holding someone's commits fails its start.
func TestPlanSlotsAndBranchesInTheWay(t *testing.T) {
	t.Parallel()
	td := start(t)
	repo := td.planProject(t, "")
	if err := os.MkdirAll(filepath.Join(repo, ".worktrees", "auth-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, repo, "branch", "task/auth/b")
	gittest.Git(t, repo, "commit", "-q", "--allow-empty", "-m", "someone's")
	gittest.Git(t, repo, "branch", "-f", "task/auth/b")
	gittest.Git(t, repo, "reset", "-q", "--hard", "HEAD~1")
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n  - id: b\n    prompt: p\n")
	td.runPlan(t, "auth", 2)
	c := td.waitPlan(t, "auth", "a running, b failed", stateIs("a:running b:failed"))
	if path := taskIn(c, "a").SlotPath; path == nil || filepath.Base(*path) != "auth-2" {
		t.Errorf("a's slot: %v", path)
	}
	if b := taskIn(c, "b"); b.Failure == nil || *b.Failure != api.PlanTaskFailureStartFailed || b.SlotPath != nil {
		t.Errorf("b: %+v", b)
	}
	if got := gitOut(t, repo, "log", "-1", "--format=%s", "task/auth/b"); got != "someone's" {
		t.Errorf("task/auth/b moved: %s", got)
	}
}

// TestPlanSlotGone: a running task whose slot was deleted fails with slot_missing,
// found when the plan is next looked at, here as the daemon starts.
func TestPlanSlotGone(t *testing.T) {
	t.Parallel()
	td := start(t)
	td.planProject(t, "")
	td.createPlan(t, "app", "auth", "goal: g\ntasks:\n  - id: a\n    prompt: p\n  - id: b\n    after: [a]\n    prompt: p\n")
	td.runPlan(t, "auth", 0)
	c := td.waitPlan(t, "auth", "a running", stateIs("a:running b:pending"))
	td.stop()
	if err := os.RemoveAll(*taskIn(c, "a").SlotPath); err != nil {
		t.Fatal(err)
	}
	td = startOn(t, td.home, telemetry.Options{}, nil)
	c = td.waitPlan(t, "auth", "a failed", stateIs("a:failed b:pending"))
	if f := taskIn(c, "a").Failure; f == nil || *f != api.PlanTaskFailureSlotMissing {
		t.Errorf("a: %+v", taskIn(c, "a"))
	}
}
