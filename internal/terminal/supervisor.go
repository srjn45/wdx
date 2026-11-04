package terminal

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/editor"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

// DefaultStopGrace is how long a shell has to end after it is hung up on,
// before its holder kills it, unless Options say otherwise.
const DefaultStopGrace = 5 * time.Second

// stopReport is how long past the grace stopping waits for the holder to
// report the shell's exit; past it the holder and shell are killed from
// here.
const stopReport = 5 * time.Second

// RunningError is returned when a terminal is starting or running and the
// request needs it not to be.
type RunningError struct {
	Terminal Terminal
}

func (e *RunningError) Error() string {
	return fmt.Sprintf("terminal %s is %s", e.Terminal.Name, e.Terminal.State)
}

// StartError is returned when a terminal's holder or shell did not start;
// the terminal is then failed.
type StartError struct {
	Terminal Terminal
	Err      error
}

func (e *StartError) Error() string {
	return fmt.Sprintf("terminal %s did not start: %v", e.Terminal.Name, e.Err)
}

func (e *StartError) Unwrap() error { return e.Err }

// NotRunningError is returned when a request needs a terminal's shell running.
type NotRunningError struct {
	Terminal Terminal
}

func (e *NotRunningError) Error() string {
	return fmt.Sprintf("terminal %s is %s, not running", e.Terminal.Name, e.Terminal.State)
}

// SendError is returned when a message did not reach a terminal's holder.
type SendError struct {
	Terminal Terminal
	Err      error
}

func (e *SendError) Error() string {
	return fmt.Sprintf("message not sent to terminal %s: %v", e.Terminal.Name, e.Err)
}

func (e *SendError) Unwrap() error { return e.Err }

// Options say how the Supervisor starts holders.
type Options struct {
	// Executable is the wdx binary holders run.
	Executable string
	Home       string
	// Env holds the variables shells inherit from the daemon, as KEY=value.
	Env []string
	// Shell is the shell terminals run; "" for the user's login shell.
	Shell string
	// HistoryLines bounds each terminal's history; 0 keeps none.
	HistoryLines int64
	// StopGrace is how long a shell has to end after it is hung up on;
	// DefaultStopGrace when 0.
	StopGrace time.Duration
	// StopReport is how long past the grace stopping waits for the holder to
	// report; stopReport when 0.
	StopReport time.Duration
	// Limits are how long holders and the supervisor wait for each other; the
	// holder package's defaults when zero.
	Limits holder.Limits
	// AgentLaunch is what a row of KindAgent runs, asked at each launch,
	// since an AI coding agent may start differently the second time.
	AgentLaunch func(ctx context.Context, t Terminal) (AgentLaunch, error)
	// AgentSession returns the conversation of the agent on a row of KindAgent now:
	// what is waiting for it goes on when a holder dies only if that is the same.
	AgentSession func(ctx context.Context, id string) (string, error)
	// AgentStatus records what a row of KindAgent's process is doing, as its
	// holder told. Nothing is recorded when nil.
	AgentStatus func(ctx context.Context, id string, status holder.Status)
	// AgentEnded is told a row of KindAgent's process ended, when not nil.
	AgentEnded func(id string)
	// Editor is the editor files are edited with; editor.Default when "".
	Editor string
	// EditorCommand is what runs the editor named program on path; its Command
	// when nil.
	EditorCommand func(program, path string) []string
}

// EditorMissingError is returned when the editor is not found through the login
// shell.
type EditorMissingError struct {
	Editor string
}

func (e *EditorMissingError) Error() string { return e.Editor + " not found" }

// AgentLaunch is how to run a row of KindAgent.
type AgentLaunch struct {
	AgentID string
	// ACA is the id of its AI coding agent, which the holder reads the screen
	// as.
	ACA     string
	Command []string
	// Dir is where it runs, which may not be the row's: it may have moved.
	Dir string
}

// Supervisor starts terminals' holders, watches them and stops them. It
// keeps one connection to each live holder, over which the holder pushes
// its shell's exit.
type Supervisor struct {
	store *Store
	opts  Options
	ctx   context.Context // ends with Close; the watchers log with it
	stop  context.CancelFunc

	mu           sync.Mutex
	watches      map[string]*watch // by terminal id
	closed       bool              // no more watching: the daemon is stopping
	shell        string            // what new terminals run; see Options.Shell
	historyLines int64             // what terminals start with; see Options.HistoryLines
	editor       string            // what new editors run; see Options.Editor
}

// Reconfigure changes what terminals and editors get from now on. Those running
// keep what they started with: their holders and shells have it already.
func (s *Supervisor) Reconfigure(shell string, historyLines int64, editorName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shell, s.historyLines, s.editor = shell, historyLines, cmp.Or(editorName, editor.Default)
}

func (s *Supervisor) newTerminals() (shell string, historyLines int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shell, s.historyLines
}

func (s *Supervisor) newEditors() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.editor
}

// watch is a terminal's live connection and the goroutine following it.
type watch struct {
	mu   sync.Mutex
	conn *holder.Conn
	// stopping is set once the holder is asked to stop: a connection dropping
	// may lose that, so that the watcher asks again on the next one.
	stopping bool
	grace    time.Duration
	done     chan struct{} // closed once the watcher returns
}

