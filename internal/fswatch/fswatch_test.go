package fswatch

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/fsnotify/fsnotify"

	"github.com/srjn45/wdx/internal/waittest"
)

// sub keeps what it is told, for the test to take.
type sub struct {
	changed chan fsnotify.Event
	lost    chan struct{}
}

func newSub() *sub {
	return &sub{changed: make(chan fsnotify.Event, 64), lost: make(chan struct{}, 8)}
}

func (s *sub) Changed(e fsnotify.Event) { s.changed <- e }
func (s *sub) Lost()                    { s.lost <- struct{}{} }

// next returns the next event s is told of, waiting until the deadline.
func (s *sub) next(t *testing.T) fsnotify.Event {
	t.Helper()
	select {
	case e := <-s.changed:
		return e
	case <-waittest.After(t):
		t.Fatal("no event came")
		return fsnotify.Event{}
	}
}

// nextIn returns the next event s is told of in dir: a directory created by the
// test gets a few of its own.
func (s *sub) nextIn(t *testing.T, dir string) fsnotify.Event {
	t.Helper()
	for {
		if e := s.next(t); filepath.Dir(e.Name) == dir || e.Name == dir {
			return e
		}
	}
}

// waitLost waits until s is told events were lost.
func (s *sub) waitLost(t *testing.T) {
	t.Helper()
	select {
	case <-s.lost:
	case <-waittest.After(t):
		t.Fatal("not told of a loss")
	}
}

// toldNothing fails if s was told of an event: call it once an event that came
// after has reached another subscriber, so that s would have had its turn.
func (s *sub) toldNothing(t *testing.T) {
	t.Helper()
	select {
	case e := <-s.changed:
		t.Fatalf("told of %v", e)
	default:
	}
}

// toldNothingIn fails if s was told of an event in dir: call it once an event that
// came after has reached s.
func (s *sub) toldNothingIn(t *testing.T, dir string) {
	t.Helper()
	for {
		select {
		case e := <-s.changed:
			if filepath.Dir(e.Name) == dir || e.Name == dir {
				t.Fatalf("told of %v in %s", e, dir)
			}
		default:
			return
		}
	}
}

func newWatcher(t *testing.T) *Watcher {
	t.Helper()
	w := New(t.Context())
	t.Cleanup(w.Close)
	if !w.Watching() {
		t.Fatal("no inotify instance left for the test: fs.inotify.max_user_instances (128 by default) are all in use")
	}
	return w
}

