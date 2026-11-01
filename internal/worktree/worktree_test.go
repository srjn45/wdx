package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/runtest"
)

func TestMain(m *testing.M) { os.Exit(runtest.Main(m)) }

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repo is a clone on main with one file committed.
func repo(t *testing.T) string {
	t.Helper()
	dir := gittest.Repo(t, "repo")
	gittest.Git(t, dir, "branch", "-M", "main")
	write(t, dir, "file", "one\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-qm", "base")
	return dir
}

func linked(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(dir), name)
	gittest.Git(t, dir, "worktree", "add", "-q", path, "-b", name)
	return path
}

func commit(t *testing.T, dir, name, content string) {
	t.Helper()
	write(t, dir, name, content)
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-qm", name)
}

func named2(list []Status, name string) (Status, bool) {
	i := slices.IndexFunc(list, func(s Status) bool { return s.Name == name })
	if i < 0 {
		return Status{}, false
	}
	return list[i], true
}

func intOf(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

func TestRead(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	commit(t, work, "landed", "a\nb\n")
	write(t, work, "file", "changed\n")
	write(t, work, "fresh", "new\n")
	clean := linked(t, dir, "clean")
	gone := linked(t, dir, "gone")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	// Only the clone is registered: the others come from git.
	list, err := Read(t.Context(), Checkouts{Clone: dir, Paths: []string{dir}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 4 {
		t.Fatalf("want 4 worktrees, got %+v", list)
	}
	if list[0].Name != "main" || list[0].Path != dir {
		t.Errorf("first: %+v", list[0])
	}
	w, ok := named2(list, "work")
	if !ok {
		t.Fatalf("no work in %+v", list)
	}
	if w.Path != work || w.Branch != "work" || w.Base != "main" || intOf(w.Ahead) != 1 || intOf(w.Added) != 2 || intOf(w.Removed) != 0 {
		t.Errorf("work: %+v", w)
	}
	var files []string
	for _, f := range w.Files {
		files = append(files, f.Status+" "+f.Path)
		if f.ModifiedAt == nil || f.Size == 0 {
			t.Errorf("%s has no size or time: %+v", f.Path, f)
		}
	}
	if !slices.Equal(files, []string{"M file", "? fresh"}) {
		t.Errorf("work's files: %v", files)
	}
	if c, _ := named2(list, "clean"); c.Path != clean || intOf(c.Ahead) != 0 || len(c.Files) != 0 {
		t.Errorf("clean: %+v", c)
	}
	if g, _ := named2(list, "gone"); !g.Missing || g.Ahead != nil {
		t.Errorf("gone: %+v", g)
	}
}

func TestListKeepsNamesOneCheckoutEach(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	other := filepath.Join(t.TempDir(), "main")
	gittest.Git(t, dir, "worktree", "add", "-q", other, "-b", "other")
	list, err := List(t.Context(), Checkouts{Clone: dir, Paths: []string{dir}})
	if err != nil {
		t.Fatal(err)
	}
	if list[0].Name != "main" || list[1].Name != "main-2" {
		t.Errorf("names: %q, %q", list[0].Name, list[1].Name)
	}
}

func TestListWithARegisteredCheckoutGone(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	gone := filepath.Join(t.TempDir(), "gone")
	list, err := List(t.Context(), Checkouts{Clone: dir, Paths: []string{dir, gone}})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || !list[1].Missing || list[1].Path != gone {
		t.Errorf("got %+v", list)
	}
}

func TestDiffs(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	commit(t, work, "landed", "a\n")
	write(t, work, "file", "changed\n")
	c := Checkouts{Clone: dir, Paths: []string{dir}}

	p, err := FileDiff(t.Context(), c, "work", "file")
	if err != nil || !strings.Contains(p.Text, "+changed") {
		t.Errorf("file diff: %+v, %v", p, err)
	}
	for _, path := range []string{"../file", "/etc/passwd", ""} {
		if _, err := FileDiff(t.Context(), c, "work", path); !isInvalidPath(err) {
			t.Errorf("%q: want an invalid path, got %v", path, err)
		}
	}
	if _, err := FileDiff(t.Context(), c, "nope", "file"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown worktree: %v", err)
	}

	r, err := CommittedDiff(t.Context(), c, "work")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status.Base != "main" || r.MergeBase == "" || !strings.Contains(r.Patch.Text, "+a") || strings.Contains(r.Patch.Text, "+changed") {
		t.Errorf("committed diff: %+v", r)
	}

	unrelated := filepath.Join(t.TempDir(), "unrelated")
	gittest.Git(t, dir, "worktree", "add", "-q", "--orphan", "-b", "unrelated", unrelated)
	commit(t, unrelated, "alone", "x\n")
	if r, err = CommittedDiff(t.Context(), c, "unrelated"); err != nil || r.MergeBase != "" || r.Patch.Text != "" {
		t.Errorf("no history in common: %+v, %v", r, err)
	}
}

func isInvalidPath(err error) bool {
	_, ok := errors.AsType[*InvalidPathError](err)
	return ok
}
