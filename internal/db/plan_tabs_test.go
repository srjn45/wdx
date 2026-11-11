package db

import (
	"path/filepath"
	"slices"
	"testing"
)

// TestPlanTabsMigration checks 0021, which builds tide_tabs again, keeps every tab,
// takes plan tabs, and keeps the triggers: the one that closes a tombstoned agent's
// tabs, and those counting each change of a project's tabs.
func TestPlanTabsMigration(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "test.db")
	d := openBefore(t, path, "0021_plan_tabs.sql")
	t.Cleanup(func() { d.Close() })
	for _, q := range []string{
		`INSERT INTO projects (id, name, state, created_at, updated_at) VALUES ('proj-a', 'a', 'open', 0, 0)`,
		`INSERT INTO terminals (id, project_id, name, dir, shell, state, kind, created_at, updated_at) VALUES ('term-a', 'proj-a', 'a', '/r', 'sh', 'running', 'agent', 0, 0)`,
		`INSERT INTO agents (id, terminal_id, aca, session_id) VALUES ('agent-a', 'term-a', 'claude', 's')`,
		`INSERT INTO docs (id, project_id, name, format, revision, created_at, updated_at) VALUES ('doc-a', 'proj-a', 'notes', 'md', 1, 0, 0)`,
		`INSERT INTO docs (id, project_id, kind, name, format, revision, created_at, updated_at) VALUES ('doc-p', 'proj-a', 'plan', 'plan:sso', 'yaml', 1, 0, 0)`,
		`INSERT INTO tide_tabs (project_id, position, kind, agent_id) VALUES ('proj-a', 0, 'agent', 'agent-a')`,
		`INSERT INTO tide_tabs (project_id, position, kind, doc_id) VALUES ('proj-a', 1, 'doc', 'doc-a')`,
		`INSERT INTO tide_tabs (project_id, position, kind, worktree, path) VALUES ('proj-a', 2, 'file', 'main', 'a.go')`,
	} {
		if _, err := d.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := d.Exec(`INSERT INTO tide_tabs (project_id, position, kind, doc_id) VALUES ('proj-a', 3, 'plan', 'doc-p')`); err == nil {
		t.Fatal("a plan tab before 0021")
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
	if _, err := d.Exec(`INSERT INTO tide_tabs (project_id, position, kind, doc_id) VALUES ('proj-a', 3, 'plan', 'doc-p')`); err != nil {
		t.Fatalf("a plan tab: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO tide_tabs (project_id, position, kind) VALUES ('proj-a', 4, 'plan')`); err == nil {
		t.Error("a plan tab with no plan")
	}
	kinds := func() []string {
		var list []string
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
			list = append(list, k)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return list
	}
	if got := kinds(); !slices.Equal(got, []string{"agent", "doc", "file", "plan"}) {
		t.Fatalf("tabs after 0021: %v", got)
	}
	if got := revision(); got != before+1 {
		t.Errorf("a tab inserted after 0021 counted %d, want %d", got, before+1)
	}
	if _, err := d.Exec("DELETE FROM docs WHERE id = 'doc-p'"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("UPDATE agents SET removed_at = 1 WHERE id = 'agent-a'"); err != nil {
		t.Fatal(err)
	}
	if got := kinds(); !slices.Equal(got, []string{"doc", "file"}) {
		t.Errorf("after removing the plan and tombstoning the agent: %v", got)
	}
	if got := revision(); got != before+3 {
		t.Errorf("after two deletions the revision is %d, want %d", got, before+3)
	}
}