// watched returns the paths the instance watches, sorted.
func (w *Watcher) watched() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.inst == nil {
		return nil
	}
	list := w.inst.WatchList()
	slices.Sort(list)
	return list
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAPathAddedByTwoIsWatchedOnceAndToldToBoth(t *testing.T) {
	t.Parallel()
	w := newWatcher(t)
	dir := t.TempDir()
	a, b := newSub(), newSub()
	for _, s := range []*sub{a, b} {
		if err := w.Add(s, dir); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.watched(); !slices.Equal(got, []string{dir}) {
		t.Fatalf("watched %v, want %v once", got, dir)
	}
	file := filepath.Join(dir, "f")
	touch(t, file)
	for _, s := range []*sub{a, b} {
		if e := s.nextIn(t, dir); e.Name != file || !e.Has(fsnotify.Create) {
			t.Errorf("told of %v, want %s created", e, file)
		}
	}
}

func TestRemovingOneKeepsTheOther(t *testing.T) {
	t.Parallel()
	w := newWatcher(t)
	dir := t.TempDir()
	a, b := newSub(), newSub()
	for _, s := range []*sub{a, b} {
		if err := w.Add(s, dir); err != nil {
			t.Fatal(err)
		}
	}
	w.Remove(a, dir)
	if got := w.watched(); !slices.Equal(got, []string{dir}) {
		t.Fatalf("watched %v after one left, want %v", got, dir)
	}
	touch(t, filepath.Join(dir, "f1"))
	touch(t, filepath.Join(dir, "f2"))
	for b.nextIn(t, dir).Name != filepath.Join(dir, "f2") {
	}
	a.toldNothing(t)
	w.Remove(b, dir)
	if got := w.watched(); len(got) != 0 {
		t.Fatalf("watched %v after both left", got)
	}
}

func TestDropRemovesEveryPathOfOne(t *testing.T) {
	t.Parallel()
	w := newWatcher(t)
	one, two := t.TempDir(), t.TempDir()
	a, b := newSub(), newSub()
	for _, add := range []struct {
		s    *sub
		path string
	}{{a, one}, {a, two}, {b, one}} {
		if err := w.Add(add.s, add.path); err != nil {
			t.Fatal(err)
		}
	}
	w.Drop(a)
	if got := w.watched(); !slices.Equal(got, []string{one}) {
		t.Fatalf("watched %v after a dropped, want %v", got, one)
	}
	touch(t, filepath.Join(one, "f1"))
	touch(t, filepath.Join(one, "f2"))
	for b.nextIn(t, one).Name != filepath.Join(one, "f2") {
	}
	a.toldNothing(t)
}

func TestSetMakesThePathsExactly(t *testing.T) {
	t.Parallel()
	w := newWatcher(t)
	one, two, three := t.TempDir(), t.TempDir(), t.TempDir()
	a := newSub()
	w.Set(a, map[string]bool{one: true, two: true})
	want := []string{one, two}
	slices.Sort(want)
	if got := w.watched(); !slices.Equal(got, want) {
		t.Fatalf("watched %v, want %v", got, want)
	}
	w.Set(a, map[string]bool{two: true, three: true})
	want = []string{two, three}
	slices.Sort(want)
	if got := w.watched(); !slices.Equal(got, want) {
		t.Fatalf("watched %v, want %v", got, want)
	}
	touch(t, filepath.Join(one, "f"))
	touch(t, filepath.Join(three, "f"))
	if e := a.nextIn(t, three); e.Name != filepath.Join(three, "f") {
		t.Fatalf("told of %v, want the file in %s", e, three)
	}
	a.toldNothingIn(t, one)
}

func TestAnEventOnTheDirectoryItselfReachesItsSubscribers(t *testing.T) {
	t.Parallel()
	w := newWatcher(t)
	dir := filepath.Join(t.TempDir(), "sub")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	a := newSub()
	if err := w.Add(a, dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if e := a.nextIn(t, dir); e.Name != dir || !e.Has(fsnotify.Remove) {
		t.Fatalf("told of %v, want %s removed", e, dir)
	}
}

func TestAPathGoneIsWatchedAgainOnceAddedBack(t *testing.T) {
	t.Parallel()
	w := newWatcher(t)
	file := filepath.Join(t.TempDir(), "f")
	touch(t, file)
	a := newSub()
	if err := w.Add(a, file); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	for !a.next(t).Has(fsnotify.Remove) {
	}
	if got := w.watched(); len(got) != 0 {
		t.Fatalf("watched %v after the file went", got)
	}
	touch(t, file)
	if err := w.Add(a, file); err != nil {
		t.Fatal(err)
	}
	touch(t, file)
	if e := a.next(t); e.Name != file || !e.Has(fsnotify.Write) {
		t.Fatalf("told of %v, want %s written", e, file)
	}
}

func TestAddOfAMissingPathSaysSo(t *testing.T) {
	t.Parallel()
	w := newWatcher(t)
	a := newSub()
	err := w.Add(a, filepath.Join(t.TempDir(), "nope"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v, want not exist", err)
	}
	if got := w.watched(); len(got) != 0 {
		t.Fatalf("watched %v, want nothing", got)
	}
}

func TestRefusedThenHealed(t *testing.T) {
	t.Parallel()
	var refuse atomic.Bool
	refuse.Store(true)
	w := newWith(t.Context(), func() (*fsnotify.Watcher, error) {
		if refuse.Load() {
			return nil, syscall.EMFILE
		}
		return fsnotify.NewWatcher()
	})
	t.Cleanup(w.Close)
	if w.Watching() {
		t.Fatal("watching with no instance")
	}
	one, two, three := t.TempDir(), t.TempDir(), t.TempDir()
	a, b := newSub(), newSub()
	if err := w.Add(a, one); err != nil {
		t.Fatalf("add while refused: %v", err)
	}
	w.Set(b, map[string]bool{two: true})
	if w.Watching() {
		t.Fatal("watching after adds while refused")
	}

	refuse.Store(false)
	if err := w.Add(a, three); err != nil {
		t.Fatal(err)
	}
	if !w.Watching() {
		t.Fatal("not watching once the kernel allows it")
	}
	a.waitLost(t)
	b.waitLost(t)
	want := []string{one, two, three}
	slices.Sort(want)
	if got := w.watched(); !slices.Equal(got, want) {
		t.Fatalf("watched %v, want %v", got, want)
	}
	touch(t, filepath.Join(one, "f"))
	if e := a.nextIn(t, one); e.Name != filepath.Join(one, "f") {
		t.Fatalf("told of %v, want the file in %s", e, one)
	}
}

func TestCloseEndsTheWatching(t *testing.T) {
	t.Parallel()
	w := New(t.Context())
	if !w.Watching() {
		t.Fatal("no inotify instance left for the test: fs.inotify.max_user_instances (128 by default) are all in use")
	}
	a := newSub()
	if err := w.Add(a, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	w.Close()
	w.Close()
	if w.Watching() {
		t.Fatal("watching after close")
	}
	if err := w.Add(a, t.TempDir()); !errors.Is(err, ErrClosed) {
		t.Fatalf("add after close: %v, want %v", err, ErrClosed)
	}
}
