package plan

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/logging"
)

// A plan of plans runs by starting the plans it lists, each once the plans before
// it are done and their work is in the default branch, as a child starts from it
// (docs/PLANS.md, Step 6 in detail).

// ChildRun is what a plan of plans' run sees of a plan it lists.
type ChildRun struct {
	Plan   Plan
	After  []string
	Landed bool // done, and its branch in the default branch, as git shows it
	// StartFailed says its parent failed to start it since the daemon started: it
	// waits for the person, whose answer says why.
	StartFailed bool
}

// ParentFacts is what NextParent decides from.
type ParentFacts struct {
	Run         Run
	ProjectOpen bool
	Children    []ChildRun // in the definition's order
}

// The actions of a plan of plans' run, besides ActionClose and ActionFinish.
const (
	ActionStartPlan  ActionKind = "start_plan"  // a draft child, from the default branch
	ActionCancelPlan ActionKind = "cancel_plan" // a child going on, its parent cancelled
)

// ErrParentParallel is returned for setting how many tasks a plan of plans runs at
// once, which each of its plans says for itself.
var ErrParentParallel = errors.New("a plan of plans runs no tasks itself: set each of its plans")

// NextParent says what to do about a plan of plans' run now, as Next does for a
// plan of tasks: start each child whose plans before it are done and landed, and
// finish once every child is done. A cancelled run cancels its children going on,
// then closes.
func NextParent(f ParentFacts) []Action {
	r := f.Run
	var actions []Action
	if r.State == StateCancelled && r.FinishedAt.IsZero() {
		for _, c := range f.Children {
			if goingOn(c.Plan.State) {
				actions = append(actions, Action{Kind: ActionCancelPlan, Plan: c.Plan.Name})
			}
		}
		return append(actions, Action{Kind: ActionClose})
	}
	if !goingOn(r.State) {
		return nil
	}
	done := true
	for _, c := range f.Children {
		if c.Plan.State != StateDone {
			done = false
		}
		if r.State == StateRunning && f.ProjectOpen && c.Plan.State == StateDraft && !c.StartFailed && childReady(c, f.Children) {
			actions = append(actions, Action{Kind: ActionStartPlan, Plan: c.Plan.Name})
		}
	}
	if done {
		actions = append(actions, Action{Kind: ActionFinish})
	}
	return actions
}

// goingOn reports whether a run in state is running or paused.
func goingOn(state string) bool { return state == StateRunning || state == StatePaused }

// childReady reports whether every plan c waits for is done and landed.
func childReady(c ChildRun, all []ChildRun) bool {
	for _, a := range c.After {
		i := slices.IndexFunc(all, func(o ChildRun) bool { return o.Plan.Name == a })
		if i < 0 || all[i].Plan.State != StateDone || !all[i].Landed {
			return false
		}
	}
	return true
}

// Waiting reports whether c waits for the person: a draft whose plans before it
// are done, but not all landed, or that its parent failed to start, while its
// parent's run goes on.
func (c ChildRun) Waiting(parent Run, all []ChildRun) bool {
	if !goingOn(parent.State) || c.Plan.State != StateDraft {
		return false
	}
	if c.StartFailed {
		return true
	}
	for _, a := range c.After {
		i := slices.IndexFunc(all, func(o ChildRun) bool { return o.Plan.Name == a })
		if i < 0 || all[i].Plan.State != StateDone {
			return false
		}
	}
	return !childReady(c, all)
}