// stop asks the holder to stop the shell, now and on any later connection.
func (w *watch) stop(grace time.Duration) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopping, w.grace = true, grace
	return w.conn.Stop(grace)
}

func (w *watch) current() *holder.Conn {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn
}

// NewSupervisor returns a Supervisor of the terminals in store. Its
// watchers run until Close, logging with ctx's logger.
func NewSupervisor(ctx context.Context, store *Store, o Options) *Supervisor {
	ctx, stop := context.WithCancel(context.WithoutCancel(ctx))
	o.StopGrace = cmp.Or(o.StopGrace, DefaultStopGrace)
	return &Supervisor{store: store, opts: o, ctx: ctx, stop: stop, watches: map[string]*watch{}, shell: o.Shell, historyLines: o.HistoryLines, editor: cmp.Or(o.Editor, editor.Default)}
}

// Store returns the Store the Supervisor keeps its terminals in.
func (s *Supervisor) Store() *Store {
	return s.store
}

// Close stops watching the holders and leaves them running, as a daemon
// that stops does: the next one adopts them.
func (s *Supervisor) Close() {
	s.stop()
	s.mu.Lock()
	s.closed = true
	watches := make([]*watch, 0, len(s.watches))
	for _, w := range s.watches {
		watches = append(watches, w)
	}
	s.mu.Unlock()
	for _, w := range watches {
		w.current().Close()
		<-w.done
	}
}

// StopWait bounds how long stopping a terminal takes.
func (s *Supervisor) StopWait() time.Duration {
	return s.opts.StopGrace + cmp.Or(s.opts.StopReport, stopReport)
}

// Create adds a terminal named name, or a generated codename when name is
// empty, to the project, and starts its shell in dir. A shell that does not
// start leaves the terminal failed and returns a *StartError.
func (s *Supervisor) Create(ctx context.Context, projectID, name, dir string) (Terminal, error) {
	return s.createRunning(ctx, Terminal{Kind: KindShell, ProjectID: projectID, Name: name, Dir: dir}, nil, nil)
}

// CreateCommand is Create for a terminal that runs command through the shell and
// ends with it, so that its exit code is the command's. Started again, it is a shell.
func (s *Supervisor) CreateCommand(ctx context.Context, projectID, name, dir string, command []string) (Terminal, error) {
	return s.createRunning(ctx, Terminal{Kind: KindShell, ProjectID: projectID, Name: name, Dir: dir}, nil, command)
}

// CreateAgent is Create for an agent's row: linked inserts what points at the
// new row, in the transaction that inserts it.
func (s *Supervisor) CreateAgent(ctx context.Context, projectID, name, dir string, linked func(ctx context.Context, tx db.Execer, id string) error) (Terminal, error) {
	return s.createRunning(ctx, Terminal{Kind: KindAgent, ProjectID: projectID, Name: name, Dir: dir}, linked, nil)
}

// lookEditorTimeout bounds looking for the editor through the login shell, which
// reads the user's profile and rc.
const lookEditorTimeout = 10 * time.Second

// OpenEditor returns the editor running in the project on path, relative to dir,
// or starts the one set on it and returns it with created true. One not found
// through the login shell fails with an *EditorMissingError, and one that does
// not start with a *StartError.
func (s *Supervisor) OpenEditor(ctx context.Context, projectID, dir, path string) (t Terminal, created bool, err error) {
	log := logging.FromContext(ctx)
	running, err := s.store.query(ctx, kindWhere(KindEditor)+" AND t.project_id = ? AND t.dir = ? AND t.editor_path = ?", projectID, dir, path)
	if err != nil {
		return Terminal{}, false, err
	}
	if len(running) > 0 {
		log.Info("editor already running on the file", "terminal_id", running[0].ID, "path", path)
		return running[0], false, nil
	}
	program := s.newEditors()
	var command []string
	if s.opts.EditorCommand != nil {
		command = s.opts.EditorCommand(program, path)
	} else {
		e, ok := editor.Lookup(program)
		if !ok {
			return Terminal{}, false, &EditorMissingError{Editor: program}
		}
		command = e.Command(path)
	}
	shell, err := s.shellFor(ctx)
	if err != nil {
		return Terminal{}, false, err
	}
	if err := s.lookPath(ctx, shell, dir, command[0]); err != nil {
		log.Info("editor not found", "editor", program, "program", command[0], "err", err)
		return Terminal{}, false, &EditorMissingError{Editor: program}
	}
	t, err = s.createRunning(ctx, Terminal{Kind: KindEditor, ProjectID: projectID, Dir: dir, Editor: program, EditorPath: path}, nil, command)
	return t, err == nil, err
}

// lookPath is holder.LookPath for at most lookEditorTimeout.
func (s *Supervisor) lookPath(ctx context.Context, shell, dir, program string) error {
	look, cancel := context.WithTimeout(ctx, lookEditorTimeout)
	defer cancel()
	return holder.LookPath(look, shell, dir, s.opts.Env, program)
}

