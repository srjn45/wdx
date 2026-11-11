package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/atomicfile"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/sqlite"
)

// DefaultTrashRetention is how long what is kept of a removed agent stays.
const DefaultTrashRetention = 7 * 24 * time.Hour

const (
	backupAgentFile   = "agent.json"
	backupRemovedFile = "removed.json"
)

// backupRecord says whose a backup is, since the agent's row goes before it
// does. Its keys are what a later feature reads from the trash.
type backupRecord struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	ProjectID string    `json:"project_id"`
	Project   string    `json:"project_name"`
	Dir       string    `json:"dir"`
	ACA       string    `json:"aca"`
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
	RemovedAt time.Time `json:"removed_at,omitzero"`
}

// Backups keeps a backup of each agent's conversation until the agent is removed,
// and for a while after. docs/AGENT_RESUME.md.
type Backups struct {
	ctx       context.Context
	store     *Store
	home      string
	acas      *ACAs
	retention time.Duration
	// OnChange, when set, is told an agent's session or directory changed.
	OnChange func()
	// OnSessionChange, when set, is told which agent's session a backup found changed.
	OnSessionChange func(agentID string)

	// work is held while a backup or the trash changes, which can take a while.
	work    sync.Mutex
	timer   *time.Timer
	stopped bool

	queue   sync.Mutex
	queued  map[string]bool
	closed  bool
	running sync.WaitGroup
}

// NewBackups returns the backups of the agents in store, under home, of the
// AI coding agents acas knows; a removed agent's stays for retention, or
// DefaultTrashRetention when 0.
func NewBackups(ctx context.Context, store *Store, home string, acas *ACAs, retention time.Duration) *Backups {
	if retention == 0 {
		retention = DefaultTrashRetention
	}
	return &Backups{
		ctx: context.WithoutCancel(ctx), store: store, home: home, acas: acas, retention: retention,
		queued: map[string]bool{},
	}
}

// Close waits for the work under way and starts no more.
func (b *Backups) Close() {
	b.queue.Lock()
	b.closed = true
	b.queue.Unlock()
	b.work.Lock()
	b.stopped = true
	if b.timer != nil {
		b.timer.Stop()
	}
	b.work.Unlock()
	b.running.Wait()
}

// Request brings the backup of the agent on terminal row terminalID up to date,
// in the background: what tells it must not wait for a copy. Requests made
// while one waits to start are the same request.
func (b *Backups) Request(terminalID string) {
	b.queue.Lock()
	defer b.queue.Unlock()
	if b.closed || b.queued[terminalID] {
		return
	}
	b.queued[terminalID] = true
	b.running.Go(func() {
		b.work.Lock()
		defer b.work.Unlock()
		b.queue.Lock()
		delete(b.queued, terminalID)
		b.queue.Unlock()
		log := logging.FromContext(b.ctx).With("terminal_id", terminalID)
		a, err := b.store.GetByTerminal(b.ctx, terminalID)
		if errors.Is(err, ErrNotFound) {
			return // removed meanwhile; Sweep took its last bytes
		}
		if err != nil {
			log.Error("agent backup not started", "err", err)
			return
		}
		if _, err := b.up(b.ctx, a); err != nil {
			notUpToDate(logging.NewContext(b.ctx, log), a.ID, err)
		}
	})
}

// RequestAll is Request for every agent, for what changed while no daemon ran.
func (b *Backups) RequestAll(ctx context.Context) error {
	list, err := b.store.List(ctx, "")
	if err != nil {
		return err
	}
	for _, a := range list {
		b.Request(a.Terminal.ID)
	}
	return nil
}

