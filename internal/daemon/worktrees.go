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
	d.marks = worktree.NewMarks(d.db)
	d.comments = worktree.NewComments(d.db)
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
		Marks:    d.marks,
		Comments: d.comments,
		Changed:  d.notices.notify,
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
		dir := a.CurrentDir
		if dir == "" {
			dir = a.Terminal.Dir
		}
		agents = append(agents, worktree.Agent{
			ID:  a.ID,
			Dir: dir,
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
	p, c, err := s.worktreeScope(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	marks, err := s.marks.List(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	drafts, err := s.comments.Drafts(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	list, err := worktree.Read(ctx, c, marks, drafts)
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

func (s *server) VerifyWorktreeFile(ctx context.Context, req api.VerifyWorktreeFileRequestObject) (api.VerifyWorktreeFileResponseObject, error) {
	p, c, err := s.worktreeScope(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if _, err := worktree.Verify(ctx, s.marks, p.ID, c, req.Worktree, req.Params.Path, deref(req.Body.ContentId)); err != nil {
		return nil, err
	}
	s.tracker.Refresh(p.ID, req.Worktree)
	return api.VerifyWorktreeFile200JSONResponse{Worktree: req.Worktree, Path: req.Params.Path, Verified: true}, nil
}

func (s *server) UnverifyWorktreeFile(ctx context.Context, req api.UnverifyWorktreeFileRequestObject) (api.UnverifyWorktreeFileResponseObject, error) {
	p, c, err := s.worktreeScope(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if _, err := worktree.Unverify(ctx, s.marks, p.ID, c, req.Worktree, req.Params.Path); err != nil {
		return nil, err
	}
	s.tracker.Refresh(p.ID, req.Worktree)
	return api.UnverifyWorktreeFile200JSONResponse{Worktree: req.Worktree, Path: req.Params.Path, Verified: false}, nil
}

func (s *server) ListWorktreeComments(ctx context.Context, req api.ListWorktreeCommentsRequestObject) (api.ListWorktreeCommentsResponseObject, error) {
	p, c, err := s.worktreeScope(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	w, err := worktree.Find(ctx, c, req.Worktree)
	if err != nil {
		return nil, err
	}
	list, err := s.comments.List(ctx, p.ID, w.Path)
	if err != nil {
		return nil, err
	}
	return api.ListWorktreeComments200JSONResponse{Comments: commentViews(list)}, nil
}

func (s *server) CreateWorktreeComment(ctx context.Context, req api.CreateWorktreeCommentRequestObject) (api.CreateWorktreeCommentResponseObject, error) {
	p, c, err := s.worktreeScope(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	b := req.Body
	added, err := worktree.AddComment(ctx, s.comments, p.ID, c, req.Worktree, worktree.Comment{
		File: b.File, Line: b.Line, Side: string(b.Side), View: string(b.View), Quote: deref(b.Quote), Body: b.Body, ContentID: deref(b.ContentId),
	})
	if err != nil {
		return nil, err
	}
	s.tracker.Refresh(p.ID, req.Worktree)
	return api.CreateWorktreeComment201JSONResponse{Comment: commentView(added)}, nil
}

func (s *server) EditWorktreeComment(ctx context.Context, req api.EditWorktreeCommentRequestObject) (api.EditWorktreeCommentResponseObject, error) {
	p, _, err := s.worktreeScope(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if err := s.comments.Edit(ctx, p.ID, req.Comment, req.Body.Body); err != nil {
		return nil, err
	}
	s.tracker.Refresh(p.ID, req.Worktree)
	return api.EditWorktreeComment200Response{}, nil
}

func (s *server) DeleteWorktreeComment(ctx context.Context, req api.DeleteWorktreeCommentRequestObject) (api.DeleteWorktreeCommentResponseObject, error) {
	p, _, err := s.worktreeScope(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if err := s.comments.Remove(ctx, p.ID, req.Comment); err != nil {
		return nil, err
	}
	s.tracker.Refresh(p.ID, req.Worktree)
	return api.DeleteWorktreeComment200Response{}, nil
}

// SendWorktreeReview gives the agent the worktree's comments as one message. They
// are sent only once the agent's inbox took it: with no agent they stay drafts.
func (s *server) SendWorktreeReview(ctx context.Context, req api.SendWorktreeReviewRequestObject) (api.SendWorktreeReviewResponseObject, error) {
	p, c, err := s.worktreeScope(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	w, err := worktree.Find(ctx, c, req.Worktree)
	if err != nil {
		return nil, err
	}
	drafts, err := s.comments.Drafts(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	var comments []worktree.Comment
	for _, d := range drafts {
		if d.Worktree == w.Path {
			comments = append(comments, d)
		}
	}
	if len(comments) == 0 {
		return nil, worktree.ErrNoComments
	}
	ref := "id:" + p.ID
	a, err := s.findAgent(ctx, req.Body.Agent, &ref, nil)
	if err != nil {
		return nil, err
	}
	var messageID string
	if a, messageID, err = s.agents.Send(ctx, a.ID, worktree.ReviewMessage(w.Name, w.Path, deref(req.Body.Note), comments), false); err != nil {
		return nil, err
	}
	if err := s.comments.MarkSent(ctx, p.ID, comments, a.ID, messageID); err != nil {
		return nil, err
	}
	s.tracker.Refresh(p.ID, req.Worktree)
	return api.SendWorktreeReview200JSONResponse{Result: api.ResultQueued, Agent: agentView(a), Comments: len(comments)}, nil
}

func commentViews(list []worktree.Comment) []api.WorktreeComment {
	views := make([]api.WorktreeComment, 0, len(list))
	for _, c := range list {
		views = append(views, commentView(c))
	}
	return views
}

func commentView(c worktree.Comment) api.WorktreeComment {
	return api.WorktreeComment{
		Id: c.ID, File: c.File, Line: c.Line, Side: api.WorktreeCommentSide(c.Side), View: api.WorktreeCommentView(c.View),
		Quote: c.Quote, Body: c.Body, State: api.WorktreeCommentState(c.State), Changed: c.Changed, CreatedAt: c.CreatedAt,
	}
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
			Name:           w.Name,
			Path:           w.Path,
			Branch:         textOrNull(w.Branch),
			Head:           textOrNull(w.Head),
			Missing:        w.Missing,
			Base:           textOrNull(w.Base),
			Ahead:          w.Ahead,
			Added:          w.Added,
			Removed:        w.Removed,
			Comments:       commentViews(w.Comments),
			Files:          make([]api.WorktreeFile, 0, len(w.Files)),
			CommittedFiles: make([]api.WorktreeCommittedFile, 0, len(w.Committed)),
		}
		for _, f := range w.Committed {
			v.CommittedFiles = append(v.CommittedFiles, api.WorktreeCommittedFile{Path: f.Path, Status: api.WorktreeCommittedFileStatus(f.Status), Verified: f.Verified})
		}
		for _, f := range w.Files {
			var at *time.Time
			if f.ModifiedAt != nil {
				t := f.ModifiedAt.UTC()
				at = &t
			}
			v.Files = append(v.Files, api.WorktreeFile{Path: f.Path, Status: api.WorktreeFileStatus(f.Status), Size: f.Size, ModifiedAt: at, Verified: f.Verified})
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
