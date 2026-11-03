package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/srjn45/wdx/internal/agent"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/project"
	"github.com/srjn45/wdx/internal/terminal"
)

// idPrefix marks a project reference in a path as an id rather than a name.
const idPrefix = "id:"

func (s *server) ListProjects(ctx context.Context, req api.ListProjectsRequestObject) (api.ListProjectsResponseObject, error) {
	var state project.State
	if req.Params.State != nil {
		state = project.State(*req.Params.State)
	}
	list, err := s.projects.List(ctx, state)
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
	v, err := s.detail(ctx, p)
	if err != nil {
		return nil, err
	}
	return api.GetProject200JSONResponse{Project: v}, nil
}

// detail is p as getProject returns it: which of its checkouts are on disk,
// its terminals and agents in every state, its docs and its plans.
func (s *server) detail(ctx context.Context, p project.Project) (api.Project, error) {
	status, err := s.projects.DiskStatus(ctx, p)
	if err != nil {
		return api.Project{}, err
	}
	v := projectView(p)
	v.Orphaned = &status.Orphaned
	for i := range v.Worktrees {
		present := status.Present[v.Worktrees[i].Id]
		v.Worktrees[i].Present = &present
	}
	terminals, err := s.terminals.Store().List(ctx, p.ID)
	if err != nil {
		return api.Project{}, err
	}
	views := terminalViews(terminals)
	v.Terminals = &views
	agents, err := s.agents.Store().List(ctx, p.ID)
	if err != nil {
		return api.Project{}, err
	}
	tree := agentTree(agents)
	v.Agents = &tree
	editors, err := s.terminals.Store().Editors(ctx, p.ID)
	if err != nil {
		return api.Project{}, err
	}
	editorList := editorViews(p, editors)
	v.Editors = &editorList
	docs, err := s.docs.List(ctx, p.ID, nil, "")
	if err != nil {
		return api.Project{}, err
	}
	docList := docViews(docs)
	v.Docs = &docList
	plans, err := s.plans.List(ctx, p.ID)
	if err != nil {
		return api.Project{}, err
	}
	planList := planViews(plans)
	v.Plans = &planList
	return v, nil
}

func (s *server) UpdateProject(ctx context.Context, req api.UpdateProjectRequestObject) (api.UpdateProjectResponseObject, error) {
	p, err := s.find(ctx, req.Project)
	if err != nil {
		return nil, err
	}
	if req.Body.State == api.ProjectStateClosed {
		stopped, err := s.stopRunning(ctx, p, deref(req.Body.StopRunning))
		if err != nil {
			return nil, err
		}
		if p, err = s.projects.Close(ctx, p.ID); err != nil {
			return nil, err
		}
		r := result(api.ResultClosed, p)
		stopped.into(&r, p)
		return api.UpdateProject200JSONResponse(r), nil
	}
	o, err := s.projects.Open(ctx, p.ID)
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
	r, err := s.remove(ctx, p, deref(req.Params.StopRunning))
	if err != nil {
		return nil, err
	}
	return api.DeleteProject200JSONResponse(r), nil
}

// remove stops the project's live terminals and agents, refusing without
// stopRunning, then deletes it.
func (s *server) remove(ctx context.Context, p project.Project, stopRunning bool) (api.ProjectResult, error) {
	stopped, err := s.stopRunning(ctx, p, stopRunning)
	if err != nil {
		return api.ProjectResult{}, err
	}
	if p, err = s.projects.Remove(ctx, p.ID); err != nil {
		return api.ProjectResult{}, err
	}
	// Its terminals, agents, docs and plans went with it.
	s.docs.RemoveProjectCopies(ctx, p.Name)
	s.plans.RemoveProjectCopies(ctx, p.ID, p.Name)
	if err := s.terminals.Sweep(ctx); err != nil {
		logging.FromContext(ctx).Error("terminal directories not swept", "err", err)
	}
	if err := s.agents.SweepBackups(ctx); err != nil {
		logging.FromContext(ctx).Error("agent backups not swept", "err", err)
	}
	r := result(api.ResultRemoved, p)
	stopped.into(&r, p)
	return r, nil
}

func (s *server) RecloneProject(ctx context.Context, req api.RecloneProjectRequestObject) (api.RecloneProjectResponseObject, error) {
	p, err := s.find(ctx, req.Project)
	if err != nil {
		return nil, err
	}
	o, err := s.projects.Reclone(ctx, p.ID, io.Discard)
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
		if o, err = s.projects.Create(ctx, name, parent); err == nil {
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
		o, err = s.projects.OpenLocal(ctx, src.Path, name)
	case api.GitSource:
		o, err = s.projects.OpenGit(ctx, src.Url, name, io.Discard)
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
		return s.projects.Open(ctx, taken.Project.ID)
	}
	if exists, ok := errors.AsType[*project.RepoExistsError](conflict); ok {
		log.Info("conflict answered", "answer", "open", "path", exists.Root)
		return s.projects.OpenLocal(ctx, exists.Root, "")
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
		o, err = s.projects.Reclone(ctx, missing.Project.ID, io.Discard)
	}
	if err != nil {
		return api.ProjectResult{}, err
	}
	return s.opened(ctx, o)
}

