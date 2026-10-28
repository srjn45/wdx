// Package db opens the SQLite database and applies schema migrations.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/srjn45/wdx/internal/logging"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// breaksOlder is the header of a migration that older binaries cannot run
// on: it changes or removes what they use, rather than only adding.
const breaksOlder = "-- breaks-older: true"

// readersVersion is the migration that creates schema_readers; from it on,
// the database records the oldest schema version that can use it.
const readersVersion = 2

type migration struct {
	name        string
	body        string
	breaksOlder bool
}

// Schema is the schema version of a database file.
type Schema struct {
	Version int // the migrations applied; 0 when there is no database
	// Oldest is the schema version a binary must know at least to use the
	// database.
	Oldest int
}

// Open opens (creating if needed) the database at path and migrates it to the
// latest schema.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := migrate(ctx, db, mustMigrations()); err != nil {
		db.Close()
		return nil, err
	}
	logging.FromContext(ctx).Info("database opened", "path", path)
	return db, nil
}

// readOnly opens the database at path only to read it, leaving its journal
// mode as it is.
func readOnly(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return db, nil
}

// Known is the schema version this binary migrates databases to.
func Known() int {
	return len(mustMigrations())
}

// ReadSchema returns the schema of the database at path without changing
// it; a missing file has version 0.
func ReadSchema(ctx context.Context, path string) (Schema, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return Schema{}, nil
	}
	db, err := readOnly(path)
	if err != nil {
		return Schema{}, err
	}
	defer db.Close()
	return readSchema(ctx, db)
}

func readSchema(ctx context.Context, db *sql.DB) (Schema, error) {
	var s Schema
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&s.Version); err != nil {
		return Schema{}, fmt.Errorf("read schema version: %w", err)
	}
	if s.Version < readersVersion {
		s.Oldest = s.Version
		return s, nil
	}
	if err := db.QueryRowContext(ctx, "SELECT oldest FROM schema_readers").Scan(&s.Oldest); err != nil {
		return Schema{}, fmt.Errorf("read oldest schema reader: %w", err)
	}
	return s, nil
}

// Backup writes a consistent copy of the database at path to dest, which
// must not exist.
func Backup(ctx context.Context, path, dest string) error {
	db, err := readOnly(path)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", dest); err != nil {
		return fmt.Errorf("back up the database: %w", err)
	}
	logging.FromContext(ctx).Info("database backed up", "path", path, "backup", dest)
	return nil
}

// Restore replaces the database at path, which nothing may have open, with
// the backup.
func Restore(ctx context.Context, backup, path string) error {
	tmp := path + ".restore"
	if err := copyFile(backup, tmp); err != nil {
		return err
	}
	// The write-ahead log belongs to the database being replaced.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("database restored", "path", path, "backup", backup)
	return nil
}

func copyFile(src, dest string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dest, b, 0o600)
}

func mustMigrations() []migration {
	list, err := readMigrations(migrationsFS)
	if err != nil {
		panic(fmt.Sprintf("db: %v", err))
	}
	return list
}

// readMigrations returns migrations/NNNN_*.sql of fsys in order.
func readMigrations(fsys fs.FS) ([]migration, error) {
	names, err := fs.Glob(fsys, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	var list []migration
	for _, name := range names {
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		first, _, _ := strings.Cut(string(body), "\n")
		list = append(list, migration{name: name, body: string(body), breaksOlder: strings.TrimSpace(first) == breaksOlder})
	}
	return list, nil
}

// migrate applies the migrations the database lacks, in order. The applied
// count is kept in PRAGMA user_version; each migration and its version bump
// run in one transaction. Migrations are forward-only: a database migrated
// further than this binary knows is used as it is when the migrations it
// lacks only add things.
func migrate(ctx context.Context, db *sql.DB, list []migration) error {
	log := logging.FromContext(ctx)
	s, err := readSchema(ctx, db)
	if err != nil {
		return err
	}
	if s.Version > len(list) {
		if s.Oldest > len(list) {
			return fmt.Errorf("database schema version %d needs a binary that knows schema version %d; this one knows %d: upgrade the binary", s.Version, s.Oldest, len(list))
		}
		log.Info("schema newer than this binary, but compatible", "version", s.Version, "oldest", s.Oldest, "known", len(list))
		return nil
	}

	for i := s.Version; i < len(list); i++ {
		if err := apply(ctx, db, list[i], i+1); err != nil {
			log.Error("migration failed", "migration", list[i].name, "err", err.Error())
			return fmt.Errorf("migration %s: %w", list[i].name, err)
		}
		log.Info("schema migrated", "migration", list[i].name, "version", i+1, "breaks_older", list[i].breaksOlder)
	}
	return nil
}

// apply runs the migration m and sets the schema version to version in one
// transaction, recording it as the oldest reader when it breaks older
// binaries.
func apply(ctx context.Context, db *sql.DB, m migration, version int) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, m.body); err != nil {
		return err
	}
	if m.breaksOlder && version >= readersVersion {
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_readers (id, oldest) VALUES (1, ?) ON CONFLICT (id) DO UPDATE SET oldest = excluded.oldest", version); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return err
	}
	return tx.Commit()
}
