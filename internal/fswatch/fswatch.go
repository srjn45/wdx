// Package fswatch watches files for the daemon through one inotify instance:
// each path is watched once however many ask, and a change is handed to each
// of them. The kernel allows a user fs.inotify.max_user_instances instances,
// 128 by default, shared by everything the user runs; the wdx doc WATCHERS.
package fswatch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"

	"github.com/srjn45/wdx/internal/logging"
)

// Subscriber is told of changes to the paths it added. Both methods run on
// the one goroutine reading the instance and must not block: set a mark,
// send on a buffered channel, return.
type Subscriber interface {
	// Changed is given each event on a path it added, or in a directory it added.
	Changed(e fsnotify.Event)
	// Lost says events were lost, so everything it watches may have changed.
	Lost()
}

// ErrClosed is returned by Add once the watcher is closed.
var ErrClosed = errors.New("the watcher is closed")

// Watcher is one inotify instance shared by all who watch through it.
type Watcher struct {
	ctx  context.Context
	open func() (*fsnotify.Watcher, error)

	mu      sync.Mutex
	inst    *fsnotify.Watcher // nil while the kernel refuses one, and once closed
	reading chan struct{}     // closed when the goroutine reading inst ends
	paths   map[string]map[Subscriber]bool
	subs    map[Subscriber]map[string]bool
	closed  bool
	refused bool // the refusal is logged, until an instance opens
}

// New opens the instance, or runs without one when the kernel refuses, as
// when the user's fs.inotify.max_user_instances are all in use: it then tries
// again on every Add and Set, and tells every subscriber Lost once it has one.
func New(ctx context.Context) *Watcher {
	return newWith(ctx, fsnotify.NewWatcher)
}

func newWith(ctx context.Context, open func() (*fsnotify.Watcher, error)) *Watcher {
	w := &Watcher{ctx: ctx, open: open, paths: map[string]map[Subscriber]bool{}, subs: map[Subscriber]map[string]bool{}}
	w.mu.Lock()
	w.tryOpen()
	w.mu.Unlock()
	return w
}

// Watching reports whether the instance is open: false means no change is seen.
func (w *Watcher) Watching() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.inst != nil
}

// Add watches path for s, a directory or a file. A path not there is not
// watched, and said so: add it again when its parent says it came.
func (w *Watcher) Add(s Subscriber, path string) error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return ErrClosed
	}
	lost := w.opened()
	err := w.add(s, filepath.Clean(path))
	w.mu.Unlock()
	tellLost(lost)
	return err
}

// Remove stops watching path for s; the kernel's watch goes when the last leaves.
func (w *Watcher) Remove(s Subscriber, path string) {
	w.mu.Lock()
	w.remove(s, filepath.Clean(path))
	w.mu.Unlock()
}

// Set makes the paths s watches exactly want: what Remove and Add would do,
// in one. A path that cannot be watched is logged, unless it is not there.
func (w *Watcher) Set(s Subscriber, want map[string]bool) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	lost := w.opened()
	clean := make(map[string]bool, len(want))
	for path := range want {
		clean[filepath.Clean(path)] = true
	}
	for path := range w.subs[s] {
		if !clean[path] {
			w.remove(s, path)
		}
	}
	for path := range clean {
		if w.subs[s][path] {
			continue
		}
		if err := w.add(s, path); err != nil && !errors.Is(err, os.ErrNotExist) {
			logging.FromContext(w.ctx).Info("path not watched", "path", path, "err", err)
		}
	}
	w.mu.Unlock()
	tellLost(lost)
}

// Drop removes every path of s.
func (w *Watcher) Drop(s Subscriber) {
	w.mu.Lock()
	for path := range w.subs[s] {
		w.remove(s, path)
	}
	w.mu.Unlock()
}

// Close closes the instance and waits for its reading to end. The daemon
// calls it once, as it stops; nothing is watched after.
func (w *Watcher) Close() {
	w.mu.Lock()
	w.closed = true
	inst, reading := w.inst, w.reading
	w.inst = nil
	w.mu.Unlock()
	if inst != nil {
		_ = inst.Close()
		<-reading
	}
}

