package agent

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/fswatch"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

// DefaultStatusInterval is the least time between two reads of what an agent's ACA
// tells of its status: its context size and its account's usage.
const DefaultStatusInterval = time.Second

// StatusReads keeps what each running agent's AI coding agent tells of its status:
// how full its context is, through ContextSize, and how much of its account's limits
// is used, through Usage, both read again each time its backup, or a file either
// named, changes, and when its session changes. docs/AGENT.md, the wdx doc USAGE.
type StatusReads struct {
	ctx      context.Context
	store    *Store
	home     string
	acas     *ACAs
	watcher  *fswatch.Watcher
	interval time.Duration
	// OnChange, when set, is told an agent's context size or its ACA's usage changed.
	OnChange func()

	mu sync.Mutex
	// asked is, by agent id, how a watch is asked for a read.
	asked map[string]chan struct{}
}

// NewStatusReads returns the status reads of the agents in store, whose backups are
// under home, of the AI coding agents acas knows, watched through the daemon's
// watcher and read at most once an interval, or DefaultStatusInterval when 0.
func NewStatusReads(ctx context.Context, store *Store, home string, acas *ACAs, watcher *fswatch.Watcher, interval time.Duration) *StatusReads {
	if interval == 0 {
		interval = DefaultStatusInterval
	}
	return &StatusReads{ctx: context.WithoutCancel(ctx), store: store, home: home, acas: acas, watcher: watcher, interval: interval, asked: map[string]chan struct{}{}}
}

// Watch follows the status of the agent on terminal row terminalID, whose process
// runs, until stop is called, which waits for the last read. An agent whose AI coding
// agent tells neither its context size nor its usage is not followed.
func (c *StatusReads) Watch(terminalID string) (stop func()) {
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

// SessionChanged asks the watch of the agent with id, if one runs, to read again:
// the new session's files are then followed at once, not at the next change of the
// backup.
func (c *StatusReads) SessionChanged(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if asked, ok := c.asked[id]; ok {
		select {
		case asked <- struct{}{}:
		default: // a read is already asked for
		}
	}
}

func (c *StatusReads) watch(ctx context.Context, terminalID string) {
	log := logging.FromContext(ctx).With("terminal_id", terminalID)
	a, err := c.store.GetByTerminal(ctx, terminalID)
	if err != nil {
		log.Error("agent status not followed", "err", err)
		return
	}
	known := c.acas.Known(a.ACA)
	if known == nil {
		return // its launch reports it
	}
	account, err := holder.LookupAccount(ctx)
	if err != nil {
		log.Error("agent status not followed", "agent_id", a.ID, "err", err)
		return
	}
	l := aca.Launch{Dir: a.Terminal.Dir, SessionID: a.SessionID, UserHome: account.Home, BackupDir: identity.AgentDir(c.home, a.ID)}
	ctx = logging.NewContext(ctx, log.With("agent_id", a.ID))
	_, _, sizeErr := known.ContextSize(l)
	_, _, usageErr := known.Usage(l)
	if errors.Is(sizeErr, aca.ErrNotSupported) && errors.Is(usageErr, aca.ErrNotSupported) {
		return
	}
	// Watched before the first read, so that no change falls between. The backup is
	// there: its launch wrote it.
	w := &statusWatch{watcher: c.watcher, events: make(chan fsnotify.Event, 16), asked: make(chan struct{}, 1)}
	if err := c.watcher.Add(w, l.BackupDir); err != nil {
		log.Error("agent status not followed", "agent_id", a.ID, "path", l.BackupDir, "err", err)
		return
	}
	defer c.watcher.Drop(w)
	c.mu.Lock()
	c.asked[a.ID] = w.asked
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.asked, a.ID)
		c.mu.Unlock()
	}()
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
		case e := <-w.events:
			// A file gone is followed no more, whoever took it: the next read follows
			// it again if it is back.
			if e.Has(fsnotify.Remove) || e.Has(fsnotify.Rename) {
				w.followed = slices.DeleteFunc(w.followed, func(path string) bool { return path == e.Name })
			}
			ask()
		case <-w.asked:
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