// up brings a's backup up to date and returns the session it is in now, as far
// as is known, with an error. A b.work holder only.
func (b *Backups) up(ctx context.Context, a Agent) (string, error) {
	known := b.known(a.ACA)
	if known == nil {
		return a.SessionID, nil // Launches reports it, on a start
	}
	account, err := holder.LookupAccount(ctx)
	if err != nil {
		return a.SessionID, err
	}
	dir := identity.AgentDir(b.home, a.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return a.SessionID, err
	}
	backup, err := known.BackUp(ctx, aca.Launch{Dir: a.Terminal.Dir, SessionID: a.SessionID, UserHome: account.Home, BackupDir: dir})
	session := backup.Session
	if backup.Dir != "" && backup.Dir != a.CurrentDir {
		changed, serr := b.store.setCurrentDir(ctx, a.ID, backup.Dir)
		if serr != nil {
			return a.SessionID, errors.Join(err, serr)
		}
		logging.FromContext(ctx).Info("agent moved", "agent_id", a.ID, "from", a.CurrentDir, "dir", backup.Dir)
		if changed && b.OnChange != nil {
			b.OnChange()
		}
		a.CurrentDir = backup.Dir
	}
	if session != "" && session != a.SessionID {
		changed, serr := b.store.setSession(ctx, a.ID, session)
		if serr != nil {
			return a.SessionID, errors.Join(err, serr)
		}
		logging.FromContext(ctx).Info("agent session changed", "agent_id", a.ID, "from", a.SessionID, "session_id", session)
		if changed && b.OnChange != nil {
			b.OnChange()
		}
		if changed && b.OnSessionChange != nil {
			b.OnSessionChange(a.ID)
		}
		a.SessionID = session
	}
	data, merr := json.Marshal(backupRecord{
		ID: a.ID, Name: a.Terminal.Name, ProjectID: a.Terminal.ProjectID, Project: a.Terminal.ProjectName,
		Dir: a.Terminal.Dir, ACA: a.ACA, SessionID: a.SessionID, CreatedAt: a.Terminal.CreatedAt,
	})
	if merr == nil {
		merr = atomicfile.Write(filepath.Join(dir, backupAgentFile), data, 0o600)
	}
	if errors.Is(err, aca.ErrNotSupported) && merr == nil {
		return a.SessionID, aca.ErrNotSupported
	}
	return a.SessionID, errors.Join(err, merr)
}

// notUpToDate logs a backup of the agent id that up could not bring up to date: at
// debug when its AI coding agent keeps none yet, which says nothing new at every start
// and every idle.
func notUpToDate(ctx context.Context, id string, err error) {
	if errors.Is(err, aca.ErrNotSupported) {
		logging.FromContext(ctx).Debug("agent backup not kept", "agent_id", id, "reason", "its AI coding agent keeps none yet")
		return
	}
	logging.FromContext(ctx).Error("agent backup not up to date", "agent_id", id, "err", err)
}

func (b *Backups) known(id string) aca.AiCodingAgent {
	return b.acas.Known(id)
}

// Sweep moves the backups of agents that have no row to the trash, taking their
// last bytes first, and deletes what expired there. However a row went, an
// agent's removal or its project's, or a crash between, it ends here.
func (b *Backups) Sweep(ctx context.Context) error {
	b.work.Lock()
	defer b.work.Unlock()
	log := logging.FromContext(ctx)
	// Listed before the agents: a directory is made after its row.
	entries, err := os.ReadDir(identity.AgentsDir(b.home))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	list, err := b.store.List(ctx, "")
	if err != nil {
		return err
	}
	exist := map[string]bool{}
	for _, a := range list {
		exist[a.ID] = true
	}
	for _, e := range entries {
		if exist[e.Name()] {
			continue
		}
		if err := b.toTrash(ctx, e.Name()); err != nil {
			log.Error("agent backup not moved to the trash", "agent_id", e.Name(), "err", err)
		}
	}
	return b.expire(ctx)
}

