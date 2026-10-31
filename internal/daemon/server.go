package daemon

import (
	"context"

	"github.com/srjn45/wdx/internal/agent"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/project"
	"github.com/srjn45/wdx/internal/terminal"
)

// server implements the API on the project store, the terminals' supervisor
// and the agents' manager.
type server struct {
	projects  *project.Store
	terminals *terminal.Supervisor
	agents    *agent.Manager
	self      api.Daemon
	notices   *notices        // tells the streams about changes
	stopping  <-chan struct{} // closed when the daemon starts stopping: the streams end
	reload    func(context.Context) error
}

var _ api.StrictServerInterface = (*server)(nil)

func (s *server) GetVersion(context.Context, api.GetVersionRequestObject) (api.GetVersionResponseObject, error) {
	return api.GetVersion200JSONResponse{Name: identity.Name, Version: identity.Version, Commit: identity.Commit}, nil
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
