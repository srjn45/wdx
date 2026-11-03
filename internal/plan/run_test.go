package plan

import (
	"context"
	"errors"
	"reflect"
	"testing"
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
	if _, err := f.store.Remove(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := f.db.QueryRow(`SELECT (SELECT count(*) FROM plan_runs) + (SELECT count(*) FROM plan_tasks) + (SELECT count(*) FROM plan_slots)`).Scan(&left); err != nil || left != 0 {
		t.Errorf("left after its plan: %d, %v", left, err)
	}
}
