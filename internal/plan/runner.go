package plan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/worktree"
)

// ProjectInfo is what the runner needs of a project.
type ProjectInfo struct {
	Clone        string // its clone's path, where branches are made
	WorktreesDir string // where its slots go
	Open         bool
}

// RunnerOptions is what a Runner needs of the rest of the daemon, so that this
// package uses no agent, terminal or project.
type RunnerOptions struct {
	// Changes is held for each step, as a request that changes anything holds it,
	// so that a step and a request never act at once.
	Changes sync.Locker
	// Parallel is the plan.parallel setting, read at each step.
	Parallel func() int
	Project  func(ctx context.Context, projectID string) (ProjectInfo, error)
	// AddWorktree adds the project's worktree at path on branch, which is there,
	// and records it; RemoveWorktree removes it, never by force, and forgets it.
	AddWorktree    func(ctx context.Context, projectID, path, branch string) error
	RemoveWorktree func(ctx context.Context, projectID, path string) error
	// CreateAgent starts an agent told first, and has linked write what points at it
	// in the transaction that adds it. It returns the agent's id.
	CreateAgent func(ctx context.Context, projectID, name, dir, first string, linked func(ctx context.Context, tx db.Execer, agentID string) error) (string, error)
	// StopAgent ends an agent so that its project opening does not start it again.
	StopAgent   func(ctx context.Context, agentID string) error
	AgentStates func(ctx context.Context, ids []string) (map[string]AgentState, error)
	// Changed is told of every step, for the project's stream.
	Changed func(projectID string)
}

// Runner runs the plans: it wakes on what it is told of, and takes each plan it
// was woken for through Next's steps until Next says nothing. One goroutine, so
// that one step is done at a time.
type Runner struct {
	ctx   context.Context
	store *Store
	opts  RunnerOptions

	mu    sync.Mutex
	woken []string // plan ids, in the order they were woken
	wake  chan struct{}
	done  chan struct{}
}

// NewRunner returns a Runner of the plans in store, which ends with ctx.
func NewRunner(ctx context.Context, store *Store, opts RunnerOptions) *Runner {
	r := &Runner{ctx: ctx, store: store, opts: opts, wake: make(chan struct{}, 1), done: make(chan struct{})}
	go r.loop()
	return r
}

// Wait waits for the runner to end, once its context is done.
func (r *Runner) Wait() { <-r.done }

// Wake has the runner look at the plan with id; it never blocks.
func (r *Runner) Wake(id string) {
	r.mu.Lock()
	if !slices.Contains(r.woken, id) {
		r.woken = append(r.woken, id)
	}
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// WakeUnfinished has the runner look at every plan running or paused, as when the
// daemon starts or a project opens.
func (r *Runner) WakeUnfinished(ctx context.Context) error {
	ids, err := r.store.Unfinished(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		r.Wake(id)
	}
	return nil
}

func (r *Runner) loop() {
	defer close(r.done)
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.wake:
		}
		for {
			id, ok := r.next()
			if !ok {
				break
			}
			r.runPlan(id)
			if r.ctx.Err() != nil {
				return
			}
		}
	}
}

func (r *Runner) next() (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.woken) == 0 {
		return "", false
	}
	id := r.woken[0]
	r.woken = r.woken[1:]
	return id, true
}

// runPlan does the plan's steps until there are none, or one fails: the failure is
// logged, and the plan is looked at again when it is next woken.
func (r *Runner) runPlan(id string) {
	ctx := logging.NewContext(r.ctx, logging.FromContext(r.ctx).With("debug_id", logging.NewDebugID(), "plan_id", id))
	logging.FromContext(ctx).Info("plan woken")
	for r.ctx.Err() == nil {
		if !r.lock() {
			return
		}
		// A step runs to its end: git cut short could leave a rebase half done.
		more, err := r.step(context.WithoutCancel(ctx), id)
		r.opts.Changes.Unlock()
		if err != nil {
			logging.FromContext(ctx).Error("plan step failed", "err", err)
			return
		}
		if !more {
			return
		}
	}
}

