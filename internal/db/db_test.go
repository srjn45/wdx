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
		rows.Scan(&n)
		names = append(names, n)
	}
	if strings.Join(names, ",") != "kept" {
		t.Fatalf("want only the project from before the backup, got %v", names)
	}
}
