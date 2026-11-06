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
	// ResumeAgent goes on with an agent in its session, told text, with linked
	// written in the transaction that tells it, and starts it if it is not running.
	ResumeAgent func(ctx context.Context, agentID, text string, linked func(ctx context.Context, tx db.Execer, agentID string) error) error
	// AgentIn returns the name of a live agent of the project working in dir, ""
	// for none.
	AgentIn func(ctx context.Context, projectID, dir string) (string, error)
	// AgentNameTaken reports whether an agent of the project has name.
	AgentNameTaken func(ctx context.Context, projectID, name string) (bool, error)
	// StopAgent ends an agent so that its project opening does not start it again.
	StopAgent func(ctx context.Context, agentID string) error
	// RemoveAgent removes an agent that has ended, as agent remove does: one with
	// children stays as a tombstone.
	RemoveAgent func(ctx context.Context, agentID string) error
	AgentStates func(ctx context.Context, ids []string) (map[string]AgentState, error)
	// CheckCommand is the project's landing check, "" for none.
	CheckCommand func(ctx context.Context, projectID string) (string, error)
	// Check runs command in the slot at dir, in the terminal CheckTerminal names,
	// until it ends; one that fails keeps its terminal. A check cut short by ctx is
	// stopped.
	Check func(ctx context.Context, projectID, dir, command string) error
	// LandByPR starts landing the project's worktree at path by PR, in the
	// background, as wdx worktree land --method pr does, or says why it cannot.
	LandByPR func(ctx context.Context, projectID, path string) error
	// Changed is told of every step, for the project's stream.
	Changed func(projectID string)
	// PullRequest returns the latest PR of branch, in the repository holding dir,
	// and false for none, or with no gh to ask. It asks GitHub, so only the
	// person's answer calls it.
	PullRequest func(ctx context.Context, dir, branch string) (PullRequest, bool, error)
}

// Runner runs the plans: it wakes on what it is told of, and takes each plan it
// was woken for through Next's steps until Next says nothing. One goroutine, so
// that one step is done at a time.
type Runner struct {
	ctx   context.Context
	store *Store
	opts  RunnerOptions

	mu       sync.Mutex
	woken    []string                      // plan ids, in the order they were woken
	landings map[string]context.CancelFunc // the plans with a landing going, which each cuts short
	// failedChildren are the plans a parent failed to start, by id, which wait for
	// the person rather than being tried at each step.
	failedChildren map[string]bool
	landers        sync.WaitGroup
	wake           chan struct{}
	done           chan struct{}
}

// NewRunner returns a Runner of the plans in store, which ends with ctx.
func NewRunner(ctx context.Context, store *Store, opts RunnerOptions) *Runner {
	r := &Runner{ctx: ctx, store: store, opts: opts, landings: map[string]context.CancelFunc{}, failedChildren: map[string]bool{}, wake: make(chan struct{}, 1), done: make(chan struct{})}
	go r.loop()
	return r
}

