package daemon

import (
	"context"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/doc"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/hublink"
	"github.com/srjn45/wdx/internal/logging"
)

func (s *server) GetProjectSync(ctx context.Context, req api.GetProjectSyncRequestObject) (api.GetProjectSyncResponseObject, error) {
	p, err := s.find(ctx, req.Project)
	if err != nil {
		return nil, err
	}
	st, err := s.syncView(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	return api.GetProjectSync200JSONResponse{Sync: st}, nil
}

func (s *server) UpdateProjectSync(ctx context.Context, req api.UpdateProjectSyncRequestObject) (api.UpdateProjectSyncResponseObject, error) {
	p, err := s.find(ctx, req.Project)
	if err != nil {
		return nil, err
	}
	sync := s.outbox.SyncOff
	if req.Body.On {
		sync = s.outbox.SyncOn
	}
	changed, err := sync(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	result := api.ResultUnchanged
	if changed {
		result = api.ResultChanged
		if req.Body.On {
			s.hub.Kick()
		}
	}
	st, err := s.syncView(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	logging.FromContext(ctx).Info("project sync set", "project_id", p.ID, "on", req.Body.On, "changed", changed, "pending", st.Pending)
	return api.UpdateProjectSync200JSONResponse{Result: result, Sync: st}, nil
}

// syncView is the sync of the project with id, as the API has it.
func (s *server) syncView(ctx context.Context, id string) (api.ProjectSync, error) {
	st, err := s.outbox.Status(ctx, id)
	if err != nil {
		return api.ProjectSync{}, err
	}
	v := api.ProjectSync{On: st.Sync, Pending: st.Pending, Failed: []api.ProjectSyncFailure{}, LoggedIn: s.hub.Status().LoggedIn()}
	for _, f := range st.Failed {
		v.Failed = append(v.Failed, api.ProjectSyncFailure{DocId: f.DocID, Name: f.Name, Code: f.Code})
	}
	if !st.LastPushedAt.IsZero() {
		at := st.LastPushedAt
		v.LastPushedAt = &at
	}
	if st.RepoKey != "" {
		key := st.RepoKey
		v.RepoKey = &key
	}
	return v, nil
}

// hubOutbox is the doc store's outbox as the link to the hub sends it: each
// change as the hub's API has it.
type hubOutbox struct{ *doc.Outbox }

var _ hublink.Outbox = hubOutbox{}

func (o hubOutbox) Next(ctx context.Context, limit int) ([]hubapi.DocChange, error) {
	list, err := o.Outbox.Next(ctx, limit)
	if err != nil {
		return nil, err
	}
	changes := make([]hubapi.DocChange, 0, len(list))
	for _, c := range list {
		changes = append(changes, docChange(c))
	}
	return changes, nil
}

// docChange is c as the hub's API has it: a removal carries only the doc's
// id, a revision its content too.
func docChange(c doc.Outgoing) hubapi.DocChange {
	h := hubapi.DocChange{Seq: c.Seq, DocId: c.DocID, Change: hubapi.DocChangeType(c.Change)}
	if c.Change == doc.ChangeRemoved {
		return h
	}
	d := c.Doc
	kind := hubapi.DocKind(c.Kind)
	status := string(d.Status)
	labels := d.Labels
	if labels == nil {
		labels = []string{}
	}
	created, updated := d.CreatedAt.UTC(), d.UpdatedAt.UTC()
	h.RepoKey, h.Kind, h.Name, h.Folder, h.Format = &c.RepoKey, &kind, &d.Name, &d.Folder, &d.Format
	h.Labels, h.Status, h.CreatedAt, h.UpdatedAt = &labels, &status, &created, &updated
	if c.Change == doc.ChangeRevision {
		rev, content := c.Revision, c.Content
		h.Revision, h.Content = &rev, &content
	}
	return h
}
