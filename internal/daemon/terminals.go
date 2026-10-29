package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/project"
	"github.com/srjn45/wdx/internal/terminal"
)

// projectClosedError is returned when a terminal would start in a closed
// project.
type projectClosedError struct {
	project project.Project
}

func (e *projectClosedError) Error() string {
	return fmt.Sprintf("project %s is closed", e.project.Name)
}

func (s *server) ListTerminals(ctx context.Context, req api.ListTerminalsRequestObject) (api.ListTerminalsResponseObject, error) {
	if err := exclusive(req.Params.Project, req.Params.Dir); err != nil {
		return nil, err
	}
	var projectID string
	if req.Params.Project != nil || req.Params.Dir != nil {
		p, err := s.projectOf(ctx, req.Params.Project, req.Params.Dir)
		if err != nil {
			return nil, err
		}
		projectID = p.ID
	}
	list, err := s.terminals.Store().List(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return api.ListTerminals200JSONResponse{Terminals: terminalViews(list)}, nil
}

func (s *server) CreateTerminal(ctx context.Context, req api.CreateTerminalRequestObject) (api.CreateTerminalResponseObject, error) {
	b := req.Body
	if err := absolute(b.Dir); err != nil {
		return nil, err
	}
	p, err := s.projectOf(ctx, b.Project, &b.Dir)
	if err != nil {
		return nil, err
	}
	dir := b.Dir
	if !p.Holds(dir) {
		dir = p.ClonePath()
		logging.FromContext(ctx).Info("dir outside the project, using its clone path", "project_id", p.ID, "path", b.Dir, "dir", dir)
	}
	if p.State == project.StateClosed {
		return nil, &projectClosedError{project: p}
	}
	t, err := s.terminals.Create(ctx, p.ID, deref(b.Name), dir)
	if err != nil {
		return nil, err
	}
	return api.CreateTerminal201JSONResponse{Result: api.ResultCreated, Terminal: terminalView(t)}, nil
}

func (s *server) GetTerminal(ctx context.Context, req api.GetTerminalRequestObject) (api.GetTerminalResponseObject, error) {
	t, err := s.findTerminal(ctx, req.Terminal, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	return api.GetTerminal200JSONResponse{Terminal: terminalView(t)}, nil
}

func (s *server) StartTerminal(ctx context.Context, req api.StartTerminalRequestObject) (api.StartTerminalResponseObject, error) {
	t, err := s.findTerminal(ctx, req.Terminal, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	p, err := s.projects.GetByID(ctx, t.ProjectID)
	if err != nil {
		return nil, err
	}
	if p.State == project.StateClosed {
		return nil, &projectClosedError{project: p}
	}
	if t, err = s.terminals.Start(ctx, t.ID); err != nil {
		return nil, err
	}
	return api.StartTerminal200JSONResponse{Result: api.ResultStarted, Terminal: terminalView(t)}, nil
}

func (s *server) DeleteTerminal(ctx context.Context, req api.DeleteTerminalRequestObject) (api.DeleteTerminalResponseObject, error) {
	t, err := s.findTerminal(ctx, req.Terminal, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if t, err = s.terminals.Remove(ctx, t.ID, deref(req.Params.StopRunning)); err != nil {
		return nil, err
	}
	return api.DeleteTerminal200JSONResponse{Result: api.ResultRemoved, Terminal: terminalView(t)}, nil
}

func (s *server) GetTerminalHistory(ctx context.Context, req api.GetTerminalHistoryRequestObject) (api.GetTerminalHistoryResponseObject, error) {
	from, count, tail := req.Params.From, req.Params.Count, req.Params.Tail
	if tail != nil && (from != nil || count != nil) {
		return nil, &requestError{err: errors.New("give tail, or from and count, not both")}
	}
	t, err := s.findTerminal(ctx, req.Terminal, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if count != nil && from == nil {
		from = new(int64)
	}
	h, err := s.terminals.History(t.ID, from, cmp.Or(deref(count), deref(tail), 1000))
	if err != nil {
		return nil, err
	}
	logging.FromContext(ctx).Info("history read", "terminal_id", t.ID, "from", h.From, "lines", len(h.Lines), "next", h.Next)
	lines := h.Lines
	if lines == nil {
		lines = []string{}
	}
	return api.GetTerminalHistory200JSONResponse{History: api.TerminalHistory{From: h.From, Next: h.Next, First: h.First, Lines: lines}}, nil
}

// projectOf returns the project ref names, or else the one whose worktree
// holds dir.
func (s *server) projectOf(ctx context.Context, ref, dir *string) (project.Project, error) {
	if ref != nil {
		return s.find(ctx, *ref)
	}
	if err := absolute(*dir); err != nil {
		return project.Project{}, err
	}
	return s.projects.GetByPath(ctx, *dir)
}

// exclusive fails when a request names a project both ways.
func exclusive(projectRef, dir *string) error {
	if projectRef != nil && dir != nil {
		return &requestError{err: errors.New("give project or dir, not both")}
	}
	return nil
}

// findTerminal returns the terminal ref names: "id:" and its id, or its
// name in the project named by projectRef or holding dir. A project given
// with an id must be the terminal's.
func (s *server) findTerminal(ctx context.Context, ref string, projectRef, dir *string) (terminal.Terminal, error) {
	if err := exclusive(projectRef, dir); err != nil {
		return terminal.Terminal{}, err
	}
	id, byID := strings.CutPrefix(ref, idPrefix)
	if !byID && projectRef == nil && dir == nil {
		return terminal.Terminal{}, &requestError{err: fmt.Errorf("terminal name %q needs project or dir: names are unique only within a project", ref)}
	}
	var p project.Project
	if projectRef != nil || dir != nil {
		var err error
		if p, err = s.projectOf(ctx, projectRef, dir); err != nil {
			return terminal.Terminal{}, err
		}
	}
	if !byID {
		return s.terminals.Store().GetByName(ctx, p.ID, ref)
	}
	t, err := s.terminals.Store().Get(ctx, id)
	if err == nil && p.ID != "" && t.ProjectID != p.ID {
		return terminal.Terminal{}, fmt.Errorf("%w: %q is not in project %s", terminal.ErrNotFound, id, p.Name)
	}
	return t, err
}

func terminalViews(list []terminal.Terminal) []api.Terminal {
	views := make([]api.Terminal, 0, len(list))
	for _, t := range list {
		views = append(views, terminalView(t))
	}
	return views
}

// optionalTerminals is list for a result, or nil when it is empty.
func optionalTerminals(list []terminal.Terminal) *[]api.Terminal {
	if len(list) == 0 {
		return nil
	}
	views := terminalViews(list)
	return &views
}

func terminalView(t terminal.Terminal) api.Terminal {
	v := api.Terminal{
		Id:          t.ID,
		Name:        t.Name,
		ProjectId:   t.ProjectID,
		ProjectName: t.ProjectName,
		Dir:         t.Dir,
		Shell:       t.Shell,
		State:       api.TerminalState(t.State),
		ExitCode:    t.ExitCode,
		CreatedAt:   t.CreatedAt.UTC(),
		UpdatedAt:   t.UpdatedAt.UTC(),
		StartedAt:   optionalTime(t.StartedAt),
		EndedAt:     optionalTime(t.EndedAt),
	}
	if t.PID != 0 {
		v.Pid = &t.PID
	}
	if t.Error != "" {
		v.Error = &t.Error
	}
	return v
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
}
