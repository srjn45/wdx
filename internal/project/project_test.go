package project

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/waittest"
)

func TestMain(m *testing.M) {
	gittest.Isolate()
	m.Run()
}

func newStore(t *testing.T) *Store {
	t.Helper()
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return NewStore(d, filepath.Join(realPath(t, t.TempDir()), "projects"))
}

// gitRepo creates a git repo named name; origin is added when non-empty.
func gitRepo(t *testing.T, name, origin string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, dir, "init", "-q")
	if origin != "" {
		gittest.Git(t, dir, "remote", "add", "origin", origin)
	}
	return realPath(t, dir)
}

func worktree(t *testing.T, repo string) string {
	t.Helper()
	gittest.Git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(t.TempDir(), "wt")
	gittest.Git(t, repo, "worktree", "add", "-q", wt)
	return realPath(t, wt)
}

// realPath resolves symlinks (e.g. /tmp on macOS) so paths compare equal to git's output.
func realPath(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func paths(p Project) []string {
	var out []string
	for _, w := range p.Worktrees {
		out = append(out, w.Path)
	}
	return out
}

func open(t *testing.T, s *Store, path, name string) Project {
	t.Helper()
	o, err := s.OpenLocal(context.Background(), path, name)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	return o.Project
}

func openGit(t *testing.T, s *Store, url, name string) Opened {
	t.Helper()
	o, err := s.OpenGit(context.Background(), url, name, io.Discard)
	if err != nil {
		t.Fatalf("open --git %s: %v", url, err)
	}
	return o
}

func countRows(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOpenLocal(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	repo := gitRepo(t, "myrepo", "git@github.com:me/myrepo.git")

	p := open(t, s, repo, "")
	if p.Name != "myrepo" || p.OriginURL != "git@github.com:me/myrepo.git" || p.State != StateOpen {
		t.Fatalf("unexpected project: %+v", p)
	}
	if len(p.Worktrees) != 1 || p.Worktrees[0].Path != repo || p.Worktrees[0].GitCommonDir != filepath.Join(repo, ".git") {
		t.Fatalf("unexpected worktrees: %+v", p.Worktrees)
	}
}

func TestOpenLocalRegisteredOnlyFirstTime(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	repo := gitRepo(t, "myrepo", "")

	for i, want := range []bool{true, false} {
		o, err := s.OpenLocal(context.Background(), repo, "")
		if err != nil {
			t.Fatal(err)
		}
		if o.Registered != want {
			t.Fatalf("open #%d: Registered = %v, want %v", i+1, o.Registered, want)
		}
	}
}

func TestOpenLocalSubdirResolvesToRoot(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	repo := gitRepo(t, "myrepo", "")
	sub := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	p := open(t, s, sub, "")
	if got := paths(p); len(got) != 1 || got[0] != repo || p.OriginURL != "" {
		t.Fatalf("unexpected project: %+v", p)
	}
}

func TestOpenLocalIsIdempotent(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	repo := gitRepo(t, "myrepo", "")

	first := open(t, s, repo, "")
	second := open(t, s, repo, "")
	if second.ID != first.ID || second.State != StateOpen || len(second.Worktrees) != 1 {
		t.Fatalf("want same open project, got %+v (first %+v)", second, first)
	}
	if n := countRows(t, s, "projects"); n != 1 {
		t.Fatalf("want 1 project, got %d", n)
	}
}

func TestOpenLocalReopensClosed(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	repo := gitRepo(t, "myrepo", "")

	p := open(t, s, repo, "")
	if _, err := s.Close(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	if p = open(t, s, repo, ""); p.State != StateOpen {
		t.Fatalf("want reopened, got %s", p.State)
	}
}

func TestWorktreeJoinsProject(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	repo := gitRepo(t, "myrepo", "") // no origin: matched by git common dir
	wt := worktree(t, repo)

	p := open(t, s, repo, "")
	q := open(t, s, wt, "")
	if q.ID != p.ID {
		t.Fatalf("worktree opened as a new project %s, want %s", q.ID, p.ID)
	}
	if got := paths(q); len(got) != 2 || got[0] != repo || got[1] != wt {
		t.Fatalf("want worktrees [%s %s], got %v", repo, wt, got)
	}
}

func TestCloneOfSameOriginJoinsProject(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	a := gitRepo(t, "app", "git@github.com:me/app.git")
	b := gitRepo(t, "app-copy", "https://github.com/me/app")

	p := open(t, s, a, "")
	q := open(t, s, b, "")
	if q.ID != p.ID || q.Name != "app" || len(q.Worktrees) != 2 {
		t.Fatalf("clone opened as %+v, want project %s with 2 worktrees", q, p.ID)
	}
}

func TestOriginAddedLater(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	repo := gitRepo(t, "myrepo", "")
	open(t, s, repo, "")

	gittest.Git(t, repo, "remote", "add", "origin", "https://example.com/r.git")
	p := open(t, s, repo, "")
	if p.OriginURL != "https://example.com/r.git" {
		t.Fatalf("origin not picked up: %+v", p)
	}
	// A clone of that origin now joins the project.
	clone := gitRepo(t, "other", "https://example.com/r")
	if q := open(t, s, clone, ""); q.ID != p.ID {
		t.Fatalf("clone opened as %s, want %s", q.ID, p.ID)
	}

	// Another remote-less repo that later gains the same origin conflicts.
	stray := gitRepo(t, "stray", "")
	open(t, s, stray, "")
	gittest.Git(t, stray, "remote", "add", "origin", "https://example.com/r.git")
	_, err := s.OpenLocal(context.Background(), stray, "")
	if m, ok := errors.AsType[*ProjectMismatchError](err); !ok || m.ByOrigin.ID != p.ID || m.Root != stray {
		t.Fatalf("want ProjectMismatchError for %s, got %v", p.ID, err)
	}
}

func TestOpenLocalNameCollision(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	a, b := gitRepo(t, "api", ""), gitRepo(t, "API", "")

	p := open(t, s, a, "")
	_, err := s.OpenLocal(context.Background(), b, "")
	if taken, ok := errors.AsType[*NameTakenError](err); !ok || taken.Project.ID != p.ID {
		t.Fatalf("want NameTakenError for %s, got %v", p.ID, err)
	}
	if p := open(t, s, b, "api-2"); p.Name != "api-2" {
		t.Fatalf("want name api-2, got %q", p.Name)
	}
	if p := open(t, s, b, ""); p.Name != "api-2" {
		t.Fatalf("reopen without --name should keep api-2, got %q", p.Name)
	}
}

func TestReinitInSamePathKeepsProject(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	repo := gitRepo(t, "myrepo", "")
	old := open(t, s, repo, "")

	// A new repo at the same path has the same .git path, so it is the same project.
	if err := os.RemoveAll(filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, repo, "init", "-q")
	gittest.Git(t, repo, "remote", "add", "origin", "https://example.com/new.git")

	p := open(t, s, repo, "")
	if p.ID != old.ID || p.OriginURL != "https://example.com/new.git" || countRows(t, s, "project_worktrees") != 1 {
		t.Fatalf("want same project with new origin, got %+v (old %+v)", p, old)
	}
}

func TestOpenLocalNotGitRepo(t *testing.T) {
	t.Parallel()
	_, err := newStore(t).OpenLocal(context.Background(), t.TempDir(), "")
	if !errors.Is(err, ErrNotGitRepo) {
		t.Fatalf("want ErrNotGitRepo, got %v", err)
	}
}

func TestOpenLocalMissingDir(t *testing.T) {
	t.Parallel()
	_, err := newStore(t).OpenLocal(context.Background(), filepath.Join(t.TempDir(), "nope"), "")
	if _, ok := errors.AsType[*InvalidPathError](err); !ok {
		t.Fatalf("want InvalidPathError, got %v", err)
	}
}

func TestClose(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	p := open(t, s, gitRepo(t, "MyRepo", ""), "")

	closed, err := s.Close(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closed.ID != p.ID || closed.State != StateClosed {
		t.Fatalf("unexpected project %+v", closed)
	}
}

func TestCloseNotFound(t *testing.T) {
	t.Parallel()
	_, err := newStore(t).Close(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestRemoveDeletesProjectAndWorktrees(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	repo := gitRepo(t, "myrepo", "")
	p := open(t, s, repo, "")
	open(t, s, worktree(t, repo), "")
	keep := open(t, s, gitRepo(t, "other", ""), "")

	removed, err := s.Remove(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if removed.ID != p.ID || len(removed.Worktrees) != 2 {
		t.Fatalf("unexpected removed project: %+v", removed)
	}
	if _, err := s.GetByID(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound after remove, got %v", err)
	}
	if n := countRows(t, s, "project_worktrees"); n != 1 {
		t.Fatalf("want only the other project's worktree left, got %d rows", n)
	}
	if _, err := s.GetByID(ctx, keep.ID); err != nil {
		t.Fatalf("other project affected: %v", err)
	}
	// Opening the path again starts a fresh project.
	if q := open(t, s, repo, ""); q.ID == p.ID {
		t.Fatalf("want a new project id after remove, got %s", q.ID)
	}
}

func TestRemoveNotFound(t *testing.T) {
	t.Parallel()
	_, err := newStore(t).Remove(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestOpenLocalDropsMissingCheckouts(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	repo := gitRepo(t, "myrepo", "")
	wt := worktree(t, repo)
	open(t, s, repo, "")
	open(t, s, wt, "")

	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	o, err := s.OpenLocal(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Dropped) != 1 || o.Dropped[0].Path != wt {
		t.Fatalf("want %s dropped, got %+v", wt, o.Dropped)
	}
	if got := paths(o.Project); len(got) != 1 || got[0] != repo {
		t.Fatalf("want only %s left, got %v", repo, got)
	}
}

func TestOpenLocalNewCloneReplacesDeletedOne(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	old := gitRepo(t, "app", "git@github.com:me/app.git")
	p := open(t, s, old, "")
	if err := os.RemoveAll(old); err != nil {
		t.Fatal(err)
	}

	clone := gitRepo(t, "app2", "https://github.com/me/app")
	o, err := s.OpenLocal(context.Background(), clone, "")
	if err != nil {
		t.Fatal(err)
	}
	if o.ID != p.ID || len(o.Dropped) != 1 || o.Dropped[0].Path != old {
		t.Fatalf("want project %s with %s dropped, got %+v", p.ID, old, o)
	}
	if got := paths(o.Project); len(got) != 1 || got[0] != clone {
		t.Fatalf("want only %s left, got %v", clone, got)
	}
}

func TestOpenGitClones(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	remote := gittest.Bare(t, "app")

	o := openGit(t, s, remote, "")
	want := filepath.Join(s.projectsDir, "app")
	if !o.Cloned || !o.Registered || o.Name != "app" || o.OriginURL != remote {
		t.Fatalf("unexpected project: %+v", o)
	}
	if got := paths(o.Project); len(got) != 1 || got[0] != want {
		t.Fatalf("want checkout %s, got %v", want, got)
	}
}

func TestOpenGitOpensKnownProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	repo := gitRepo(t, "app", "git@gitlab.com:group/sub/app.git")
	p := open(t, s, repo, "")
	if _, err := s.Close(ctx, p.ID); err != nil {
		t.Fatal(err)
	}

	o := openGit(t, s, "https://gitlab.com/group/sub/app", "")
	if o.Cloned || o.Registered || o.ID != p.ID || o.State != StateOpen {
		t.Fatalf("want project %s reopened without cloning, got %+v", p.ID, o)
	}
	if _, err := os.Stat(s.projectsDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nothing should be cloned, stat %s: %v", s.projectsDir, err)
	}
}

func TestOpenGitReusesLeftoverClone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	remote := gittest.Bare(t, "app")
	p := openGit(t, s, remote, "")
	if _, err := s.Remove(ctx, p.ID); err != nil {
		t.Fatal(err)
	}

	o := openGit(t, s, remote, "")
	if o.Cloned || !o.Registered || o.ID == p.ID || paths(o.Project)[0] != paths(p.Project)[0] {
		t.Fatalf("want new project on the existing clone, got %+v", o)
	}
}

func TestOpenGitEmptyURL(t *testing.T) {
	t.Parallel()
	if _, err := newStore(t).OpenGit(context.Background(), " ", "", io.Discard); !errors.Is(err, ErrEmptyURL) {
		t.Fatalf("want ErrEmptyURL, got %v", err)
	}
}

func TestOpenGitDirectoryTaken(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	if err := os.MkdirAll(filepath.Join(s.projectsDir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := s.OpenGit(context.Background(), gittest.Bare(t, "app"), "", io.Discard)
	if exists, ok := errors.AsType[*PathExistsError](err); !ok || exists.Path != filepath.Join(s.projectsDir, "app") {
		t.Fatalf("want PathExistsError, got %v", err)
	}
	if o := openGit(t, s, gittest.Bare(t, "app"), "app-2"); o.Name != "app-2" {
		t.Fatalf("want name app-2, got %q", o.Name)
	}
}

func TestOpenGitNameTakenDoesNotClone(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	p := open(t, s, gitRepo(t, "app", ""), "")

	_, err := s.OpenGit(context.Background(), gittest.Bare(t, "app"), "", io.Discard)
	if taken, ok := errors.AsType[*NameTakenError](err); !ok || taken.Project.ID != p.ID {
		t.Fatalf("want NameTakenError for %s, got %v", p.ID, err)
	}
	if _, err := os.Stat(s.projectsDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nothing should be cloned, stat %s: %v", s.projectsDir, err)
	}
}

func TestOpenGitRejectsPathAsName(t *testing.T) {
	t.Parallel()
	_, err := newStore(t).OpenGit(context.Background(), gittest.Bare(t, "app"), "../app", io.Discard)
	if _, ok := errors.AsType[*InvalidNameError](err); !ok {
		t.Fatalf("want InvalidNameError, got %v", err)
	}
}

func TestOpenGitCloneFailureLeavesNothing(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	missing := filepath.Join(t.TempDir(), "nope.git")

	_, err := s.OpenGit(context.Background(), missing, "", io.Discard)
	if failed, ok := errors.AsType[*git.CloneError](err); !ok || failed.Output == "" {
		t.Fatalf("want CloneError with git's output, got %v", err)
	}
	if n := countRows(t, s, "projects"); n != 0 {
		t.Fatalf("want no projects, got %d", n)
	}
	if _, err := os.Stat(filepath.Join(s.projectsDir, "nope")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want no leftover directory, got %v", err)
	}
}

// TestOpenGitCloneKilled kills a clone midway, as a daemon giving up at
// shutdown does: nothing is left that a later open would take for a
// checkout, and the next open clones afresh.
func TestOpenGitCloneKilled(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	remote := gittest.Bare(t, "app")
	ctx, cancel := context.WithCancel(context.Background())
	deadline := waittest.Deadline(t)
	go func() {
		defer cancel()
		for time.Now().Before(deadline) {
			if entries, _ := os.ReadDir(s.projectsDir); len(entries) > 0 {
				return // git is cloning
			}
			time.Sleep(100 * time.Microsecond)
		}
	}()
	if _, err := s.OpenGit(ctx, remote, "", io.Discard); err == nil {
		t.Skip("the clone finished before it was killed")
	}
	if entries, _ := os.ReadDir(s.projectsDir); len(entries) > 0 {
		t.Fatalf("the killed clone left %s", entries[0].Name())
	}
	if o, err := s.OpenGit(context.Background(), remote, "", io.Discard); err != nil || !o.Cloned {
		t.Fatalf("want a fresh clone, got %+v, %v", o, err)
	}
}

// TestOpenGitRemovesCloneLeftovers clones beside what a daemon that died
// midway through a clone left: the leftover goes.
func TestOpenGitRemovesCloneLeftovers(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	left := filepath.Join(s.projectsDir, ".web.clone-123")
	if err := os.MkdirAll(filepath.Join(left, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if o, err := s.OpenGit(context.Background(), gittest.Bare(t, "app"), "", io.Discard); err != nil || !o.Cloned {
		t.Fatalf("want a clone, got %+v, %v", o, err)
	}
	if _, err := os.Stat(left); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want the leftover removed, got %v", err)
	}
	if info, err := os.Stat(filepath.Join(s.projectsDir, "app")); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("want the clone at app, readable as git makes it, got %v, %v", info, err)
	}
}

func TestOpenGitCheckoutMissingThenReclone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	remote := gittest.Bare(t, "app")
	p := openGit(t, s, remote, "")
	dest := paths(p.Project)[0]
	if err := os.RemoveAll(dest); err != nil {
		t.Fatal(err)
	}

	_, err := s.OpenGit(ctx, remote, "", io.Discard)
	missing, ok := errors.AsType[*CheckoutMissingError](err)
	if !ok || missing.Project.ID != p.ID || missing.Project.ClonePath() != dest {
		t.Fatalf("want CheckoutMissingError for %s, got %v", p.ID, err)
	}

	o, err := s.Reclone(ctx, p.ID, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !o.Cloned || o.Registered || o.ID != p.ID || len(o.Worktrees) != 1 || o.Worktrees[0].Path != dest {
		t.Fatalf("want %s cloned back into %s, got %+v", p.ID, dest, o)
	}
}

func TestClonePathSkipsLinkedWorktrees(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t, "myrepo", "")
	wt := worktree(t, repo)
	s := newStore(t)
	open(t, s, wt, "")
	p := open(t, s, repo, "")

	if got := p.ClonePath(); got != repo {
		t.Fatalf("ClonePath() = %s, want %s", got, repo)
	}
}

func create(t *testing.T, s *Store, name, parent string) Project {
	t.Helper()
	o, err := s.Create(context.Background(), name, parent)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	if !o.Registered || o.Cloned {
		t.Fatalf("create %s: want registered, not cloned: %+v", name, o)
	}
	return o.Project
}

func TestCreateInProjectsDir(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	p := create(t, s, "app", "")

	want := filepath.Join(s.projectsDir, "app")
	if p.Name != "app" || p.OriginURL != "" || p.State != StateOpen || paths(p)[0] != want {
		t.Fatalf("unexpected project: %+v", p)
	}
	if ok, err := isCheckout(context.Background(), want); err != nil || !ok {
		t.Fatalf("want git repository at %s: %v", want, err)
	}
}

func TestCreateInPath(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	parent := filepath.Join(realPath(t, t.TempDir()), "a", "b")

	if p := create(t, s, "app", parent); paths(p)[0] != filepath.Join(parent, "app") {
		t.Fatalf("want project in %s, got %v", parent, paths(p))
	}
}

func TestCreateInitsExistingDirectory(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	parent := realPath(t, t.TempDir())
	file := filepath.Join(parent, "app", "main.go")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	create(t, s, "app", parent)
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("existing files must be kept: %v", err)
	}
}

func TestCreateNameTaken(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	p := create(t, s, "app", "")

	_, err := s.Create(context.Background(), "APP", t.TempDir())
	taken, ok := errors.AsType[*NameTakenError](err)
	if !ok || taken.Project.ID != p.ID {
		t.Fatalf("want NameTakenError for %s, got %v", p.ID, err)
	}
	if o, err := s.Open(context.Background(), taken.Project.ID); err != nil || o.ID != p.ID {
		t.Fatalf("open taken project: %+v, %v", o, err)
	}
}

func TestCreateRepoExists(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	repo := gitRepo(t, "app", "")

	_, err := s.Create(context.Background(), "app", filepath.Dir(repo))
	exists, ok := errors.AsType[*RepoExistsError](err)
	if !ok || exists.Root != repo || exists.Project != nil {
		t.Fatalf("want RepoExistsError for unknown repo %s, got %v", repo, err)
	}

	p := open(t, s, repo, "other")
	_, err = s.Create(context.Background(), "app", filepath.Dir(repo))
	if exists, ok := errors.AsType[*RepoExistsError](err); !ok || exists.Project == nil || exists.Project.ID != p.ID {
		t.Fatalf("want RepoExistsError for project %s, got %v", p.ID, err)
	}
}

func TestCreateInsideRepo(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	repo := gitRepo(t, "outer", "")

	_, err := s.Create(context.Background(), "app", filepath.Join(repo, "sub"))
	if exists, ok := errors.AsType[*RepoExistsError](err); !ok || exists.Root != repo {
		t.Fatalf("want RepoExistsError for %s, got %v", repo, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "sub")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nothing should be created, got %v", err)
	}
}

func TestCreatePathIsFile(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := s.Create(context.Background(), "app", file)
	if invalid, ok := errors.AsType[*InvalidPathError](err); !ok || invalid.Path != file {
		t.Fatalf("want InvalidPathError for %s, got %v", file, err)
	}
}

func TestCreateRejectsPathAsName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", "../app", "a/b", "."} {
		if _, err := newStore(t).Create(context.Background(), name, ""); err == nil {
			t.Fatalf("create %q: want error", name)
		} else if _, ok := errors.AsType[*InvalidNameError](err); !ok {
			t.Fatalf("create %q: want InvalidNameError, got %v", name, err)
		}
	}
}

func TestNameCannotContainColon(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	p := open(t, s, gitRepo(t, "app", ""), "")
	for what, f := range map[string]func() error{
		"create":   func() error { _, err := s.Create(ctx, "id:app", ""); return err },
		"register": func() error { _, err := s.OpenLocal(ctx, gitRepo(t, "a:b", ""), ""); return err },
		"rename":   func() error { _, err := s.OpenLocal(ctx, p.Worktrees[0].Path, "id:"+p.ID); return err },
	} {
		if _, ok := errors.AsType[*InvalidNameError](f()); !ok {
			t.Fatalf("%s: want InvalidNameError", what)
		}
	}
	if got, err := s.GetByID(ctx, p.ID); err != nil || got.Name != "app" || countRows(t, s, "projects") != 1 {
		t.Fatalf("want only app unchanged, got %+v, %v", got, err)
	}
}

func failInserts(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.db.Exec("CREATE TRIGGER fail BEFORE INSERT ON projects BEGIN SELECT RAISE(ABORT, 'boom'); END"); err != nil {
		t.Fatal(err)
	}
}

func TestCreateFailureRemovesCreatedDirectory(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	failInserts(t, s)
	parent := realPath(t, t.TempDir())

	if _, err := s.Create(context.Background(), "app", filepath.Join(parent, "new")); err == nil {
		t.Fatal("want error")
	}
	if _, err := os.Stat(filepath.Join(parent, "new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want created directory removed, got %v", err)
	}
}

func TestCreateFailureKeepsExistingDirectory(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	failInserts(t, s)
	parent := realPath(t, t.TempDir())
	dest := filepath.Join(parent, "app")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Create(context.Background(), "app", parent); err == nil {
		t.Fatal("want error")
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("want existing directory kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want .git removed, got %v", err)
	}
}

func TestListEmpty(t *testing.T) {
	t.Parallel()
	list, err := newStore(t).List(context.Background(), "")
	if err != nil || len(list) != 0 {
		t.Fatalf("want no projects, got %+v, %v", list, err)
	}
}

func TestListSortsAndFilters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	beta := gitRepo(t, "Beta", "")
	open(t, s, worktree(t, beta), "Beta")
	open(t, s, beta, "")
	open(t, s, gitRepo(t, "cat", ""), "")
	app := open(t, s, gitRepo(t, "app", ""), "")
	if _, err := s.Close(ctx, app.ID); err != nil {
		t.Fatal(err)
	}

	for state, want := range map[State]string{"": "app:1 Beta:2 cat:1", StateOpen: "Beta:2 cat:1", StateClosed: "app:1"} {
		list, err := s.List(ctx, state)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, p := range list {
			got = append(got, fmt.Sprintf("%s:%d", p.Name, p.WorktreeCount))
		}
		if strings.Join(got, " ") != want {
			t.Fatalf("List(%q) = %v, want %s", state, got, want)
		}
	}
}

func TestGetByIDAndName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	p := open(t, s, gitRepo(t, "App", ""), "")
	if _, err := s.Close(ctx, p.ID); err != nil {
		t.Fatal(err)
	}

	for _, get := range []func() (Project, error){
		func() (Project, error) { return s.GetByID(ctx, p.ID) },
		func() (Project, error) { return s.GetByName(ctx, "app") },
		func() (Project, error) { return s.GetByName(ctx, "APP") },
	} {
		if got, err := get(); err != nil || got.ID != p.ID || got.State != StateClosed || len(got.Worktrees) != 1 {
			t.Fatalf("want closed project %s with its worktree, got %+v, %v", p.ID, got, err)
		}
	}
	if _, err := s.GetByID(ctx, "App"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID must not match a name, got %v", err)
	}
	if _, err := s.GetByName(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByName must not match an id, got %v", err)
	}
}

func TestDiskStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	repo := gitRepo(t, "app", "")
	wt := worktree(t, repo)
	open(t, s, wt, "")
	p := open(t, s, repo, "")

	status := func() DiskStatus {
		t.Helper()
		st, err := s.DiskStatus(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	if st := status(); st.Orphaned || !st.Present[p.Worktrees[0].ID] || !st.Present[p.Worktrees[1].ID] {
		t.Fatalf("want both checkouts present, got %+v", st)
	}

	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	if st := status(); st.Orphaned || st.Present[idOf(t, p, wt)] || !st.Present[idOf(t, p, repo)] {
		t.Fatalf("want only %s missing, not orphaned, got %+v", wt, st)
	}

	// A directory that is no longer a repository is not a checkout either.
	if err := os.RemoveAll(filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	if st := status(); !st.Orphaned || st.Present[idOf(t, p, repo)] {
		t.Fatalf("want orphaned, got %+v", st)
	}
	if st, err := s.DiskStatus(ctx, Project{}); err != nil || !st.Orphaned {
		t.Fatalf("a project without worktrees is orphaned, got %+v, %v", st, err)
	}
	if got, err := s.GetByID(ctx, p.ID); err != nil || len(got.Worktrees) != 2 {
		t.Fatalf("DiskStatus must not drop checkouts, got %+v, %v", got, err)
	}
}

func idOf(t *testing.T, p Project, path string) string {
	t.Helper()
	for _, w := range p.Worktrees {
		if w.Path == path {
			return w.ID
		}
	}
	t.Fatalf("no worktree at %s in %+v", path, p.Worktrees)
	return ""
}

func TestGetByPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	outer := gitRepo(t, "outer", "")
	inner := filepath.Join(outer, "vendor", "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, inner, "init", "-q")
	o, err := s.OpenLocal(ctx, outer, "")
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.OpenLocal(ctx, inner, "")
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(outer, link); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]string{
		outer:                             o.ID,
		filepath.Join(outer, "src", ".."): o.ID,
		filepath.Join(outer, "vendor"):    o.ID,
		filepath.Join(inner, "src"):       i.ID,
		filepath.Join(link, "src"):        o.ID,
	} {
		p, err := s.GetByPath(ctx, path)
		if err != nil || p.ID != want {
			t.Errorf("%s: want %s, got %s, %v", path, want, p.ID, err)
		}
	}
	for _, path := range []string{outer + "-sibling", filepath.Dir(outer)} {
		if _, err := s.GetByPath(ctx, path); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: want not found, got %v", path, err)
		}
	}
	if !o.Holds(filepath.Join(inner, "src")) || o.Holds(outer+"-sibling") {
		t.Error("want outer to hold what is below it, and only that")
	}
}