// LookProgram fails when program is not found through the shell new terminals run,
// started as a holder starts it, in the user's home: where an AI coding agent's
// program is looked for.
func (s *Supervisor) LookProgram(ctx context.Context, program string) error {
	shell, err := s.shellFor(ctx)
	if err != nil {
		return err
	}
	account, err := holder.LookupAccount(ctx)
	if err != nil {
		return err
	}
	return s.lookPath(ctx, shell, account.Home, program)
}

// shellFor returns the shell new terminals run.
func (s *Supervisor) shellFor(ctx context.Context) (string, error) {
	shell, _ := s.newTerminals()
	if shell != "" {
		return shell, nil
	}
	account, err := holder.LookupAccount(ctx)
	if err != nil {
		return "", err
	}
	shell = account.LoginShell()
	logging.FromContext(ctx).Info("login shell looked up", "shell", shell)
	return shell, nil
}

// RenameAgent renames the agent's row with id. Its holder and process go by id,
// and do not notice.
func (s *Supervisor) RenameAgent(ctx context.Context, id, name string) (Terminal, error) {
	return s.store.rename(ctx, id, name)
}

// createRunning inserts the row t asks for, with the shell, and starts it, running
// command when given.
func (s *Supervisor) createRunning(ctx context.Context, t Terminal, linked func(ctx context.Context, tx db.Execer, id string) error, command []string) (Terminal, error) {
	shell, err := s.shellFor(ctx)
	if err != nil {
		return Terminal{}, err
	}
	t.Shell = shell
	if t, err = s.store.insert(ctx, t, linked); err != nil {
		return Terminal{}, err
	}
	return s.launchRunning(ctx, t, command)
}

// Start starts a fresh shell for a terminal whose last one ended; a live
// terminal fails with a *RunningError. An agent's row is not found.
func (s *Supervisor) Start(ctx context.Context, id string) (Terminal, error) {
	return s.start(ctx, KindShell, id)
}

// StartAgent is Start for an agent's row; a terminal's is not found.
func (s *Supervisor) StartAgent(ctx context.Context, id string) (Terminal, error) {
	return s.start(ctx, KindAgent, id)
}

func (s *Supervisor) start(ctx context.Context, kind Kind, id string) (Terminal, error) {
	t, err := s.store.ofKind(ctx, kind, id)
	if err != nil {
		return Terminal{}, err
	}
	if t.State.Live() {
		return Terminal{}, &RunningError{Terminal: t}
	}
	if _, err := s.store.relaunch(ctx, id, StateExited, StateFailed, StateLost, StateStopped); err != nil {
		return Terminal{}, err
	}
	return s.launch(ctx, t)
}

// RestartAgent stops the process of an agent's row if it has one, has prepare move it to
// a new session once nothing can write to the old one, and starts it. One that does not
// start is failed, and returns a *StartError. A terminal's is not found.
func (s *Supervisor) RestartAgent(ctx context.Context, id string, prepare func(ctx context.Context) error) (Terminal, error) {
	t, err := s.store.ofKind(ctx, KindAgent, id)
	if err != nil {
		return Terminal{}, err
	}
	if t.State.Live() || s.watching(id) {
		if err := s.stopOne(ctx, t); err != nil {
			return Terminal{}, err
		}
	}
	if err := prepare(ctx); err != nil {
		return Terminal{}, err
	}
	if _, err := s.store.relaunch(ctx, id, StateExited, StateFailed, StateLost, StateStopped); err != nil {
		return Terminal{}, err
	}
	return s.launch(ctx, t)
}

// launch starts the holder of t, which is starting.
func (s *Supervisor) launch(ctx context.Context, t Terminal) (Terminal, error) {
	return s.launchRunning(ctx, t, nil)
}

// launchRunning is launch with command to run in place of the shell, if any.
func (s *Supervisor) launchRunning(ctx context.Context, t Terminal, command []string) (Terminal, error) {
	log := logging.FromContext(ctx)
	var agent AgentLaunch
	var started holder.Started
	var err error
	_, historyLines := s.newTerminals()
	if t.Kind == KindEditor {
		historyLines = 0 // what scrolled by in an editor is the file, kept on disk already
	}
	if t.Kind == KindAgent {
		agent, err = s.opts.AgentLaunch(ctx, t)
		command = agent.Command
	}
	if err == nil {
		started, err = holder.Start(holder.WithLimits(ctx, s.opts.Limits), holder.StartOptions{
			Executable: s.opts.Executable, Home: s.opts.Home,
			ID: t.ID, Project: t.ProjectName, Dir: cmp.Or(agent.Dir, t.Dir), Shell: t.Shell, AgentID: agent.AgentID, ACA: agent.ACA, Command: command, Env: s.opts.Env,
			HistoryLines: historyLines,
		})
	}
	var conn *holder.Conn
	if err == nil {
		if conn, err = holder.Dial(holder.WithLimits(ctx, s.opts.Limits), s.socket(t.ID), t.ID); err != nil {
			log.Error("holder started but does not answer, killing it", "terminal_id", t.ID, "holder_pid", started.HolderPID, "err", err)
			kill(started.HolderPID, started.PID)
			err = fmt.Errorf("the holder did not answer: %w", err)
		}
	}
	if err != nil {
		if _, ferr := s.store.failed(ctx, t.ID, err.Error()); ferr != nil {
			return Terminal{}, ferr
		}
		t.State, t.Error = StateFailed, err.Error()
		failed, gerr := s.rowOr(ctx, t)
		if gerr != nil {
			return Terminal{}, gerr
		}
		return Terminal{}, &StartError{Terminal: failed, Err: err}
	}
	// Before it is running, so that nothing shows what the last run was doing.
	s.recordStatus(ctx, t.Kind, t.ID, conn)
	if _, err := s.store.running(ctx, t.ID, started.PID, started.HolderPID, time.Now()); err != nil {
		conn.Close()
		return Terminal{}, err
	}
	s.catchUp(ctx, t, conn)
	s.follow(t, conn)
	t.State = StateExited // if its row is gone, it ended already
	return s.rowOr(ctx, t)
}

