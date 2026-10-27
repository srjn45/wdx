package project

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/warden/internal/db"
	"github.com/srjn45/warden/internal/gittest"
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
	gitCmd(t, dir, "init", "-q")
	if origin != "" {
		gitCmd(t, dir, "remote", "add", "origin", origin)
	}
	return realPath(t, dir)
}

// worktree adds a linked worktree of repo and returns its path.
// bareRemote creates a bare repository with one commit to clone from.
func bareRemote(t *testing.T, name string) string {
	t.Helper()
	src := gitRepo(t, name, "")
	gitCmd(t, src, "commit", "-q", "--allow-empty", "-m", "init")
	bare := filepath.Join(realPath(t, t.TempDir()), name+".git")
	gitCmd(t, src, "clone", "-q", "--bare", src, bare)
	return bare
}

func worktree(t *testing.T, repo string) string {
	t.Helper()
	gitCmd(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
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
	if _, err := s.Close(context.Background(), p.Name); err != nil {
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	_, err := newStore(t).OpenLocal(context.Background(), t.TempDir(), "")
	if !errors.Is(err, ErrNotGitRepo) {
		t.Fatalf("want ErrNotGitRepo, got %v", err)
	}
}

func TestOpenLocalMissingDir(t *testing.T) {
	t.Parallel()
	_, err := newStore(t).OpenLocal(context.Background(), filepath.Join(t.TempDir(), "nope"), "")
	if err == nil {
		t.Fatal("want error for missing directory")
	}
}

func TestCloseByNameAndID(t *testing.T) {
	t.Parallel()
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
	remote := bareRemote(t, "app")

	o := openGit(t, s, remote, "")
	want := filepath.Join(s.projectsDir, "app")
	if !o.Cloned || o.Name != "app" || o.OriginURL != remote {
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
	if o.Cloned || o.ID != p.ID || o.State != StateOpen {
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
	remote := bareRemote(t, "app")
	p := openGit(t, s, remote, "")
	if _, err := s.Remove(ctx, p.ID); err != nil {
		t.Fatal(err)
	}

	o := openGit(t, s, remote, "")
	if o.Cloned || o.ID == p.ID || paths(o.Project)[0] != paths(p.Project)[0] {
		t.Fatalf("want new project on the existing clone, got %+v", o)
	}
}

func TestOpenGitDirectoryTaken(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	if err := os.MkdirAll(filepath.Join(s.projectsDir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := s.OpenGit(context.Background(), bareRemote(t, "app"), "", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "--name") {
		t.Fatalf("want directory taken error, got %v", err)
	}
	if o := openGit(t, s, bareRemote(t, "app"), "app-2"); o.Name != "app-2" {
		t.Fatalf("want name app-2, got %q", o.Name)
	}
}

func TestOpenGitNameTakenDoesNotClone(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	open(t, s, gitRepo(t, "app", ""), "")

	_, err := s.OpenGit(context.Background(), bareRemote(t, "app"), "", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "--name") {
		t.Fatalf("want name collision error, got %v", err)
	}
	if _, err := os.Stat(s.projectsDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nothing should be cloned, stat %s: %v", s.projectsDir, err)
	}
}

func TestOpenGitRejectsPathAsName(t *testing.T) {
	t.Parallel()
	_, err := newStore(t).OpenGit(context.Background(), bareRemote(t, "app"), "../app", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "directory name") {
		t.Fatalf("want invalid name error, got %v", err)
	}
}

func TestOpenGitCloneFailureLeavesNothing(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	missing := filepath.Join(t.TempDir(), "nope.git")

	if _, err := s.OpenGit(context.Background(), missing, "", io.Discard); err == nil {
		t.Fatal("want clone error")
	}
	if n := countRows(t, s, "projects"); n != 0 {
		t.Fatalf("want no projects, got %d", n)
	}
	if _, err := os.Stat(filepath.Join(s.projectsDir, "nope")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want no leftover directory, got %v", err)
	}
}

func TestOpenGitCheckoutMissingThenReclone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	remote := bareRemote(t, "app")
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
	if !o.Cloned || o.ID != p.ID || len(o.Worktrees) != 1 || o.Worktrees[0].Path != dest {
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

	if _, err := s.Create(context.Background(), "app", file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("want not a directory error, got %v", err)
	}
}

func TestCreateRejectsPathAsName(t *testing.T) {
	t.Parallel()
	_, err := newStore(t).Create(context.Background(), "../app", "")
	if err == nil || !strings.Contains(err.Error(), "directory name") {
		t.Fatalf("want invalid name error, got %v", err)
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
