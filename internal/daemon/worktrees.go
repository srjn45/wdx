package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"strings"

	"github.com/srjn45/wdx/internal/agent"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/gh"
	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/plan"
	"github.com/srjn45/wdx/internal/project"
	"github.com/srjn45/wdx/internal/screen"
	"github.com/srjn45/wdx/internal/terminal"
	"github.com/srjn45/wdx/internal/worktree"
)

var (
	errProjectOrDir      = errors.New("give project or dir")
	errLandNotConfigured = errors.New("the project has not said how it lands")
)

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
	d.pullRequests = worktree.NewPullRequests(logging.NewContext(context.Background(), d.log), d.db, gh.CLI{Program: d.opts.GH}, func(string) { d.notices.notify() })
	d.lander = worktree.NewLander(logging.NewContext(context.Background(), d.log), worktree.LanderOptions{
		Check: d.runLandCheck,
		Changed: func(projectID string) {
			d.tracker.Refresh(projectID, "") // it moved branches, which a watch may not see at once
			d.notices.notify()
			if d.runner != nil {
				// A landing may have put a plan's work in the default branch, which the
				// plan after it waits for.
				d.runner.WakeParents(logging.NewContext(context.Background(), d.log), projectID)
			}
		},
		PullRequests: d.pullRequests,
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

// landTerminal names the terminal that runs a worktree's check.
func landTerminal(worktreeName string) string { return "land-" + worktreeName }

// runLandCheck runs the project's check in a terminal of its own, so that it can be
// watched and its output stays, and waits for it. One that passes takes its terminal
// with it. A terminal of that name still running is left alone: it may not be ours.
func (d *Daemon) runLandCheck(ctx context.Context, projectID, worktreeName, dir, command string, started func(terminal string)) error {
	store := d.terminals.Store()
	name := landTerminal(worktreeName)
	old, err := store.GetByName(ctx, projectID, name)
	switch {
	case err == nil && (old.Kind != terminal.KindShell || old.State.Live()):
		return fmt.Errorf("terminal %s is still running: remove it first", name)
	case err == nil:
		if _, err := d.terminals.Remove(ctx, old.ID, false); err != nil {
			return err
		}
	case !errors.Is(err, terminal.ErrNotFound):
		return err
	}
	// Subscribed before it starts, so that its end cannot go unseen.
	changed, stop := d.notices.subscribe()
	defer stop()
	t, err := d.terminals.CreateCommand(ctx, projectID, name, dir, []string{"sh", "-c", command})
	if err != nil {
		return fmt.Errorf("the check did not start: %w", err)
	}
	started(t.Name)
	for {
		row, err := store.Get(ctx, t.ID)
		if errors.Is(err, terminal.ErrNotFound) {
			return fmt.Errorf("terminal %s was removed before the check ended", name)
		}
		if err != nil {
			return err
		}
		if !row.State.Live() {
			if row.State == terminal.StateExited && row.ExitCode != nil && *row.ExitCode == 0 {
				if _, err := d.terminals.Remove(ctx, t.ID, false); err != nil {
					logging.FromContext(ctx).Error("the check's terminal not removed", "terminal_id", t.ID, "err", err)
				}
				return nil
			}
			if row.ExitCode != nil {
				return fmt.Errorf("the check exited with %d: terminal %s has its output", *row.ExitCode, name)
			}
			return fmt.Errorf("the check ended as %s: terminal %s has its output", row.State, name)
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return fmt.Errorf("the daemon stopped during the check: %w", ctx.Err())
		}
	}
}

func workingAgents(list []agent.Agent) []worktree.Agent {
	agents := make([]worktree.Agent, 0, len(list))
	for _, a := range list {
		agents = append(agents, worktree.Agent{
			ID:  a.ID,
			Dir: workingDir(a),
			Working: a.Terminal.State == terminal.StateRunning &&
				a.Activity == string(screen.ActivityWorking),
		})
	}
	return agents
}

// workingDir is where the agent last said it works, else where it started.
func workingDir(a agent.Agent) string {
	if a.CurrentDir != "" {
		return a.CurrentDir
	}
	return a.Terminal.Dir
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
	return api.ListWorktrees200JSONResponse{Worktrees: s.withLandings(ctx, p.ID, worktreeStatusViews(list))}, nil
}

// withLandings says how far each worktree's landing got, which only the lander knows,
// and what gh last said of its PR.
func (s *server) withLandings(ctx context.Context, projectID string, views []api.WorktreeStatus) []api.WorktreeStatus {
	prs, err := s.pullRequests.List(ctx, projectID)
	if err != nil {
		logging.FromContext(ctx).Error("pull requests not read", "project_id", projectID, "err", err)
	}
	for i := range views {
		if l, ok := s.lander.Of(projectID, views[i].Path); ok {
			v := landingView(l)
			views[i].Landing = &v
		}
		if pr, ok := prs[views[i].Path]; ok {
			views[i].PullRequest = pullRequestView(&pr)
		}
	}
	return views
}

func pullRequestView(pr *gh.PullRequest) *api.WorktreePullRequest {
	if pr == nil {
		return nil
	}
	return &api.WorktreePullRequest{Number: pr.Number, Url: pr.URL, State: api.WorktreePullRequestState(pr.State), Head: pr.Head}
}

func landingView(l worktree.Landing) api.WorktreeLanding {
	v := api.WorktreeLanding{
		Id:        l.ID,
		State:     api.WorktreeLandingState(l.State),
		Code:      textOrNull(string(l.Failure)),
		Detail:    textOrNull(l.Detail),
		Conflicts: l.Conflicts,
		Terminal:  textOrNull(l.Terminal),
		Base:      l.Base,
		Commit:    textOrNull(l.Commit),
		Head:      textOrNull(l.Head),
		Pushed:    l.Pushed,
		PushError: textOrNull(l.PushError),

		PullRequest: pullRequestView(l.PullRequest),
	}
	if v.Conflicts == nil {
		v.Conflicts = []string{}
	}
	return v
}

// LandWorktree starts landing a worktree, once the project has said how.
func (s *server) LandWorktree(ctx context.Context, req api.LandWorktreeRequestObject) (api.LandWorktreeResponseObject, error) {
	p, c, err := s.worktreeScope(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if p.State == project.StateClosed {
		return nil, &projectClosedError{project: p}
	}
	if w, err := worktree.Find(ctx, c, req.Worktree); err == nil {
		if err := s.notInPlan(ctx, w); err != nil {
			return nil, err
		}
	}
	settings, ok, err := s.projects.LandSettings(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errLandNotConfigured
	}
	agents, err := s.agents.Store().List(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	method := settings.Method
	if req.Params.Method != nil {
		method = string(*req.Params.Method)
	}
	l, err := s.lander.Start(ctx, p.ID, c, req.Worktree, workingAgents(agents), worktree.LandSettings{Method: method, Check: settings.Check, Push: settings.Push})
	if err != nil {
		return nil, err
	}
	return api.LandWorktree202JSONResponse{Result: api.ResultStarted, Worktree: req.Worktree, ProjectId: p.ID, Landing: landingView(l)}, nil
}

// RebaseWorktree rebases a worktree onto its base, in the request: without a check
// it is short. A slot of a plan going on is refused, as for landing.
func (s *server) RebaseWorktree(ctx context.Context, req api.RebaseWorktreeRequestObject) (api.RebaseWorktreeResponseObject, error) {
	p, c, err := s.worktreeScope(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if p.State == project.StateClosed {
		return nil, &projectClosedError{project: p}
	}
	if w, err := worktree.Find(ctx, c, req.Worktree); err == nil {
		if err := s.notInPlan(ctx, w); err != nil {
			return nil, err
		}
	}
	agents, err := s.agents.Store().List(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	r, err := worktree.Rebase(ctx, c, req.Worktree, workingAgents(agents))
	if err != nil {
		return nil, err
	}
	result := api.ResultRebased
	if r.UpToDate() {
		result = api.ResultUpToDate
	}
	return api.RebaseWorktree200JSONResponse{Result: result, Worktree: r.Name, Base: r.Base, HeadBefore: r.Before, HeadAfter: r.After, Pushed: r.Pushed}, nil
}

func (s *server) GetProjectLanding(ctx context.Context, req api.GetProjectLandingRequestObject) (api.GetProjectLandingResponseObject, error) {
	p, err := s.find(ctx, req.Project)
	if err != nil {
		return nil, err
	}
	v, ok, err := s.projects.LandSettings(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	if !ok {
		v.Method = worktree.MethodRebase
	}
	return api.GetProjectLanding200JSONResponse{Configured: ok, Method: api.ProjectLandingMethod(v.Method), Check: textOrNull(v.Check), Push: v.Push}, nil
}

func (s *server) SetProjectLanding(ctx context.Context, req api.SetProjectLandingRequestObject) (api.SetProjectLandingResponseObject, error) {
	p, err := s.find(ctx, req.Project)
	if err != nil {
		return nil, err
	}
	saved, ok, err := s.projects.LandSettings(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	v := project.LandSettings{Method: worktree.MethodRebase, Check: strings.TrimSpace(deref(req.Body.Check)), Push: req.Body.Push}
	switch {
	case req.Body.Method != nil:
		v.Method = string(*req.Body.Method)
	case ok:
		v.Method = saved.Method
	}
	if err := s.projects.SetLandSettings(ctx, p.ID, v); err != nil {
		return nil, err
	}
	result := api.ResultUpdated
	return api.SetProjectLanding200JSONResponse{Result: &result, Configured: true, Method: api.ProjectLandingMethod(v.Method), Check: textOrNull(v.Check), Push: v.Push}, nil
}

// RemoveWorktree removes a worktree that landed. Its agents are told once it is
// gone, so that a refusal by git never sends them away from a worktree still there.
// worktreeInPlanError is returned for landing or removing a slot of a plan going on,
// which the plan lands itself, and needs until it is over.
type worktreeInPlanError struct {
	worktree string
	plan     plan.Plan
}

func (e *worktreeInPlanError) Error() string {
	return fmt.Sprintf("worktree %s is a slot of plan %s, which is %s", e.worktree, e.plan.Name, e.plan.State)
}

// notInPlan returns a *worktreeInPlanError when w is a slot of a plan going on.
func (s *server) notInPlan(ctx context.Context, w worktree.Status) error {
	p, ok, err := s.plans.SlotAt(ctx, w.Path)
	if err != nil || !ok {
		return err
	}
	logging.FromContext(ctx).Info("worktree in a plan", "worktree", w.Name, "plan_id", p.ID)
	return &worktreeInPlanError{worktree: w.Name, plan: p}
}

func (s *server) RemoveWorktree(ctx context.Context, req api.RemoveWorktreeRequestObject) (api.RemoveWorktreeResponseObject, error) {
	p, c, err := s.worktreeScope(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if w, err := worktree.Find(ctx, c, req.Worktree); err == nil {
		if err := s.notInPlan(ctx, w); err != nil {
			return nil, err
		}
	}
	w, err := worktree.Removable(ctx, c, req.Worktree, s.pullRequests)
	if err != nil {
		return nil, err
	}
	// Before git removes the directory they run in.
	editors, err := s.terminals.StopEditors(ctx, p.ID, w.Path)
	if err != nil {
		return nil, err
	}
	r, err := worktree.Remove(ctx, w)
	if err != nil {
		return nil, err
	}
	if err := s.projects.ForgetWorktree(ctx, p.ID, w.Path); err != nil {
		return nil, err
	}
	s.lander.Forget(p.ID, w.Path)
	if err := s.pullRequests.Forget(ctx, p.ID, w.Path); err != nil {
		logging.FromContext(ctx).Error("pull request not forgotten", "path", w.Path, "err", err)
	}
	if err := s.tabs.RemoveWorktree(ctx, p.ID, w.Name); err != nil {
		return nil, err
	}
	notified, err := s.tellRemoved(ctx, p.ID, w, c.Clone)
	if err != nil {
		return nil, err
	}
	s.tracker.Refresh(p.ID, "")
	return api.RemoveWorktree200JSONResponse{
		Result:         api.ResultRemoved,
		Worktree:       w.Name,
		Path:           w.Path,
		BranchDeleted:  textOrNull(r.BranchDeleted),
		BranchKept:     textOrNull(r.BranchKept),
		Notified:       notified,
		StoppedEditors: editorViews(p, editors),
	}, nil
}

// tellRemoved sends the agents running in the worktree where to go on, and returns
// who was told. The worktree is gone by now, so one that cannot be told is logged and
// fails nothing.
func (s *server) tellRemoved(ctx context.Context, projectID string, w worktree.Status, clone string) ([]string, error) {
	list, err := s.agents.Store().List(ctx, projectID)
	if err != nil {
		return nil, err
	}
	notified := []string{}
	for _, a := range list {
		if a.Terminal.State != terminal.StateRunning || !w.Holds(workingDir(a)) {
			continue
		}
		if _, _, err := s.agents.Send(ctx, a.ID, worktree.RemovedMessage(w, clone), false); err != nil {
			logging.FromContext(ctx).Error("agent not told of the removal", "agent_id", a.ID, "worktree", w.Name, "err", err)
			continue
		}
		notified = append(notified, a.Terminal.Name)
	}
	return notified, nil
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

func (s *server) ListWorktreeFiles(ctx context.Context, req api.ListWorktreeFilesRequestObject) (api.ListWorktreeFilesResponseObject, error) {
	_, c, err := s.worktreeScope(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	f, err := worktree.ListFiles(ctx, c, req.Worktree)
	if err != nil {
		return nil, err
	}
	return api.ListWorktreeFiles200JSONResponse{Worktree: req.Worktree, Paths: f.Paths, EmptyDirs: f.EmptyDirs, Truncated: false}, nil
}

func (s *server) CreateWorktreeFile(ctx context.Context, req api.CreateWorktreeFileRequestObject) (api.CreateWorktreeFileResponseObject, error) {
	p, c, err := s.worktreeScope(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	path, err := worktree.CreateEntry(ctx, c, req.Worktree, req.Body.Path, deref(req.Body.Directory))
	if err != nil {
		return nil, err
	}
	s.tracker.Refresh(p.ID, req.Worktree)
	return api.CreateWorktreeFile201JSONResponse{Result: api.ResultCreated, Worktree: req.Worktree, Path: path}, nil
}

func (s *server) RenameWorktreeFile(ctx context.Context, req api.RenameWorktreeFileRequestObject) (api.RenameWorktreeFileResponseObject, error) {
	p, c, err := s.worktreeScope(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if err := s.noEditorOn(ctx, p, c, req.Worktree, req.Body.Path); err != nil {
		return nil, err
	}
	path, err := worktree.RenameEntry(ctx, c, req.Worktree, req.Body.Path, req.Body.Name)
	if err != nil {
		return nil, err
	}
	s.tracker.Refresh(p.ID, req.Worktree)
	from := filepath.Clean(req.Body.Path)
	if err := s.tabs.RenamePath(ctx, p.ID, req.Worktree, from, path); err != nil {
		return nil, err
	}
	return api.RenameWorktreeFile200JSONResponse{Result: api.ResultRenamed, Worktree: req.Worktree, Path: path, From: &from}, nil
}

func (s *server) RemoveWorktreeFile(ctx context.Context, req api.RemoveWorktreeFileRequestObject) (api.RemoveWorktreeFileResponseObject, error) {
	p, c, err := s.worktreeScope(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if err := s.noEditorOn(ctx, p, c, req.Worktree, req.Params.Path); err != nil {
		return nil, err
	}
	if err := worktree.RemoveEntry(ctx, c, req.Worktree, req.Params.Path); err != nil {
		return nil, err
	}
	s.tracker.Refresh(p.ID, req.Worktree)
	if err := s.tabs.RemovePath(ctx, p.ID, req.Worktree, filepath.Clean(req.Params.Path)); err != nil {
		return nil, err
	}
	return api.RemoveWorktreeFile200JSONResponse{Result: api.ResultRemoved, Worktree: req.Worktree, Path: filepath.Clean(req.Params.Path)}, nil
}

// editorRunningError is a file, or a directory, an editor has open, which renaming or
// deleting would take from under it.
type editorRunningError struct {
	path    string
	editors []api.Editor
}

func (e *editorRunningError) Error() string {
	return fmt.Sprintf("an editor has %s open: close it first", e.path)
}

// noEditorOn fails when an editor runs on the file at path in the worktree called
// name, or on a file under it.
func (s *server) noEditorOn(ctx context.Context, p project.Project, c worktree.Checkouts, name, path string) error {
	w, err := worktree.Find(ctx, c, name)
	if err != nil {
		return err
	}
	live, err := s.terminals.LiveEditors(ctx, p.ID, w.Path)
	if err != nil {
		return err
	}
	clean := filepath.Clean(path)
	var on []terminal.Terminal
	for _, t := range live {
		if t.EditorPath == clean || strings.HasPrefix(t.EditorPath, clean+"/") {
			on = append(on, t)
		}
	}
	if len(on) > 0 {
		logging.FromContext(ctx).Info("editor running on the path", "worktree", name, "path", clean, "editors", len(on))
		return &editorRunningError{path: clean, editors: editorViews(p, on)}
	}
	return nil
}

func (s *server) GetWorktreeFileContent(ctx context.Context, req api.GetWorktreeFileContentRequestObject) (api.GetWorktreeFileContentResponseObject, error) {
	_, c, err := s.worktreeScope(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	f, err := worktree.ReadFile(ctx, c, req.Worktree, req.Params.Path)
	if err != nil {
		return nil, err
	}
	view := api.WorktreeFileContent{Worktree: req.Worktree, Path: req.Params.Path, Binary: f.Binary, TooLarge: f.TooLarge, Size: f.Size}
	if !f.Binary && !f.TooLarge {
		view.Content = &f.Text // an empty file has content
	}
	return api.GetWorktreeFileContent200JSONResponse(view), nil
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
	name := deref(req.Params.Worktree)
	s.tracker.Refresh(p.ID, name)
	if deref(req.Params.PullRequests) {
		s.pullRequests.Ask(p.ID, p.ClonePath(), name, s.tracker.Statuses(p.ID))
	}
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
			Unread:         w.Unread,
			Base:           textOrNull(w.Base),
			Ahead:          w.Ahead,
			Behind:         w.Behind,
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
