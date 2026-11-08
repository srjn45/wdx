// Package tidestate keeps what tide shows, so that it comes back to it and a
// removal made through wdx reaches it whether tide runs or not
// (docs/TIDE_STATE.md): the tabs open in each project.
package tidestate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/logging"
)

// Kind is what a tab shows.
type Kind string

const (
	KindAgent    Kind = "agent"    // an agent's terminal
	KindInfo     Kind = "info"     // what wdx knows of an agent
	KindSettings Kind = "settings" // the settings
	KindFile     Kind = "file"     // a worktree's file
	KindDiff     Kind = "diff"     // the diff of a worktree's file
	KindPR       Kind = "pr"       // what a worktree changed
	KindDoc      Kind = "doc"      // a doc
	KindPlan     Kind = "plan"     // a plan, its id in DocID as plans are kept as docs
	KindStats    Kind = "stats"    // the stats of the AI coding agents
)

// Tab is a tab tide has open. A field its kind does not take is "".
type Tab struct {
	Kind     Kind
	AgentID  string // agent and info
	DocID    string // doc
	Worktree string // file, diff and pr: the worktree's name, main for the clone
	Path     string // file and diff: relative to the worktree
}

// InvalidError is a list of tabs that cannot be kept: Index is the tab's place in it.
type InvalidError struct {
	Index  int
	Reason string
}

func (e *InvalidError) Error() string {
	return fmt.Sprintf("tab %d: %s", e.Index, e.Reason)
}

// Store keeps the tabs.
type Store struct {
	db *sql.DB
}

// NewStore returns the store kept in database.
func NewStore(database *sql.DB) *Store { return &Store{db: database} }

// Tabs returns the project's tabs in their order, and the revision they are at:
// how many changes they had, which a later read never has fewer of.
func (s *Store) Tabs(ctx context.Context, projectID string) ([]Tab, int64, error) {
	// One transaction, so that the revision is the tabs'.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	tabs, err := readTabs(ctx, tx, projectID)
	if err != nil {
		return nil, 0, err
	}
	revision, err := readRevision(ctx, tx, projectID)
	return tabs, revision, err
}

func readTabs(ctx context.Context, tx *sql.Tx, projectID string) ([]Tab, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT kind, coalesce(agent_id, ''), coalesce(doc_id, ''), coalesce(worktree, ''), coalesce(path, '')
		FROM tide_tabs WHERE project_id = ? ORDER BY position`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tabs := []Tab{}
	for rows.Next() {
		var t Tab
		if err := rows.Scan(&t.Kind, &t.AgentID, &t.DocID, &t.Worktree, &t.Path); err != nil {
			return nil, err
		}
		tabs = append(tabs, t)
	}
	return tabs, rows.Err()
}

// readRevision is the revision of the project's tabs, 0 before any change.
func readRevision(ctx context.Context, tx *sql.Tx, projectID string) (int64, error) {
	var revision int64
	err := tx.QueryRowContext(ctx, "SELECT revision FROM tide_tab_revisions WHERE project_id = ?", projectID).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return revision, err
}

// Replace makes tabs the project's tabs, in their order, and returns the revision
// they are at then. A tab its kind does not fit, one twice, or an agent or a doc
// not the project's returns an *InvalidError.
func (s *Store) Replace(ctx context.Context, projectID string, tabs []Tab) (int64, error) {
	for i, t := range tabs {
		if err := check(t); err != nil {
			return 0, &InvalidError{Index: i, Reason: err.Error()}
		}
		if slices.Contains(tabs[:i], t) {
			return 0, &InvalidError{Index: i, Reason: "the same tab as an earlier one"}
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for i, t := range tabs {
		if why, err := notOwned(ctx, tx, projectID, t); err != nil {
			return 0, err
		} else if why != "" {
			return 0, &InvalidError{Index: i, Reason: why}
		}
	}
	if _, err := db.Exec(ctx, tx, "DELETE FROM tide_tabs WHERE project_id = ?", projectID); err != nil {
		return 0, err
	}
	for i, t := range tabs {
		if _, err := db.Exec(ctx, tx, `
			INSERT INTO tide_tabs (project_id, position, kind, agent_id, doc_id, worktree, path) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			projectID, i, t.Kind, null(t.AgentID), null(t.DocID), null(t.Worktree), null(t.Path)); err != nil {
			return 0, err
		}
	}
	revision, err := readRevision(ctx, tx, projectID)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	logging.FromContext(ctx).Info("tabs replaced", "project_id", projectID, "count", len(tabs), "revision", revision)
	return revision, nil
}

