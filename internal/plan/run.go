package plan

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/logging"
)

// The states of a run, which are its plan's; a plan with no run is StateDraft.
const (
	StateRunning   = "running"
	StatePaused    = "paused"
	StateDone      = "done"
	StateCancelled = "cancelled"
)

// The states of a task in a run.
const (
	TaskPending    = "pending"
	TaskRunning    = "running"
	TaskLanding    = "landing"
	TaskLandFailed = "land_failed"
	TaskFailed     = "failed"
	TaskDone       = "done"
	TaskSkipped    = "skipped"
)

// Why a task is failed or land_failed.
const (
	FailureAgentEnded  = "agent_ended"
	FailureStartFailed = "start_failed"
	FailureSlotMissing = "slot_missing"
	FailureRebase      = "rebase"
	FailureCheck       = "check"
)

// Run is a plan's run: the revision it runs, its branch, its tasks and its slots.
type Run struct {
	PlanID     string
	ProjectID  string
	Revision   int
	Base       string // the branch it started from
	Branch     string // plan/<name>, where its tasks land
	State      string
	Parallel   int // the most tasks at once; 0 follows the setting
	StartedAt  time.Time
	FinishedAt time.Time  // zero until it is done or cancelled
	Definition Definition // of Revision
	Tasks      []TaskRun  // in the definition's DAG order
	Slots      []Slot     // by number
}

// Task returns the task of r with id.
func (r Run) Task(id string) (TaskRun, bool) {
	i := slices.IndexFunc(r.Tasks, func(t TaskRun) bool { return t.ID == id })
	if i < 0 {
		return TaskRun{}, false
	}
	return r.Tasks[i], true
}

// TaskRun is a task's part in a run.
type TaskRun struct {
	ID         string
	State      string
	Failure    string   // why it is failed or land_failed
	Conflicts  []string // the files a rebase stopped on
	AgentID    string
	Slot       int // the slot it holds; 0 for none
	Summary    string
	StartedAt  time.Time
	FinishedAt time.Time
}

// Slot is a worktree a run lends its tasks, one at a time.
type Slot struct {
	Number    int
	Path      string
	CreatedAt time.Time
}

// StartedError is returned for what only a draft takes, such as a run, from a plan
// that has run.
type StartedError struct {
	Plan Plan
}

func (e *StartedError) Error() string {
	return fmt.Sprintf("plan %s has run and is %s: only a draft can be changed or run", e.Plan.Name, e.Plan.State)
}

// TaskStateError is returned for a change of a task that is not in a state the
// change is from.
type TaskStateError struct {
	PlanID string
	Task   TaskRun
	From   []string
}

func (e *TaskStateError) Error() string {
	return fmt.Sprintf("task %s is %s, not %s", e.Task.ID, e.Task.State, strings.Join(e.From, " or "))
}

