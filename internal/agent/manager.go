package agent

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/screen"
	"github.com/srjn45/wdx/internal/sqlite"
	"github.com/srjn45/wdx/internal/terminal"
)

// RunningError is returned when an agent is starting or running and the
// request needs it not to be.
type RunningError struct {
	Agent Agent
	// Detached is the session working outside the agent's terminal, if that is why.
	Detached string
}

func (e *RunningError) Error() string {
	if e.Detached != "" {
		return fmt.Sprintf("agent %s has a background session at work: %s", e.Agent.Terminal.Name, e.Detached)
	}
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

// BusyError is returned when a restart would stop an agent in the middle of
// something, and was not told to.
type BusyError struct {
	Agent Agent
	// Detached is the session working outside the agent's terminal, if that is why.
	Detached string
}

func (e *BusyError) Error() string {
	if e.Detached != "" {
		return fmt.Sprintf("agent %s has a background session at work: %s", e.Agent.Terminal.Name, e.Detached)
	}
	doing := e.Agent.Activity
	if e.Agent.Draft {
		doing = "holding a draft in its input box"
	}
	return fmt.Sprintf("agent %s is %s", e.Agent.Terminal.Name, cmp.Or(doing, string(e.Agent.Terminal.State)))
}

// busy reports whether a restart would stop a in the middle of something.
func (a Agent) busy() bool {
	return a.Terminal.State.Live() && (a.Activity != string(screen.ActivityIdle) || a.Draft)
}

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

// PromptChangedError is an answer to a prompt the agent's screen did not show,
// or that changed while its holder answered it, with no key of the holder's.
// Question is the index of the answer it stopped at; Entered says Enter was
// pressed for an answer before it.
type PromptChangedError struct {
	Agent    Agent
	Question int
	Entered  bool
}

func (e *PromptChangedError) Error() string {
	return fmt.Sprintf("the prompt of agent %s is not the one answered, or changed while it was answered", e.Agent.Terminal.Name)
}

// InvalidAnswerError is an answer the agent's prompt cannot take; Reason says
// why, in no words of the prompt's.
type InvalidAnswerError struct {
	Agent  Agent
	Reason string
}

func (e *InvalidAnswerError) Error() string {
	return fmt.Sprintf("the prompt of agent %s cannot take the answer: %s", e.Agent.Terminal.Name, e.Reason)
}

// AnswerError is an answer the agent's holder could not give, or did not see
// taken: Result and Reason say which, and Entered whether Enter was pressed,
// so that the prompt may have taken it.
type AnswerError struct {
	Agent   Agent
	Result  holder.AnswerResult
	Reason  string
	Entered bool
	Err     error
}

func (e *AnswerError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("the prompt of agent %s was not answered: %v", e.Agent.Terminal.Name, e.Err)
	}
	return fmt.Sprintf("the prompt of agent %s was not answered: %s, %s", e.Agent.Terminal.Name, e.Result, e.Reason)
}

func (e *AnswerError) Unwrap() error { return e.Err }

