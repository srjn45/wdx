package nodeaudit

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/db/dbtest"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wdx.db")
	dbtest.Seed(t, path)
	d, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

var start = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

// appendN appends n entries, each in a transaction of its own.
func appendN(t *testing.T, d *sql.DB, n int) {
	t.Helper()
	for i := range n {
		e := Entry{At: start.Add(time.Duration(i) * time.Minute), Action: ActionRecoveryKeyWrong, Details: map[string]any{"delay_ms": 1000 << i}}
		if i == 0 {
			e = Entry{At: start, Action: ActionCodeSetUp}
		}
		if i == 2 {
			e.NodeID = "node-1a2b3c4d"
		}
		if err := AppendOne(context.Background(), d, e); err != nil {
			t.Fatal(err)
		}
	}
}

func verify(t *testing.T, d *sql.DB) Verification {
	t.Helper()
	v, err := Verify(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func exec(t *testing.T, d *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := d.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyChain(t *testing.T) {
	t.Parallel()
	if v := verify(t, openDB(t)); v.Rows != 0 || v.Head != "" || v.Broken != nil {
		t.Errorf("Verify = %+v, want no rows", v)
	}
}

func TestChainAppendedAndVerified(t *testing.T) {
	t.Parallel()
	d := openDB(t)
	appendN(t, d, 4)
	v := verify(t, d)
	if v.Rows != 4 || v.Head == "" || v.Broken != nil {
		t.Fatalf("Verify = %+v, want 4 rows that match", v)
	}
	var action string
	var node sql.NullString
	if err := d.QueryRow("SELECT action, node_id FROM hub_audit WHERE id = 3").Scan(&action, &node); err != nil || action != ActionRecoveryKeyWrong || node.String != "node-1a2b3c4d" {
		t.Errorf("row 3 = %q, %v, %v", action, node, err)
	}
}

func TestChangedRowFound(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]string{
		"action":   "UPDATE hub_audit SET action = 'code.replaced' WHERE id = 2",
		"details":  `UPDATE hub_audit SET details = '{"delay_ms":1}' WHERE id = 2`,
		"at":       "UPDATE hub_audit SET at = at + 1 WHERE id = 2",
		"node set": "UPDATE hub_audit SET node_id = 'node-x' WHERE id = 2",
		"removed":  "DELETE FROM hub_audit WHERE id = 2",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := openDB(t)
			appendN(t, d, 4)
			exec(t, d, change)
			v := verify(t, d)
			if v.Broken == nil || v.Rows != 1 {
				t.Fatalf("Verify = %+v, want the chain broken after row 1", v)
			}
			// A removed row breaks the chain at the one after it.
			if v.Broken.ID != 2 && v.Broken.ID != 3 {
				t.Errorf("broken at %d, want 2, or 3 once 2 is gone", v.Broken.ID)
			}
		})
	}
}

func TestAppendRolledBackWithItsAct(t *testing.T) {
	t.Parallel()
	d := openDB(t)
	ctx := context.Background()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Append(ctx, tx, Entry{At: start, Action: ActionRemoteAccessOn}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if v := verify(t, d); v.Rows != 0 {
		t.Errorf("Verify = %+v, want the row rolled back with its act", v)
	}
}

func TestWrongCodes(t *testing.T) {
	t.Parallel()
	d := openDB(t)
	ctx := context.Background()
	wrong := func(at time.Time) {
		t.Helper()
		if err := AppendOne(ctx, d, Entry{At: at, Action: ActionCodeWrong, NodeID: "node-1a2b3c4d"}); err != nil {
			t.Fatal(err)
		}
	}
	want := func(count int, latest time.Time) {
		t.Helper()
		n, at, err := WrongCodes(ctx, d)
		if err != nil || n != count || !at.Equal(latest) {
			t.Fatalf("got %d at %v (%v), want %d at %v", n, at, err, count, latest)
		}
	}

	want(0, time.Time{})
	wrong(start)
	wrong(start.Add(time.Minute))
	want(2, start.Add(time.Minute))
	// A session opened starts the count again.
	if err := AppendOne(ctx, d, Entry{At: start.Add(2 * time.Minute), Action: ActionSessionOpened, NodeID: "node-1a2b3c4d"}); err != nil {
		t.Fatal(err)
	}
	want(0, time.Time{})
	wrong(start.Add(3 * time.Minute))
	want(1, start.Add(3*time.Minute))
}
