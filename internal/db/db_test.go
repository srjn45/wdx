package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/sqlite"
)

func TestTerminalsBelongToProjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	exec := func(query string, args ...any) error {
		_, err := d.ExecContext(ctx, query, args...)
		return err
	}
	insertTerminal := func(id, project, name string) error {
		return exec(`INSERT INTO terminals (id, project_id, name, dir, shell, state, created_at, updated_at)
			VALUES (?, ?, ?, '/tmp', '/bin/sh', 'starting', 0, 0)`, id, project, name)
	}
	for _, p := range []string{"proj-1", "proj-2"} {
		if err := exec(`INSERT INTO projects (id, name, state, created_at, updated_at) VALUES (?, ?, 'open', 0, 0)`, p, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := insertTerminal("term-1", "proj-1", "swift-falcon"); err != nil {
		t.Fatal(err)
	}
	if err := insertTerminal("term-2", "proj-2", "swift-falcon"); err != nil {
		t.Fatalf("want a name taken in another project allowed, got %v", err)
	}
	if err := insertTerminal("term-3", "proj-1", "Swift-Falcon"); err == nil {
		t.Fatal("want a name unique in its project, ignoring case")
	}
	if err := insertTerminal("term-4", "proj-none", "calm-otter"); err == nil {
		t.Fatal("want a terminal of an unknown project refused")
	}
	if err := exec(`UPDATE terminals SET state = 'gone' WHERE id = 'term-1'`); err == nil {
		t.Fatal("want an unknown state refused")
	}

	if err := exec(`DELETE FROM projects WHERE id = 'proj-1'`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.QueryRowContext(ctx, "SELECT count(*) FROM terminals WHERE project_id = 'proj-1'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("want the project's terminals removed with it, got %d, %v", n, err)
	}
}

// openBefore opens a database at path migrated up to the migration named
// name, which it lacks.
func openBefore(t *testing.T, path, name string) *sql.DB {
	t.Helper()
	i := slices.IndexFunc(migrations, func(m sqlite.Migration) bool { return m.Name == name })
	if i < 0 {
		t.Fatalf("no migration %s", name)
	}
	d, err := sqlite.Open(context.Background(), path, migrations[:i])
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// reopen closes d and opens its database at path again, migrated to the latest schema.
func reopen(t *testing.T, d *sql.DB, path string) *sql.DB {
	t.Helper()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