// lock takes the changes' lock, unless the runner ends first.
func (r *Runner) lock() bool {
	got := make(chan struct{})
	go func() {
		r.opts.Changes.Lock()
		close(got)
	}()
	select {
	case <-got:
		return true
	case <-r.ctx.Done():
		go func() {
			<-got
			r.opts.Changes.Unlock()
		}()
		return false
	}
}

// step does the first thing Next says, and reports whether there was one.
func (r *Runner) step(ctx context.Context, id string) (bool, error) {
	run, ok, err := r.store.Run(ctx, id)
	if err != nil || !ok {
		return false, err
	}
	project, err := r.opts.Project(ctx, run.ProjectID)
	if err != nil {
		return false, err
	}
	f := Facts{Run: run, Parallel: run.Parallel, ProjectOpen: project.Open, SlotsGone: map[int]bool{}}
	if f.Parallel == 0 {
		f.Parallel = r.opts.Parallel()
	}
	var agents []string
	for _, t := range run.Tasks {
		if t.AgentID != "" {
			agents = append(agents, t.AgentID)
		}
	}
	if f.Agents, err = r.opts.AgentStates(ctx, agents); err != nil {
		return false, err
	}
	for _, sl := range run.Slots {
		if _, err := os.Stat(sl.Path); errors.Is(err, os.ErrNotExist) {
			f.SlotsGone[sl.Number] = true
		}
	}
	actions := Next(f)
	if len(actions) == 0 {
		return false, nil
	}
	a := actions[0]
	logging.FromContext(ctx).Info("plan step", "action", a.Kind, "task_id", a.Task, "slot", a.Slot, "failure", a.Failure)
	defer r.opts.Changed(run.ProjectID)
	switch a.Kind {
	case ActionDropSlot:
		return true, r.dropSlot(ctx, run, a.Slot)
	case ActionFail:
		return true, r.store.FailTask(ctx, id, a.Task, TaskRunning, a.Failure)
	case ActionLand:
		return true, r.land(ctx, run, a.Task)
	case ActionStart:
		return true, r.start(ctx, run, project, a.Task, a.Slot)
	case ActionFinish:
		return true, r.finish(ctx, run)
	}
	return false, fmt.Errorf("no step does %q", a.Kind)
}

func (r *Runner) dropSlot(ctx context.Context, run Run, slot int) error {
	if sl, ok := slotOf(run, slot); ok {
		// Its directory is gone; this drops git's entry and the project's record.
		if err := r.opts.RemoveWorktree(ctx, run.ProjectID, sl.Path); err != nil {
			logging.FromContext(ctx).Info("slot's worktree not removed", "slot", slot, "path", sl.Path, "err", err)
		}
	}
	return r.store.DropSlot(ctx, run.PlanID, slot)
}

func slotOf(run Run, number int) (Slot, bool) {
	i := slices.IndexFunc(run.Slots, func(s Slot) bool { return s.Number == number })
	if i < 0 {
		return Slot{}, false
	}
	return run.Slots[i], true
}

// planName is the name of the run's plan, from its branch.
func planName(run Run) string { return strings.TrimPrefix(run.Branch, BranchPrefix) }

// BranchPrefix starts a plan's branch, which the plan's name ends.
const BranchPrefix = "plan/"

// TaskBranch is the branch of a plan's task, in a slot of its run.
func TaskBranch(plan, task string) string { return "task/" + plan + "/" + task }

