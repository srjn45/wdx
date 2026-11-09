package daemon

import (
	"context"

	"github.com/srjn45/wdx/internal/agent"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/doc"
	"github.com/srjn45/wdx/internal/hublink"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/plan"
	"github.com/srjn45/wdx/internal/project"
	"github.com/srjn45/wdx/internal/remote"
	"github.com/srjn45/wdx/internal/terminal"
	"github.com/srjn45/wdx/internal/tidestate"
	"github.com/srjn45/wdx/internal/worktree"
)

// server implements the API on the project store, the terminals' supervisor,
// the agents' manager, and the doc and plan stores.
type server struct {
	projects  *project.Store
	terminals *terminal.Supervisor
	agents    *agent.Manager
	docs      *doc.Store
	outbox    *doc.Outbox
	plans     *plan.Store
	tabs      *tidestate.Store
	gitCopy   *gitCopy
	self      api.Daemon
	notices   *notices // tells the streams about changes
	// sessionNotices tells the session streams a session opened or closed.
	sessionNotices *notices
	stopping       <-chan struct{} // closed when the daemon starts stopping: the streams end
	reload         func(context.Context) error
	settings       SettingStore // nil when it has none
	tracker        *worktree.Tracker
	marks          *worktree.Marks
	comments       *worktree.Comments
	lander         *worktree.Lander
	pullRequests   *worktree.PullRequests
	runner         *plan.Runner
	planParallel   func() int
	ghProgram      string // as Options.GH
	hub            *hublink.Link
	hubLogins      *hubLogins
	access         *hubAccess
	remote         *remote.Remote
	nodeStreams    *nodeStreams
	clock          hublink.Clock
}

var _ api.StrictServerInterface = (*server)(nil)

func (s *server) GetVersion(context.Context, api.GetVersionRequestObject) (api.GetVersionResponseObject, error) {
	return api.GetVersion200JSONResponse{Name: identity.Name, Version: identity.Version, Commit: identity.Commit}, nil
}

func (s *server) ListSettings(ctx context.Context, _ api.ListSettingsRequestObject) (api.ListSettingsResponseObject, error) {
	list := []api.Setting{}
	if s.settings != nil {
		var err error
		if list, err = s.settings.List(); err != nil {
			return nil, &ReloadError{err}
		}
	}
	logging.FromContext(ctx).Info("settings listed", "count", len(list))
	return api.ListSettings200JSONResponse{Settings: list}, nil
}

func (s *server) UpdateSetting(ctx context.Context, req api.UpdateSettingRequestObject) (api.UpdateSettingResponseObject, error) {
	if s.settings == nil {
		return nil, &UnknownSettingError{Key: req.Setting}
	}
	setting, changed, err := s.settings.Set(req.Setting, req.Body.Value)
	if err != nil {
		return nil, err
	}
	logging.FromContext(ctx).Info("setting saved", "name", req.Setting, "value", setting.Value, "changed", changed)
	// Run even when unchanged, so that setting it again recovers from a reload
	// that failed.
	if err := s.reload(context.WithoutCancel(ctx)); err != nil {
		return nil, err
	}
	result := api.ResultUnchanged
	if changed {
		result = api.ResultUpdated
	}
	return api.UpdateSetting200JSONResponse{Result: result, Setting: setting}, nil
}

func (s *server) ReloadDaemon(ctx context.Context, _ api.ReloadDaemonRequestObject) (api.ReloadDaemonResponseObject, error) {
	// A reload runs to its end when the client leaves, as a change does.
	if err := s.reload(context.WithoutCancel(ctx)); err != nil {
		return nil, err
	}
	return api.ReloadDaemon200JSONResponse{Result: api.ResultReloaded}, nil
}

func (s *server) GetDaemon(context.Context, api.GetDaemonRequestObject) (api.GetDaemonResponseObject, error) {
	return api.GetDaemon200JSONResponse(s.self), nil
}
