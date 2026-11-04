package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
)

// TestAgentLaunchMigration checks 0023: agents from before ran with the user's own
// settings, so they read ask, with no model; no other mode is taken.
func TestAgentLaunchMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "test.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	list := mustMigrations()
	i := slices.IndexFunc(list, func(m migration) bool { return m.name == "migrations/0023_agent_launch.sql" })
	if i < 0 {
		t.Fatal("no 0023")
	}
	if err := migrate(ctx, d, list[:i]); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO projects (id, name, state, created_at, updated_at) VALUES ('proj-a', 'a', 'open', 0, 0)`,
		`INSERT INTO terminals (id, project_id, name, dir, shell, state, kind, created_at, updated_at) VALUES ('term-a', 'proj-a', 'a', '/r', 'sh', 'running', 'agent', 0, 0)`,
		`INSERT INTO terminals (id, project_id, name, dir, shell, state, kind, created_at, updated_at) VALUES ('term-b', 'proj-a', 'b', '/r', 'sh', 'running', 'agent', 0, 0)`,
		`INSERT INTO agents (id, terminal_id, aca, session_id) VALUES ('agent-a', 'term-a', 'claude_code', 's')`,
	} {
		if _, err := d.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := migrate(ctx, d, list); err != nil {
		t.Fatal(err)
	}
	var model sql.NullString
	var permissions string
	if err := d.QueryRow(`SELECT requested_model, permissions FROM agents WHERE id = 'agent-a'`).Scan(&model, &permissions); err != nil {
		t.Fatal(err)
	}
	if model.Valid || permissions != "ask" {
		t.Errorf("an agent from before: model %v, permissions %q", model, permissions)
	}
	if _, err := d.Exec(`INSERT INTO agents (id, terminal_id, aca, session_id, permissions) VALUES ('agent-b', 'term-b', 'claude_code', 's', 'some')`); err == nil {
		t.Error("an unknown mode was taken")
	}
}
