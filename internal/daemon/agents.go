package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/srjn45/wdx/internal/agent"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/project"
)

func (s *server) ListAgents(ctx context.Context, req api.ListAgentsRequestObject) (api.ListAgentsResponseObject, error) {
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
	list, err := s.agents.Store().List(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return api.ListAgents200JSONResponse{Agents: agentViews(list)}, nil
}

func (s *server) CreateAgent(ctx context.Context, req api.CreateAgentRequestObject) (api.CreateAgentResponseObject, error) {
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
	a, err := s.agents.Create(ctx, p.ID, deref(b.Name), dir)
	if err != nil {
		return nil, err
	}
	return api.CreateAgent201JSONResponse{Result: api.ResultCreated, Agent: agentView(a)}, nil
}

func (s *server) GetAgent(ctx context.Context, req api.GetAgentRequestObject) (api.GetAgentResponseObject, error) {
	a, err := s.findAgent(ctx, req.Agent, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	return api.GetAgent200JSONResponse{Agent: agentView(a)}, nil
}

func (s *server) StartAgent(ctx context.Context, req api.StartAgentRequestObject) (api.StartAgentResponseObject, error) {
	a, err := s.findAgent(ctx, req.Agent, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	p, err := s.projects.GetByID(ctx, a.Terminal.ProjectID)
	if err != nil {
		return nil, err
	}
	if p.State == project.StateClosed {
		return nil, &projectClosedError{project: p}
	}
	if a, err = s.agents.Start(ctx, a.ID); err != nil {
		return nil, err
	}
	return api.StartAgent200JSONResponse{Result: api.ResultStarted, Agent: agentView(a)}, nil
}

func (s *server) DeleteAgent(ctx context.Context, req api.DeleteAgentRequestObject) (api.DeleteAgentResponseObject, error) {
	a, err := s.findAgent(ctx, req.Agent, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if a, err = s.agents.Remove(ctx, a.ID, deref(req.Params.StopRunning)); err != nil {
		return nil, err
	}
	return api.DeleteAgent200JSONResponse{Result: api.ResultRemoved, Agent: agentView(a)}, nil
}

func (s *server) GetAgentHistory(ctx context.Context, req api.GetAgentHistoryRequestObject) (api.GetAgentHistoryResponseObject, error) {
	from, count, tail := req.Params.From, req.Params.Count, req.Params.Tail
	if tail != nil && (from != nil || count != nil) {
		return nil, &requestError{err: errors.New("give tail, or from and count, not both")}
	}
	a, err := s.findAgent(ctx, req.Agent, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if count != nil && from == nil {
		from = new(int64)
	}
	h, err := s.agents.History(ctx, a.ID, from, cmp.Or(deref(count), deref(tail), 1000))
	if err != nil {
		return nil, err
	}
	logging.FromContext(ctx).Info("history read", "agent_id", a.ID, "from", h.From, "lines", len(h.Lines), "next", h.Next)
	lines := h.Lines
	if lines == nil {
		lines = []string{}
	}
	return api.GetAgentHistory200JSONResponse{History: api.TerminalHistory{From: h.From, Next: h.Next, First: h.First, Lines: lines}}, nil
}

// findAgent returns the agent ref names: "id:" and its id, or its name in the
// project named by projectRef or holding dir. A project given with an id must
// be the agent's.
func (s *server) findAgent(ctx context.Context, ref string, projectRef, dir *string) (agent.Agent, error) {
	if err := exclusive(projectRef, dir); err != nil {
		return agent.Agent{}, err
	}
	id, byID := strings.CutPrefix(ref, idPrefix)
	if !byID && projectRef == nil && dir == nil {
		return agent.Agent{}, &requestError{err: fmt.Errorf("agent name %q needs project or dir: names are unique only within a project", ref)}
	}
	var p project.Project
	if projectRef != nil || dir != nil {
		var err error
		if p, err = s.projectOf(ctx, projectRef, dir); err != nil {
			return agent.Agent{}, err
		}
	}
	if !byID {
		return s.agents.Store().GetByName(ctx, p.ID, ref)
	}
	a, err := s.agents.Store().Get(ctx, id)
	if err == nil && p.ID != "" && a.Terminal.ProjectID != p.ID {
		return agent.Agent{}, fmt.Errorf("%w: %q is not in project %s", agent.ErrNotFound, id, p.Name)
	}
	return a, err
}

func agentViews(list []agent.Agent) []api.Agent {
	views := make([]api.Agent, 0, len(list))
	for _, a := range list {
		views = append(views, agentView(a))
	}
	return views
}

// optionalAgents is list for a result, or nil when it is empty.
func optionalAgents(list []agent.Agent) *[]api.Agent {
	if len(list) == 0 {
		return nil
	}
	views := agentViews(list)
	return &views
}

func agentView(a agent.Agent) api.Agent {
	t := a.Terminal
	v := api.Agent{
		Id:          a.ID,
		Name:        t.Name,
		ProjectId:   t.ProjectID,
		ProjectName: t.ProjectName,
		TerminalId:  t.ID,
		Aca:         a.ACA,
		SessionId:   a.SessionID,
		Dir:         t.Dir,
		State:       api.AgentState(t.State),
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
