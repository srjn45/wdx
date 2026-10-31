package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// committed is the sha256 of each committed migration, in order. Migrations
// are append-only: add the sum of a new one here, never change one.
var committed = []string{
	"89466dd1bea0a3b0ce3a744e4f04b090f59534b754e57c5db9b69aa37d7a48be", // 0001_projects.sql
	"2dd7fb8b56a1e9034e56639afd054ff676cad79ea236026a282943d01fdb6167", // 0002_schema_readers.sql
	"b3e0c3404c6e719b224d08ffd963bc671ed0c4db321cf97d3e7378caf0fa4d68", // 0003_terminals.sql
	"af5346f10dbf65585b0ec0992edd4df477758fe1cf3607b66cc7319a20e0cd29", // 0004_agents.sql
	"05fbe5f1afb148ef3779586e9b78f133538ff34a55a001e7cb15461d8d31ac12", // 0005_child_agents.sql
}

func TestMigrationsNumbered(t *testing.T) {
	t.Parallel()
	for i, m := range mustMigrations() {
		if want := fmt.Sprintf("migrations/%04d_", i+1); !strings.HasPrefix(m.name, want) {
			t.Errorf("migration %d is %s, want it named %s*.sql", i+1, m.name, want)
		}
	}
}

func TestMigrationsAppendOnly(t *testing.T) {
	t.Parallel()
	list := mustMigrations()
	for i, m := range list {
		sum := sha256.Sum256([]byte(m.body))
		got := hex.EncodeToString(sum[:])
		switch {
		case i >= len(committed):
			t.Errorf("%s is new: add its sum to committed, %q", m.name, got)
		case got != committed[i]:
			t.Errorf("%s was changed: migrations are append-only, add a new one instead", m.name)
		}
	}
	if len(list) < len(committed) {
		t.Errorf("want %d migrations, got %d: migrations are never removed", len(committed), len(list))
	}
}

func TestTablesStrict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
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
