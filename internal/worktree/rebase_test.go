package worktree

import (
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/gittest"
)

func TestRebaseUpToDate(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	commit(t, work, "more", "a\n")
	before := head(t, work, "HEAD")
	r, err := Rebase(t.Context(), Checkouts{Clone: dir, Paths: []string{dir}}, "work", nil)
	if err != nil || !r.UpToDate() || r.Before != before || r.After != before || r.Base != "main" || r.Pushed {
		t.Fatalf("rebase: %+v, %v", r, err)
	}
}

func TestRebaseOntoTheBase(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	commit(t, work, "more", "a\n")
	before := head(t, work, "HEAD")
	commit(t, dir, "later", "b\n")
	tip := head(t, dir, "main")
	c := Checkouts{Clone: dir, Paths: []string{dir}}
	if s, err := Find(t.Context(), c, "work"); err != nil {
		t.Fatal(err)
	} else if err := readCommits(t.Context(), &s); err != nil || intOf(s.Behind) != 1 {
		t.Fatalf("behind before: %v, %v", s.Behind, err)
	}

	r, err := Rebase(t.Context(), c, "work", []Agent{{ID: "a", Dir: work}, {ID: "b", Dir: dir, Working: true}})
	if err != nil || r.UpToDate() || r.Before != before || r.After != head(t, work, "HEAD") || r.Pushed {
		t.Fatalf("rebase: %+v, %v", r, err)
	}
	if head(t, work, "HEAD^") != tip || head(t, dir, "main") != tip {
		t.Error("work does not sit on main's tip, or main moved")
	}
	s, _ := Find(t.Context(), c, "work")
	if err := readCommits(t.Context(), &s); err != nil || intOf(s.Behind) != 0 || intOf(s.Ahead) != 1 {
		t.Errorf("after: behind %v, ahead %v, %v", s.Behind, s.Ahead, err)
	}
}

func TestRebaseUndoesAConflict(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	commit(t, work, "file", "theirs\n")
	before := head(t, work, "HEAD")
	commit(t, dir, "file", "ours\n")
	_, err := Rebase(t.Context(), Checkouts{Clone: dir, Paths: []string{dir}}, "work", nil)
	if e, ok := errors.AsType[*git.RebaseError](err); !ok || !slices.Equal(e.Conflicts, []string{"file"}) {
		t.Fatalf("rebase: %v", err)
	}
	if head(t, work, "HEAD") != before {
		t.Error("the branch moved")
	}
	if busy, err := git.OperationInProgress(t.Context(), work); err != nil || busy {
		t.Errorf("a rebase is left in progress: %v, %v", busy, err)
	}
}

func TestRebaseRefuses(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	c := Checkouts{Clone: dir, Paths: []string{dir}}
	dirty := linked(t, dir, "dirty")
	write(t, dirty, "fresh", "new\n")
	working := linked(t, dir, "working")
	stopped := linked(t, dir, "stopped")
	commit(t, stopped, "file", "theirs\n")
	commit(t, dir, "file", "ours\n")
	if err := exec.Command("git", "-C", stopped, "rebase", "main").Run(); err == nil {
		t.Fatal("want the rebase to stop")
	}
	gittest.Git(t, dir, "worktree", "add", "-q", "--detach", filepath.Join(filepath.Dir(dir), "detached"))

	for _, tc := range []struct {
		name   string
		agents []Agent
		want   error
	}{
		{"main", nil, ErrIsClone},
		{"nope", nil, ErrNotFound},
		{"dirty", nil, ErrNotCommitted},
		{"stopped", nil, ErrWorktreeBusy},
		{"detached", nil, ErrDetachedHead},
		{"working", []Agent{{ID: "a", Dir: filepath.Join(working, "sub"), Working: true}}, ErrAgentWorking},
	} {
		if _, err := Rebase(t.Context(), c, tc.name, tc.agents); !errors.Is(err, tc.want) {
			t.Errorf("%s: want %v, got %v", tc.name, tc.want, err)
		}
	}
	_, err := Rebase(t.Context(), c, "dirty", nil)
	if e, ok := errors.AsType[*NotCommittedError](err); !ok || !slices.Equal(e.Files, []string{"fresh"}) {
		t.Errorf("dirty: want the files, got %v", err)
	}

	// With no local default branch there is nothing to rebase onto.
	origin := filepath.Join(t.TempDir(), "origin.git")
	gittest.Git(t, dir, "init", "-q", "--bare", origin)
	gittest.Git(t, dir, "remote", "add", "origin", origin)
	gittest.Git(t, dir, "push", "-q", "origin", "main")
	gittest.Git(t, dir, "switch", "-q", "--detach")
	gittest.Git(t, dir, "branch", "-D", "main")
	if _, err := Rebase(t.Context(), c, "working", nil); !errors.Is(err, ErrNoLocalBase) {
		t.Errorf("no local base: %v", err)
	}
}

func TestRebasePushesOverItsCommitOnOrigin(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	gittest.Git(t, dir, "init", "-q", "--bare", origin)
	gittest.Git(t, dir, "remote", "add", "origin", origin)
	gittest.Git(t, dir, "push", "-q", "origin", "main")
	c := Checkouts{Clone: dir, Paths: []string{dir}}

	pushed := linked(t, dir, "pushed")
	commit(t, pushed, "more", "a\n")
	gittest.Git(t, pushed, "push", "-q", "origin", "pushed")
	alone := linked(t, dir, "alone")
	commit(t, alone, "other", "b\n")
	commit(t, dir, "later", "c\n")

	r, err := Rebase(t.Context(), c, "pushed", nil)
	if err != nil || !r.Pushed {
		t.Fatalf("rebase: %+v, %v", r, err)
	}
	if got := head(t, origin, "pushed"); got != r.After {
		t.Errorf("origin has %s, want %s", got, r.After)
	}
	// One not on origin is not pushed.
	if r, err := Rebase(t.Context(), c, "alone", nil); err != nil || r.UpToDate() || r.Pushed {
		t.Errorf("alone: %+v, %v", r, err)
	}
	if ok, _ := git.BranchExists(t.Context(), origin, "alone"); ok {
		t.Error("alone was pushed")
	}

	// Something pushed to origin since it was last fetched is never overwritten.
	other := filepath.Join(t.TempDir(), "other")
	gittest.Git(t, filepath.Dir(other), "clone", "-q", "-b", "pushed", origin, other)
	commit(t, other, "theirs", "d\n")
	gittest.Git(t, other, "push", "-q", "origin", "pushed")
	theirs := head(t, other, "HEAD")
	commit(t, dir, "again", "e\n")
	r, err = Rebase(t.Context(), c, "pushed", nil)
	if _, ok := errors.AsType[*PushError](err); !ok {
		t.Fatalf("want a push refused, got %+v, %v", r, err)
	}
	if head(t, origin, "pushed") != theirs {
		t.Error("origin's commit was overwritten")
	}
	if head(t, pushed, "HEAD^") != head(t, dir, "main") {
		t.Error("the rebase was not kept")
	}
}
