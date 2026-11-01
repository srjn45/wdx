package agent

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/terminal"
)

// RunningError is returned when an agent is starting or running and the
// request needs it not to be.
type RunningError struct {
	Agent Agent
}

func (e *RunningError) Error() string {
	return fmt.Sprintf("agent %s is %s", e.Agent.Terminal.Name, e.Agent.Terminal.State)
}

// RemovedError is returned when an agent is a tombstone and the request needs
// a live one.
type RemovedError struct {
	Agent Agent
}

func (e *RemovedError) Error() string {
	return fmt.Sprintf("agent %s was removed and stays only for its children", e.Agent.Terminal.Name)
}

// StartError is returned when an agent's holder or AI coding agent did not
// start; the agent is then failed.
type StartError struct {
	Agent Agent
	Err   error
}

func (e *StartError) Error() string {
	return fmt.Sprintf("agent %s did not start: %v", e.Agent.Terminal.Name, e.Err)
}

func (e *StartError) Unwrap() error { return e.Err }

// NotRunningError is returned when a request needs an agent's AI coding agent
// running.
type NotRunningError struct {
	Agent Agent
}

func (e *NotRunningError) Error() string {
	return fmt.Sprintf("agent %s is %s, not running", e.Agent.Terminal.Name, e.Agent.Terminal.State)
}

// SendError is returned when a message did not reach an agent's holder.
type SendError struct {
	Agent Agent
	Err   error
}

func (e *SendError) Error() string {
	return fmt.Sprintf("message not sent to agent %s: %v", e.Agent.Terminal.Name, e.Err)
}

func (e *SendError) Unwrap() error { return e.Err }

// Launches returns what the terminals' supervisor asks at each launch of an
// agent's row: the agent's id, and the id and command of its AI coding agent,
// among those of b, for the session it was last in, which the backup knows. The
// supervisor needs it before a Manager can exist.
func Launches(b *Backups) func(context.Context, terminal.Terminal) (terminal.AgentLaunch, error) {
	return func(ctx context.Context, t terminal.Terminal) (terminal.AgentLaunch, error) {
		b.work.Lock()
		defer b.work.Unlock()
		a, err := b.store.GetByTerminal(ctx, t.ID)
		if err != nil {
			return terminal.AgentLaunch{}, err
		}
		if candidate := b.known(a.ACA); candidate != nil {
			session, err := b.up(ctx, a)
			if err != nil {
				// A start does not depend on its backup.
				logging.FromContext(ctx).Error("agent backup not up to date", "agent_id", a.ID, "err", err)
			}
			a.SessionID = session
			account, err := holder.LookupAccount(ctx)
			if err != nil {
				return terminal.AgentLaunch{}, err
			}
			command, err := candidate.Command(aca.Launch{Dir: t.Dir, SessionID: a.SessionID, UserHome: account.Home, BackupDir: identity.AgentDir(b.home, a.ID)})
			if err != nil {
				return terminal.AgentLaunch{}, err
			}
			logging.FromContext(ctx).Info("agent command chosen", "agent_id", a.ID, "aca", a.ACA, "session_id", a.SessionID, "command", command)
			return terminal.AgentLaunch{AgentID: a.ID, ACA: a.ACA, Command: command}, nil
		}
		return terminal.AgentLaunch{}, fmt.Errorf("this %s does not know the AI coding agent %q", identity.Name, a.ACA)
	}
}

// Manager creates, starts and removes agents, through the terminals'
// supervisor, which owns their processes.
type Manager struct {
	store     *Store
	terminals *terminal.Supervisor
	backups   *Backups
	aca       aca.AiCodingAgent // what new agents run
}

// NewManager returns a Manager of the agents in store; new agents run
// newAgents, and b keeps their backups.
func NewManager(store *Store, terminals *terminal.Supervisor, b *Backups, newAgents aca.AiCodingAgent) *Manager {
	return &Manager{store: store, terminals: terminals, backups: b, aca: newAgents}
}

// Store returns the Store the Manager keeps its agents in.
func (m *Manager) Store() *Store {
	return m.store
}

