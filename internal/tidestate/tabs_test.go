package tidestate

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/db/dbtest"
)

type fixture struct {
	db    *sql.DB
	store *Store
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	dbtest.Seed(t, path)
	d, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return fixture{db: d, store: NewStore(d)}
}

func (f fixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) project(t *testing.T, name string) string {
	t.Helper()
	id := "proj-" + name
	f.exec(t, `INSERT INTO projects (id, name, state, created_at, updated_at) VALUES (?, ?, 'open', 0, 0)`, id, name)
	return id
}

func (f fixture) agent(t *testing.T, projectID, name string) string {
	t.Helper()
	id := "agent-" + name
	f.exec(t, `INSERT INTO terminals (id, project_id, name, dir, shell, state, kind, created_at, updated_at)
		VALUES (?, ?, ?, '/r', 'sh', 'running', 'agent', 0, 0)`, "term-"+name, projectID, name)
	f.exec(t, `INSERT INTO agents (id, terminal_id, aca, session_id) VALUES (?, ?, 'claude', 's')`, id, "term-"+name)
	return id
}

func (f fixture) doc(t *testing.T, projectID, name string) string {
	t.Helper()
	id := "doc-" + name
	f.exec(t, `INSERT INTO docs (id, project_id, name, format, revision, created_at, updated_at) VALUES (?, ?, ?, 'md', 1, 0, 0)`, id, projectID, name)
	return id
}

func (f fixture) tabs(t *testing.T, projectID string) []Tab {
	t.Helper()
	tabs, err := f.store.Tabs(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	return tabs
}

func (f fixture) replace(t *testing.T, projectID string, tabs ...Tab) {
	t.Helper()
	if err := f.store.Replace(context.Background(), projectID, tabs); err != nil {
		t.Fatal(err)
	}
}

func fileTab(worktree, path string) Tab { return Tab{Kind: KindFile, Worktree: worktree, Path: path} }

// TestReplace: the tabs come back in the order given, and a list replaces the last
// whole, for its project alone.
func TestReplace(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p, other := f.project(t, "wdx"), f.project(t, "other")
	if got := f.tabs(t, p); got == nil || len(got) != 0 {
		t.Fatalf("none yet: %#v", got)
	}
	a, d := f.agent(t, p, "falcon"), f.doc(t, p, "plan")
	all := []Tab{
		{Kind: KindDoc, DocID: d},
		{Kind: KindAgent, AgentID: a},
		{Kind: KindInfo, AgentID: a},
		{Kind: KindSettings},
		fileTab("main", "go.mod"),
		{Kind: KindDiff, Worktree: "feat", Path: "a/b.go"},
		{Kind: KindPR, Worktree: "feat"},
	}
	f.replace(t, p, all...)
	f.replace(t, other, Tab{Kind: KindSettings})
	if got := f.tabs(t, p); !slices.Equal(got, all) {
		t.Fatalf("got %#v", got)
	}
	f.replace(t, p, all[4], all[0])
	if got := f.tabs(t, p); !slices.Equal(got, []Tab{all[4], all[0]}) {
		t.Fatalf("after a second list %#v", got)
	}
	if got := f.tabs(t, other); !slices.Equal(got, []Tab{{Kind: KindSettings}}) {
		t.Errorf("the other project's %#v", got)
	}
}

// TestReplaceRefused: a list with a tab that cannot be kept changes nothing.
func TestReplaceRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p, other := f.project(t, "wdx"), f.project(t, "other")
	theirs, theirDoc := f.agent(t, other, "owl"), f.doc(t, other, "notes")
	gone := f.agent(t, p, "tomb")
	f.exec(t, "UPDATE agents SET removed_at = 1 WHERE id = ?", gone)
	kept := []Tab{{Kind: KindSettings}}
	f.replace(t, p, kept...)
	for tab, why := range map[Tab]string{
		{Kind: "terminal"}:                          "not a kind of tab",
		{Kind: KindAgent}:                           "needs agent_id",
		{Kind: KindSettings, Path: "x"}:             "has no path",
		{Kind: KindPR, Worktree: "feat", Path: "x"}: "has no path",
		{Kind: KindFile, Path: "x"}:                 "needs worktree",
		fileTab("main", "/etc/passwd"):              "not a clean path",
		fileTab("main", "../x"):                     "not a clean path",
		fileTab("main", "a/../b"):                   "not a clean path",
		fileTab("main", "."):                        "not a clean path",
		{Kind: KindAgent, AgentID: theirs}:          "no agent " + theirs,
		{Kind: KindAgent, AgentID: "agent-nowhere"}: "no agent agent-nowhere",
		{Kind: KindDoc, DocID: theirDoc}:            "no doc " + theirDoc,
	} {
		err := f.store.Replace(context.Background(), p, []Tab{fileTab("main", "ok.go"), tab})
		var invalid *InvalidError
		if !errors.As(err, &invalid) || invalid.Index != 1 || !strings.Contains(err.Error(), why) {
			t.Errorf("%#v: %v, want tab 1 refused for %q", tab, err, why)
		}
	}
	// A tombstone is still the project's: tide opens one to read what it did.
	f.replace(t, p, Tab{Kind: KindInfo, AgentID: gone})
	f.replace(t, p, kept...)
	err := f.store.Replace(context.Background(), p, []Tab{fileTab("main", "a"), fileTab("main", "a")})
	if err == nil || !strings.Contains(err.Error(), "tab 1: the same tab") {
		t.Errorf("twice: %v", err)
	}
	if got := f.tabs(t, p); !slices.Equal(got, kept) {
		t.Errorf("a refused list changed them: %#v", got)
	}
}