// Launches returns what the terminals' supervisor asks at each launch of an
// agent's row: the agent's id, and the id and command of its AI coding agent,
// among those of b, for the session it was last in, which the backup knows. The
// supervisor needs it before a Manager can exist.
func Launches(b *Backups) func(context.Context, terminal.Terminal) (terminal.AgentLaunch, error) {
	return func(ctx context.Context, t terminal.Terminal) (terminal.AgentLaunch, error) {
		// Before the backups' lock, as looking can take a while.
		if a, err := b.store.GetByTerminal(ctx, t.ID); err != nil {
			return terminal.AgentLaunch{}, err
		} else if known := b.known(a.ACA); known != nil {
			if err := b.acas.Installed(ctx, known); err != nil {
				return terminal.AgentLaunch{}, err
			}
		}
		b.work.Lock()
		defer b.work.Unlock()
		a, err := b.store.GetByTerminal(ctx, t.ID)
		if err != nil {
			return terminal.AgentLaunch{}, err
		}
		if err := b.store.clearModel(ctx, t.ID); err != nil {
			return terminal.AgentLaunch{}, err
		}
		if candidate := b.known(a.ACA); candidate != nil {
			session, err := b.up(ctx, a)
			if err != nil {
				// A start does not depend on its backup.
				notUpToDate(ctx, a.ID, err)
			}
			a.SessionID = session
			account, err := holder.LookupAccount(ctx)
			if err != nil {
				return terminal.AgentLaunch{}, err
			}
			// Again, as the backup may just have said it moved.
			if a, err = b.store.GetByTerminal(ctx, t.ID); err != nil {
				return terminal.AgentLaunch{}, err
			}
			a.SessionID = session
			dir, _ := runDir(a)
			note := LaunchNote()
			command, err := candidate.Command(ctx, aca.Launch{Dir: dir, SessionID: a.SessionID, UserHome: account.Home, BackupDir: identity.AgentDir(b.home, a.ID), Note: note,
				Model: a.RequestedModel, Permissions: a.Permissions})
			if err != nil {
				return terminal.AgentLaunch{}, err
			}
			logging.FromContext(ctx).Info("agent command chosen", "agent_id", a.ID, "aca", a.ACA, "session_id", a.SessionID, "dir", dir, "command", withoutNote(command, note))
			return terminal.AgentLaunch{AgentID: a.ID, ACA: a.ACA, Command: command, Dir: dir}, nil
		}
		return terminal.AgentLaunch{}, notKnown(a.ACA)
	}
}

// runDir is where a relaunch of a runs: where it last worked if that is still there,
// else its own dir, as a conversation that thinks it is in a worktree must not wake up
// in another checkout. gone is a place that was not there.
func runDir(a Agent) (dir, gone string) {
	if a.CurrentDir == "" || a.CurrentDir == a.Terminal.Dir {
		return a.Terminal.Dir, ""
	}
	if info, err := os.Stat(a.CurrentDir); err != nil || !info.IsDir() {
		return a.Terminal.Dir, a.CurrentDir
	}
	return a.CurrentDir, ""
}

// Manager creates, starts and removes agents, through the terminals'
// supervisor, which owns their processes.
type Manager struct {
	store     *Store
	terminals *terminal.Supervisor
	backups   *Backups
	acas      *ACAs
}

// NewManager returns a Manager of the agents in store, whose backups b keeps;
// new agents run the ACAs of b, usable ones only.
func NewManager(store *Store, terminals *terminal.Supervisor, b *Backups) *Manager {
	return &Manager{store: store, terminals: terminals, backups: b, acas: b.acas}
}

// ACAs returns the AI coding agents the Manager runs.
func (m *Manager) ACAs() *ACAs {
	return m.acas
}

// Store returns the Store the Manager keeps its agents in.
func (m *Manager) Store() *Store {
	return m.store
}

// ErrUnknownACA is returned for an AI coding agent this daemon does not run.
var ErrUnknownACA = errors.New("no such AI coding agent here")

// Models returns the models the AI coding agent with id offers, as it says; only
// one a new agent can run, as ACAs.ForNew says.
func (m *Manager) Models(ctx context.Context, id string) ([]aca.Model, error) {
	chosen, err := m.acas.ForNew(ctx, id)
	if err != nil {
		return nil, err
	}
	account, err := holder.LookupAccount(ctx)
	if err != nil {
		return nil, err
	}
	models, err := chosen.Models(ctx, aca.Launch{UserHome: account.Home, Shell: account.LoginShell()})
	if err != nil {
		return nil, err
	}
	logging.FromContext(ctx).Info("models listed", "aca", id, "count", len(models))
	return models, nil
}

// Launch is what an agent is started with every time, chosen when it is
// created.
type Launch struct {
	ACA         string          // the id of the AI coding agent it runs; "" is the first usable
	Model       string          // passed to its ACA as its model; "" is the ACA's default
	Permissions aca.Permissions // "" is aca.PermissionsFull
}

