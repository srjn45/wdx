package daemon

import (
	"context"
	"encoding/json"

	"github.com/srjn45/wdx/internal/api"
)

// tide's view, which the daemon keeps without reading (docs/TIDE_STATE.md).

func (s *server) GetTideView(ctx context.Context, _ api.GetTideViewRequestObject) (api.GetTideViewResponseObject, error) {
	view, err := s.tabs.View(ctx)
	if err != nil {
		return nil, err
	}
	v, err := viewObject(view)
	return api.GetTideView200JSONResponse{View: v}, err
}

func (s *server) ReplaceTideView(ctx context.Context, req api.ReplaceTideViewRequestObject) (api.ReplaceTideViewResponseObject, error) {
	view, err := json.Marshal(req.Body.View)
	if err != nil {
		return nil, err
	}
	if err := s.tabs.ReplaceView(ctx, view); err != nil {
		return nil, err
	}
	return api.ReplaceTideView200JSONResponse{Result: api.ResultUpdated, View: req.Body.View}, nil
}

func (s *server) GetProjectView(ctx context.Context, req api.GetProjectViewRequestObject) (api.GetProjectViewResponseObject, error) {
	p, err := s.find(ctx, req.Project)
	if err != nil {
		return nil, err
	}
	view, err := s.tabs.ProjectView(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	v, err := viewObject(view)
	return api.GetProjectView200JSONResponse{View: v}, err
}

func (s *server) ReplaceProjectView(ctx context.Context, req api.ReplaceProjectViewRequestObject) (api.ReplaceProjectViewResponseObject, error) {
	p, err := s.find(ctx, req.Project)
	if err != nil {
		return nil, err
	}
	view, err := json.Marshal(req.Body.View)
	if err != nil {
		return nil, err
	}
	if err := s.tabs.ReplaceProjectView(ctx, p.ID, view); err != nil {
		return nil, err
	}
	return api.ReplaceProjectView200JSONResponse{Result: api.ResultUpdated, View: req.Body.View}, nil
}

// viewObject is a view kept as JSON, as the API's object.
func viewObject(view json.RawMessage) (map[string]any, error) {
	v := map[string]any{}
	return v, json.Unmarshal(view, &v)
}