// rowOr returns the row of t as it is now, or t when the row is gone, as an
// editor's is once it ended.
func (s *Supervisor) rowOr(ctx context.Context, t Terminal) (Terminal, error) {
	now, err := s.store.row(ctx, t.ID)
	if errors.Is(err, ErrNotFound) && t.Kind == KindEditor {
		return t, nil
	}
	return now, err
}

// Remove deletes a terminal and returns it as it was last, stopped when
// it was live. A live one fails with a *RunningError unless stopRunning,
// which stops its shell first. An agent's row is not found.
func (s *Supervisor) Remove(ctx context.Context, id string, stopRunning bool) (Terminal, error) {
	return s.remove(ctx, KindShell, id, stopRunning)
}

// RemoveAgent is Remove for an agent's row, which takes the agent with it; a
// terminal's is not found.
func (s *Supervisor) RemoveAgent(ctx context.Context, id string, stopRunning bool) (Terminal, error) {
	return s.remove(ctx, KindAgent, id, stopRunning)
}

// TombstoneAgent ends an agent's row for good but keeps it, with its history:
// stopped when it was live, and failing with a *RunningError unless
// stopRunning, as RemoveAgent. linked marks what points at the row, in the
// transaction that ends it. A terminal's is not found.
func (s *Supervisor) TombstoneAgent(ctx context.Context, id string, stopRunning bool, linked func(ctx context.Context, tx db.Execer) error) (Terminal, error) {
	if _, err := s.stopForRemoval(ctx, KindAgent, id, stopRunning); err != nil {
		return Terminal{}, err
	}
	if err := s.store.tombstone(ctx, id, linked); err != nil {
		return Terminal{}, err
	}
	return s.store.row(ctx, id)
}

func (s *Supervisor) remove(ctx context.Context, kind Kind, id string, stopRunning bool) (Terminal, error) {
	t, err := s.stopForRemoval(ctx, kind, id, stopRunning)
	if err != nil {
		return Terminal{}, err
	}
	if err := s.store.remove(ctx, id); err != nil {
		return Terminal{}, err
	}
	if err := s.Sweep(ctx); err != nil {
		logging.FromContext(ctx).Error("terminal directories not swept", "err", err)
	}
	return t, nil
}

// stopForRemoval returns the row with id, its process gone: a live one fails
// with a *RunningError unless stopRunning.
func (s *Supervisor) stopForRemoval(ctx context.Context, kind Kind, id string, stopRunning bool) (Terminal, error) {
	t, err := s.store.ofKind(ctx, kind, id)
	if err != nil {
		return Terminal{}, err
	}
	if t.State.Live() && !stopRunning {
		return Terminal{}, &RunningError{Terminal: t}
	}
	if t.State.Live() || s.watching(id) {
		if err := s.stopOne(ctx, t); err != nil {
			return Terminal{}, err
		}
		return s.store.row(ctx, id)
	}
	return t, nil
}

// History reads count lines of the terminal's history from line from on,
// or its last count lines when from is nil. An agent's row is not found.
func (s *Supervisor) History(ctx context.Context, id string, from *int64, count int) (holder.History, error) {
	return s.history(ctx, KindShell, id, from, count)
}

// AgentHistory is History for an agent's row; a terminal's is not found.
func (s *Supervisor) AgentHistory(ctx context.Context, id string, from *int64, count int) (holder.History, error) {
	return s.history(ctx, KindAgent, id, from, count)
}

func (s *Supervisor) history(ctx context.Context, kind Kind, id string, from *int64, count int) (holder.History, error) {
	if _, err := s.store.ofKind(ctx, kind, id); err != nil {
		return holder.History{}, err
	}
	dir := holder.HistoryDir(s.opts.Home, id)
	if from == nil {
		return holder.TailHistory(dir, count)
	}
	return holder.ReadHistory(dir, *from, count)
}

// Send puts prompt in the inbox of a running terminal's holder and returns the
// terminal. An agent's row is not found.
func (s *Supervisor) Send(ctx context.Context, id, prompt string) (Terminal, string, error) {
	return s.send(ctx, KindShell, id, prompt, false)
}

// SendAgent is Send for an agent's row; instant says the message may interrupt
// its turn.
func (s *Supervisor) SendAgent(ctx context.Context, id, prompt string, instant bool) (Terminal, string, error) {
	return s.send(ctx, KindAgent, id, prompt, instant)
}

