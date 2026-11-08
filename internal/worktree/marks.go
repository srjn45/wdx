package worktree

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/sqlite"
)

// contentDeleted is the content of a file verified as deleted.
const contentDeleted = "deleted"

// A mark holds what a human looked at when they marked a file verified. Nothing
// watches for it to change: each read of a checkout compares the file's content
// with the mark's, and a mark that differs is gone (docs/TIDE_WORKTREES.md). It is
// a label, which nothing else depends on.
type Mark struct {
	Worktree  string // the checkout's path
	File      string // relative to it
	ContentID string
}

// Marks keeps the marks.
type Marks struct {
	db *sql.DB
}

// NewMarks returns the marks kept in db.
func NewMarks(db *sql.DB) *Marks { return &Marks{db: db} }

// List returns the project's marks.
func (m *Marks) List(ctx context.Context, projectID string) ([]Mark, error) {
	rows, err := m.db.QueryContext(ctx, "SELECT worktree, file, content_id FROM verified_files WHERE project_id = ?", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Mark
	for rows.Next() {
		var mark Mark
		if err := rows.Scan(&mark.Worktree, &mark.File, &mark.ContentID); err != nil {
			return nil, err
		}
		list = append(list, mark)
	}
	return list, rows.Err()
}

// Set marks a file verified.
func (m *Marks) Set(ctx context.Context, projectID string, mark Mark) error {
	_, err := sqlite.Exec(ctx, m.db, `
		INSERT INTO verified_files (project_id, worktree, file, content_id, verified_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (project_id, worktree, file) DO UPDATE SET content_id = excluded.content_id, verified_at = excluded.verified_at`,
		projectID, mark.Worktree, mark.File, mark.ContentID, time.Now().UnixMilli())
	if err == nil {
		logging.FromContext(ctx).Info("file marked verified", "project_id", projectID, "path", mark.Worktree, "file", mark.File)
	}
	return err
}

// Remove takes a mark away.
func (m *Marks) Remove(ctx context.Context, projectID, worktree, file string) error {
	_, err := sqlite.Exec(ctx, m.db, "DELETE FROM verified_files WHERE project_id = ? AND worktree = ? AND file = ?", projectID, worktree, file)
	if err == nil {
		logging.FromContext(ctx).Info("verified mark removed", "project_id", projectID, "path", worktree, "file", file)
	}
	return err
}

// contentOf returns the content of the file at path in the checkout as git would
// store it, which stays the same once it is committed. known is false for what has
// no such content to compare: a symbolic link, whose target git would read, or a
// submodule.
func contentOf(ctx context.Context, checkout, path string) (id string, known bool, err error) {
	info, err := os.Lstat(filepath.Join(checkout, path))
	if errors.Is(err, os.ErrNotExist) {
		return contentDeleted, true, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", false, nil
	}
	id, err = git.ContentID(ctx, checkout, path)
	return id, err == nil, err
}

// applyMarks sets what s has verified from the marks, and returns those that no
// longer hold: the file was edited, or has nothing left to verify. With the commits
// not known, as when reading them failed, a file not among the changes may be among
// those, so its mark stays.
func applyMarks(ctx context.Context, s *Status, marks []Mark) (stale []Mark) {
	for i := range s.Files {
		s.Files[i].Verified = false
	}
	for i := range s.Committed {
		s.Committed[i].Verified = false
	}
	for _, m := range marks {
		if m.Worktree != s.Path {
			continue
		}
		var verified *bool
		for i := range s.Files {
			if s.Files[i].Path == m.File {
				verified = &s.Files[i].Verified
			}
		}
		for i := range s.Committed {
			if s.Committed[i].Path == m.File && verified == nil {
				verified = &s.Committed[i].Verified
			}
		}
		if verified == nil {
			if s.Base == "" || s.Ahead != nil {
				stale = append(stale, m)
			}
			continue
		}
		id, known, err := contentOf(ctx, s.Path, m.File)
		switch {
		case err != nil:
			logging.FromContext(ctx).Info("verified file not read", "path", s.Path, "file", m.File, "err", err)
			*verified = true // not known to have changed
		case !known || id == m.ContentID:
			*verified = true
		default:
			stale = append(stale, m)
		}
	}
	return stale
}

// Verify marks the file at path in the checkout called name verified, with the
// content seen as contentID: a mark for what was on the screen, which an edit since
// makes stale at once. "" takes the content it has now. It returns the checkout.
func Verify(ctx context.Context, marks *Marks, projectID string, c Checkouts, name, path, contentID string) (Status, error) {
	s, err := findFile(ctx, c, name, path)
	if err != nil {
		return Status{}, err
	}
	if contentID == "" {
		id, known, err := contentOf(ctx, s.Path, path)
		if err != nil {
			return Status{}, err
		}
		if !known {
			id = "unknown"
		}
		contentID = id
	}
	if strings.Trim(contentID, "0") == "" { // git's id of no content: the file is deleted
		contentID = contentDeleted
	}
	return s, marks.Set(ctx, projectID, Mark{Worktree: s.Path, File: path, ContentID: contentID})
}

// Unverify takes the mark of the file at path in the checkout called name away.
func Unverify(ctx context.Context, marks *Marks, projectID string, c Checkouts, name, path string) (Status, error) {
	s, err := findFile(ctx, c, name, path)
	if err != nil {
		return Status{}, err
	}
	return s, marks.Remove(ctx, projectID, s.Path, path)
}

func findFile(ctx context.Context, c Checkouts, name, path string) (Status, error) {
	if !filepath.IsLocal(path) {
		return Status{}, &InvalidPathError{Path: path}
	}
	return Find(ctx, c, name)
}
