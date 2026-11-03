package tidestate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/logging"
)

// A view is what tide shows besides its tabs, as the JSON object tide sends:
// only tide reads it, and it adds fields as its view grows.

// ErrNotObject is a view that is not a JSON object.
var ErrNotObject = errors.New("a view is a JSON object")

// emptyView is the view of a tide or a project none was saved for.
var emptyView = json.RawMessage(`{}`)

// View returns tide's view as a whole, {} when none was saved.
func (s *Store) View(ctx context.Context) (json.RawMessage, error) {
	return s.readView(ctx, "SELECT view FROM tide_view WHERE id = 1")
}

// ReplaceView makes view tide's view as a whole.
func (s *Store) ReplaceView(ctx context.Context, view json.RawMessage) error {
	if err := checkView(view); err != nil {
		return err
	}
	if _, err := db.Exec(ctx, s.db, `INSERT INTO tide_view (id, view, updated_at) VALUES (1, ?, ?)
		ON CONFLICT (id) DO UPDATE SET view = excluded.view, updated_at = excluded.updated_at`,
		string(view), time.Now().UnixMilli()); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("tide view saved", "bytes", len(view))
	return nil
}

// ProjectView returns tide's view of the project, {} when none was saved.
func (s *Store) ProjectView(ctx context.Context, projectID string) (json.RawMessage, error) {
	return s.readView(ctx, "SELECT view FROM tide_project_views WHERE project_id = ?", projectID)
}

// ReplaceProjectView makes view tide's view of the project.
func (s *Store) ReplaceProjectView(ctx context.Context, projectID string, view json.RawMessage) error {
	if err := checkView(view); err != nil {
		return err
	}
	if _, err := db.Exec(ctx, s.db, `INSERT INTO tide_project_views (project_id, view, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (project_id) DO UPDATE SET view = excluded.view, updated_at = excluded.updated_at`,
		projectID, string(view), time.Now().UnixMilli()); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("project view saved", "project_id", projectID, "bytes", len(view))
	return nil
}

func (s *Store) readView(ctx context.Context, query string, args ...any) (json.RawMessage, error) {
	var view string
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&view)
	if errors.Is(err, sql.ErrNoRows) {
		return emptyView, nil
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(view), nil
}

// checkView fails for a view that is not a JSON object, before the database's
// CHECK would, with an error the daemon can name.
func checkView(view json.RawMessage) error {
	var object map[string]json.RawMessage
	if json.Unmarshal(view, &object) != nil || object == nil {
		return ErrNotObject
	}
	return nil
}
