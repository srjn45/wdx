package worktree

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/db/dbtest"
	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/waittest"
)

// trackerHarness follows a project of one repository, and says when what is kept
// changed.
type trackerHarness struct {
	t       *testing.T
	tracker *Tracker
	dir     string
	changed chan struct{}

	mu     sync.Mutex
	agents []Agent
	reads  int    // how many times the checkouts were read
	during func() // run once at the end of a read, holding it open
	listed func() // run once after a read lists the checkouts, holding it open
	// reading is run as a read starts on a checkout, holding it open.
	reading func(path string)
}

func track(t *testing.T, dir string) *trackerHarness {
	t.Helper()
	return trackWith(t, dir, nil)
}

// trackWith is track with marks kept by the tracker, when not nil, and the
// harness set up before it follows the project.
func trackWith(t *testing.T, dir string, marks *Marks, setup ...func(*trackerHarness)) *trackerHarness {
	t.Helper()
	h := &trackerHarness{t: t, dir: dir, changed: make(chan struct{}, 1)}
	for _, f := range setup {
		f(h)
	}
	ctx, cancel := context.WithCancel(t.Context())
	h.tracker = NewTracker(ctx, Options{
		Checkouts: func(context.Context, string) (Checkouts, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.reads++
			return Checkouts{Clone: dir, Paths: []string{dir}}, nil
		},
		Agents: func(context.Context, string) ([]Agent, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			return slices.Clone(h.agents), nil
		},
		Changed: func() {
			select {
			case h.changed <- struct{}{}:
			default:
			}
		},
		Settle: time.Millisecond,
		Marks:  marks,
		listed: func() {
			h.mu.Lock()
			listed := h.listed
			h.listed = nil
			h.mu.Unlock()
			if listed != nil {
				listed()
			}
		},
		reading: func(path string) {
			h.mu.Lock()
			reading := h.reading
			h.mu.Unlock()
			if reading != nil {
				reading(path)
			}
		},
		readDone: func() {
			h.mu.Lock()
			during := h.during
			h.during = nil
			h.mu.Unlock()
			if during != nil {
				during()
			}
		},
	})
	stop := h.tracker.Follow("p")
	t.Cleanup(func() {
		stop()
		cancel()
		h.tracker.Wait()
	})
	return h
}

// waitFor waits until what is kept satisfies ok. Each change wakes it.
func (h *trackerHarness) waitFor(what string, ok func([]Status) bool) {
	h.t.Helper()
	deadline := time.NewTimer(waittest.Limit(h.t))
	defer deadline.Stop()
	for {
		list := h.tracker.Statuses("p")
		if list != nil && ok(list) {
			return
		}
		select {
		case <-h.changed:
		case <-deadline.C:
			h.t.Fatalf("waiting for %s, last saw %+v", what, list)
		}
	}
}

// isRead says the worktree is listed and was read: listed alone, its files and
// commits may not be known yet.
func isRead(list []Status, worktree string) bool {
	s, ok := named2(list, worktree)
	return ok && !s.Unread
}

func fileIn(list []Status, worktree, path string) bool {
	s, ok := named2(list, worktree)
	return ok && slices.ContainsFunc(s.Files, func(f File) bool { return f.Path == path })
}

func TestTrackerReadsWhenFollowed(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	write(t, dir, "fresh", "new\n")
	h := track(t, dir)
	h.waitFor("the first read", func(list []Status) bool { return fileIn(list, "main", "fresh") })
}

// TestTrackerReadsCheckoutsAtOnce holds the read of the clone open: the worktree
// is read meanwhile and told of, and the clone shows as not read yet.
func TestTrackerReadsCheckoutsAtOnce(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	write(t, work, "fresh", "new\n")
	held := make(chan struct{})
	release := sync.OnceFunc(func() { close(held) })
	h := trackWith(t, dir, nil, func(h *trackerHarness) {
		h.reading = func(path string) {
			if path == dir {
				<-held
			}
		}
	})
	t.Cleanup(release) // before the tracker's cleanup waits for its reads
	h.waitFor("the worktree read with the clone held", func(list []Status) bool {
		clone, _ := named2(list, "main")
		return fileIn(list, "work", "fresh") && clone.Unread
	})
	release()
	h.waitFor("the clone read", func(list []Status) bool {
		clone, _ := named2(list, "main")
		return !clone.Unread
	})
}

