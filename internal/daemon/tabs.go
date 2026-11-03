package daemon

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/tidestate"
)

// ReplaceTabs makes the list given the tabs tide has open in the project.
func (s *server) ReplaceTabs(ctx context.Context, req api.ReplaceTabsRequestObject) (api.ReplaceTabsResponseObject, error) {
	p, err := s.find(ctx, req.Project)
	if err != nil {
		return nil, err
	}
	tabs := make([]tidestate.Tab, len(req.Body.Tabs))
	for i, t := range req.Body.Tabs {
		tabs[i] = tidestate.Tab{Kind: tidestate.Kind(t.Kind), AgentID: deref(t.AgentId), DocID: deref(t.DocId), Worktree: deref(t.Worktree), Path: deref(t.Path)}
	}
	revision, err := s.tabs.Replace(ctx, p.ID, tabs)
	if err != nil {
		return nil, err
	}
	return api.ReplaceTabs200JSONResponse{Result: api.ResultUpdated, Tabs: s.tabViews(p.ID, tabs), TabsRevision: revision}, nil
}

// tabViews are the project's tabs as the API shows them, each saying whether what
// it shows is gone. That is read from what the tracker last read of the project's
// worktrees, which it keeps while a stream follows them, and a file tab's file
// from the disk: a handful of tabs, beside an event. With nothing read yet, no
// tab is missing. The tracker is given what the tabs show to watch before the
// disk is looked at, so that what goes after is told.
func (s *server) tabViews(projectID string, tabs []tidestate.Tab) []api.Tab {
	statuses := s.tracker.Statuses(projectID)
	dirs := map[string]string{} // the worktrees there, by name
	for _, w := range statuses {
		if !w.Missing {
			dirs[w.Name] = w.Path
		}
	}
	if statuses != nil {
		var files, worktrees []string
		for _, t := range tabs {
			if dir, there := dirs[t.Worktree]; there {
				worktrees = append(worktrees, dir)
				if t.Kind == tidestate.KindFile {
					files = append(files, filepath.Join(dir, t.Path))
				}
			}
		}
		s.tracker.WatchTabs(projectID, files, worktrees)
	}
	views := make([]api.Tab, len(tabs))
	for i, t := range tabs {
		missing := false
		if statuses != nil && t.Worktree != "" {
			dir, there := dirs[t.Worktree]
			missing = !there
			if there && t.Kind == tidestate.KindFile {
				_, err := os.Lstat(filepath.Join(dir, t.Path))
				missing = errors.Is(err, fs.ErrNotExist)
			}
		}
		views[i] = api.Tab{Kind: api.TabKind(t.Kind), AgentId: textOrNull(t.AgentID), DocId: textOrNull(t.DocID),
			Worktree: textOrNull(t.Worktree), Path: textOrNull(t.Path), Missing: &missing}
	}
	return views
}