// send stores the message, then hands it to the holder, whose inbox goes on as it
// always has: the table is what recovers it if that holder dies. It returns the
// message's id.
func (s *Supervisor) send(ctx context.Context, kind Kind, id, prompt string, instant bool) (Terminal, string, error) {
	t, err := s.store.ofKind(ctx, kind, id)
	if err != nil {
		return Terminal{}, "", err
	}
	if t.State != StateRunning {
		return Terminal{}, "", &NotRunningError{Terminal: t}
	}
	conn, ok := s.currentConn(id)
	if !ok {
		return Terminal{}, "", &SendError{Terminal: t, Err: errors.New("no connection to its holder")}
	}
	messageID, err := s.store.addMessage(ctx, id, prompt, instant, s.sessionOf(ctx, kind, id))
	if err != nil {
		return Terminal{}, "", err
	}
	if err := s.store.sendOne(ctx, kind, conn, Message{ID: messageID, Text: prompt, Instant: instant}); err != nil {
		// The sender is told it failed, and will send it again if it still wants to.
		if derr := s.store.DiscardMessage(ctx, messageID); derr != nil {
			logging.FromContext(ctx).Error("message not taken back", "message_id", messageID, "err", derr)
		}
		return Terminal{}, "", &SendError{Terminal: t, Err: err}
	}
	logging.FromContext(ctx).Info("message queued", "message_id", messageID, "terminal_id", id, "prompt_bytes", len(prompt), "instant", instant)
	return t, messageID, nil
}

// currentConn returns the connection to the terminal's holder.
func (s *Supervisor) currentConn(id string) (*holder.Conn, bool) {
	s.mu.Lock()
	w := s.watches[id]
	s.mu.Unlock()
	if w == nil {
		return nil, false
	}
	return w.current(), true
}

// sessionOf is the agent's conversation now, "" for a shell.
func (s *Supervisor) sessionOf(ctx context.Context, kind Kind, id string) string {
	if kind != KindAgent || s.opts.AgentSession == nil {
		return ""
	}
	session, err := s.opts.AgentSession(ctx, id)
	if err != nil {
		logging.FromContext(ctx).Info("agent session not read", "terminal_id", id, "err", err)
	}
	return session
}

// catchUp brings the table and a holder just connected to in line: what it typed
// while nobody was there to be told, then what it should have. A holder ignores a
// message it already has, so one it may have is simply sent again. Messages for a
// holder that died go on only if they still mean something: to the same conversation
// of an agent. Else, as in a shell started anew, a human decides.
func (s *Supervisor) catchUp(ctx context.Context, t Terminal, conn *holder.Conn) {
	log := logging.FromContext(ctx).With("terminal_id", t.ID)
	if err := s.store.messagesDelivered(ctx, conn.TakeDelivered()); err != nil {
		log.Error("deliveries not recorded", "err", err)
	}
	if !conn.Confirms() {
		return // nothing it says can be gone by, so it is not sent anything twice
	}
	pending, err := s.store.pendingMessages(ctx, t.ID)
	if err != nil {
		log.Error("messages not read", "err", err)
		return
	}
	holderPID := conn.Hello().HolderPID
	session := s.sessionOf(ctx, t.Kind, t.ID)
	for _, m := range pending {
		sameHolder := m.HolderPID == 0 || m.HolderPID == holderPID
		if !sameHolder && (t.Kind != KindAgent || m.SessionID == "" || m.SessionID != session) {
			if err := s.store.holdMessage(ctx, m.ID); err != nil {
				log.Error("message not held", "message_id", m.ID, "err", err)
			}
			continue
		}
		if err := s.store.sendOne(ctx, t.Kind, conn, m); err != nil {
			log.Info("message not sent again", "message_id", m.ID, "err", err)
			return // the connection went: the next one comes back here
		}
		log.Info("message sent again", "message_id", m.ID, "attempts", m.Attempts+1)
	}
}

// ResendMessage sends a message still waiting to its terminal or agent as it is now.
func (s *Supervisor) ResendMessage(ctx context.Context, id string) (Message, error) {
	m, err := s.store.Message(ctx, id)
	if err != nil {
		return Message{}, err
	}
	t, err := s.store.row(ctx, m.TerminalID)
	if err != nil {
		return Message{}, err
	}
	if t.State != StateRunning {
		return Message{}, &NotRunningError{Terminal: t}
	}
	conn, ok := s.currentConn(t.ID)
	if !ok {
		return Message{}, &SendError{Terminal: t, Err: errors.New("no connection to its holder")}
	}
	session := s.sessionOf(ctx, t.Kind, t.ID)
	if _, err := db.Exec(ctx, s.store.db, "UPDATE messages SET state = ?, session_id = NULLIF(?, ''), holder_pid = NULL WHERE id = ?", MessagePending, session, id); err != nil {
		return Message{}, err
	}
	if err := s.store.sendOne(ctx, t.Kind, conn, m); err != nil {
		return Message{}, &SendError{Terminal: t, Err: err}
	}
	s.store.changed()
	// As sent, not read back: it may be delivered by now, and its text cleared.
	m.State, m.SessionID, m.Attempts = MessagePending, session, m.Attempts+1
	return m, nil
}

