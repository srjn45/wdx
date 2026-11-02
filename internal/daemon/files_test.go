package daemon

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

// TestWorktreeFileChanges creates, renames and deletes files and directories in a
// worktree, and lists a directory that holds nothing.
func TestWorktreeFileChanges(t *testing.T) {
	t.Parallel()
	td, p := startEditing(t, "")
	dir := p.Worktrees[0].Path
	ctx := context.Background()
	project := "api"
	scope := &api.CreateWorktreeFileParams{Project: &project}
	yes := true

	made, err := td.client.CreateWorktreeFileWithResponse(ctx, "main", scope, api.WorktreeFileCreate{Path: "src", Directory: &yes})
	if err != nil || made.JSON201 == nil || made.JSON201.Result != api.ResultCreated || made.JSON201.Path != "src" {
		t.Fatalf("directory: %v, %s", err, made.Body)
	}
	files, err := td.client.ListWorktreeFilesWithResponse(ctx, "main", &api.ListWorktreeFilesParams{Project: &project})
	if err != nil || files.JSON200 == nil || !slices.Equal(files.JSON200.EmptyDirs, []string{"src"}) {
		t.Fatalf("the empty directory listed: %v, %s", err, files.Body)
	}
	made, err = td.client.CreateWorktreeFileWithResponse(ctx, "main", scope, api.WorktreeFileCreate{Path: "src/./b.go"})
	if err != nil || made.JSON201 == nil || made.JSON201.Path != "src/b.go" {
		t.Fatalf("file: %v, %s", err, made.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, "src/b.go")); err != nil {
		t.Fatal(err)
	}

	renamed, err := td.client.RenameWorktreeFileWithResponse(ctx, "main", &api.RenameWorktreeFileParams{Project: &project}, api.WorktreeFileRename{Path: "src/b.go", Name: "c.go"})
	if err != nil || renamed.JSON200 == nil || renamed.JSON200.Result != api.ResultRenamed || renamed.JSON200.Path != "src/c.go" || renamed.JSON200.From == nil || *renamed.JSON200.From != "src/b.go" {
		t.Fatalf("rename: %v, %s", err, renamed.Body)
	}
	removed, err := td.client.RemoveWorktreeFileWithResponse(ctx, "main", &api.RemoveWorktreeFileParams{Project: &project, Path: "src"})
	if err != nil || removed.JSON200 == nil || removed.JSON200.Result != api.ResultRemoved || removed.JSON200.Path != "src" {
		t.Fatalf("remove: %v, %s", err, removed.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, "src")); !os.IsNotExist(err) {
		t.Fatalf("src still there: %v", err)
	}

	for _, tc := range []struct {
		what string
		do   func() (int, *api.Problem, []byte)
		want int
		code api.ErrorCode
	}{
		{"create where a file is", func() (int, *api.Problem, []byte) {
			r, _ := td.client.CreateWorktreeFileWithResponse(ctx, "main", scope, api.WorktreeFileCreate{Path: "a.go"})
			return r.StatusCode(), r.ApplicationproblemJSONDefault, r.Body
		}, http.StatusConflict, api.ErrorCodePathExists},
		{"create in a directory not there", func() (int, *api.Problem, []byte) {
			r, _ := td.client.CreateWorktreeFileWithResponse(ctx, "main", scope, api.WorktreeFileCreate{Path: "nope/x"})
			return r.StatusCode(), r.ApplicationproblemJSONDefault, r.Body
		}, http.StatusNotFound, api.ErrorCodeNotFound},
		{"create outside", func() (int, *api.Problem, []byte) {
			r, _ := td.client.CreateWorktreeFileWithResponse(ctx, "main", scope, api.WorktreeFileCreate{Path: "../x"})
			return r.StatusCode(), r.ApplicationproblemJSONDefault, r.Body
		}, http.StatusUnprocessableEntity, api.ErrorCodeInvalidPath},
		{"create in an unknown worktree", func() (int, *api.Problem, []byte) {
			r, _ := td.client.CreateWorktreeFileWithResponse(ctx, "nope", scope, api.WorktreeFileCreate{Path: "x"})
			return r.StatusCode(), r.ApplicationproblemJSONDefault, r.Body
		}, http.StatusNotFound, api.ErrorCodeNotFound},
		{"rename to a name with a slash", func() (int, *api.Problem, []byte) {
			r, _ := td.client.RenameWorktreeFileWithResponse(ctx, "main", &api.RenameWorktreeFileParams{Project: &project}, api.WorktreeFileRename{Path: "a.go", Name: "x/y"})
			return r.StatusCode(), r.ApplicationproblemJSONDefault, r.Body
		}, http.StatusUnprocessableEntity, api.ErrorCodeInvalidName},
		{"rename what is not there", func() (int, *api.Problem, []byte) {
			r, _ := td.client.RenameWorktreeFileWithResponse(ctx, "main", &api.RenameWorktreeFileParams{Project: &project}, api.WorktreeFileRename{Path: "gone", Name: "x"})
			return r.StatusCode(), r.ApplicationproblemJSONDefault, r.Body
		}, http.StatusNotFound, api.ErrorCodeNotFound},
		{"delete the worktree", func() (int, *api.Problem, []byte) {
			r, _ := td.client.RemoveWorktreeFileWithResponse(ctx, "main", &api.RemoveWorktreeFileParams{Project: &project, Path: "."})
			return r.StatusCode(), r.ApplicationproblemJSONDefault, r.Body
		}, http.StatusUnprocessableEntity, api.ErrorCodeInvalidPath},
		{"a request with no path", func() (int, *api.Problem, []byte) {
			r, _ := td.client.CreateWorktreeFileWithResponse(ctx, "main", scope, api.WorktreeFileCreate{})
			return r.StatusCode(), r.ApplicationproblemJSONDefault, r.Body
		}, http.StatusBadRequest, api.ErrorCodeUsage},
	} {
		status, problem, body := tc.do()
		if status != tc.want || problem == nil || problem.Code != tc.code {
			t.Errorf("%s: %d %s", tc.what, status, body)
		}
	}
}

