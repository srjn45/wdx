package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/waittest"
)

// landerUnderTest is a Lander whose changes finish waits on.
type landerUnderTest struct {
	*Lander
	changed chan struct{}
}

func newLander(t *testing.T, check func(ctx context.Context, projectID, name, dir, command string, started func(string)) error) *landerUnderTest {
	t.Helper()
	return newLanderWith(t, LanderOptions{Check: check})
}

func newLanderWith(t *testing.T, opts LanderOptions) *landerUnderTest {
	t.Helper()
	l := &landerUnderTest{changed: make(chan struct{}, 1)}
	opts.Changed = func(string) {
		select {
		case l.changed <- struct{}{}:
		default:
		}
	}
	l.Lander = NewLander(t.Context(), opts)
	t.Cleanup(l.Close)
	return l
}

// finish waits for the landing of path to end, and returns how.
func (l *landerUnderTest) finish(t *testing.T, path string) Landing {
	t.Helper()
	timer := time.NewTimer(time.Until(waittest.Deadline(t)))
	defer timer.Stop()
	for {
		if v, ok := l.Of("p", path); ok && v.Finished() {
			return v
		}
		select {
		case <-l.changed:
		case <-timer.C:
			v, _ := l.Of("p", path)
			t.Fatalf("the landing did not end: %+v", v)
		}
	}
}