// RemoveWorktree deletes the tabs of the worktree called name, once wdx removed it.
func (s *Store) RemoveWorktree(ctx context.Context, projectID, name string) error {
	n, err := db.Exec(ctx, s.db, "DELETE FROM tide_tabs WHERE project_id = ? AND worktree = ?", projectID, name)
	if err != nil {
		return err
	}
	logging.FromContext(ctx).Info("worktree's tabs removed", "project_id", projectID, "worktree", name, "count", n)
	return nil
}

// RemovePath deletes the tabs of the file at path in the worktree called name, or
// of every file under it when it is a directory, once wdx deleted it.
func (s *Store) RemovePath(ctx context.Context, projectID, name, path string) error {
	n, err := db.Exec(ctx, s.db, "DELETE FROM tide_tabs WHERE project_id = ? AND worktree = ? AND "+atOrUnder,
		projectID, name, path)
	if err != nil {
		return err
	}
	logging.FromContext(ctx).Info("path's tabs removed", "project_id", projectID, "worktree", name, "path", path, "count", n)
	return nil
}

// RenamePath moves the tabs of the file at from in the worktree called name, or of
// every file under it when it is a directory, to to, once wdx renamed it.
func (s *Store) RenamePath(ctx context.Context, projectID, name, from, to string) error {
	n, err := db.Exec(ctx, s.db, `UPDATE tide_tabs SET path = ?4 || substr(path, length(?3) + 1)
		WHERE project_id = ?1 AND worktree = ?2 AND `+atOrUnder,
		projectID, name, from, to)
	if err != nil {
		return err
	}
	logging.FromContext(ctx).Info("path's tabs renamed", "project_id", projectID, "worktree", name, "path", from, "new_path", to, "count", n)
	return nil
}

// atOrUnder matches a tab's path that is the third argument or under it. It
// compares prefixes rather than using LIKE, which would read a _ or % in a path.
const atOrUnder = "(path = ?3 OR substr(path, 1, length(?3) + 1) = ?3 || '/')"

// check fails for a tab whose kind is unknown or that has the wrong fields for it.
func check(t Tab) error {
	agent, doc, worktree, path := false, false, false, false
	switch t.Kind {
	case KindAgent, KindInfo:
		agent = true
	case KindSettings, KindStats:
	case KindFile, KindDiff:
		worktree, path = true, true
	case KindPR:
		worktree = true
	case KindDoc, KindPlan:
		doc = true
	default:
		return fmt.Errorf("%q is not a kind of tab", t.Kind)
	}
	for _, f := range []struct {
		name  string
		value string
		takes bool
	}{{"agent_id", t.AgentID, agent}, {"doc_id", t.DocID, doc}, {"worktree", t.Worktree, worktree}, {"path", t.Path, path}} {
		switch {
		case f.takes && f.value == "":
			return fmt.Errorf("a %s tab needs %s", t.Kind, f.name)
		case !f.takes && f.value != "":
			return fmt.Errorf("a %s tab has no %s", t.Kind, f.name)
		}
	}
	if path && !relative(t.Path) {
		return fmt.Errorf("path %q is not a clean path inside the worktree", t.Path)
	}
	return nil
}

// relative reports whether path is clean and inside the directory it is relative to.
func relative(path string) bool {
	return filepath.Clean(path) == path && path != "." && !filepath.IsAbs(path) &&
		path != ".." && !strings.HasPrefix(path, "../")
}

// notOwned says why a tab of an agent, a doc or a plan cannot be the project's: it is
// another's. It is "" when it can. An agent kept as a tombstone is still the
// project's: tide opens one to read what it did, and the trigger closes only
// the tabs open when it became one.
func notOwned(ctx context.Context, tx *sql.Tx, projectID string, t Tab) (string, error) {
	var query, what, id string
	switch {
	case t.AgentID != "":
		query, what, id = `SELECT count(*) FROM agents a JOIN terminals t ON t.id = a.terminal_id
			WHERE a.id = ? AND t.project_id = ?`, "agent", t.AgentID
	case t.DocID != "":
		// kind is doc or plan, as check let through, and so is docs.kind.
		query, what, id = "SELECT count(*) FROM docs WHERE id = ? AND project_id = ? AND kind = '"+string(t.Kind)+"'", string(t.Kind), t.DocID
	default:
		return "", nil
	}
	var n int
	if err := tx.QueryRowContext(ctx, query, id, projectID).Scan(&n); err != nil {
		return "", err
	}
	if n == 0 {
		return fmt.Sprintf("the project has no %s %s", what, id), nil
	}
	return "", nil
}

// null is s, or NULL when it is "".
func null(s string) any {
	if s == "" {
		return nil
	}
	return s
}
