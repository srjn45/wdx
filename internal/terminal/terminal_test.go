package terminal

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/holder/holdertest"
)

func TestMain(m *testing.M) {
	holdertest.RunIfHolder()
	os.Exit(m.Run())
}

// waitLimit bounds every wait for a holder or a shell.
const waitLimit = 10 * time.Second

// shellScript is the shell of every test terminal: it runs the file run in
// its dir, if there is one, else stays alive.
const shellScript = "#!/bin/sh\nif [ -f run ]; then . ./run; fi\nexec sleep 60\n"

type fixture struct {
	db   *sql.DB
	home string
	opts Options
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	// Holder sockets live under home: a path in t.TempDir can be too long.
	home, err := os.MkdirTemp("/tmp", "wdx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	shell := filepath.Join(home, "shell")
	if err := os.WriteFile(shell, []byte(shellScript), 0o755); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return fixture{db: d, home: home, opts: Options{Executable: exe, Home: home, Env: []string{"PATH=/usr/bin:/bin"}, Shell: shell}}
}

// supervisor returns a Supervisor that removes every terminal it can when
// the test ends, so no holder outlives it.
func (f fixture) supervisor(t *testing.T) *Supervisor {
	t.Helper()
	s := NewSupervisor(context.Background(), NewStore(f.db), f.opts)
	t.Cleanup(func() {
		list, _ := s.store.List(context.Background(), "")
		for _, term := range list {
			// What removing leaves running is killed next.
			_, _ = s.Remove(context.Background(), term.ID, true)
			kill(term.HolderPID, term.PID)
		}
		s.Close()
	})
	return s
}

// exec changes the database behind the Supervisor's back.
func (f fixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) project(t *testing.T, name string) string {
	t.Helper()
	id := "proj-" + name
	if _, err := f.db.Exec(`INSERT INTO projects (id, name, state, created_at, updated_at) VALUES (?, ?, 'open', 0, 0)`, id, name); err != nil {
		t.Fatal(err)
	}
	return id
}

// dir returns a new directory for a terminal whose shell runs script, or
// stays alive when script is empty.
func dir(t *testing.T, script string) string {
	t.Helper()
	d := t.TempDir()
	if script != "" {
		if err := os.WriteFile(filepath.Join(d, "run"), []byte(script+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func create(t *testing.T, s *Supervisor, projectID, name, dir string) Terminal {
	t.Helper()
	term, err := s.Create(context.Background(), projectID, name, dir)
	if err != nil {
		t.Fatal(err)
	}
	return term
}

// waitState waits until the terminal with id is in state and returns it.
func waitState(t *testing.T, s *Supervisor, id string, state State) Terminal {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for {
		term, err := s.store.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if term.State == state {
			return term
		}
		if time.Now().After(deadline) {
			t.Fatalf("want terminal %s %s, it is %s", id, state, term.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func alive(pid int) bool {
	return pid > 0 && syscall.Kill(pid, 0) == nil
}

func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d is still running", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestCreateRuns(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	s := f.supervisor(t)
	p := f.project(t, "warden")
	d := dir(t, "")
	term := create(t, s, p, "", d)
	if term.State != StateRunning || !alive(term.PID) || !alive(term.HolderPID) || term.StartedAt.IsZero() {
		t.Fatalf("want a running terminal, got %+v", term)
	}
	if term.ProjectName != "warden" || term.Dir != d || term.Shell != f.opts.Shell || term.ExitCode != nil || term.Error != "" {
		t.Fatalf("unexpected terminal %+v", term)
	}
	if !regexp.MustCompile(`^term-[0-9a-f]{8}$`).MatchString(term.ID) || !regexp.MustCompile(`^[a-z]+-[a-z]+$`).MatchString(term.Name) {
		t.Fatalf("want a term- id and a codename, got %s %s", term.ID, term.Name)
	}
}

func TestCreateLoginShell(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.opts.Shell = ""
	s := f.supervisor(t)
	term := create(t, s, f.project(t, "warden"), "", dir(t, ""))
	if !filepath.IsAbs(term.Shell) {
		t.Fatalf("want the login shell, got %q", term.Shell)
	}
}

func TestNames(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	s := f.supervisor(t)
	ctx := context.Background()
	p, other := f.project(t, "warden"), f.project(t, "other")
	first := create(t, s, p, "Build", dir(t, ""))

	_, err := s.Create(ctx, p, "build", dir(t, ""))
	if e, ok := errors.AsType[*NameTakenError](err); !ok || e.Terminal.ID != first.ID {
		t.Fatalf("want the name taken in its project, ignoring case, got %v", err)
	}
	if _, err := s.Create(ctx, p, "id:x", dir(t, "")); !isType[*InvalidNameError](err) {
		t.Fatalf("want a name with : refused, got %v", err)
	}
	create(t, s, other, "build", dir(t, ""))

	if got, err := s.store.GetByName(ctx, p, "BUILD"); err != nil || got.ID != first.ID {
		t.Fatalf("want the terminal found by name, ignoring case, got %+v, %v", got, err)
	}
	if _, err := s.store.GetByName(ctx, other, "nothing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	all, err := s.store.List(ctx, "")
	if err != nil || len(all) != 2 || all[0].ProjectName != "other" || all[1].ProjectName != "warden" {
		t.Fatalf("want every terminal by project name, got %+v, %v", all, err)
	}
	if mine, err := s.store.List(ctx, p); err != nil || len(mine) != 1 || mine[0].ID != first.ID {
		t.Fatalf("want the project's terminals, got %+v, %v", mine, err)
	}
}

func isType[E error](err error) bool {
	_, ok := errors.AsType[E](err)
	return ok
}

func TestExitAndStartAgain(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	s := f.supervisor(t)
	d := dir(t, "exit 3")
	term := create(t, s, f.project(t, "warden"), "", d)
	exited := waitState(t, s, term.ID, StateExited)
	if exited.ExitCode == nil || *exited.ExitCode != 3 || exited.PID != 0 || exited.EndedAt.IsZero() {
		t.Fatalf("want exit code 3 recorded, got %+v", exited)
	}
	waitGone(t, term.HolderPID)

	os.Remove(filepath.Join(d, "run"))
	again, err := s.Start(context.Background(), term.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != term.ID || again.Name != term.Name || again.State != StateRunning || again.ExitCode != nil || !again.EndedAt.IsZero() || again.PID == term.PID {
		t.Fatalf("want the same terminal running a new shell, got %+v", again)
	}
	if _, err := s.Start(context.Background(), term.ID); !isType[*RunningError](err) {
		t.Fatalf("want a running terminal refused, got %v", err)
	}
}

func TestCreateFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	s := f.supervisor(t)
	missing := filepath.Join(t.TempDir(), "missing")
	_, err := s.Create(context.Background(), f.project(t, "warden"), "", missing)
	e, ok := errors.AsType[*StartError](err)
	if !ok || e.Terminal.State != StateFailed || e.Terminal.Error == "" || e.Terminal.PID != 0 {
		t.Fatalf("want a failed terminal, got %v", err)
	}

	if err := os.Mkdir(missing, 0o755); err != nil {
		t.Fatal(err)
	}
	started, err := s.Start(context.Background(), e.Terminal.ID)
	if err != nil || started.State != StateRunning || started.Error != "" {
		t.Fatalf("want it running once its dir exists, got %+v, %v", started, err)
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	s := f.supervisor(t)
	ctx := context.Background()
	term := create(t, s, f.project(t, "warden"), "", dir(t, ""))
	if _, err := s.Remove(ctx, term.ID, false); !isType[*RunningError](err) {
		t.Fatalf("want a running terminal kept, got %v", err)
	}
	if _, err := s.Remove(ctx, term.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.Get(ctx, term.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want the row deleted, got %v", err)
	}
	waitGone(t, term.PID)
	waitGone(t, term.HolderPID)

	exited := create(t, s, f.project(t, "other"), "", dir(t, "exit 0"))
	waitState(t, s, exited.ID, StateExited)
	if _, err := s.Remove(ctx, exited.ID, false); err != nil {
		t.Fatalf("want an exited terminal removed, got %v", err)
	}
}

func TestStopAndStartProject(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	s := f.supervisor(t)
	ctx := context.Background()
	p := f.project(t, "warden")
	// The shell hangs up only after a while: stopping waits for it.
	slow := create(t, s, p, "slow", dir(t, "trap 'sleep 0.3; exit 0' HUP; while :; do sleep 0.1; done"))
	quick := create(t, s, p, "quick", dir(t, ""))
	done := create(t, s, p, "done", dir(t, "exit 4"))
	waitState(t, s, done.ID, StateExited)
	other := create(t, s, f.project(t, "other"), "", dir(t, ""))

	if live, err := s.Live(ctx, p); err != nil || len(live) != 2 {
		t.Fatalf("want 2 live terminals, got %+v, %v", live, err)
	}
	stopped, err := s.StopProject(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(stopped) != 2 {
		t.Fatalf("want 2 stopped, got %+v", stopped)
	}
	for _, term := range stopped {
		if term.State != StateStopped || term.PID != 0 || term.EndedAt.IsZero() {
			t.Fatalf("want it stopped with its shell gone, got %+v", term)
		}
	}
	for _, pid := range []int{slow.PID, quick.PID} {
		waitGone(t, pid)
	}
	if got, _ := s.store.Get(ctx, other.ID); got.State != StateRunning {
		t.Fatalf("want another project's terminal running, got %s", got.State)
	}
	if got, _ := s.store.Get(ctx, done.ID); got.State != StateExited {
		t.Fatalf("want an exited terminal left exited, got %s", got.State)
	}

	started, err := s.StartProject(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, term := range started {
		if term.State != StateRunning || !alive(term.PID) {
			t.Fatalf("want it running again, got %+v", term)
		}
		names = append(names, term.Name)
	}
	if !slices.Equal(names, []string{"quick", "slow"}) {
		t.Fatalf("want the stopped terminals started, got %v", names)
	}
	if got, _ := s.store.Get(ctx, done.ID); got.State != StateExited {
		t.Fatalf("want an exited terminal left exited, got %s", got.State)
	}
}

func TestStartProjectDirGone(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	s := f.supervisor(t)
	ctx := context.Background()
	p := f.project(t, "warden")
	d := dir(t, "")
	term := create(t, s, p, "", d)
	if _, err := s.StopProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(d)
	started, err := s.StartProject(ctx, p)
	if err != nil || len(started) != 1 || started[0].ID != term.ID || started[0].State != StateFailed {
		t.Fatalf("want it failed, not an error, got %+v, %v", started, err)
	}
}

func TestLost(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	s := f.supervisor(t)
	term := create(t, s, f.project(t, "warden"), "", dir(t, ""))
	kill(term.HolderPID, term.PID)
	lost := waitState(t, s, term.ID, StateLost)
	if lost.PID != 0 || lost.HolderPID != 0 || lost.ExitCode != nil || lost.EndedAt.IsZero() {
		t.Fatalf("want it lost without an exit code, got %+v", lost)
	}
}

// TestReconcile stops a daemon's supervisor, changes what its terminals'
// holders do while none runs, and starts another, which must find out.
// TestCreateAfterClose launches a terminal once the Supervisor is closed,
// as a change the stopping daemon waits for does: its holder is left for
// the next daemon to adopt, not watched by a watcher nothing would stop.
func TestCreateAfterClose(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	s := NewSupervisor(context.Background(), NewStore(f.db), f.opts)
	s.Close()
	term := create(t, s, f.project(t, "warden"), "late", dir(t, ""))
	t.Cleanup(func() { kill(term.HolderPID, term.PID) })
	if s.watching(term.ID) || !alive(term.HolderPID) {
		t.Fatalf("want the holder running and not watched, got watched %v", s.watching(term.ID))
	}
}

func TestReconcile(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	p := f.project(t, "warden")
	first := NewSupervisor(ctx, NewStore(f.db), f.opts)
	kept := create(t, first, p, "kept", dir(t, ""))
	exitsDir := dir(t, "while [ ! -e go ]; do sleep 0.05; done; exit 7")
	exits := create(t, first, p, "exits", exitsDir)
	killed := create(t, first, p, "killed", dir(t, ""))
	stopping := create(t, first, p, "stopping", dir(t, ""))
	starting := create(t, first, p, "starting", dir(t, ""))
	crashed := create(t, first, p, "crashed", dir(t, ""))
	first.Close()
	t.Cleanup(func() {
		for _, term := range []Terminal{kept, exits, killed, stopping, starting, crashed} {
			kill(term.HolderPID, term.PID)
		}
	})

	if !alive(kept.HolderPID) || !alive(kept.PID) {
		t.Fatal("want the holders left running")
	}
	if err := os.WriteFile(filepath.Join(exitsDir, "go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	kill(killed.HolderPID, killed.PID)
	waitGone(t, killed.HolderPID)
	// The daemon died after recording a stop and before signalling, or while
	// starting a holder: one that answers and one that does not.
	f.exec(t, "UPDATE terminals SET state = 'stopped' WHERE id = ?", stopping.ID)
	f.exec(t, "UPDATE terminals SET state = 'starting', pid = NULL, holder_pid = NULL WHERE id = ?", starting.ID)
	kill(crashed.HolderPID, crashed.PID)
	waitGone(t, crashed.HolderPID)
	f.exec(t, "UPDATE terminals SET state = 'starting', pid = NULL, holder_pid = NULL WHERE id = ?", crashed.ID)
	waitGone(t, exits.PID)

	second := f.supervisor(t)
	if err := second.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if got := waitState(t, second, kept.ID, StateRunning); got.PID != kept.PID {
		t.Fatalf("want the shell adopted, got %+v", got)
	}
	if got := waitState(t, second, exits.ID, StateExited); got.ExitCode == nil || *got.ExitCode != 7 {
		t.Fatalf("want the exit kept by the holder recorded, got %+v", got)
	}
	waitGone(t, exits.HolderPID)
	waitState(t, second, killed.ID, StateLost)
	waitGone(t, stopping.PID)
	if got := waitState(t, second, stopping.ID, StateStopped); got.PID != 0 {
		t.Fatalf("want the stop finished, got %+v", got)
	}
	if got := waitState(t, second, starting.ID, StateRunning); got.PID != starting.PID || got.HolderPID != starting.HolderPID {
		t.Fatalf("want the started shell adopted, got %+v", got)
	}
	if got := waitState(t, second, crashed.ID, StateFailed); got.Error != "interrupted while starting" {
		t.Fatalf("want it failed, got %+v", got)
	}

	// The adopted terminal is watched again.
	kill(0, kept.PID)
	waitState(t, second, kept.ID, StateExited)
}