func head(t *testing.T, dir, ref string) string {
	t.Helper()
	sha, err := git.Resolve(t.Context(), dir, ref)
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

func noCheck(context.Context, string, string, string, string, func(string)) error {
	return errors.New("no check was asked for")
}

func failingCheck(context.Context, string, string, string, string, func(string)) error {
	return errors.New("the check failed")
}

func TestLandMovesTheBase(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	commit(t, work, "more", "a\n")
	c := Checkouts{Clone: dir, Paths: []string{dir}}
	want := head(t, work, "HEAD")

	l := newLander(t, noCheck)
	if _, err := l.Start(t.Context(), "p", c, "work", nil, LandSettings{}); err != nil {
		t.Fatal(err)
	}
	v := l.finish(t, work)
	if v.State != LandLanded || v.Commit != want || v.Base != "main" {
		t.Fatalf("landing: %+v", v)
	}
	if got := head(t, dir, "main"); got != want {
		t.Errorf("main is at %s, want %s", got, want)
	}
	// The clone has main, so its files follow.
	if _, err := os.Stat(filepath.Join(dir, "more")); err != nil {
		t.Errorf("the clone's files: %v", err)
	}
}

func TestLandRebasesFirst(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	commit(t, work, "more", "a\n")
	before := head(t, work, "HEAD")
	commit(t, dir, "moved", "b\n")
	c := Checkouts{Clone: dir, Paths: []string{dir}}

	l := newLander(t, noCheck)
	if _, err := l.Start(t.Context(), "p", c, "work", nil, LandSettings{}); err != nil {
		t.Fatal(err)
	}
	v := l.finish(t, work)
	if v.State != LandLanded || v.Commit == before {
		t.Fatalf("landing: %+v", v)
	}
	if head(t, dir, "main") != head(t, work, "HEAD") {
		t.Error("main and the worktree differ")
	}
	if _, err := git.Resolve(t.Context(), dir, "HEAD^2"); err == nil {
		t.Error("a merge commit")
	}
}

func TestLandUndoesARebaseThatStops(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	commit(t, work, "file", "theirs\n")
	commit(t, dir, "file", "ours\n")
	before, base := head(t, work, "HEAD"), head(t, dir, "main")
	c := Checkouts{Clone: dir, Paths: []string{dir}}

	l := newLander(t, noCheck)
	if _, err := l.Start(t.Context(), "p", c, "work", nil, LandSettings{}); err != nil {
		t.Fatal(err)
	}
	v := l.finish(t, work)
	if v.State != LandFailed || v.Failure != LandRebaseFailed || !slices.Equal(v.Conflicts, []string{"file"}) || v.Head != before {
		t.Fatalf("landing: %+v", v)
	}
	if head(t, work, "HEAD") != before || head(t, dir, "main") != base {
		t.Error("something moved")
	}
	if busy, err := git.OperationInProgress(t.Context(), work); err != nil || busy {
		t.Errorf("a rebase was left: %v, %v", busy, err)
	}
	changes, err := git.Changes(t.Context(), work)
	if err != nil || len(changes) != 0 {
		t.Errorf("files were left changed: %+v, %v", changes, err)
	}
}

func TestLandStopsWhenTheCheckFails(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	commit(t, work, "more", "a\n")
	base := head(t, dir, "main")
	c := Checkouts{Clone: dir, Paths: []string{dir}}

	var ran string
	l := newLander(t, func(_ context.Context, _, name, at, command string, started func(string)) error {
		started("land-" + name)
		ran = at + " " + command
		return errors.New("the check exited with 2")
	})
	if _, err := l.Start(t.Context(), "p", c, "work", nil, LandSettings{Check: "make test"}); err != nil {
		t.Fatal(err)
	}
	v := l.finish(t, work)
	if v.State != LandFailed || v.Failure != LandCheckFailed || v.Terminal != "land-work" || !strings.Contains(v.Detail, "exited with 2") {
		t.Fatalf("landing: %+v", v)
	}
	if ran != work+" make test" {
		t.Errorf("ran %q", ran)
	}
	if head(t, dir, "main") != base {
		t.Error("the base moved")
	}
}

func TestLandTakesTheCommitChecked(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	commit(t, work, "more", "a\n")
	checked := head(t, work, "HEAD")
	c := Checkouts{Clone: dir, Paths: []string{dir}}

	// A commit made during the check has not been checked.
	l := newLander(t, func(_ context.Context, _, _, at, _ string, _ func(string)) error {
		commit(t, at, "late", "b\n")
		return nil
	})
	if _, err := l.Start(t.Context(), "p", c, "work", nil, LandSettings{Check: "make test"}); err != nil {
		t.Fatal(err)
	}
	if v := l.finish(t, work); v.State != LandLanded || v.Commit != checked {
		t.Fatalf("landing: %+v", v)
	}
	if got := head(t, dir, "main"); got != checked {
		t.Errorf("main is at %s, want %s", got, checked)
	}
}

func TestLandStopsWhenTheBaseMovesDuringTheCheck(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	commit(t, work, "more", "a\n")
	c := Checkouts{Clone: dir, Paths: []string{dir}}

	var moved string
	l := newLander(t, func(context.Context, string, string, string, string, func(string)) error {
		commit(t, dir, "moved", "b\n")
		moved = head(t, dir, "main")
		return nil
	})
	if _, err := l.Start(t.Context(), "p", c, "work", nil, LandSettings{Check: "make test"}); err != nil {
		t.Fatal(err)
	}
	if v := l.finish(t, work); v.State != LandFailed || v.Failure != LandBaseMoved {
		t.Fatalf("landing: %+v", v)
	}
	if head(t, dir, "main") != moved {
		t.Error("the base did not stay where it moved to")
	}
}

// Local changes in the base's checkout stay, whichever way it goes.
func TestLandInACheckoutWithLocalChanges(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	overlap := linked(t, dir, "overlap")
	commit(t, overlap, "file", "landing\n")
	apart := linked(t, dir, "apart")
	commit(t, apart, "other", "landing\n")
	write(t, dir, "file", "local\n")
	c := Checkouts{Clone: dir, Paths: []string{dir}}
	base := head(t, dir, "main")

	l := newLander(t, noCheck)
	if _, err := l.Start(t.Context(), "p", c, "overlap", nil, LandSettings{}); err != nil {
		t.Fatal(err)
	}
	if v := l.finish(t, overlap); v.State != LandFailed || v.Failure != LandFastForwardFailed {
		t.Fatalf("overlap: %+v", v)
	}
	if head(t, dir, "main") != base {
		t.Error("the base moved")
	}
	if _, err := l.Start(t.Context(), "p", c, "apart", nil, LandSettings{}); err != nil {
		t.Fatal(err)
	}
	if v := l.finish(t, apart); v.State != LandLanded {
		t.Fatalf("apart: %+v", v)
	}
	got, err := os.ReadFile(filepath.Join(dir, "file"))
	if err != nil || string(got) != "local\n" {
		t.Errorf("the local change: %q, %v", got, err)
	}
}

// With no checkout on the base, only the ref moves.
func TestLandOnABaseNoCheckoutHas(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	gittest.Git(t, dir, "switch", "-qc", "side")
	work := linked(t, dir, "work")
	commit(t, work, "more", "a\n")
	c := Checkouts{Clone: dir, Paths: []string{dir}}
	want := head(t, work, "HEAD")

	l := newLander(t, noCheck)
	if _, err := l.Start(t.Context(), "p", c, "work", nil, LandSettings{}); err != nil {
		t.Fatal(err)
	}
	if v := l.finish(t, work); v.State != LandLanded {
		t.Fatalf("landing: %+v", v)
	}
	if got := head(t, dir, "main"); got != want {
		t.Errorf("main is at %s, want %s", got, want)
	}
}

func TestLandPushes(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	gittest.Git(t, dir, "init", "-q", "--bare", origin)
	gittest.Git(t, dir, "remote", "add", "origin", origin)
	gittest.Git(t, dir, "push", "-q", "origin", "main")
	work := linked(t, dir, "work")
	commit(t, work, "more", "a\n")
	c := Checkouts{Clone: dir, Paths: []string{dir}}

	l := newLander(t, noCheck)
	if _, err := l.Start(t.Context(), "p", c, "work", nil, LandSettings{Push: true}); err != nil {
		t.Fatal(err)
	}
	v := l.finish(t, work)
	if v.State != LandLanded || !v.Pushed || v.PushError != "" {
		t.Fatalf("landing: %+v", v)
	}
	if got := head(t, origin, "main"); got != v.Commit {
		t.Errorf("origin is at %s, want %s", got, v.Commit)
	}

	// One that cannot push has landed all the same.
	if err := os.RemoveAll(origin); err != nil {
		t.Fatal(err)
	}
	second := linked(t, dir, "second")
	commit(t, second, "again", "b\n")
	if _, err := l.Start(t.Context(), "p", c, "second", nil, LandSettings{Push: true}); err != nil {
		t.Fatal(err)
	}
	if v = l.finish(t, second); v.State != LandLanded || v.Pushed || v.PushError == "" {
		t.Fatalf("no origin: %+v", v)
	}
}

func TestLandRefuses(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	c := Checkouts{Clone: dir, Paths: []string{dir}}
	linked(t, dir, "nothing")
	dirty := linked(t, dir, "dirty")
	commit(t, dirty, "more", "a\n")
	write(t, dirty, "fresh", "new\n")
	ahead := linked(t, dir, "ahead")
	commit(t, ahead, "more", "a\n")
	stopped := linked(t, dir, "stopped")
	commit(t, stopped, "file", "theirs\n")
	commit(t, dir, "file", "ours\n")
	if err := exec.Command("git", "-C", stopped, "rebase", "main").Run(); err == nil {
		t.Fatal("want the rebase to stop")
	}

	l := newLander(t, noCheck)
	for _, tc := range []struct {
		name   string
		agents []Agent
		want   error
	}{
		{"main", nil, ErrIsClone},
		{"nope", nil, ErrNotFound},
		{"nothing", nil, ErrNothingToLand},
		{"dirty", nil, ErrNotCommitted},
		{"stopped", nil, ErrWorktreeBusy},
		{"ahead", []Agent{{ID: "a", Dir: filepath.Join(ahead, "sub"), Working: true}}, ErrAgentWorking},
	} {
		if _, err := l.Start(t.Context(), "p", c, tc.name, tc.agents, LandSettings{}); !errors.Is(err, tc.want) {
			t.Errorf("%s: want %v, got %v", tc.name, tc.want, err)
		}
	}
	if _, ok := l.Of("p", ahead); ok {
		t.Error("a refusal left a landing")
	}
	// An agent idle there, or one working elsewhere, does not stop it.
	if _, err := l.Start(t.Context(), "p", c, "ahead", []Agent{{ID: "a", Dir: ahead}, {ID: "b", Dir: dir, Working: true}}, LandSettings{}); err != nil {
		t.Errorf("idle: %v", err)
	}
	l.finish(t, ahead)
}

func TestLandOneAtATime(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	commit(t, work, "more", "a\n")
	c := Checkouts{Clone: dir, Paths: []string{dir}}
	release := make(chan struct{})
	running := make(chan struct{})
	l := newLander(t, func(context.Context, string, string, string, string, func(string)) error {
		close(running)
		<-release
		return nil
	})
	if _, err := l.Start(t.Context(), "p", c, "work", nil, LandSettings{Check: "x"}); err != nil {
		t.Fatal(err)
	}
	<-running
	if _, err := l.Start(t.Context(), "p", c, "work", nil, LandSettings{Check: "x"}); !errors.Is(err, ErrLandingRunning) {
		t.Errorf("second: %v", err)
	}
	close(release)
	if v := l.finish(t, work); v.State != LandLanded {
		t.Errorf("landing: %+v", v)
	}
}

// planSlot returns a repository with the branch plan/x, which no checkout has, and a
// worktree on task, a branch from it, as a plan's slot.
func planSlot(t *testing.T, task string) (dir, slot string) {
	t.Helper()
	dir = repo(t)
	gittest.Git(t, dir, "branch", "plan/x")
	slot = filepath.Join(filepath.Dir(dir), task)
	gittest.Git(t, dir, "worktree", "add", "-q", slot, "-b", task, "plan/x")
	return dir, slot
}

// moveOn moves plan/x on by a commit of name made in a worktree of its own, as an
// earlier task's landing does.
func moveOn(t *testing.T, dir, name, content string) string {
	t.Helper()
	other := filepath.Join(filepath.Dir(dir), "other-"+name)
	gittest.Git(t, dir, "worktree", "add", "-q", "--detach", other, "plan/x")
	commit(t, other, name, content)
	tip := head(t, other, "HEAD")
	gittest.Git(t, dir, "update-ref", "refs/heads/plan/x", tip)
	return tip
}

func TestRebaseOnto(t *testing.T) {
	t.Parallel()
	dir, slot := planSlot(t, "task")
	commit(t, slot, "more", "a\n")
	want := head(t, slot, "HEAD")
	tip := head(t, dir, "plan/x")
	got, err := RebaseOnto(t.Context(), slot, "plan/x")
	if err != nil || got != want {
		t.Fatalf("rebased to %s, %v; want %s", got, err, want)
	}
	if head(t, dir, "plan/x") != tip {
		t.Error("plan/x moved")
	}
}

func TestRebaseOntoABaseMovedOn(t *testing.T) {
	t.Parallel()
	dir, slot := planSlot(t, "task")
	commit(t, slot, "more", "a\n")
	before := head(t, slot, "HEAD")
	tip := moveOn(t, dir, "earlier", "b\n")
	got, err := RebaseOnto(t.Context(), slot, "plan/x")
	if err != nil || got == before || got != head(t, slot, "HEAD") {
		t.Fatalf("rebased to %s, %v", got, err)
	}
	if head(t, slot, "HEAD^") != tip || head(t, dir, "plan/x") != tip {
		t.Error("the slot does not hold the earlier commit and then this one, or plan/x moved")
	}
}

func TestRebaseOntoNothingCommitted(t *testing.T) {
	t.Parallel()
	dir, slot := planSlot(t, "task")
	tip := head(t, dir, "plan/x")
	if got, err := RebaseOnto(t.Context(), slot, "plan/x"); err != nil || got != tip {
		t.Fatalf("rebased to %s, %v", got, err)
	}
}

func TestRebaseOntoUndoesARebaseThatStops(t *testing.T) {
	t.Parallel()
	dir, slot := planSlot(t, "task")
	commit(t, slot, "file", "theirs\n")
	before := head(t, slot, "HEAD")
	tip := moveOn(t, dir, "file", "ours\n")
	_, err := RebaseOnto(t.Context(), slot, "plan/x")
	e, ok := errors.AsType[*git.RebaseError](err)
	if !ok || !slices.Equal(e.Conflicts, []string{"file"}) {
		t.Fatalf("rebased: %v", err)
	}
	if head(t, slot, "HEAD") != before || head(t, dir, "plan/x") != tip {
		t.Error("something moved")
	}
	if busy, err := git.OperationInProgress(t.Context(), slot); err != nil || busy {
		t.Errorf("a rebase was left: %v, %v", busy, err)
	}
}
