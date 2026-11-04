package terminal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/waittest"
)

// editorScript is the test's editor: it says which file it was started on, in the
// directory it runs in, and stays. The file is renamed into place, as a redirection
// creates it empty before printf writes, and a test waiting for it could read that.
const editorScript = "#!/bin/sh\nprintf %s \"$1\" > editing.new && mv editing.new editing\nexec sleep 60\n"

// editorFixture is a fixture whose shell runs a command given to it, and whose
// editor is editorScript, named vi.
func editorFixture(t *testing.T) (fixture, *Supervisor, string) {
	t.Helper()
	f := newFixture(t)
	f.opts.Shell = runtest.LoginShell(t)
	f.opts.HistoryLines = 100 // which an editor does not keep
	program := filepath.Join(t.TempDir(), "editor")
	if err := runtest.WriteExecutable(program, editorScript); err != nil {
		t.Fatal(err)
	}
	f.opts.EditorCommand = func(_, path string) []string { return []string{program, path} }
	return f, f.supervisor(t), f.project(t, "warden")
}

// openEditor opens the editor on path in dir and waits until it runs there.
func openEditor(t *testing.T, s *Supervisor, projectID, dir, path string) Terminal {
	t.Helper()
	e, created, err := s.OpenEditor(context.Background(), projectID, dir, path)
	if err != nil || !created {
		t.Fatalf("created %v, %v", created, err)
	}
	waitFile(t, filepath.Join(dir, "editing"))
	if got, err := os.ReadFile(filepath.Join(dir, "editing")); err != nil || string(got) != path {
		t.Fatalf("the editor was started on %q, %v; want %q", got, err, path)
	}
	return e
}

// waitRowGone waits until the row with id is deleted.
func waitRowGone(t *testing.T, s *Supervisor, id string) {
	t.Helper()
	var err error
	if !waittest.Until(t, func() bool {
		_, err = s.store.row(context.Background(), id)
		return errors.Is(err, ErrNotFound)
	}) {
		t.Fatalf("waited until the deadline for row %s to go: %v", id, err)
	}
}

