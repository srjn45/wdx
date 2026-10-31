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
	return api.ListAgents200JSONResponse{Agents: agentTree(list)}, nil
}

func (s *server) CreateAgent(ctx context.Context, req api.CreateAgentRequestObject) (api.CreateAgentResponseObject, error) {
	b := req.Body
	if err := absolute(b.Dir); err != nil {
		return nil, err
	}
	var p project.Project
	var dir, parentID string
	var err error
	if b.Parent != nil {
		parent, err := s.findParent(ctx, *b.Parent, b.Project, b.Dir)
		if err != nil {
			return nil, err
		}
		if p, err = s.projects.GetByID(ctx, parent.Terminal.ProjectID); err != nil {
			return nil, err
		}
		if parent.Tombstone() {
			return nil, &agent.RemovedError{Agent: parent}
		}
		dir, parentID = parent.Terminal.Dir, parent.ID
		logging.FromContext(ctx).Info("starting in the parent's dir", "parent_agent_id", parentID, "dir", dir)
	} else {
		if p, err = s.projectOf(ctx, b.Project, &b.Dir); err != nil {
			return nil, err
		}
		dir = p.InWorktree(b.Dir)
		if !p.Holds(dir) {
			dir = p.ClonePath()
			logging.FromContext(ctx).Info("dir outside the project, using its clone path", "project_id", p.ID, "path", b.Dir, "dir", dir)
		}
	}
	if p.State == project.StateClosed {
		return nil, &projectClosedError{project: p}
	}
	var worktreeCreated *bool
	if b.Worktree != nil {
		var created bool
		if dir, created, err = s.projects.WorktreeNamed(ctx, p, *b.Worktree, dir); err != nil {
			return nil, err
		}
		if created {
			worktreeCreated = &created
		}
	}
	a, err := s.agents.Create(ctx, p.ID, deref(b.Name), dir, parentID)
	if err != nil {
		return nil, err
	}
	return api.CreateAgent201JSONResponse{Result: api.ResultCreated, Agent: agentView(a), WorktreeCreated: worktreeCreated}, nil
}

func (s *server) GetAgent(ctx context.Context, req api.GetAgentRequestObject) (api.GetAgentResponseObject, error) {
	a, err := s.findAgent(ctx, req.Agent, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	list, err := s.agents.Store().List(ctx, a.Terminal.ProjectID)
	if err != nil {
		return nil, err
	}
	return api.GetAgent200JSONResponse{Agent: agentNode(a, childrenOf(list))}, nil
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

func (s *server) SendAgent(ctx context.Context, req api.SendAgentRequestObject) (api.SendAgentResponseObject, error) {
	a, err := s.findAgent(ctx, req.Agent, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if a, err = s.agents.Send(ctx, a.ID, req.Body.Prompt, deref(req.Body.Instant)); err != nil {
		return nil, err
	}
	return api.SendAgent200JSONResponse{Result: api.ResultQueued, Agent: agentView(a)}, nil
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

// findParent returns the agent ref names as a new agent's parent: an id needs
// no project, and is checked against one when given; a name is looked up in
// the project given, or else the one holding dir.
func (s *server) findParent(ctx context.Context, ref string, projectRef *string, dir string) (agent.Agent, error) {
	if projectRef != nil || strings.HasPrefix(ref, idPrefix) {
		return s.findAgent(ctx, ref, projectRef, nil)
	}
	return s.findAgent(ctx, ref, nil, &dir)
}

// childrenOf returns the agents in list by the id of their parent, "" for
// those at the top level, keeping their order.
func childrenOf(list []agent.Agent) map[string][]agent.Agent {
	children := map[string][]agent.Agent{}
	for _, a := range list {
		children[a.ParentID] = append(children[a.ParentID], a)
	}
	return children
}

// agentTree is the agents in list at the top level, with their children.
func agentTree(list []agent.Agent) []api.Agent {
	children := childrenOf(list)
	views := make([]api.Agent, 0, len(children[""]))
	for _, a := range children[""] {
		views = append(views, agentNode(a, children))
	}
	return views
}

// agentNode is a with its children, as childrenOf returned them.
func agentNode(a agent.Agent, children map[string][]agent.Agent) api.Agent {
	v := agentView(a)
	nodes := make([]api.Agent, 0, len(children[a.ID]))
	for _, c := range children[a.ID] {
		nodes = append(nodes, agentNode(c, children))
	}
	v.Children = &nodes
	return v
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
	if a.ParentID != "" {
		v.ParentAgentId = &a.ParentID
	}
	if a.Worktree != "" {
		v.Worktree = &a.Worktree
	}
	if a.Tombstone() {
		v.State = api.AgentStateRemoved
		v.RemovedAt = optionalTime(a.RemovedAt)
	}
	if t.PID != 0 {
		v.Pid = &t.PID
	}
	if t.Error != "" {
		v.Error = &t.Error
	}
	return v
}
