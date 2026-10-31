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
	"github.com/srjn45/wdx/internal/identity"
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
	home      string
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
		AgentLaunch: Launches(store, []aca.AiCodingAgent{test}),
	})
	agents := NewManager(store, terminals, test)
	t.Cleanup(func() {
		list, _ := store.List(ctx, "")
		for _, a := range list {
			_, _ = agents.Remove(ctx, a.ID, true) // no holder is left to fail the test: each is stopped, or killed past the grace
		}
		terminals.Close()
	})
	return fixture{db: d, aca: test, terminals: terminals, agents: agents, project: "proj-app", dir: t.TempDir(), home: home}
}

func (f fixture) create(t *testing.T, name string) Agent {
	t.Helper()
	a, err := f.agents.Create(context.Background(), f.project, name, f.dir, "")
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

func (f fixture) createChild(t *testing.T, name, parentID string) Agent {
	t.Helper()
	a, err := f.agents.Create(context.Background(), f.project, name, f.dir, parentID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (f fixture) gone(t *testing.T, id string) {
	t.Helper()
	if a, err := f.agents.Store().Get(context.Background(), id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("agent %s: %+v, %v, want it gone", id, a, err)
	}
}

func TestCreateChild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "")
	parent := f.create(t, "parent")
	child := f.createChild(t, "child", parent.ID)
	if parent.ParentID != "" || child.ParentID != parent.ID {
		t.Fatalf("parents: %q, %q", parent.ParentID, child.ParentID)
	}
	if _, err := f.agents.Create(ctx, f.project, "x", f.dir, "agent-none"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown parent: %v", err)
	}
	if _, err := f.db.Exec(`INSERT INTO projects (id, name, state, created_at, updated_at) VALUES ('proj-lib', 'lib', 'open', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.agents.Create(ctx, "proj-lib", "x", f.dir, parent.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("parent of another project: %v", err)
	}
}

// TestTombstone follows a parent removed before its children: it stays with
// its history while they run on, refuses what needs a live agent, and goes
// with its last child, up the tree.
func TestTombstone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "")
	top := f.create(t, "top")
	middle := f.createChild(t, "middle", top.ID)
	leaf := f.createChild(t, "leaf", middle.ID)
	kept := identity.TerminalDir(f.home, top.Terminal.ID)

	if _, err := f.agents.Remove(ctx, top.ID, false); !isRunning(err) {
		t.Fatalf("remove live parent: %v", err)
	}
	got, err := f.agents.Remove(ctx, top.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Tombstone() || got.Terminal.State != terminal.StateExited || got.Terminal.PID != 0 {
		t.Fatalf("tombstone: %+v", got)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("what it keeps on disk: %v", err)
	}
	if a, err := f.agents.Store().Get(ctx, middle.ID); err != nil || a.Terminal.State != terminal.StateRunning {
		t.Fatalf("its child: %+v, %v", a, err)
	}

	for what, err := range map[string]error{
		"start":  second(f.agents.Start(ctx, top.ID)),
		"remove": second(f.agents.Remove(ctx, top.ID, true)),
		"child":  second(f.agents.Create(ctx, f.project, "x", f.dir, top.ID)),
	} {
		if _, ok := errors.AsType[*RemovedError](err); !ok {
			t.Errorf("%s of a tombstone: %v", what, err)
		}
	}
	// The name stays taken.
	if _, err := f.agents.Create(ctx, f.project, "top", f.dir, ""); err == nil {
		t.Fatal("want the name taken")
	}

	if _, err := f.agents.Remove(ctx, middle.ID, true); err != nil {
		t.Fatal(err)
	}
	if a, err := f.agents.Store().Get(ctx, top.ID); err != nil || !a.Tombstone() {
		t.Fatalf("top with a tombstone under it: %+v, %v", a, err)
	}
	if _, err := f.agents.Remove(ctx, leaf.ID, true); err != nil {
		t.Fatal(err)
	}
	f.gone(t, leaf.ID)
	f.gone(t, middle.ID)
	f.gone(t, top.ID)
	if _, err := os.Stat(kept); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("what it kept on disk stayed: %v", err)
	}
}

// TestRemoveChildKeepsLiveParent: only a tombstone goes with its last child.
func TestRemoveChildKeepsLiveParent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "")
	parent := f.create(t, "parent")
	child := f.createChild(t, "child", parent.ID)
	if _, err := f.agents.Remove(ctx, child.ID, true); err != nil {
		t.Fatal(err)
	}
	if a, err := f.agents.Store().Get(ctx, parent.ID); err != nil || a.Tombstone() || a.Terminal.State != terminal.StateRunning {
		t.Fatalf("parent: %+v, %v", a, err)
	}
}

// TestTombstoneOfStoppedAgentIsNotRelaunched: an agent stopped with its
// project, removed, stays down when the project opens; its child comes back.
func TestTombstoneOfStoppedAgentIsNotRelaunched(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "")
	parent := f.create(t, "parent")
	child := f.createChild(t, "child", parent.ID)
	if _, err := f.terminals.StopProject(ctx, f.project); err != nil {
		t.Fatal(err)
	}
	got, err := f.agents.Remove(ctx, parent.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Terminal.State != terminal.StateExited {
		t.Fatalf("state %s", got.Terminal.State)
	}
	started, err := f.terminals.StartProject(ctx, f.project)
	if err != nil {
		t.Fatal(err)
	}
	if len(started) != 1 || started[0].ID != child.Terminal.ID {
		t.Fatalf("started %+v", started)
	}
}

// TestEmptyTombstoneCanBeRemoved: a daemon that died between deleting a child
// and its tombstone leaves one no child would ever take away.
func TestEmptyTombstoneCanBeRemoved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "")
	a := f.create(t, "left")
	if _, err := f.agents.Remove(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}
	b := f.create(t, "over")
	if _, err := f.db.Exec("UPDATE agents SET removed_at = 1 WHERE id = ?", b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.agents.Remove(ctx, b.ID, true); err != nil {
		t.Fatal(err)
	}
	f.gone(t, b.ID)
}

// TestAgentIDInEnvironment: what an agent passes to create a child of itself.
func TestAgentIDInEnvironment(t *testing.T) {
	t.Parallel()
	f := newFixture(t, `printf %s "$WDX_AGENT_ID" > agent-id; exec sleep 1000`)
	a := f.create(t, "helper")
	path := filepath.Join(f.dir, "agent-id")
	waittest.For(t, "the id written", func() bool {
		data, err := os.ReadFile(path)
		return err == nil && string(data) == a.ID
	})
}

func isRunning(err error) bool {
	_, ok := errors.AsType[*RunningError](err)
	return ok
}

func second[T any](_ T, err error) error { return err }