// toTrash moves the backup of the agent id, whose row is gone. A b.work holder
// only.
func (b *Backups) toTrash(ctx context.Context, id string) error {
	log := logging.FromContext(ctx)
	dir := identity.AgentDir(b.home, id)
	record := backupRecord{ID: id}
	if data, err := os.ReadFile(filepath.Join(dir, backupAgentFile)); err == nil {
		if err := json.Unmarshal(data, &record); err != nil {
			log.Error("agent record not read", "agent_id", id, "err", err)
		}
	}
	if known := b.known(record.ACA); known != nil {
		account, err := holder.LookupAccount(ctx)
		if err == nil {
			_, err = known.BackUp(ctx, aca.Launch{Dir: record.Dir, SessionID: record.SessionID, UserHome: account.Home, BackupDir: dir})
		}
		switch {
		case errors.Is(err, aca.ErrNotSupported):
			log.Debug("last of the agent's backup not taken", "agent_id", id, "reason", "its AI coding agent keeps none yet")
		case err != nil:
			log.Error("last of the agent's backup not taken", "agent_id", id, "err", err)
		}
	}
	record.RemovedAt = time.Now().UTC()
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(filepath.Join(dir, backupRemovedFile), data, 0o600); err != nil {
		return err
	}
	trash := identity.TrashAgentsDir(b.home)
	if err := os.MkdirAll(trash, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(trash), 0o700); err != nil { //nolint:gosec // a directory, private to the user
		return err
	}
	to := filepath.Join(trash, id)
	if err := os.RemoveAll(to); err != nil {
		return err
	}
	if err := os.Rename(dir, to); err != nil {
		return err
	}
	log.Info("agent backup moved to the trash", "agent_id", id, "path", to)
	return nil
}

// expire deletes what is past its time in the trash, and arms one timer for the
// next to go. A b.work holder only.
func (b *Backups) expire(ctx context.Context) error {
	log := logging.FromContext(ctx)
	trash := identity.TrashAgentsDir(b.home)
	entries, err := os.ReadDir(trash)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var next time.Time
	now := time.Now()
	for _, e := range entries {
		due := b.trashedAt(filepath.Join(trash, e.Name()), e).Add(b.retention)
		if due.After(now) {
			if next.IsZero() || due.Before(next) {
				next = due
			}
			continue
		}
		path := filepath.Join(trash, e.Name())
		if err := os.RemoveAll(path); err != nil {
			log.Error("agent backup not deleted from the trash", "path", path, "err", err)
			continue
		}
		log.Info("agent backup deleted from the trash", "agent_id", e.Name(), "path", path)
	}
	if b.timer != nil {
		b.timer.Stop()
	}
	if next.IsZero() {
		return nil
	}
	b.timer = time.AfterFunc(time.Until(next), func() {
		b.work.Lock()
		defer b.work.Unlock()
		if b.stopped {
			return
		}
		if err := b.expire(ctx); err != nil {
			log.Error("agent trash not swept", "err", err)
		}
	})
	return nil
}

// trashedAt is when the backup at path was removed: what it says, or else its
// directory's time, for one a crash left unfinished.
func (b *Backups) trashedAt(path string, e os.DirEntry) time.Time {
	var record backupRecord
	if data, err := os.ReadFile(filepath.Join(path, backupRemovedFile)); err == nil && json.Unmarshal(data, &record) == nil && !record.RemovedAt.IsZero() {
		return record.RemovedAt
	}
	if info, err := e.Info(); err == nil {
		return info.ModTime()
	}
	return time.Now()
}

// Transcript is lines of an agent's conversation as text.
type Transcript struct {
	Path  string // the file, for whoever would grep it
	From  int64  // the number of the first of Lines
	Next  int64  // the number the next line written will take
	Lines []string
}

