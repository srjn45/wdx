// Package project manages projects: git repositories that agents work in.
package project

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/srjn45/warden/internal/git"
	"github.com/srjn45/warden/internal/identity"
)

// State is a project's lifecycle state.
type State string

const (
	StateOpen   State = "open"
	StateClosed State = "closed"
)

// Project is a git repository registered with warden. All clones of the
// repository and all their worktrees belong to one project.
type Project struct {
	ID        string
	Name      string
	OriginURL string // "" when the repo has no origin remote
	State     State
	Worktrees []Worktree
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Worktree is a local checkout of a project: a clone or a linked worktree.
type Worktree struct {
	ID           string
	Path         string // root of the work tree
	GitCommonDir string // shared by all worktrees of one clone
	CreatedAt    time.Time
}

// ErrNotGitRepo is returned when opening a directory that is not a git repository.
var ErrNotGitRepo = errors.New("can only work with git projects use " + identity.Name + " init")

// ErrNotFound is returned when no project matches a name or id.
var ErrNotFound = errors.New("project not found")

// Store persists projects.
type Store struct {
	db *sql.DB
}

// NewStore returns a Store backed by db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// OpenLocal opens the git repository containing path; a path inside a repo
// resolves to its work tree root. The work tree joins an existing project when
// it is a worktree of a known clone or a clone of a known origin; otherwise a
// new project is created. Opening is idempotent.
//
// name names the project; when empty, a new project is named after its work
// tree directory and a known project keeps its name. Names are unique.
func (s *Store) OpenLocal(ctx context.Context, path, name string) (Project, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Project{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Project{}, fmt.Errorf("%s: no such directory", abs)
		}
		return Project{}, err
	}
	if !info.IsDir() {
		return Project{}, fmt.Errorf("%s: not a directory", abs)
	}

	root, err := git.TopLevel(ctx, abs)
	if errors.Is(err, git.ErrNotRepo) {
		return Project{}, ErrNotGitRepo
	}
	if err != nil {
		return Project{}, err
	}
	commonDir, err := git.CommonDir(ctx, root)
	if err != nil {
		return Project{}, err
	}
	origin, err := git.RemoteURL(ctx, root, "origin")
	if err != nil {
		return Project{}, err
	}
	key := git.RepoKey(origin)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Project{}, err
	}
	defer tx.Rollback()

	byClone, err := queryID(ctx, tx, "SELECT project_id FROM project_worktrees WHERE git_common_dir = ? LIMIT 1", commonDir)
	if err != nil {
		return Project{}, err
	}
	byOrigin, err := queryID(ctx, tx, "SELECT id FROM projects WHERE origin_key = ?", key)
	if err != nil {
		return Project{}, err
	}
	if byClone != "" && byOrigin != "" && byClone != byOrigin {
		return Project{}, fmt.Errorf("%s is a worktree of project %s but its origin %s belongs to project %s", root, byClone, origin, byOrigin)
	}
	id := cmp.Or(byClone, byOrigin)
	now := time.Now().UnixMilli()

	if id == "" {
		if name == "" {
			name = filepath.Base(root)
		}
		if err := checkName(ctx, tx, name, ""); err != nil {
			return Project{}, err
		}
		if id, err = newID("proj"); err != nil {
			return Project{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO projects (id, name, origin_url, origin_key, state, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, name, nullable(origin), nullable(key), StateOpen, now, now); err != nil {
			return Project{}, err
		}
	} else {
		if name != "" {
			if err := checkName(ctx, tx, name, id); err != nil {
				return Project{}, err
			}
		}
		// Keep the stored origin when this checkout has none; another clone may.
		if _, err := tx.ExecContext(ctx, `
			UPDATE projects SET
				name       = coalesce(?, name),
				origin_url = coalesce(?, origin_url),
				origin_key = coalesce(?, origin_key),
				state      = ?,
				updated_at = ?
			WHERE id = ?`,
			nullable(name), nullable(origin), nullable(key), StateOpen, now, id); err != nil {
			return Project{}, err
		}
	}

	// A known path is re-pointed, in case it was deleted and re-cloned.
	wtID, err := newID("wt")
	if err != nil {
		return Project{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO project_worktrees (id, project_id, path, git_common_dir, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (path) DO UPDATE SET
			project_id     = excluded.project_id,
			git_common_dir = excluded.git_common_dir`,
		wtID, id, root, commonDir, now); err != nil {
		return Project{}, err
	}
	if err := tx.Commit(); err != nil {
		return Project{}, err
	}
	return s.Get(ctx, id)
}

// Get returns the project whose id or name is ref. An id match wins.
func (s *Store) Get(ctx context.Context, ref string) (Project, error) {
	var p Project
	var origin sql.NullString
	var created, updated int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, origin_url, state, created_at, updated_at FROM projects
		WHERE id = ?1 OR name = ?1 ORDER BY id = ?1 DESC LIMIT 1`, ref).
		Scan(&p.ID, &p.Name, &origin, &p.State, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, fmt.Errorf("%w: %q", ErrNotFound, ref)
	}
	if err != nil {
		return Project{}, err
	}
	p.OriginURL = origin.String
	p.CreatedAt, p.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, path, git_common_dir, created_at FROM project_worktrees
		WHERE project_id = ? ORDER BY created_at, path`, p.ID)
	if err != nil {
		return Project{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var w Worktree
		if err := rows.Scan(&w.ID, &w.Path, &w.GitCommonDir, &created); err != nil {
			return Project{}, err
		}
		w.CreatedAt = time.UnixMilli(created)
		p.Worktrees = append(p.Worktrees, w)
	}
	return p, rows.Err()
}

// Close marks the project whose id or name is ref as closed.
func (s *Store) Close(ctx context.Context, ref string) (Project, error) {
	p, err := s.Get(ctx, ref)
	if err != nil {
		return Project{}, err
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE projects SET state = ?, updated_at = ? WHERE id = ?",
		StateClosed, time.Now().UnixMilli(), p.ID); err != nil {
		return Project{}, err
	}
	return s.Get(ctx, p.ID)
}

// Remove deletes the project whose id or name is ref along with everything
// that belongs to it. Files on disk are not touched.
func (s *Store) Remove(ctx context.Context, ref string) (Project, error) {
	p, err := s.Get(ctx, ref)
	if err != nil {
		return Project{}, err
	}
	// Related rows go via ON DELETE CASCADE.
	if _, err := s.db.ExecContext(ctx, "DELETE FROM projects WHERE id = ?", p.ID); err != nil {
		return Project{}, err
	}
	return p, nil
}

// checkName fails if another project than self already uses name.
func checkName(ctx context.Context, tx *sql.Tx, name, self string) error {
	other, err := queryID(ctx, tx, "SELECT id FROM projects WHERE name = ? AND id <> ?", name, self)
	if err != nil {
		return err
	}
	if other != "" {
		return fmt.Errorf("project name %q is already used by project %s; choose another with --name", name, other)
	}
	return nil
}

// queryID returns the single string column of the first row, or "" if none.
func queryID(ctx context.Context, tx *sql.Tx, query string, args ...any) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, query, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func nullable(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func newID(prefix string) (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(b), nil
}