// start starts the task in slot, or in a new one when slot is 0: it holds the slot,
// puts it on the task's branch from the plan's, and creates the task's agent. What
// fails fails the task, with start_failed, keeping its slot for the person.
func (r *Runner) start(ctx context.Context, run Run, project ProjectInfo, taskID string, slot int) error {
	log := logging.FromContext(ctx).With("task_id", taskID)
	task, _ := run.Task(taskID)
	if slot == 0 {
		slot = newSlotNumber(run, project, planName(run))
	}
	if task.Slot == 0 {
		if err := r.store.HoldSlot(ctx, run.PlanID, taskID, slot); err != nil {
			return err
		}
	}
	err := r.startIn(ctx, run, project, taskID, slot)
	if err == nil {
		return nil
	}
	log.Error("task not started", "slot", slot, "err", err)
	from := TaskPending
	if t, ok, rerr := r.store.Run(ctx, run.PlanID); rerr == nil && ok {
		if now, ok := t.Task(taskID); ok && now.State == TaskRunning {
			from = TaskRunning // its agent was created, and did not start
		}
	}
	return r.store.FailTask(ctx, run.PlanID, taskID, from, FailureStartFailed)
}

func (r *Runner) startIn(ctx context.Context, run Run, project ProjectInfo, taskID string, slot int) error {
	name := planName(run)
	branch := TaskBranch(name, taskID)
	exists, err := git.BranchExists(ctx, project.Clone, branch)
	if err != nil {
		return err
	}
	if exists {
		// A start a crash cut short made it; one holding commits not on the plan's
		// branch is someone's work.
		held, err := git.IsAncestor(ctx, project.Clone, branch, run.Branch)
		if err != nil {
			return err
		}
		if !held {
			return fmt.Errorf("branch %s has commits %s does not", branch, run.Branch)
		}
	}
	path := slotPath(project, name, slot)
	if sl, ok := slotOf(run, slot); ok {
		path = sl.Path
	}
	if _, err := os.Stat(path); err == nil {
		if err := git.SwitchReset(ctx, path, branch, run.Branch); err != nil {
			return err
		}
	} else {
		if err := git.ResetBranch(ctx, project.Clone, branch, run.Branch); err != nil {
			return err
		}
		if err := r.opts.AddWorktree(ctx, run.ProjectID, path, branch); err != nil {
			return err
		}
	}
	if _, ok := slotOf(run, slot); !ok {
		if err := r.store.AddSlot(ctx, run.PlanID, slot, path); err != nil {
			return err
		}
	}
	_, err = r.opts.CreateAgent(ctx, run.ProjectID, name+"-"+taskID, path, firstMessage(run, name, taskID), func(ctx context.Context, tx db.Execer, agentID string) error {
		return r.store.StartTask(ctx, tx, run.PlanID, taskID, agentID)
	})
	return err
}

// newSlotNumber is the lowest number neither a slot of the run, nor held by a task,
// has, and whose path is not on disk: a worktree of that name is someone's.
func newSlotNumber(run Run, project ProjectInfo, name string) int {
	for n := 1; ; n++ {
		_, taken := slotOf(run, n)
		taken = taken || slices.ContainsFunc(run.Tasks, func(t TaskRun) bool { return t.Slot == n })
		if taken {
			continue
		}
		if _, err := os.Stat(slotPath(project, name, n)); errors.Is(err, os.ErrNotExist) {
			return n
		}
	}
}

func slotPath(project ProjectInfo, plan string, n int) string {
	return filepath.Join(project.WorktreesDir, plan+"-"+strconv.Itoa(n))
}

