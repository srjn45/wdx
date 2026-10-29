package terminal

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"

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
}

// Supervisor starts terminals' holders, watches them and stops them. It
// keeps one connection to each live holder, over which the holder pushes
// its shell's exit.
type Supervisor struct {
	store *Store
	opts  Options
	ctx   context.Context // ends with Close; the watchers log with it
	stop  context.CancelFunc

	mu      sync.Mutex
	watches map[string]*watch // by terminal id
	closed  bool              // no more watching: the daemon is stopping
}

// watch is a terminal's live connection and the goroutine following it.
type watch struct {
	mu   sync.Mutex
	conn *holder.Conn
	done chan struct{} // closed once the watcher returns
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
	return &Supervisor{store: store, opts: o, ctx: ctx, stop: stop, watches: map[string]*watch{}}
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
	return s.opts.StopGrace + stopReport
}

// Create adds a terminal named name, or a generated codename when name is
// empty, to the project, and starts its shell in dir. A shell that does not
// start leaves the terminal failed and returns a *StartError.
func (s *Supervisor) Create(ctx context.Context, projectID, name, dir string) (Terminal, error) {
	shell := s.opts.Shell
	if shell == "" {
		account, err := holder.LookupAccount(ctx)
		if err != nil {
			return Terminal{}, err
		}
		shell = account.LoginShell()
		logging.FromContext(ctx).Info("login shell looked up", "shell", shell)
	}
	t, err := s.store.insert(ctx, projectID, name, dir, shell)
	if err != nil {
		return Terminal{}, err
	}
	return s.launch(ctx, t)
}

// Start starts a fresh shell for a terminal whose last one ended; a live
// terminal fails with a *RunningError.
func (s *Supervisor) Start(ctx context.Context, id string) (Terminal, error) {
	t, err := s.store.Get(ctx, id)
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

// launch starts the holder of t, which is starting.
func (s *Supervisor) launch(ctx context.Context, t Terminal) (Terminal, error) {
	log := logging.FromContext(ctx)
	started, err := holder.Start(ctx, holder.StartOptions{
		Executable: s.opts.Executable, Home: s.opts.Home,
		ID: t.ID, Project: t.ProjectName, Dir: t.Dir, Shell: t.Shell, Env: s.opts.Env,
		HistoryLines: s.opts.HistoryLines,
	})
	var conn *holder.Conn
	if err == nil {
		if conn, err = holder.Dial(ctx, s.socket(t.ID), t.ID); err != nil {
			log.Error("holder started but does not answer, killing it", "terminal_id", t.ID, "holder_pid", started.HolderPID, "err", err.Error())
			kill(started.HolderPID, started.PID)
			err = fmt.Errorf("the holder did not answer: %w", err)
		}
	}
	if err != nil {
		if _, ferr := s.store.failed(ctx, t.ID, err.Error()); ferr != nil {
			return Terminal{}, ferr
		}
		failed, gerr := s.store.Get(ctx, t.ID)
		if gerr != nil {
			return Terminal{}, gerr
		}
		return Terminal{}, &StartError{Terminal: failed, Err: err}
	}
	if _, err := s.store.running(ctx, t.ID, started.PID, started.HolderPID, time.Now()); err != nil {
		conn.Close()
		return Terminal{}, err
	}
	s.follow(t.ID, conn)
	return s.store.Get(ctx, t.ID)
}

// Remove deletes a terminal and returns it as it was last, stopped when
// it was live. A live one fails with a *RunningError unless stopRunning,
// which stops its shell first.
func (s *Supervisor) Remove(ctx context.Context, id string, stopRunning bool) (Terminal, error) {
	t, err := s.store.Get(ctx, id)
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
		if t, err = s.store.Get(ctx, id); err != nil {
			return Terminal{}, err
		}
	}
	if err := s.store.remove(ctx, id); err != nil {
		return Terminal{}, err
	}
	if err := s.Sweep(ctx); err != nil {
		logging.FromContext(ctx).Error("terminal directories not swept", "err", err.Error())
	}
	return t, nil
}

// History reads count lines of the terminal's history from line from on,
// or its last count lines when from is nil.
func (s *Supervisor) History(id string, from *int64, count int) (holder.History, error) {
	dir := holder.HistoryDir(s.opts.Home, id)
	if from == nil {
		return holder.TailHistory(dir, count)
	}
	return holder.ReadHistory(dir, *from, count)
}

// Sweep deletes what terminals that no longer exist kept on disk: removing
// one, or its project, leaves it behind, as does a daemon that died
// meanwhile. Their holders are gone: a live terminal is stopped before it
// is removed.
func (s *Supervisor) Sweep(ctx context.Context) error {
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
	list, err := s.store.List(ctx, "")
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
			log.Error("terminal directory not deleted", "path", dir, "err", err.Error())
			continue
		}
		log.Info("terminal directory deleted", "terminal_id", e.Name(), "path", dir)
	}
	return nil
}