func TestOpenEditor(t *testing.T) {
	t.Parallel()
	f, s, p := editorFixture(t)
	ctx := context.Background()
	d := dir(t, "")
	e := openEditor(t, s, p, d, "a.go")
	if e.Kind != KindEditor || e.Editor != "vi" || e.EditorPath != "a.go" || e.Dir != d || e.State != StateRunning {
		t.Fatalf("got %+v", e)
	}
	// What scrolled by in an editor is the file, on disk already.
	if _, err := os.Stat(holder.HistoryDir(f.home, e.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the editor keeps a history: %v", err)
	}
	again, created, err := s.OpenEditor(ctx, p, d, "a.go")
	if err != nil || created || again.ID != e.ID {
		t.Fatalf("opening it again: %+v, created %v, %v", again, created, err)
	}
	if list, err := s.store.Editors(ctx, p); err != nil || len(list) != 1 || list[0].ID != e.ID || list[0].Kind != KindEditor {
		t.Fatalf("editors %+v, %v", list, err)
	}
	// Not a terminal: the terminal flow finds no such row.
	if list, err := s.store.List(ctx, p); err != nil || len(list) != 0 {
		t.Fatalf("terminals %+v, %v", list, err)
	}
	if _, err := s.store.Get(ctx, e.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get found the editor's row: %v", err)
	}
	if _, err := s.Start(ctx, e.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Start found the editor's row: %v", err)
	}
	// Another file, or the same file in another worktree, is another editor.
	other := dir(t, "")
	if o := openEditor(t, s, p, other, "a.go"); o.ID == e.ID {
		t.Fatal("one editor for two worktrees")
	}
}

func TestEditorMissing(t *testing.T) {
	t.Parallel()
	f, _, p := editorFixture(t)
	f.opts.EditorCommand = func(_, path string) []string { return []string{"/nonexistent/vi", path} }
	s := f.supervisor(t)
	_, _, err := s.OpenEditor(context.Background(), p, dir(t, ""), "a.go")
	if e, ok := errors.AsType[*EditorMissingError](err); !ok || e.Editor != "vi" {
		t.Fatalf("got %v", err)
	}
	if list, err := s.store.rows(context.Background(), p); err != nil || len(list) != 0 {
		t.Fatalf("rows %+v, %v", list, err)
	}
}

// TestEditorEnds checks an editor's row goes however its process ends, so that it
// is never started again.
func TestEditorEnds(t *testing.T) {
	t.Parallel()
	_, s, p := editorFixture(t)
	exits := openEditor(t, s, p, dir(t, ""), "a.go")
	kill(0, exits.PID)
	waitRowGone(t, s, exits.ID)

	lost := openEditor(t, s, p, dir(t, ""), "a.go")
	kill(lost.HolderPID, 0)
	waitGone(t, lost.HolderPID)
	waitRowGone(t, s, lost.ID)
	kill(0, lost.PID)
}

func TestStopProjectStopsEditors(t *testing.T) {
	t.Parallel()
	_, s, p := editorFixture(t)
	ctx := context.Background()
	e := openEditor(t, s, p, dir(t, ""), "a.go")
	stopped, err := s.StopProject(ctx, p)
	if err != nil || len(stopped) != 1 || stopped[0].ID != e.ID || stopped[0].State != StateStopped || stopped[0].Kind != KindEditor {
		t.Fatalf("stopped %+v, %v", stopped, err)
	}
	waitGone(t, e.PID)
	waitRowGone(t, s, e.ID)
	if started, err := s.StartProject(ctx, p); err != nil || len(started) != 0 {
		t.Fatalf("started %+v, %v", started, err)
	}
}

func TestStopEditors(t *testing.T) {
	t.Parallel()
	_, s, p := editorFixture(t)
	ctx := context.Background()
	removed, kept := dir(t, ""), dir(t, "")
	e := openEditor(t, s, p, removed, "a.go")
	other := openEditor(t, s, p, kept, "a.go")
	shell := create(t, s, p, "shell", removed)
	stopped, err := s.StopEditors(ctx, p, removed)
	if err != nil || len(stopped) != 1 || stopped[0].ID != e.ID {
		t.Fatalf("stopped %+v, %v", stopped, err)
	}
	waitRowGone(t, s, e.ID)
	if got := waitState(t, s, shell.ID, StateRunning); got.PID != shell.PID {
		t.Fatalf("the shell in the worktree changed: %+v", got)
	}
	if list, err := s.store.Editors(ctx, p); err != nil || len(list) != 1 || list[0].ID != other.ID {
		t.Fatalf("editors %+v, %v", list, err)
	}
}

// TestReconcileEditors checks a daemon that starts adopts an editor still running,
// and drops the row of one that ended meanwhile.
func TestReconcileEditors(t *testing.T) {
	t.Parallel()
	f, first, p := editorFixture(t)
	ctx := context.Background()
	kept := openEditor(t, first, p, dir(t, ""), "a.go")
	crashed := openEditor(t, first, p, dir(t, ""), "b.go")
	first.Close()
	t.Cleanup(func() {
		kill(kept.HolderPID, kept.PID)
		kill(crashed.HolderPID, crashed.PID)
	})
	kill(crashed.HolderPID, crashed.PID)
	waitGone(t, crashed.HolderPID)

	second := f.supervisor(t)
	if err := second.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	waitRowState(t, second, kept.ID, StateRunning)
	if got, err := second.store.row(ctx, kept.ID); err != nil || got.PID != kept.PID || got.Kind != KindEditor {
		t.Fatalf("want the editor adopted, got %+v, %v", got, err)
	}
	waitRowGone(t, second, crashed.ID)
	// Watched again.
	kill(0, kept.PID)
	waitRowGone(t, second, kept.ID)
}

// TestStartProjectLeavesStoppedEditors checks opening a project does not start an
// editor whose stop a daemon that died left unfinished: its row goes once it ends.
func TestStartProjectLeavesStoppedEditors(t *testing.T) {
	t.Parallel()
	f, s, p := editorFixture(t)
	e := openEditor(t, s, p, dir(t, ""), "a.go")
	f.exec(t, "UPDATE terminals SET state = 'stopped' WHERE id = ?", e.ID)
	if started, err := s.StartProject(context.Background(), p); err != nil || len(started) != 0 {
		t.Fatalf("started %+v, %v", started, err)
	}
	if got, err := s.store.row(context.Background(), e.ID); err != nil || got.State != StateStopped || got.PID != e.PID {
		t.Fatalf("got %+v, %v", got, err)
	}
}