// TestTrackerNotFollowed is the promise that nobody following costs no git.
func TestTrackerNotFollowed(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	reads := 0
	tracker := NewTracker(t.Context(), Options{
		Checkouts: func(context.Context, string) (Checkouts, error) {
			reads++
			return Checkouts{Clone: dir, Paths: []string{dir}}, nil
		},
		Agents:  func(context.Context, string) ([]Agent, error) { return nil, nil },
		Changed: func() {},
	})
	tracker.Refresh("p", "")
	tracker.AgentsChanged()
	if got := tracker.Statuses("p"); got != nil {
		t.Errorf("kept %+v", got)
	}
	if reads != 0 {
		t.Errorf("read %d times", reads)
	}
}

func TestTrackerSeesGitCommands(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	h := track(t, dir)
	h.waitFor("the worktree read", func(list []Status) bool { return isRead(list, "work") })

	// git add changes the index, and only that.
	write(t, work, "staged", "s\n")
	gittest.Git(t, work, "add", "staged")
	h.waitFor("a file added", func(list []Status) bool { return fileIn(list, "work", "staged") })

	// git commit moves HEAD.
	gittest.Git(t, work, "commit", "-qm", "staged")
	h.waitFor("a commit", func(list []Status) bool {
		s, _ := named2(list, "work")
		return intOf(s.Ahead) == 1 && len(s.Files) == 0
	})

	// reset --soft moves only HEAD's log: no index is written.
	gittest.Git(t, work, "reset", "-q", "--soft", "HEAD~1")
	h.waitFor("a soft reset", func(list []Status) bool {
		s, _ := named2(list, "work")
		return intOf(s.Ahead) == 0 && fileIn(list, "work", "staged")
	})
}

func TestTrackerSeesWorktreesComeAndGo(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	h := track(t, dir) // with no worktrees directory yet
	h.waitFor("the clone", func(list []Status) bool { return len(list) == 1 })

	later := linked(t, dir, "later")
	h.waitFor("a worktree added and read", func(list []Status) bool { return isRead(list, "later") })

	gittest.Git(t, dir, "worktree", "remove", later)
	h.waitFor("a worktree removed", func(list []Status) bool { _, ok := named2(list, "later"); return !ok })
}

func TestTrackerSeesTheBaseMove(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	gittest.Git(t, dir, "switch", "-qc", "elsewhere")
	commit(t, work, "mine", "m\n")
	h := track(t, dir)
	h.waitFor("one ahead", func(list []Status) bool {
		s, _ := named2(list, "work")
		return intOf(s.Ahead) == 1
	})

	// Landed by moving main, which no checkout has checked out: only its ref and reflog say.
	gittest.Git(t, dir, "update-ref", "refs/heads/main", "work")
	h.waitFor("landed", func(list []Status) bool {
		s, _ := named2(list, "work")
		return intOf(s.Ahead) == 0
	})
}

// TestTrackerWatchesABaseBeforeTellingOfIt moves the base right after the worktree
// was told of, while another checkout's read holds the read open: the move must
// be seen, so the base was watched before the worktree was told of.
func TestTrackerWatchesABaseBeforeTellingOfIt(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	gittest.Git(t, dir, "switch", "-qc", "elsewhere")
	commit(t, work, "mine", "m\n")
	held := make(chan struct{})
	release := sync.OnceFunc(func() { close(held) })
	h := trackWith(t, dir, nil, func(h *trackerHarness) {
		h.reading = func(path string) {
			if path == dir {
				<-held
			}
		}
	})
	t.Cleanup(release) // before the tracker's cleanup waits for its reads
	h.waitFor("one ahead, the clone held", func(list []Status) bool {
		s, _ := named2(list, "work")
		return intOf(s.Ahead) == 1
	})
	gittest.Git(t, dir, "update-ref", "refs/heads/main", "work")
	release()
	h.waitFor("landed", func(list []Status) bool {
		s, _ := named2(list, "work")
		return intOf(s.Ahead) == 0
	})
}

// oneAheadOfMain returns a repository whose worktree "work" is a commit ahead of
// main, which no checkout has checked out.
func oneAheadOfMain(t *testing.T) string {
	t.Helper()
	dir := repo(t)
	work := linked(t, dir, "work")
	gittest.Git(t, dir, "switch", "-qc", "elsewhere")
	commit(t, work, "mine", "m\n")
	return dir
}

