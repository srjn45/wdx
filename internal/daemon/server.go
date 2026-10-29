package daemon

import (
	"context"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/project"
	"github.com/srjn45/wdx/internal/terminal"
)

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
