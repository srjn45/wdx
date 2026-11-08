package sqlitetest

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/srjn45/wdx/internal/sqlite"
)

func migrations(table string) sqlite.Migrations {
	list, err := sqlite.Load(fstest.MapFS{"0001_" + table + ".sql": {Data: []byte("CREATE TABLE " + table + " (id INTEGER PRIMARY KEY) STRICT;")}})
	if err != nil {
		panic(err)
	}
	return list
}

var apples, pears = migrations("apples"), migrations("pears")

func openApples(ctx context.Context, path string) (*sql.DB, error) {
	return sqlite.Open(ctx, path, apples)
}

func openPears(ctx context.Context, path string) (*sql.DB, error) {
	return sqlite.Open(ctx, path, pears)
}

// tables returns the tables of the database at path, which open opens.
func tables(t *testing.T, path string, open Open) []string {
	t.Helper()
	d, err := open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	rows, err := d.Query("SELECT name FROM pragma_table_list WHERE schema = 'main' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}

// Each Open seeds its own schema, whichever seeded first.
func TestSeedIsTheSchemaOfItsOpen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, p := filepath.Join(dir, "a", "apples.db"), filepath.Join(dir, "pears.db")
	Seed(t, a, openApples)
	Seed(t, p, openPears)
	if got := tables(t, a, openApples); !slices.Equal(got, []string{"apples"}) {
		t.Errorf("the apples database has %v", got)
	}
	if got := tables(t, p, openPears); !slices.Equal(got, []string{"pears"}) {
		t.Errorf("the pears database has %v", got)
	}
}

func TestSeedKeepsADatabaseThatIsThere(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "mine.db")
	if err := os.WriteFile(path, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	Seed(t, path, openApples)
	if got, err := os.ReadFile(path); err != nil || string(got) != "mine" {
		t.Errorf("the file became %q, %v", got, err)
	}
}