// ErrInvalidModel is returned for a model with whitespace or a control character in
// it, or longer than maxModel, which no ACA names a model with. Nothing else is
// checked: the ACA says whether it knows it.
var ErrInvalidModel = errors.New("the model cannot have whitespace or control characters, or be longer than 200 bytes")

const maxModel = 200

// withDefaults is l checked, its permissions aca.PermissionsFull when it has none.
func (l Launch) withDefaults() (Launch, error) {
	if len(l.Model) > maxModel || strings.ContainsFunc(l.Model, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return Launch{}, fmt.Errorf("%w: %q", ErrInvalidModel, l.Model)
	}
	l.Permissions = cmp.Or(l.Permissions, aca.PermissionsFull)
	return l, nil
}

// Create adds an agent named name, or a generated codename when name is
// empty, to the project, under the agent with parentID unless it is "", and
// starts its AI coding agent in dir in a new session, with l then and on every
// later start. A parent that is not in the project is not found, and a tombstone
// returns a *RemovedError. One that does not start leaves the agent failed and
// returns a *StartError.
func (m *Manager) Create(ctx context.Context, projectID, name, dir, parentID string, l Launch) (Agent, error) {
	return m.create(ctx, projectID, name, dir, parentID, l, "", nil)
}

// CreateLinked is Create for an agent with no parent that is told first first, and
// whose linked writes what points at it, both in the transaction that adds it: so
// that it is never there without its first message, nor what points at it without
// it.
func (m *Manager) CreateLinked(ctx context.Context, projectID, name, dir string, l Launch, first string, linked func(ctx context.Context, tx sqlite.Execer, agentID string) error) (Agent, error) {
	return m.create(ctx, projectID, name, dir, "", l, first, linked)
}

func (m *Manager) create(ctx context.Context, projectID, name, dir, parentID string, l Launch, first string, linked func(ctx context.Context, tx sqlite.Execer, agentID string) error) (Agent, error) {
	l, err := l.withDefaults()
	if err != nil {
		return Agent{}, err
	}
	chosen, err := m.acas.ForNew(ctx, l.ACA)
	if err != nil {
		return Agent{}, err
	}
	logging.FromContext(ctx).Info("aca chosen", "aca", chosen.ID(), "asked", l.ACA)
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
	id, err := sqlite.NewID("agent")
	if err != nil {
		return Agent{}, err
	}
	sessionID, err := newSessionID()
	if err != nil {
		return Agent{}, err
	}
	t, err := m.terminals.CreateAgent(ctx, projectID, name, dir, func(ctx context.Context, tx sqlite.Execer, terminalID string) error {
		if err := m.store.insert(ctx, tx, id, terminalID, chosen.ID(), sessionID, parentID, l); err != nil {
			return err
		}
		if first != "" {
			if err := m.terminals.Store().AddFirstMessage(ctx, tx, terminalID, sessionID, first); err != nil {
				return err
			}
		}
		if linked != nil {
			return linked(ctx, tx, id)
		}
		return nil
	})
	if err != nil {
		return Agent{}, m.agentError(ctx, err)
	}
	return m.withTerminal(ctx, t)
}

// Start starts an agent whose AI coding agent ended, in the same session; a
// live agent fails with a *RunningError and a tombstone with a *RemovedError.
//
// A session of it still running outside its terminal holds the conversation that start
// goes on with, and is stopped first; it returns those it stopped. One at work fails with
// a *BusyError, as stopping it could lose the work.
func (m *Manager) Start(ctx context.Context, id string) (Agent, []string, error) {
	a, err := m.store.Get(ctx, id)
	if err != nil {
		return Agent{}, nil, err
	}
	if a.Tombstone() {
		return Agent{}, nil, &RemovedError{Agent: a}
	}
	var stopped []string
	if !a.Terminal.State.Live() {
		if err := m.installed(ctx, a); err != nil {
			return Agent{}, nil, err
		}
		for _, d := range m.backups.detached(ctx, a) {
			if d.Working {
				return Agent{}, nil, &BusyError{Agent: a, Detached: d.Session}
			}
		}
		stopped = m.backups.stop(ctx, a)
		if a, err = m.store.Get(ctx, id); err != nil {
			return Agent{}, nil, err
		}
	}
	t, err := m.terminals.StartAgent(ctx, a.Terminal.ID)
	if err != nil {
		return Agent{}, nil, m.agentError(ctx, err)
	}
	return a.OnRow(t), stopped, nil
}

