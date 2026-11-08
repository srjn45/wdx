package hubdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/sqlite"
	"github.com/srjn45/wdx/internal/sqlite/sqlitetest"
)

// committed is the sha256 of each committed migration, in order. Migrations
// are append-only: add the sum of a new one here, never change one.
var committed = []string{
	"2dd7fb8b56a1e9034e56639afd054ff676cad79ea236026a282943d01fdb6167", // 0001_schema_readers.sql
	"6b205d67bcd817a5c21a700691a1cb31ce6387bb06738255924b3140d25faa43", // 0002_accounts.sql
	"c462764ec552e45e9faeee4dc8412d02e53a789227ef5a2e82b0ea63d49929f4", // 0003_nodes.sql
	"8c98ea6935da271ba5f8126e703fe4ab839d04f5eaad59f0bc9c9d8a7620b953", // 0004_docs.sql
}

func TestOpenMigrates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "home", "hub.db")
	for range 2 {
		d, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Close(); err != nil {
			t.Fatal(err)
		}
		schema, err := sqlite.ReadSchema(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		// 0001 breaks older binaries, which knew no hub database.
		if schema.Version != Known() || schema.Oldest != 1 {
			t.Fatalf("schema %+v, want version %d, oldest 1", schema, Known())
		}
	}
}

func TestSeeded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hub.db")
	sqlitetest.Seed(t, path, Open)
	if schema, err := sqlite.ReadSchema(ctx, path); err != nil || schema.Version != Known() {
		t.Fatalf("seeded schema %+v, %v; want version %d", schema, err, Known())
	}
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.ExecContext(ctx, "SELECT oldest FROM schema_readers"); err != nil {
		t.Errorf("no schema_readers table: %v", err)
	}
}

func TestMigrationsNumbered(t *testing.T) {
	t.Parallel()
	for i, m := range migrations {
		if want := fmt.Sprintf("%04d_", i+1); !strings.HasPrefix(m.Name, want) {
			t.Errorf("migration %d is %s, want it named %s*.sql", i+1, m.Name, want)
		}
	}
}

func TestMigrationsAppendOnly(t *testing.T) {
	t.Parallel()
	for i, m := range migrations {
		sum := sha256.Sum256([]byte(m.Body))
		got := hex.EncodeToString(sum[:])
		switch {
		case i >= len(committed):
			t.Errorf("%s is new: add its sum to committed, %q", m.Name, got)
		case got != committed[i]:
			t.Errorf("%s was changed: migrations are append-only, add a new one instead", m.Name)
		}
	}
	if len(migrations) < len(committed) {
		t.Errorf("want %d migrations, got %d: migrations are never removed", len(committed), len(migrations))
	}
}

func TestTablesStrict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	rows, err := d.QueryContext(ctx, "SELECT name FROM pragma_table_list WHERE schema = 'main' AND type = 'table' AND NOT strict AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		t.Errorf("table %s is not STRICT", name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
