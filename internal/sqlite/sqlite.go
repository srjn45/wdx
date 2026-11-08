// Package sqlite opens SQLite databases and applies an application's numbered
// migrations to them.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/srjn45/wdx/internal/atomicfile"
	"github.com/srjn45/wdx/internal/logging"
)

// breaksOlder is the header of a migration that older binaries cannot run
// on: it changes or removes what they use, rather than only adding.
const breaksOlder = "-- breaks-older: true"

// Migration is one file of an application's migrations.
type Migration struct {
	Name        string
	Body        string
	BreaksOlder bool
}

// Migrations are an application's migrations in the order they apply; the
// schema version of a database is how many of them it has.
type Migrations []Migration

// Schema is the schema version of a database file.
type Schema struct {
	Version int // the migrations applied; 0 when there is no database
	// Oldest is the schema version a binary must know at least to use the
	// database.
	Oldest int
}

// Load returns the *.sql files at the root of fsys in name order.
func Load(fsys fs.FS) (Migrations, error) {
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, err
	}
	var list Migrations
	for _, name := range names {
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		first, _, _ := strings.Cut(string(body), "\n")
		list = append(list, Migration{Name: name, Body: string(body), BreaksOlder: strings.TrimSpace(first) == breaksOlder})
	}
	return list, nil
}

// Open opens (creating if needed) the database at path and migrates it to the
// latest schema of migrations.
func Open(ctx context.Context, path string, migrations Migrations) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := migrate(ctx, db, migrations); err != nil {
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

// readSchema reads the oldest reader from schema_readers, which a database
// records from the migration that creates it on; before that, any binary that
// knows its version can use it.
func readSchema(ctx context.Context, db *sql.DB) (Schema, error) {
	var s Schema
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&s.Version); err != nil {
		return Schema{}, fmt.Errorf("read schema version: %w", err)
	}
	readers, err := hasReaders(ctx, db)
	if err != nil {
		return Schema{}, err
	}
	if !readers {
		s.Oldest = s.Version
		return s, nil
	}
	if err := db.QueryRowContext(ctx, "SELECT oldest FROM schema_readers").Scan(&s.Oldest); err != nil {
		return Schema{}, fmt.Errorf("read oldest schema reader: %w", err)
	}
	return s, nil
}

func hasReaders(ctx context.Context, q Querier) (bool, error) {
	var n int
	if err := q.QueryRowContext(ctx, "SELECT count(*) FROM pragma_table_list WHERE schema = 'main' AND name = 'schema_readers'").Scan(&n); err != nil {
		return false, fmt.Errorf("look for schema_readers: %w", err)
	}
	return n > 0, nil
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
	// The write-ahead log belongs to the database being replaced.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if err := atomicfile.Copy(path, backup, 0o600); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("database restored", "path", path, "backup", backup)
	return nil
}

// migrate applies the migrations the database lacks, in order. The applied
// count is kept in PRAGMA user_version; each migration and its version bump
// run in one transaction. Migrations are forward-only: a database migrated
// further than this binary knows is used as it is when the migrations it
// lacks only add things.
func migrate(ctx context.Context, db *sql.DB, list Migrations) error {
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
			log.Error("migration failed", "migration", list[i].Name, "err", err)
			return fmt.Errorf("migration %s: %w", list[i].Name, err)
		}
		log.Info("schema migrated", "migration", list[i].Name, "version", i+1, "breaks_older", list[i].BreaksOlder)
	}
	return nil
}

// apply runs the migration m and sets the schema version to version in one
// transaction, recording it as the oldest reader when it breaks older
// binaries.
func apply(ctx context.Context, db *sql.DB, m Migration, version int) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, m.Body); err != nil {
		return err
	}
	if m.BreaksOlder {
		readers, err := hasReaders(ctx, tx)
		if err != nil {
			return err
		}
		if !readers {
			return errors.New("a migration that breaks older binaries needs schema_readers to exist, to record it in")
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_readers (id, oldest) VALUES (1, ?) ON CONFLICT (id) DO UPDATE SET oldest = excluded.oldest", version); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return err
	}
	return tx.Commit()
}