// Sweep deletes what terminals that no longer exist kept on disk: removing
// one, or its project, leaves it behind, as does a daemon that died
// meanwhile. Their holders are gone: a live terminal is stopped before it
// is removed. It also deletes the sockets killed holders left.
func (s *Supervisor) Sweep(ctx context.Context) error {
	if err := s.sweepSockets(ctx); err != nil {
		return err
	}
	log := logging.FromContext(ctx)
	parent := identity.TerminalsDir(s.opts.Home)
	// Listed before the terminals: a directory is made after its row.
	entries, err := os.ReadDir(parent)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	list, err := s.store.rows(ctx, "")
	if err != nil {
		return err
	}
	exist := map[string]bool{}
	for _, t := range list {
		exist[t.ID] = true
	}
	for _, e := range entries {
		if exist[e.Name()] {
			continue
		}
		dir := filepath.Join(parent, e.Name())
		if err := os.RemoveAll(dir); err != nil {
			log.Error("terminal directory not deleted", "path", dir, "err", err)
			continue
		}
		log.Info("terminal directory deleted", "terminal_id", e.Name(), "path", dir)
	}
	return nil
}

// sweepSockets deletes the sockets of holders that cannot be running: a
// holder removes its own when it quits, but not when it is killed.
func (s *Supervisor) sweepSockets(ctx context.Context) error {
	log := logging.FromContext(ctx)
	// Globbed before the terminals are listed: a holder's socket comes
	// after its row.
	sockets, err := filepath.Glob(s.socket("term-*"))
	if err != nil {
		return err
	}
	list, err := s.store.rows(ctx, "")
	if err != nil {
		return err
	}
	live := map[string]bool{}
	for _, t := range list {
		live[t.ID] = t.State.Live() || s.watching(t.ID)
	}
	for _, path := range sockets {
		id := strings.TrimSuffix(filepath.Base(path), ".sock")
		if live[id] {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Error("holder socket not deleted", "path", path, "err", err)
			continue
		}
		log.Info("holder socket deleted", "terminal_id", id, "path", path)
	}
	return nil
}

// Live returns the project's rows of both kinds that are starting or
// running: closing a project stops them all.
func (s *Supervisor) Live(ctx context.Context, projectID string) ([]Terminal, error) {
	list, err := s.store.rows(ctx, projectID)
	return slices.DeleteFunc(list, func(t Terminal) bool { return !t.State.Live() }), err
}