// Children returns the plans the plan of plans with id lists, as its run sees
// them, in its definition's order; with no run, each is a draft, not landed.
func (r *Runner) Children(ctx context.Context, id string) ([]ChildRun, Run, error) {
	run, ran, err := r.store.Run(ctx, id)
	if err != nil {
		return nil, Run{}, err
	}
	def := run.Definition
	if !ran {
		p, err := r.store.Get(ctx, id)
		if err != nil {
			return nil, Run{}, err
		}
		if _, _, def, err = r.store.Content(ctx, id, 0); err != nil {
			return nil, Run{}, err
		}
		run = Run{PlanID: id, ProjectID: p.ProjectID, State: StateDraft, Definition: def}
	}
	var clone string
	if ran {
		project, err := r.opts.Project(ctx, run.ProjectID)
		if err != nil {
			return nil, Run{}, err
		}
		clone = project.Clone
	}
	children := make([]ChildRun, 0, len(def.Plans))
	for _, c := range def.Plans {
		p, err := r.store.GetByName(ctx, run.ProjectID, c.ID)
		if err != nil {
			return nil, Run{}, err
		}
		child := ChildRun{Plan: p, After: c.After, StartFailed: r.startFailed(p.ID)}
		if ran && p.State == StateDone {
			if child.Landed, err = landed(ctx, clone, p.Name, run.Base); err != nil {
				return nil, Run{}, err
			}
		}
		children = append(children, child)
	}
	return children, run, nil
}

// landed reports whether the done plan called name has its work in base: its
// branch's tip is in it, or its branch is gone, which wdx deletes only once git
// says it is merged.
func landed(ctx context.Context, clone, name, base string) (bool, error) {
	log := logging.FromContext(ctx).With("child", name, "base", base)
	branch := BranchPrefix + name
	there, err := git.BranchExists(ctx, clone, branch)
	if err != nil {
		return false, err
	}
	if !there {
		log.Info("plan taken as landed, as its branch is gone")
		return true, nil
	}
	in, err := git.IsAncestor(ctx, clone, "refs/heads/"+branch, "refs/heads/"+base)
	if err != nil {
		return false, err
	}
	log.Info("plan's landing read with git", "landed", in)
	return in, nil
}

// stepParent does the first thing NextParent says, and reports whether there was
// one.
func (r *Runner) stepParent(ctx context.Context, run Run, project ProjectInfo) (bool, error) {
	log := logging.FromContext(ctx)
	children, run, err := r.Children(ctx, run.PlanID)
	if err != nil {
		return false, err
	}
	var waiting []string
	for _, c := range children {
		if c.Waiting(run, children) {
			waiting = append(waiting, c.Plan.Name)
		}
	}
	if r.store.setWaiting(run.PlanID, waiting) {
		log.Info("plans waiting for the person", "children", waiting)
		r.opts.Changed(run.ProjectID) // the project's stream sends it again
	}
	actions := NextParent(ParentFacts{Run: run, ProjectOpen: project.Open, Children: children})
	if len(actions) == 0 {
		return false, nil
	}
	a := actions[0]
	log.Info("plan step", "action", a.Kind, "child", a.Plan)
	defer r.opts.Changed(run.ProjectID)
	switch a.Kind {
	case ActionStartPlan:
		r.startChild(ctx, run, a.Plan)
		return true, nil
	case ActionCancelPlan:
		child, err := r.store.GetByName(ctx, run.ProjectID, a.Plan)
		if err != nil {
			return false, err
		}
		return true, r.Cancel(ctx, child.ID)
	case ActionClose:
		return true, r.store.Close(ctx, run.PlanID)
	case ActionFinish:
		return true, r.store.Finish(ctx, run.PlanID)
	}
	return false, errors.New("no step of a plan of plans does " + string(a.Kind))
}

// startChild starts the parent's child called name from the default branch; one
// that fails is left for the person, so that it is not tried again at each step.
func (r *Runner) startChild(ctx context.Context, parent Run, name string) {
	child, err := r.store.GetByName(ctx, parent.ProjectID, name)
	if err == nil {
		_, err = r.startPlan(ctx, child, 0)
	}
	if err != nil {
		logging.FromContext(ctx).Error("plan not started by its parent: it waits for the person", "child", name, "err", err)
		r.mu.Lock()
		r.failedChildren[child.ID] = true
		r.mu.Unlock()
		return
	}
	logging.FromContext(ctx).Info("plan started by its parent", "child", name, "child_id", child.ID)
}

