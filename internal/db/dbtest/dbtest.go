// Package dbtest saves tests from migrating a database of their own: under the
// race detector that is much of what a test home costs. internal/db's own tests
// still run the migrations, as does the first Seed of each test binary.
package dbtest

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/srjn45/wdx/internal/db"
)

// migrated is the file of a database at the latest schema.
var migrated = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "dbtest")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "migrated.db")
	d, err := db.Open(context.Background(), path)
	if err != nil {
		return nil, err
	}
	// Closing the last connection moves the write-ahead log into the file.
	if err := d.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
})

// Seed puts a migrated database at path, for db.Open to find; a database that
// is there stays.
func Seed(t *testing.T, path string) {
	t.Helper()
	body, err := migrated()
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
