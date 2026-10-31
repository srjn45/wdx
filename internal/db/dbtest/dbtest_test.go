package dbtest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/srjn45/wdx/internal/db"
)

func TestSeedIsAtTheLatestSchema(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "home", "wdx.db")
	Seed(t, path)

	schema, err := db.ReadSchema(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if schema.Version != db.Known() {
		t.Errorf("schema version %d, want %d", schema.Version, db.Known())
	}
	d, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.ExecContext(context.Background(), "SELECT id FROM projects"); err != nil {
		t.Errorf("no projects table: %v", err)
	}
}

func TestSeedKeepsADatabaseThatIsThere(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "wdx.db")
	if err := os.WriteFile(path, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	Seed(t, path)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "mine" {
		t.Errorf("the file became %d bytes", len(got))
	}
}
