package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/srjn45/wdx/internal/git"
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
	list, err := Read(t.Context(), Checkouts{Clone: dir, Paths: []string{dir}}, nil, nil)
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

func TestRemovable(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	ahead := linked(t, dir, "ahead")
	commit(t, ahead, "more", "a\n")
	dirty := linked(t, dir, "dirty")
	write(t, dirty, "fresh", "new\n")
	gone := linked(t, dir, "gone")
	commit(t, gone, "kept", "a\n")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	clean := linked(t, dir, "clean")
	lost := filepath.Join(t.TempDir(), "lost")
	c := Checkouts{Clone: dir, Paths: []string{dir, lost}}

	for _, name := range []string{"ahead", "dirty"} {
		if _, err := Removable(t.Context(), c, name, nil); !isNotLanded(err) {
			t.Errorf("%s: want not landed, got %v", name, err)
		}
	}
	if _, err := Removable(t.Context(), c, "main", nil); !errors.Is(err, ErrIsClone) {
		t.Errorf("the clone: %v", err)
	}
	if _, err := Removable(t.Context(), c, "nope", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
	for name, path := range map[string]string{"clean": clean, "gone": gone, "lost": lost} {
		if s, err := Removable(t.Context(), c, name, nil); err != nil || s.Path != path {
			t.Errorf("%s: %+v, %v", name, s, err)
		}
	}
}

func isNotLanded(err error) bool {
	_, ok := errors.AsType[*NotLandedError](err)
	return ok
}

func TestRemove(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	clean := linked(t, dir, "clean")
	c := Checkouts{Clone: dir, Paths: []string{dir}}
	s, err := Removable(t.Context(), c, "clean", nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Remove(t.Context(), s)
	if err != nil || r.BranchDeleted != "clean" || r.BranchKept != "" {
		t.Fatalf("removed: %+v, %v", r, err)
	}
	if isDir(clean) {
		t.Error("the directory stayed")
	}
	if ok, err := git.BranchExists(t.Context(), dir, "clean"); err != nil || ok {
		t.Errorf("the branch stayed: %v, %v", ok, err)
	}
	if list, err := List(t.Context(), c); err != nil || len(list) != 1 {
		t.Errorf("listed: %+v, %v", list, err)
	}
}

// What happens after the check is git's to refuse, so nothing can be lost to a race.
func TestRemoveLosesNothingWrittenAfterTheCheck(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	c := Checkouts{Clone: dir, Paths: []string{dir}}

	late := linked(t, dir, "late")
	s, err := Removable(t.Context(), c, "late", nil)
	if err != nil {
		t.Fatal(err)
	}
	write(t, late, "fresh", "written since\n")
	if _, err := Remove(t.Context(), s); err == nil {
		t.Error("a file written since did not stop it")
	} else if _, ok := errors.AsType[*git.WorktreeRemoveError](err); !ok {
		t.Errorf("err: %v", err)
	}
	if _, err := os.Stat(filepath.Join(late, "fresh")); err != nil {
		t.Errorf("the file went: %v", err)
	}

	committed := linked(t, dir, "committed")
	if s, err = Removable(t.Context(), c, "committed", nil); err != nil {
		t.Fatal(err)
	}
	commit(t, committed, "more", "committed since\n")
	r, err := Remove(t.Context(), s)
	if err != nil || r.BranchKept != "committed" || r.BranchDeleted != "" {
		t.Fatalf("a commit made since: %+v, %v", r, err)
	}
	gittest.Git(t, dir, "cat-file", "-e", "committed:more") // the commit is still there
}

func TestRemoveKeepsWhatMustStay(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	c := Checkouts{Clone: dir, Paths: []string{dir}}

	// A directory gone with commits on its branch: only git's entry goes.
	gone := linked(t, dir, "gone")
	commit(t, gone, "kept", "a\n")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	s, err := Removable(t.Context(), c, "gone", nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Remove(t.Context(), s)
	if err != nil || r.BranchKept != "gone" {
		t.Fatalf("gone: %+v, %v", r, err)
	}
	if list, err := List(t.Context(), c); err != nil || len(list) != 1 {
		t.Errorf("git's entry stayed: %+v, %v", list, err)
	}
	gittest.Git(t, dir, "cat-file", "-e", "gone:kept") // and so did its commits

	// The default branch, which the clone left and a linked checkout has.
	gittest.Git(t, dir, "switch", "-qc", "side")
	onMain := filepath.Join(t.TempDir(), "on-main")
	gittest.Git(t, dir, "worktree", "add", "-q", onMain, "main")
	r, err = Remove(t.Context(), Status{Name: "on-main", Path: onMain, Branch: "main", repository: filepath.Join(dir, ".git")})
	if err != nil || r.BranchKept != "main" {
		t.Fatalf("default branch: %+v, %v", r, err)
	}
	gittest.Git(t, dir, "rev-parse", "--verify", "refs/heads/main")

	// A record no repository lists has nothing for git to do.
	if r, err = Remove(t.Context(), Status{Name: "lost", Path: filepath.Join(t.TempDir(), "lost")}); err != nil || r != (Removal{}) {
		t.Errorf("record only: %+v, %v", r, err)
	}
}

func TestRemovedMessage(t *testing.T) {
	t.Parallel()
	got := RemovedMessage(Status{Name: "auth", Path: "/r/auth", Base: "main"}, "/r")
	for _, want := range []string{"auth", "/r/auth", "at /r, on main"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in %q", want, got)
		}
	}
}

func TestHolds(t *testing.T) {
	t.Parallel()
	s := Status{Path: "/r/auth"}
	for dir, want := range map[string]bool{"/r/auth": true, "/r/auth/x": true, "/r/auth-2": false, "/r": false} {
		if s.Holds(dir) != want {
			t.Errorf("%s: want %v", dir, want)
		}
	}
}

func TestListFiles(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	write(t, work, ".gitignore", "ignored\n")
	write(t, work, "ignored", "x\n")
	write(t, work, "untracked", "x\n")
	c := Checkouts{Clone: dir, Paths: []string{dir}}
	paths, _, err := ListFiles(t.Context(), c, "work")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(paths, "ignored") || !slices.Contains(paths, "untracked") || !slices.Contains(paths, ".gitignore") {
		t.Errorf("paths: %q", paths)
	}
	if _, _, err := ListFiles(t.Context(), c, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown worktree: %v", err)
	}
}

func TestReadFile(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	c := Checkouts{Clone: dir, Paths: []string{dir}}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(work, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, work, "text", "héllo\n")
	write(t, work, "empty", "")
	write(t, work, "nul", "a\x00b")
	write(t, work, "latin1", "caf\xe9\n")
	write(t, work, "at-limit", strings.Repeat("x", maxFileContent))
	write(t, work, "too-large", strings.Repeat("x", maxFileContent+1))
	write(t, work, ".gitignore", "ignored\n")
	write(t, work, "ignored", "still a file\n")
	for link, target := range map[string]string{"in": "text", "out": outside, "up": "../", "dangling": "nowhere", "to-dir": "dir"} {
		if err := os.Symlink(target, filepath.Join(work, link)); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Mkfifo(filepath.Join(work, "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		path string
		want FileContent
	}{
		{"text", FileContent{Text: "héllo\n", Size: 7}},
		{"empty", FileContent{}},
		{"in", FileContent{Text: "héllo\n", Size: 7}},
		{"ignored", FileContent{Text: "still a file\n", Size: 13}},
		{"nul", FileContent{Binary: true, Size: 3}},
		{"latin1", FileContent{Binary: true, Size: 5}},
		{"too-large", FileContent{TooLarge: true, Size: maxFileContent + 1}},
	} {
		got, err := ReadFile(t.Context(), c, "work", tc.path)
		if err != nil || got != tc.want {
			t.Errorf("%s: %+v, %v", tc.path, got, err)
		}
	}
	if got, err := ReadFile(t.Context(), c, "work", "at-limit"); err != nil || got.TooLarge || len(got.Text) != maxFileContent {
		t.Errorf("at the limit: %d bytes, too large %v, %v", len(got.Text), got.TooLarge, err)
	}

	for _, path := range []string{"../x", "/etc/passwd", "", "out", "up"} {
		if _, err := ReadFile(t.Context(), c, "work", path); !isInvalidPath(err) {
			t.Errorf("%q: want an invalid path, got %v", path, err)
		}
	}
	for _, path := range []string{"dir", "to-dir", "fifo", "."} {
		if _, err := ReadFile(t.Context(), c, "work", path); !errors.As(err, new(*NotAFileError)) {
			t.Errorf("%q: want not a file, got %v", path, err)
		}
	}
	for _, path := range []string{"gone", "dangling", "text/below"} {
		if _, err := ReadFile(t.Context(), c, "work", path); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: want not found, got %v", path, err)
		}
	}
	if _, err := ReadFile(t.Context(), c, "nope", "text"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown worktree: %v", err)
	}
}