// startFailed reports whether the plan with id's parent failed to start it since
// the daemon started.
func (r *Runner) startFailed(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failedChildren[id]
}

// startParent runs the plan of plans p, once every plan it lists, each a draft as a
// plan of plans lists only drafts, has a branch that can be made, so that the run
// does not stop halfway on one in the way.
func (r *Runner) startParent(ctx context.Context, p Plan) (Plan, error) {
	project, err := r.opts.Project(ctx, p.ProjectID)
	if err != nil {
		return Plan{}, err
	}
	base, err := git.DefaultBranch(ctx, project.Clone)
	if err != nil {
		return Plan{}, err
	}
	if base == "" {
		return Plan{}, errors.New("the project has no default branch for the plans to start from")
	}
	for _, name := range p.Plans {
		child, err := r.store.GetByName(ctx, p.ProjectID, name)
		if err != nil {
			return Plan{}, err
		}
		branch := BranchPrefix + child.Name
		there, err := git.BranchExists(ctx, project.Clone, branch)
		if err != nil {
			return Plan{}, err
		}
		if there {
			return Plan{}, &BranchExistsError{Branch: branch, Err: errors.New("it is there")}
		}
	}
	if _, err := r.store.Start(ctx, p.ID, base, "", 0); err != nil {
		return Plan{}, err
	}
	r.Wake(p.ID)
	return r.store.Get(ctx, p.ID)
}

// wakeParent wakes the parent of the plan with id, if it has one, as a child done
// may start the next.
func (r *Runner) wakeParent(ctx context.Context, id string) {
	p, err := r.store.Get(ctx, id)
	if err != nil || p.Parent == "" {
		return
	}
	parent, err := r.store.GetByName(ctx, p.ProjectID, p.Parent)
	if err != nil {
		logging.FromContext(ctx).Info("plan's parent not woken", "err", err)
		return
	}
	r.Wake(parent.ID)
}

// WakeParents wakes the plans of plans going on in the project with projectID, as
// a landing through wdx may have put a child's work in the default branch.
func (r *Runner) WakeParents(ctx context.Context, projectID string) {
	ids, err := r.store.goingParents(ctx, projectID)
	if err != nil {
		logging.FromContext(ctx).Info("plans of plans not woken", "err", err)
		return
	}
	for _, id := range ids {
		r.Wake(id)
	}
}

