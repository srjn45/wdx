package worktree

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/waittest"
)

// waitChanged waits for Changed to be called.
func (h *trackerHarness) waitChanged(what string) {
	h.t.Helper()
	deadline := time.NewTimer(waittest.Limit(h.t))
	defer deadline.Stop()
	select {
	case <-h.changed:
	case <-deadline.C:
		h.t.Fatalf("waiting for %s: Changed not called", what)
	}
}

// TestTrackerWatchesTabs: a tab's file removed or created outside git, which no
// watch on git sees, calls Changed, and a tab's worktree removed with its
// directory is read as missing.
func TestTrackerWatchesTabs(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	work := linked(t, dir, "work")
	write(t, work, ".gitignore", "*.log\n")
	ignored := filepath.Join(work, "x.log")
	write(t, work, "x.log", "x\n")
	h := track(t, dir)
	h.waitFor("the worktree read", func(list []Status) bool { return isRead(list, "work") })
	h.tracker.WatchTabs("p", []string{ignored}, []string{work})
	select {
	case <-h.changed: // what told of the read
	default:
	}

	if err := os.Remove(ignored); err != nil {
		t.Fatal(err)
	}
	h.waitChanged("the ignored file removed")
	write(t, work, "x.log", "x\n")
	h.waitChanged("the ignored file back")

	if err := os.RemoveAll(work); err != nil {
		t.Fatal(err)
	}
	h.waitFor("the worktree missing", func(list []Status) bool {
		s, ok := named2(list, "work")
		return ok && s.Missing
	})
}