// Create adds an agent named name, or a generated codename when name is
// empty, to the project, under the agent with parentID unless it is "", and
// starts its AI coding agent in dir in a new session. A parent that is not in
// the project is not found, and a tombstone returns a *RemovedError. One that
// does not start leaves the agent failed and returns a *StartError.
func (m *Manager) Create(ctx context.Context, projectID, name, dir, parentID string) (Agent, error) {
	if parentID != "" {
		parent, err := m.store.Get(ctx, parentID)
		if err != nil {
			return Agent{}, err
		}
		if parent.Terminal.ProjectID != projectID {
			return Agent{}, fmt.Errorf("%w: %q is not in the project", ErrNotFound, parentID)
		}
		if parent.Tombstone() {
			return Agent{}, &RemovedError{Agent: parent}
		}
	}
	id, err := db.NewID("agent")
	if err != nil {
		return Agent{}, err
	}
	sessionID, err := newSessionID()
	if err != nil {
		return Agent{}, err
	}
	t, err := m.terminals.CreateAgent(ctx, projectID, name, dir, func(ctx context.Context, tx db.Execer, terminalID string) error {
		return m.store.insert(ctx, tx, id, terminalID, m.aca.ID(), sessionID, parentID)
	})
	if err != nil {
		return Agent{}, m.agentError(ctx, err)
	}
	return m.withTerminal(ctx, t)
}

// Start starts an agent whose AI coding agent ended, in the same session; a
// live agent fails with a *RunningError and a tombstone with a *RemovedError.
func (m *Manager) Start(ctx context.Context, id string) (Agent, error) {
	a, err := m.store.Get(ctx, id)
	if err != nil {
		return Agent{}, err
	}
	if a.Tombstone() {
		return Agent{}, &RemovedError{Agent: a}
	}
	t, err := m.terminals.StartAgent(ctx, a.Terminal.ID)
	if err != nil {
		return Agent{}, m.agentError(ctx, err)
	}
	return a.OnRow(t), nil
}

// Rename gives an agent a new name; a tombstone fails with a *RemovedError.
func (m *Manager) Rename(ctx context.Context, id, name string) (Agent, error) {
	a, err := m.store.Get(ctx, id)
	if err != nil {
		return Agent{}, err
	}
	if a.Tombstone() {
		return Agent{}, &RemovedError{Agent: a}
	}
	t, err := m.terminals.RenameAgent(ctx, a.Terminal.ID, name)
	if err != nil {
		return Agent{}, m.agentError(ctx, err)
	}
	return a.OnRow(t), nil
}

// Remove deletes an agent and returns it as it was last, stopped when it was
// live. A live one fails with a *RunningError unless stopRunning. What the AI
// coding agent keeps of the session stays on disk.
//
// One with children stays as a tombstone, and goes with its last child, as
// tombstones above it then may. A tombstone that has children returns a
// *RemovedError; one left without any, by a daemon that died between the two
// deletions, is deleted.
func (m *Manager) Remove(ctx context.Context, id string, stopRunning bool) (Agent, error) {
	a, err := m.store.Get(ctx, id)
	if err != nil {
		return Agent{}, err
	}
	children, err := m.store.children(ctx, id)
	if err != nil {
		return Agent{}, err
	}
	if children > 0 {
		if a.Tombstone() {
			return Agent{}, &RemovedError{Agent: a}
		}
		_, err := m.terminals.TombstoneAgent(ctx, a.Terminal.ID, stopRunning, func(ctx context.Context, tx db.Execer) error {
			return m.store.markRemoved(ctx, tx, id)
		})
		if err != nil {
			return Agent{}, m.agentError(ctx, err)
		}
		return m.store.Get(ctx, id)
	}
	t, err := m.terminals.RemoveAgent(ctx, a.Terminal.ID, stopRunning)
	if err != nil {
		return Agent{}, m.agentError(ctx, err)
	}
	a = a.OnRow(t)
	logging.FromContext(ctx).Info("agent removed", "agent_id", a.ID)
	if err := m.removeEmptyTombstones(ctx, a.ParentID); err != nil {
		logging.FromContext(ctx).Error("tombstones not removed", "agent_id", a.ID, "err", err)
	}
	if err := m.backups.Sweep(ctx); err != nil {
		logging.FromContext(ctx).Error("agent backups not swept", "err", err)
	}
	return a, nil
}