// TestRemovedThroughWdx: what removes an agent, a doc or a project takes its tabs,
// and an agent kept as a tombstone too.
func TestRemovedThroughWdx(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p, other := f.project(t, "wdx"), f.project(t, "other")
	a, tomb, d := f.agent(t, p, "falcon"), f.agent(t, p, "owl"), f.doc(t, p, "plan")
	f.replace(t, p, Tab{Kind: KindAgent, AgentID: a}, Tab{Kind: KindInfo, AgentID: tomb}, Tab{Kind: KindAgent, AgentID: tomb},
		Tab{Kind: KindDoc, DocID: d}, Tab{Kind: KindSettings})
	f.replace(t, other, Tab{Kind: KindSettings})

	f.exec(t, "DELETE FROM terminals WHERE id = ?", "term-falcon") // removing an agent removes its terminal row
	f.exec(t, "UPDATE agents SET removed_at = 1 WHERE id = ?", tomb)
	f.exec(t, "DELETE FROM docs WHERE id = ?", d)
	if got := f.tabs(t, p); !slices.Equal(got, []Tab{{Kind: KindSettings}}) {
		t.Fatalf("got %#v", got)
	}
	f.exec(t, "DELETE FROM projects WHERE id = ?", p)
	var n int
	if err := f.db.QueryRow("SELECT count(*) FROM tide_tabs WHERE project_id = ?", p).Scan(&n); err != nil || n != 0 {
		t.Errorf("a removed project's tabs: %d, %v", n, err)
	}
	if got := f.tabs(t, other); len(got) != 1 {
		t.Errorf("the other project's %#v", got)
	}
}

// TestPathsThroughWdx: removing a worktree takes its tabs; removing or renaming a
// file or a directory takes or moves the tabs of what is at it or under it, and of
// nothing else.
func TestPathsThroughWdx(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.project(t, "wdx")
	f.replace(t, p,
		fileTab("main", "a"),
		fileTab("main", "a/b.go"),
		Tab{Kind: KindDiff, Worktree: "main", Path: "a/c/d.go"},
		fileTab("main", "ab.go"),  // a's name, then more: not under it
		fileTab("main", "a_x/y"),  // what LIKE would read in a_
		fileTab("feat", "a/b.go"), // another worktree
		Tab{Kind: KindPR, Worktree: "feat"},
		fileTab("main", "keep.go"),
	)
	if err := f.store.RenamePath(ctx, p, "main", "a", "z"); err != nil {
		t.Fatal(err)
	}
	want := []Tab{
		fileTab("main", "z"),
		fileTab("main", "z/b.go"),
		{Kind: KindDiff, Worktree: "main", Path: "z/c/d.go"},
		fileTab("main", "ab.go"),
		fileTab("main", "a_x/y"),
		fileTab("feat", "a/b.go"),
		{Kind: KindPR, Worktree: "feat"},
		fileTab("main", "keep.go"),
	}
	if got := f.tabs(t, p); !slices.Equal(got, want) {
		t.Fatalf("renamed:\n%#v\nwant\n%#v", got, want)
	}
	if err := f.store.RemovePath(ctx, p, "main", "z"); err != nil {
		t.Fatal(err)
	}
	want = slices.Delete(want, 0, 3)
	if got := f.tabs(t, p); !slices.Equal(got, want) {
		t.Fatalf("removed:\n%#v\nwant\n%#v", got, want)
	}
	if err := f.store.RemoveWorktree(ctx, p, "feat"); err != nil {
		t.Fatal(err)
	}
	want = []Tab{fileTab("main", "ab.go"), fileTab("main", "a_x/y"), fileTab("main", "keep.go")}
	if got := f.tabs(t, p); !slices.Equal(got, want) {
		t.Fatalf("worktree removed:\n%#v\nwant\n%#v", got, want)
	}
}
