package db

import (
	"path/filepath"
	"slices"
	"testing"
)

// TestStatsTabMigration checks 0029, which builds tide_tabs again to take stats tabs,
// keeps every tab, and keeps the triggers counting each change of a project's tabs.
func TestStatsTabMigration(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "test.db")
	d := openBefore(t, path, "0029_stats_tab.sql")
	t.Cleanup(func() { d.Close() })
	for _, q := range []string{
		`INSERT INTO projects (id, name, state, created_at, updated_at) VALUES ('proj-a', 'a', 'open', 0, 0)`,
		`INSERT INTO tide_tabs (project_id, position, kind) VALUES ('proj-a', 0, 'settings')`,
		`INSERT INTO tide_tabs (project_id, position, kind, worktree, path) VALUES ('proj-a', 1, 'file', 'main', 'a.go')`,
	} {
		if _, err := d.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := d.Exec(`INSERT INTO tide_tabs (project_id, position, kind) VALUES ('proj-a', 2, 'stats')`); err == nil {
		t.Fatal("a stats tab before 0029")
	}
	revision := func() int {
		var n int
		if err := d.QueryRow("SELECT revision FROM tide_tab_revisions WHERE project_id = 'proj-a'").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := revision()
	d = reopen(t, d, path)
	if got := revision(); got != before {
		t.Errorf("building the table again counted as changes: %d, was %d", got, before)
	}
	if _, err := d.Exec(`INSERT INTO tide_tabs (project_id, position, kind) VALUES ('proj-a', 2, 'stats')`); err != nil {
		t.Fatalf("a stats tab: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO tide_tabs (project_id, position, kind, worktree) VALUES ('proj-a', 3, 'stats', 'main')`); err == nil {
		t.Error("a stats tab with a worktree")
	}
	var kinds []string
	rows, err := d.Query("SELECT kind FROM tide_tabs ORDER BY position")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, k)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(kinds, []string{"settings", "file", "stats"}) {
		t.Fatalf("tabs after 0029: %v", kinds)
	}
	if got := revision(); got != before+1 {
		t.Errorf("a tab inserted after 0029 counted %d, want %d", got, before+1)
	}
}
