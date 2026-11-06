package agent

import (
	"context"
	"errors"
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
// agent tells through ContextSize, read again each time its backup changes.
// docs/AGENT.md.
type ContextSizes struct {
	ctx      context.Context
	store    *Store
	home     string
	acas     *ACAs
	interval time.Duration
	// OnChange, when set, is told an agent's context size changed.
	OnChange func()
}

// NewContextSizes returns the context sizes of the agents in store, whose backups
// are under home, of the AI coding agents acas knows, read at most once an interval,
// or DefaultContextInterval when 0.
func NewContextSizes(ctx context.Context, store *Store, home string, acas *ACAs, interval time.Duration) *ContextSizes {
	if interval == 0 {
		interval = DefaultContextInterval
	}
	return &ContextSizes{ctx: context.WithoutCancel(ctx), store: store, home: home, acas: acas, interval: interval}
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
	if _, err := known.ContextSize(l); errors.Is(err, aca.ErrNotSupported) {
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
	c.read(ctx, known, a.ID, l)
	last := time.Now()
	// next fires the read a change asked for, once the interval since the last is up;
	// nil while none waits.
	var next <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-watcher.Events:
			if !ok {
				return
			}
			if next == nil {
				next = time.After(max(0, c.interval-time.Since(last)))
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			// An overflow lost changes: read again, as for one.
			log.Error("agent backup watch failed", "agent_id", a.ID, "err", err)
			if next == nil {
				next = time.After(max(0, c.interval-time.Since(last)))
			}
		case <-next:
			next = nil
			c.read(ctx, known, a.ID, l)
			last = time.Now()
		}
	}
}

// read records what known says of the context size of the agent with id.
func (c *ContextSizes) read(ctx context.Context, known aca.AiCodingAgent, id string, l aca.Launch) {
	log := logging.FromContext(ctx)
	size, err := known.ContextSize(l)
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
