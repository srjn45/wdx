package project

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/logging"
)

// Store persists projects.
type Store struct {
	db          *sql.DB
	projectsDir string
}

// NewStore returns a Store backed by db that clones projects into projectsDir.
func NewStore(db *sql.DB, projectsDir string) *Store {
	return &Store{db: db, projectsDir: projectsDir}
}

// OpenLocal opens the git repository containing path; a path inside a repo
// resolves to its work tree root. The work tree joins an existing project when
// it is a worktree of a known clone or a clone of a known origin; otherwise a
// new project is created. Checkouts of a known project that are no longer on
// disk are dropped. Opening is idempotent.
//
// name names the project; when empty, a new project is named after its work
// tree directory and a known project keeps its name. Names are unique.
func (s *Store) OpenLocal(ctx context.Context, path, name string) (Opened, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Opened{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Opened{}, &InvalidPathError{Path: abs, Reason: "no such directory"}
		}
		return Opened{}, err
	}
	if !info.IsDir() {
		return Opened{}, &InvalidPathError{Path: abs, Reason: "not a directory"}
	}

	root, err := git.TopLevel(ctx, abs)
	if errors.Is(err, git.ErrNotRepo) {
		return Opened{}, ErrNotGitRepo
	}
	if err != nil {
		return Opened{}, err
	}
	commonDir, err := git.CommonDir(ctx, root)
	if err != nil {
		return Opened{}, err
	}
	origin, err := git.RemoteURL(ctx, root, "origin")
	if err != nil {
		return Opened{}, err
	}
	key := git.RepoKey(origin)
	log := logging.FromContext(ctx)
	log.Info("repository resolved", "path", abs, "root", root, "git_common_dir", commonDir, "origin", origin)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Opened{}, err
	}
	defer tx.Rollback()

	byClone, err := queryID(ctx, tx, "SELECT project_id FROM project_worktrees WHERE git_common_dir = ? LIMIT 1", commonDir)
	if err != nil {
		return Opened{}, err
	}
	byOrigin, err := queryID(ctx, tx, "SELECT id FROM projects WHERE origin_key = ?", key)
	if err != nil {
		return Opened{}, err
	}
	log.Info("project looked up", "by_checkout", byClone, "by_origin", byOrigin)
	if byClone != "" && byOrigin != "" && byClone != byOrigin {
		tx.Rollback()
		return Opened{}, s.mismatch(ctx, root, origin, byClone, byOrigin)
	}
	id := cmp.Or(byClone, byOrigin)
	now := time.Now().UnixMilli()
	registered := id == ""
	var dropped []Worktree

	if registered {
		if name == "" {
			name = filepath.Base(root)
		}
		if err := s.checkName(ctx, tx, name, ""); err != nil {
			return Opened{}, err
		}
		if id, err = db.NewID("proj"); err != nil {
			return Opened{}, err
		}
		if _, err := db.Exec(ctx, tx, `
			INSERT INTO projects (id, name, origin_url, origin_key, state, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, name, nullable(origin), nullable(key), StateOpen, now, now); err != nil {
			return Opened{}, err
		}
		log.Info("project registered", "project_id", id, "name", name, "origin", origin)
	} else {
		if name != "" {
			if err := s.checkName(ctx, tx, name, id); err != nil {
				return Opened{}, err
			}
		}
		if dropped, err = dropMissing(ctx, tx, id); err != nil {
			return Opened{}, err
		}
		// Keep the stored origin when this checkout has none; another clone may.
		if _, err := db.Exec(ctx, tx, `
			UPDATE projects SET
				name       = coalesce(?, name),
				origin_url = coalesce(?, origin_url),
				origin_key = coalesce(?, origin_key),
				state      = ?,
				updated_at = ?
			WHERE id = ?`,
			nullable(name), nullable(origin), nullable(key), StateOpen, now, id); err != nil {
			return Opened{}, err
		}
		log.Info("project reopened", "project_id", id, "new_name", name, "origin", origin)
	}

	// A known path is re-pointed, in case it was deleted and re-cloned.
	wtID, err := db.NewID("wt")
	if err != nil {
		return Opened{}, err
	}
	if _, err := db.Exec(ctx, tx, `
		INSERT INTO project_worktrees (id, project_id, path, git_common_dir, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (path) DO UPDATE SET
			project_id     = excluded.project_id,
			git_common_dir = excluded.git_common_dir`,
		wtID, id, root, commonDir, now); err != nil {
		return Opened{}, err
	}
	if err := tx.Commit(); err != nil {
		return Opened{}, err
	}
	log.Info("worktree recorded", "project_id", id, "path", root, "git_common_dir", commonDir)
	p, err := s.GetByID(ctx, id)
	return Opened{Project: p, Registered: registered, Dropped: dropped}, err
}

func (s *Store) mismatch(ctx context.Context, root, origin, byCloneID, byOriginID string) error {
	byClone, err := s.GetByID(ctx, byCloneID)
	if err != nil {
		return err
	}
	byOrigin, err := s.GetByID(ctx, byOriginID)
	if err != nil {
		return err
	}
	return &ProjectMismatchError{Root: root, Origin: origin, ByClone: byClone, ByOrigin: byOrigin}
}

// OpenGit opens the project whose origin is url. A known project is opened
// from one of its checkouts; if none is left on disk, a *CheckoutMissingError
// is returned. An unknown repository is cloned into the projects directory,
// writing git's output to progress, and opened from there.
//
// name names the project as in OpenLocal; when empty, a new project is named
// after the last element of the URL path.
func (s *Store) OpenGit(ctx context.Context, url, name string, progress io.Writer) (Opened, error) {
	key := git.RepoKey(url)
	if key == "" {
		return Opened{}, ErrEmptyURL
	}
	log := logging.FromContext(ctx)
	id, err := queryID(ctx, s.db, "SELECT id FROM projects WHERE origin_key = ?", key)
	if err != nil {
		return Opened{}, err
	}
	log.Info("origin looked up", "url", url, "origin_key", key, "project_id", id)
	if id != "" {
		return s.openKnown(ctx, id, name)
	}

	if name == "" {
		name = path.Base(key)
	}
	if err := s.checkName(ctx, s.db, name, ""); err != nil {
		return Opened{}, err
	}
	if err := checkDirName(name); err != nil {
		return Opened{}, err
	}
	dest := filepath.Join(s.projectsDir, name)
	if _, err := os.Stat(dest); err == nil {
		// Left behind by a removed project: reuse it if it is the same repository.
		origin, err := git.RemoteURL(ctx, dest, "origin")
		if err != nil {
			return Opened{}, err
		}
		ok, err := isCheckout(ctx, dest)
		if err != nil {
			return Opened{}, err
		}
		log.Info("clone path taken", "path", dest, "is_checkout", ok, "origin", origin)
		if !ok || git.RepoKey(origin) != key {
			return Opened{}, &PathExistsError{Path: dest}
		}
		log.Info("existing checkout reused", "path", dest)
		return s.OpenLocal(ctx, dest, name)
	}
	return s.cloneAndOpen(ctx, url, dest, name, progress)
}

// Reclone clones the project with id back into its clone path,
// for when none of its checkouts are left on disk.
func (s *Store) Reclone(ctx context.Context, id string, progress io.Writer) (Opened, error) {
	p, err := s.GetByID(ctx, id)
	if err != nil {
		return Opened{}, err
	}
	if p.OriginURL == "" {
		return Opened{}, fmt.Errorf("%w: %s", ErrNoOrigin, p.Name)
	}
	dest := cmp.Or(p.ClonePath(), filepath.Join(s.projectsDir, p.Name))
	if _, err := os.Stat(dest); err == nil {
		return Opened{}, &PathExistsError{Path: dest}
	}
	logging.FromContext(ctx).Info("recloning", "project_id", p.ID, "dest", dest)
	return s.cloneAndOpen(ctx, p.OriginURL, dest, "", progress)
}

// Create creates an empty git repository at parent/name and opens it as a new
// project named name; parent defaults to the projects directory. An existing
// directory that is not in a git repository is initialized in place. A taken
// name returns a *NameTakenError, a destination in a git repository a
// *RepoExistsError.
func (s *Store) Create(ctx context.Context, name, parent string) (Opened, error) {
	if name == "" {
		return Opened{}, &InvalidNameError{Name: name, Reason: "empty"}
	}
	if err := checkDirName(name); err != nil {
		return Opened{}, err
	}
	if err := s.checkName(ctx, s.db, name, ""); err != nil {
		return Opened{}, err
	}

	dest, err := filepath.Abs(filepath.Join(cmp.Or(parent, s.projectsDir), name))
	if err != nil {
		return Opened{}, err
	}
	log := logging.FromContext(ctx)
	existing, err := existingAncestor(dest)
	if err != nil {
		return Opened{}, err
	}
	log.Info("destination resolved", "dest", dest, "existing_ancestor", existing)
	if root, err := git.TopLevel(ctx, existing); err == nil {
		return Opened{}, s.repoExists(ctx, dest, root)
	} else if !errors.Is(err, git.ErrNotRepo) {
		return Opened{}, err
	}

	// Undo only what Create made: the first missing directory, or else .git.
	created := filepath.Join(dest, ".git")
	if existing == dest {
		if _, err := os.Lstat(created); err == nil {
			return Opened{}, &InvalidPathError{Path: created, Reason: "exists but is not a git repository"}
		}
	} else {
		rel, err := filepath.Rel(existing, dest)
		if err != nil {
			return Opened{}, err
		}
		created = filepath.Join(existing, strings.Split(rel, string(filepath.Separator))[0])
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return Opened{}, err
	}
	log.Info("directory created", "path", dest)
	o, err := s.initAndOpen(ctx, dest, name)
	if err != nil {
		if rmErr := os.RemoveAll(created); rmErr != nil {
			log.Error("undo failed", "path", created, "err", rmErr)
			return Opened{}, errors.Join(err, rmErr)
		}
		log.Info("creation undone", "removed", created, "err", err)
		return Opened{}, err
	}
	return o, nil
}

func (s *Store) initAndOpen(ctx context.Context, dest, name string) (Opened, error) {
	if err := git.Init(ctx, dest); err != nil {
		return Opened{}, err
	}
	return s.OpenLocal(ctx, dest, name)
}

func (s *Store) repoExists(ctx context.Context, dest, root string) error {
	e := &RepoExistsError{Dest: dest, Root: root}
	id, err := queryID(ctx, s.db, "SELECT project_id FROM project_worktrees WHERE path = ?", root)
	if err != nil || id == "" {
		return cmp.Or(err, error(e))
	}
	p, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}
	e.Project = &p
	return e
}

// Open opens the known project with id from one of its
// checkouts; if none is left on disk, a *CheckoutMissingError is returned.
func (s *Store) Open(ctx context.Context, id string) (Opened, error) {
	p, err := s.GetByID(ctx, id)
	if err != nil {
		return Opened{}, err
	}
	return s.openKnown(ctx, p.ID, "")
}

func (s *Store) openKnown(ctx context.Context, id, name string) (Opened, error) {
	p, err := s.GetByID(ctx, id)
	if err != nil {
		return Opened{}, err
	}
	for _, w := range p.Worktrees {
		ok, err := isCheckout(ctx, w.Path)
		if err != nil {
			return Opened{}, err
		}
		if ok {
			logging.FromContext(ctx).Info("checkout chosen", "project_id", id, "path", w.Path)
			return s.OpenLocal(ctx, w.Path, name)
		}
	}
	logging.FromContext(ctx).Info("no checkout left", "project_id", id, "worktrees", len(p.Worktrees))
	return Opened{}, &CheckoutMissingError{Project: p}
}

// cloneAndOpen clones url beside dest and moves it into place once whole,
// so that dest is never a clone cut short, then opens it.
func (s *Store) cloneAndOpen(ctx context.Context, url, dest, name string, progress io.Writer) (Opened, error) {
	log := logging.FromContext(ctx)
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Opened{}, err
	}
	removeCloneLeftovers(ctx, parent)
	tmp, err := os.MkdirTemp(parent, "."+filepath.Base(dest)+cloneSuffix)
	if err != nil {
		return Opened{}, err
	}
	// As git would make it, not private as a temporary directory is.
	if err := os.Chmod(tmp, 0o755); err != nil {
		removeClone(ctx, tmp)
		return Opened{}, err
	}
	if err := git.Clone(ctx, url, tmp, progress); err != nil {
		removeClone(ctx, tmp)
		return Opened{}, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		removeClone(ctx, tmp)
		return Opened{}, err
	}
	log.Info("clone moved into place", "from", tmp, "path", dest)
	o, err := s.OpenLocal(ctx, dest, name)
	o.Cloned = err == nil
	return o, err
}

// cloneSuffix marks a clone in progress, made beside where it goes as
// .<name>.clone-<random>.
const cloneSuffix = ".clone-"

// removeCloneLeftovers removes the clones in parent that a daemon dying
// midway left behind; changes run one at a time, so none is in progress.
func removeCloneLeftovers(ctx context.Context, parent string) {
	left, err := filepath.Glob(filepath.Join(parent, ".*"+cloneSuffix+"*"))
	if err != nil {
		return // the pattern is valid
	}
	for _, dir := range left {
		removeClone(ctx, dir)
	}
}

func removeClone(ctx context.Context, dir string) {
	log := logging.FromContext(ctx)
	if err := os.RemoveAll(dir); err != nil {
		log.Error("unfinished clone not removed", "path", dir, "err", err)
		return
	}
	log.Info("unfinished clone removed", "path", dir)
}

// GetByID returns the project with id, whatever its state.
func (s *Store) GetByID(ctx context.Context, id string) (Project, error) {
	return s.get(ctx, "id", id)
}

// GetByName returns the project named name, ignoring case, whatever its state.
func (s *Store) GetByName(ctx context.Context, name string) (Project, error) {
	return s.get(ctx, "name", name)
}

// GetByPath returns the project one of whose worktrees holds path, whatever
// its state. Worktrees can nest, so the deepest one wins.
func (s *Store) GetByPath(ctx context.Context, path string) (Project, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT project_id, path FROM project_worktrees")
	if err != nil {
		return Project{}, err
	}
	defer rows.Close()
	forms := pathForms(path)
	var id, deepest string
	for rows.Next() {
		var projectID, root string
		if err := rows.Scan(&projectID, &root); err != nil {
			return Project{}, err
		}
		if len(root) > len(deepest) && within(root, forms) {
			id, deepest = projectID, root
		}
	}
	if err := rows.Err(); err != nil {
		return Project{}, err
	}
	logging.FromContext(ctx).Info("project looked up by path", "path", path, "worktree", deepest, "project_id", id)
	if id == "" {
		return Project{}, fmt.Errorf("%w: no project holds %s", ErrNotFound, path)
	}
	return s.GetByID(ctx, id)
}

// get returns the project whose column equals value; column is a constant.
func (s *Store) get(ctx context.Context, column, value string) (Project, error) {
	var p Project
	var origin sql.NullString
	var created, updated int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, origin_url, state, created_at, updated_at FROM projects
		WHERE `+column+` = ?`, value).
		Scan(&p.ID, &p.Name, &origin, &p.State, &created, &updated)
	logging.FromContext(ctx).Debug("project looked up", "by", column, "value", value, "found", err == nil)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, fmt.Errorf("%w: %q", ErrNotFound, value)
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

// List returns the projects in state, or all of them when state is "",
// sorted by name.
func (s *Store) List(ctx context.Context, state State) ([]Summary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.name, p.origin_url, p.state, p.created_at, p.updated_at,
			(SELECT count(*) FROM project_worktrees w WHERE w.project_id = p.id)
		FROM projects p
		WHERE ?1 = '' OR p.state = ?1
		ORDER BY p.name, p.id`, state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Summary
	for rows.Next() {
		var p Summary
		var origin sql.NullString
		var created, updated int64
		if err := rows.Scan(&p.ID, &p.Name, &origin, &p.State, &created, &updated, &p.WorktreeCount); err != nil {
			return nil, err
		}
		p.OriginURL = origin.String
		p.CreatedAt, p.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
		list = append(list, p)
	}
	logging.FromContext(ctx).Debug("projects listed", "state", state, "count", len(list))
	return list, rows.Err()
}

// DiskStatus is which of a project's checkouts are still on disk.
type DiskStatus struct {
	Present  map[string]bool // by worktree id
	Orphaned bool            // none of the checkouts is on disk
}

// DiskStatus checks which of p's checkouts are still on disk. Unlike opening
// the project, it changes nothing.
func (s *Store) DiskStatus(ctx context.Context, p Project) (DiskStatus, error) {
	st := DiskStatus{Present: make(map[string]bool, len(p.Worktrees)), Orphaned: true}
	for _, w := range p.Worktrees {
		ok, err := isCheckout(ctx, w.Path)
		if err != nil {
			return DiskStatus{}, err
		}
		st.Present[w.ID] = ok
		if ok {
			st.Orphaned = false
		}
	}
	return st, nil
}

// Close marks the project with id as closed.
func (s *Store) Close(ctx context.Context, id string) (Project, error) {
	p, err := s.GetByID(ctx, id)
	if err != nil {
		return Project{}, err
	}
	if _, err := db.Exec(ctx, s.db, "UPDATE projects SET state = ?, updated_at = ? WHERE id = ?",
		StateClosed, time.Now().UnixMilli(), p.ID); err != nil {
		return Project{}, err
	}
	logging.FromContext(ctx).Info("project closed", "project_id", p.ID, "name", p.Name)
	return s.GetByID(ctx, p.ID)
}

// Remove deletes the project with id along with everything
// that belongs to it. Files on disk are not touched.
func (s *Store) Remove(ctx context.Context, id string) (Project, error) {
	p, err := s.GetByID(ctx, id)
	if err != nil {
		return Project{}, err
	}
	// Related rows go via ON DELETE CASCADE.
	if _, err := db.Exec(ctx, s.db, "DELETE FROM projects WHERE id = ?", p.ID); err != nil {
		return Project{}, err
	}
	logging.FromContext(ctx).Info("project removed", "project_id", p.ID, "name", p.Name, "worktrees", len(p.Worktrees))
	return p, nil
}

// dropMissing deletes the project's checkouts that are no longer on disk and
// returns them.
func dropMissing(ctx context.Context, tx *sql.Tx, projectID string) ([]Worktree, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, path, git_common_dir, created_at FROM project_worktrees
		WHERE project_id = ? ORDER BY created_at, path`, projectID)
	if err != nil {
		return nil, err
	}
	var all []Worktree
	for rows.Next() {
		var w Worktree
		var created int64
		if err := rows.Scan(&w.ID, &w.Path, &w.GitCommonDir, &created); err != nil {
			rows.Close()
			return nil, err
		}
		w.CreatedAt = time.UnixMilli(created)
		all = append(all, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var dropped []Worktree
	for _, w := range all {
		ok, err := isCheckout(ctx, w.Path)
		if err != nil {
			return nil, err
		}
		if ok {
			continue
		}
		if _, err := db.Exec(ctx, tx, "DELETE FROM project_worktrees WHERE id = ?", w.ID); err != nil {
			return nil, err
		}
		logging.FromContext(ctx).Info("worktree dropped", "project_id", projectID, "worktree_id", w.ID, "path", w.Path)
		dropped = append(dropped, w)
	}
	return dropped, nil
}

// queryRower is satisfied by *sql.DB and *sql.Tx.
type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// checkName returns an *InvalidNameError if name contains a colon, which
// marks an id reference such as "id:proj-1a2b3c4d", or a *NameTakenError if
// a project other than self already uses name.
func (s *Store) checkName(ctx context.Context, q queryRower, name, self string) error {
	if strings.Contains(name, ":") {
		return &InvalidNameError{Name: name, Reason: `cannot contain ":"`}
	}
	other, err := queryID(ctx, q, "SELECT id FROM projects WHERE name = ? AND id <> ?", name, self)
	if err != nil || other == "" {
		return err
	}
	logging.FromContext(ctx).Info("name taken", "name", name, "project_id", other)
	p, err := s.GetByID(ctx, other)
	if err != nil {
		return err
	}
	return &NameTakenError{Name: name, Project: p}
}

// queryID returns the single string column of the first row, or "" if none.
func queryID(ctx context.Context, q queryRower, query string, args ...any) (string, error) {
	var id string
	err := q.QueryRowContext(ctx, query, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	logging.FromContext(ctx).Debug("sql query", "query", query, "args", args, "id", id)
	return id, err
}

func nullable(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