// ResumeLinked goes on with the agent in its session: it tells it text, and has
// linked write what points at it, in one transaction, then starts it if it is not
// running, as Start does. So a task's agent resumed is never running for its task
// without being told why. A tombstone fails with a *RemovedError.
func (m *Manager) ResumeLinked(ctx context.Context, id, text string, linked func(ctx context.Context, tx sqlite.Execer, agentID string) error) (Agent, error) {
	a, err := m.store.Get(ctx, id)
	if err != nil {
		return Agent{}, err
	}
	if a.Tombstone() {
		return Agent{}, &RemovedError{Agent: a}
	}
	tx, err := m.store.db.BeginTx(ctx, nil)
	if err != nil {
		return Agent{}, err
	}
	defer tx.Rollback()
	if err := m.terminals.Store().AddFirstMessage(ctx, tx, a.Terminal.ID, a.SessionID, text); err != nil {
		return Agent{}, err
	}
	if err := linked(ctx, tx, id); err != nil {
		return Agent{}, err
	}
	if err := tx.Commit(); err != nil {
		return Agent{}, err
	}
	logging.FromContext(ctx).Info("agent resumed", "agent_id", id, "message_bytes", len(text), "live", a.Terminal.State.Live())
	if a.Terminal.State.Live() {
		return a, nil
	}
	a, _, err = m.Start(ctx, id)
	return a, err
}

// Stop ends the agent's AI coding agent, if it runs, and waits until it is gone. Its
// session stays, to go on with; its project opening does not start it again, as it
// does an agent stopped with the project.
func (m *Manager) Stop(ctx context.Context, id string) (Agent, error) {
	a, err := m.store.Get(ctx, id)
	if err != nil {
		return Agent{}, err
	}
	t, err := m.terminals.StopAgent(ctx, a.Terminal.ID)
	if err != nil {
		return Agent{}, m.agentError(ctx, err)
	}
	logging.FromContext(ctx).Info("agent stopped", "agent_id", id, "state", t.State)
	return a.OnRow(t), nil
}

