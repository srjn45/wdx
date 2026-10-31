// Package agent manages agents: AI coding agents running in a project, on a
// terminal row of kind agent that holds their name, dir and process. See
// docs/AGENT.md.
package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/terminal"
)

// Agent is an agent's row with its terminal row.
type Agent struct {
	ID        string
	ACA       string    // which AI coding agent it runs: aca.AiCodingAgent's ID
	SessionID string    // the AI coding agent's session, which it goes on with
	ParentID  string    // the agent it was created under, or "" at the top level
	Worktree  string    // the name of the project's worktree holding its dir, "main" for the clone; "" when none does
	RemovedAt time.Time // when it was removed, keeping it as a tombstone; zero otherwise
	Terminal  terminal.Terminal
}

// Tombstone reports whether a was removed and stays only for its children.
func (a Agent) Tombstone() bool {
	return !a.RemovedAt.IsZero()
}

// ErrNotFound is returned when no agent matches a name or id.
var ErrNotFound = errors.New("agent not found")

// Store persists agents. Every query joins them with their terminal rows.
type Store struct {
	db *sql.DB
}

// NewStore returns a Store backed by db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Get returns the agent with id.
func (s *Store) Get(ctx context.Context, id string) (Agent, error) {
	return s.one(ctx, "a.id = ?", id)
}

// GetByName returns the agent named name, ignoring case, in the project with
// projectID.
func (s *Store) GetByName(ctx context.Context, projectID, name string) (Agent, error) {
	return s.one(ctx, "t.project_id = ? AND t.name = ?", projectID, name)
}

// GetByTerminal returns the agent whose terminal row has id terminalID.
func (s *Store) GetByTerminal(ctx context.Context, terminalID string) (Agent, error) {
	return s.one(ctx, "a.terminal_id = ?", terminalID)
}

// List returns the agents of the project with projectID, or of every project
// when it is "", by project name and then name.
func (s *Store) List(ctx context.Context, projectID string) ([]Agent, error) {
	return s.query(ctx, "?1 = '' OR t.project_id = ?1", projectID)
}

func (s *Store) one(ctx context.Context, where string, args ...any) (Agent, error) {
	list, err := s.query(ctx, where, args...)
	if err != nil {
		return Agent{}, err
	}
	if len(list) == 0 {
		return Agent{}, fmt.Errorf("%w: %q", ErrNotFound, args[len(args)-1])
	}
	return list[0], nil
}

// worktreeOfDir finds the deepest worktree of an agent's project holding its
// dir, since the project's .worktrees is inside its clone: "main" for a clone,
// else its path. An agent has no column for it: its dir already tells.
const worktreeOfDir = `
	SELECT CASE WHEN w.git_common_dir = w.path || '/.git' THEN 'main' ELSE w.path END
	FROM project_worktrees w
	WHERE w.project_id = t.project_id AND (t.dir = w.path OR substr(t.dir, 1, length(w.path) + 1) = w.path || '/')
	ORDER BY length(w.path) DESC LIMIT 1`

// query returns the agents matching where, a constant condition on a, the
// agents, t, their terminal rows, and p, their projects.
func (s *Store) query(ctx context.Context, where string, args ...any) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.aca, a.session_id, a.parent_agent_id, a.removed_at, (`+worktreeOfDir+`), `+terminal.Columns+`
		FROM agents a
		JOIN terminals t ON t.id = a.terminal_id
		JOIN projects p ON p.id = t.project_id
		WHERE `+where+`
		ORDER BY p.name, t.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Agent
	for rows.Next() {
		var a Agent
		var parent sql.NullString
		var removed sql.NullInt64
		var worktree sql.NullString
		if a.Terminal, err = terminal.ScanRow(rows.Scan, &a.ID, &a.ACA, &a.SessionID, &parent, &removed, &worktree); err != nil {
			return nil, err
		}
		a.ParentID = parent.String
		if worktree.Valid {
			a.Worktree = filepath.Base(worktree.String)
		}
		if removed.Valid {
			a.RemovedAt = time.UnixMilli(removed.Int64)
		}
		list = append(list, a)
	}
	logging.FromContext(ctx).Debug("agents looked up", "where", where, "args", args, "count", len(list))
	return list, rows.Err()
}

// children returns how many agents were created under the agent with id and
// are still there.
func (s *Store) children(ctx context.Context, id string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM agents WHERE parent_agent_id = ?", id).Scan(&n)
	logging.FromContext(ctx).Debug("children counted", "agent_id", id, "count", n)
	return n, err
}

// insert adds an agent on the terminal row with terminalID, under the agent
// with parentID unless it is "", in e, the transaction that adds the row.
func (s *Store) insert(ctx context.Context, e db.Execer, id, terminalID, acaID, sessionID, parentID string) error {
	var parent sql.NullString
	if parentID != "" {
		parent = sql.NullString{String: parentID, Valid: true}
	}
	_, err := db.Exec(ctx, e, "INSERT INTO agents (id, terminal_id, aca, session_id, parent_agent_id) VALUES (?, ?, ?, ?, ?)", id, terminalID, acaID, sessionID, parent)
	if err == nil {
		logging.FromContext(ctx).Info("agent created", "agent_id", id, "terminal_id", terminalID, "aca", acaID, "session_id", sessionID, "parent_agent_id", parentID)
	}
	return err
}

// markRemoved makes the agent with id a tombstone, in e, the transaction that
// ends its terminal row.
func (s *Store) markRemoved(ctx context.Context, e db.Execer, id string) error {
	_, err := db.Exec(ctx, e, "UPDATE agents SET removed_at = ? WHERE id = ?", time.Now().UnixMilli(), id)
	if err == nil {
		logging.FromContext(ctx).Info("agent kept as a tombstone", "agent_id", id)
	}
	return err
}
