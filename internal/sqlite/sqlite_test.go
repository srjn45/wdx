package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

const readers = `-- breaks-older: true
CREATE TABLE schema_readers (
	id     INTEGER PRIMARY KEY CHECK (id = 1),
	oldest INTEGER NOT NULL
) STRICT;`

// load returns migrations that create table a, then schema_readers, then the
// tables b, c and d; breaking marks those of b, c and d that break older
// binaries.
func load(t *testing.T, breaking map[string]bool) Migrations {
	t.Helper()
	fsys := fstest.MapFS{
		"0001_a.sql":       {Data: []byte("CREATE TABLE a (x INTEGER) STRICT;")},
		"0002_readers.sql": {Data: []byte(readers)},
		"README.md":        {Data: []byte("not a migration")},
		"old/0003_x.sql":   {Data: []byte("not at the root")},
	}
	for i, table := range []string{"b", "c", "d"} {
		body := "CREATE TABLE " + table + " (x INTEGER) STRICT;"
		if breaking[table] {
			body = breaksOlder + "\n" + body
		}
		fsys[fmt.Sprintf("%04d_%s.sql", i+3, table)] = &fstest.MapFile{Data: []byte(body)}
	}
	list, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestLoad(t *testing.T) {
	t.Parallel()
	list := load(t, map[string]bool{"c": true})
	var names []string
	for _, m := range list {
		names = append(names, m.Name)
	}
	if got := strings.Join(names, ","); got != "0001_a.sql,0002_readers.sql,0003_b.sql,0004_c.sql,0005_d.sql" {
		t.Fatalf("migrations %s", got)
	}
	for i, want := range []bool{false, true, false, true, false} {
		if list[i].BreaksOlder != want {
			t.Errorf("%s breaks older binaries: %v, want %v", list[i].Name, list[i].BreaksOlder, want)
		}
	}
}

func TestOpenMigratesAndIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "test.db")
	list := load(t, nil)

	for i := range 2 {
		d, err := Open(ctx, path, list)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		var n int
		if err := d.QueryRowContext(ctx, "SELECT count(*) FROM a, schema_readers, d").Scan(&n); err != nil {
			t.Fatalf("tables missing: %v", err)
		}
		d.Close()
	}
	if s, err := ReadSchema(ctx, path); err != nil || s != (Schema{Version: 5, Oldest: 2}) {
		t.Fatalf("want version 5 readable from 2, got %+v, %v", s, err)
	}
}

func TestOpenRefusesNewerSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	list := load(t, nil)

	d, err := Open(ctx, path, list)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, "PRAGMA user_version = 999; UPDATE schema_readers SET oldest = 999"); err != nil {
		t.Fatal(err)
	}
	d.Close()

	if _, err := Open(ctx, path, list); err == nil || !strings.Contains(err.Error(), "upgrade the binary") {
		t.Fatalf("want newer-schema error, got %v", err)
	}
}

func TestOpenOlderBinaryOnAddedSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	list := load(t, nil)
	d, err := Open(ctx, path, list)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	// A binary that knows none of b, c and d, which only add tables.
	d, err = Open(ctx, path, list[:2])
	if err != nil {
		t.Fatalf("want an older binary to use it, got %v", err)
	}
	d.Close()
}

func TestOpenOlderBinaryOnBrokenSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	list := load(t, map[string]bool{"c": true})
	d, err := Open(ctx, path, list)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	if s, err := ReadSchema(ctx, path); err != nil || s.Oldest != 4 {
		t.Fatalf("want oldest reader 4, got %+v, %v", s, err)
	}
	d, err = Open(ctx, path, list[:4])
	if err != nil {
		t.Fatalf("want a binary that knows c to use it, got %v", err)
	}
	d.Close()
	if _, err := Open(ctx, path, list[:3]); err == nil || !strings.Contains(err.Error(), "upgrade the binary") {
		t.Fatalf("want a binary that does not know c refused, got %v", err)
	}
}

func TestBreakingMigrationNeedsReaders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	list, err := Load(fstest.MapFS{
		"0001_a.sql": {Data: []byte("CREATE TABLE a (x INTEGER) STRICT;")},
		"0002_b.sql": {Data: []byte(breaksOlder + "\nCREATE TABLE b (x INTEGER) STRICT;")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, path, list); err == nil || !strings.Contains(err.Error(), "needs schema_readers") {
		t.Fatalf("want a breaking migration refused without schema_readers, got %v", err)
	}
	if s, err := ReadSchema(ctx, path); err != nil || s != (Schema{Version: 1, Oldest: 1}) {
		t.Fatalf("want the breaking migration rolled back, got %+v, %v", s, err)
	}
}

func TestSchemaWithoutReaders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := Open(ctx, path, load(t, nil)[:1])
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	if s, err := ReadSchema(ctx, path); err != nil || s != (Schema{Version: 1, Oldest: 1}) {
		t.Fatalf("want version 1 readable from 1, got %+v, %v", s, err)
	}
}

func TestBackupAndRestore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	path, backup := filepath.Join(dir, "test.db"), filepath.Join(dir, "backup.db")
	list := load(t, nil)
	if s, err := ReadSchema(ctx, path); err != nil || s != (Schema{}) {
		t.Fatalf("want no schema for a missing file, got %+v, %v", s, err)
	}
	d, err := Open(ctx, path, list)
	if err != nil {
		t.Fatal(err)
	}
	insert := func(x int) {
		t.Helper()
		if _, err := d.ExecContext(ctx, "INSERT INTO a (x) VALUES (?)", x); err != nil {
			t.Fatal(err)
		}
	}
	insert(1)
	if err := Backup(ctx, path, backup); err != nil {
		t.Fatal(err)
	}
	insert(2)
	d.Close()

	if err := Restore(ctx, backup, path); err != nil {
		t.Fatal(err)
	}
	if d, err = Open(ctx, path, list); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var got string
	if err := d.QueryRowContext(ctx, "SELECT group_concat(x) FROM a").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "1" {
		t.Fatalf("want only the row from before the backup, got %v", got)
	}
}
