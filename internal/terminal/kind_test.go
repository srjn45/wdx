package terminal

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/waittest"
)

// agentFixture is a fixture whose agent rows run sleep, and a project.
func agentFixture(t *testing.T) (fixture, *Supervisor, string) {
	t.Helper()
	f := newFixture(t)
	f.opts.Shell = "/bin/sh"
	f.opts.AgentLaunch = func(context.Context, Terminal) (AgentLaunch, error) {
		return AgentLaunch{Command: []string{"sleep", "60"}}, nil
	}
	return f, f.supervisor(t), f.project(t, "app")
}

func noLink(context.Context, db.Execer, string) error { return nil }

func TestShellEntriesDoNotFindAnAgentsRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, s, p := agentFixture(t)
	row, err := s.CreateAgent(ctx, p, "helper", t.TempDir(), noLink)
	if err != nil {
		t.Fatal(err)
	}
	if row.Kind != KindAgent || row.State != StateRunning {
		t.Fatalf("row = %+v", row)
	}
	if _, err := s.store.Get(ctx, row.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get: %v", err)
	}
	if _, err := s.store.GetByName(ctx, p, "helper"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByName: %v", err)
	}
	if list, err := s.store.List(ctx, p); err != nil || len(list) != 0 {
		t.Errorf("List: %+v, %v", list, err)
	}
	if list, err := s.store.List(ctx, ""); err != nil || len(list) != 0 {
		t.Errorf("List of all: %+v, %v", list, err)
	}
	if _, err := s.Start(ctx, row.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Start: %v", err)
	}
	if _, err := s.Remove(ctx, row.ID, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("Remove: %v", err)
	}
	if _, err := s.History(ctx, row.ID, nil, 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("History: %v", err)
	}
	// None of them touched it.
	if got, err := s.store.row(ctx, row.ID); err != nil || got.State != StateRunning || got.PID != row.PID {
		t.Fatalf("after the refused calls: %+v, %v", got, err)
	}
}

func TestAgentEntriesDoNotFindAShellsRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, s, p := agentFixture(t)
	term, err := s.Create(ctx, p, "shell", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartAgent(ctx, term.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("StartAgent: %v", err)
	}
	if _, err := s.RemoveAgent(ctx, term.ID, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("RemoveAgent: %v", err)
	}
	if _, err := s.AgentHistory(ctx, term.ID, nil, 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("AgentHistory: %v", err)
	}
	if got, err := s.store.Get(ctx, term.ID); err != nil || got.State != StateRunning {
		t.Fatalf("after the refused calls: %+v, %v", got, err)
	}
}

func TestNamesAreUniqueAcrossKinds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, s, p := agentFixture(t)
	dir := t.TempDir()
	if _, err := s.CreateAgent(ctx, p, "helper", dir, noLink); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, p, "HELPER", dir); err == nil {
		t.Fatal("a terminal took an agent's name")
	} else if taken, ok := errors.AsType[*NameTakenError](err); !ok || taken.Terminal.Kind != KindAgent {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Create(ctx, p, "shell", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAgent(ctx, p, "shell", dir, noLink); err == nil {
		t.Fatal("an agent took a terminal's name")
	} else if taken, ok := errors.AsType[*NameTakenError](err); !ok || taken.Terminal.Kind != KindShell {
		t.Fatalf("CreateAgent: %v", err)
	}
}

// TestLinkFailureLeavesNoRow: the two rows of an agent come together.
func TestLinkFailureLeavesNoRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, s, p := agentFixture(t)
	failure := errors.New("no link")
	_, err := s.CreateAgent(ctx, p, "helper", t.TempDir(), func(context.Context, db.Execer, string) error { return failure })
	if !errors.Is(err, failure) {
		t.Fatalf("CreateAgent: %v", err)
	}
	if list, err := s.store.rows(ctx, p); err != nil || len(list) != 0 {
		t.Fatalf("rows: %+v, %v", list, err)
	}
}

