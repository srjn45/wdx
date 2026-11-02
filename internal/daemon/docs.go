package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/doc"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/project"
)

func (s *server) ListDocs(ctx context.Context, req api.ListDocsRequestObject) (api.ListDocsResponseObject, error) {
	p, err := s.docProject(ctx, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	list, err := s.docs.List(ctx, p.ID, deref(req.Params.Labels), doc.Status(deref(req.Params.Status)))
	if err != nil {
		return nil, err
	}
	return api.ListDocs200JSONResponse{Docs: docViews(list)}, nil
}

func (s *server) CreateDoc(ctx context.Context, req api.CreateDocRequestObject) (api.CreateDocResponseObject, error) {
	b := req.Body
	p, err := s.docProject(ctx, b.Project, b.Dir)
	if err != nil {
		return nil, err
	}
	format := "md"
	if b.Format != nil {
		format = *b.Format
	}
	d, err := s.docs.Create(ctx, p.ID, doc.New{
		Name:    b.Name,
		Format:  format,
		Labels:  deref(b.Labels),
		Status:  doc.Status(deref(b.Status)),
		Content: []byte(deref(b.Content)),
	})
	if err != nil {
		return nil, err
	}
	return api.CreateDoc201JSONResponse{Result: api.ResultCreated, Doc: docView(d)}, nil
}

func (s *server) GetDoc(ctx context.Context, req api.GetDocRequestObject) (api.GetDocResponseObject, error) {
	d, err := s.findDoc(ctx, req.Doc, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	content, r, err := s.docs.Content(ctx, d.ID, int(deref(req.Params.Revision)))
	if err != nil {
		return nil, err
	}
	logging.FromContext(ctx).Info("doc read", "doc_id", d.ID, "name", d.Name, "revision", r.Number, "size", r.Size)
	return api.GetDoc200JSONResponse{Doc: docView(d), Revision: int64(r.Number), Content: string(content)}, nil
}

func (s *server) UpdateDoc(ctx context.Context, req api.UpdateDocRequestObject) (api.UpdateDocResponseObject, error) {
	b := req.Body
	if b.Name != nil && (b.Status != nil || b.AddLabels != nil || b.RemoveLabels != nil) {
		return nil, &requestError{err: errors.New("give name, or status and labels, not both")}
	}
	d, err := s.findDoc(ctx, req.Doc, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	changed := false
	if b.Name != nil {
		// Renaming to the name it has, case and all, writes nothing.
		if *b.Name != d.Name {
			if d, err = s.docs.Rename(ctx, d.ID, *b.Name); err != nil {
				return nil, err
			}
			changed = true
		}
	} else {
		d, changed, err = s.docs.Set(ctx, d.ID, doc.Change{
			Status:       doc.Status(deref(b.Status)),
			AddLabels:    deref(b.AddLabels),
			RemoveLabels: deref(b.RemoveLabels),
		})
		if err != nil {
			return nil, err
		}
	}
	result := api.ResultUnchanged
	if changed {
		result = api.ResultUpdated
	}
	return api.UpdateDoc200JSONResponse{Result: result, Doc: docView(d)}, nil
}

func (s *server) UpdateDocContent(ctx context.Context, req api.UpdateDocContentRequestObject) (api.UpdateDocContentResponseObject, error) {
	d, err := s.findDoc(ctx, req.Doc, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	u, err := s.docs.Update(ctx, d.ID, int(req.Body.Base), []byte(req.Body.Content))
	if err != nil {
		return nil, err
	}
	r := api.UpdateDocContent200JSONResponse{Result: api.ResultUnchanged, Doc: docView(u.Doc)}
	if u.Changed {
		previous := api.DocStatus(u.Previous)
		r.Result, r.PreviousStatus = api.ResultUpdated, &previous
	}
	return r, nil
}

func (s *server) ListDocRevisions(ctx context.Context, req api.ListDocRevisionsRequestObject) (api.ListDocRevisionsResponseObject, error) {
	d, err := s.findDoc(ctx, req.Doc, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	list, err := s.docs.Revisions(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	views := make([]api.DocRevision, 0, len(list))
	for _, r := range list {
		views = append(views, api.DocRevision{Number: int64(r.Number), Size: int64(r.Size), CreatedAt: r.CreatedAt.UTC()})
	}
	return api.ListDocRevisions200JSONResponse{Revisions: views}, nil
}

func (s *server) DeleteDoc(ctx context.Context, req api.DeleteDocRequestObject) (api.DeleteDocResponseObject, error) {
	d, err := s.findDoc(ctx, req.Doc, req.Params.Project, req.Params.Dir)
	if err != nil {
		return nil, err
	}
	if d, err = s.docs.Remove(ctx, d.ID); err != nil {
		return nil, err
	}
	return api.DeleteDoc200JSONResponse{Result: api.ResultRemoved, Doc: docView(d)}, nil
}

// docProject is the project ref names, or the one whose worktree holds dir: a
// doc's project is never left to be guessed.
func (s *server) docProject(ctx context.Context, ref, dir *string) (project.Project, error) {
	if err := exclusive(ref, dir); err != nil {
		return project.Project{}, err
	}
	if ref == nil && dir == nil {
		return project.Project{}, &requestError{err: errors.New("give project or dir: docs belong to a project")}
	}
	return s.projectOf(ctx, ref, dir)
}

// findDoc returns the doc ref names: "id:" and its id, or its name in the project
// named by projectRef or holding dir. A project given with an id must be the
// doc's.
func (s *server) findDoc(ctx context.Context, ref string, projectRef, dir *string) (doc.Doc, error) {
	if err := exclusive(projectRef, dir); err != nil {
		return doc.Doc{}, err
	}
	id, byID := strings.CutPrefix(ref, idPrefix)
	if !byID && projectRef == nil && dir == nil {
		return doc.Doc{}, &requestError{err: fmt.Errorf("doc name %q needs project or dir: names are unique only within a project", ref)}
	}
	var p project.Project
	if projectRef != nil || dir != nil {
		var err error
		if p, err = s.projectOf(ctx, projectRef, dir); err != nil {
			return doc.Doc{}, err
		}
	}
	if !byID {
		return s.docs.GetByName(ctx, p.ID, ref)
	}
	d, err := s.docs.Get(ctx, id)
	if err == nil && p.ID != "" && d.ProjectID != p.ID {
		return doc.Doc{}, fmt.Errorf("%w: %q is not in project %s", doc.ErrNotFound, id, p.Name)
	}
	return d, err
}

func docViews(list []doc.Doc) []api.Doc {
	views := make([]api.Doc, 0, len(list))
	for _, d := range list {
		views = append(views, docView(d))
	}
	return views
}

func docView(d doc.Doc) api.Doc {
	return api.Doc{
		Id:          d.ID,
		Name:        d.Name,
		ProjectId:   d.ProjectID,
		ProjectName: d.ProjectName,
		Format:      d.Format,
		Labels:      d.Labels,
		Status:      api.DocStatus(d.Status),
		Revision:    int64(d.Revision),
		Size:        int64(d.Size),
		CreatedAt:   d.CreatedAt.UTC(),
		UpdatedAt:   d.UpdatedAt.UTC(),
	}
}