// Start runs the plan with id: it writes its run, of the latest revision, with a
// pending row for each of its tasks. A plan that has run returns a *StartedError.
// Making the branch and starting the tasks are the caller's.
func (s *Store) Start(ctx context.Context, id, base, branch string, parallel int) (Run, error) {
	p, err := s.Get(ctx, id)
	if err != nil {
		return Run{}, err
	}
	if p.State != StateDraft {
		return Run{}, &StartedError{Plan: p}
	}
	_, r, def, err := s.Content(ctx, id, 0)
	if err != nil {
		return Run{}, err
	}
	if def.Plans != nil {
		return Run{}, fmt.Errorf("plan %s lists plans, which do not run yet", p.Name)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	if _, err := db.Exec(ctx, tx, `INSERT INTO plan_runs (plan_id, revision, base, branch, state, parallel, started_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, r.Number, base, branch, StateRunning, nullInt(parallel), now); err != nil {
		return Run{}, err
	}
	for _, t := range def.Tasks {
		if _, err := db.Exec(ctx, tx, `INSERT INTO plan_tasks (plan_id, task_id, state) VALUES (?, ?, ?)`, id, t.ID, TaskPending); err != nil {
			return Run{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Run{}, err
	}
	logging.FromContext(ctx).Info("plan started", "plan_id", id, "name", p.Name, "revision", r.Number, "base", base, "branch", branch, "parallel", parallel, "tasks", len(def.Tasks))
	run, _, err := s.Run(ctx, id)
	return run, err
}

// Run returns the run of the plan with id, and false for a draft.
func (s *Store) Run(ctx context.Context, id string) (Run, bool, error) {
	var r Run
	var parallel, finished sql.NullInt64
	var started int64
	err := s.db.QueryRowContext(ctx, `
		SELECT r.plan_id, d.project_id, r.revision, r.base, r.branch, r.state, r.parallel, r.started_at, r.finished_at
		FROM plan_runs r JOIN docs d ON d.id = r.plan_id WHERE r.plan_id = ?`, id).
		Scan(&r.PlanID, &r.ProjectID, &r.Revision, &r.Base, &r.Branch, &r.State, &parallel, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, err
	}
	r.Parallel, r.StartedAt, r.FinishedAt = int(parallel.Int64), time.UnixMilli(started), timeOf(finished)
	if _, _, r.Definition, err = s.Content(ctx, id, r.Revision); err != nil {
		return Run{}, false, err
	}
	tasks, err := s.taskRuns(ctx, id)
	if err != nil {
		return Run{}, false, err
	}
	for _, t := range r.Definition.Tasks {
		if tr, ok := tasks[t.ID]; ok {
			r.Tasks = append(r.Tasks, tr)
		}
	}
	if r.Slots, err = s.slots(ctx, id); err != nil {
		return Run{}, false, err
	}
	return r, true, nil
}

func (s *Store) taskRuns(ctx context.Context, planID string) (map[string]TaskRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_id, state, failure, conflicts, agent_id, slot, summary, started_at, finished_at
		FROM plan_tasks WHERE plan_id = ?`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := map[string]TaskRun{}
	for rows.Next() {
		var t TaskRun
		var failure, conflicts, agent, summary sql.NullString
		var slot, started, finished sql.NullInt64
		if err := rows.Scan(&t.ID, &t.State, &failure, &conflicts, &agent, &slot, &summary, &started, &finished); err != nil {
			return nil, err
		}
		t.Failure, t.AgentID, t.Summary = failure.String, agent.String, summary.String
		t.Slot, t.StartedAt, t.FinishedAt = int(slot.Int64), timeOf(started), timeOf(finished)
		if conflicts.Valid {
			if err := json.Unmarshal([]byte(conflicts.String), &t.Conflicts); err != nil {
				return nil, err
			}
		}
		tasks[t.ID] = t
	}
	return tasks, rows.Err()
}

func (s *Store) slots(ctx context.Context, planID string) ([]Slot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT slot, path, created_at FROM plan_slots WHERE plan_id = ? ORDER BY slot`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Slot
	for rows.Next() {
		var sl Slot
		var created int64
		if err := rows.Scan(&sl.Number, &sl.Path, &created); err != nil {
			return nil, err
		}
		sl.CreatedAt = time.UnixMilli(created)
		list = append(list, sl)
	}
	return list, rows.Err()
}

// Unfinished returns the ids of the plans whose runs are running or paused, which
// the daemon looks at again when it starts.
func (s *Store) Unfinished(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT plan_id FROM plan_runs WHERE state IN (?, ?) ORDER BY started_at`, StateRunning, StatePaused)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// HoldSlot gives the pending task the slot numbered slot, before the slot is made
// or switched to it, so that a start cut short is found holding it.
func (s *Store) HoldSlot(ctx context.Context, planID, taskID string, slot int) error {
	if err := s.move(ctx, s.db, planID, taskID, []string{TaskPending}, "slot = ?", slot); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("plan slot held", "plan_id", planID, "task_id", taskID, "slot", slot)
	return nil
}

// AddSlot records the slot numbered slot, made at path.
func (s *Store) AddSlot(ctx context.Context, planID string, slot int, path string) error {
	if _, err := db.Exec(ctx, s.db, `INSERT INTO plan_slots (plan_id, slot, path, created_at) VALUES (?, ?, ?, ?)`, planID, slot, path, time.Now().UnixMilli()); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("plan slot added", "plan_id", planID, "slot", slot, "path", path)
	return nil
}

// DropSlot forgets the slot numbered slot: it is gone, or no longer the plan's. A
// task still holding its number keeps it.
func (s *Store) DropSlot(ctx context.Context, planID string, slot int) error {
	if _, err := db.Exec(ctx, s.db, `DELETE FROM plan_slots WHERE plan_id = ? AND slot = ?`, planID, slot); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("plan slot dropped", "plan_id", planID, "slot", slot)
	return nil
}

// StartTask makes the pending task running with its agent, in e, the transaction
// that adds the agent.
func (s *Store) StartTask(ctx context.Context, e db.Execer, planID, taskID, agentID string) error {
	if err := s.move(ctx, e, planID, taskID, []string{TaskPending}, "state = ?, agent_id = ?, started_at = ?", TaskRunning, agentID, time.Now().UnixMilli()); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("plan task running", "plan_id", planID, "task_id", taskID, "agent_id", agentID)
	return nil
}

// FailTask makes the task, in the state from, failed for failure.
func (s *Store) FailTask(ctx context.Context, planID, taskID, from, failure string) error {
	if err := s.move(ctx, s.db, planID, taskID, []string{from}, "state = ?, failure = ?", TaskFailed, failure); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("plan task failed", "plan_id", planID, "task_id", taskID, "from", from, "failure", failure)
	return nil
}

// TaskDone makes the running task landing, with its agent's summary of it.
func (s *Store) TaskDone(ctx context.Context, planID, taskID, summary string) (TaskRun, error) {
	if err := s.move(ctx, s.db, planID, taskID, []string{TaskRunning}, "state = ?, summary = ?", TaskLanding, summary); err != nil {
		return TaskRun{}, err
	}
	logging.FromContext(ctx).Info("plan task landing", "plan_id", planID, "task_id", taskID, "summary_bytes", len(summary))
	return s.taskRun(ctx, planID, taskID)
}

// Landed makes the landing task done, and frees its slot.
func (s *Store) Landed(ctx context.Context, planID, taskID string) error {
	if err := s.move(ctx, s.db, planID, taskID, []string{TaskLanding}, "state = ?, slot = NULL, finished_at = ?", TaskDone, time.Now().UnixMilli()); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("plan task done", "plan_id", planID, "task_id", taskID)
	return nil
}

// LandFailed makes the landing task land_failed for failure, with the files a
// rebase stopped on.
func (s *Store) LandFailed(ctx context.Context, planID, taskID, failure string, conflicts []string) error {
	var list any
	if conflicts != nil {
		raw, err := json.Marshal(conflicts)
		if err != nil {
			return err
		}
		list = string(raw)
	}
	if err := s.move(ctx, s.db, planID, taskID, []string{TaskLanding}, "state = ?, failure = ?, conflicts = ?", TaskLandFailed, failure, list); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("plan task land failed", "plan_id", planID, "task_id", taskID, "failure", failure, "conflicts", len(conflicts))
	return nil
}

// Finish makes the running run done, once every task is done or skipped.
func (s *Store) Finish(ctx context.Context, planID string) error {
	n, err := db.Exec(ctx, s.db, `
		UPDATE plan_runs SET state = ?, finished_at = ?
		WHERE plan_id = ? AND state = ?
		AND NOT EXISTS (SELECT 1 FROM plan_tasks WHERE plan_id = ? AND state NOT IN (?, ?))`,
		StateDone, time.Now().UnixMilli(), planID, StateRunning, planID, TaskDone, TaskSkipped)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("plan %s is not running with every task done or skipped", planID)
	}
	logging.FromContext(ctx).Info("plan done", "plan_id", planID)
	return nil
}

// move changes the task with set when its state is one of from, and returns a
// *TaskStateError when it is not.
func (s *Store) move(ctx context.Context, e db.Execer, planID, taskID string, from []string, set string, args ...any) error {
	query := "UPDATE plan_tasks SET " + set + " WHERE plan_id = ? AND task_id = ? AND state IN (?" + strings.Repeat(", ?", len(from)-1) + ")"
	args = append(args, planID, taskID)
	for _, f := range from {
		args = append(args, f)
	}
	n, err := db.Exec(ctx, e, query, args...)
	if err != nil || n > 0 {
		return err
	}
	t, err := s.taskRun(ctx, planID, taskID)
	if err != nil {
		return err
	}
	return &TaskStateError{PlanID: planID, Task: t, From: from}
}

func (s *Store) taskRun(ctx context.Context, planID, taskID string) (TaskRun, error) {
	tasks, err := s.taskRuns(ctx, planID)
	if err != nil {
		return TaskRun{}, err
	}
	t, ok := tasks[taskID]
	if !ok {
		return TaskRun{}, fmt.Errorf("%w: plan %s has no task %s in its run", ErrNotFound, planID, taskID)
	}
	return t, nil
}

// runState is what a plan's row shows of its run.
type runState struct {
	state string
	done  int
}

// runStates returns the runs' states of the plans matched by where, on docs d, by
// plan id.
func (s *Store) runStates(ctx context.Context, where string, args ...any) (map[string]runState, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.plan_id, r.state, (SELECT count(*) FROM plan_tasks t WHERE t.plan_id = r.plan_id AND t.state = 'done')
		FROM plan_runs r JOIN docs d ON d.id = r.plan_id WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := map[string]runState{}
	for rows.Next() {
		var id string
		var rs runState
		if err := rows.Scan(&id, &rs.state, &rs.done); err != nil {
			return nil, err
		}
		states[id] = rs
	}
	return states, rows.Err()
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func timeOf(ms sql.NullInt64) time.Time {
	if !ms.Valid {
		return time.Time{}
	}
	return time.UnixMilli(ms.Int64)
}
