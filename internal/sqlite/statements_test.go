package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/srjn45/wdx/internal/sqlite"
)

// TestStatements runs a statement many times, on several connections at once and
// after a run of it was cancelled, which must not leave it unusable.
func TestStatements(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "wdx.db")
	database, err := sqlite.Open(t.Context(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	statements := sqlite.NewStatements(database)
	const query = "SELECT ?1 + 1"
	count := func(ctx context.Context) (int, error) {
		rows, err := statements.Query(ctx, query, 1)
		if err != nil {
			return 0, err
		}
		defer rows.Close()
		var n int
		for rows.Next() {
			if err := rows.Scan(&n); err != nil {
				return 0, err
			}
		}
		return n, rows.Err()
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := count(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled before it was prepared: got %v", err)
	}
	if n, err := count(t.Context()); err != nil || n != 2 {
		t.Fatalf("got %d, %v", n, err)
	}
	if _, err := count(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled once prepared: got %v", err)
	}

	// Rows held open keep their connection, so the next run takes another.
	held, err := statements.Query(t.Context(), query, 41)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if n, err := count(t.Context()); err != nil || n != 2 {
		t.Fatalf("beside rows held open: got %d, %v", n, err)
	}
	var n int
	if !held.Next() || held.Scan(&n) != nil || n != 42 {
		t.Fatalf("the rows held open: got %d, %v", n, held.Err())
	}
}