// StopProject stops the project's live terminals, as closing or removing
// it does, and returns them stopped. Each is marked stopped before its
// shell is signalled, so a daemon that dies meanwhile finishes the stop
// when it starts. All are hung up on, then waited for together: closing a
// project takes one stop however many terminals it has.
func (s *Supervisor) StopProject(ctx context.Context, projectID string) ([]Terminal, error) {
	live, err := s.Live(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return s.stopAll(ctx, live)
}

// LiveEditors returns the editors starting or running in the project's checkout at
// dir.
func (s *Supervisor) LiveEditors(ctx context.Context, projectID, dir string) ([]Terminal, error) {
	editors, err := s.store.Editors(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(editors, func(t Terminal) bool { return t.Dir != dir || !t.State.Live() }), nil
}

// StopEditors stops the editors running in the project in dir, as removing that
// worktree does, and returns them as StopProject does.
func (s *Supervisor) StopEditors(ctx context.Context, projectID, dir string) ([]Terminal, error) {
	editors, err := s.store.Editors(ctx, projectID)
	if err != nil {
		return nil, err
	}
	editors = slices.DeleteFunc(editors, func(t Terminal) bool { return t.Dir != dir || !t.State.Live() })
	return s.stopAll(ctx, editors)
}

// stopAll stops the terminals live and returns them stopped: all are hung up on,
// then waited for together.
func (s *Supervisor) stopAll(ctx context.Context, live []Terminal) ([]Terminal, error) {
	var err error
	watches := make([]*watch, len(live))
	for i, t := range live {
		if watches[i], err = s.hangUp(ctx, t, true); err != nil {
			return nil, err
		}
	}
	deadline := time.Now().Add(s.StopWait())
	for i, t := range live {
		if err := s.waitStopped(ctx, t, watches[i], deadline); err != nil {
			return nil, err
		}
	}
	stopped := []Terminal{}
	for _, t := range live {
		t.State, t.PID, t.HolderPID = StateStopped, 0, 0
		if t, err = s.rowOr(ctx, t); err != nil {
			return nil, err
		}
		stopped = append(stopped, t)
	}
	return stopped, nil
}

// StartProject relaunches the project's stopped terminals, as opening it
// does, and returns them in the state they reached: one that did not start
// is failed, not an error.
func (s *Supervisor) StartProject(ctx context.Context, projectID string) ([]Terminal, error) {
	list, err := s.store.rows(ctx, projectID)
	if err != nil {
		return nil, err
	}
	started := []Terminal{}
	for _, t := range list {
		// An editor stopped is on its way out: its row goes once it is gone.
		if t.State != StateStopped || t.Kind == KindEditor {
			continue
		}
		if _, err := s.store.relaunch(ctx, t.ID, StateStopped); err != nil {
			return nil, err
		}
		t, err := s.launch(ctx, t)
		if serr, ok := errors.AsType[*StartError](err); ok {
			t, err = serr.Terminal, nil
		}
		if err != nil {
			return nil, err
		}
		started = append(started, t)
	}
	return started, nil
}

// StopAgent ends the process of an agent's row and waits until it is gone. Its row is
// not marked stopped but ends exited, or lost, as one that ended by itself: StartProject
// starts stopped rows again, and one stopped this way is done with. One not live is
// returned as it is, but one stopped with its project ends exited too. A terminal's
// is not found.
func (s *Supervisor) StopAgent(ctx context.Context, id string) (Terminal, error) {
	t, err := s.store.ofKind(ctx, KindAgent, id)
	if err != nil {
		return Terminal{}, err
	}
	if !t.State.Live() && !s.watching(id) {
		if t.State == StateStopped {
			if _, err := s.store.doneWith(ctx, id); err != nil {
				return Terminal{}, err
			}
		}
		return s.store.row(ctx, id)
	}
	w, err := s.hangUp(ctx, t, false)
	if err != nil {
		return Terminal{}, err
	}
	if err := s.waitStopped(ctx, t, w, time.Now().Add(s.StopWait())); err != nil {
		return Terminal{}, err
	}
	return s.store.row(ctx, id)
}

// stopOne marks t stopped, hangs up on its shell and waits until the
// holder reports it gone.
func (s *Supervisor) stopOne(ctx context.Context, t Terminal) error {
	w, err := s.hangUp(ctx, t, true)
	if err != nil {
		return err
	}
	return s.waitStopped(ctx, t, w, time.Now().Add(s.StopWait()))
}

// hangUp marks t stopped, when mark, and hangs up on its shell, and returns the
// watch that tells when the holder reports it gone; nil when it has no holder,
// and is marked ended, or lost when not marked stopped.
func (s *Supervisor) hangUp(ctx context.Context, t Terminal, mark bool) (*watch, error) {
	log := logging.FromContext(ctx)
	if mark {
		if _, err := s.store.stopped(ctx, t.ID); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	w := s.watches[t.ID]
	s.mu.Unlock()
	if w == nil {
		// Nothing watches it, as after a launch that failed once its holder
		// started: the holder may still run. One that does not answer is
		// gone; its pids are never killed, as they may be another's now.
		conn, err := holder.Dial(holder.WithLimits(ctx, s.opts.Limits), s.socket(t.ID), t.ID)
		if err != nil {
			log.Info("terminal has no holder to stop", "terminal_id", t.ID, "err", err)
			if !mark {
				s.recordGone(ctx, t.ID)
				return nil, nil
			}
			_, err := s.store.ended(ctx, t.ID, time.Now())
			return nil, err
		}
		log.Info("unwatched holder found", "terminal_id", t.ID, "holder_pid", conn.Hello().HolderPID)
		if w = s.follow(t, conn); w == nil {
			return nil, nil // the daemon is stopping: the next one finishes the stop
		}
	}
	log.Info("stopping terminal", "terminal_id", t.ID, "grace_ms", s.opts.StopGrace.Milliseconds())
	if err := w.stop(s.opts.StopGrace); err != nil {
		log.Info("stop not sent", "terminal_id", t.ID, "err", err)
	}
	return w, nil
}

// waitStopped waits until the holder reports t's shell gone, and kills
// both when it has not by deadline.
func (s *Supervisor) waitStopped(ctx context.Context, t Terminal, w *watch, deadline time.Time) error {
	if w == nil {
		return nil
	}
	log := logging.FromContext(ctx)
	select {
	case <-w.done:
		return nil
	case <-time.After(time.Until(deadline)):
	}
	log.Error("holder did not report the shell gone, killing both", "terminal_id", t.ID, "holder_pid", t.HolderPID, "pid", t.PID)
	kill(t.HolderPID, t.PID)
	w.current().Close()
	<-w.done
	_, err := s.store.ended(ctx, t.ID, time.Now())
	return err
}

// Reconcile adopts the holders that outlived the last daemon and records
// what happened while none was running; run it before serving. A starting
// terminal whose holder answers is running, one whose holder does not is
// failed. A running one whose holder does not answer is lost. A stopped one
// whose holder still runs is stopped again.
func (s *Supervisor) Reconcile(ctx context.Context) error {
	log := logging.FromContext(ctx)
	list, err := s.store.inStates(ctx, StateStarting, StateRunning, StateStopped)
	if err != nil {
		return err
	}
	for _, t := range list {
		conn, err := holder.Dial(holder.WithLimits(ctx, s.opts.Limits), s.socket(t.ID), t.ID)
		if err != nil {
			log.Info("holder does not answer", "terminal_id", t.ID, "state", t.State, "err", err)
			switch t.State {
			case StateStarting:
				_, err = s.store.failed(ctx, t.ID, "interrupted while starting")
			case StateRunning:
				_, err = s.store.lost(ctx, t.ID)
			case StateStopped:
				_, err = s.store.ended(ctx, t.ID, time.Now())
			}
			if err != nil {
				return err
			}
			continue
		}
		hello := conn.Hello()
		log.Info("holder adopted", "terminal_id", t.ID, "state", t.State, "pid", hello.PID, "holder_pid", hello.HolderPID, "exited", hello.Exited != nil)
		s.recordStatus(ctx, t.Kind, t.ID, conn)
		if t.State == StateStarting {
			if _, err := s.store.running(ctx, t.ID, hello.PID, hello.HolderPID, time.Now()); err != nil {
				conn.Close()
				return err
			}
		}
		s.catchUp(ctx, t, conn)
		// The watcher records an exit the holder kept.
		s.follow(t, conn)
		if t.State == StateStopped && hello.Exited == nil {
			log.Info("finishing the stop", "terminal_id", t.ID)
			if err := conn.Stop(s.opts.StopGrace); err != nil {
				log.Info("stop not sent", "terminal_id", t.ID, "err", err)
			}
		}
	}
	return nil
}

// follow watches conn until the shell exits, then records the exit and
// releases the holder. A connection that drops is dialled again once; a
// holder that does not answer is lost. Once the Supervisor is closed it
// closes conn instead and returns nil: the next daemon adopts the holder.
func (s *Supervisor) follow(t Terminal, conn *holder.Conn) *watch {
	id := t.ID
	w := &watch{conn: conn, done: make(chan struct{})}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		logging.FromContext(s.ctx).Info("holder not followed: the daemon is stopping", "terminal_id", id)
		conn.Close()
		return nil
	}
	s.watches[id] = w
	s.mu.Unlock()
	go func() {
		defer close(w.done)
		defer func() {
			s.mu.Lock()
			if s.watches[id] == w {
				delete(s.watches, id)
			}
			s.mu.Unlock()
		}()
		ctx := s.ctx
		log := logging.FromContext(ctx).With("terminal_id", id)
		for {
			c := w.current()
			select {
			case exit := <-c.Exited():
				// What it typed last, or it would be sent again.
				if err := s.store.messagesDelivered(ctx, c.TakeDelivered()); err != nil {
					log.Error("deliveries not recorded", "err", err)
				}
				s.recordExit(ctx, id, exit)
				s.agentEnded(t)
				if err := c.Release(); err != nil {
					log.Info("release not sent", "err", err)
				}
				return
			case <-c.StatusChanged():
				s.recordStatus(ctx, t.Kind, id, c)
			case <-c.Delivered():
				if err := s.store.messagesDelivered(ctx, c.TakeDelivered()); err != nil {
					log.Error("deliveries not recorded", "err", err)
				}
			case <-c.Done():
				if ctx.Err() != nil {
					return // the daemon is stopping; the holder runs on
				}
				log.Info("holder connection dropped, dialling again")
				next, err := holder.Dial(holder.WithLimits(ctx, s.opts.Limits), s.socket(id), id)
				if err != nil && ctx.Err() != nil {
					return // the daemon stopped meanwhile; the next one dials it
				}
				if err != nil {
					log.Info("holder gone", "err", err)
					s.recordGone(ctx, id)
					s.agentEnded(t)
					return
				}
				// Under w.mu, which Close takes to close the current
				// connection: either Close closes next, or this sees it began.
				w.mu.Lock()
				if ctx.Err() != nil {
					w.mu.Unlock()
					next.Close()
					return
				}
				w.conn = next
				if w.stopping {
					log.Info("asking the holder to stop again, on the new connection")
					if err := next.Stop(w.grace); err != nil {
						log.Info("stop not sent", "err", err)
					}
				}
				w.mu.Unlock()
				s.recordStatus(ctx, t.Kind, id, next)
				s.catchUp(ctx, t, next)
			}
		}
	}()
	return w
}

func (s *Supervisor) recordStatus(ctx context.Context, kind Kind, id string, conn *holder.Conn) {
	if kind == KindAgent && s.opts.AgentStatus != nil {
		s.opts.AgentStatus(ctx, id, conn.Status())
	}
}

func (s *Supervisor) agentEnded(t Terminal) {
	if t.Kind == KindAgent && s.opts.AgentEnded != nil {
		s.opts.AgentEnded(t.ID)
	}
}

func (s *Supervisor) recordExit(ctx context.Context, id string, exit holder.Exit) {
	changed, err := s.store.exited(ctx, id, exit.Code, exit.At)
	if err == nil && !changed {
		// Stopped on purpose: the state stays.
		_, err = s.store.ended(ctx, id, exit.At)
	}
	if err != nil {
		logging.FromContext(ctx).Error("exit not recorded", "terminal_id", id, "exit_code", exit.Code, "err", err)
	}
}

func (s *Supervisor) recordGone(ctx context.Context, id string) {
	changed, err := s.store.lost(ctx, id)
	if err == nil && !changed {
		_, err = s.store.ended(ctx, id, time.Now())
	}
	if err != nil {
		logging.FromContext(ctx).Error("lost holder not recorded", "terminal_id", id, "err", err)
	}
}

func (s *Supervisor) watching(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.watches[id] != nil
}

func (s *Supervisor) socket(id string) string {
	return identity.HolderSocket(s.opts.Home, id)
}

// kill kills a holder and its shell's process group, when known.
func kill(holderPID, pid int) {
	// What is gone already needs no killing.
	if pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	if holderPID > 0 {
		_ = syscall.Kill(holderPID, syscall.SIGKILL)
	}
}
