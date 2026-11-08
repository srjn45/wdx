package audit

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/hub/hubdb"
	"github.com/srjn45/wdx/internal/sqlite/sqlitetest"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hub.db")
	sqlitetest.Seed(t, path, hubdb.Open)
	d, err := hubdb.Open(context.Background(), path)
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
	ctx := context.Background()
	for i := range n {
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		e := Entry{At: start.Add(time.Duration(i) * time.Minute), UserID: "usr-1a2b3c4d", Actor: ActorUser, Action: "user.logged_in", Address: "192.0.2.1", Details: map[string]any{"recovery_code": i%2 == 0}}
		if i == 0 {
			e = Entry{At: start, Actor: ActorAdmin, Action: "user.invited"}
		}
		if err := Append(ctx, tx, e); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
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
	d := openDB(t)
	if v := verify(t, d); v.Rows != 0 || v.Head != "" || v.Broken != nil {
		t.Errorf("Verify = %+v, want no rows", v)
	}
	if h, err := Head(context.Background(), d); err != nil || h != (Chain{}) {
		t.Errorf("Head = %+v, %v; want no rows", h, err)
	}
}

func TestChainAppendedAndVerified(t *testing.T) {
	t.Parallel()
	d := openDB(t)
	appendN(t, d, 3)
	v := verify(t, d)
	if v.Rows != 3 || v.Broken != nil || len(v.Head) != 64 {
		t.Fatalf("Verify = %+v, want 3 rows and a head", v)
	}
	h, err := Head(context.Background(), d)
	if err != nil || h != v.Chain {
		t.Errorf("Head = %+v, %v; want %+v", h, err, v.Chain)
	}
	var first []byte
	if err := d.QueryRow("SELECT hash FROM audit WHERE id = 1").Scan(&first); err != nil {
		t.Fatal(err)
	}
	r := row{id: 1, at: start.UnixMilli(), actor: "admin", action: "user.invited", details: "{}"}
	if got := r.compute(make([]byte, 32)); string(got) != string(first) {
		t.Errorf("the first row's hash is not chained to 32 zero bytes")
	}
}

func TestChainBroken(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		tamper func(t *testing.T, d *sql.DB)
		rows   int
		broken int64
	}{
		{"an action changed", func(t *testing.T, d *sql.DB) {
			exec(t, d, "UPDATE audit SET action = 'user.enabled' WHERE id = 2")
		}, 1, 2},
		{"details changed", func(t *testing.T, d *sql.DB) {
			exec(t, d, `UPDATE audit SET details = '{"recovery_code":false}' WHERE id = 3`)
		}, 2, 3},
		{"an address made NULL", func(t *testing.T, d *sql.DB) {
			exec(t, d, "UPDATE audit SET address = NULL WHERE id = 2")
		}, 1, 2},
		{"a row removed", func(t *testing.T, d *sql.DB) {
			exec(t, d, "DELETE FROM audit WHERE id = 2")
		}, 1, 3},
		{"a row inserted", func(t *testing.T, d *sql.DB) {
			// The forged row is chained to the one before it, as Append
			// would; the row it pushed along is not.
			exec(t, d, "UPDATE audit SET id = 5 WHERE id = 3")
			var prev []byte
			if err := d.QueryRow("SELECT hash FROM audit WHERE id = 2").Scan(&prev); err != nil {
				t.Fatal(err)
			}
			r := row{id: 3, at: start.Add(90 * time.Second).UnixMilli(), actor: "admin", action: "user.enabled", details: "{}"}
			exec(t, d, "INSERT INTO audit (id, at, actor, action, details, hash) VALUES (?, ?, ?, ?, ?, ?)", r.id, r.at, r.actor, r.action, r.details, r.compute(prev))
		}, 3, 5},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := openDB(t)
			appendN(t, d, 3)
			c.tamper(t, d)
			v := verify(t, d)
			if v.Broken == nil || v.Broken.ID != c.broken || v.Rows != c.rows {
				t.Fatalf("Verify = %+v, broken %+v; want %d rows, broken at %d", v, v.Broken, c.rows, c.broken)
			}
		})
	}
}

func TestAppendRolledBackWithItsTransaction(t *testing.T) {
	t.Parallel()
	d := openDB(t)
	appendN(t, d, 1)
	ctx := context.Background()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Append(ctx, tx, Entry{At: start, Actor: ActorHub, Action: "user.disabled"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if h, err := Head(ctx, d); err != nil || h.Rows != 1 {
		t.Fatalf("Head = %+v, %v; want the one row committed", h, err)
	}
	appendN(t, d, 1)
	if v := verify(t, d); v.Rows != 2 || v.Broken != nil {
		t.Errorf("Verify = %+v, want 2 rows unbroken", v)
	}
}
