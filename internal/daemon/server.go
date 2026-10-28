package daemon

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/project"
	"github.com/srjn45/wdx/internal/terminal"
)

// idPrefix marks a project reference in a path as an id rather than a name.
const idPrefix = "id:"

// server implements the API on the project store and the terminals'
// supervisor.
type server struct {
	store     *project.Store
	terminals *terminal.Supervisor
	self      api.Daemon
}

var _ api.StrictServerInterface = (*server)(nil)

func (s *server) GetVersion(context.Context, api.GetVersionRequestObject) (api.GetVersionResponseObject, error) {
	return api.GetVersion200JSONResponse{Name: identity.Name, Version: identity.Version, Commit: identity.Commit}, nil
}

func (s *server) GetDaemon(context.Context, api.GetDaemonRequestObject) (api.GetDaemonResponseObject, error) {
	return api.GetDaemon200JSONResponse(s.self), nil
}

func (s *server) ListProjects(ctx context.Context, req api.ListProjectsRequestObject) (api.ListProjectsResponseObject, error) {
	var state project.State
	if req.Params.State != nil {
		state = project.State(*req.Params.State)
	}
	list, err := s.store.List(ctx, state)
	if err != nil {
		return nil, err
	}
	r := api.ListProjects200JSONResponse{Projects: make([]api.ProjectSummary, 0, len(list))}
	for _, p := range list {
		r.Projects = append(r.Projects, api.ProjectSummary{
			Id:            p.ID,
			Name:          p.Name,
			Origin:        origin(p.OriginURL),
			State:         api.ProjectState(p.State),
			WorktreeCount: p.WorktreeCount,
			CreatedAt:     p.CreatedAt.UTC(),
			UpdatedAt:     p.UpdatedAt.UTC(),
		})
	}
	return r, nil
}