// find returns the project ref names: "id:" and its id, or its name.
func (s *server) find(ctx context.Context, ref string) (project.Project, error) {
	if id, ok := strings.CutPrefix(ref, idPrefix); ok {
		return s.projects.GetByID(ctx, id)
	}
	return s.projects.GetByName(ctx, ref)
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

// opened relaunches the terminals and agents closing the project stopped and
// returns what opening it led to.
func (s *server) opened(ctx context.Context, o project.Opened) (api.ProjectResult, error) {
	rows, err := s.terminals.StartProject(ctx, o.ID)
	if err != nil {
		return api.ProjectResult{}, err
	}
	started, err := s.byKind(ctx, rows)
	if err != nil {
		return api.ProjectResult{}, err
	}
	r := openedResult(o)
	r.Started = optionalTerminals(started.terminals)
	r.StartedAgents = optionalAgents(started.agents)
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

func deref[T any](p *T) T {
	var v T
	if p != nil {
		v = *p
	}
	return v
}

// projectInUseError is returned when a project with live terminals or agents
// would be closed or removed without stopping them.
type projectInUseError struct {
	project project.Project
	rows
}

func (e *projectInUseError) Error() string {
	var names, what []string
	for _, t := range e.terminals {
		names = append(names, t.Name)
	}
	for _, a := range e.agents {
		names = append(names, a.Terminal.Name)
	}
	for _, t := range e.editors {
		names = append(names, editorView(e.project, t).Path)
	}
	for _, kind := range []struct {
		noun string
		n    int
	}{{"terminals", len(e.terminals)}, {"agents", len(e.agents)}, {"editors", len(e.editors)}} {
		if kind.n > 0 {
			what = append(what, kind.noun)
		}
	}
	if len(what) > 1 {
		what = []string{strings.Join(what[:len(what)-1], ", ") + " and " + what[len(what)-1]}
	}
	return fmt.Sprintf("project %s has %s running: %s", e.project.Name, strings.Join(what, ""), strings.Join(names, ", "))
}

// rows are a project's rows of every kind, as what they are.
type rows struct {
	terminals []terminal.Terminal
	agents    []agent.Agent
	editors   []terminal.Terminal
}

// stopped is what stopping a project's rows stopped.
type stopped struct {
	rows
	sessions []string // its agents' sessions outside their terminals
}

// into puts what was stopped in the project p into r.
func (st stopped) into(r *api.ProjectResult, p project.Project) {
	r.Stopped = optionalTerminals(st.terminals)
	r.StoppedAgents = optionalAgents(st.agents)
	r.StoppedSessions = optionalSessions(st.sessions)
	if len(st.editors) > 0 {
		views := editorViews(p, st.editors)
		r.StoppedEditors = &views
	}
}

// stopRunning stops the project's live terminals, agents and editors before it
// is closed or removed; without stop it refuses when there are any. The agents
// are read while their rows still exist: removing the project deletes them.
func (s *server) stopRunning(ctx context.Context, p project.Project, stop bool) (stopped, error) {
	live, err := s.terminals.Live(ctx, p.ID)
	if err != nil {
		return stopped{}, err
	}
	if len(live) > 0 && !stop {
		running, err := s.byKind(ctx, live)
		if err != nil {
			return stopped{}, err
		}
		return stopped{}, &projectInUseError{project: p, rows: running}
	}
	if !stop {
		working, err := s.agents.DetachedAtWork(ctx, p.ID)
		if err != nil {
			return stopped{}, err
		}
		if len(working) > 0 {
			return stopped{}, &projectInUseError{project: p, rows: rows{agents: working}}
		}
	}
	list, err := s.terminals.StopProject(ctx, p.ID)
	if err != nil {
		return stopped{}, err
	}
	st := stopped{}
	if st.rows, err = s.byKind(ctx, list); err != nil {
		return stopped{}, err
	}
	if st.sessions, err = s.agents.StopDetached(ctx, p.ID); err != nil {
		logging.FromContext(ctx).Error("agent background sessions not stopped", "project_id", p.ID, "err", err)
	}
	return st, nil
}

// byKind splits what the supervisor did to a project's rows of every kind into
// the terminals, agents and editors they are.
func (s *server) byKind(ctx context.Context, list []terminal.Terminal) (rows, error) {
	var r rows
	for _, t := range list {
		switch t.Kind {
		case terminal.KindShell:
			r.terminals = append(r.terminals, t)
		case terminal.KindEditor:
			r.editors = append(r.editors, t)
		case terminal.KindAgent:
			a, err := s.agents.Store().GetByTerminal(ctx, t.ID)
			if err != nil {
				return rows{}, err
			}
			r.agents = append(r.agents, a.OnRow(t))
		}
	}
	return r, nil
}
