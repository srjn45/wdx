// Package agent manages agents: AI coding agents running in a project, on a
// terminal row of kind agent that holds their name, dir and process. See
// docs/AGENT.md.
package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/screen"
	"github.com/srjn45/wdx/internal/sqlite"
	"github.com/srjn45/wdx/internal/terminal"
)

// Agent is an agent's row with its terminal row.
type Agent struct {
	ID        string
	ACA       string // which AI coding agent it runs: aca.AiCodingAgent's ID
	SessionID string // the AI coding agent's latest session, which it goes on with
	ParentID  string // the agent it was created under, or "" at the top level
	Parent    string // that agent's name
	Worktree  string // the name of the project's worktree holding its dir, "main" for the clone; "" when none does
	// CurrentDir is where its ACA last said it worked, which may not be its
	// terminal's dir: it may have moved into a worktree it made itself. "" until it
	// has, or when its ACA does not say.
	CurrentDir string
	Held       int       // how many messages for it wait for a human: docs/SEND.md
	RemovedAt  time.Time // when it was removed, keeping it as a tombstone; zero otherwise
	Activity   string    // what its process is doing, as a screen.Activity; "" unless it is running
	// Prompt is what its process asks while Activity is waiting, when its holder
	// reads it; nil otherwise, and unless it is running.
	Prompt *screen.Prompt
	Draft  bool   // whether a human left text in its input box
	Queued int    // how many messages wait to go into it
	Model  string // the AI model's name as its ACA last showed it; "" until it has, or unless it is running
	// RequestedModel is the model it is started with every time, as it was asked
	// for; "" is its ACA's default.
	RequestedModel string
	Permissions    aca.Permissions // what it may do without asking, on every start
	// Context is how full its context is, as its ACA last told; nil until it has,
	// and again once a restart begins a new session. It stays once its terminal ends.
	Context  *aca.ContextSize
	Plan     string // the name of the plan whose task it does; "" for none
	Task     string // the id of that task
	Terminal terminal.Terminal
}

// Tombstone reports whether a was removed and stays only for its children.
func (a Agent) Tombstone() bool {
	return !a.RemovedAt.IsZero()
}

// OnRow is a with t as its terminal row, read at another moment: the status goes
// by t's state, as a query's does.
func (a Agent) OnRow(t terminal.Terminal) Agent {
	a.Terminal = t
	switch {
	case t.State != terminal.StateRunning:
		a.Activity, a.Draft, a.Queued, a.Model, a.Prompt = "", false, 0, "", nil
	case a.Activity == "":
		a.Activity = string(screen.ActivityUnknown)
	}
	return a
}

// ErrNotFound is returned when no agent matches a name or id.
var ErrNotFound = errors.New("agent not found")

// Store persists agents. Every query joins them with their terminal rows.
type Store struct {
	db      *sql.DB
	queries *sqlite.Statements
}

