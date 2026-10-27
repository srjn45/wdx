// Package db opens the SQLite database and applies schema migrations.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/srjn45/warden/internal/logging"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open opens (creating if needed) the database at path and migrates it to the
// latest schema.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	logging.FromContext(ctx).Info("database opened", "path", path)
	return db, nil
}

// migrate applies migrations/NNNN_*.sql in order. The applied count is kept in
// PRAGMA user_version; each migration and its version bump run in one
// transaction. Migrations are forward-only.
func migrate(ctx context.Context, db *sql.DB) error {
	names, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}

	var current int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if current > len(names) {
		return fmt.Errorf("database schema version %d is newer than this binary supports (%d); upgrade the binary", current, len(names))
	}

	for i := current; i < len(names); i++ {
		if err := apply(ctx, db, names[i], i+1); err != nil {
			logging.FromContext(ctx).Error("migration failed", "migration", names[i], "err", err.Error())
			return fmt.Errorf("migration %s: %w", names[i], err)
		}
		logging.FromContext(ctx).Info("schema migrated", "migration", names[i], "version", i+1)
	}
	return nil
}

// apply runs the migration name and sets the schema version to version in one
// transaction.
func apply(ctx context.Context, db *sql.DB, name string, version int) error {
	body, err := migrationsFS.ReadFile(name)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return err
	}
	return tx.Commit()
}
