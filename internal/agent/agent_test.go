package agent

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/aca/acatest"
	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/db/dbtest"
	"github.com/srjn45/wdx/internal/holder/holdertest"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/screen"
	"github.com/srjn45/wdx/internal/sqlite"
	"github.com/srjn45/wdx/internal/terminal"
	"github.com/srjn45/wdx/internal/waittest"
)

func TestMain(m *testing.M) {
	holdertest.RunIfHolder()
	os.Exit(runtest.Main(m))
}

type fixture struct {
	db        *sql.DB
	aca       *acatest.ACA
	bare      *acatest.Bare // known too, and what new agents run in newBareFixture's
	terminals *terminal.Supervisor
	agents    *Manager
	backups   *Backups
	project   string
	dir       string
	home      string
}

// newFixture returns agents that run script, on real holders.
func newFixture(t *testing.T, script string) fixture {
	t.Helper()
	return newFixtureOf(t, script, false)
}

// newBareFixture is newFixture whose new agents run an ACA with only stubs.
func newBareFixture(t *testing.T) fixture {
	t.Helper()
	return newFixtureOf(t, "", true)
}

func newFixtureOf(t *testing.T, script string, bare bool) fixture {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	dbtest.Seed(t, path)
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	// Holder sockets live under home: a path in t.TempDir can be too long.
	home := runtest.Dir(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO projects (id, name, state, created_at, updated_at) VALUES ('proj-app', 'app', 'open', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	test := &acatest.ACA{Launches: filepath.Join(t.TempDir(), "launches"), Notes: filepath.Join(t.TempDir(), "notes"), Kept: filepath.Join(t.TempDir(), "kept"), Script: script}
	stubs := &acatest.Bare{Name: "bare"}
	known := []aca.AiCodingAgent{test, stubs}
	if bare {
		known = []aca.AiCodingAgent{stubs, test} // new agents run the first
	}
	store := NewStore(d)
	backups := NewBackups(ctx, store, home, NewACAs(known, acatest.LookProgram, time.Nanosecond), 0)
	terminals := terminal.NewSupervisor(ctx, terminal.NewStore(d), terminal.Options{
		Executable: exe, Home: home, Env: []string{"PATH=/usr/bin:/bin"}, Shell: runtest.LoginShell(t),
		StopReport: holdertest.Patience(t), Limits: holdertest.Limits(t),
		AgentLaunch: Launches(backups),
	})
	agents := NewManager(store, terminals, backups)
	t.Cleanup(func() {
		list, _ := store.List(ctx, "")
		for _, a := range list {
			_, _, _ = agents.Remove(ctx, a.ID, true) // no holder is left to fail the test: each is stopped, or killed past the grace
		}
		terminals.Close()
		backups.Close()
	})
	return fixture{db: d, aca: test, bare: stubs, terminals: terminals, agents: agents, backups: backups, project: "proj-app", dir: t.TempDir(), home: home}
}

func (f fixture) create(t *testing.T, name string) Agent {
	t.Helper()
	a, err := f.agents.Create(context.Background(), f.project, name, f.dir, "", Launch{})
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

func TestRename(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "")
	ctx := context.Background()
	parent := f.create(t, "parent")
	child := f.createChild(t, "child", parent.ID)
	if child.Parent != "parent" {
		t.Fatalf("parent name %q", child.Parent)
	}
	if parent.Parent != "" {
		t.Fatalf("a top-level agent has parent %q", parent.Parent)
	}

	renamed, err := f.agents.Rename(ctx, parent.ID, "elder")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Terminal.Name != "elder" || renamed.SessionID != parent.SessionID || renamed.Terminal.PID != parent.Terminal.PID || renamed.Terminal.State != terminal.StateRunning {
		t.Fatalf("renamed: %+v", renamed)
	}
	if got, err := f.agents.Store().Get(ctx, child.ID); err != nil || got.Parent != "elder" {
		t.Fatalf("the child's parent name: %+v, %v", got, err)
	}

	if _, _, err := f.agents.Remove(ctx, parent.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.agents.Rename(ctx, parent.ID, "ghost"); !isRemoved(err) {
		t.Fatalf("want a tombstone refused, got %v", err)
	}
	if _, err := f.agents.Rename(ctx, "agent-none", "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}

func isRemoved(err error) bool {
	_, ok := errors.AsType[*RemovedError](err)
	return ok
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

// TestEveryStartGivesTheNote: the first start and the next, as what the ACA does with
// it on a resume is its own.
func TestEveryStartGivesTheNote(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "exit 0")
	a := f.create(t, "helper")
	f.waitState(t, a.ID, terminal.StateExited)
	if _, _, err := f.agents.Start(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	f.waitState(t, a.ID, terminal.StateExited)
	notes, err := f.aca.NotesGiven()
	if err != nil {
		t.Fatal(err)
	}
	if note := LaunchNote(); !slices.Equal(notes, []string{note, note}) {
		t.Fatalf("notes = %q, want the launch note twice", notes)
	}
}

// TestEveryStartKeepsTheModelAndPermissions: a restart, as the daemon restarting does,
// goes through the same launch.
func TestEveryStartKeepsTheModelAndPermissions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "exit 0")
	a, err := f.agents.Create(ctx, f.project, "helper", f.dir, "", Launch{Model: "claude-opus-4-8[effort=high]", Permissions: aca.PermissionsAsk})
	if err != nil {
		t.Fatal(err)
	}
	if a.RequestedModel != "claude-opus-4-8[effort=high]" || a.Permissions != aca.PermissionsAsk {
		t.Errorf("created with %q %q", a.RequestedModel, a.Permissions)
	}
	f.waitState(t, a.ID, terminal.StateExited)
	if _, _, err := f.agents.Start(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	f.waitState(t, a.ID, terminal.StateExited)
	kept, err := f.aca.KeptGiven()
	if err != nil {
		t.Fatal(err)
	}
	if want := "claude-opus-4-8[effort=high] ask"; !slices.Equal(kept, []string{want, want}) {
		t.Fatalf("starts were given %q, want %q twice", kept, want)
	}
}

// TestCreateAllowsEverythingByDefault: with no model, the ACA's own.
func TestCreateAllowsEverythingByDefault(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "exit 0")
	a := f.create(t, "helper")
	if a.RequestedModel != "" || a.Permissions != aca.PermissionsFull {
		t.Errorf("created with %q %q", a.RequestedModel, a.Permissions)
	}
	f.waitState(t, a.ID, terminal.StateExited)
	if kept, err := f.aca.KeptGiven(); err != nil || !slices.Equal(kept, []string{" full"}) {
		t.Fatalf("start was given %q, %v", kept, err)
	}
}

func TestCreateRefusesAModelNoACANames(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "")
	for _, model := range []string{"opus 4", "opus\n", "a\x00b", strings.Repeat("m", 201)} {
		if _, err := f.agents.Create(context.Background(), f.project, "", f.dir, "", Launch{Model: model}); !errors.Is(err, ErrInvalidModel) {
			t.Errorf("model %q: %v", model, err)
		}
	}
	if list, err := f.agents.Store().List(context.Background(), ""); err != nil || len(list) != 0 {
		t.Errorf("agents = %v, %v", list, err)
	}
}

// TestLaunchNoteNamesTheManual: the note's one command, which the cli's help tests keep.
func TestLaunchNoteNamesTheManual(t *testing.T) {
	t.Parallel()
	note := LaunchNote()
	for _, want := range []string{"`" + identity.Name + " help agents`", identity.Env("AGENT_ID")} {
		if !strings.Contains(note, want) {
			t.Errorf("note %q does not name %q", note, want)
		}
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
	again, _, err := f.agents.Start(ctx, a.ID)
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
	_, _, err := f.agents.Start(context.Background(), a.ID)
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
	again, _, err := f.agents.Start(ctx, a.ID)
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
	_, _, err := f.agents.Remove(ctx, a.ID, false)
	if e, ok := errors.AsType[*RunningError](err); !ok || e.Agent.ID != a.ID {
		t.Fatalf("Remove without stopRunning: %v", err)
	}
	removed, _, err := f.agents.Remove(ctx, a.ID, true)
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
	_, _, err := f.agents.Start(ctx, a.ID)
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
	a, err := f.agents.Create(context.Background(), f.project, name, f.dir, parentID, Launch{})
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
	if _, err := f.agents.Create(ctx, f.project, "x", f.dir, "agent-none", Launch{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown parent: %v", err)
	}
	if _, err := f.db.Exec(`INSERT INTO projects (id, name, state, created_at, updated_at) VALUES ('proj-lib', 'lib', 'open', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.agents.Create(ctx, "proj-lib", "x", f.dir, parent.ID, Launch{}); !errors.Is(err, ErrNotFound) {
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

	if _, _, err := f.agents.Remove(ctx, top.ID, false); !isRunning(err) {
		t.Fatalf("remove live parent: %v", err)
	}
	got, _, err := f.agents.Remove(ctx, top.ID, true)
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
		"start":  third(f.agents.Start(ctx, top.ID)),
		"remove": third(f.agents.Remove(ctx, top.ID, true)),
		"child":  second(f.agents.Create(ctx, f.project, "x", f.dir, top.ID, Launch{})),
	} {
		if _, ok := errors.AsType[*RemovedError](err); !ok {
			t.Errorf("%s of a tombstone: %v", what, err)
		}
	}
	// The name stays taken.
	if _, err := f.agents.Create(ctx, f.project, "top", f.dir, "", Launch{}); err == nil {
		t.Fatal("want the name taken")
	}

	if _, _, err := f.agents.Remove(ctx, middle.ID, true); err != nil {
		t.Fatal(err)
	}
	if a, err := f.agents.Store().Get(ctx, top.ID); err != nil || !a.Tombstone() {
		t.Fatalf("top with a tombstone under it: %+v, %v", a, err)
	}
	if _, _, err := f.agents.Remove(ctx, leaf.ID, true); err != nil {
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
	if _, _, err := f.agents.Remove(ctx, child.ID, true); err != nil {
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
	got, _, err := f.agents.Remove(ctx, parent.ID, false)
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
	if _, _, err := f.agents.Remove(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}
	b := f.create(t, "over")
	if _, err := f.db.Exec("UPDATE agents SET removed_at = 1 WHERE id = ?", b.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.agents.Remove(ctx, b.ID, true); err != nil {
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

func third[T, U any](_ T, _ U, err error) error { return err }

// TestSetStatus: a change is told apart from a repeat, a value this daemon does
// not know reads unknown, and only a running agent shows any.
func TestSetStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "")
	a := f.create(t, "helper")
	store := f.agents.Store()
	set := func(activity string, draft bool, queued int, wantChanged bool) Agent {
		t.Helper()
		changed, err := store.SetStatus(ctx, a.Terminal.ID, activity, draft, queued, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if changed != wantChanged {
			t.Errorf("SetStatus(%s, %v) changed = %v", activity, draft, changed)
		}
		got, err := store.Get(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := set("waiting", false, 0, true); got.Activity != "waiting" || got.Draft {
		t.Errorf("after waiting: %q %v", got.Activity, got.Draft)
	}
	set("waiting", false, 0, false)
	if got := set("waiting", true, 0, true); !got.Draft {
		t.Error("the draft not kept")
	}
	if got := set("waiting", true, 2, true); got.Queued != 2 {
		t.Errorf("the queue's length not kept: %d", got.Queued)
	}
	setModel := func(model string, wantChanged bool) string {
		t.Helper()
		changed, err := store.SetStatus(ctx, a.Terminal.ID, "waiting", true, 2, model, nil)
		if err != nil {
			t.Fatal(err)
		}
		if changed != wantChanged {
			t.Errorf("SetStatus(model %q) changed = %v", model, changed)
		}
		got, err := store.Get(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Model
	}
	if got := setModel("Haiku 5.5", true); got != "Haiku 5.5" {
		t.Errorf("the model not kept: %q", got)
	}
	if got := setModel("Haiku 5.5", false); got != "Haiku 5.5" {
		t.Errorf("the model after a repeat: %q", got)
	}
	if got := setModel("", true); got != "" {
		t.Errorf("a holder that has read none, as after a relaunch, leaves %q", got)
	}
	if got := set("limited", true, 2, true); got.Activity != "unknown" {
		t.Errorf("a value not known reads %q", got.Activity)
	}

	prompt := &screen.Prompt{Kind: screen.PromptDecision, Title: "Fruits", Question: "Which fruits?", Options: []string{"Apple", "Pear"}, Selected: 0, Multi: true, Ticked: []int{1}}
	setPrompt := func(activity string, p *screen.Prompt, wantChanged bool) *screen.Prompt {
		t.Helper()
		changed, err := store.SetStatus(ctx, a.Terminal.ID, activity, true, 2, "", p)
		if err != nil {
			t.Fatal(err)
		}
		if changed != wantChanged {
			t.Errorf("SetStatus(%s, prompt %v) changed = %v", activity, p != nil, changed)
		}
		got, err := store.Get(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Prompt
	}
	if got := setPrompt("waiting", prompt, true); !got.Equal(prompt) {
		t.Errorf("the prompt not kept: %+v", got)
	}
	setPrompt("waiting", prompt, false)
	moved := *prompt
	moved.Selected = 1
	if got := setPrompt("waiting", &moved, true); got == nil || got.Selected != 1 {
		t.Errorf("the selection moving is no change: %+v", got)
	}
	if got := setPrompt("waiting", nil, true); got != nil {
		t.Errorf("a prompt no longer read stays: %+v", got)
	}
	if got := setPrompt("working", prompt, true); got != nil {
		t.Errorf("a prompt is kept with an activity other than waiting: %+v", got)
	}
	setPrompt("waiting", prompt, true)
	if changed, err := store.ClearPrompt(ctx, a.Terminal.ID); err != nil || !changed {
		t.Errorf("clearing the prompt: changed %v, %v", changed, err)
	}
	if got, err := store.Get(ctx, a.ID); err != nil || got.Prompt != nil || got.Activity != "waiting" {
		t.Errorf("after clearing: %+v, %v", got.Prompt, err)
	}
	if changed, err := store.ClearPrompt(ctx, a.Terminal.ID); err != nil || changed {
		t.Errorf("clearing again: changed %v, %v", changed, err)
	}

	if _, _, err := f.agents.Remove(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}
	if changed, err := store.SetStatus(ctx, "term-none", "idle", false, 0, "", nil); err != nil || changed {
		t.Errorf("no agent: changed %v, %v", changed, err)
	}
}

// TestStatusOnlyWhileRunning: what was recorded does not show once the process
// ends, with nothing done about it.
func TestStatusOnlyWhileRunning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "until [ -f end ]; do sleep 0.01; done; exit 0")
	a := f.create(t, "helper")
	if _, err := f.agents.Store().SetStatus(ctx, a.Terminal.ID, "waiting", true, 3, "", &screen.Prompt{Kind: screen.PromptYesNo, Question: "Do you want to proceed?", Options: []string{"Yes", "No"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, "end"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := f.waitState(t, a.ID, terminal.StateExited); got.Activity != "" || got.Draft || got.Queued != 0 || got.Prompt != nil {
		t.Errorf("an exited agent shows %q, draft %v, queued %d, prompt %+v", got.Activity, got.Draft, got.Queued, got.Prompt)
	}
}

// setIdle has the agent read as idle, as its holder tells of a screen the test ACA's
// shell does not draw.
func (f fixture) setIdle(t *testing.T, a Agent, draft bool) {
	t.Helper()
	if _, err := f.agents.Store().SetStatus(context.Background(), a.Terminal.ID, "idle", draft, 0, "", nil); err != nil {
		t.Fatal(err)
	}
}

func TestRestartWaitsForAnIdleAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "")
	a := f.create(t, "helper")
	if _, _, err := f.agents.Restart(ctx, a.ID, false); !isBusy(err) {
		t.Fatalf("activity %s: %v", a.Activity, err)
	}
	f.setIdle(t, a, true)
	if _, _, err := f.agents.Restart(ctx, a.ID, false); !isBusy(err) {
		t.Fatalf("a draft: %v", err)
	}
	f.setIdle(t, a, false)
	again, _, err := f.agents.Restart(ctx, a.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if again.Terminal.State != terminal.StateRunning || again.SessionID == a.SessionID {
		t.Errorf("restarted = %+v", again)
	}
	forced, _, err := f.agents.Restart(ctx, a.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if forced.SessionID == again.SessionID {
		t.Errorf("forced = %+v", forced)
	}
}

func isBusy(err error) bool {
	_, ok := errors.AsType[*BusyError](err)
	return ok
}

// The new session survives what a backup says: a later start goes on in it, however the
// old session was what the ACA last said.
func TestARestartBeginsASessionThatSticks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "exit 0")
	f.aca.Session = filepath.Join(t.TempDir(), "session")
	a := f.create(t, "helper")
	if err := os.WriteFile(f.aca.Session, []byte(a.SessionID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.waitState(t, a.ID, terminal.StateExited)
	again, _, err := f.agents.Restart(ctx, a.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != a.ID || again.SessionID == a.SessionID {
		t.Fatalf("restarted = %+v", again)
	}
	f.waitState(t, a.ID, terminal.StateExited)
	later, _, err := f.agents.Start(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if later.SessionID != again.SessionID {
		t.Errorf("a later start is in %s, not %s", later.SessionID, again.SessionID)
	}
	f.waitState(t, a.ID, terminal.StateExited)
	sessions, err := f.aca.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sessions, []string{a.SessionID, again.SessionID, again.SessionID}) {
		t.Errorf("sessions = %q", sessions)
	}
}

// A session at work outside the terminal counts as busy; force, or its being idle, stops
// it and says so.
func TestARestartStopsWhatRunsOutsideTheTerminal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "exit 0")
	a := f.create(t, "helper")
	f.waitState(t, a.ID, terminal.StateExited)

	f.aca.Running = []aca.DetachedSession{{ID: "bg", Session: "s-bg", Working: true}}
	var busy *BusyError
	if _, _, err := f.agents.Restart(ctx, a.ID, false); !errors.As(err, &busy) || busy.Detached != "s-bg" {
		t.Fatalf("restart with a working session = %v", err)
	}
	if len(f.aca.Stopped) != 0 {
		t.Errorf("stopped %q when refusing", f.aca.Stopped)
	}

	f.aca.Running[0].Working = false
	_, stopped, err := f.agents.Restart(ctx, a.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stopped, []string{"s-bg"}) || !slices.Equal(f.aca.Stopped, []string{"s-bg"}) {
		t.Errorf("stopped = %q, the ACA saw %q", stopped, f.aca.Stopped)
	}
	f.waitState(t, a.ID, terminal.StateExited)

	f.aca.Running = []aca.DetachedSession{{ID: "bg2", Session: "s-bg2", Working: true}}
	if _, stopped, err = f.agents.Restart(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stopped, []string{"s-bg2"}) {
		t.Errorf("forced: stopped = %q", stopped)
	}
}

// Starting an agent goes on with a conversation that a session outside the terminal still
// holds, so that session stops first; at work, it is refused, stopping nothing.
func TestStartStopsWhatRunsOutsideTheTerminal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "exit 0")
	a := f.create(t, "helper")
	f.waitState(t, a.ID, terminal.StateExited)

	f.aca.Running = []aca.DetachedSession{{ID: "bg", Session: "s-bg", Working: true}}
	var busy *BusyError
	if _, _, err := f.agents.Start(ctx, a.ID); !errors.As(err, &busy) || busy.Detached != "s-bg" {
		t.Fatalf("start with a working session = %v", err)
	}
	if len(f.aca.Stopped) != 0 {
		t.Errorf("stopped %q when refusing", f.aca.Stopped)
	}

	f.aca.Running[0].Working = false
	_, stopped, err := f.agents.Start(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stopped, []string{"s-bg"}) || !slices.Equal(f.aca.Stopped, []string{"s-bg"}) {
		t.Errorf("stopped = %q, the ACA saw %q", stopped, f.aca.Stopped)
	}
}

// A start refused as the agent is live stops nothing: it goes on working where it is.
func TestStartOfALiveAgentStopsNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "")
	a := f.create(t, "helper")
	f.aca.Running = []aca.DetachedSession{{ID: "bg", Session: "s-bg"}}
	var running *RunningError
	if _, _, err := f.agents.Start(context.Background(), a.ID); !errors.As(err, &running) {
		t.Fatalf("start = %v", err)
	}
	if len(f.aca.Stopped) != 0 {
		t.Errorf("stopped %q", f.aca.Stopped)
	}
}

func TestRemoveStopsWhatRunsOutsideTheTerminal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("a live agent's removal refused stops nothing", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, "")
		a := f.create(t, "helper")
		f.aca.Running = []aca.DetachedSession{{ID: "bg", Session: "s-bg"}}
		var running *RunningError
		if _, _, err := f.agents.Remove(ctx, a.ID, false); !errors.As(err, &running) {
			t.Fatalf("remove = %v", err)
		}
		if len(f.aca.Stopped) != 0 {
			t.Errorf("stopped %q", f.aca.Stopped)
		}
		_, stopped, err := f.agents.Remove(ctx, a.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(stopped, []string{"s-bg"}) {
			t.Errorf("stopped = %q", stopped)
		}
	})

	t.Run("one at work is the same question", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, "exit 0")
		a := f.create(t, "helper")
		f.waitState(t, a.ID, terminal.StateExited)
		f.aca.Running = []aca.DetachedSession{{ID: "bg", Session: "s-bg", Working: true}}
		var running *RunningError
		if _, _, err := f.agents.Remove(ctx, a.ID, false); !errors.As(err, &running) || running.Detached != "s-bg" {
			t.Fatalf("remove = %v", err)
		}
		if len(f.aca.Stopped) != 0 {
			t.Errorf("stopped %q when refusing", f.aca.Stopped)
		}
		if _, stopped, err := f.agents.Remove(ctx, a.ID, true); err != nil || !slices.Equal(stopped, []string{"s-bg"}) {
			t.Errorf("stopped = %q, %v", stopped, err)
		}
	})

	t.Run("one idle goes with an ended agent", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, "exit 0")
		a := f.create(t, "helper")
		f.waitState(t, a.ID, terminal.StateExited)
		f.aca.Running = []aca.DetachedSession{{ID: "bg", Session: "s-bg"}}
		if _, stopped, err := f.agents.Remove(ctx, a.ID, false); err != nil || !slices.Equal(stopped, []string{"s-bg"}) {
			t.Errorf("stopped = %q, %v", stopped, err)
		}
	})

	t.Run("a tombstone's too", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, "exit 0")
		parent := f.create(t, "parent")
		if _, err := f.agents.Create(ctx, f.project, "child", f.dir, parent.ID, Launch{}); err != nil {
			t.Fatal(err)
		}
		f.waitState(t, parent.ID, terminal.StateExited)
		f.aca.Running = []aca.DetachedSession{{ID: "bg", Session: "s-bg"}}
		if _, stopped, err := f.agents.Remove(ctx, parent.ID, false); err != nil || !slices.Equal(stopped, []string{"s-bg"}) {
			t.Errorf("stopped = %q, %v", stopped, err)
		}
	})
}

// messagesOf is what waits for the agent, in the order it goes.
func (f fixture) messagesOf(t *testing.T, a Agent) []terminal.Message {
	t.Helper()
	list, err := f.terminals.Store().Undelivered(context.Background(), a.Terminal.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// What waits goes on to the new session behind the first message, which holds the way to
// the history; what is held stays for a human. A message pending for a holder that is gone
// would be held, but for the restart.
func TestARestartCarriesWhatIsPendingBehindItsFirstMessage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "exit 0")
	a := f.create(t, "helper")
	f.waitState(t, a.ID, terminal.StateExited)
	insert := func(id string, instant int, state string, holderPID any) {
		t.Helper()
		if _, err := f.db.Exec(`INSERT INTO messages (id, terminal_id, text, instant, state, session_id, holder_pid, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, 1000)`,
			id, a.Terminal.ID, id, instant, state, a.SessionID, holderPID); err != nil {
			t.Fatal(err)
		}
	}
	insert("msg-general", 0, "pending", 1)
	insert("msg-instant", 1, "pending", 1)
	insert("msg-held", 0, "held", 1)

	again, _, err := f.agents.Restart(ctx, a.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	f.waitState(t, a.ID, terminal.StateExited)
	var first, general, instant, held terminal.Message
	list := f.messagesOf(t, a)
	for _, m := range list {
		switch m.ID {
		case "msg-general":
			general = m
		case "msg-instant":
			instant = m
		case "msg-held":
			held = m
		default:
			first = m
		}
	}
	if len(list) != 4 || list[0].ID != first.ID {
		t.Fatalf("messages = %+v", list)
	}
	if !first.Instant || !strings.Contains(first.Text, "wdx agent transcript --id "+a.ID+" --brief") || first.SessionID != again.SessionID || !first.CreatedAt.Before(general.CreatedAt) {
		t.Errorf("first = %+v", first)
	}
	if general.SessionID != again.SessionID || instant.SessionID != again.SessionID {
		t.Errorf("not moved: %+v, %+v", general, instant)
	}
	if instant.Instant {
		t.Errorf("an instant one would interrupt the brief: %+v", instant)
	}
	if held.State != terminal.MessageHeld || held.SessionID != a.SessionID {
		t.Errorf("held = %+v", held)
	}
	if strings.Contains(first.Text, "no longer exists") {
		t.Errorf("first = %q", first.Text)
	}

	// The next restart replaces a first message that has not gone.
	if _, _, err := f.agents.Restart(ctx, a.ID, false); err != nil {
		t.Fatal(err)
	}
	next := f.messagesOf(t, a)
	if len(next) != 4 || next[0].ID == first.ID {
		t.Errorf("after a second restart: %+v", next)
	}
}

// A relaunch runs where the agent last worked if it can, or a conversation in a worktree
// would wake up in another checkout; and a restart says when it cannot.
func TestARelaunchRunsWhereTheAgentLastWorked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "")
	where := filepath.Join(t.TempDir(), "where")
	f.aca.Script = "pwd >> " + where + "; exit 0"
	f.aca.Dir = filepath.Join(t.TempDir(), "dir")
	a := f.create(t, "helper")
	f.waitState(t, a.ID, terminal.StateExited)
	worktree := t.TempDir()
	if err := os.WriteFile(f.aca.Dir, []byte(worktree+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.agents.Start(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	f.waitState(t, a.ID, terminal.StateExited)
	if _, _, err := f.agents.Restart(ctx, a.ID, false); err != nil {
		t.Fatal(err)
	}
	f.waitState(t, a.ID, terminal.StateExited)
	if list := f.messagesOf(t, a); len(list) != 1 || strings.Contains(list[0].Text, "no longer exists") {
		t.Errorf("messages = %+v", list)
	}

	if err := os.RemoveAll(worktree); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.agents.Restart(ctx, a.ID, false); err != nil {
		t.Fatal(err)
	}
	f.waitState(t, a.ID, terminal.StateExited)
	list := f.messagesOf(t, a)
	if len(list) != 1 || !strings.Contains(list[0].Text, "You were last in "+worktree+", which no longer exists; you are in "+f.dir+".") {
		t.Errorf("messages = %+v", list)
	}

	data, err := os.ReadFile(where)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(string(data))
	if want := []string{f.dir, worktree, worktree, f.dir}; !slices.Equal(got, want) {
		t.Errorf("ran in %q, want %q", got, want)
	}
}

func TestRestartOfATombstoneOrAFailedLaunch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "exit 0")
	parent := f.create(t, "parent")
	f.createChild(t, "child", parent.ID)
	if _, _, err := f.agents.Remove(ctx, parent.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.agents.Restart(ctx, parent.ID, true); !isRemoved(err) {
		t.Errorf("tombstone: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "going")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	a, err := f.agents.Create(ctx, f.project, "helper", dir, "", Launch{})
	if err != nil {
		t.Fatal(err)
	}
	f.waitState(t, a.ID, terminal.StateExited)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	_, _, err = f.agents.Restart(ctx, a.ID, false)
	failed, ok := errors.AsType[*StartError](err)
	if !ok || failed.Agent.Terminal.State != terminal.StateFailed || failed.Agent.SessionID == a.SessionID {
		t.Fatalf("failed launch: %v %+v", err, failed)
	}
	if list := f.messagesOf(t, a); len(list) != 1 || !strings.Contains(list[0].Text, "--brief") {
		t.Errorf("its first message waits: %+v", list)
	}
	if _, _, err := f.agents.Restart(ctx, a.ID, false); !errors.As(err, &failed) {
		t.Fatal(err)
	}
	if list := f.messagesOf(t, a); len(list) != 1 {
		t.Errorf("two first messages: %+v", list)
	}
}

// TestNoteNotLogged: a launch's log line keeps the command short.
func TestNoteNotLogged(t *testing.T) {
	t.Parallel()
	got := withoutNote([]string{"claude", "--append-system-prompt", "use wdx"}, "use wdx")
	if want := []string{"claude", "--append-system-prompt", "<launch note>"}; !slices.Equal(got, want) {
		t.Errorf("logged = %q, want %q", got, want)
	}
}

// readyThenRecord draws Claude Code's empty prompt, so that the holder delivers, then
// writes what it is sent to the file got.
const readyThenRecord = `rule=────────────────
stty raw -echo
printf '\033[2J\033[H%s\r\n❯ \r\n%s\r\n  ⏸ manual mode on · ? for shortcuts' "$rule" "$rule"
exec cat > got
`

// An agent created linked is told its first message, and what points at it is written
// with it; when that fails, nothing of it is left.
func TestCreateLinked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "")
	if err := os.WriteFile(filepath.Join(f.dir, "run"), []byte(readyThenRecord), 0o600); err != nil {
		t.Fatal(err)
	}
	var linkedID string
	a, err := f.agents.CreateLinked(ctx, f.project, "app-schema", f.dir, Launch{}, "do the schema", func(ctx context.Context, tx sqlite.Execer, agentID string) error {
		linkedID = agentID
		_, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at = 7 WHERE id = ?`, f.project)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if linkedID != a.ID || a.ParentID != "" || a.Terminal.Name != "app-schema" {
		t.Errorf("created %+v, linked %q", a, linkedID)
	}
	var updated int64
	if err := f.db.QueryRow(`SELECT updated_at FROM projects WHERE id = ?`, f.project).Scan(&updated); err != nil || updated != 7 {
		t.Errorf("linked not written: %d, %v", updated, err)
	}
	var got string
	waittest.For(t, "the first message to arrive", func() bool {
		data, err := os.ReadFile(filepath.Join(f.dir, "got"))
		got = string(data)
		return err == nil && strings.Contains(got, "\r")
	})
	if !strings.Contains(got, "do the schema") {
		t.Errorf("arrived as %q", got)
	}

	_, err = f.agents.CreateLinked(ctx, f.project, "app-api", f.dir, Launch{}, "do the api", func(ctx context.Context, tx sqlite.Execer, agentID string) error {
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at = 8 WHERE id = ?`, f.project); err != nil {
			return err
		}
		return errors.New("refused")
	})
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("linked failing: %v", err)
	}
	if _, err := f.agents.Store().GetByName(ctx, f.project, "app-api"); !errors.Is(err, ErrNotFound) {
		t.Errorf("the agent was left: %v", err)
	}
	var messages int
	if err := f.db.QueryRow(`SELECT (SELECT count(*) FROM messages WHERE text = 'do the api') + (SELECT updated_at FROM projects WHERE id = ?)`, f.project).Scan(&messages); err != nil || messages != 7 {
		t.Errorf("its message or linked change was left: %d, %v", messages, err)
	}
}

// An agent stopped on its own ends as one that exited, so that its project opening
// does not start it again, and keeps its session.
func TestStopIsNotRelaunchedWithTheProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "")
	a := f.create(t, "helper")
	other := f.create(t, "other")
	got, err := f.agents.Stop(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Terminal.State != terminal.StateExited || got.Terminal.PID != 0 || got.SessionID != a.SessionID {
		t.Errorf("stopped: %+v", got)
	}
	if again, err := f.agents.Stop(ctx, a.ID); err != nil || again.Terminal.State != terminal.StateExited {
		t.Errorf("stopped again: %+v, %v", again, err)
	}
	if _, err := f.terminals.StopProject(ctx, f.project); err != nil {
		t.Fatal(err)
	}
	if _, err := f.terminals.StartProject(ctx, f.project); err != nil {
		t.Fatal(err)
	}
	if got, err := f.agents.Store().Get(ctx, a.ID); err != nil || got.Terminal.State != terminal.StateExited {
		t.Errorf("relaunched with its project: %+v, %v", got, err)
	}
	if got, err := f.agents.Store().Get(ctx, other.ID); err != nil || got.Terminal.State != terminal.StateRunning {
		t.Errorf("the other: %+v, %v", got, err)
	}
	if started, _, err := f.agents.Start(ctx, a.ID); err != nil || started.Terminal.State != terminal.StateRunning {
		t.Errorf("started again by hand: %+v, %v", started, err)
	}
	if _, err := f.terminals.StopProject(ctx, f.project); err != nil {
		t.Fatal(err)
	}
	if got, err := f.agents.Stop(ctx, other.ID); err != nil || got.Terminal.State != terminal.StateExited {
		t.Errorf("stopped once stopped with its project: %+v, %v", got, err)
	}
	if _, err := f.terminals.StartProject(ctx, f.project); err != nil {
		t.Fatal(err)
	}
	if got, err := f.agents.Store().Get(ctx, other.ID); err != nil || got.Terminal.State != terminal.StateExited {
		t.Errorf("relaunched once stopped: %+v, %v", got, err)
	}
	if _, err := f.agents.Stop(ctx, "agent-none"); !errors.Is(err, ErrNotFound) {
		t.Errorf("an agent it does not have: %v", err)
	}
}

// The first message waits for the agent in its session, instant, as a restart's does,
// so that nothing sent later overtakes it.
func TestCreateLinkedFirstMessageWaits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "")
	a, err := f.agents.CreateLinked(ctx, f.project, "app-schema", f.dir, Launch{}, "do the schema", nil)
	if err != nil {
		t.Fatal(err)
	}
	list := f.messagesOf(t, a)
	if len(list) != 1 || list[0].Text != "do the schema" || !list[0].Instant || list[0].SessionID != a.SessionID || list[0].TerminalID != a.Terminal.ID {
		t.Errorf("messages = %+v", list)
	}
	plain := f.create(t, "plain")
	for _, m := range f.messagesOf(t, plain) {
		if m.TerminalID == plain.Terminal.ID {
			t.Errorf("a plain create has a first message: %+v", m)
		}
	}
}