// firstMessage is what a task's agent is told first. It is the plan's text and the
// summaries of agents, so it is never logged.
func firstMessage(run Run, plan, taskID string) string {
	def := run.Definition
	var b strings.Builder
	fmt.Fprintf(&b, "You are doing task %s of the plan %s, run by %s.\n", taskID, plan, identity.Name)
	fmt.Fprintf(&b, "Goal: %s\n", def.Goal)
	if len(def.Constraints) > 0 {
		b.WriteString("Constraints:\n")
		for _, c := range def.Constraints {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	i := slices.IndexFunc(def.Tasks, func(t Task) bool { return t.ID == taskID })
	task := def.Tasks[i]
	if len(task.After) > 0 {
		b.WriteString("Done before you, which this task waits for:\n")
		for _, a := range task.After {
			done, _ := run.Task(a)
			fmt.Fprintf(&b, "- %s: %s\n", a, done.Summary)
		}
	}
	fmt.Fprintf(&b, "Your task:\n%s\n", task.Prompt)
	fmt.Fprintf(&b, "When it is done, commit your work on this branch, then run:\n    %s plan task done --agent-id $%s --summary \"<what you did, in a few lines>\"\n",
		identity.Name, identity.Env("AGENT_ID"))
	return b.String()
}

// land stops the task's agent, so that nothing writes in its slot, and lands the
// slot on the plan's branch. A rebase that stops leaves the task land_failed.
func (r *Runner) land(ctx context.Context, run Run, taskID string) error {
	log := logging.FromContext(ctx).With("task_id", taskID)
	task, _ := run.Task(taskID)
	if task.AgentID != "" {
		if err := r.opts.StopAgent(ctx, task.AgentID); err != nil {
			return err
		}
	}
	sl, ok := slotOf(run, task.Slot)
	if !ok {
		log.Info("landing task holds no slot", "slot", task.Slot)
		return r.store.LandFailed(ctx, run.PlanID, taskID, FailureRebase, nil)
	}
	commit, err := worktree.LandOnto(ctx, sl.Path, run.Branch)
	if err != nil {
		var conflicts []string
		if e, ok := errors.AsType[*git.RebaseError](err); ok {
			conflicts = e.Conflicts
		}
		log.Info("task not landed", "slot", sl.Number, "err", err)
		return r.store.LandFailed(ctx, run.PlanID, taskID, FailureRebase, conflicts)
	}
	if err := git.Detach(ctx, sl.Path, commit); err != nil {
		return err
	}
	branch := TaskBranch(planName(run), taskID)
	if deleted, err := git.DeleteBranch(ctx, sl.Path, branch); err != nil || !deleted {
		log.Info("task branch kept", "branch", branch, "err", err)
	}
	return r.store.Landed(ctx, run.PlanID, taskID)
}

// finish leaves the run's branch in one of its free slots, an ordinary worktree now
// for the person to land, removes the other free ones, and makes the run done. A
// slot git will not remove, as it has changes, is kept as an ordinary worktree too.
func (r *Runner) finish(ctx context.Context, run Run) error {
	log := logging.FromContext(ctx)
	kept := false
	for _, sl := range run.Slots {
		held := slices.ContainsFunc(run.Tasks, func(t TaskRun) bool { return t.Slot == sl.Number })
		switch {
		case held:
			log.Info("slot kept for its task", "slot", sl.Number, "path", sl.Path)
		case !kept:
			if err := git.Switch(ctx, sl.Path, run.Branch); err != nil {
				return err
			}
			kept = true
			log.Info("slot kept on the plan's branch", "slot", sl.Number, "path", sl.Path, "branch", run.Branch)
		default:
			if err := r.opts.RemoveWorktree(ctx, run.ProjectID, sl.Path); err != nil {
				log.Info("slot kept, as git would not remove it", "slot", sl.Number, "path", sl.Path, "err", err)
			}
		}
		if err := r.store.DropSlot(ctx, run.PlanID, sl.Number); err != nil {
			return err
		}
	}
	return r.store.Finish(ctx, run.PlanID)
}

// ErrPlansDoNotRun is returned for running a plan of plans, which runs in a later
// version.
var ErrPlansDoNotRun = errors.New("a plan of plans does not run yet: run each of its plans")

// BranchExistsError is returned when a run's branch cannot be made, as it, or a
// branch in its way, is there.
type BranchExistsError struct {
	Branch string
	Err    error
}

func (e *BranchExistsError) Error() string {
	return fmt.Sprintf("branch %s cannot be made: %v", e.Branch, e.Err)
}

func (e *BranchExistsError) Unwrap() error { return e.Err }

// WrongBranchError is returned for a task said done in a slot that is not on its
// branch, whose commits would never land.
type WrongBranchError struct {
	Want string
	On   string // "" when detached
}

func (e *WrongBranchError) Error() string {
	on := "no branch"
	if e.On != "" {
		on = e.On
	}
	return fmt.Sprintf("the task's worktree is on %s, not %s: switch back, with its commits", on, e.Want)
}

// Start runs the plan with id, a draft of tasks no plan of plans lists, in its
// project, which is open: it makes the branch plan/<name> at the default branch's
// tip, writes the run and wakes it. parallel is the run's, 0 for the setting's.
func (r *Runner) Start(ctx context.Context, id string, parallel int) (Plan, error) {
	p, err := r.store.draft(ctx, id)
	if err != nil {
		return Plan{}, err
	}
	if p.Plans != nil {
		return Plan{}, ErrPlansDoNotRun
	}
	if p.Parent != "" {
		parent, err := r.store.GetByName(ctx, p.ProjectID, p.Parent)
		if err != nil {
			return Plan{}, err
		}
		return Plan{}, &InUseError{Plan: p, Parent: parent}
	}
	project, err := r.opts.Project(ctx, p.ProjectID)
	if err != nil {
		return Plan{}, err
	}
	base, err := git.DefaultBranch(ctx, project.Clone)
	if err != nil {
		return Plan{}, err
	}
	if base == "" {
		return Plan{}, errors.New("the project has no default branch to start the plan from")
	}
	branch := BranchPrefix + p.Name
	if err := git.CreateBranch(ctx, project.Clone, branch, base); err != nil {
		return Plan{}, &BranchExistsError{Branch: branch, Err: err}
	}
	if _, err := r.store.Start(ctx, id, base, branch, parallel); err != nil {
		if derr := git.DeleteNewBranch(ctx, project.Clone, branch); derr != nil {
			logging.FromContext(ctx).Error("plan's branch left", "branch", branch, "err", derr)
		}
		return Plan{}, err
	}
	r.Wake(id)
	return r.store.Get(ctx, id)
}

// TaskDone says the running task is done, with its agent's summary, once its slot
// holds every change committed on the task's branch, and wakes the plan to land it.
func (r *Runner) TaskDone(ctx context.Context, planID, taskID, summary string) (TaskRun, error) {
	run, ok, err := r.store.Run(ctx, planID)
	if err != nil {
		return TaskRun{}, err
	}
	task, found := run.Task(taskID)
	if !ok || !found {
		return TaskRun{}, fmt.Errorf("%w: plan %s has no task %s in a run", ErrNotFound, planID, taskID)
	}
	if task.State != TaskRunning {
		return TaskRun{}, &TaskStateError{PlanID: planID, Task: task, From: []string{TaskRunning}}
	}
	sl, ok := slotOf(run, task.Slot)
	if !ok {
		return TaskRun{}, fmt.Errorf("task %s holds no slot", taskID)
	}
	changes, err := git.Changes(ctx, sl.Path)
	if err != nil {
		return TaskRun{}, err
	}
	if len(changes) > 0 {
		return TaskRun{}, fmt.Errorf("%w: %d in %s", worktree.ErrNotCommitted, len(changes), sl.Path)
	}
	want := TaskBranch(planName(run), taskID)
	on, err := git.CurrentBranch(ctx, sl.Path)
	if err != nil {
		return TaskRun{}, err
	}
	if on != want {
		return TaskRun{}, &WrongBranchError{Want: want, On: on}
	}
	done, err := r.store.TaskDone(ctx, planID, taskID, summary)
	if err != nil {
		return TaskRun{}, err
	}
	r.Wake(planID)
	return done, nil
}
