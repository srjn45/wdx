package git

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/gittest"
)

// readRepo is a repository with a commit ahead of main on the branch checked out,
// a file changed and one untracked.
func readRepo(t *testing.T) string {
	t.Helper()
	dir := gittest.Repo(t, "read")
	gittest.Git(t, dir, "branch", "-M", "main")
	write(t, dir, "kept", "one\ntwo\nthree\n")
	write(t, dir, "bare.txt", "base\n")
	if err := os.Mkdir(filepath.Join(dir, "several"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "several/one", "one\n")
	write(t, dir, "several/two", "two\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-qm", "base")
	gittest.Git(t, dir, "switch", "-qc", "work")
	write(t, dir, "landing", "first\nsecond\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-qm", "work")
	write(t, dir, "kept", "one\n2\nthree\n")
	write(t, dir, "new", "fresh\n")
	return dir
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// staleIndex makes git's record of the files out of date with their contents the
// same: what makes a command that may write refresh the index.
func staleIndex(t *testing.T, dir string, n int) {
	t.Helper()
	at := time.Now().Add(time.Duration(n+1) * time.Hour)
	for _, name := range []string{"bare.txt", "landing", "several/one", "several/two"} {
		if err := os.Chtimes(filepath.Join(dir, name), at, at); err != nil {
			t.Fatal(err)
		}
	}
}

// reads are every read of this file, as they run against the repository of readRepo.
func reads(dir string) map[string]func(context.Context) error {
	ignoring := func(_ any, err error) error { return err }
	return map[string]func(context.Context) error{
		"Checkouts":     func(ctx context.Context) error { return ignoring(Checkouts(ctx, dir)) },
		"Dirs":          func(ctx context.Context) error { _, _, err := Dirs(ctx, dir); return err },
		"Changes":       func(ctx context.Context) error { return ignoring(Changes(ctx, dir)) },
		"DefaultBranch": func(ctx context.Context) error { return ignoring(DefaultBranch(ctx, dir)) },
		"BaseOf":        func(ctx context.Context) error { return ignoring(BaseOf(ctx, dir, "work")) },
		"Summarize":     func(ctx context.Context) error { return ignoring(Summarize(ctx, dir, "main")) },
		"FileDiff":      func(ctx context.Context) error { return ignoring(FileDiff(ctx, dir, "kept")) },
		"FileDiff new":  func(ctx context.Context) error { return ignoring(FileDiff(ctx, dir, "new")) },
		// git refreshes the index when more than one file it compares proves unchanged.
		"FileDiff several": func(ctx context.Context) error { return ignoring(FileDiff(ctx, dir, "several")) },
		"MergeBase":        func(ctx context.Context) error { return ignoring(MergeBase(ctx, dir, "main")) },
		"RangeDiff":        func(ctx context.Context) error { return ignoring(RangeDiff(ctx, dir, "main")) },
	}
}

// gitFiles is every file of the git directory with its time and size.
func gitFiles(t *testing.T, gitDir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(gitDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files[path] = info.ModTime().String() + " " + strconv.FormatInt(info.Size(), 10)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// TestReadsWriteNothing runs each read on an index out of date, when git would
// rewrite it if it may, and finds the git directory as it was.
func TestReadsWriteNothing(t *testing.T) {
	t.Parallel()
	dir := readRepo(t)
	n := 0
	for name, run := range reads(dir) {
		n++
		gittest.Git(t, dir, "update-index", "-q", "--refresh")
		staleIndex(t, dir, n)
		before := gitFiles(t, filepath.Join(dir, ".git"))
		if err := run(t.Context()); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		after := gitFiles(t, filepath.Join(dir, ".git"))
		for path, was := range before {
			if after[path] != was {
				t.Errorf("%s changed %s", name, path)
			}
		}
		for path := range after {
			if _, ok := before[path]; !ok {
				t.Errorf("%s created %s", name, path)
			}
		}
	}
}

// TestReadsBesideIndexLock runs each read while another command holds the index:
// it neither fails nor touches the lock.
func TestReadsBesideIndexLock(t *testing.T) {
	t.Parallel()
	dir := readRepo(t)
	lock := filepath.Join(dir, ".git", "index.lock")
	write(t, filepath.Dir(lock), "index.lock", "held")
	for name, run := range reads(dir) {
		if err := run(t.Context()); err != nil {
			t.Errorf("%s with the index locked: %v", name, err)
		}
		if got, err := os.ReadFile(lock); err != nil || string(got) != "held" {
			t.Fatalf("%s touched the lock: %q, %v", name, got, err)
		}
	}
}

// TestReadsLeaveOtherCommandsAlone runs add and rm beside reads on an index kept out
// of date, as an agent's commands run beside wdx's: none may fail on the lock.
func TestReadsLeaveOtherCommandsAlone(t *testing.T) {
	t.Parallel()
	dir := readRepo(t)
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	var readers sync.WaitGroup
	readers.Go(func() {
		for n := 0; ctx.Err() == nil; n++ {
			staleIndex(t, dir, n%50)
			_, _ = Changes(ctx, dir)
			_, _ = FileDiff(ctx, dir, "kept")
		}
	})
	defer readers.Wait()
	defer stop()
	for range 100 {
		for _, args := range [][]string{{"add", "new"}, {"rm", "-q", "--cached", "new"}} {
			out, err := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
			if err != nil {
				t.Fatalf("git %v beside reads: %v\n%s", args, err, out)
			}
		}
	}
}

func TestCheckouts(t *testing.T) {
	t.Parallel()
	dir := readRepo(t)
	linked := filepath.Join(filepath.Dir(dir), "linked")
	gone := filepath.Join(filepath.Dir(dir), "gone")
	gittest.Git(t, dir, "worktree", "add", "-q", linked, "-b", "linked")
	gittest.Git(t, dir, "worktree", "add", "-q", "--detach", gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	list, err := Checkouts(t.Context(), linked)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("want 3 checkouts, got %+v", list)
	}
	if c := list[0]; c.Path != dir || c.Branch != "work" || c.Head == "" || c.Prunable {
		t.Errorf("main checkout: %+v", c)
	}
	if c := list[1]; c.Path != linked && c.Path != gone {
		t.Errorf("second checkout: %+v", c)
	}
	for _, c := range list[1:] {
		switch c.Path {
		case linked:
			if c.Branch != "linked" || c.Prunable {
				t.Errorf("linked: %+v", c)
			}
		case gone:
			if c.Branch != "" || !c.Prunable {
				t.Errorf("gone: %+v", c)
			}
		}
	}
}

func TestChanges(t *testing.T) {
	t.Parallel()
	dir := gittest.Repo(t, "changes")
	for _, name := range []string{"kept", "gone", "staged", "moved", "both"} {
		write(t, dir, name, name+"\n")
	}
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-qm", "base")
	write(t, dir, "kept", "other\n")
	if err := os.Remove(filepath.Join(dir, "gone")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "added", "new\n")
	gittest.Git(t, dir, "add", "added")
	write(t, dir, "both", "staged\n")
	gittest.Git(t, dir, "add", "both")
	write(t, dir, "both", "and not\n")
	gittest.Git(t, dir, "mv", "moved", "renamed")
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "sub/with space", "untracked\n")

	got, err := Changes(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"kept": "M", "gone": "D", "added": "A", "both": "M", "moved": "D", "renamed": "A", "sub/with space": "?"}
	if len(got) != len(want) {
		t.Errorf("want %d changes, got %+v", len(want), got)
	}
	for _, c := range got {
		if want[c.Path] != c.Status {
			t.Errorf("%q: want status %q, got %q", c.Path, want[c.Path], c.Status)
		}
	}
}

func TestBaseOf(t *testing.T) {
	t.Parallel()
	bare := gittest.Bare(t, "origin")
	dir := filepath.Join(t.TempDir(), "clone")
	gittest.Git(t, filepath.Dir(dir), "clone", "-q", bare, dir)
	name, err := DefaultBranch(t.Context(), dir)
	if err != nil || name == "" {
		t.Fatalf("default branch: %q, %v", name, err)
	}
	if got, err := BaseOf(t.Context(), dir, name); err != nil || got != "origin/"+name {
		t.Errorf("on the default branch: want origin/%s, got %q, %v", name, got, err)
	}
	if got, err := BaseOf(t.Context(), dir, "work"); err != nil || got != name {
		t.Errorf("on another branch: want %s, got %q, %v", name, got, err)
	}

	alone := gittest.Repo(t, "alone")
	gittest.Git(t, alone, "branch", "-M", "master")
	if got, err := DefaultBranch(t.Context(), alone); err != nil || got != "master" {
		t.Errorf("no origin: want master, got %q, %v", got, err)
	}
	if got, err := BaseOf(t.Context(), alone, "master"); err != nil || got != "" {
		t.Errorf("on it with no origin: want none, got %q, %v", got, err)
	}
	gittest.Git(t, alone, "branch", "-M", "trunk")
	if got, err := DefaultBranch(t.Context(), alone); err != nil || got != "" {
		t.Errorf("none known: want none, got %q, %v", got, err)
	}
}

func TestSummarize(t *testing.T) {
	t.Parallel()
	dir := readRepo(t)
	s, err := Summarize(t.Context(), dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if s != (Summary{Ahead: 1, Lines: true, Added: 2}) {
		t.Errorf("got %+v", s)
	}

	// A later commit on the base is not a removal here.
	gittest.Git(t, dir, "stash", "-q", "-u", "-m", "aside")
	gittest.Git(t, dir, "switch", "-q", "main")
	write(t, dir, "later", "on the base\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-qm", "later")
	gittest.Git(t, dir, "switch", "-q", "work")
	if s, err = Summarize(t.Context(), dir, "main"); err != nil || s != (Summary{Ahead: 1, Lines: true, Added: 2}) {
		t.Errorf("after the base moved: %+v, %v", s, err)
	}
	p, err := RangeDiff(t.Context(), dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Text, "later") || !strings.Contains(p.Text, "+first") {
		t.Errorf("range diff shows the base's commit, or not the branch's:\n%s", p.Text)
	}
}

func TestFileDiff(t *testing.T) {
	t.Parallel()
	dir := readRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "bin"), []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path   string
		want   []string
		binary bool
	}{
		{path: "kept", want: []string{" one", "-two", "+2", " three"}}, // whole: the lines unchanged too
		{path: "new", want: []string{"+fresh"}},
		{path: "bin", binary: true},
		{path: "landing"}, // committed: nothing uncommitted
		{path: "no-such-file"},
	} {
		p, err := FileDiff(t.Context(), dir, tc.path)
		if err != nil {
			t.Errorf("%s: %v", tc.path, err)
			continue
		}
		lines := strings.Split(p.Text, "\n")
		for _, want := range tc.want {
			if !slices.Contains(lines, want) {
				t.Errorf("%s: no line %q in\n%s", tc.path, want, p.Text)
			}
		}
		if len(tc.want) == 0 && !tc.binary && p.Text != "" {
			t.Errorf("%s: want nothing, got\n%s", tc.path, p.Text)
		}
		if p.Binary != tc.binary {
			t.Errorf("%s: binary %v, got %+v", tc.path, tc.binary, p)
		}
	}
}

func TestUncommittedDiff(t *testing.T) {
	t.Parallel()
	dir := readRepo(t)
	write(t, dir, "bare.txt", "staged\n")
	gittest.Git(t, dir, "add", "bare.txt")
	p, err := UncommittedDiff(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"+2", "-two", "+staged", "+fresh", "diff --git a/new b/new"} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("no %q in\n%s", want, p.Text)
		}
	}
	// Changes only: a line far from any change is not in it, nor what is committed.
	if strings.Contains(p.Text, "+first") {
		t.Errorf("what is committed is in it:\n%s", p.Text)
	}
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-qm", "all")
	if p, err = UncommittedDiff(t.Context(), dir); err != nil || p.Text != "" {
		t.Errorf("all committed: %+v, %v", p, err)
	}
}

