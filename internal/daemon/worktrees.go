package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/srjn45/wdx/internal/agent"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/project"
	"github.com/srjn45/wdx/internal/screen"
	"github.com/srjn45/wdx/internal/terminal"
	"github.com/srjn45/wdx/internal/worktree"
)

var errProjectOrDir = errors.New("give project or dir")

// startTracker starts what keeps the worktrees' statuses for the streams asking
// for them, and tells it of every change: it finds which agents stopped working.
// Its reads run as long as the daemon, not as a request: they serve every stream.
func (d *Daemon) startTracker() {
	ctx, cancel := context.WithCancel(logging.NewContext(context.Background(), d.log))
	d.stopTracker = cancel
	d.tracker = worktree.NewTracker(ctx, worktree.Options{
		Checkouts: func(ctx context.Context, projectID string) (worktree.Checkouts, error) {
			p, err := d.projects.GetByID(ctx, projectID)
			if err != nil {
				return worktree.Checkouts{}, err
			}
			return checkoutsOf(p), nil
		},
		Agents: func(ctx context.Context, projectID string) ([]worktree.Agent, error) {
			list, err := d.agents.Store().List(ctx, projectID)
			if err != nil {
				return nil, err
			}
			return workingAgents(list), nil
		},
		Changed: d.notices.notify,
	})
	changed, stop := d.notices.subscribe()
	d.trackerNotices.Go(func() {
		defer stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-changed:
				d.tracker.AgentsChanged()
			}
		}
	})
}

func workingAgents(list []agent.Agent) []worktree.Agent {
	agents := make([]worktree.Agent, 0, len(list))
	for _, a := range list {
		agents = append(agents, worktree.Agent{
			ID:  a.ID,
			Dir: a.Terminal.Dir,
			Working: a.Terminal.State == terminal.StateRunning &&
				a.Activity == string(screen.ActivityWorking),
		})
	}
	return agents
}

func checkoutsOf(p project.Project) worktree.Checkouts {
	c := worktree.Checkouts{Clone: p.ClonePath()}
	for _, w := range p.Worktrees {
		c.Paths = append(c.Paths, w.Path)
	}
	return c
}

// worktreeScope returns the checkouts of the project a worktree request names,
// which it must do one way.
func (s *server) worktreeScope(ctx context.Context, ref, dir *string) (project.Project, worktree.Checkouts, error) {
	if err := exclusive(ref, dir); err != nil {
		return project.Project{}, worktree.Checkouts{}, err
	}
	if ref == nil && dir == nil {
		return project.Project{}, worktree.Checkouts{}, &requestError{err: errProjectOrDir}
	}
	p, err := s.projectOf(ctx, ref, dir)
	if dir != nil && errors.Is(err, project.ErrNotFound) {
		// Every worktree git lists counts, also one wdx never heard of.
		p, err = s.projects.GetByRepository(ctx, *dir)
	}
	if err != nil {
		return project.Project{}, worktree.Checkouts{}, err
	}
	return p, checkoutsOf(p), nil
}

func (s *server) ListWorktrees(ctx context.Context, req api.ListWorktreesRequestObject) (api.ListWorktreesResponseObject, error) {
	_, c, err := s.worktreeScope(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	list, err := worktree.Read(ctx, c)
	if err != nil {
		return nil, err
	}
	return api.ListWorktrees200JSONResponse{Worktrees: worktreeStatusViews(list)}, nil
}

func (s *server) GetWorktreeDiff(ctx context.Context, req api.GetWorktreeDiffRequestObject) (api.GetWorktreeDiffResponseObject, error) {
	_, c, err := s.worktreeScope(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if deref(req.Params.Uncommitted) {
		r, err := worktree.UncommittedDiff(ctx, c, req.Worktree)
		if err != nil {
			return nil, err
		}
		return api.GetWorktreeDiff200JSONResponse{Worktree: r.Status.Name, Patch: r.Patch.Text, Binary: r.Patch.Binary, Truncated: r.Patch.Truncated}, nil
	}
	r, err := worktree.CommittedDiff(ctx, c, req.Worktree)
	if err != nil {
		return nil, err
	}
	return api.GetWorktreeDiff200JSONResponse{
		Worktree:  r.Status.Name,
		Base:      textOrNull(r.Status.Base),
		MergeBase: textOrNull(r.MergeBase),
		Ahead:     r.Status.Ahead,
		Patch:     r.Patch.Text,
		Binary:    r.Patch.Binary,
		Truncated: r.Patch.Truncated,
	}, nil
}

func (s *server) GetWorktreeFileDiff(ctx context.Context, req api.GetWorktreeFileDiffRequestObject) (api.GetWorktreeFileDiffResponseObject, error) {
	_, c, err := s.worktreeScope(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	p, err := worktree.FileDiff(ctx, c, req.Worktree, req.Params.Path)
	if err != nil {
		return nil, err
	}
	return api.GetWorktreeFileDiff200JSONResponse(fileDiffView(req.Worktree, req.Params.Path, p)), nil
}

func fileDiffView(name, path string, p git.Patch) api.WorktreeFileDiff {
	return api.WorktreeFileDiff{Worktree: name, Path: path, Patch: p.Text, Binary: p.Binary, Truncated: p.Truncated}
}

func (s *server) RefreshWorktrees(ctx context.Context, req api.RefreshWorktreesRequestObject) (api.RefreshWorktreesResponseObject, error) {
	p, err := s.find(ctx, req.Project)
	if err != nil {
		return nil, err
	}
	s.tracker.Refresh(p.ID, deref(req.Params.Worktree))
	return api.RefreshWorktrees202Response{}, nil
}

func worktreeStatusViews(list []worktree.Status) []api.WorktreeStatus {
	views := make([]api.WorktreeStatus, 0, len(list))
	for _, w := range list {
		v := api.WorktreeStatus{
			Name:    w.Name,
			Path:    w.Path,
			Branch:  textOrNull(w.Branch),
			Head:    textOrNull(w.Head),
			Missing: w.Missing,
			Base:    textOrNull(w.Base),
			Ahead:   w.Ahead,
			Added:   w.Added,
			Removed: w.Removed,
			Files:   make([]api.WorktreeFile, 0, len(w.Files)),
		}
		for _, f := range w.Files {
			var at *time.Time
			if f.ModifiedAt != nil {
				t := f.ModifiedAt.UTC()
				at = &t
			}
			v.Files = append(v.Files, api.WorktreeFile{Path: f.Path, Status: api.WorktreeFileStatus(f.Status), Size: f.Size, ModifiedAt: at})
		}
		views = append(views, v)
	}
	return views
}

// textOrNull is s, with "" as an absent value.
func textOrNull(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
