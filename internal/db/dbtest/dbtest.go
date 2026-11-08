// Package dbtest saves tests from migrating a database of their own: under the
// race detector that is much of what a test home costs. internal/db's own tests
// still run the migrations, as does the first Seed of each test binary.
package dbtest

import (
	"testing"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/sqlite/sqlitetest"
)

// Seed puts a migrated database at path, for db.Open to find; a database that
// is there stays.
func Seed(t *testing.T, path string) {
	t.Helper()
	sqlitetest.Seed(t, path, db.Open)
}