// removeEmptyTombstones deletes the agent with id if it is a tombstone with no
// children left, and goes on up the tree.
func (m *Manager) removeEmptyTombstones(ctx context.Context, id string) error {
	for id != "" {
		a, err := m.store.Get(ctx, id)
		if err != nil {
			return err
		}
		children, err := m.store.children(ctx, id)
		if err != nil {
			return err
		}
		if !a.Tombstone() || children > 0 {
			return nil
		}
		if _, err := m.terminals.RemoveAgent(ctx, a.Terminal.ID, false); err != nil {
			return err
		}
		logging.FromContext(ctx).Info("tombstone removed", "agent_id", id)
		id = a.ParentID
	}
	return nil
}

// Send queues a message for a running agent; instant says it may interrupt the
// agent's turn. A tombstone fails with a *RemovedError, one not running with a
// *NotRunningError and one whose holder does not take it with a *SendError.
func (m *Manager) Send(ctx context.Context, id, prompt string, instant bool) (Agent, string, error) {
	a, err := m.store.Get(ctx, id)
	if err != nil {
		return Agent{}, "", err
	}
	if a.Tombstone() {
		return Agent{}, "", &RemovedError{Agent: a}
	}
	t, messageID, err := m.terminals.SendAgent(ctx, a.Terminal.ID, prompt, instant)
	if err != nil {
		return Agent{}, "", m.agentError(ctx, err)
	}
	return a.OnRow(t), messageID, nil
}

// History reads count lines of the agent's history from line from on, or its
// last count lines when from is nil.
func (m *Manager) History(ctx context.Context, id string, from *int64, count int) (holder.History, error) {
	a, err := m.store.Get(ctx, id)
	if err != nil {
		return holder.History{}, err
	}
	return m.terminals.AgentHistory(ctx, a.Terminal.ID, from, count)
}

// SweepBackups moves the backups of agents whose rows went with their project to
// the trash.
func (m *Manager) SweepBackups(ctx context.Context) error {
	return m.backups.Sweep(ctx)
}

// Transcript reads count lines of the agent's conversation as text, from line
// from on, or its last count lines when from is nil, in any state.
func (m *Manager) Transcript(ctx context.Context, id string, from *int64, count int) (Transcript, error) {
	a, err := m.store.Get(ctx, id)
	if err != nil {
		return Transcript{}, err
	}
	return m.backups.transcript(ctx, a, from, count)
}

// withTerminal returns the agent on row t.
func (m *Manager) withTerminal(ctx context.Context, t terminal.Terminal) (Agent, error) {
	a, err := m.store.GetByTerminal(ctx, t.ID)
	if err != nil {
		return Agent{}, err
	}
	return a.OnRow(t), nil
}

// agentError says err, about an agent's terminal row, of the agent.
func (m *Manager) agentError(ctx context.Context, err error) error {
	if e, ok := errors.AsType[*terminal.RunningError](err); ok {
		a, gerr := m.withTerminal(ctx, e.Terminal)
		if gerr != nil {
			return gerr
		}
		return &RunningError{Agent: a}
	}
	if e, ok := errors.AsType[*terminal.StartError](err); ok {
		a, gerr := m.withTerminal(ctx, e.Terminal)
		if gerr != nil {
			return gerr
		}
		return &StartError{Agent: a, Err: e.Err}
	}
	if e, ok := errors.AsType[*terminal.NotRunningError](err); ok {
		a, gerr := m.withTerminal(ctx, e.Terminal)
		if gerr != nil {
			return gerr
		}
		return &NotRunningError{Agent: a}
	}
	if e, ok := errors.AsType[*terminal.SendError](err); ok {
		a, gerr := m.withTerminal(ctx, e.Terminal)
		if gerr != nil {
			return gerr
		}
		return &SendError{Agent: a, Err: e.Err}
	}
	return err
}

// newSessionID returns a random UUID, the form AI CLIs expect of a session id.
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // the RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
