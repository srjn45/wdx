package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenMigratesAndIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "test.db")

	for i := range 2 {
		d, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		var n int
		if err := d.QueryRowContext(ctx, "SELECT count(*) FROM projects, schema_readers").Scan(&n); err != nil {
			t.Fatalf("projects table missing: %v", err)
		}
		d.Close()
	}
}

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

func TestOpenRefusesNewerSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")

	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, "PRAGMA user_version = 999; UPDATE schema_readers SET oldest = 999"); err != nil {
		t.Fatal(err)
	}
	d.Close()

	if _, err := Open(ctx, path); err == nil || !strings.Contains(err.Error(), "upgrade the binary") {
		t.Fatalf("want newer-schema error, got %v", err)
	}
}

// testMigrations returns migrations that create tables a, b and c in turn,
// after the real ones; breaking marks those of them that break older
// binaries.
func testMigrations(t *testing.T, breaking map[string]bool) []migration {
	t.Helper()
	list := mustMigrations()
	for _, table := range []string{"a", "b", "c"} {
		list = append(list, migration{name: table, body: "CREATE TABLE " + table + " (x INTEGER) STRICT", breaksOlder: breaking[table]})
	}
	return list
}

func openWith(t *testing.T, path string, list []migration) error {
	t.Helper()
	d, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	return migrate(context.Background(), d, list)
}

func TestOpenOlderBinaryOnAddedSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	list := testMigrations(t, nil)
	if err := openWith(t, path, list); err != nil {
		t.Fatal(err)
	}
	if s, err := ReadSchema(ctx, path); err != nil || s.Version != len(list) || s.Oldest != readersVersion {
		t.Fatalf("want version %d readable from %d, got %+v, %v", len(list), readersVersion, s, err)
	}
	// This binary knows none of a, b and c, which only add tables.
	if _, err := Open(ctx, path); err != nil {
		t.Fatalf("want an older binary to use it, got %v", err)
	}
}

func TestOpenOlderBinaryOnBrokenSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	list := testMigrations(t, map[string]bool{"b": true})
	if err := openWith(t, path, list); err != nil {
		t.Fatal(err)
	}
	b := Known() + 2
	if s, err := ReadSchema(ctx, path); err != nil || s.Oldest != b {
		t.Fatalf("want oldest reader %d, got %+v, %v", b, s, err)
	}
	if err := openWith(t, path, list[:b]); err != nil {
		t.Fatalf("want a binary that knows b to use it, got %v", err)
	}
	if _, err := Open(ctx, path); err == nil || !strings.Contains(err.Error(), "upgrade the binary") {
		t.Fatalf("want a binary that does not know b refused, got %v", err)
	}
}

func TestBackupAndRestore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	path, backup := filepath.Join(dir, "test.db"), filepath.Join(dir, "backup.db")
	if s, err := ReadSchema(ctx, path); err != nil || s != (Schema{}) {
		t.Fatalf("want no schema for a missing file, got %+v, %v", s, err)
	}
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	insert := func(id string) {
		t.Helper()
		if _, err := d.ExecContext(ctx, "INSERT INTO projects (id, name, state, created_at, updated_at) VALUES (?, ?, 'open', 0, 0)", id, id); err != nil {
			t.Fatal(err)
		}
	}
	insert("kept")
	if err := Backup(ctx, path, backup); err != nil {
		t.Fatal(err)
	}
	insert("lost")
	d.Close()

	if err := Restore(ctx, backup, path); err != nil {
		t.Fatal(err)
	}
	if d, err = Open(ctx, path); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var names []string
	rows, err := d.QueryContext(ctx, "SELECT name FROM projects ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if strings.Join(names, ",") != "kept" {
		t.Fatalf("want only the project from before the backup, got %v", names)
	}
}