// TestWorktreeFileChangesRefusedForAnEditor: what an editor has open is neither
// renamed nor deleted, nor the directory it is in; the rest is.
func TestWorktreeFileChangesRefusedForAnEditor(t *testing.T) {
	t.Parallel()
	td, p := startEditing(t, "")
	dir := p.Worktrees[0].Path
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeIn(t, dir, "src/a.go", "package a\n")
	writeIn(t, dir, "src2", "not the directory\n")
	if r := td.openEditor(t, "main", "src/a.go"); r.JSON201 == nil {
		t.Fatalf("editor: %d %s", r.StatusCode(), r.Body)
	}
	ctx := context.Background()
	project := "api"
	for _, path := range []string{"src/a.go", "src", "./src/"} {
		r, err := td.client.RenameWorktreeFileWithResponse(ctx, "main", &api.RenameWorktreeFileParams{Project: &project}, api.WorktreeFileRename{Path: path, Name: "x"})
		if err != nil {
			t.Fatal(err)
		}
		wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeEditorRunning)
		d, err := td.client.RemoveWorktreeFileWithResponse(ctx, "main", &api.RemoveWorktreeFileParams{Project: &project, Path: path})
		if err != nil {
			t.Fatal(err)
		}
		wantProblem(t, d.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeEditorRunning)
	}
	if _, err := os.Stat(filepath.Join(dir, "src/a.go")); err != nil {
		t.Fatalf("the file: %v", err)
	}
	r, err := td.client.RemoveWorktreeFileWithResponse(ctx, "main", &api.RemoveWorktreeFileParams{Project: &project, Path: "src2"})
	if err != nil || r.JSON200 == nil {
		t.Fatalf("a file whose name starts as the directory's: %v, %s", err, r.Body)
	}
}
