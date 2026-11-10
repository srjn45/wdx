package server

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/sqlite"
)

// api implements the hub's operations; the endpoint each is served on is the
// handler's to check.
type api struct {
	server     *Server
	apiVersion string // the spec's info.version
}

var _ hubapi.StrictServerInterface = (*api)(nil)

func (a *api) version() hubapi.VersionJSONResponse {
	return hubapi.VersionJSONResponse{Name: hubconfig.Name, Version: a.server.version(), Commit: identity.Commit, ApiVersion: a.apiVersion}
}

func (a *api) GetVersion(context.Context, hubapi.GetVersionRequestObject) (hubapi.GetVersionResponseObject, error) {
	return hubapi.GetVersion200JSONResponse{VersionJSONResponse: a.version()}, nil
}

func (a *api) GetNodeVersion(context.Context, hubapi.GetNodeVersionRequestObject) (hubapi.GetNodeVersionResponseObject, error) {
	return hubapi.GetNodeVersion200JSONResponse{VersionJSONResponse: a.version()}, nil
}

func (a *api) GetAdminVersion(context.Context, hubapi.GetAdminVersionRequestObject) (hubapi.GetAdminVersionResponseObject, error) {
	return hubapi.GetAdminVersion200JSONResponse{VersionJSONResponse: a.version()}, nil
}

func (a *api) ListSettings(ctx context.Context, _ hubapi.ListSettingsRequestObject) (hubapi.ListSettingsResponseObject, error) {
	list := []hubapi.Setting{}
	if store := a.server.opts.Settings; store != nil {
		var err error
		if list, err = store.List(); err != nil {
			return nil, &ReloadError{err}
		}
	}
	logging.FromContext(ctx).Info("settings listed", "count", len(list))
	return hubapi.ListSettings200JSONResponse{Settings: list}, nil
}

func (a *api) UpdateSetting(ctx context.Context, req hubapi.UpdateSettingRequestObject) (hubapi.UpdateSettingResponseObject, error) {
	store := a.server.opts.Settings
	if store == nil {
		return nil, &UnknownSettingError{Key: req.Setting}
	}
	setting, changed, err := store.Set(req.Setting, req.Body.Value)
	if err != nil {
		return nil, err
	}
	logging.FromContext(ctx).Info("setting saved", "name", req.Setting, "value", setting.Value, "changed", changed)
	// Run even when unchanged, so that setting it again recovers from a reload
	// that failed.
	notApplied, err := a.server.reload(ctx)
	if err != nil {
		return nil, err
	}
	result := hubapi.ResultUnchanged
	if changed {
		result = hubapi.ResultUpdated
	}
	return hubapi.UpdateSetting200JSONResponse{Result: result, Setting: setting, NotApplied: notApplied}, nil
}

func (a *api) Reload(ctx context.Context, _ hubapi.ReloadRequestObject) (hubapi.ReloadResponseObject, error) {
	notApplied, err := a.server.reload(ctx)
	if err != nil {
		return nil, err
	}
	return hubapi.Reload200JSONResponse{Result: hubapi.ResultReloaded, NotApplied: notApplied}, nil
}

// caSuffix names the certificate authority's copy after the database's.
const caSuffix = ".ca"

func (a *api) Backup(ctx context.Context, req hubapi.BackupRequestObject) (hubapi.BackupResponseObject, error) {
	path := req.Body.Path
	log := logging.FromContext(ctx)
	if !filepath.IsAbs(path) {
		return nil, &InvalidPathError{Path: path, Reason: "not absolute"}
	}
	caPath := path + caSuffix
	for _, p := range []string{path, caPath} {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			log.Info("backup refused", "path", p, "reason", "path exists")
			return nil, &PathExistsError{Path: p}
		}
	}
	// The CA is written apart and renamed into place once the database is
	// copied, so that a backup is whole or not there.
	tmp, err := os.MkdirTemp(filepath.Dir(path), "."+filepath.Base(caPath)+"-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp) // gone once renamed
	if err := a.server.ca.CopyTo(tmp); err != nil {
		return nil, err
	}
	if err := sqlite.Backup(ctx, hubconfig.DBPath(a.server.opts.Config.Home), path); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, caPath); err != nil {
		if err := os.Remove(path); err != nil {
			log.Error("backup not removed", "path", path, "err", err)
		}
		return nil, err
	}
	log.Info("ca backed up", "path", caPath)
	return hubapi.Backup201JSONResponse{Result: hubapi.ResultBackedUp, Path: path, CaPath: caPath}, nil
}
