package agent

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

// DefaultContextInterval is the least time between two reads of an agent's context
// size.
const DefaultContextInterval = time.Second

// ContextSizes keeps how full each running agent's context is, as its AI coding
// agent tells through ContextSize, read again each time its backup, or a file
// ContextSize named, changes, and when its session changes. docs/AGENT.md.
type ContextSizes struct {
	ctx      context.Context
	store    *Store
	home     string
	acas     *ACAs
	interval time.Duration
	// OnChange, when set, is told an agent's context size changed.
	OnChange func()

	mu sync.Mutex
	// asked is, by agent id, how a watch is asked for a read.
	asked map[string]chan struct{}
}

// NewContextSizes returns the context sizes of the agents in store, whose backups
// are under home, of the AI coding agents acas knows, read at most once an interval,
// or DefaultContextInterval when 0.
func NewContextSizes(ctx context.Context, store *Store, home string, acas *ACAs, interval time.Duration) *ContextSizes {
	if interval == 0 {
		interval = DefaultContextInterval
	}
	return &ContextSizes{ctx: context.WithoutCancel(ctx), store: store, home: home, acas: acas, interval: interval, asked: map[string]chan struct{}{}}
}

// Watch follows the context size of the agent on terminal row terminalID, whose
// process runs, until stop is called, which waits for the last read. An agent whose
// AI coding agent does not tell it is not followed.
func (c *ContextSizes) Watch(terminalID string) (stop func()) {
	ctx, cancel := context.WithCancel(c.ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.watch(ctx, terminalID)
	}()
	return func() {
		cancel()
		<-done
	}
}

// SessionChanged asks the watch of the agent with id, if one runs, to read its size
// again: the new session's files are then followed at once, not at the next change
// of the backup.
func (c *ContextSizes) SessionChanged(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if asked, ok := c.asked[id]; ok {
		select {
		case asked <- struct{}{}:
		default: // a read is already asked for
		}
	}
}

func (c *ContextSizes) watch(ctx context.Context, terminalID string) {
	log := logging.FromContext(ctx).With("terminal_id", terminalID)
	a, err := c.store.GetByTerminal(ctx, terminalID)
	if err != nil {
		log.Error("agent context size not followed", "err", err)
		return
	}
	known := c.acas.Known(a.ACA)
	if known == nil {
		return // its launch reports it
	}
	account, err := holder.LookupAccount(ctx)
	if err != nil {
		log.Error("agent context size not followed", "agent_id", a.ID, "err", err)
		return
	}
	l := aca.Launch{Dir: a.Terminal.Dir, SessionID: a.SessionID, UserHome: account.Home, BackupDir: identity.AgentDir(c.home, a.ID)}
	ctx = logging.NewContext(ctx, log.With("agent_id", a.ID))
	if _, _, err := known.ContextSize(l); errors.Is(err, aca.ErrNotSupported) {
		return
	}
	// Watched before the first read, so that no change falls between. The backup is
	// there: its launch wrote it.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Error("agent context size not followed", "agent_id", a.ID, "err", err)
		return
	}
	defer watcher.Close()
	if err := watcher.Add(l.BackupDir); err != nil {
		log.Error("agent context size not followed", "agent_id", a.ID, "path", l.BackupDir, "err", err)
		return
	}
	asked := make(chan struct{}, 1)
	c.mu.Lock()
	c.asked[a.ID] = asked
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.asked, a.ID)
		c.mu.Unlock()
	}()
	w := &contextWatch{watcher: watcher}
	read := func() bool {
		var ok bool
		if l, ok = c.launch(ctx, terminalID, l); !ok {
			return false
		}
		c.read(ctx, known, a.ID, l, w)
		return true
	}
	if !read() {
		return
	}
	last := time.Now()
	// next fires the read a change asked for, once the interval since the last is up;
	// nil while none waits.
	var next <-chan time.Time
	ask := func() {
		if next == nil {
			next = time.After(max(0, c.interval-time.Since(last)))
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-watcher.Events:
			if !ok {
				return
			}
			// A file gone is followed no more, whoever took it: the next read follows
			// it again if it is back.
			if e.Has(fsnotify.Remove) || e.Has(fsnotify.Rename) {
				w.followed = slices.DeleteFunc(w.followed, func(path string) bool { return path == e.Name })
			}
			ask()
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			// An overflow lost changes: read again, as for one.
			log.Error("agent backup watch failed", "agent_id", a.ID, "err", err)
			ask()
		case <-asked:
			ask()
		case <-next:
			next = nil
			if !read() {
				return
			}
			last = time.Now()
		}
	}
}

// contextWatch is what one agent's watch follows besides its backup: the files its
// AI coding agent's ContextSize named.
type contextWatch struct {
	watcher  *fsnotify.Watcher
	followed []string
}

// launch is l with what the agent's row says now, its session above all, which a
// backup changes when the AI coding agent names a new one; false once the agent is
// gone.
func (c *ContextSizes) launch(ctx context.Context, terminalID string, l aca.Launch) (aca.Launch, bool) {
	a, err := c.store.GetByTerminal(ctx, terminalID)
	if errors.Is(err, ErrNotFound) {
		return l, false
	}
	if err != nil {
		logging.FromContext(ctx).Error("agent context size read in its last session", "err", err)
		return l, true
	}
	l.SessionID = a.SessionID
	return l, true
}

// read records what known says of the context size of the agent with id, and has w
// follow the files it read. A file followed only now is read again, for what was
// written to it between the read and the follow.
func (c *ContextSizes) read(ctx context.Context, known aca.AiCodingAgent, id string, l aca.Launch, w *contextWatch) {
	log := logging.FromContext(ctx)
	size, follow, err := known.ContextSize(l)
	if c.follow(ctx, w, follow) {
		size, _, err = known.ContextSize(l)
	}
	switch {
	case errors.Is(err, aca.ErrNoContextSize):
		return
	case err != nil:
		log.Error("agent context size not read", "err", err)
		return
	}
	log.Debug("context size read", "tokens", size.Tokens, "window", size.Window)
	changed, err := c.store.setContext(ctx, id, size)
	if err != nil {
		log.Error("agent context size not recorded", "err", err)
		return
	}
	if changed && c.OnChange != nil {
		c.OnChange()
	}
}

// follow has w follow the files in want, dropping those it followed that are not
// wanted now, and says whether it follows a new one. A file it cannot follow, as one
// gone meanwhile, is tried again at the next read.
func (c *ContextSizes) follow(ctx context.Context, w *contextWatch, want []string) (added bool) {
	log := logging.FromContext(ctx)
	for _, path := range w.followed {
		if slices.Contains(want, path) {
			continue
		}
		if err := w.watcher.Remove(path); err != nil && !errors.Is(err, fsnotify.ErrNonExistentWatch) {
			log.Error("agent context file not dropped", "path", path, "err", err)
			continue
		}
		log.Info("agent context file dropped", "path", path)
	}
	w.followed = slices.DeleteFunc(w.followed, func(path string) bool { return !slices.Contains(want, path) })
	for _, path := range want {
		if slices.Contains(w.followed, path) {
			continue
		}
		if err := w.watcher.Add(path); err != nil {
			log.Error("agent context file not followed", "path", path, "err", err)
			continue
		}
		log.Info("agent context file followed", "path", path)
		w.followed = append(w.followed, path)
		added = true
	}
	return added
}
