package agent

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/aca/acatest"
	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/holder/holdertest"
	"github.com/srjn45/wdx/internal/terminal"
	"github.com/srjn45/wdx/internal/waittest"
)

func TestMain(m *testing.M) {
	holdertest.RunIfHolder()
	os.Exit(m.Run())
}

type fixture struct {
	db        *sql.DB
	aca       *acatest.ACA
	terminals *terminal.Supervisor
	agents    *Manager
	project   string
	dir       string
}

// newFixture returns agents that run script, on real holders.
func newFixture(t *testing.T, script string) fixture {
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
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO projects (id, name, state, created_at, updated_at) VALUES ('proj-app', 'app', 'open', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	test := &acatest.ACA{Launches: filepath.Join(t.TempDir(), "launches"), Script: script}
	store := NewStore(d)
	terminals := terminal.NewSupervisor(ctx, terminal.NewStore(d), terminal.Options{
		Executable: exe, Home: home, Env: []string{"PATH=/usr/bin:/bin"}, Shell: "/bin/sh",
		AgentCommand: Commands(store, []aca.AiCodingAgent{test}),
	})
	agents := NewManager(store, terminals, test)
	t.Cleanup(func() {
		list, _ := store.List(ctx, "")
		for _, a := range list {
			_, _ = agents.Remove(ctx, a.ID, true) // no holder is left to fail the test: each is stopped, or killed past the grace
		}
		terminals.Close()
	})
	return fixture{db: d, aca: test, terminals: terminals, agents: agents, project: "proj-app", dir: t.TempDir()}
}

func (f fixture) create(t *testing.T, name string) Agent {
	t.Helper()
	a, err := f.agents.Create(context.Background(), f.project, name, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (f fixture) waitState(t *testing.T, id string, want terminal.State) Agent {
	t.Helper()
	var got Agent
	waittest.For(t, "agent "+string(want), func() bool {
		var err error
		got, err = f.agents.Store().Get(context.Background(), id)
		return err == nil && got.Terminal.State == want
	})
	return got
}

func TestCreate(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "")
	a := f.create(t, "helper")
	if !regexp.MustCompile(`^agent-[0-9a-f]{8}$`).MatchString(a.ID) {
		t.Errorf("id = %q", a.ID)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(a.SessionID) {
		t.Errorf("session id = %q", a.SessionID)
	}
	if a.ACA != f.aca.ID() || a.Terminal.Kind != terminal.KindAgent || a.Terminal.Name != "helper" ||
		a.Terminal.State != terminal.StateRunning || a.Terminal.PID == 0 || a.Terminal.Dir != f.dir || a.Terminal.ProjectName != "app" {
		t.Errorf("agent = %+v", a)
	}
}

func TestCreateGeneratesAName(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "")
	if a := f.create(t, ""); a.Terminal.Name == "" {
		t.Fatalf("agent = %+v", a)
	}
}

func TestLookups(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "")
	a := f.create(t, "helper")
	if got, err := f.agents.Store().Get(ctx, a.ID); err != nil || got.Terminal.ID != a.Terminal.ID {
		t.Errorf("Get: %+v, %v", got, err)
	}
	if got, err := f.agents.Store().GetByName(ctx, f.project, "HELPER"); err != nil || got.ID != a.ID {
		t.Errorf("GetByName: %+v, %v", got, err)
	}
	if got, err := f.agents.Store().GetByTerminal(ctx, a.Terminal.ID); err != nil || got.ID != a.ID {
		t.Errorf("GetByTerminal: %+v, %v", got, err)
	}
	for _, project := range []string{f.project, ""} {
		if list, err := f.agents.Store().List(ctx, project); err != nil || len(list) != 1 || list[0].ID != a.ID {
			t.Errorf("List(%q): %+v, %v", project, list, err)
		}
	}
	if _, err := f.agents.Store().Get(ctx, "agent-nothing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get of nothing: %v", err)
	}
}

