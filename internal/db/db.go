// Package db is wdx's one database: its migrations, applied on internal/sqlite.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/srjn45/wdx/internal/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

var migrations = mustLoad()

func mustLoad() sqlite.Migrations {
	dir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		panic(fmt.Sprintf("db: %v", err))
	}
	list, err := sqlite.Load(dir)
	if err != nil {
		panic(fmt.Sprintf("db: %v", err))
	}
	return list
}

// Open opens (creating if needed) wdx's database at path and migrates it to the
// latest schema.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	return sqlite.Open(ctx, path, migrations)
}

// Known is the schema version this binary migrates databases to.
func Known() int {
	return len(migrations)
}