// TestTrackerSeesTheRefMoveAfterItsReflog takes the moment git is between writing
// the reflog and moving the ref, when a read finds the old value: only the ref's
// own move says it is time to read again. With no reflog to write, the move is
// all there is.
func TestTrackerSeesTheRefMoveAfterItsReflog(t *testing.T) {
	t.Parallel()
	dir := oneAheadOfMain(t)
	gittest.Git(t, dir, "config", "core.logAllRefUpdates", "false")
	if err := os.Remove(filepath.Join(dir, ".git", "logs", "refs", "heads", "main")); err != nil {
		t.Fatal(err)
	}
	h := track(t, dir)
	h.waitFor("one ahead", func(list []Status) bool {
		s, _ := named2(list, "work")
		return intOf(s.Ahead) == 1
	})

	gittest.Git(t, dir, "update-ref", "refs/heads/main", "work")
	h.waitFor("landed", func(list []Status) bool {
		s, _ := named2(list, "work")
		return intOf(s.Ahead) == 0
	})
}

// TestTrackerSeesTheBaseMoveDuringARead moves the base when a read has read the
// commits and has not told of them yet: the watch must hold through the read, for
// the move to fire a read of its own.
func TestTrackerSeesTheBaseMoveDuringARead(t *testing.T) {
	t.Parallel()
	dir := oneAheadOfMain(t)
	h := track(t, dir)
	h.waitFor("one ahead", func(list []Status) bool {
		s, _ := named2(list, "work")
		return intOf(s.Ahead) == 1
	})

	reading, moved := make(chan struct{}), make(chan struct{})
	h.mu.Lock()
	h.during = func() {
		close(reading)
		<-moved
	}
	h.mu.Unlock()
	h.tracker.Refresh("p", "work")
	select {
	case <-reading:
	case <-waittest.After(t):
		close(moved)
		t.Fatal("no read came of the refresh")
	}
	gittest.Git(t, dir, "update-ref", "refs/heads/main", "work")
	close(moved)
	h.waitFor("landed", func(list []Status) bool {
		s, _ := named2(list, "work")
		return intOf(s.Ahead) == 0
	})
}

// TestTrackerKeepsTheMarkOfAFileCommittedDuringARead commits a verified file after
// a read listed the checkouts and before it reads their files: that read finds the
// file in neither the changes nor, by the head it listed, the commits. The mark is
// lost for good if the read goes by that.
func TestTrackerKeepsTheMarkOfAFileCommittedDuringARead(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	path := filepath.Join(t.TempDir(), "wdx.db")
	dbtest.Seed(t, path)
	database, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if _, err := database.ExecContext(t.Context(), "INSERT INTO projects (id, name, state, created_at, updated_at) VALUES ('p', 'p', 'open', 0, 0)"); err != nil {
		t.Fatal(err)
	}
	marks := NewMarks(database)
	h := trackWith(t, dir, marks)

	write(t, work, "file", "two\n")
	gittest.Git(t, work, "add", "file")
	h.waitFor("the file changed", func(list []Status) bool { return fileIn(list, "work", "file") })
	h.waitFor("its commits read", func(list []Status) bool {
		s, _ := named2(list, "work")
		return s.Ahead != nil
	})
	if _, err := Verify(t.Context(), marks, "p", Checkouts{Clone: dir, Paths: []string{dir}}, "work", "file", ""); err != nil {
		t.Fatal(err)
	}

	reading, committed := make(chan struct{}), make(chan struct{})
	h.mu.Lock()
	h.listed = func() {
		close(reading)
		<-committed
	}
	h.mu.Unlock()
	// Staging something else reads only the changes, as the index is all that moved.
	write(t, work, "other", "o\n")
	gittest.Git(t, work, "add", "other")
	select {
	case <-reading:
	case <-waittest.After(t):
		close(committed)
		t.Fatal("no read came of staging a file")
	}
	gittest.Git(t, work, "commit", "-qm", "two", "--", "file")
	close(committed)

	var row CommittedFile
	h.waitFor("the file among the commits", func(list []Status) bool {
		s, _ := named2(list, "work")
		i := slices.IndexFunc(s.Committed, func(f CommittedFile) bool { return f.Path == "file" })
		if i < 0 {
			return false
		}
		row = s.Committed[i]
		return true
	})
	if !row.Verified {
		t.Fatal("the commit took the mark away")
	}
}

// TestTrackerAgentStops shows the event for what git's files never say: an edit
// not staged, seen when the agent that made it stops.
func TestTrackerAgentStops(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	h := track(t, dir)
	h.waitFor("the worktree read", func(list []Status) bool { return isRead(list, "work") })

	h.setAgents(Agent{ID: "a", Dir: filepath.Join(work, "sub"), Working: true})
	h.tracker.AgentsChanged()
	write(t, work, "edited", "e\n")
	// Still working: a change elsewhere reads only its own worktree, so the edit does not show.
	write(t, dir, "elsewhere", "e\n")
	gittest.Git(t, dir, "add", "elsewhere")
	h.waitFor("the other worktree read", func(list []Status) bool { return fileIn(list, "main", "elsewhere") })
	if fileIn(h.tracker.Statuses("p"), "work", "edited") {
		t.Fatal("read while the agent was working")
	}

	h.setAgents(Agent{ID: "a", Dir: filepath.Join(work, "sub"), Working: false})
	h.tracker.AgentsChanged()
	h.waitFor("the agent's edit", func(list []Status) bool { return fileIn(list, "work", "edited") })
}