// transcript brings a's backup up to date, then reads count lines from line from
// on, or its last count lines when from is nil.
func (b *Backups) transcript(ctx context.Context, a Agent, from *int64, count int) (Transcript, error) {
	b.work.Lock()
	defer b.work.Unlock()
	if _, err := b.up(ctx, a); errors.Is(err, aca.ErrNotSupported) {
		// An empty transcript would say the agent said nothing.
		return Transcript{}, fmt.Errorf("the transcript of agent %s: %w", a.ID, err)
	} else if err != nil {
		notUpToDate(ctx, a.ID, err)
	}
	path := filepath.Join(identity.AgentDir(b.home, a.ID), aca.TranscriptFile)
	t := Transcript{Path: path, Lines: []string{}}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return Transcript{}, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadString('\n')
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Transcript{}, err
		}
		n := t.Next
		t.Next++
		line = line[:len(line)-1]
		switch {
		case from == nil:
			t.Lines = append(t.Lines, line)
			if len(t.Lines) > count {
				t.Lines = t.Lines[1:]
			}
		case n >= *from && len(t.Lines) < count:
			t.Lines = append(t.Lines, line)
		}
	}
	if from == nil {
		t.From = t.Next - int64(len(t.Lines))
	} else {
		t.From = min(*from, t.Next)
	}
	return t, nil
}

// beginSession moves the agent to a new session, its process gone: its backup is brought
// up to date, then linked and the row change in a transaction, and last the AI coding
// agent is told, as it says which session a later backup finds. All under b.work, or a
// backup between would put the row back. A crash before it is told leaves the agent in
// its old session with a message that says otherwise; the other way round, it would be
// in a new session with nothing to tell it so.
func (b *Backups) beginSession(ctx context.Context, id, session string, linked func(ctx context.Context, tx sqlite.Querier, a Agent) error) ([]string, error) {
	b.work.Lock()
	defer b.work.Unlock()
	a, err := b.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	previous, err := b.up(ctx, a)
	if err != nil {
		notUpToDate(ctx, id, err)
	}
	if a, err = b.store.Get(ctx, id); err != nil {
		return nil, err
	}
	// Before the backup that is the old session's last, for what it wrote until it stopped.
	stopped := b.stopDetached(ctx, a)
	if len(stopped) > 0 {
		if previous, err = b.up(ctx, a); err != nil {
			notUpToDate(ctx, id, err)
		}
		if a, err = b.store.Get(ctx, id); err != nil {
			return nil, err
		}
	}
	known := b.known(a.ACA)
	if known == nil {
		return nil, notKnown(a.ACA)
	}
	account, err := holder.LookupAccount(ctx)
	if err != nil {
		return nil, err
	}
	if err := b.store.beginSession(ctx, id, session, func(ctx context.Context, tx sqlite.Querier) error { return linked(ctx, tx, a) }); err != nil {
		return nil, err
	}
	dir := identity.AgentDir(b.home, id)
	if err := known.BeginSession(ctx, aca.Launch{Dir: a.Terminal.Dir, SessionID: previous, UserHome: account.Home, BackupDir: dir}, session); err != nil {
		return nil, err
	}
	logging.FromContext(ctx).Info("agent session begun", "agent_id", id, "from", previous, "session_id", session)
	if b.OnChange != nil {
		b.OnChange()
	}
	return stopped, nil
}

// detachedOf asks a's AI coding agent what of a runs outside its terminal, of what the
// backup knows. Not knowing is no reason to refuse or fail anything: there is then nothing
// that can be stopped either.
func (b *Backups) detachedOf(ctx context.Context, a Agent) (aca.AiCodingAgent, aca.Launch, []aca.DetachedSession) {
	known := b.known(a.ACA)
	if known == nil {
		return nil, aca.Launch{}, nil
	}
	account, err := holder.LookupAccount(ctx)
	if err != nil {
		logging.FromContext(ctx).Error("agent background sessions not looked for", "agent_id", a.ID, "err", err)
		return nil, aca.Launch{}, nil
	}
	l := aca.Launch{Dir: a.Terminal.Dir, SessionID: a.SessionID, UserHome: account.Home, BackupDir: identity.AgentDir(b.home, a.ID), Shell: account.LoginShell()}
	detached, err := known.Detached(ctx, l)
	if err != nil {
		logging.FromContext(ctx).Error("agent background sessions not looked for", "agent_id", a.ID, "err", err)
		return nil, aca.Launch{}, nil
	}
	return known, l, detached
}

