package db

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenMigratesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "test.db")

	for i := range 2 {
		d, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		var n int
		if err := d.QueryRowContext(ctx, "SELECT count(*) FROM projects").Scan(&n); err != nil {
			t.Fatalf("projects table missing: %v", err)
		}
		d.Close()
	}
}

func TestOpenRefusesNewerSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")

	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, "PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	d.Close()

	if _, err := Open(ctx, path); err == nil || !strings.Contains(err.Error(), "newer than this binary") {
		t.Fatalf("want newer-schema error, got %v", err)
	}
}
