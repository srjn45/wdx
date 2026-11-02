package daemon

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/telemetry"
)

// startEditing starts a daemon whose editor is testEditor, or program when given,
// with a project api whose clone holds a committed file, a.go.
func startEditing(t *testing.T, program string) (*testDaemon, api.Project) {
	t.Helper()
	if program == "" {
		program = filepath.Join(t.TempDir(), "editor")
		if err := runtest.WriteExecutable(program, "#!/bin/sh\nexec sleep 60\n"); err != nil {
			t.Fatal(err)
		}
	}
	td := startOn(t, shortDir(t), telemetry.Options{}, func(o *Options) {
		o.EditorCommand = func(_, path string) []string { return []string{program, path} }
	})
	p := td.create(t, "api")
	dir := p.Worktrees[0].Path
	gittest.Git(t, dir, "branch", "-M", "main")
	writeIn(t, dir, "a.go", "package a\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-qm", "base")
	return td, p
}

func (td *testDaemon) openEditor(t *testing.T, worktree, path string) *api.OpenEditorResponse {
	t.Helper()
	r, err := td.client.OpenEditorWithResponse(context.Background(), api.OpenEditor{Project: optional("api"), Worktree: worktree, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// editors returns the editors getProject lists.
func (td *testDaemon) editors(t *testing.T) []api.Editor {
	t.Helper()
	r, err := td.client.GetProjectWithResponse(context.Background(), "api")
	if err != nil || r.JSON200 == nil || r.JSON200.Project.Editors == nil {
		t.Fatalf("get project: %v %s", err, r.Body)
	}
	return *r.JSON200.Project.Editors
}

func TestOpenEditor(t *testing.T) {
	t.Parallel()
	td, _ := startEditing(t, "")
	r := td.openEditor(t, "main", "./a.go")
	if r.JSON201 == nil || r.JSON201.Result != api.ResultCreated {
		t.Fatalf("want created, got %d: %s", r.StatusCode(), r.Body)
	}
	e := r.JSON201.Editor
	if e.Worktree != "main" || e.Path != "a.go" || e.Program != "vi" || e.State != api.TerminalStateRunning || e.TerminalId == "" {
		t.Fatalf("got %+v", e)
	}
	again := td.openEditor(t, "main", "a.go")
	if again.JSON200 == nil || again.JSON200.Result != api.ResultOpened || again.JSON200.Editor.TerminalId != e.TerminalId {
		t.Fatalf("want the one running opened, got %d: %s", again.StatusCode(), again.Body)
	}
	if list := td.editors(t); len(list) != 1 || list[0] != e {
		t.Fatalf("editors %+v", list)
	}
	td.waitProject(t, "api", "the editor", func(ev event) bool {
		return ev.project.Editors != nil && len(*ev.project.Editors) == 1 && (*ev.project.Editors)[0].TerminalId == e.TerminalId
	})
	// Not a terminal: no terminal request finds it.
	if list := td.terminals(t, nil); len(list) != 0 {
		t.Fatalf("terminals %+v", list)
	}
	got, err := td.client.GetTerminalWithResponse(context.Background(), "id:"+e.TerminalId, &api.GetTerminalParams{})
	if err != nil || got.ApplicationproblemJSONDefault == nil || got.ApplicationproblemJSONDefault.Code != api.ErrorCodeNotFound {
		t.Fatalf("want the editor's terminal not found, got %v: %s", err, got.Body)
	}
}

func TestOpenEditorRefused(t *testing.T) {
	t.Parallel()
	td, p := startEditing(t, "")
	dir := p.Worktrees[0].Path
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		worktree, path string
		code           api.ErrorCode
	}{
		{"main", "../a.go", api.ErrorCodeInvalidPath},
		{"main", "sub", api.ErrorCodeInvalidPath},
		{"main", "gone.go", api.ErrorCodeNotFound},
		{"work", "a.go", api.ErrorCodeNotFound},
	} {
		r := td.openEditor(t, tc.worktree, tc.path)
		if r.ApplicationproblemJSONDefault == nil || r.ApplicationproblemJSONDefault.Code != tc.code {
			t.Errorf("%s/%s: want %s, got %d: %s", tc.worktree, tc.path, tc.code, r.StatusCode(), r.Body)
		}
	}
	if _, err := td.client.UpdateProjectWithResponse(context.Background(), "api", api.UpdateProject{State: api.ProjectStateClosed}); err != nil {
		t.Fatal(err)
	}
	r := td.openEditor(t, "main", "a.go")
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeProjectClosed)
	if list := td.editors(t); len(list) != 0 {
		t.Fatalf("editors %+v", list)
	}
}

func TestOpenEditorMissing(t *testing.T) {
	t.Parallel()
	td, _ := startEditing(t, "/nonexistent/vi")
	r := td.openEditor(t, "main", "a.go")
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusServiceUnavailable, api.ErrorCodeDependencyMissing)
	if d := r.ApplicationproblemJSONDefault.Details; d == nil || (*d)["dependency"] != "vi" {
		t.Fatalf("details %v", d)
	}
}

