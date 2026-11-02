package daemon

import (
	"context"
	"path/filepath"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/project"
	"github.com/srjn45/wdx/internal/terminal"
	"github.com/srjn45/wdx/internal/worktree"
)

func (s *server) OpenEditor(ctx context.Context, req api.OpenEditorRequestObject) (api.OpenEditorResponseObject, error) {
	b := req.Body
	p, c, err := s.worktreeScope(ctx, b.Project, b.Dir)
	if err != nil {
		return nil, err
	}
	if p.State == project.StateClosed {
		return nil, &projectClosedError{project: p}
	}
	dir, path, err := worktree.EditableFile(ctx, c, b.Worktree, b.Path)
	if err != nil {
		return nil, err
	}
	t, created, err := s.terminals.OpenEditor(ctx, p.ID, dir, path)
	if err != nil {
		return nil, err
	}
	if created {
		return api.OpenEditor201JSONResponse{Result: api.ResultCreated, Editor: editorView(p, t)}, nil
	}
	return api.OpenEditor200JSONResponse{Result: api.ResultOpened, Editor: editorView(p, t)}, nil
}

func editorViews(p project.Project, list []terminal.Terminal) []api.Editor {
	views := make([]api.Editor, 0, len(list))
	for _, t := range list {
		views = append(views, editorView(p, t))
	}
	return views
}

// editorView is the editor on the row t of the project p.
func editorView(p project.Project, t terminal.Terminal) api.Editor {
	name := filepath.Base(t.Dir)
	if t.Dir == p.ClonePath() {
		name = "main"
	}
	return api.Editor{TerminalId: t.ID, Worktree: name, Path: t.EditorPath, Program: t.Editor, State: api.TerminalState(t.State)}
}