func TestAgentLaunchFailureLeavesItFailed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	f.opts.Shell = "/bin/sh"
	f.opts.AgentLaunch = func(context.Context, Terminal) (AgentLaunch, error) { return AgentLaunch{}, errors.New("unknown ACA") }
	s := f.supervisor(t)
	_, err := s.CreateAgent(ctx, f.project(t, "app"), "helper", t.TempDir(), noLink)
	serr, ok := errors.AsType[*StartError](err)
	if !ok {
		t.Fatalf("CreateAgent: %v", err)
	}
	if serr.Terminal.State != StateFailed || serr.Terminal.Error != "unknown ACA" {
		t.Fatalf("row = %+v", serr.Terminal)
	}
}

// TestAgentLaunchAskedAtEachLaunch: an AI CLI may start differently the
// second time.
func TestAgentLaunchAskedAtEachLaunch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	f.opts.Shell = "/bin/sh"
	var asked []string
	f.opts.AgentLaunch = func(_ context.Context, row Terminal) (AgentLaunch, error) {
		asked = append(asked, row.ID)
		return AgentLaunch{Command: []string{"/bin/sh", "-c", "exit 3"}}, nil
	}
	s := f.supervisor(t)
	row, err := s.CreateAgent(ctx, f.project(t, "app"), "helper", t.TempDir(), noLink)
	if err != nil {
		t.Fatal(err)
	}
	waitRowState(t, s, row.ID, StateExited)
	if got, _ := s.store.row(ctx, row.ID); got.ExitCode == nil || *got.ExitCode != 3 {
		t.Fatalf("exit code = %v", got.ExitCode)
	}
	if _, err := s.StartAgent(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	waitRowState(t, s, row.ID, StateExited)
	if !slices.Equal(asked, []string{row.ID, row.ID}) {
		t.Fatalf("asked = %q", asked)
	}
}

// TestProjectAndDaemonWorkSeesBothKinds: stopping, relaunching and
// reconciling must not miss an agent's row.
func TestProjectAndDaemonWorkSeesBothKinds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, s, p := agentFixture(t)
	dir := t.TempDir()
	row, err := s.CreateAgent(ctx, p, "helper", dir, noLink)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, p, "shell", dir); err != nil {
		t.Fatal(err)
	}
	if live, err := s.Live(ctx, p); err != nil || len(live) != 2 {
		t.Fatalf("Live: %+v, %v", live, err)
	}
	stopped, err := s.StopProject(ctx, p)
	if err != nil || len(stopped) != 2 {
		t.Fatalf("StopProject: %+v, %v", stopped, err)
	}
	started, err := s.StartProject(ctx, p)
	if err != nil || len(started) != 2 {
		t.Fatalf("StartProject: %+v, %v", started, err)
	}

	// A new supervisor, as a new daemon has, adopts the agent's holder, and
	// the sweep keeps what it has on disk.
	s.Close()
	next := NewSupervisor(ctx, NewStore(f.db), f.opts)
	t.Cleanup(next.Close)
	if err := next.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if err := next.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := next.store.row(ctx, row.ID)
	if err != nil || got.State != StateRunning {
		t.Fatalf("after reconciling: %+v, %v", got, err)
	}
	conn, err := holder.Dial(ctx, next.socket(row.ID), row.ID)
	if err != nil {
		t.Fatalf("the holder is gone: %v", err)
	}
	conn.Close()
	if _, err := next.RemoveAgent(ctx, row.ID, true); err != nil {
		t.Fatal(err)
	}
}

func waitRowState(t *testing.T, s *Supervisor, id string, want State) {
	t.Helper()
	deadline := waittest.Deadline(t)
	for {
		got, err := s.store.row(context.Background(), id)
		if err == nil && got.State == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("row %s: %+v, %v; want %s", id, got, err, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