func TestFileDiffBeforeTheFirstCommit(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "empty")
	gittest.Git(t, filepath.Dir(dir), "init", "-q", dir)
	write(t, dir, "staged", "in the index\n")
	gittest.Git(t, dir, "add", "staged")
	p, err := FileDiff(t.Context(), dir, "staged")
	if err != nil || !strings.Contains(p.Text, "+in the index") {
		t.Errorf("got %+v, %v", p, err)
	}
}

func TestPathIsLiteral(t *testing.T) {
	t.Parallel()
	dir := readRepo(t)
	p, err := FileDiff(t.Context(), dir, "*")
	if err != nil || p.Text != "" {
		t.Errorf("a pathspec's magic was applied: %+v, %v", p, err)
	}
}

func TestBoundedBuffer(t *testing.T) {
	t.Parallel()
	b := &boundedBuffer{room: 4}
	for _, chunk := range []string{"abc", "defg", "h"} {
		if n, err := b.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("write %q: %d, %v", chunk, n, err)
		}
	}
	if !bytes.Equal(b.Bytes(), []byte("abcd")) || !b.dropped {
		t.Errorf("got %q, dropped %v", b.Bytes(), b.dropped)
	}
}

// TestPartialClone runs the reads of a clone lacking its files: one that runs by
// itself downloads nothing, one a user asked for may.
func TestPartialClone(t *testing.T) {
	t.Parallel()
	src := gittest.Repo(t, "src")
	gittest.Git(t, src, "config", "uploadpack.allowFilter", "true")
	gittest.Git(t, src, "config", "uploadpack.allowAnySHA1InWant", "true")
	gittest.Git(t, src, "branch", "-M", "main")
	write(t, src, "file", "base\n")
	gittest.Git(t, src, "add", ".")
	gittest.Git(t, src, "commit", "-qm", "base")
	gittest.Git(t, src, "switch", "-qc", "work")
	write(t, src, "file", "work\n")
	gittest.Git(t, src, "commit", "-qam", "work")
	gittest.Git(t, src, "switch", "-q", "main")

	dir := filepath.Join(t.TempDir(), "partial")
	gittest.Git(t, filepath.Dir(dir), "clone", "-q", "--filter=blob:none", "--no-checkout", "file://"+src, dir)
	gittest.Git(t, dir, "switch", "-qc", "here", "origin/work")

	// HEAD's file came with the checkout; the base's did not.
	before := gitFiles(t, filepath.Join(dir, ".git", "objects"))
	s, err := Summarize(t.Context(), dir, "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	if s.Ahead != 1 || s.Lines {
		t.Errorf("summary: want one ahead and no lines, got %+v", s)
	}
	after := gitFiles(t, filepath.Join(dir, ".git", "objects"))
	if len(after) != len(before) {
		t.Errorf("a read that runs by itself downloaded: %d files, then %d", len(before), len(after))
	}

	p, err := RangeDiff(t.Context(), dir, "origin/main")
	if err != nil || !strings.Contains(p.Text, "+work") {
		t.Errorf("one a user asked for: %+v, %v", p, err)
	}
}