// opened opens the instance when there is none, under w.mu, and returns the
// subscribers to tell Lost when it just did: what changed while there was none
// went unseen.
func (w *Watcher) opened() []Subscriber {
	if !w.tryOpen() {
		return nil
	}
	lost := make([]Subscriber, 0, len(w.subs))
	for s := range w.subs {
		lost = append(lost, s)
	}
	return lost
}

// tryOpen opens the instance when there is none, under w.mu, and watches
// every path wanted meanwhile; true when it just opened.
func (w *Watcher) tryOpen() bool {
	if w.inst != nil || w.closed {
		return false
	}
	log := logging.FromContext(w.ctx)
	inst, err := w.open()
	if err != nil {
		if !w.refused {
			log.Error("files not watched", "err", err, "setting", "fs.inotify.max_user_instances")
			w.refused = true
		}
		return false
	}
	w.inst, w.reading = inst, make(chan struct{})
	go w.read(inst, w.reading)
	for path, subs := range w.paths {
		if err := inst.Add(path); err != nil {
			// Set adds it again when its subscriber wants it still.
			log.Info("path not watched", "path", path, "err", err)
			for s := range subs {
				w.remove(s, path)
			}
		}
	}
	if w.refused {
		log.Info("files watched again", "paths", len(w.paths))
		w.refused = false
	}
	return true
}

// add records that s watches path, under w.mu, watching it when s is its first:
// an error leaves nothing recorded.
func (w *Watcher) add(s Subscriber, path string) error {
	if w.paths[path][s] {
		return nil
	}
	if len(w.paths[path]) == 0 && w.inst != nil {
		if err := w.inst.Add(path); err != nil {
			return err
		}
	}
	if w.paths[path] == nil {
		w.paths[path] = map[Subscriber]bool{}
	}
	if w.subs[s] == nil {
		w.subs[s] = map[string]bool{}
	}
	w.paths[path][s], w.subs[s][path] = true, true
	return nil
}

// remove forgets that s watches path, under w.mu, unwatching it when s was its last.
func (w *Watcher) remove(s Subscriber, path string) {
	if !w.paths[path][s] {
		return
	}
	delete(w.paths[path], s)
	delete(w.subs[s], path)
	if len(w.subs[s]) == 0 {
		delete(w.subs, s)
	}
	if len(w.paths[path]) == 0 {
		delete(w.paths, path)
		if w.inst != nil {
			_ = w.inst.Remove(path) // a path gone took its watch with it
		}
	}
}

// read hands what the instance says to the subscribers until it is closed.
func (w *Watcher) read(inst *fsnotify.Watcher, reading chan struct{}) {
	defer close(reading)
	log := logging.FromContext(w.ctx)
	for {
		select {
		case e, ok := <-inst.Events:
			if !ok {
				return
			}
			w.changed(e)
		case err, ok := <-inst.Errors:
			if !ok {
				return
			}
			if !errors.Is(err, fsnotify.ErrEventOverflow) {
				log.Error("file watch failed", "err", err)
				continue
			}
			log.Info("file events lost", "err", err)
			w.mu.Lock()
			lost := make([]Subscriber, 0, len(w.subs))
			for s := range w.subs {
				lost = append(lost, s)
			}
			w.mu.Unlock()
			tellLost(lost)
		}
	}
}

// changed hands e to the subscribers of its path and of its directory, each
// once. A watched path removed or renamed took the kernel's watch with it: it is
// forgotten, so that adding it again, once it is back, watches it again.
func (w *Watcher) changed(e fsnotify.Event) {
	w.mu.Lock()
	var subs []Subscriber
	seen := map[Subscriber]bool{}
	for _, path := range []string{e.Name, filepath.Dir(e.Name)} {
		for s := range w.paths[path] {
			if !seen[s] {
				seen[s] = true
				subs = append(subs, s)
			}
		}
	}
	if e.Has(fsnotify.Remove) || e.Has(fsnotify.Rename) {
		for s := range w.paths[e.Name] {
			delete(w.subs[s], e.Name)
			if len(w.subs[s]) == 0 {
				delete(w.subs, s)
			}
		}
		delete(w.paths, e.Name)
	}
	w.mu.Unlock()
	for _, s := range subs {
		s.Changed(e)
	}
}

func tellLost(subs []Subscriber) {
	for _, s := range subs {
		s.Lost()
	}
}