// statusWatch is one agent's subscription to the daemon's watcher: its backup, and
// the files its AI coding agent's ContextSize and Usage named, which it follows
// besides.
type statusWatch struct {
	watcher  *fswatch.Watcher
	events   chan fsnotify.Event // a full one drops the event: a read is asked for already
	asked    chan struct{}       // a read asked for: events lost, or the session changed
	followed []string
}

// Changed is given each event on the backup or on a file followed, by the
// watcher's goroutine: it is kept for the agent's own.
func (w *statusWatch) Changed(e fsnotify.Event) {
	select {
	case w.events <- e:
	default:
	}
}

// Lost says events were lost: a read is asked for, as for one.
func (w *statusWatch) Lost() {
	select {
	case w.asked <- struct{}{}:
	default:
	}
}

// launch is l with what the agent's row says now, its session above all, which a
// backup changes when the AI coding agent names a new one; false once the agent is
// gone.
func (c *StatusReads) launch(ctx context.Context, terminalID string, l aca.Launch) (aca.Launch, bool) {
	a, err := c.store.GetByTerminal(ctx, terminalID)
	if errors.Is(err, ErrNotFound) {
		return l, false
	}
	if err != nil {
		logging.FromContext(ctx).Error("agent status read in its last session", "err", err)
		return l, true
	}
	l.SessionID = a.SessionID
	return l, true
}

// read records what known says of the context size of the agent with id and of its
// account's usage, and has w follow the files it read. A file followed only now is
// read again, for what was written to it between the read and the follow.
func (c *StatusReads) read(ctx context.Context, known aca.AiCodingAgent, id string, l aca.Launch, w *statusWatch) {
	size, followSize, sizeErr := known.ContextSize(l)
	rows, followUsage, usageErr := known.Usage(l)
	if c.follow(ctx, w, slices.Concat(followSize, followUsage)) {
		size, _, sizeErr = known.ContextSize(l)
		rows, _, usageErr = known.Usage(l)
	}
	c.recordSize(ctx, id, size, sizeErr)
	c.recordUsage(ctx, known.ID(), id, rows, usageErr)
}

func (c *StatusReads) recordSize(ctx context.Context, id string, size aca.ContextSize, err error) {
	log := logging.FromContext(ctx)
	switch {
	case errors.Is(err, aca.ErrNoContextSize), errors.Is(err, aca.ErrNotSupported):
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

// recordUsage records rows as the usage of the ACA acaID, told through the agent
// with id.
func (c *StatusReads) recordUsage(ctx context.Context, acaID, id string, rows []aca.Usage, err error) {
	log := logging.FromContext(ctx)
	switch {
	case errors.Is(err, aca.ErrNoUsage), errors.Is(err, aca.ErrNotSupported):
		return
	case err != nil:
		log.Error("usage not read", "err", err)
		return
	}
	log.Debug("usage read", "aca", acaID, "rows", len(rows))
	changed, err := c.store.setUsage(ctx, acaID, id, rows, time.Now())
	if err != nil {
		log.Error("usage not recorded", "aca", acaID, "err", err)
		return
	}
	if changed {
		log.Info("usage recorded", "aca", acaID, "rows", len(rows))
		if c.OnChange != nil {
			c.OnChange()
		}
	}
}

// follow has w follow the files in want, dropping those it followed that are not
// wanted now, and says whether it follows a new one. A file it cannot follow, as one
// gone meanwhile, is tried again at the next read.
func (c *StatusReads) follow(ctx context.Context, w *statusWatch, want []string) (added bool) {
	log := logging.FromContext(ctx)
	for _, path := range w.followed {
		if slices.Contains(want, path) {
			continue
		}
		w.watcher.Remove(w, path)
		log.Info("agent context file dropped", "path", path)
	}
	w.followed = slices.DeleteFunc(w.followed, func(path string) bool { return !slices.Contains(want, path) })
	for _, path := range want {
		if slices.Contains(w.followed, path) {
			continue
		}
		if err := w.watcher.Add(w, path); err != nil {
			log.Error("agent context file not followed", "path", path, "err", err)
			continue
		}
		log.Info("agent context file followed", "path", path)
		w.followed = append(w.followed, path)
		added = true
	}
	return added
}
