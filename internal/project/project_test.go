package project

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/warden/internal/db"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return NewStore(d)
}

// gitRepo creates a git repo named name; origin is added when non-empty.
func gitRepo(t *testing.T, name, origin string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "init", "-q")
	if origin != "" {
		gitCmd(t, dir, "remote", "add", "origin", origin)
	}
	return realPath(t, dir)
}

// worktree adds a linked worktree of repo and returns its path.
func worktree(t *testing.T, repo string) string {
	t.Helper()
	gitCmd(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(t.TempDir(), "wt")
	gitCmd(t, repo, "worktree", "add", "-q", wt)
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

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
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
	p, err := s.OpenLocal(context.Background(), path, name)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	return p
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

func TestOpenLocalSubdirResolvesToRoot(t *testing.T) {
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
	s := newStore(t)
	repo := gitRepo(t, "myrepo", "")

	p := open(t, s, repo, "")
	if _, err := s.Close(context.Background(), p.Name); err != nil {
		t.Fatal(err)
	}
	if p = open(t, s, repo, ""); p.State != StateOpen {
		t.Fatalf("want reopened, got %s", p.State)
	}
}

func TestWorktreeJoinsProject(t *testing.T) {
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
	s := newStore(t)
	repo := gitRepo(t, "myrepo", "")
	p := open(t, s, repo, "")

	gitCmd(t, repo, "remote", "add", "origin", "https://example.com/r.git")
	if p = open(t, s, repo, ""); p.OriginURL != "https://example.com/r.git" {
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
	gitCmd(t, stray, "remote", "add", "origin", "https://example.com/r.git")
	if _, err := s.OpenLocal(context.Background(), stray, ""); err == nil || !strings.Contains(err.Error(), "belongs to project "+p.ID) {
		t.Fatalf("want origin conflict, got %v", err)
	}
}

func TestOpenLocalNameCollision(t *testing.T) {
	s := newStore(t)
	a, b := gitRepo(t, "api", ""), gitRepo(t, "API", "")

	open(t, s, a, "")
	if _, err := s.OpenLocal(context.Background(), b, ""); err == nil || !strings.Contains(err.Error(), "--name") {
		t.Fatalf("want name collision error, got %v", err)
	}
	if p := open(t, s, b, "api-2"); p.Name != "api-2" {
		t.Fatalf("want name api-2, got %q", p.Name)
	}
	if p := open(t, s, b, ""); p.Name != "api-2" {
		t.Fatalf("reopen without --name should keep api-2, got %q", p.Name)
	}
}

func TestReinitInSamePathKeepsProject(t *testing.T) {
	s := newStore(t)
	repo := gitRepo(t, "myrepo", "")
	old := open(t, s, repo, "")

	// A new repo at the same path has the same .git path, so it is the same project.
	if err := os.RemoveAll(filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "init", "-q")
	gitCmd(t, repo, "remote", "add", "origin", "https://example.com/new.git")

	p := open(t, s, repo, "")
	if p.ID != old.ID || p.OriginURL != "https://example.com/new.git" || countRows(t, s, "project_worktrees") != 1 {
		t.Fatalf("want same project with new origin, got %+v (old %+v)", p, old)
	}
}

func TestOpenLocalNotGitRepo(t *testing.T) {
	_, err := newStore(t).OpenLocal(context.Background(), t.TempDir(), "")
	if !errors.Is(err, ErrNotGitRepo) {
		t.Fatalf("want ErrNotGitRepo, got %v", err)
	}
}

func TestOpenLocalMissingDir(t *testing.T) {
	_, err := newStore(t).OpenLocal(context.Background(), filepath.Join(t.TempDir(), "nope"), "")
	if err == nil {
		t.Fatal("want error for missing directory")
	}
}

func TestCloseByNameAndID(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	p := open(t, s, gitRepo(t, "MyRepo", ""), "")

	for _, ref := range []string{p.Name, "myrepo", p.ID} {
		open(t, s, p.Worktrees[0].Path, "")
		closed, err := s.Close(ctx, ref)
		if err != nil {
			t.Fatalf("close %q: %v", ref, err)
		}
		if closed.ID != p.ID || closed.State != StateClosed {
			t.Fatalf("close %q: unexpected project %+v", ref, closed)
		}
	}
}

func TestCloseNotFound(t *testing.T) {
	_, err := newStore(t).Close(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestRemoveDeletesProjectAndWorktrees(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	repo := gitRepo(t, "myrepo", "")
	p := open(t, s, repo, "")
	open(t, s, worktree(t, repo), "")
	keep := open(t, s, gitRepo(t, "other", ""), "")

	removed, err := s.Remove(ctx, "MYREPO")
	if err != nil {
		t.Fatal(err)
	}
	if removed.ID != p.ID || len(removed.Worktrees) != 2 {
		t.Fatalf("unexpected removed project: %+v", removed)
	}
	if _, err := s.Get(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound after remove, got %v", err)
	}
	if n := countRows(t, s, "project_worktrees"); n != 1 {
		t.Fatalf("want only the other project's worktree left, got %d rows", n)
	}
	if _, err := s.Get(ctx, keep.ID); err != nil {
		t.Fatalf("other project affected: %v", err)
	}
	// Opening the path again starts a fresh project.
	if q := open(t, s, repo, ""); q.ID == p.ID {
		t.Fatalf("want a new project id after remove, got %s", q.ID)
	}
}

func TestRemoveNotFound(t *testing.T) {
	_, err := newStore(t).Remove(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