// children returns the plans the plan of plans p lists.
func (s *Store) children(ctx context.Context, p Plan) ([]Plan, error) {
	list := make([]Plan, 0, len(p.Plans))
	for _, name := range p.Plans {
		c, err := s.GetByName(ctx, p.ProjectID, name)
		if err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, nil
}

// goingParents returns the ids of the project's plans of plans running or paused.
func (s *Store) goingParents(ctx context.Context, projectID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.plan_id FROM plan_runs r JOIN docs d ON d.id = r.plan_id
		WHERE d.project_id = ? AND r.branch = '' AND r.state IN (?, ?)`, projectID, StateRunning, StatePaused)
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

// PullRequest is what the runner reads of a plan's PR, through gh.
type PullRequest struct {
	Number      int
	MergeCommit string // the commit its merge made, "" until it is merged
}

// WaitsError is returned for running a plan its parent lists before the plans it
// waits for are done, or before their work is in the default branch, Base.
type WaitsError struct {
	Plan      Plan
	Base      string
	NotDone   []Plan
	NotLanded []Plan // done, their work not in Base, nor merged as GitHub says
	NotPulled []Plan // merged on GitHub, their merge not in the local Base
}

func (e *WaitsError) Error() string {
	if len(e.NotDone) > 0 {
		return fmt.Sprintf("plan %s waits for %s, not done", e.Plan.Name, planNames(e.NotDone))
	}
	if len(e.NotLanded) > 0 {
		return fmt.Sprintf("plan %s waits for the work of %s, not in %s", e.Plan.Name, planNames(e.NotLanded), e.Base)
	}
	return fmt.Sprintf("plan %s waits for the work of %s, merged on GitHub, not yet in the local %s", e.Plan.Name, planNames(e.NotPulled), e.Base)
}

// planNames is the plans' names, for a sentence.
func planNames(list []Plan) string {
	names := make([]string, len(list))
	for i, p := range list {
		names[i] = p.Name
	}
	return strings.Join(names, ", ")
}

// answer starts child, a plan the running plan of plans parent lists, as the
// person asked: once the plans it waits for are done, and their work in the
// default branch, as git shows it or, for the rest, GitHub says their PR was merged
// with its merge commit in the local default branch.
func (r *Runner) answer(ctx context.Context, child, parent Plan, parallel int) (Plan, error) {
	log := logging.FromContext(ctx).With("child", child.Name)
	children, run, err := r.Children(ctx, parent.ID)
	if err != nil {
		return Plan{}, err
	}
	project, err := r.opts.Project(ctx, run.ProjectID)
	if err != nil {
		return Plan{}, err
	}
	i := slices.IndexFunc(children, func(c ChildRun) bool { return c.Plan.ID == child.ID })
	if i < 0 {
		return Plan{}, fmt.Errorf("plan %s does not list %s in its run", parent.Name, child.Name)
	}
	waits := &WaitsError{Plan: child, Base: run.Base}
	for _, a := range children[i].After {
		j := slices.IndexFunc(children, func(c ChildRun) bool { return c.Plan.Name == a })
		if j < 0 {
			return Plan{}, fmt.Errorf("plan %s lists no %s", parent.Name, a)
		}
		before := children[j]
		switch {
		case before.Plan.State != StateDone:
			waits.NotDone = append(waits.NotDone, before.Plan)
		case before.Landed:
		default:
			merged, err := r.mergedIn(ctx, project.Clone, before.Plan.Name, run.Base)
			if err != nil {
				return Plan{}, err
			}
			switch merged {
			case mergedNot:
				waits.NotLanded = append(waits.NotLanded, before.Plan)
			case mergedNotPulled:
				waits.NotPulled = append(waits.NotPulled, before.Plan)
			}
		}
	}
	if len(waits.NotDone)+len(waits.NotLanded)+len(waits.NotPulled) > 0 {
		log.Info("plan not started: it waits", "err", waits)
		return Plan{}, waits
	}
	log.Info("plan started as the person asked")
	p, err := r.startPlan(ctx, child, parallel)
	if err == nil {
		r.Wake(parent.ID)
	}
	return p, err
}

// How a plan's PR left its work.
const (
	mergedNot       = iota // not merged, or no PR
	mergedNotPulled        // merged on GitHub, its merge commit not in the local base
	mergedIn               // merged, and its merge commit in the local base
)

// mergedIn says what GitHub says of the PR of the plan called name, and whether its
// merge is in the local base.
func (r *Runner) mergedIn(ctx context.Context, clone, name, base string) (int, error) {
	log := logging.FromContext(ctx).With("child", name)
	if r.opts.PullRequest == nil {
		return mergedNot, nil
	}
	pr, found, err := r.opts.PullRequest(ctx, clone, BranchPrefix+name)
	if err != nil {
		return mergedNot, err
	}
	if !found || pr.MergeCommit == "" {
		log.Info("plan's PR read", "found", found, "number", pr.Number, "merged", false)
		return mergedNot, nil
	}
	// A merge commit the clone has not fetched is not in base either.
	in, err := git.IsAncestor(ctx, clone, pr.MergeCommit, "refs/heads/"+base)
	if err != nil {
		return mergedNot, err
	}
	log.Info("plan's PR read", "number", pr.Number, "merged", true, "commit", pr.MergeCommit, "pulled", in)
	if in {
		return mergedIn, nil
	}
	return mergedNotPulled, nil
}