// TestEditorGoesWhenItExits checks an editor is gone from the project once its
// program ends.
func TestEditorGoesWhenItExits(t *testing.T) {
	t.Parallel()
	program := filepath.Join(t.TempDir(), "editor")
	if err := runtest.WriteExecutable(program, "#!/bin/sh\nwhile [ ! -e quit ]; do sleep 0.05; done\n"); err != nil {
		t.Fatal(err)
	}
	td, p := startEditing(t, program)
	if r := td.openEditor(t, "main", "a.go"); r.JSON201 == nil {
		t.Fatalf("open: %s", r.Body)
	}
	writeIn(t, p.Worktrees[0].Path, "quit", "")
	td.waitProject(t, "api", "no editor", func(ev event) bool {
		return ev.project.Editors != nil && len(*ev.project.Editors) == 0
	})
}

func TestCloseProjectStopsEditors(t *testing.T) {
	t.Parallel()
	td, _ := startEditing(t, "")
	ctx := context.Background()
	e := td.openEditor(t, "main", "a.go").JSON201.Editor
	refused, err := td.client.UpdateProjectWithResponse(ctx, "api", api.UpdateProject{State: api.ProjectStateClosed})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, refused.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeProjectInUse)
	if d := refused.ApplicationproblemJSONDefault.Details; d == nil || (*d)["editors"] == nil {
		t.Fatalf("details %v", d)
	}
	closed, err := td.client.UpdateProjectWithResponse(ctx, "api", api.UpdateProject{State: api.ProjectStateClosed, StopRunning: optional(true)})
	if err != nil || closed.JSON200 == nil {
		t.Fatalf("close: %v %s", err, closed.Body)
	}
	if s := closed.JSON200.StoppedEditors; s == nil || len(*s) != 1 || (*s)[0].TerminalId != e.TerminalId || (*s)[0].State != api.TerminalStateStopped {
		t.Fatalf("stopped editors %v", s)
	}
	opened, err := td.client.UpdateProjectWithResponse(ctx, "api", api.UpdateProject{State: api.ProjectStateOpen})
	if err != nil || opened.JSON200 == nil || opened.JSON200.Started != nil {
		t.Fatalf("open: %v %s", err, opened.Body)
	}
	if list := td.editors(t); len(list) != 0 {
		t.Fatalf("an editor started again: %+v", list)
	}
}

func TestRemoveWorktreeStopsEditors(t *testing.T) {
	t.Parallel()
	td, p := startEditing(t, "")
	ctx := context.Background()
	created, err := td.client.CreateAgentWithResponse(ctx, api.CreateAgent{Project: optional("api"), Dir: p.Worktrees[0].Path, Name: optional("worker"), Worktree: optional("work")})
	if err != nil || created.JSON201 == nil {
		t.Fatalf("create: %v, %s", err, created.Body)
	}
	inWork := td.openEditor(t, "work", "a.go").JSON201.Editor
	inMain := td.openEditor(t, "main", "a.go").JSON201.Editor
	r, err := td.client.RemoveWorktreeWithResponse(ctx, "work", &api.RemoveWorktreeParams{Project: optional("api")})
	if err != nil || r.JSON200 == nil {
		t.Fatalf("remove: %v, %s", err, r.Body)
	}
	if s := r.JSON200.StoppedEditors; len(s) != 1 || s[0].TerminalId != inWork.TerminalId || s[0].Worktree != "work" {
		t.Fatalf("stopped editors %+v", s)
	}
	if list := td.editors(t); len(list) != 1 || list[0].TerminalId != inMain.TerminalId {
		t.Fatalf("editors %+v", list)
	}
}

func TestReloadEditor(t *testing.T) {
	t.Parallel()
	program := filepath.Join(t.TempDir(), "editor")
	if err := runtest.WriteExecutable(program, "#!/bin/sh\nexec sleep 60\n"); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var started []string
	td := startOn(t, shortDir(t), telemetry.Options{}, func(o *Options) {
		o.EditorCommand = func(name, path string) []string {
			mu.Lock()
			defer mu.Unlock()
			started = append(started, name)
			return []string{program, path}
		}
		o.Reload = func() (Settings, error) { return Settings{Editor: "nano"}, nil }
	})
	td.create(t, "api")
	if r := reload(t, td); r.JSON200 == nil {
		t.Fatalf("reload: %s", r.Body)
	}
	dir := td.editorProject(t)
	r, err := td.client.OpenEditorWithResponse(context.Background(), api.OpenEditor{Dir: optional(dir), Worktree: "main", Path: "a.go"})
	mu.Lock()
	defer mu.Unlock()
	if err != nil || r.JSON201 == nil || r.JSON201.Editor.Program != "nano" || len(started) != 1 || started[0] != "nano" {
		t.Fatalf("want nano started, got %v %v: %s", started, err, r.Body)
	}
}

// editorProject puts a.go in the clone of api and returns the clone.
func (td *testDaemon) editorProject(t *testing.T) string {
	t.Helper()
	r, err := td.client.GetProjectWithResponse(context.Background(), "api")
	if err != nil || r.JSON200 == nil {
		t.Fatalf("get project: %v", err)
	}
	dir := r.JSON200.Project.Worktrees[0].Path
	writeIn(t, dir, "a.go", "package a\n")
	return dir
}