// DetachedAtWork returns the project's agents with a session at work outside their
// terminal, which closing or removing the project would stop.
func (m *Manager) DetachedAtWork(ctx context.Context, projectID string) ([]Agent, error) {
	list, err := m.store.List(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var working []Agent
	for _, a := range list {
		if !a.Tombstone() && slices.ContainsFunc(m.backups.detached(ctx, a), func(d aca.DetachedSession) bool { return d.Working }) {
			working = append(working, a)
		}
	}
	return working, nil
}

// StopDetached stops what of the project's agents runs outside their terminals, and
// returns the sessions it stopped.
func (m *Manager) StopDetached(ctx context.Context, projectID string) ([]string, error) {
	list, err := m.store.List(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var stopped []string
	for _, a := range list {
		if !a.Tombstone() {
			stopped = append(stopped, m.backups.stop(ctx, a)...)
		}
	}
	return stopped, nil
}

// Restart starts an agent in a new session under the same id, whose first message tells
// it to read its history. A tombstone fails with a *RemovedError, one in the middle of
// something with a *BusyError unless force, and one that does not start with a
// *StartError, leaving it failed in the new session.
//
// It also returns the sessions of the agent it stopped outside its terminal: what is left
// running there would go on with the old conversation beside the new one.
func (m *Manager) Restart(ctx context.Context, id string, force bool) (Agent, []string, error) {
	a, err := m.store.Get(ctx, id)
	if err != nil {
		return Agent{}, nil, err
	}
	if a.Tombstone() {
		return Agent{}, nil, &RemovedError{Agent: a}
	}
	// Asked first: the restart stops the terminal and moves the row before the AI
	// coding agent is told of the new session.
	if known := m.backups.known(a.ACA); known != nil && !aca.CanBeginSession(known) {
		return Agent{}, nil, fmt.Errorf("agent %s cannot be restarted in a new session: %w", a.ID, aca.ErrNotSupported)
	}
	if err := m.installed(ctx, a); err != nil {
		return Agent{}, nil, err
	}
	if !force {
		if a.busy() {
			return Agent{}, nil, &BusyError{Agent: a}
		}
		for _, d := range m.backups.detached(ctx, a) {
			if d.Working {
				return Agent{}, nil, &BusyError{Agent: a, Detached: d.Session}
			}
		}
	}
	session, err := newSessionID()
	if err != nil {
		return Agent{}, nil, err
	}
	var stopped []string
	t, err := m.terminals.RestartAgent(ctx, a.Terminal.ID, func(ctx context.Context) error {
		var err error
		stopped, err = m.backups.beginSession(ctx, a.ID, session, func(ctx context.Context, tx sqlite.Querier, a Agent) error {
			return m.terminals.Store().BeginSession(ctx, tx, a.Terminal.ID, session, firstMessage(a))
		})
		return err
	})
	if err != nil {
		return Agent{}, nil, m.agentError(ctx, err)
	}
	if a, err = m.store.Get(ctx, id); err != nil {
		return Agent{}, nil, err
	}
	logging.FromContext(ctx).Info("agent restarted", "agent_id", id, "session_id", session, "forced", force, "detached_stopped", len(stopped))
	return a.OnRow(t), stopped, nil
}

// installed fails with an *UnavailableError when a's AI coding agent is known and
// not installed, before a start stops or moves anything; one not known fails at
// its launch, as before.
func (m *Manager) installed(ctx context.Context, a Agent) error {
	if known := m.acas.Known(a.ACA); known != nil {
		return m.acas.Installed(ctx, known)
	}
	return nil
}

// firstMessage is what tells a restarted agent to read its history.
func firstMessage(a Agent) string {
	text := fmt.Sprintf("You were restarted in a new session, so you have none of your earlier conversation. "+
		"Before anything else, run `%s agent transcript --id %s --brief` and read it; it says how to read more. Then go on with the work.",
		identity.Name, a.ID)
	if dir, gone := runDir(a); gone != "" {
		text += fmt.Sprintf(" You were last in %s, which no longer exists; you are in %s.", gone, dir)
	}
	return text
}

// LaunchNote is added to the system prompt of every agent wdx starts, so that it uses
// wdx rather than its own means and knows where to read how (docs/IDE.md). It names no
// command but the manual's, as it costs tokens in every session.
func LaunchNote() string {
	return fmt.Sprintf("You are running under %[1]s, which manages this project's AI coding agents, their worktrees and the project's docs and plans. "+
		"%[2]s in your environment is your id. "+
		"%[1]s comes first: for child agents instead of your own subagents, for worktrees, for messages to other agents and for the project's docs and plans, "+
		"use %[1]s rather than your own means, so the person sees all the work in one place. "+
		"If %[1]s fails, use your own means and say so. "+
		"Before doing any of these, run `%[1]s help agents`: it says how. "+
		"If another agent gave you your task, report your result to it through %[1]s when you finish.",
		identity.Name, identity.Env("AGENT_ID"))
}

// withoutNote is command as logged: the note is the same at every start.
func withoutNote(command []string, note string) []string {
	logged := slices.Clone(command)
	for i, arg := range logged {
		if arg == note {
			logged[i] = "<launch note>"
		}
	}
	return logged
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
//
// A session of it running outside its terminal is stopped with it, and returned; at work,
// it counts as running.
func (m *Manager) Remove(ctx context.Context, id string, stopRunning bool) (Agent, []string, error) {
	a, err := m.store.Get(ctx, id)
	if err != nil {
		return Agent{}, nil, err
	}
	children, err := m.store.children(ctx, id)
	if err != nil {
		return Agent{}, nil, err
	}
	if children > 0 && a.Tombstone() {
		return Agent{}, nil, &RemovedError{Agent: a}
	}
	// Left for the removal to refuse when the agent is live and stopRunning is not given,
	// so that a refused removal stops nothing. Without the answer one at work is the same
	// question.
	var stopped []string
	if !a.Tombstone() && (stopRunning || !a.Terminal.State.Live()) {
		if !stopRunning {
			for _, d := range m.backups.detached(ctx, a) {
				if d.Working {
					return Agent{}, nil, &RunningError{Agent: a, Detached: d.Session}
				}
			}
		}
		stopped = m.backups.stop(ctx, a)
	}
	if children > 0 {
		_, err := m.terminals.TombstoneAgent(ctx, a.Terminal.ID, stopRunning, func(ctx context.Context, tx sqlite.Execer) error {
			return m.store.markRemoved(ctx, tx, id)
		})
		if err != nil {
			return Agent{}, nil, m.agentError(ctx, err)
		}
		a, err = m.store.Get(ctx, id)
		return a, stopped, err
	}
	t, err := m.terminals.RemoveAgent(ctx, a.Terminal.ID, stopRunning)
	if err != nil {
		return Agent{}, nil, m.agentError(ctx, err)
	}
	a = a.OnRow(t)
	logging.FromContext(ctx).Info("agent removed", "agent_id", a.ID, "detached_stopped", len(stopped))
	if err := m.removeEmptyTombstones(ctx, a.ParentID); err != nil {
		logging.FromContext(ctx).Error("tombstones not removed", "agent_id", a.ID, "err", err)
	}
	if err := m.backups.Sweep(ctx); err != nil {
		logging.FromContext(ctx).Error("agent backups not swept", "err", err)
	}
	return a, stopped, nil
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

// Answer has the holder of a running Claude Code agent answer the prompt on its
// screen with answers, one per question, by keys, and returns the agent once it
// saw the prompt close or its next question show. Another ACA's prompts are not
// read, so its agent fails with aca.ErrNotSupported; a tombstone with a
// *RemovedError, one not running with a *NotRunningError, and an answer not
// taken with a *PromptChangedError, *InvalidAnswerError or *AnswerError.
func (m *Manager) Answer(ctx context.Context, id string, answers []holder.Answer) (Agent, error) {
	a, err := m.store.Get(ctx, id)
	if err != nil {
		return Agent{}, err
	}
	if a.Tombstone() {
		return Agent{}, &RemovedError{Agent: a}
	}
	if a.ACA != (aca.ClaudeCode{}).ID() {
		return Agent{}, fmt.Errorf("%w: only Claude Code's prompts are answered, and agent %s runs %s", aca.ErrNotSupported, a.Terminal.Name, a.ACA)
	}
	t, report, err := m.terminals.AnswerAgent(ctx, a.Terminal.ID, answers)
	if err != nil {
		if e, ok := errors.AsType[*terminal.SendError](err); ok {
			a, gerr := m.withTerminal(ctx, e.Terminal)
			if gerr != nil {
				return Agent{}, gerr
			}
			return Agent{}, &AnswerError{Agent: a, Err: e.Err}
		}
		return Agent{}, m.agentError(ctx, err)
	}
	a = a.OnRow(t)
	logging.FromContext(ctx).Info("agent prompt answer", "agent_id", id, "answers", len(answers), "result", report.Result, "question", report.Question, "entered", report.Entered)
	switch report.Result {
	case holder.Answered:
		return a, nil
	case holder.AnswerPromptChanged:
		return Agent{}, &PromptChangedError{Agent: a, Question: report.Question, Entered: report.Entered}
	case holder.AnswerInvalid:
		return Agent{}, &InvalidAnswerError{Agent: a, Reason: report.Reason}
	}
	return Agent{}, &AnswerError{Agent: a, Result: report.Result, Reason: report.Reason, Entered: report.Entered}
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

// Brief reads the agent's conversation in brief, in any state.
func (m *Manager) Brief(ctx context.Context, id string) (Transcript, error) {
	a, err := m.store.Get(ctx, id)
	if err != nil {
		return Transcript{}, err
	}
	return m.backups.brief(ctx, a)
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