// NewStore returns a Store backed by db.
func NewStore(database *sql.DB) *Store {
	return &Store{db: database, queries: sqlite.NewStatements(database)}
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

// SetStatus records what the process of the agent on terminalID is doing, and
// reports whether that changes anything. A running holder can be newer than this
// daemon, so an activity it does not know is unknown. The prompt is kept only
// with waiting, as JSON, and its text is never logged.
func (s *Store) SetStatus(ctx context.Context, terminalID, activity string, draft bool, queued int, model string, prompt *screen.Prompt) (bool, error) {
	switch screen.Activity(activity) {
	case screen.ActivityWorking, screen.ActivityIdle, screen.ActivityWaiting, screen.ActivityUnknown:
	default:
		logging.FromContext(ctx).Warn("unknown activity read as unknown", "terminal_id", terminalID, "activity", activity)
		activity = string(screen.ActivityUnknown)
	}
	if activity != string(screen.ActivityWaiting) {
		prompt = nil
	}
	var promptJSON sql.NullString
	if prompt != nil {
		b, err := json.Marshal(prompt)
		if err != nil {
			return false, err
		}
		promptJSON = sql.NullString{String: string(b), Valid: true}
	}
	rows, err := sqlite.Exec(ctx, s.db, "UPDATE agents SET activity = ?1, draft = ?2, queued = ?3, model = NULLIF(?4, ''), prompt = ?6 WHERE terminal_id = ?5 AND (activity IS NOT ?1 OR draft IS NOT ?2 OR queued IS NOT ?3 OR model IS NOT NULLIF(?4, '') OR prompt IS NOT ?6)", activity, draft, queued, model, terminalID, promptJSON)
	if err != nil {
		return false, err
	}
	if rows > 0 {
		attrs := []any{"terminal_id", terminalID, "activity", activity, "draft", draft, "queued", queued, "model", model}
		if prompt != nil {
			attrs = append(attrs, "prompt_kind", prompt.Kind, "prompt_options", len(prompt.Options))
		}
		logging.FromContext(ctx).Info("agent status changed", attrs...)
	}
	return rows > 0, nil
}

// ClearPrompt forgets the prompt of the agent on terminalID, whose terminal ended:
// the prompt's text stays only while it is open. It reports whether there was one.
func (s *Store) ClearPrompt(ctx context.Context, terminalID string) (bool, error) {
	rows, err := sqlite.Exec(ctx, s.db, "UPDATE agents SET prompt = NULL WHERE terminal_id = ? AND prompt IS NOT NULL", terminalID)
	if rows > 0 {
		logging.FromContext(ctx).Info("agent prompt cleared", "terminal_id", terminalID)
	}
	return rows > 0, err
}

// ClearEndedPrompts forgets the prompts of the agents whose terminals are not
// running, which ended while no daemon was there to clear them.
func (s *Store) ClearEndedPrompts(ctx context.Context) error {
	rows, err := sqlite.Exec(ctx, s.db, "UPDATE agents SET prompt = NULL WHERE prompt IS NOT NULL AND terminal_id IN (SELECT id FROM terminals WHERE state != 'running')")
	if rows > 0 {
		logging.FromContext(ctx).Info("prompts of ended agents cleared", "count", rows)
	}
	return err
}

// setContext records how full the context of the agent with id is, and reports
// whether that changed anything. A window of 0, which its ACA does not tell, is
// kept as null.
func (s *Store) setContext(ctx context.Context, id string, size aca.ContextSize) (bool, error) {
	n, err := sqlite.Exec(ctx, s.db, "UPDATE agents SET context_tokens = ?1, context_window = NULLIF(?2, 0) WHERE id = ?3 AND (context_tokens IS NOT ?1 OR context_window IS NOT NULLIF(?2, 0))",
		size.Tokens, size.Window, id)
	return n > 0, err
}

// worktreeOfDir finds the deepest worktree of an agent's project holding its
// dir, since the project's .worktrees is inside its clone: "main" for a clone,
// else its path. An agent has no column for it: its dir already tells.
const worktreeOfDir = `
	SELECT CASE WHEN w.git_common_dir = w.path || '/.git' THEN 'main' ELSE w.path END
	FROM project_worktrees w
	WHERE w.project_id = t.project_id AND (t.dir = w.path OR substr(t.dir, 1, length(w.path) + 1) = w.path || '/')
	ORDER BY length(w.path) DESC LIMIT 1`

// taskOfAgent finds the plan's task an agent does, by plan_tasks.agent_id. A plan is
// a doc of the kind plan, its name kept as plan:<name> (docs/PLANS.md), which the
// substr strips; the plan package says so.
const taskOfAgent = `
	SELECT substr(d.name, 6) || char(0) || pt.task_id
	FROM plan_tasks pt JOIN docs d ON d.id = pt.plan_id
	WHERE pt.agent_id = a.id LIMIT 1`

// query returns the agents matching where, a constant condition on a, the
// agents, t, their terminal rows, and p, their projects. Going by the terminal's
// state, the status needs nothing done when the process ends, however it ends.
// setCurrentDir records where the agent last worked, and reports whether that
// changed anything.
func (s *Store) setCurrentDir(ctx context.Context, id, dir string) (bool, error) {
	n, err := sqlite.Exec(ctx, s.db, "UPDATE agents SET current_dir = ?1 WHERE id = ?2 AND current_dir IS NOT ?1", dir, id)
	return n > 0, err
}

// setSession records the session the agent is in now, and reports whether that
// changed anything: /clear starts another.
func (s *Store) setSession(ctx context.Context, id, session string) (bool, error) {
	r, err := s.db.ExecContext(ctx, `UPDATE agents SET session_id = ? WHERE id = ?`, session, id)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n > 0, err
}

// beginSession gives the agent the session in one transaction with what linked does.
func (s *Store) beginSession(ctx context.Context, id, session string, linked func(ctx context.Context, tx sqlite.Querier) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// What the old session filled says nothing of the new one.
	if _, err := sqlite.Exec(ctx, tx, "UPDATE agents SET session_id = ?, context_tokens = NULL, context_window = NULL WHERE id = ?", session, id); err != nil {
		return err
	}
	if err := linked(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) query(ctx context.Context, where string, args ...any) ([]Agent, error) {
	rows, err := s.queries.Query(ctx, `
		SELECT a.id, a.aca, a.session_id, a.parent_agent_id,
			(SELECT pt.name FROM agents pa JOIN terminals pt ON pt.id = pa.terminal_id WHERE pa.id = a.parent_agent_id),
			a.removed_at, (`+worktreeOfDir+`),
			CASE WHEN t.state = 'running' THEN COALESCE(a.activity, 'unknown') END,
			CASE WHEN t.state = 'running' THEN COALESCE(a.draft, 0) END,
			CASE WHEN t.state = 'running' THEN COALESCE(a.queued, 0) END,
			CASE WHEN t.state = 'running' THEN a.model END,
			CASE WHEN t.state = 'running' THEN a.prompt END,
			a.current_dir, a.requested_model, a.permissions, a.context_tokens, a.context_window,
			(SELECT COUNT(*) FROM messages m WHERE m.terminal_id = a.terminal_id AND m.state = 'held'),
			(`+taskOfAgent+`),
			`+terminal.Columns+`
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
		var parent, parentName sql.NullString
		var removed sql.NullInt64
		var worktree, activity, model, prompt, currentDir, requestedModel, task sql.NullString
		var draft sql.NullBool
		var queued, contextTokens, contextWindow sql.NullInt64
		if a.Terminal, err = terminal.ScanRow(rows.Scan, &a.ID, &a.ACA, &a.SessionID, &parent, &parentName, &removed, &worktree, &activity, &draft, &queued, &model, &prompt, &currentDir, &requestedModel, &a.Permissions,
			&contextTokens, &contextWindow, &a.Held, &task); err != nil {
			return nil, err
		}
		a.ParentID, a.Parent = parent.String, parentName.String
		a.Activity, a.Draft, a.Queued, a.Model = activity.String, draft.Bool, int(queued.Int64), model.String
		if prompt.Valid {
			a.Prompt = &screen.Prompt{}
			if err := json.Unmarshal([]byte(prompt.String), a.Prompt); err != nil {
				return nil, fmt.Errorf("prompt of agent %s: %w", a.ID, err)
			}
		}
		a.CurrentDir, a.RequestedModel = currentDir.String, requestedModel.String
		a.Plan, a.Task, _ = strings.Cut(task.String, "\x00")
		if contextTokens.Valid {
			a.Context = &aca.ContextSize{Tokens: contextTokens.Int64, Window: contextWindow.Int64}
		}
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
func (s *Store) insert(ctx context.Context, e sqlite.Execer, id, terminalID, acaID, sessionID, parentID string, l Launch) error {
	_, err := sqlite.Exec(ctx, e, "INSERT INTO agents (id, terminal_id, aca, session_id, parent_agent_id, requested_model, permissions) VALUES (?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?)",
		id, terminalID, acaID, sessionID, parentID, l.Model, l.Permissions)
	if err == nil {
		logging.FromContext(ctx).Info("agent created", "agent_id", id, "terminal_id", terminalID, "aca", acaID, "session_id", sessionID, "parent_agent_id", parentID,
			"requested_model", l.Model, "permissions", l.Permissions)
	}
	return err
}

// markRemoved makes the agent with id a tombstone, in e, the transaction that
// ends its terminal row.
func (s *Store) markRemoved(ctx context.Context, e sqlite.Execer, id string) error {
	_, err := sqlite.Exec(ctx, e, "UPDATE agents SET removed_at = ? WHERE id = ?", time.Now().UnixMilli(), id)
	if err == nil {
		logging.FromContext(ctx).Info("agent kept as a tombstone", "agent_id", id)
	}
	return err
}
