// Package sqlitetest saves tests from migrating a database of their own: under
// the race detector that is much of what a test home costs. A database's own
// tests still run its migrations, as does the first Seed of each test binary
// for each Open.
package sqlitetest

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

// Open opens a database at path and migrates it to the latest schema, as
// db.Open does.
type Open func(ctx context.Context, path string) (*sql.DB, error)

// migrated holds, by the Open that made it, the file of a database at the
// latest schema; a func is no map key, so its code's address stands for it.
var migrated sync.Map

// Seed puts a database open migrated at path, for open to find; a database
// that is there stays. open is a function declared in its package, such as
// db.Open, whose migrated file is made once per test binary: never a closure,
// as the closures of one function share their code, and so their file.
func Seed(t *testing.T, path string, open Open) {
	t.Helper()
	once, _ := migrated.LoadOrStore(reflect.ValueOf(open).Pointer(), sync.OnceValues(func() ([]byte, error) {
		return migrate(open)
	}))
	body, err := once.(func() ([]byte, error))()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(body); err != nil {
		t.Fatal(err)
	}
}

// migrate returns the file of a database open migrated.
func migrate(open Open) ([]byte, error) {
	dir, err := os.MkdirTemp("", "sqlitetest")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "migrated.db")
	d, err := open(context.Background(), path)
	if err != nil {
		return nil, err
	}
	// Closing the last connection moves the write-ahead log into the file.
	if err := d.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}
