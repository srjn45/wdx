package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/srjn45/wdx/internal/agent"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/doc"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/plan"
	"github.com/srjn45/wdx/internal/project"
)

func (s *server) ListPlans(ctx context.Context, req api.ListPlansRequestObject) (api.ListPlansResponseObject, error) {
	p, err := s.planProject(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	list, err := s.plans.List(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	if !deref(req.Params.Archived) {
		list = withoutArchived(list)
	}
	return api.ListPlans200JSONResponse{Plans: planViews(list)}, nil
}

// withoutArchived is list less the archived plans, and the drafts an archived
// plan of plans lists, which went with it.
func withoutArchived(list []plan.Plan) []plan.Plan {
	gone := map[string]bool{}
	for _, p := range list {
		if !p.ArchivedAt.IsZero() && p.Plans != nil {
			gone[strings.ToLower(p.Name)] = true
		}
	}
	return slices.DeleteFunc(list, func(p plan.Plan) bool {
		return !p.ArchivedAt.IsZero() || (p.State == plan.StateDraft && gone[strings.ToLower(p.Parent)])
	})
}

func (s *server) CopyPlan(ctx context.Context, req api.CopyPlanRequestObject) (api.CopyPlanResponseObject, error) {
	p, err := s.findPlan(ctx, req.Plan, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	saved, err := s.plans.Copy(ctx, p.ID, req.Body.Name, req.Body.Folder)
	if err != nil {
		return nil, err
	}
	dropped := droppedViews(saved.Dropped)
	return api.CopyPlan201JSONResponse{Result: api.ResultCreated, Plan: planView(saved.Plan), Dropped: &dropped, Moved: &saved.Moved}, nil
}

func (s *server) ArchivePlan(ctx context.Context, req api.ArchivePlanRequestObject) (api.ArchivePlanResponseObject, error) {
	p, err := s.findPlan(ctx, req.Plan, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	changed, err := s.plans.Archive(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	if p, err = s.plans.Get(ctx, p.ID); err != nil {
		return nil, err
	}
	return api.ArchivePlan200JSONResponse{Result: changedResult(changed, api.ResultArchived), Plan: planView(p)}, nil
}

func (s *server) UnarchivePlan(ctx context.Context, req api.UnarchivePlanRequestObject) (api.UnarchivePlanResponseObject, error) {
	p, err := s.findPlan(ctx, req.Plan, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	changed, err := s.plans.Unarchive(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	if p, err = s.plans.Get(ctx, p.ID); err != nil {
		return nil, err
	}
	return api.UnarchivePlan200JSONResponse{Result: changedResult(changed, api.ResultUnarchived), Plan: planView(p)}, nil
}

func (s *server) CreatePlan(ctx context.Context, req api.CreatePlanRequestObject) (api.CreatePlanResponseObject, error) {
	b := req.Body
	p, err := s.planProject(ctx, b.Project, b.Dir)
	if err != nil {
		return nil, err
	}
	saved, err := s.plans.Create(ctx, p.ID, b.Name, deref(b.Folder), []byte(b.Content))
	if err != nil {
		return nil, err
	}
	dropped := droppedViews(saved.Dropped)
	return api.CreatePlan201JSONResponse{Result: api.ResultCreated, Plan: planView(saved.Plan), Dropped: &dropped, Moved: &saved.Moved}, nil
}

func (s *server) GetPlan(ctx context.Context, req api.GetPlanRequestObject) (api.GetPlanResponseObject, error) {
	p, err := s.findPlan(ctx, req.Plan, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	content, r, def, err := s.plans.Content(ctx, p.ID, int(deref(req.Params.Revision)))
	if err != nil {
		return nil, err
	}
	run, ran, err := s.plans.Run(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	var runRef *plan.Run
	if ran {
		runRef = &run
		p = p.AsOf(run)
	}
	names, err := s.agentNames(ctx, runRef)
	if err != nil {
		return nil, err
	}
	var children []api.PlanChild
	if def.Plans != nil {
		if children, err = s.childViews(ctx, p.ID); err != nil {
			return nil, err
		}
		// Counted from the same read as its plans, so that the two agree when one
		// finished between the reads.
		p.PlansDone, p.PlansWaiting = 0, nil
		for _, c := range children {
			if c.State == api.PlanStateDone {
				p.PlansDone++
			}
			if c.Waiting {
				p.PlansWaiting = append(p.PlansWaiting, c.Id)
			}
		}
	}
	logging.FromContext(ctx).Info("plan read", "plan_id", p.ID, "name", p.Name, "revision", r.Number, "size", r.Size, "state", p.State)
	return api.GetPlan200JSONResponse{Plan: planView(p), Revision: int64(r.Number), Content: string(content), Definition: definitionView(def, runRef, names, children), Run: runView(runRef, p)}, nil
}

// childViews returns the plans the plan of plans with id lists, as its run sees
// them, by name.
func (s *server) childViews(ctx context.Context, id string) ([]api.PlanChild, error) {
	children, run, err := s.runner.Children(ctx, id)
	if err != nil {
		return nil, err
	}
	views := make([]api.PlanChild, 0, len(children))
	for _, c := range children {
		v := api.PlanChild{Id: c.Plan.Name, After: c.After, State: api.PlanState(c.Plan.State), Tasks: int64(c.Plan.Tasks), Waiting: c.Waiting(run, children)}
		if c.Plan.State != plan.StateDraft {
			done := int64(c.Plan.TasksDone)
			v.TasksDone = &done
		}
		views = append(views, v)
	}
	return views, nil
}

func (s *server) RunPlan(ctx context.Context, req api.RunPlanRequestObject) (api.RunPlanResponseObject, error) {
	p, err := s.findPlan(ctx, req.Plan, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	proj, err := s.projects.GetByID(ctx, p.ProjectID)
	if err != nil {
		return nil, err
	}
	if proj.State == project.StateClosed {
		return nil, &projectClosedError{project: proj}
	}
	if p, err = s.runner.Start(ctx, p.ID, int(deref(req.Body.Parallel))); err != nil {
		return nil, err
	}
	return api.RunPlan200JSONResponse{Result: api.ResultStarted, Plan: planView(p)}, nil
}

func (s *server) FinishPlanTask(ctx context.Context, req api.FinishPlanTaskRequestObject) (api.FinishPlanTaskResponseObject, error) {
	if strings.TrimSpace(req.Body.Summary) == "" {
		return nil, &requestError{err: errors.New("summary is blank: the tasks after this one are told it")}
	}
	p, err := s.findPlan(ctx, req.Plan, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if _, err := s.runner.TaskDone(ctx, p.ID, req.Task, req.Body.Summary); err != nil {
		return nil, err
	}
	t, err := s.taskResult(ctx, p.ID, req.Task)
	if err != nil {
		return nil, err
	}
	return api.FinishPlanTask200JSONResponse{Result: api.ResultLanding, Task: t}, nil
}

// taskResult is the task of the plan's run, as a response shows it.
func (s *server) taskResult(ctx context.Context, planID, taskID string) (api.PlanTask, error) {
	run, _, err := s.plans.Run(ctx, planID)
	if err != nil {
		return api.PlanTask{}, err
	}
	names, err := s.agentNames(ctx, &run)
	if err != nil {
		return api.PlanTask{}, err
	}
	tasks := *definitionView(run.Definition, &run, names, nil).Tasks
	i := slices.IndexFunc(tasks, func(t api.PlanTask) bool { return t.Id == taskID })
	return tasks[i], nil
}

func (s *server) RetryPlanTask(ctx context.Context, req api.RetryPlanTaskRequestObject) (api.RetryPlanTaskResponseObject, error) {
	p, err := s.findPlan(ctx, req.Plan, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if _, err := s.runner.RetryTask(ctx, p.ID, req.Task, deref(req.Body.Fresh)); err != nil {
		return nil, err
	}
	t, err := s.taskResult(ctx, p.ID, req.Task)
	if err != nil {
		return nil, err
	}
	return api.RetryPlanTask200JSONResponse{Result: api.ResultRetried, Task: t}, nil
}

func (s *server) LandPlanTask(ctx context.Context, req api.LandPlanTaskRequestObject) (api.LandPlanTaskResponseObject, error) {
	p, err := s.findPlan(ctx, req.Plan, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if _, err := s.runner.LandTask(ctx, p.ID, req.Task); err != nil {
		return nil, err
	}
	t, err := s.taskResult(ctx, p.ID, req.Task)
	if err != nil {
		return nil, err
	}
	return api.LandPlanTask200JSONResponse{Result: api.ResultLanding, Task: t}, nil
}

func (s *server) SkipPlanTask(ctx context.Context, req api.SkipPlanTaskRequestObject) (api.SkipPlanTaskResponseObject, error) {
	p, err := s.findPlan(ctx, req.Plan, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if _, err := s.runner.SkipTask(ctx, p.ID, req.Task); err != nil {
		return nil, err
	}
	t, err := s.taskResult(ctx, p.ID, req.Task)
	if err != nil {
		return nil, err
	}
	return api.SkipPlanTask200JSONResponse{Result: api.ResultSkipped, Task: t}, nil
}

func (s *server) PausePlan(ctx context.Context, req api.PausePlanRequestObject) (api.PausePlanResponseObject, error) {
	p, err := s.findPlan(ctx, req.Plan, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	changed, err := s.runner.Pause(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	if p, err = s.plans.Get(ctx, p.ID); err != nil {
		return nil, err
	}
	return api.PausePlan200JSONResponse{Result: changedResult(changed, api.ResultPaused), Plan: planView(p)}, nil
}

func (s *server) ResumePlan(ctx context.Context, req api.ResumePlanRequestObject) (api.ResumePlanResponseObject, error) {
	p, err := s.findPlan(ctx, req.Plan, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	proj, err := s.projects.GetByID(ctx, p.ProjectID)
	if err != nil {
		return nil, err
	}
	if proj.State == project.StateClosed {
		return nil, &projectClosedError{project: proj}
	}
	changed, err := s.runner.Resume(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	if p, err = s.plans.Get(ctx, p.ID); err != nil {
		return nil, err
	}
	return api.ResumePlan200JSONResponse{Result: changedResult(changed, api.ResultResumed), Plan: planView(p)}, nil
}

func (s *server) CancelPlan(ctx context.Context, req api.CancelPlanRequestObject) (api.CancelPlanResponseObject, error) {
	p, err := s.findPlan(ctx, req.Plan, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if err := s.runner.Cancel(ctx, p.ID); err != nil {
		return nil, err
	}
	if p, err = s.plans.Get(ctx, p.ID); err != nil {
		return nil, err
	}
	return api.CancelPlan200JSONResponse{Result: api.ResultCancelled, Plan: planView(p)}, nil
}

// changedResult is result when something changed, else unchanged.
func changedResult(changed bool, result api.Result) api.Result {
	if changed {
		return result
	}
	return api.ResultUnchanged
}

// agentNames returns the names of the agents of run's tasks, by id. One removed
// since run was read, as a plan that is over removes its agents, is left out, as
// the task has none now.
func (s *server) agentNames(ctx context.Context, run *plan.Run) (map[string]string, error) {
	names := map[string]string{}
	if run == nil {
		return names, nil
	}
	for _, t := range run.Tasks {
		if t.AgentID == "" {
			continue
		}
		a, err := s.agents.Store().Get(ctx, t.AgentID)
		if errors.Is(err, agent.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		names[t.AgentID] = a.Terminal.Name
	}
	return names, nil
}

func (s *server) UpdatePlan(ctx context.Context, req api.UpdatePlanRequestObject) (api.UpdatePlanResponseObject, error) {
	name, folder, parallel := req.Body.Name, req.Body.Folder, req.Body.Parallel
	if (name == nil) == (folder == nil && parallel == nil) {
		return nil, &requestError{err: errors.New("give name, or folder, parallel or both")}
	}
	p, err := s.findPlan(ctx, req.Plan, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if name != nil {
		// Renaming to the name it has, case and all, writes nothing.
		if *name == p.Name {
			return api.UpdatePlan200JSONResponse{Result: api.ResultUnchanged, Plan: planView(p), Moved: &[]string{}}, nil
		}
		if p, err = s.plans.Rename(ctx, p.ID, *name); err != nil {
			return nil, err
		}
		return api.UpdatePlan200JSONResponse{Result: api.ResultUpdated, Plan: planView(p), Moved: &[]string{}}, nil
	}
	// The folder is checked before parallel changes anything, and parallel is set
	// before the folder, so that a request refused changes neither.
	if folder != nil {
		if _, err := doc.CleanFolder(*folder); err != nil {
			return nil, err
		}
	}
	changed, moved := false, []string{}
	if parallel != nil {
		if changed, err = s.runner.SetParallel(ctx, p.ID, *parallel); err != nil {
			return nil, err
		}
	}
	if folder != nil {
		var folderChanged bool
		if _, folderChanged, moved, err = s.plans.Move(ctx, p.ID, *folder); err != nil {
			return nil, err
		}
		changed = changed || folderChanged
	}
	if p, err = s.plans.Get(ctx, p.ID); err != nil {
		return nil, err
	}
	return api.UpdatePlan200JSONResponse{Result: changedResult(changed, api.ResultUpdated), Plan: planView(p), Moved: &moved}, nil
}

func (s *server) UpdatePlanContent(ctx context.Context, req api.UpdatePlanContentRequestObject) (api.UpdatePlanContentResponseObject, error) {
	p, err := s.findPlan(ctx, req.Plan, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	saved, err := s.plans.Update(ctx, p.ID, int(req.Body.Base), []byte(req.Body.Content))
	if err != nil {
		return nil, err
	}
	result := api.ResultUnchanged
	if saved.Changed {
		result = api.ResultUpdated
	}
	dropped := droppedViews(saved.Dropped)
	return api.UpdatePlanContent200JSONResponse{Result: result, Plan: planView(saved.Plan), Dropped: &dropped, Moved: &saved.Moved}, nil
}

func (s *server) ListPlanRevisions(ctx context.Context, req api.ListPlanRevisionsRequestObject) (api.ListPlanRevisionsResponseObject, error) {
	p, err := s.findPlan(ctx, req.Plan, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	list, err := s.plans.Revisions(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	views := make([]api.DocRevision, 0, len(list))
	for _, r := range list {
		views = append(views, api.DocRevision{Number: int64(r.Number), Size: int64(r.Size), CreatedAt: r.CreatedAt.UTC()})
	}
	return api.ListPlanRevisions200JSONResponse{Revisions: views}, nil
}

func (s *server) DeletePlan(ctx context.Context, req api.DeletePlanRequestObject) (api.DeletePlanResponseObject, error) {
	p, err := s.findPlan(ctx, req.Plan, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if p, err = s.plans.Remove(ctx, p.ID); err != nil {
		return nil, err
	}
	return api.DeletePlan200JSONResponse{Result: api.ResultRemoved, Plan: planView(p)}, nil
}

// planProject is the project ref names, or the one whose worktree holds dir: a
// plan's project is never left to be guessed.
func (s *server) planProject(ctx context.Context, ref, dir *string) (project.Project, error) {
	if err := exclusive(ref, dir); err != nil {
		return project.Project{}, err
	}
	if ref == nil && dir == nil {
		return project.Project{}, &requestError{err: errors.New("give project or dir: plans belong to a project")}
	}
	return s.projectOf(ctx, ref, dir)
}

// findPlan returns the plan ref names: "id:" and its id, or its name in the
// project named by projectRef or holding dir. A project given with an id must be
// the plan's.
func (s *server) findPlan(ctx context.Context, ref string, projectRef, dir *string) (plan.Plan, error) {
	if err := exclusive(projectRef, dir); err != nil {
		return plan.Plan{}, err
	}
	id, byID := strings.CutPrefix(ref, idPrefix)
	if !byID && projectRef == nil && dir == nil {
		return plan.Plan{}, &requestError{err: fmt.Errorf("plan name %q needs project or dir: names are unique only within a project", ref)}
	}
	var p project.Project
	if projectRef != nil || dir != nil {
		var err error
		if p, err = s.projectOf(ctx, projectRef, dir); err != nil {
			return plan.Plan{}, err
		}
	}
	if !byID {
		return s.plans.GetByName(ctx, p.ID, ref)
	}
	found, err := s.plans.Get(ctx, id)
	if err == nil && p.ID != "" && found.ProjectID != p.ID {
		return plan.Plan{}, fmt.Errorf("%w: %q is not in project %s", plan.ErrNotFound, id, p.Name)
	}
	return found, err
}

func planViews(list []plan.Plan) []api.Plan {
	views := make([]api.Plan, 0, len(list))
	for _, p := range list {
		views = append(views, planView(p))
	}
	return views
}

func planView(p plan.Plan) api.Plan {
	v := api.Plan{
		Id:          p.ID,
		Name:        p.Name,
		ProjectId:   p.ProjectID,
		ProjectName: p.ProjectName,
		Folder:      p.Folder,
		State:       api.PlanState(p.State),
		Revision:    int64(p.Revision),
		Size:        int64(p.Size),
		CreatedAt:   p.CreatedAt.UTC(),
		UpdatedAt:   p.UpdatedAt.UTC(),
	}
	if p.Plans != nil {
		v.Plans = &p.Plans
		if p.State != plan.StateDraft {
			done := int64(p.PlansDone)
			v.PlansDone = &done
		}
		waiting := append([]string{}, p.PlansWaiting...)
		v.PlansWaiting = &waiting
	} else {
		tasks := int64(p.Tasks)
		v.Tasks = &tasks
	}
	if p.Parent != "" {
		v.Parent = &p.Parent
	}
	v.Slots = make([]api.PlanSlot, 0, len(p.Slots))
	for _, sl := range p.Slots {
		v.Slots = append(v.Slots, api.PlanSlot{Slot: sl.Number, Path: sl.Path, Task: textOrNull(sl.Task)})
	}
	if !p.ArchivedAt.IsZero() {
		at := p.ArchivedAt.UTC()
		v.ArchivedAt = &at
	}
	if p.State != plan.StateDraft && p.Plans == nil {
		done, failed := int64(p.TasksDone), int64(p.TasksFailed)
		v.TasksDone, v.TasksFailed = &done, &failed
	}
	return v
}

// runView is r as its plan p shows it: paused while its project is closed.
func runView(r *plan.Run, p plan.Plan) *api.PlanRun {
	if r == nil {
		return nil
	}
	v := &api.PlanRun{State: api.PlanState(p.State), Revision: int64(r.Revision), Base: r.Base, Branch: r.Branch, StartedAt: r.StartedAt.UTC(), FinishedAt: optionalTime(r.FinishedAt)}
	if r.Parallel != 0 {
		v.Parallel = &r.Parallel
	}
	if p.PausedReason != "" {
		reason := api.PlanRunPausedReason(p.PausedReason)
		v.PausedReason = &reason
	}
	return v
}

// definitionView is d with each task as run has it, or pending when run is nil;
// names are the run's agents' names, by id. A plan of plans' are its children's
// views, by name.
func definitionView(d plan.Definition, run *plan.Run, names map[string]string, children []api.PlanChild) api.PlanDefinition {
	v := api.PlanDefinition{Goal: d.Goal, Constraints: d.Constraints}
	if v.Constraints == nil {
		v.Constraints = []string{}
	}
	if d.Plans != nil {
		v.Plans = &children
	} else {
		states, slots := map[string]plan.TaskRun{}, map[int]string{}
		if run != nil {
			for _, t := range run.Tasks {
				states[t.ID] = t
			}
			for _, sl := range run.Slots {
				slots[sl.Number] = sl.Path
			}
		}
		tasks := make([]api.PlanTask, len(d.Tasks))
		for i, t := range d.Tasks {
			tasks[i] = taskView(t, states, slots, names)
		}
		v.Tasks = &tasks
	}
	return v
}

func taskView(t plan.Task, states map[string]plan.TaskRun, slots map[int]string, names map[string]string) api.PlanTask {
	r, ok := states[t.ID]
	if !ok {
		r = plan.TaskRun{ID: t.ID, State: plan.TaskPending}
	}
	v := api.PlanTask{Id: t.ID, After: t.After, Prompt: t.Prompt, State: api.PlanTaskState(r.State), WaitingFor: []string{},
		StartedAt: optionalTime(r.StartedAt), FinishedAt: optionalTime(r.FinishedAt)}
	if r.Conflicts != nil {
		v.Conflicts = &r.Conflicts
	}
	if path, ok := slots[r.Slot]; ok {
		v.SlotPath = &path
		if r.State == plan.TaskLandFailed && r.Failure == plan.FailureCheck {
			check := plan.CheckTerminal(path)
			v.CheckTerminal = &check
		}
	}
	for _, a := range t.After {
		if s := states[a].State; s != plan.TaskDone && s != plan.TaskSkipped {
			v.WaitingFor = append(v.WaitingFor, a)
		}
	}
	if r.Failure != "" {
		f := api.PlanTaskFailure(r.Failure)
		v.Failure = &f
	}
	if name, ok := names[r.AgentID]; ok {
		v.Agent = &api.PlanTaskAgent{Id: r.AgentID, Name: name}
	}
	if r.Summary != "" {
		v.Summary = &r.Summary
	}
	return v
}

func droppedViews(list []plan.Dropped) []api.PlanDropped {
	views := make([]api.PlanDropped, len(list))
	for i, d := range list {
		views[i] = api.PlanDropped{Entry: d.Entry, After: d.After, Via: d.Via}
	}
	return views
}

func problemViews(list []plan.Problem) []api.PlanProblem {
	views := make([]api.PlanProblem, len(list))
	for i, p := range list {
		views[i] = api.PlanProblem{Line: int64(p.Line), Message: p.Message}
	}
	return views
}
