package daemon

import (
	"context"

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
	if err := s.tabs.Replace(ctx, p.ID, tabs); err != nil {
		return nil, err
	}
	return api.ReplaceTabs200JSONResponse{Result: api.ResultUpdated, Tabs: tabViews(tabs)}, nil
}

// tabViews are the tabs as the API shows them.
func tabViews(tabs []tidestate.Tab) []api.Tab {
	views := make([]api.Tab, len(tabs))
	for i, t := range tabs {
		views[i] = api.Tab{Kind: api.TabKind(t.Kind), AgentId: textOrNull(t.AgentID), DocId: textOrNull(t.DocID), Worktree: textOrNull(t.Worktree), Path: textOrNull(t.Path)}
	}
	return views
}
