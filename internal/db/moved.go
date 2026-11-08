package db

import (
	"context"
	"database/sql"

	"github.com/srjn45/wdx/internal/sqlite"
)

// What moved to internal/sqlite, until its callers use it there.

type (
	Execer     = sqlite.Execer
	Querier    = sqlite.Querier
	Statements = sqlite.Statements
	Schema     = sqlite.Schema
)

func Exec(ctx context.Context, e Execer, query string, args ...any) (int64, error) {
	return sqlite.Exec(ctx, e, query, args...)
}

func NewID(prefix string) (string, error) { return sqlite.NewID(prefix) }

func NewStatements(db *sql.DB) *Statements { return sqlite.NewStatements(db) }

func ReadSchema(ctx context.Context, path string) (Schema, error) {
	return sqlite.ReadSchema(ctx, path)
}

func Backup(ctx context.Context, path, dest string) error { return sqlite.Backup(ctx, path, dest) }

func Restore(ctx context.Context, backup, path string) error {
	return sqlite.Restore(ctx, backup, path)
}
