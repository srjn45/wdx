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
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/srjn45/warden/internal/git"
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

// Summary is a project as listed, without its worktrees.
type Summary struct {
	ID            string
	Name          string
	OriginURL     string // "" when the repo has no origin remote
	State         State
	WorktreeCount int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Worktree is a local checkout of a project: a clone or a linked worktree.
type Worktree struct {
	ID           string
	Path         string // root of the work tree
	GitCommonDir string // shared by all worktrees of one clone
	CreatedAt    time.Time
}

// ClonePath is where the project was first cloned: its earliest checkout that
// is not a linked worktree.
func (p Project) ClonePath() string {
	for _, w := range p.Worktrees {
		if w.GitCommonDir == filepath.Join(w.Path, ".git") {
			return w.Path
		}
	}
	if len(p.Worktrees) > 0 {
		return p.Worktrees[0].Path
	}
	return ""
}

// Opened is the result of opening a project.
type Opened struct {
	Project
	Registered bool       // the project did not exist before
	Cloned     bool       // the repository was cloned to open it
	Dropped    []Worktree // checkouts no longer on disk, removed from the project
}

// CheckoutMissingError is returned when none of a known project's checkouts
// exist on disk anymore. Reclone or Remove resolves it.
type CheckoutMissingError struct {
	Project Project
}

func (e *CheckoutMissingError) Error() string {
	return fmt.Sprintf("project %s: no checkout found on disk", e.Project.Name)
}

// NameTakenError is returned when a project would get a name another project
// already uses.
type NameTakenError struct {
	Name    string
	Project Project
}

func (e *NameTakenError) Error() string {
	return fmt.Sprintf("project name %q is already used by project %s", e.Name, e.Project.ID)
}

// RepoExistsError is returned when creating a project at Dest, which is or is
// inside the git repository Root. Project is set when Root is a known checkout.
type RepoExistsError struct {
	Dest    string
	Root    string
	Project *Project
}

func (e *RepoExistsError) Error() string {
	msg := fmt.Sprintf("%s is already a git repository", e.Dest)
	if e.Root != e.Dest {
		msg = fmt.Sprintf("%s is inside git repository %s", e.Dest, e.Root)
	}
	if e.Project != nil {
		msg += fmt.Sprintf(" (project %s)", e.Project.Name)
	}
	return msg
}

// InvalidNameError is returned for a project name that cannot be used.
type InvalidNameError struct {
	Name   string
	Reason string
}

func (e *InvalidNameError) Error() string {
	return fmt.Sprintf("invalid project name %q: %s", e.Name, e.Reason)
}

// InvalidPathError is returned for a path that cannot be used as asked.
type InvalidPathError struct {
	Path   string
	Reason string
}

func (e *InvalidPathError) Error() string {
	return e.Path + ": " + e.Reason
}

// PathExistsError is returned when a project would be cloned to a path that
// is already taken by something else.
type PathExistsError struct {
	Path string
}

func (e *PathExistsError) Error() string {
	return e.Path + " already exists"
}

// ProjectMismatchError is returned when the work tree at Root is a checkout of
// project ByClone but its origin belongs to project ByOrigin.
type ProjectMismatchError struct {
	Root     string
	Origin   string
	ByClone  Project
	ByOrigin Project
}

func (e *ProjectMismatchError) Error() string {
	return fmt.Sprintf("%s is a worktree of project %s but its origin %s belongs to project %s",
		e.Root, e.ByClone.Name, e.Origin, e.ByOrigin.Name)
}

// ErrEmptyURL is returned when opening an empty git URL.
var ErrEmptyURL = errors.New("git URL is empty")

// ErrNotGitRepo is returned when opening a directory that is not a git repository.
var ErrNotGitRepo = errors.New("not a git repository")

// ErrNotFound is returned when no project matches a name or id.
var ErrNotFound = errors.New("project not found")

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
		if id, err = newID("proj"); err != nil {
			return Opened{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO projects (id, name, origin_url, origin_key, state, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, name, nullable(origin), nullable(key), StateOpen, now, now); err != nil {
			return Opened{}, err
		}
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
		if _, err := tx.ExecContext(ctx, `
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
	}

	// A known path is re-pointed, in case it was deleted and re-cloned.
	wtID, err := newID("wt")
	if err != nil {
		return Opened{}, err
	}
	if _, err := tx.ExecContext(ctx, `
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
	p, err := s.Get(ctx, id)
	return Opened{Project: p, Registered: registered, Dropped: dropped}, err
}

func (s *Store) mismatch(ctx context.Context, root, origin, byCloneID, byOriginID string) error {
	byClone, err := s.Get(ctx, byCloneID)
	if err != nil {
		return err
	}
	byOrigin, err := s.Get(ctx, byOriginID)
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
	var id string
	err := s.db.QueryRowContext(ctx, "SELECT id FROM projects WHERE origin_key = ?", key).Scan(&id)
	if err == nil {
		return s.openKnown(ctx, id, name)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Opened{}, err
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
		if !ok || git.RepoKey(origin) != key {
			return Opened{}, &PathExistsError{Path: dest}
		}
		return s.OpenLocal(ctx, dest, name)
	}
	return s.cloneAndOpen(ctx, url, dest, name, progress)
}

// Reclone clones the project whose id or name is ref back into its clone path,
// for when none of its checkouts are left on disk.
func (s *Store) Reclone(ctx context.Context, ref string, progress io.Writer) (Opened, error) {
	p, err := s.Get(ctx, ref)
	if err != nil {
		return Opened{}, err
	}
	if p.OriginURL == "" {
		return Opened{}, fmt.Errorf("project %s has no origin to clone from", p.Name)
	}
	dest := cmp.Or(p.ClonePath(), filepath.Join(s.projectsDir, p.Name))
	if _, err := os.Stat(dest); err == nil {
		return Opened{}, &PathExistsError{Path: dest}
	}
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
	existing, err := existingAncestor(dest)
	if err != nil {
		return Opened{}, err
	}
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
	o, err := s.initAndOpen(ctx, dest, name)
	if err != nil {
		return Opened{}, errors.Join(err, os.RemoveAll(created))
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
	p, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	e.Project = &p
	return e
}

// existingAncestor returns path or its nearest ancestor that exists, which
// must be a directory.
func existingAncestor(path string) (string, error) {
	for {
		info, err := os.Stat(path)
		if err == nil {
			if !info.IsDir() {
				return "", &InvalidPathError{Path: path, Reason: "not a directory"}
			}
			return path, nil
		}
		// ENOTDIR: an ancestor is a file, which the loop reaches and reports.
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		path = parent
	}
}

// Open opens the known project whose id or name is ref from one of its
// checkouts; if none is left on disk, a *CheckoutMissingError is returned.
func (s *Store) Open(ctx context.Context, ref string) (Opened, error) {
	p, err := s.Get(ctx, ref)
	if err != nil {
		return Opened{}, err
	}
	return s.openKnown(ctx, p.ID, "")
}

func (s *Store) openKnown(ctx context.Context, id, name string) (Opened, error) {
	p, err := s.Get(ctx, id)
	if err != nil {
		return Opened{}, err
	}
	for _, w := range p.Worktrees {
		ok, err := isCheckout(ctx, w.Path)
		if err != nil {
			return Opened{}, err
		}
		if ok {
			return s.OpenLocal(ctx, w.Path, name)
		}
	}
	return Opened{}, &CheckoutMissingError{Project: p}
}

func (s *Store) cloneAndOpen(ctx context.Context, url, dest, name string, progress io.Writer) (Opened, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return Opened{}, err
	}
	if err := git.Clone(ctx, url, dest, progress); err != nil {
		return Opened{}, err
	}
	o, err := s.OpenLocal(ctx, dest, name)
	o.Cloned = err == nil
	return o, err
}

// Get returns the project whose id or name is ref. An id match wins.
func (s *Store) Get(ctx context.Context, ref string) (Project, error) {
	p, err := s.GetByID(ctx, ref)
	if errors.Is(err, ErrNotFound) {
		return s.GetByName(ctx, ref)
	}
	return p, err
}

// GetByID returns the project with id, whatever its state.
func (s *Store) GetByID(ctx context.Context, id string) (Project, error) {
	return s.get(ctx, "id", id)
}

// GetByName returns the project named name, ignoring case, whatever its state.
func (s *Store) GetByName(ctx context.Context, name string) (Project, error) {
	return s.get(ctx, "name", name)
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
		if _, err := tx.ExecContext(ctx, "DELETE FROM project_worktrees WHERE id = ?", w.ID); err != nil {
			return nil, err
		}
		dropped = append(dropped, w)
	}
	return dropped, nil
}

// isCheckout reports whether path is still the root of a git work tree.
func isCheckout(ctx context.Context, path string) (bool, error) {
	root, err := git.TopLevel(ctx, path)
	if errors.Is(err, git.ErrNotRepo) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, err
	}
	return root == real, nil
}

// queryRower is satisfied by *sql.DB and *sql.Tx.
type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// checkName returns a *NameTakenError if a project other than self already
// uses name.
func (s *Store) checkName(ctx context.Context, q queryRower, name, self string) error {
	other, err := queryID(ctx, q, "SELECT id FROM projects WHERE name = ? AND id <> ?", name, self)
	if err != nil || other == "" {
		return err
	}
	p, err := s.Get(ctx, other)
	if err != nil {
		return err
	}
	return &NameTakenError{Name: name, Project: p}
}

// checkDirName fails if name cannot be used as a directory name.
func checkDirName(name string) error {
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return &InvalidNameError{Name: name, Reason: "cannot be used as a directory name"}
	}
	return nil
}

// queryID returns the single string column of the first row, or "" if none.
func queryID(ctx context.Context, q queryRower, query string, args ...any) (string, error) {
	var id string
	err := q.QueryRowContext(ctx, query, args...).Scan(&id)
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
