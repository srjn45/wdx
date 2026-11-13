package db

import (
	"path/filepath"
	"slices"
	"testing"
)

// TestPromptsTabMigration checks 0035, which builds tide_tabs again to take prompts
// tabs and leaves the kinds to tidestate, keeps every tab, the columns' checks and
// the triggers counting each change of a project's tabs.
func TestPromptsTabMigration(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "test.db")
	d := openBefore(t, path, "0035_prompts_tab.sql")
	t.Cleanup(func() { d.Close() })
	for _, q := range []string{
		`INSERT INTO projects (id, name, state, created_at, updated_at) VALUES ('proj-a', 'a', 'open', 0, 0)`,
		`INSERT INTO tide_tabs (project_id, position, kind) VALUES ('proj-a', 0, 'stats')`,
		`INSERT INTO tide_tabs (project_id, position, kind, worktree, path) VALUES ('proj-a', 1, 'file', 'main', 'a.go')`,
	} {
		if _, err := d.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := d.Exec(`INSERT INTO tide_tabs (project_id, position, kind) VALUES ('proj-a', 2, 'prompts')`); err == nil {
		t.Fatal("a prompts tab before 0035")
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
	if _, err := d.Exec(`INSERT INTO tide_tabs (project_id, position, kind) VALUES ('proj-a', 2, 'prompts')`); err != nil {
		t.Fatalf("a prompts tab: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO tide_tabs (project_id, position, kind, worktree) VALUES ('proj-a', 3, 'prompts', 'main')`); err == nil {
		t.Error("a prompts tab with a worktree")
	}
	if _, err := d.Exec(`INSERT INTO tide_tabs (project_id, position, kind) VALUES ('proj-a', 3, 'file')`); err == nil {
		t.Error("a file tab without its worktree")
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
	if !slices.Equal(kinds, []string{"stats", "file", "prompts"}) {
		t.Fatalf("tabs after 0035: %v", kinds)
	}
	if got := revision(); got != before+1 {
		t.Errorf("a tab inserted after 0035 counted %d, want %d", got, before+1)
	}
}
