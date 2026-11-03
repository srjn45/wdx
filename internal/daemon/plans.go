package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/srjn45/wdx/internal/api"
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
	return api.ListPlans200JSONResponse{Plans: planViews(list)}, nil
}

func (s *server) CreatePlan(ctx context.Context, req api.CreatePlanRequestObject) (api.CreatePlanResponseObject, error) {
	b := req.Body
	p, err := s.planProject(ctx, b.Project, b.Dir)
	if err != nil {
		return nil, err
	}
	saved, err := s.plans.Create(ctx, p.ID, b.Name, []byte(b.Content))
	if err != nil {
		return nil, err
	}
	dropped := droppedViews(saved.Dropped)
	return api.CreatePlan201JSONResponse{Result: api.ResultCreated, Plan: planView(saved.Plan), Dropped: &dropped}, nil
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
	logging.FromContext(ctx).Info("plan read", "plan_id", p.ID, "name", p.Name, "revision", r.Number, "size", r.Size)
	return api.GetPlan200JSONResponse{Plan: planView(p), Revision: int64(r.Number), Content: string(content), Definition: definitionView(def)}, nil
}

func (s *server) UpdatePlan(ctx context.Context, req api.UpdatePlanRequestObject) (api.UpdatePlanResponseObject, error) {
	p, err := s.findPlan(ctx, req.Plan, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	// Renaming to the name it has, case and all, writes nothing.
	if req.Body.Name == p.Name {
		return api.UpdatePlan200JSONResponse{Result: api.ResultUnchanged, Plan: planView(p)}, nil
	}
	if p, err = s.plans.Rename(ctx, p.ID, req.Body.Name); err != nil {
		return nil, err
	}
	return api.UpdatePlan200JSONResponse{Result: api.ResultUpdated, Plan: planView(p)}, nil
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
	return api.UpdatePlanContent200JSONResponse{Result: result, Plan: planView(saved.Plan), Dropped: &dropped}, nil
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
		State:       api.PlanState(p.State),
		Revision:    int64(p.Revision),
		Size:        int64(p.Size),
		CreatedAt:   p.CreatedAt.UTC(),
		UpdatedAt:   p.UpdatedAt.UTC(),
	}
	if p.Plans != nil {
		v.Plans = &p.Plans
	} else {
		tasks := int64(p.Tasks)
		v.Tasks = &tasks
	}
	if p.Parent != "" {
		v.Parent = &p.Parent
	}
	return v
}

func definitionView(d plan.Definition) api.PlanDefinition {
	v := api.PlanDefinition{Goal: d.Goal, Constraints: d.Constraints}
	if v.Constraints == nil {
		v.Constraints = []string{}
	}
	if d.Plans != nil {
		children := make([]api.PlanChild, len(d.Plans))
		for i, c := range d.Plans {
			children[i] = api.PlanChild{Id: c.ID, After: c.After}
		}
		v.Plans = &children
	} else {
		tasks := make([]api.PlanTask, len(d.Tasks))
		for i, t := range d.Tasks {
			tasks[i] = api.PlanTask{Id: t.ID, After: t.After, Prompt: t.Prompt}
		}
		v.Tasks = &tasks
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