func (s *server) GetProject(ctx context.Context, req api.GetProjectRequestObject) (api.GetProjectResponseObject, error) {
	p, err := s.find(ctx, req.Project)
	if err != nil {
		return nil, err
	}
	status, err := s.store.DiskStatus(ctx, p)
	if err != nil {
		return nil, err
	}
	v := projectView(p)
	v.Orphaned = &status.Orphaned
	for i := range v.Worktrees {
		present := status.Present[v.Worktrees[i].Id]
		v.Worktrees[i].Present = &present
	}
	terminals, err := s.terminals.Store().List(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	views := terminalViews(terminals)
	v.Terminals = &views
	return api.GetProject200JSONResponse{Project: v}, nil
}

func (s *server) UpdateProject(ctx context.Context, req api.UpdateProjectRequestObject) (api.UpdateProjectResponseObject, error) {
	p, err := s.find(ctx, req.Project)
	if err != nil {
		return nil, err
	}
	if req.Body.State == api.ProjectStateClosed {
		stopped, err := s.stopTerminals(ctx, p, derefBool(req.Body.StopRunning))
		if err != nil {
			return nil, err
		}
		if p, err = s.store.Close(ctx, p.ID); err != nil {
			return nil, err
		}
		r := result(api.ResultClosed, p)
		r.Stopped = optionalTerminals(stopped)
		return api.UpdateProject200JSONResponse(r), nil
	}
	o, err := s.store.Open(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	r, err := s.opened(ctx, o)
	if err != nil {
		return nil, err
	}
	return api.UpdateProject200JSONResponse(r), nil
}

func (s *server) DeleteProject(ctx context.Context, req api.DeleteProjectRequestObject) (api.DeleteProjectResponseObject, error) {
	p, err := s.find(ctx, req.Project)
	if err != nil {
		return nil, err
	}
	r, err := s.remove(ctx, p, derefBool(req.Params.StopRunning))
	if err != nil {
		return nil, err
	}
	return api.DeleteProject200JSONResponse(r), nil
}

// remove stops the project's live terminals, refusing without stopRunning,
// then deletes it.
func (s *server) remove(ctx context.Context, p project.Project, stopRunning bool) (api.ProjectResult, error) {
	stopped, err := s.stopTerminals(ctx, p, stopRunning)
	if err != nil {
		return api.ProjectResult{}, err
	}
	if p, err = s.store.Remove(ctx, p.ID); err != nil {
		return api.ProjectResult{}, err
	}
	r := result(api.ResultRemoved, p)
	r.Stopped = optionalTerminals(stopped)
	return r, nil
}

func (s *server) RecloneProject(ctx context.Context, req api.RecloneProjectRequestObject) (api.RecloneProjectResponseObject, error) {
	p, err := s.find(ctx, req.Project)
	if err != nil {
		return nil, err
	}
	o, err := s.store.Reclone(ctx, p.ID, io.Discard)
	if err != nil {
		return nil, err
	}
	r, err := s.opened(ctx, o)
	if err != nil {
		return nil, err
	}
	return api.RecloneProject201JSONResponse(r), nil
}

func (s *server) CreateProject(ctx context.Context, req api.CreateProjectRequestObject) (api.CreateProjectResponseObject, error) {
	r, err := s.create(ctx, *req.Body)
	if err != nil {
		return nil, err
	}
	switch r.Result {
	case api.ResultCreated, api.ResultRegistered, api.ResultCloned:
		return api.CreateProject201JSONResponse(r), nil
	}
	return api.CreateProject200JSONResponse(r), nil
}

func (s *server) create(ctx context.Context, b api.CreateProject) (api.ProjectResult, error) {
	source, err := b.Source.ValueByDiscriminator()
	if err != nil {
		return api.ProjectResult{}, &requestError{err: err}
	}
	name := deref(b.Name)
	var o project.Opened
	var conflict error
	switch src := source.(type) {
	case api.NewSource:
		parent := deref(src.Path)
		if parent != "" {
			if err := absolute(parent); err != nil {
				return api.ProjectResult{}, err
			}
		}
		if o, err = s.store.Create(ctx, name, parent); err == nil {
			return result(api.ResultCreated, o.Project), nil
		}
		if b.OnConflict == nil || *b.OnConflict != api.CreateProjectOnConflictOpen {
			return api.ProjectResult{}, err
		}
		conflict = err
		o, err = s.openConflicting(ctx, conflict)
	case api.LocalSource:
		if err := absolute(src.Path); err != nil {
			return api.ProjectResult{}, err
		}
		o, err = s.store.OpenLocal(ctx, src.Path, name)
	case api.GitSource:
		o, err = s.store.OpenGit(ctx, src.Url, name, io.Discard)
	}

	r, err := s.settleMissing(ctx, o, err, b.OnMissingCheckout)
	if err != nil {
		return api.ProjectResult{}, err
	}
	if conflict != nil {
		p := problemOf(ctx, conflict)
		r.Conflict = &p
	}
	return r, nil
}

// openConflicting opens the project that creating a new one conflicted with,
// as on_conflict "open" asks.
func (s *server) openConflicting(ctx context.Context, conflict error) (project.Opened, error) {
	log := logging.FromContext(ctx)
	if taken, ok := errors.AsType[*project.NameTakenError](conflict); ok {
		log.Info("conflict answered", "answer", "open", "project_id", taken.Project.ID)
		return s.store.Open(ctx, taken.Project.ID)
	}
	if exists, ok := errors.AsType[*project.RepoExistsError](conflict); ok {
		log.Info("conflict answered", "answer", "open", "path", exists.Root)
		return s.store.OpenLocal(ctx, exists.Root, "")
	}
	return project.Opened{}, conflict
}

// settleMissing returns what opening a project led to: when none of its
// checkouts is left, it is cloned again or removed as choice asks.
func (s *server) settleMissing(ctx context.Context, o project.Opened, err error, choice *api.CreateProjectOnMissingCheckout) (api.ProjectResult, error) {
	missing, ok := errors.AsType[*project.CheckoutMissingError](err)
	if ok && choice != nil {
		logging.FromContext(ctx).Info("missing checkout answered", "answer", *choice, "project_id", missing.Project.ID)
		if *choice == api.CreateProjectOnMissingCheckoutRemove {
			return s.remove(ctx, missing.Project, false)
		}
		o, err = s.store.Reclone(ctx, missing.Project.ID, io.Discard)
	}
	if err != nil {
		return api.ProjectResult{}, err
	}
	return s.opened(ctx, o)
}

// find returns the project ref names: "id:" and its id, or its name.
func (s *server) find(ctx context.Context, ref string) (project.Project, error) {
	if id, ok := strings.CutPrefix(ref, idPrefix); ok {
		return s.store.GetByID(ctx, id)
	}
	return s.store.GetByName(ctx, ref)
}

// absolute fails for a relative path: the daemon's working directory means
// nothing to the client.
func absolute(path string) error {
	if !filepath.IsAbs(path) {
		return &project.InvalidPathError{Path: path, Reason: "not absolute"}
	}
	return nil
}

func result(r api.Result, p project.Project) api.ProjectResult {
	return api.ProjectResult{Result: r, Project: projectView(p)}
}

// opened relaunches the terminals closing the project stopped and returns
// what opening it led to.
func (s *server) opened(ctx context.Context, o project.Opened) (api.ProjectResult, error) {
	started, err := s.terminals.StartProject(ctx, o.ID)
	if err != nil {
		return api.ProjectResult{}, err
	}
	r := openedResult(o)
	r.Started = optionalTerminals(started)
	return r, nil
}

func openedResult(o project.Opened) api.ProjectResult {
	r := api.ResultOpened
	if o.Cloned {
		r = api.ResultCloned
	} else if o.Registered {
		r = api.ResultRegistered
	}
	res := result(r, o.Project)
	if len(o.Dropped) > 0 {
		dropped := worktreeViews(o.Dropped)
		res.Dropped = &dropped
	}
	return res
}

func projectView(p project.Project) api.Project {
	return api.Project{
		Id:        p.ID,
		Name:      p.Name,
		Origin:    origin(p.OriginURL),
		State:     api.ProjectState(p.State),
		Worktrees: worktreeViews(p.Worktrees),
		CreatedAt: p.CreatedAt.UTC(),
		UpdatedAt: p.UpdatedAt.UTC(),
	}
}

func worktreeViews(wts []project.Worktree) []api.Worktree {
	views := make([]api.Worktree, 0, len(wts))
	for _, w := range wts {
		views = append(views, api.Worktree{Id: w.ID, Path: w.Path, CreatedAt: w.CreatedAt.UTC()})
	}
	return views
}

func origin(url string) *string {
	if url == "" {
		return nil
	}
	return &url
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