// Live returns the project's terminals that are starting or running.
func (s *Supervisor) Live(ctx context.Context, projectID string) ([]Terminal, error) {
	list, err := s.store.List(ctx, projectID)
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
	watches := make([]*watch, len(live))
	for i, t := range live {
		if watches[i], err = s.hangUp(ctx, t); err != nil {
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
		if t, err = s.store.Get(ctx, t.ID); err != nil {
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
	list, err := s.store.List(ctx, projectID)
	if err != nil {
		return nil, err
	}
	started := []Terminal{}
	for _, t := range list {
		if t.State != StateStopped {
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

// stopOne marks t stopped, hangs up on its shell and waits until the
// holder reports it gone.
func (s *Supervisor) stopOne(ctx context.Context, t Terminal) error {
	w, err := s.hangUp(ctx, t)
	if err != nil {
		return err
	}
	return s.waitStopped(ctx, t, w, time.Now().Add(s.StopWait()))
}

// hangUp marks t stopped and hangs up on its shell, and returns the watch
// that tells when the holder reports it gone; nil when it has no holder,
// and is marked ended.
func (s *Supervisor) hangUp(ctx context.Context, t Terminal) (*watch, error) {
	log := logging.FromContext(ctx)
	if _, err := s.store.stopped(ctx, t.ID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	w := s.watches[t.ID]
	s.mu.Unlock()
	if w == nil {
		log.Info("terminal has no holder to stop", "terminal_id", t.ID)
		_, err := s.store.ended(ctx, t.ID, time.Now())
		return nil, err
	}
	log.Info("stopping terminal", "terminal_id", t.ID, "grace_ms", s.opts.StopGrace.Milliseconds())
	if err := w.current().Stop(s.opts.StopGrace); err != nil {
		log.Info("stop not sent", "terminal_id", t.ID, "err", err.Error())
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
		conn, err := holder.Dial(ctx, s.socket(t.ID), t.ID)
		if err != nil {
			log.Info("holder does not answer", "terminal_id", t.ID, "state", t.State, "err", err.Error())
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
		if t.State == StateStarting {
			if _, err := s.store.running(ctx, t.ID, hello.PID, hello.HolderPID, time.Now()); err != nil {
				conn.Close()
				return err
			}
		}
		// The watcher records an exit the holder kept.
		s.follow(t.ID, conn)
		if t.State == StateStopped && hello.Exited == nil {
			log.Info("finishing the stop", "terminal_id", t.ID)
			if err := conn.Stop(s.opts.StopGrace); err != nil {
				log.Info("stop not sent", "terminal_id", t.ID, "err", err.Error())
			}
		}
	}
	return nil
}

// follow watches conn until the shell exits, then records the exit and
// releases the holder. A connection that drops is dialled again once; a
// holder that does not answer is lost. Once the Supervisor is closed it
// closes conn instead: the next daemon adopts the holder.
func (s *Supervisor) follow(id string, conn *holder.Conn) {
	w := &watch{conn: conn, done: make(chan struct{})}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		logging.FromContext(s.ctx).Info("holder not followed: the daemon is stopping", "terminal_id", id)
		conn.Close()
		return
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
				s.recordExit(ctx, id, exit)
				if err := c.Release(); err != nil {
					log.Info("release not sent", "err", err.Error())
				}
				return
			case <-c.Done():
				if ctx.Err() != nil {
					return // the daemon is stopping; the holder runs on
				}
				log.Info("holder connection dropped, dialling again")
				next, err := holder.Dial(ctx, s.socket(id), id)
				if err != nil && ctx.Err() != nil {
					return // the daemon stopped meanwhile; the next one dials it
				}
				if err != nil {
					log.Info("holder gone", "err", err.Error())
					s.recordGone(ctx, id)
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
				w.mu.Unlock()
			}
		}
	}()
}

func (s *Supervisor) recordExit(ctx context.Context, id string, exit holder.Exit) {
	changed, err := s.store.exited(ctx, id, exit.Code, exit.At)
	if err == nil && !changed {
		// Stopped on purpose: the state stays.
		_, err = s.store.ended(ctx, id, exit.At)
	}
	if err != nil {
		logging.FromContext(ctx).Error("exit not recorded", "terminal_id", id, "exit_code", exit.Code, "err", err.Error())
	}
}

func (s *Supervisor) recordGone(ctx context.Context, id string) {
	changed, err := s.store.lost(ctx, id)
	if err == nil && !changed {
		_, err = s.store.ended(ctx, id, time.Now())
	}
	if err != nil {
		logging.FromContext(ctx).Error("lost holder not recorded", "terminal_id", id, "err", err.Error())
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
