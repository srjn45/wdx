package plan

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func (f fixture) agent(t *testing.T, id string) {
	t.Helper()
	if _, err := f.db.Exec(`INSERT INTO terminals (id, project_id, name, dir, shell, state, kind, created_at, updated_at)
		VALUES (?, ?, ?, '/r', 'sh', 'running', 'agent', 0, 0)`, "term-"+id, f.project, id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO agents (id, terminal_id, aca, session_id) VALUES (?, ?, 'claude', 's')`, id, "term-"+id); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) run(t *testing.T, id string) Run {
	t.Helper()
	r, ok, err := f.store.Run(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("run of %s: %v, %v", id, ok, err)
	}
	return r
}

func taskState(t *testing.T, err error) *TaskStateError {
	t.Helper()
	e, ok := errors.AsType[*TaskStateError](err)
	if !ok {
		t.Fatalf("%v, want a task in another state", err)
	}
	return e
}

func TestStart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.create(t, "auth", "goal: g\ntasks:\n  - id: b\n    after: [a]\n    prompt: p\n  - id: a\n    prompt: p\n")
	if _, ok, err := f.store.Run(ctx, p.ID); ok || err != nil {
		t.Errorf("a draft's run: %v, %v", ok, err)
	}
	r, err := f.store.Start(ctx, p.ID, "main", "plan/auth", 2)
	if err != nil {
		t.Fatal(err)
	}
	if r.PlanID != p.ID || r.ProjectID != f.project || r.Revision != 1 || r.Base != "main" || r.Branch != "plan/auth" || r.State != StateRunning || r.Parallel != 2 || r.StartedAt.IsZero() || !r.FinishedAt.IsZero() {
		t.Errorf("run %+v", r)
	}
	if len(r.Tasks) != 2 || r.Tasks[0].ID != "a" || r.Tasks[1].ID != "b" || r.Tasks[0].State != TaskPending || r.Slots != nil {
		t.Errorf("tasks not in DAG order, or not pending: %+v", r.Tasks)
	}
	if got := f.get(t, "auth"); got.State != StateRunning || got.TasksDone != 0 {
		t.Errorf("the plan: %+v", got)
	}
	var started *StartedError
	if _, err := f.store.Start(ctx, p.ID, "main", "plan/auth", 0); !errors.As(err, &started) || started.Plan.State != StateRunning {
		t.Errorf("run again: %v", err)
	}

	other := f.create(t, "other", tasks("x"))
	r, err = f.store.Start(ctx, other.ID, "main", "plan/other", 0)
	if err != nil || r.Parallel != 0 {
		t.Errorf("the setting's parallel: %+v, %v", r, err)
	}
	if ids, err := f.store.Unfinished(ctx); err != nil || !reflect.DeepEqual(ids, []string{p.ID, other.ID}) {
		t.Errorf("unfinished: %v, %v", ids, err)
	}
}

func TestStartRefusesAParent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	f.create(t, "child", tasks("x"))
	parent := f.create(t, "big", "goal: g\nplans:\n  - id: child\n")
	if _, err := f.store.Start(ctx, parent.ID, "main", "plan/big", 0); err == nil {
		t.Error("a plan of plans ran")
	}
	if _, ok, err := f.store.Run(ctx, parent.ID); ok || err != nil {
		t.Errorf("a run was left: %v, %v", ok, err)
	}
}

func TestATaskThroughItsRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.create(t, "auth", tasks("a", "b"))
	if _, err := f.store.Start(ctx, p.ID, "main", "plan/auth", 0); err != nil {
		t.Fatal(err)
	}
	f.agent(t, "agent-a")

	if err := f.store.HoldSlot(ctx, p.ID, "a", 1); err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddSlot(ctx, p.ID, 1, "/r/.worktrees/auth-1"); err != nil {
		t.Fatal(err)
	}
	r := f.run(t, p.ID)
	if a, _ := r.Task("a"); a.Slot != 1 || a.State != TaskPending || len(r.Slots) != 1 || r.Slots[0].Path != "/r/.worktrees/auth-1" {
		t.Errorf("held: %+v, %+v", a, r.Slots)
	}
	if _, err := f.store.TaskDone(ctx, p.ID, "a", "s"); taskState(t, err).Task.State != TaskPending {
		t.Errorf("done before running: %v", err)
	}

	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.StartTask(ctx, tx, p.ID, "a", "agent-a"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if a, _ := f.run(t, p.ID).Task("a"); a.State != TaskPending {
		t.Errorf("started outside its transaction: %+v", a)
	}
	if err := f.store.StartTask(ctx, f.db, p.ID, "a", "agent-a"); err != nil {
		t.Fatal(err)
	}
	if a, _ := f.run(t, p.ID).Task("a"); a.State != TaskRunning || a.AgentID != "agent-a" || a.StartedAt.IsZero() {
		t.Errorf("running: %+v", a)
	}
	if err := f.store.HoldSlot(ctx, p.ID, "a", 2); taskState(t, err).Task.State != TaskRunning {
		t.Errorf("a slot held while running: %v", err)
	}

	got, err := f.store.TaskDone(ctx, p.ID, "a", "did a")
	if err != nil || got.State != TaskLanding || got.Summary != "did a" {
		t.Errorf("done: %+v, %v", got, err)
	}
	if err := f.store.Landed(ctx, p.ID, "a"); err != nil {
		t.Fatal(err)
	}
	r = f.run(t, p.ID)
	if a, _ := r.Task("a"); a.State != TaskDone || a.Slot != 0 || a.FinishedAt.IsZero() || a.Summary != "did a" {
		t.Errorf("landed: %+v", a)
	}
	if got := f.get(t, "auth"); got.TasksDone != 1 {
		t.Errorf("tasks done: %+v", got)
	}
	if list, err := f.store.List(ctx, f.project); err != nil || list[0].State != StateRunning || list[0].TasksDone != 1 {
		t.Errorf("listed %+v, %v", list, err)
	}
	if err := f.store.Finish(ctx, p.ID); err == nil {
		t.Error("finished with b pending")
	}

	if err := f.store.FailTask(ctx, p.ID, "b", TaskPending, FailureStartFailed); err != nil {
		t.Fatal(err)
	}
	if b, _ := f.run(t, p.ID).Task("b"); b.State != TaskFailed || b.Failure != FailureStartFailed {
		t.Errorf("failed: %+v", b)
	}
	if err := f.store.Landed(ctx, p.ID, "b"); !reflect.DeepEqual(taskState(t, err).From, []string{TaskLanding}) {
		t.Errorf("landed when failed: %v", err)
	}
	if _, err := f.store.TaskDone(ctx, p.ID, "nowhere", "s"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a task it does not have: %v", err)
	}
}

func TestLandFailedAndFinish(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.create(t, "auth", tasks("a", "b"))
	if _, err := f.store.Start(ctx, p.ID, "main", "plan/auth", 0); err != nil {
		t.Fatal(err)
	}
	f.agent(t, "agent-a")
	f.agent(t, "agent-b")
	for _, id := range []string{"a", "b"} {
		if err := f.store.StartTask(ctx, f.db, p.ID, id, "agent-"+id); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.TaskDone(ctx, p.ID, id, "s"); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.store.LandFailed(ctx, p.ID, "a", FailureRebase, []string{"x.go", "y.go"}); err != nil {
		t.Fatal(err)
	}
	if a, _ := f.run(t, p.ID).Task("a"); a.State != TaskLandFailed || a.Failure != FailureRebase || !reflect.DeepEqual(a.Conflicts, []string{"x.go", "y.go"}) {
		t.Errorf("land failed: %+v", a)
	}
	if err := f.store.LandFailed(ctx, p.ID, "b", FailureCheck, nil); err != nil {
		t.Fatal(err)
	}
	var stored *string
	if err := f.db.QueryRow(`SELECT conflicts FROM plan_tasks WHERE task_id = 'b'`).Scan(&stored); err != nil || stored != nil {
		t.Errorf("conflicts of a check: %v, %v", stored, err)
	}

	q := f.create(t, "done", tasks("a"))
	if _, err := f.store.Start(ctx, q.ID, "main", "plan/done", 0); err != nil {
		t.Fatal(err)
	}
	if err := f.store.HoldSlot(ctx, q.ID, "a", 1); err != nil {
		t.Fatal(err)
	}
	if err := f.store.StartTask(ctx, f.db, q.ID, "a", "agent-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.TaskDone(ctx, q.ID, "a", "s"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Landed(ctx, q.ID, "a"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Finish(ctx, q.ID); err != nil {
		t.Fatal(err)
	}
	r := f.run(t, q.ID)
	if r.State != StateDone || r.FinishedAt.IsZero() {
		t.Errorf("finished: %+v", r)
	}
	if err := f.store.Finish(ctx, q.ID); err == nil {
		t.Error("finished twice")
	}
	if ids, err := f.store.Unfinished(ctx); err != nil || !reflect.DeepEqual(ids, []string{p.ID}) {
		t.Errorf("unfinished: %v, %v", ids, err)
	}
	if got := f.get(t, "done"); got.State != StateDone || got.TasksDone != 1 {
		t.Errorf("the plan: %+v", got)
	}
}

func TestSlotsAndTheirTasks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.create(t, "auth", tasks("a"))
	if _, err := f.store.Start(ctx, p.ID, "main", "plan/auth", 0); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{2, 1} {
		if err := f.store.AddSlot(ctx, p.ID, n, "/s"); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.store.AddSlot(ctx, p.ID, 1, "/again"); err == nil {
		t.Error("a slot added twice")
	}
	if r := f.run(t, p.ID); len(r.Slots) != 2 || r.Slots[0].Number != 1 || r.Slots[1].Number != 2 || r.Slots[0].CreatedAt.IsZero() {
		t.Errorf("slots: %+v", r.Slots)
	}
	if err := f.store.HoldSlot(ctx, p.ID, "a", 1); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DropSlot(ctx, p.ID, 1); err != nil {
		t.Fatal(err)
	}
	r := f.run(t, p.ID)
	if a, _ := r.Task("a"); len(r.Slots) != 1 || r.Slots[0].Number != 2 || a.Slot != 1 {
		t.Errorf("dropped: %+v, %+v", r.Slots, a)
	}

	f.agent(t, "agent-a")
	if err := f.store.StartTask(ctx, f.db, p.ID, "a", "agent-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`DELETE FROM agents WHERE id = 'agent-a'`); err != nil {
		t.Fatal(err)
	}
	if a, _ := f.run(t, p.ID).Task("a"); a.AgentID != "" || a.State != TaskRunning {
		t.Errorf("its agent removed: %+v", a)
	}
	// A plan that has run goes only with its project.
	if _, err := f.db.Exec(`DELETE FROM projects WHERE id = ?`, f.project); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := f.db.QueryRow(`SELECT (SELECT count(*) FROM plan_runs) + (SELECT count(*) FROM plan_tasks) + (SELECT count(*) FROM plan_slots)`).Scan(&left); err != nil || left != 0 {
		t.Errorf("left after its plan: %d, %v", left, err)
	}
}

// Once a plan has run, only its run changes it, and no plan of plans takes it.
func TestAPlanThatHasRunIsKept(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.create(t, "auth", tasks("a"))
	if _, err := f.store.Start(ctx, p.ID, "main", "plan/auth", 0); err != nil {
		t.Fatal(err)
	}
	refused := func(what string, err error) {
		t.Helper()
		if e, ok := errors.AsType[*StartedError](err); !ok || e.Plan.ID != p.ID {
			t.Errorf("%s: %v", what, err)
		}
	}
	_, err := f.store.Update(ctx, p.ID, 1, []byte(tasks("a", "b")))
	refused("update", err)
	_, err = f.store.Rename(ctx, p.ID, "other")
	refused("rename", err)
	_, err = f.store.Remove(ctx, p.ID)
	refused("remove", err)
	_, err = f.store.Create(ctx, f.project, "big", []byte("goal: g\nplans:\n  - id: auth\n"))
	refused("listed by a new parent", err)
	if got := f.get(t, "auth"); got.Revision != 1 || got.Parent != "" {
		t.Errorf("changed: %+v", got)
	}
}

func TestTaskOfAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.create(t, "auth", tasks("a", "b"))
	if _, err := f.store.Start(ctx, p.ID, "main", "plan/auth", 0); err != nil {
		t.Fatal(err)
	}
	f.agent(t, "agent-b")
	if err := f.store.StartTask(ctx, f.db, p.ID, "b", "agent-b"); err != nil {
		t.Fatal(err)
	}
	if plan, task, ok, err := f.store.TaskOfAgent(ctx, "agent-b"); err != nil || !ok || plan != p.ID || task != "b" {
		t.Errorf("agent-b: %s %s %v %v", plan, task, ok, err)
	}
	if _, _, ok, err := f.store.TaskOfAgent(ctx, "agent-none"); err != nil || ok {
		t.Errorf("an agent of no task: %v, %v", ok, err)
	}
}

// Every change to a run moves its plan's updated_at on, so that the projects'
// streams send the plan again.
func TestARunChangeMovesThePlanOn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.create(t, "auth", tasks("a"))
	last := p.UpdatedAt
	moved := func(what string) {
		t.Helper()
		got := f.get(t, "auth").UpdatedAt
		if !got.After(last) {
			t.Errorf("%s: updated_at %v, was %v", what, got, last)
		}
		last = got
	}
	if _, err := f.store.Start(ctx, p.ID, "main", "plan/auth", 0); err != nil {
		t.Fatal(err)
	}
	moved("started")
	if err := f.store.HoldSlot(ctx, p.ID, "a", 1); err != nil {
		t.Fatal(err)
	}
	moved("slot held")
	if err := f.store.AddSlot(ctx, p.ID, 1, "/s"); err != nil {
		t.Fatal(err)
	}
	moved("slot added")
	f.agent(t, "agent-a")
	if err := f.store.StartTask(ctx, f.db, p.ID, "a", "agent-a"); err != nil {
		t.Fatal(err)
	}
	moved("task running")
	if _, err := f.store.TaskDone(ctx, p.ID, "a", "s"); err != nil {
		t.Fatal(err)
	}
	moved("task landing")
	if err := f.store.Landed(ctx, p.ID, "a"); err != nil {
		t.Fatal(err)
	}
	moved("task done")
	if err := f.store.DropSlot(ctx, p.ID, 1); err != nil {
		t.Fatal(err)
	}
	moved("slot dropped")
	if err := f.store.Finish(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	moved("finished")
	if err := f.store.Landed(ctx, p.ID, "a"); err == nil {
		t.Fatal("landed twice")
	}
	if got := f.get(t, "auth").UpdatedAt; !got.Equal(last) {
		t.Errorf("a change refused moved it: %v, was %v", got, last)
	}
}

func TestFirstMessage(t *testing.T) {
	t.Parallel()
	def, problems := Parse([]byte("goal: Users log in.\nconstraints:\n  - Run make check.\n  - No new deps.\ntasks:\n  - id: a\n    prompt: Add it.\n  - id: b\n    prompt: Show it.\n  - id: c\n    after: [a, b]\n    prompt: |-\n      Document it.\n      Both of it.\n"))
	if problems != nil {
		t.Fatal(problems)
	}
	run := Run{Definition: def, Tasks: []TaskRun{{ID: "a", Summary: "added the table"}, {ID: "b", Summary: "showed it"}, {ID: "c"}}}
	want := "You are doing task c of the plan auth, run by wdx.\n" +
		"Goal: Users log in.\n" +
		"Constraints:\n- Run make check.\n- No new deps.\n" +
		"Done before you, which this task waits for:\n- a: added the table\n- b: showed it\n" +
		"Your task:\nDocument it.\nBoth of it.\n" +
		"When it is done, commit your work on this branch, then run:\n    wdx plan task done --agent-id $WDX_AGENT_ID --summary \"<what you did, in a few lines>\"\n"
	if got := firstMessage(run, "auth", "c"); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if got := firstMessage(Run{Definition: Definition{Goal: "g", Tasks: []Task{{ID: "a", Prompt: "p"}}}}, "x", "a"); strings.Contains(got, "Constraints") || strings.Contains(got, "Done before you") {
		t.Errorf("a first task with no constraints: %s", got)
	}
}

func TestRetryNote(t *testing.T) {
	t.Parallel()
	run := Run{Branch: "plan/auth"}
	started := time.UnixMilli(1)
	for _, tc := range []struct {
		task TaskRun
		want string
	}{
		{TaskRun{ID: "a"}, ""},
		{TaskRun{ID: "a", StartedAt: started, Failure: FailureAgentEnded}, "You ended before saying task a of the plan auth was done. Go on with it where you left it.\n"},
		{TaskRun{ID: "a", StartedAt: started, Failure: FailureStartFailed}, "You ended before saying task a of the plan auth was done. Go on with it where you left it.\n"},
		{TaskRun{ID: "a", StartedAt: started, Failure: FailureRebase, Conflicts: []string{"x.go", "y.go"}},
			"Landing task a of the plan auth on plan/auth stopped on conflicts in x.go, y.go, and was undone. Rebase task/auth/a onto plan/auth, resolve them, and go on.\n"},
		{TaskRun{ID: "a", StartedAt: started, Failure: FailureCheck},
			"After rebasing task a of the plan auth onto plan/auth, the check `make check` failed; its output is in the terminal land-auth-1. Run it, fix what fails, and go on.\n"},
	} {
		if got := retryNote(run, tc.task, "make check", "/r/.worktrees/auth-1"); got != tc.want {
			t.Errorf("%s %s: got %q, want %q", tc.task.ID, tc.task.Failure, got, tc.want)
		}
	}
	if got := previousAttempt(run, "task/auth/a", "why\n"); got != "A previous attempt at this task left its commits on this branch, task/auth/a: read them with `git log plan/auth..HEAD` before going on.\nwhy\n" {
		t.Errorf("previous attempt: %q", got)
	}
}

func notRunning(t *testing.T, err error, state string) {
	t.Helper()
	e, ok := errors.AsType[*NotRunningError](err)
	if !ok || e.Plan.State != state {
		t.Errorf("%v, want a plan %s, not running", err, state)
	}
}

func TestTheAnswersToATask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.create(t, "auth", tasks("a", "b", "c"))
	_, err := f.store.RetryTask(ctx, p.ID, "a", false)
	notRunning(t, err, StateDraft)
	if _, err := f.store.Start(ctx, p.ID, "main", "plan/auth", 0); err != nil {
		t.Fatal(err)
	}
	f.agent(t, "agent-a")
	task := func(id string) TaskRun {
		t.Helper()
		tr, _ := f.run(t, p.ID).Task(id)
		return tr
	}
	if err := f.store.HoldSlot(ctx, p.ID, "a", 1); err != nil {
		t.Fatal(err)
	}
	if err := f.store.StartTask(ctx, f.db, p.ID, "a", "agent-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE plan_tasks SET started_at = 5 WHERE task_id = 'a'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.RetryTask(ctx, p.ID, "a", false); taskState(t, err).Task.State != TaskRunning {
		t.Errorf("retried while running: %v", err)
	}

	if err := f.store.FailTask(ctx, p.ID, "a", TaskRunning, FailureAgentEnded); err != nil {
		t.Fatal(err)
	}
	if a, err := f.store.RetryTask(ctx, p.ID, "a", false); err != nil || a.State != TaskPending || a.AgentID != "agent-a" || a.Slot != 1 || a.Failure != FailureAgentEnded {
		t.Errorf("retried, to resume its agent: %+v, %v", a, err)
	}
	if err := f.store.StartTask(ctx, f.db, p.ID, "a", "agent-a"); err != nil {
		t.Fatal(err)
	}
	if a := task("a"); a.State != TaskRunning || a.Failure != "" || a.StartedAt.UnixMilli() != 5 {
		t.Errorf("running again: %+v", a)
	}

	if err := f.store.FailTask(ctx, p.ID, "a", TaskRunning, FailureAgentEnded); err != nil {
		t.Fatal(err)
	}
	if a, err := f.store.RetryTask(ctx, p.ID, "a", true); err != nil || a.AgentID != "" || a.Slot != 1 {
		t.Errorf("retried fresh: %+v, %v", a, err)
	}
	if err := f.store.StartTask(ctx, f.db, p.ID, "a", "agent-a"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.FailTask(ctx, p.ID, "a", TaskRunning, FailureSlotMissing); err != nil {
		t.Fatal(err)
	}
	if a, err := f.store.RetryTask(ctx, p.ID, "a", false); err != nil || a.AgentID != "" || a.Slot != 0 {
		t.Errorf("retried with its slot missing: %+v, %v", a, err)
	}

	if _, err := f.store.LandTask(ctx, p.ID, "a"); taskState(t, err).Task.State != TaskPending {
		t.Errorf("landed while pending: %v", err)
	}
	if err := f.store.StartTask(ctx, f.db, p.ID, "a", "agent-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.TaskDone(ctx, p.ID, "a", "s"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.LandFailed(ctx, p.ID, "a", FailureRebase, []string{"x.go"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.RetryTask(ctx, p.ID, "a", false); err != nil {
		t.Fatal(err)
	}
	if err := f.store.StartTask(ctx, f.db, p.ID, "a", "agent-a"); err != nil {
		t.Fatal(err)
	}
	if a := task("a"); a.Failure != "" || a.Conflicts != nil {
		t.Errorf("running again after its conflicts: %+v", a)
	}
	if _, err := f.store.TaskDone(ctx, p.ID, "a", "s"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.LandFailed(ctx, p.ID, "a", FailureRebase, []string{"x.go"}); err != nil {
		t.Fatal(err)
	}
	if a, err := f.store.LandTask(ctx, p.ID, "a"); err != nil || a.State != TaskLanding {
		t.Errorf("landing again: %+v, %v", a, err)
	}
	if err := f.store.Landed(ctx, p.ID, "a"); err != nil {
		t.Fatal(err)
	}
	if a := task("a"); a.State != TaskDone || a.Failure != "" || a.Conflicts != nil {
		t.Errorf("landed: %+v", a)
	}

	if err := f.store.StartTask(ctx, f.db, p.ID, "b", "agent-a"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.FailTask(ctx, p.ID, "b", TaskRunning, FailureAgentEnded); err != nil {
		t.Fatal(err)
	}
	if b, err := f.store.TaskDone(ctx, p.ID, "b", "by hand"); err != nil || b.State != TaskLanding || b.Summary != "by hand" {
		t.Errorf("done by the person: %+v, %v", b, err)
	}

	if err := f.store.HoldSlot(ctx, p.ID, "c", 2); err != nil {
		t.Fatal(err)
	}
	if c, err := f.store.SkipTask(ctx, p.ID, "c"); err != nil || c.State != TaskSkipped || c.FinishedAt.IsZero() || c.Slot != 2 {
		t.Errorf("skipped: %+v, %v", c, err)
	}
	if _, err := f.store.SkipTask(ctx, p.ID, "c"); taskState(t, err).Task.State != TaskSkipped {
		t.Errorf("skipped twice: %v", err)
	}
	if err := f.store.LeaveSlot(ctx, p.ID, "c"); err != nil {
		t.Fatal(err)
	}
	if c := task("c"); c.Slot != 0 {
		t.Errorf("its slot left: %+v", c)
	}
}

func TestTheAnswersToARun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.create(t, "auth", tasks("a"))
	_, err := f.store.Pause(ctx, p.ID)
	notRunning(t, err, StateDraft)
	if _, err := f.store.Start(ctx, p.ID, "main", "plan/auth", 0); err != nil {
		t.Fatal(err)
	}
	f.agent(t, "agent-a")
	for _, step := range []struct {
		name   string
		change func(context.Context, string) (bool, error)
		want   bool
		state  string
	}{
		{"paused", f.store.Pause, true, StatePaused},
		{"paused again", f.store.Pause, false, StatePaused},
		{"resumed", f.store.Resume, true, StateRunning},
		{"resumed again", f.store.Resume, false, StateRunning},
	} {
		if changed, err := step.change(ctx, p.ID); err != nil || changed != step.want || f.run(t, p.ID).State != step.state {
			t.Errorf("%s: %v, %v, %s", step.name, changed, err, f.run(t, p.ID).State)
		}
	}
	if changed, err := f.store.SetParallel(ctx, p.ID, 4); err != nil || !changed || f.run(t, p.ID).Parallel != 4 {
		t.Errorf("parallel set: %v, %v", changed, err)
	}
	if changed, err := f.store.SetParallel(ctx, p.ID, 4); err != nil || changed {
		t.Errorf("parallel set again: %v, %v", changed, err)
	}
	if err := f.store.StartTask(ctx, f.db, p.ID, "a", "agent-a"); err != nil {
		t.Fatal(err)
	}

	if err := f.store.Cancel(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if r := f.run(t, p.ID); r.State != StateCancelled || !r.FinishedAt.IsZero() {
		t.Errorf("cancelled: %+v", r)
	}
	if ids, err := f.store.Unfinished(ctx); err != nil || !reflect.DeepEqual(ids, []string{p.ID}) {
		t.Errorf("unfinished while cancelling: %v, %v", ids, err)
	}
	notRunning(t, f.store.Cancel(ctx, p.ID), StateCancelled)
	_, err = f.store.Resume(ctx, p.ID)
	notRunning(t, err, StateCancelled)
	_, err = f.store.SetParallel(ctx, p.ID, 2)
	notRunning(t, err, StateCancelled)
	_, err = f.store.TaskDone(ctx, p.ID, "a", "s")
	notRunning(t, err, StateCancelled)
	_, err = f.store.SkipTask(ctx, p.ID, "a")
	notRunning(t, err, StateCancelled)
	_, err = f.store.LandTask(ctx, p.ID, "a")
	notRunning(t, err, StateCancelled)
	if err := f.store.Close(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(ctx, p.ID); err == nil {
		t.Error("closed twice")
	}
	if r := f.run(t, p.ID); r.FinishedAt.IsZero() {
		t.Errorf("closed: %+v", r)
	}
	if ids, err := f.store.Unfinished(ctx); err != nil || len(ids) != 0 {
		t.Errorf("unfinished once closed: %v, %v", ids, err)
	}

	q := f.create(t, "paused", tasks("a"))
	if _, err := f.store.Start(ctx, q.ID, "main", "plan/paused", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pause(ctx, q.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Finish(ctx, q.ID); err == nil {
		t.Error("finished with a task pending")
	}
	if _, err := f.store.SkipTask(ctx, q.ID, "a"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Finish(ctx, q.ID); err != nil {
		t.Errorf("a paused run finished: %v", err)
	}
	if r := f.run(t, q.ID); r.State != StateDone {
		t.Errorf("finished: %+v", r)
	}
}