// TestTrackerAgentMoved reads the worktree an agent turns out to have moved to: it
// is learned of only after the agent stopped, when the stop read the old one.
func TestTrackerAgentMoved(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	h := track(t, dir)
	h.setAgents(Agent{ID: "a", Dir: dir})
	h.waitFor("the worktree read", func(list []Status) bool { return isRead(list, "work") })
	h.tracker.AgentsChanged()

	write(t, work, "edited", "e\n") // says nothing to git's files
	h.setAgents(Agent{ID: "a", Dir: work})
	h.tracker.AgentsChanged()
	h.waitFor("its edit", func(list []Status) bool { return fileIn(list, "work", "edited") })
}

func (h *trackerHarness) setAgents(agents ...Agent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.agents = agents
}

func TestTrackerRefresh(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	h := track(t, dir)
	h.waitFor("the worktree read", func(list []Status) bool { return isRead(list, "work") })

	write(t, work, "edited", "e\n") // says nothing to git's files
	h.tracker.Refresh("p", "work")
	h.waitFor("the edit", func(list []Status) bool { return fileIn(list, "work", "edited") })

	write(t, dir, "too", "e\n")
	h.tracker.Refresh("p", "")
	h.waitFor("all of them", func(list []Status) bool { return fileIn(list, "main", "too") })
}

// TestTrackerWaitsForTheIndexLock holds the lock as a long pull does: nothing runs
// in that worktree until it ends, which fires the read by itself.
func TestTrackerWaitsForTheIndexLock(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	h := track(t, dir)
	h.waitFor("the worktree read", func(list []Status) bool { return isRead(list, "work") })

	gitDir := filepath.Join(dir, ".git", "worktrees", "work")
	lock := filepath.Join(gitDir, "index.lock")
	write(t, gitDir, "index.lock", "held")
	write(t, work, "edited", "e\n")
	// An agent stopping would read it, were it not locked.
	h.setAgents(Agent{ID: "a", Dir: work, Working: true})
	h.tracker.AgentsChanged()
	h.setAgents()
	h.tracker.AgentsChanged()
	// That comes before a later change that must be seen: nothing about the lock.
	write(t, dir, "elsewhere", "e\n")
	gittest.Git(t, dir, "add", "elsewhere")
	h.waitFor("the other worktree", func(list []Status) bool { return fileIn(list, "main", "elsewhere") })
	if fileIn(h.tracker.Statuses("p"), "work", "edited") {
		t.Fatal("read with the index locked")
	}

	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	h.waitFor("the read when the lock ended", func(list []Status) bool { return fileIn(list, "work", "edited") })
}

// TestTrackerRefreshIgnoresALockLeftBehind is a crashed git's lock, which must not
// hide changes for good.
func TestTrackerRefreshIgnoresALockLeftBehind(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	h := track(t, dir)
	h.waitFor("the clone", func(list []Status) bool { return len(list) == 1 })

	write(t, filepath.Join(dir, ".git"), "index.lock", "left by a crash")
	write(t, dir, "edited", "e\n")
	h.tracker.Refresh("p", "main")
	h.waitFor("the edit", func(list []Status) bool { return fileIn(list, "main", "edited") })
}

// TestTrackerLastFollowerLeaves drops everything, and the next follower reads anew.
func TestTrackerLastFollowerLeaves(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	h := track(t, dir)
	h.waitFor("the clone", func(list []Status) bool { return len(list) == 1 })
	second := h.tracker.Follow("p")
	second()
	second() // calling it twice counts once
	if h.tracker.Statuses("p") == nil {
		t.Fatal("dropped while still followed")
	}
}

func TestHolding(t *testing.T) {
	t.Parallel()
	statuses := []Status{{Path: "/r"}, {Path: "/r/.worktrees/a"}, {Path: "/other"}}
	for dir, want := range map[string]string{
		"/r":                   "/r",
		"/r/sub":               "/r",
		"/r/.worktrees/a":      "/r/.worktrees/a",
		"/r/.worktrees/a/deep": "/r/.worktrees/a",
		"/rest":                "",
	} {
		if got, ok := holding(statuses, dir); got != want || ok != (want != "") {
			t.Errorf("%s: want %q, got %q", dir, want, got)
		}
	}
}