// TestAStartKeepsTheSession: the conversation comes back.
func TestAStartKeepsTheSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "exit 4")
	a := f.create(t, "helper")
	exited := f.waitState(t, a.ID, terminal.StateExited)
	if exited.Terminal.ExitCode == nil || *exited.Terminal.ExitCode != 4 {
		t.Fatalf("exit code = %v", exited.Terminal.ExitCode)
	}
	again, err := f.agents.Start(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != a.ID || again.SessionID != a.SessionID {
		t.Fatalf("started = %+v, created = %+v", again, a)
	}
	f.waitState(t, a.ID, terminal.StateExited)
	sessions, err := f.aca.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sessions, []string{a.SessionID, a.SessionID}) {
		t.Fatalf("sessions = %q, want %s twice", sessions, a.SessionID)
	}
}

func TestStartLive(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "")
	a := f.create(t, "helper")
	_, err := f.agents.Start(context.Background(), a.ID)
	if e, ok := errors.AsType[*RunningError](err); !ok || e.Agent.ID != a.ID {
		t.Fatalf("Start: %v", err)
	}
}

// TestLostAgentStartsAgain: a reboot leaves it lost, with its session.
func TestLostAgentStartsAgain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "exit 0")
	a := f.create(t, "helper")
	f.waitState(t, a.ID, terminal.StateExited)
	if _, err := f.db.Exec("UPDATE terminals SET state = 'lost' WHERE id = ?", a.Terminal.ID); err != nil {
		t.Fatal(err)
	}
	again, err := f.agents.Start(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.SessionID != a.SessionID {
		t.Fatalf("session = %q, want %q", again.SessionID, a.SessionID)
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "")
	a := f.create(t, "helper")
	_, err := f.agents.Remove(ctx, a.ID, false)
	if e, ok := errors.AsType[*RunningError](err); !ok || e.Agent.ID != a.ID {
		t.Fatalf("Remove without stopRunning: %v", err)
	}
	removed, err := f.agents.Remove(ctx, a.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if removed.ID != a.ID || removed.Terminal.State != terminal.StateStopped {
		t.Fatalf("removed = %+v", removed)
	}
	if _, err := f.agents.Store().Get(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after removing: %v", err)
	}
	var rows int
	if err := f.db.QueryRow("SELECT count(*) FROM terminals WHERE id = ?", a.Terminal.ID).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("the terminal row stayed: %d, %v", rows, err)
	}
}

func TestProjectRemovalCascades(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "exit 0")
	a := f.create(t, "helper")
	f.waitState(t, a.ID, terminal.StateExited)
	if _, err := f.db.Exec("DELETE FROM projects WHERE id = ?", f.project); err != nil {
		t.Fatal(err)
	}
	if _, err := f.agents.Store().Get(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after the project went: %v", err)
	}
	var rows int
	if err := f.db.QueryRow("SELECT count(*) FROM agents").Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("agents left: %d, %v", rows, err)
	}
}

func TestUnknownACAFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "exit 0")
	a := f.create(t, "helper")
	f.waitState(t, a.ID, terminal.StateExited)
	if _, err := f.db.Exec("UPDATE agents SET aca = 'gone' WHERE id = ?", a.ID); err != nil {
		t.Fatal(err)
	}
	_, err := f.agents.Start(ctx, a.ID)
	e, ok := errors.AsType[*StartError](err)
	if !ok {
		t.Fatalf("Start: %v", err)
	}
	if e.Agent.Terminal.State != terminal.StateFailed || e.Agent.Terminal.Error == "" {
		t.Fatalf("agent = %+v", e.Agent)
	}
}

func TestStopAndRelaunchWithTheProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "")
	a := f.create(t, "helper")
	if _, err := f.terminals.StopProject(ctx, f.project); err != nil {
		t.Fatal(err)
	}
	if got, err := f.agents.Store().Get(ctx, a.ID); err != nil || got.Terminal.State != terminal.StateStopped {
		t.Fatalf("after stopping: %+v, %v", got, err)
	}
	if _, err := f.terminals.StartProject(ctx, f.project); err != nil {
		t.Fatal(err)
	}
	if got, err := f.agents.Store().Get(ctx, a.ID); err != nil || got.Terminal.State != terminal.StateRunning {
		t.Fatalf("after relaunching: %+v, %v", got, err)
	}
	sessions, err := f.aca.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sessions, []string{a.SessionID, a.SessionID}) {
		t.Fatalf("sessions = %q", sessions)
	}
}