// Wait waits for the runner and its landings to end, once its context is done.
func (r *Runner) Wait() {
	<-r.done
	r.landers.Wait()
}

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
		if !r.lock(r.ctx) {
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

// lock takes the changes' lock, unless ctx ends first.
func (r *Runner) lock(ctx context.Context) bool {
	got := make(chan struct{})
	go func() {
		r.opts.Changes.Lock()
		close(got)
	}()
	select {
	case <-got:
		return true
	case <-ctx.Done():
		go func() {
			<-got
			r.opts.Changes.Unlock()
		}()
		return false
	}
}

// landing returns how to cut short the plan's landing going, if one is.
func (r *Runner) landing(planID string) (context.CancelFunc, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cancel, ok := r.landings[planID]
	return cancel, ok
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
	if run.Definition.Plans != nil {
		return r.stepParent(ctx, run, project)
	}
	cancelLanding, landing := r.landing(id)
	if landing && run.State == StateCancelled {
		logging.FromContext(ctx).Info("landing cut short, as the plan is cancelled")
		cancelLanding()
	}
	f := Facts{Run: run, Parallel: run.Parallel, ProjectOpen: project.Open, Landing: landing, SlotsGone: map[int]bool{}}
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
	case ActionStop:
		task, _ := run.Task(a.Task)
		return true, r.opts.StopAgent(ctx, task.AgentID)
	case ActionRelease:
		return true, r.release(ctx, run, f, a.Task, a.Slot)
	case ActionClose:
		if err := r.store.Close(ctx, id); err != nil {
			return false, err
		}
		r.removeAgents(ctx, run, f.Agents)
		return true, nil
	case ActionLand:
		return true, r.land(ctx, run, a.Task)
	case ActionStart:
		return true, r.start(ctx, run, project, a.Task, a.Slot)
	case ActionFinish:
		kept, err := r.finish(ctx, run)
		if err != nil {
			return false, err
		}
		r.removeAgents(ctx, run, f.Agents)
		r.openPR(ctx, run, kept)
		r.wakeParent(ctx, id)
		return true, nil
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

// startIn puts the slot on the task's branch and starts its agent. A task that
// started before keeps its branch as it is, with its earlier work, and resumes its
// agent if it still has one, or starts a new one named after the attempt.
func (r *Runner) startIn(ctx context.Context, run Run, project ProjectInfo, taskID string, slot int) error {
	name := planName(run)
	branch := TaskBranch(name, taskID)
	task, _ := run.Task(taskID)
	exists, err := git.BranchExists(ctx, project.Clone, branch)
	if err != nil {
		return err
	}
	again := exists && !task.StartedAt.IsZero()
	if exists && !again {
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
	if err := r.putOnBranch(ctx, run, project, path, branch, again); err != nil {
		return err
	}
	if _, ok := slotOf(run, slot); !ok {
		if err := r.store.AddSlot(ctx, run.PlanID, slot, path); err != nil {
			return err
		}
	}
	linked := func(ctx context.Context, tx db.Execer, agentID string) error {
		return r.store.StartTask(ctx, tx, run.PlanID, taskID, agentID)
	}
	check, err := r.opts.CheckCommand(ctx, run.ProjectID)
	if err != nil {
		return err
	}
	why := retryNote(run, task, check, path)
	if task.AgentID != "" {
		logging.FromContext(ctx).Info("task's agent resumed", "task_id", taskID, "agent_id", task.AgentID, "failure", task.Failure)
		return r.opts.ResumeAgent(ctx, task.AgentID, why+doneLine(), linked)
	}
	agentName := name + "-" + taskID
	first := firstMessage(run, name, taskID)
	if !task.StartedAt.IsZero() {
		if agentName, err = r.attemptName(ctx, run.ProjectID, agentName); err != nil {
			return err
		}
		first = previousAttempt(run, branch, why) + first
	}
	_, err = r.opts.CreateAgent(ctx, run.ProjectID, agentName, path, first, linked)
	return err
}

// putOnBranch puts the slot at path on the task's branch: from the plan's branch,
// or as it is for a task that started before.
func (r *Runner) putOnBranch(ctx context.Context, run Run, project ProjectInfo, path, branch string, again bool) error {
	_, err := os.Stat(path)
	switch {
	case err == nil && again:
		on, err := git.CurrentBranch(ctx, path)
		if err != nil || on == branch {
			return err
		}
		return git.Switch(ctx, path, branch)
	case err == nil:
		return git.SwitchReset(ctx, path, branch, run.Branch)
	case !again:
		if err := git.ResetBranch(ctx, project.Clone, branch, run.Branch); err != nil {
			return err
		}
	}
	return r.opts.AddWorktree(ctx, run.ProjectID, path, branch)
}

// attemptName is name-<n> for the lowest n from 2 no agent of the project has: a
// task's earlier agent keeps its name.
func (r *Runner) attemptName(ctx context.Context, projectID, name string) (string, error) {
	for n := 2; ; n++ {
		try := name + "-" + strconv.Itoa(n)
		taken, err := r.opts.AgentNameTaken(ctx, projectID, try)
		if err != nil || !taken {
			return try, err
		}
	}
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
	b.WriteString(doneLine())
	return b.String()
}

// doneLine is how a task's agent says it is done.
func doneLine() string {
	return fmt.Sprintf("When it is done, commit your work on this branch, then run:\n    %s plan task done --agent-id $%s --summary \"<what you did, in a few lines>\"\n",
		identity.Name, identity.Env("AGENT_ID"))
}

// CheckTerminal names the terminal that runs the landing check of the slot at path,
// as a worktree's landing names its own.
func CheckTerminal(path string) string { return "land-" + filepath.Base(path) }

// retryNote says why the task runs again, by why it failed; "" for a task that
// never started. Never logged, as it names the plan's files.
func retryNote(run Run, task TaskRun, check, slotPath string) string {
	plan := planName(run)
	switch {
	case task.StartedAt.IsZero():
		return ""
	case task.Failure == FailureRebase:
		return fmt.Sprintf("Landing task %s of the plan %s on %s stopped on conflicts in %s, and was undone. Rebase %s onto %s, resolve them, and go on.\n",
			task.ID, plan, run.Branch, strings.Join(task.Conflicts, ", "), TaskBranch(plan, task.ID), run.Branch)
	case task.Failure == FailureCheck:
		return fmt.Sprintf("After rebasing task %s of the plan %s onto %s, the check `%s` failed; its output is in the terminal %s. Run it, fix what fails, and go on.\n",
			task.ID, plan, run.Branch, check, CheckTerminal(slotPath))
	default:
		return fmt.Sprintf("You ended before saying task %s of the plan %s was done. Go on with it where you left it.\n", task.ID, plan)
	}
}

// previousAttempt is what a new agent of a task that started before is told first.
func previousAttempt(run Run, branch, why string) string {
	return fmt.Sprintf("A previous attempt at this task left its commits on this branch, %s: read them with `git log %s..HEAD` before going on.\n%s",
		branch, run.Branch, why)
}

// land stops the task's agent, so that nothing writes in its slot, and lands the
// slot on the plan's branch in a goroutine of its own, as a check can take many
// minutes; the plan is woken when it ends.
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
	check, err := r.opts.CheckCommand(ctx, run.ProjectID)
	if err != nil {
		return err
	}
	lctx, cancel := context.WithCancel(logging.NewContext(r.ctx, log))
	r.mu.Lock()
	r.landings[run.PlanID] = cancel
	r.mu.Unlock()
	log.Info("landing started", "slot", sl.Number, "check", check != "")
	r.landers.Go(func() {
		wake := r.landTask(lctx, run, taskID, sl, check)
		r.mu.Lock()
		delete(r.landings, run.PlanID)
		r.mu.Unlock()
		cancel()
		if wake {
			r.Wake(run.PlanID)
		}
	})
	return nil
}

// landTask rebases the slot onto the plan's branch, runs the check, and moves the
// branch to the slot's commit, again from the rebase if the branch moved during the
// check. Git runs to its end, holding the changes' lock; the check holds nothing,
// and one cut short, or ended by its project closing, leaves the task landing, to
// land again when it can. It reports whether to wake the plan: not after a step
// that failed, which, as any step's failure, waits for the plan's next wake rather
// than going round again at once.
func (r *Runner) landTask(ctx context.Context, run Run, taskID string, sl Slot, check string) (wake bool) {
	log := logging.FromContext(ctx)
	gitCtx := context.WithoutCancel(ctx)
	wake = true
	locked := func(do func() error) bool {
		if !r.lock(ctx) {
			log.Info("landing cut short")
			return false
		}
		defer r.opts.Changes.Unlock()
		defer r.opts.Changed(run.ProjectID)
		if err := do(); err != nil {
			log.Error("landing step failed", "err", err)
			wake = false
			return false
		}
		return true
	}
	failed := func(failure string, conflicts []string) {
		locked(func() error { return r.store.LandFailed(gitCtx, run.PlanID, taskID, failure, conflicts) })
	}
	for {
		var commit string
		var rebaseErr error
		if !locked(func() error {
			commit, rebaseErr = worktree.RebaseOnto(gitCtx, sl.Path, run.Branch)
			return nil
		}) {
			return wake
		}
		if rebaseErr != nil {
			var conflicts []string
			if e, ok := errors.AsType[*git.RebaseError](rebaseErr); ok {
				conflicts = e.Conflicts
			}
			log.Info("task not landed", "slot", sl.Number, "err", rebaseErr)
			failed(FailureRebase, conflicts)
			return wake
		}
		if check != "" {
			err := r.opts.Check(ctx, run.ProjectID, sl.Path, check)
			if ctx.Err() != nil {
				log.Info("landing cut short during the check")
				return wake
			}
			if err != nil {
				if p, perr := r.opts.Project(gitCtx, run.ProjectID); perr == nil && !p.Open {
					log.Info("landing waits for the project to open", "err", err)
					return wake
				}
				log.Info("task's check failed", "slot", sl.Number, "err", err)
				failed(FailureCheck, nil)
				return wake
			}
		}
		again := false
		if !locked(func() error {
			now, err := worktree.RebaseOnto(gitCtx, sl.Path, run.Branch)
			if err != nil || now != commit {
				log.Info("plan's branch moved during the check: landing again", "err", err)
				again = true
				return nil
			}
			return r.landed(gitCtx, run, taskID, sl, commit)
		}) || !again {
			return wake
		}
	}
}

// landed moves the plan's branch to commit, and frees the slot: detached at it, the
// task's branch deleted, as the plan's holds it.
func (r *Runner) landed(ctx context.Context, run Run, taskID string, sl Slot, commit string) error {
	if err := git.MoveBranch(ctx, sl.Path, run.Branch, commit); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("landed", "slot", sl.Number, "branch", run.Branch, "commit", commit)
	if err := git.Detach(ctx, sl.Path, commit); err != nil {
		return err
	}
	branch := TaskBranch(planName(run), taskID)
	if deleted, err := git.DeleteBranch(ctx, sl.Path, branch); err != nil || !deleted {
		logging.FromContext(ctx).Info("task branch kept", "branch", branch, "err", err)
	}
	return r.store.Landed(ctx, run.PlanID, taskID)
}

// release takes back the slot a skipped task holds, whose agent has ended, or, in a
// cancelled run whose agents are stopped, any slot. One holding work (changes, or commits on its task's branch the plan's
// branch does not hold) leaves the plan as an ordinary worktree, as wdx never
// deletes work. One holding none is freed: detached at the plan's branch, its
// task's branch deleted; and removed if the run is cancelled.
func (r *Runner) release(ctx context.Context, run Run, f Facts, taskID string, slot int) error {
	log := logging.FromContext(ctx).With("task_id", taskID, "slot", slot)
	leave := func() error {
		if taskID == "" {
			return nil
		}
		return r.store.LeaveSlot(ctx, run.PlanID, taskID)
	}
	sl, ok := slotOf(run, slot)
	if !ok || f.SlotsGone[slot] {
		return leave() // Next drops a slot gone that nothing holds
	}
	branch := TaskBranch(planName(run), taskID)
	work, err := holdsWork(ctx, sl.Path, branch, run.Branch, taskID != "")
	if err != nil {
		return err
	}
	if work {
		log.Info("slot kept as a worktree, as it holds work", "path", sl.Path)
		if err := r.store.DropSlot(ctx, run.PlanID, slot); err != nil {
			return err
		}
		return leave()
	}
	if taskID != "" {
		tip, err := git.Resolve(ctx, sl.Path, run.Branch)
		if err != nil {
			return err
		}
		if err := git.Detach(ctx, sl.Path, tip); err != nil {
			return err
		}
		if exists, err := git.BranchExists(ctx, sl.Path, branch); err == nil && exists {
			if deleted, err := git.DeleteBranch(ctx, sl.Path, branch); err != nil || !deleted {
				log.Info("task branch kept", "branch", branch, "err", err)
			}
		}
		if err := leave(); err != nil {
			return err
		}
	}
	if run.State != StateCancelled {
		log.Info("slot freed", "path", sl.Path)
		return nil
	}
	if err := r.opts.RemoveWorktree(ctx, run.ProjectID, sl.Path); err != nil {
		log.Info("slot kept, as git would not remove it", "path", sl.Path, "err", err)
	}
	return r.store.DropSlot(ctx, run.PlanID, slot)
}

// holdsWork reports whether the slot at path has changes, or, when it was a task's,
// commits on branch that base does not hold.
func holdsWork(ctx context.Context, path, branch, base string, tasks bool) (bool, error) {
	changes, err := git.Changes(ctx, path)
	if err != nil || len(changes) > 0 {
		return len(changes) > 0, err
	}
	if !tasks {
		return false, nil
	}
	exists, err := git.BranchExists(ctx, path, branch)
	if err != nil || !exists {
		return false, err
	}
	held, err := git.IsAncestor(ctx, path, branch, base)
	return !held, err
}

// finish leaves the run's branch in one of its free slots, an ordinary worktree now
// for the person to land, removes the other free ones, and makes the run done. A
// slot git will not remove, as it has changes, is kept as an ordinary worktree too.
// It returns the path of the worktree on the run's branch, "" for none.
func (r *Runner) finish(ctx context.Context, run Run) (string, error) {
	log := logging.FromContext(ctx)
	kept := ""
	for _, sl := range run.Slots {
		held := slices.ContainsFunc(run.Tasks, func(t TaskRun) bool { return t.Slot == sl.Number })
		switch {
		case held:
			log.Info("slot kept for its task", "slot", sl.Number, "path", sl.Path)
		case kept == "":
			if err := git.Switch(ctx, sl.Path, run.Branch); err != nil {
				return "", err
			}
			kept = sl.Path
			log.Info("slot kept on the plan's branch", "slot", sl.Number, "path", sl.Path, "branch", run.Branch)
		default:
			if err := r.opts.RemoveWorktree(ctx, run.ProjectID, sl.Path); err != nil {
				log.Info("slot kept, as git would not remove it", "slot", sl.Number, "path", sl.Path, "err", err)
			}
		}
		if err := r.store.DropSlot(ctx, run.PlanID, sl.Number); err != nil {
			return "", err
		}
	}
	return kept, r.store.Finish(ctx, run.PlanID)
}

// openPR lands the worktree at path, on the done run's branch, by PR, if its plan,
// or the plan of plans listing it, says pr: true. A landing that fails or cannot
// start leaves the plan done, its work on its branch either way: one that fails
// says why in the worktree's landing, one that cannot start in the log, and the
// person lands it once they have dealt with it.
func (r *Runner) openPR(ctx context.Context, run Run, path string) {
	log := logging.FromContext(ctx).With("branch", run.Branch)
	pr, err := r.wantsPR(ctx, run)
	if err != nil {
		log.Error("plan's PR not opened, as whether it wants one is not known", "err", err)
		return
	}
	switch {
	case !pr:
		return
	case path == "":
		log.Info("plan's PR not opened, as no worktree is on its branch")
		return
	}
	if err := r.opts.LandByPR(ctx, run.ProjectID, path); err != nil {
		log.Error("plan's PR landing not started: the plan stays done", "path", path, "err", err)
		return
	}
	log.Info("plan's PR landing started", "path", path)
}

// wantsPR reports whether the run's plan wants its PR opened once done: a plan a
// plan of plans lists, as its parent says, any other as it says itself.
func (r *Runner) wantsPR(ctx context.Context, run Run) (bool, error) {
	p, err := r.store.Get(ctx, run.PlanID)
	if err != nil || p.Parent == "" {
		return run.Definition.PR, err
	}
	parent, err := r.store.GetByName(ctx, p.ProjectID, p.Parent)
	if err != nil {
		return false, err
	}
	parentRun, ran, err := r.store.Run(ctx, parent.ID)
	if err != nil || !ran {
		return false, err
	}
	return parentRun.Definition.PR, nil
}

// removeAgents removes the agents of the tasks of a run that is over, done or
// closed, whose agents have all ended. Each task keeps its state and summary, its
// agent becoming null. One that cannot be removed stays for the person to remove,
// as the run is over either way.
func (r *Runner) removeAgents(ctx context.Context, run Run, agents map[string]AgentState) {
	log := logging.FromContext(ctx)
	for _, t := range run.Tasks {
		if state, ok := agents[t.AgentID]; t.AgentID == "" || !ok || state == AgentRemoved {
			continue
		}
		if err := r.opts.RemoveAgent(ctx, t.AgentID); err != nil {
			log.Error("plan agent not removed", "task_id", t.ID, "agent_id", t.AgentID, "err", err)
			continue
		}
		log.Info("plan agent removed", "plan_id", run.PlanID, "task_id", t.ID, "agent_id", t.AgentID)
	}
}

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

// Start runs the plan with id, a draft, in its project, which is open: a plan of
// tasks as startPlan says, a plan of plans as startParent does, and one a running
// plan of plans lists as the person's answer, which answer says. parallel is the
// run's, 0 for the setting's.
func (r *Runner) Start(ctx context.Context, id string, parallel int) (Plan, error) {
	p, err := r.store.draft(ctx, id)
	if err != nil {
		return Plan{}, err
	}
	if p.Parent != "" {
		parent, err := r.store.GetByName(ctx, p.ProjectID, p.Parent)
		if err != nil {
			return Plan{}, err
		}
		if parent.State == StateRunning {
			return r.answer(ctx, p, parent, parallel)
		}
		return Plan{}, &InUseError{Plan: p, Parent: parent}
	}
	if p.Plans != nil {
		if parallel != 0 {
			return Plan{}, ErrParentParallel
		}
		return r.startParent(ctx, p)
	}
	return r.startPlan(ctx, p, parallel, "")
}

// startPlan runs the draft plan of tasks p: it makes the branch plan/<name> at the
// default branch's tip, or, stacked on the plan called on, at the tip of that one's
// branch, which the run keeps as its base commit; then it writes the run and wakes
// it.
func (r *Runner) startPlan(ctx context.Context, p Plan, parallel int, on string) (Plan, error) {
	id := p.ID
	project, err := r.opts.Project(ctx, p.ProjectID)
	if err != nil {
		return Plan{}, err
	}
	base, from, baseCommit := "", "", ""
	if on != "" {
		base = BranchPrefix + on
		if baseCommit, err = git.Resolve(ctx, project.Clone, "refs/heads/"+base); err != nil {
			return Plan{}, err
		}
		from = baseCommit
		logging.FromContext(ctx).Info("plan starting stacked", "plan_id", id, "on", base, "base_commit", baseCommit)
	} else {
		if base, err = git.DefaultBranch(ctx, project.Clone); err != nil {
			return Plan{}, err
		}
		if base == "" {
			return Plan{}, errors.New("the project has no default branch to start the plan from")
		}
		from = base
	}
	branch := BranchPrefix + p.Name
	if err := git.CreateBranch(ctx, project.Clone, branch, from); err != nil {
		return Plan{}, &BranchExistsError{Branch: branch, Err: err}
	}
	if _, err := r.store.Start(ctx, id, base, branch, parallel, baseCommit); err != nil {
		if derr := git.DeleteNewBranch(ctx, project.Clone, branch); derr != nil {
			logging.FromContext(ctx).Error("plan's branch left", "branch", branch, "err", derr)
		}
		return Plan{}, err
	}
	r.Wake(id)
	return r.store.Get(ctx, id)
}

// TaskDone says the task is done, with a summary, once its slot holds every change
// committed on the task's branch, and wakes the plan to land it: a running task,
// from its agent, or a failed one the person finished, once no agent works in its
// slot.
func (r *Runner) TaskDone(ctx context.Context, planID, taskID, summary string) (TaskRun, error) {
	if _, err := r.landable(ctx, planID, taskID, []string{TaskRunning, TaskFailed}); err != nil {
		return TaskRun{}, err
	}
	done, err := r.store.TaskDone(ctx, planID, taskID, summary)
	if err != nil {
		return TaskRun{}, err
	}
	r.Wake(planID)
	return done, nil
}

// LandTask lands the land_failed task again, once the person dealt with what
// stopped it: its slot holds every change committed on its branch, and no agent
// works in it.
func (r *Runner) LandTask(ctx context.Context, planID, taskID string) (TaskRun, error) {
	if _, err := r.landable(ctx, planID, taskID, []string{TaskLandFailed}); err != nil {
		return TaskRun{}, err
	}
	t, err := r.store.LandTask(ctx, planID, taskID)
	if err != nil {
		return TaskRun{}, err
	}
	r.Wake(planID)
	return t, nil
}

// landable returns the task, in one of the states from, of a run going on, once its
// slot can land: every change committed, on the task's branch, and, for a task not
// running, no agent at work in it, as nothing may write there during the landing.
func (r *Runner) landable(ctx context.Context, planID, taskID string, from []string) (TaskRun, error) {
	if err := r.store.goingOn(ctx, planID); err != nil {
		return TaskRun{}, err
	}
	run, _, err := r.store.Run(ctx, planID)
	if err != nil {
		return TaskRun{}, err
	}
	task, found := run.Task(taskID)
	if !found {
		return TaskRun{}, fmt.Errorf("%w: plan %s has no task %s in a run", ErrNotFound, planID, taskID)
	}
	if !slices.Contains(from, task.State) {
		return TaskRun{}, &TaskStateError{PlanID: planID, Task: task, From: from}
	}
	sl, ok := slotOf(run, task.Slot)
	if !ok {
		return TaskRun{}, fmt.Errorf("task %s holds no slot", taskID)
	}
	if task.State != TaskRunning {
		name, err := r.opts.AgentIn(ctx, run.ProjectID, sl.Path)
		if err != nil {
			return TaskRun{}, err
		}
		if name != "" {
			return TaskRun{}, fmt.Errorf("%w: %s is in %s", worktree.ErrAgentWorking, name, sl.Path)
		}
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
	return task, nil
}

// RetryTask runs the failed or land_failed task again, resuming its agent unless
// fresh or removed, and wakes the plan to start it.
func (r *Runner) RetryTask(ctx context.Context, planID, taskID string, fresh bool) (TaskRun, error) {
	if !fresh {
		removed, err := r.agentRemoved(ctx, planID, taskID)
		if err != nil {
			return TaskRun{}, err
		}
		if removed {
			logging.FromContext(ctx).Info("task's agent was removed: a new one starts", "task_id", taskID)
			fresh = true
		}
	}
	t, err := r.store.RetryTask(ctx, planID, taskID, fresh)
	if err == nil {
		r.Wake(planID)
	}
	return t, err
}

// SkipTask skips the task, and wakes the plan to release its slot and start what
// waits for it.
func (r *Runner) SkipTask(ctx context.Context, planID, taskID string) (TaskRun, error) {
	t, err := r.store.SkipTask(ctx, planID, taskID)
	if err == nil {
		r.Wake(planID)
	}
	return t, err
}

// Pause pauses the plan's run, and reports false when it already was; a plan of
// plans pauses its plans that run too.
func (r *Runner) Pause(ctx context.Context, planID string) (bool, error) {
	changed, err := r.store.Pause(ctx, planID)
	if !changed || err != nil {
		return changed, err
	}
	running := func(c Plan) bool { return c.State == StateRunning || c.PausedReason == PausedProjectClosed }
	return true, r.eachChild(ctx, planID, running, func(id string) error {
		_, err := r.store.Pause(ctx, id)
		return err
	})
}

// Resume resumes the plan's run, and wakes it, reporting false when it was running;
// a plan of plans resumes its plans that are paused too.
func (r *Runner) Resume(ctx context.Context, planID string) (bool, error) {
	changed, err := r.store.Resume(ctx, planID)
	if !changed || err != nil {
		return changed, err
	}
	r.Wake(planID)
	paused := func(c Plan) bool { return c.PausedReason == PausedByPerson }
	return true, r.eachChild(ctx, planID, paused, func(id string) error {
		if _, err := r.store.Resume(ctx, id); err != nil {
			return err
		}
		r.Wake(id)
		return nil
	})
}

// eachChild calls do with the id of each plan the plan of plans with id lists that
// is; nothing for a plan of tasks. A run going on in a closed project shows paused,
// for the project's reason, and is running.
func (r *Runner) eachChild(ctx context.Context, id string, is func(Plan) bool, do func(id string) error) error {
	p, err := r.store.Get(ctx, id)
	if err != nil || p.Plans == nil {
		return err
	}
	children, err := r.store.children(ctx, p)
	if err != nil {
		return err
	}
	for _, c := range children {
		if is(c) {
			if err := do(c.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Cancel cancels the plan's run, and wakes it to stop its agents and landing and
// release its slots.
func (r *Runner) Cancel(ctx context.Context, planID string) error {
	err := r.store.Cancel(ctx, planID)
	if err == nil {
		r.Wake(planID)
	}
	return err
}

// SetParallel sets the most tasks the plan's run runs at once, and wakes it, as
// more may start, reporting false when it already was n.
func (r *Runner) SetParallel(ctx context.Context, planID string, n int) (bool, error) {
	if p, err := r.store.Get(ctx, planID); err != nil || p.Plans != nil {
		if err == nil {
			err = ErrParentParallel
		}
		return false, err
	}
	changed, err := r.store.SetParallel(ctx, planID, n)
	if changed {
		r.Wake(planID)
	}
	return changed, err
}

// agentRemoved reports whether the agent of the plan's task was removed, so that
// there is none to resume.
func (r *Runner) agentRemoved(ctx context.Context, planID, taskID string) (bool, error) {
	run, ok, err := r.store.Run(ctx, planID)
	if err != nil || !ok {
		return false, err
	}
	task, found := run.Task(taskID)
	if !found || task.AgentID == "" {
		return false, nil
	}
	states, err := r.opts.AgentStates(ctx, []string{task.AgentID})
	if err != nil {
		return false, err
	}
	state, known := states[task.AgentID]
	return !known || state == AgentRemoved, nil
}