// detached brings a's backup up to date, which is what finds a session gone elsewhere,
// and returns what of a runs outside its terminal.
func (b *Backups) detached(ctx context.Context, a Agent) []aca.DetachedSession {
	b.work.Lock()
	defer b.work.Unlock()
	if _, err := b.up(ctx, a); err != nil {
		notUpToDate(ctx, a.ID, err)
	}
	_, _, detached := b.detachedOf(ctx, a)
	return detached
}

// stop brings a's backup up to date, stops what of a runs outside its terminal, and backs
// up what that wrote until it stopped.
func (b *Backups) stop(ctx context.Context, a Agent) []string {
	b.work.Lock()
	defer b.work.Unlock()
	if _, err := b.up(ctx, a); err != nil {
		notUpToDate(ctx, a.ID, err)
	}
	stopped := b.stopDetached(ctx, a)
	if len(stopped) > 0 {
		if _, err := b.up(ctx, a); err != nil {
			notUpToDate(ctx, a.ID, err)
		}
	}
	return stopped
}

// stopDetached stops what of a runs outside its terminal and returns the sessions it
// stopped. One that will not stop is logged and left, which should not cost the restart.
func (b *Backups) stopDetached(ctx context.Context, a Agent) []string {
	known, l, detached := b.detachedOf(ctx, a)
	var stopped []string
	for _, d := range detached {
		if err := known.StopDetached(ctx, l, d); err != nil {
			logging.FromContext(ctx).Error("agent background session not stopped", "agent_id", a.ID, "session_id", d.Session, "err", err)
			continue
		}
		logging.FromContext(ctx).Info("agent background session stopped", "agent_id", a.ID, "session_id", d.Session)
		stopped = append(stopped, d.Session)
	}
	return stopped
}

// briefLimit is how much of an agent's conversation its brief holds. docs/AGENT_RESUME.md.
const briefLimit = 64 << 10

// brief brings a's backup up to date, then reads its conversation in brief, with a line
// on how to read what it leaves out.
func (b *Backups) brief(ctx context.Context, a Agent) (Transcript, error) {
	b.work.Lock()
	defer b.work.Unlock()
	if _, err := b.up(ctx, a); err != nil {
		notUpToDate(ctx, a.ID, err)
	}
	dir := identity.AgentDir(b.home, a.ID)
	t := Transcript{Path: filepath.Join(dir, aca.TranscriptFile), Lines: []string{}}
	known := b.known(a.ACA)
	if known == nil {
		return t, notKnown(a.ACA)
	}
	account, err := holder.LookupAccount(ctx)
	if err != nil {
		return Transcript{}, err
	}
	brief, err := known.Brief(ctx, aca.Launch{Dir: a.Terminal.Dir, SessionID: a.SessionID, UserHome: account.Home, BackupDir: dir}, briefLimit)
	if err != nil {
		return Transcript{}, fmt.Errorf("the brief of agent %s: %w", a.ID, err)
	}
	t.From, t.Next = brief.From, brief.Next
	if len(brief.Start) == 0 {
		return t, nil
	}
	read := fmt.Sprintf("%s agent transcript --id %s --from <line> --count <lines>", identity.Name, a.ID)
	t.Lines = append(t.Lines, "This is the conversation so far in brief, from line "+strconv.FormatInt(brief.From, 10)+" of "+t.Path+": each tool call is one line, with the line it is at. Read any of it in full with: "+read, "")
	t.Lines = append(t.Lines, brief.Start...)
	if brief.OmittedCount > 0 {
		t.Lines = append(t.Lines, fmt.Sprintf("[Too long for a brief: read the %d lines left out here with: %s agent transcript --id %s --from %d --count %d]", brief.OmittedCount, identity.Name, a.ID, brief.OmittedFrom, min(brief.OmittedCount, 10_000)), "")
	}
	t.Lines = append(t.Lines, brief.Rest...)
	return t, nil
}
