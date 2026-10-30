// Package terminal manages terminals: shells in PTYs that belong to a
// project and outlive the daemon. The Store keeps their rows; the
// Supervisor starts, watches and stops their holders. See docs/TERMINALS.md.
package terminal

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/logging"
)

// State is where a terminal is in its life.
type State string

const (
	StateStarting State = "starting" // its holder is being started
	StateRunning  State = "running"  // its shell is alive
	StateExited   State = "exited"   // its shell ended by itself
	StateStopped  State = "stopped"  // wdx stopped it, with its project
	StateFailed   State = "failed"   // its holder or shell did not start
	StateLost     State = "lost"     // its holder is gone and nobody stopped it
)

// Live reports whether a terminal in s has, or is getting, a shell.
func (s State) Live() bool {
	return s == StateStarting || s == StateRunning
}

// Kind says what a terminal row is for. Rows of KindAgent hold the process
// of an agent and are its only: the terminal commands and API deal with
// KindShell alone. See docs/AGENT.md.
type Kind string

const (
	KindShell Kind = "shell" // a shell to type in
	KindAgent Kind = "agent" // an AI coding agent, read through internal/agent
)

// Terminal is a terminal's row.
type Terminal struct {
	ID          string
	Kind        Kind
	Name        string
	ProjectID   string
	ProjectName string
	Dir         string
	Shell       string
	State       State
	PID         int  // the shell's; 0 when it has none
	HolderPID   int  // 0 when it has none
	ExitCode    *int // only when exited
	Error       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	StartedAt   time.Time // zero until started
	EndedAt     time.Time // zero until it stopped running
}

// ErrNotFound is returned when no terminal matches a name or id.
var ErrNotFound = errors.New("terminal not found")

// NameTakenError is returned when a terminal would get a name another one
// in its project already has.
type NameTakenError struct {
	Name     string
	Terminal Terminal
}

func (e *NameTakenError) Error() string {
	return fmt.Sprintf("name %q is already used by %s %s", e.Name, e.Terminal.Kind.noun(), e.Terminal.Name)
}

// noun is what a user calls a row of kind k.
func (k Kind) noun() string {
	if k == KindAgent {
		return "agent"
	}
	return "terminal"
}

// InvalidNameError is returned for a terminal name that cannot be used.
type InvalidNameError struct {
	Name   string
	Reason string
}

func (e *InvalidNameError) Error() string {
	return fmt.Sprintf("invalid name %q: %s", e.Name, e.Reason)
}

// Store persists terminals.
type Store struct {
	db      *sql.DB
	changed func() // called after every change; see OnChange
}

// NewStore returns a Store backed by db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db, changed: func() {}}
}

// OnChange makes the store call fn after every change it writes, before it
// is used: a terminal created, changing state or removed.
func (s *Store) OnChange(fn func()) { s.changed = fn }

// codenameTries is how many generated names are tried before one gets a
// random suffix: there are about ten thousand.
const codenameTries = 10

// insert adds a terminal in state starting, named name or, when name is
// empty, a generated codename free in its project among both kinds. linked,
// if any, runs in the same transaction with the new id, so that a crash
// leaves both its rows or neither.
func (s *Store) insert(ctx context.Context, kind Kind, projectID, name, dir, shell string, linked func(ctx context.Context, tx db.Execer, id string) error) (Terminal, error) {
	if strings.Contains(name, ":") {
		return Terminal{}, &InvalidNameError{Name: name, Reason: `cannot contain ":"`}
	}
	id, err := db.NewID("term")
	if err != nil {
		return Terminal{}, err
	}
	if name == "" {
		if name, err = s.freeCodename(ctx, projectID); err != nil {
			return Terminal{}, err
		}
	} else if other, err := s.rowByName(ctx, projectID, name); err == nil {
		return Terminal{}, &NameTakenError{Name: name, Terminal: other}
	} else if !errors.Is(err, ErrNotFound) {
		return Terminal{}, err
	}
	now := time.Now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Terminal{}, err
	}
	defer tx.Rollback()
	if _, err := db.Exec(ctx, tx, `
		INSERT INTO terminals (id, kind, project_id, name, dir, shell, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, kind, projectID, name, dir, shell, StateStarting, now, now); err != nil {
		return Terminal{}, err
	}
	if linked != nil {
		if err := linked(ctx, tx, id); err != nil {
			return Terminal{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Terminal{}, err
	}
	logging.FromContext(ctx).Info("terminal created", "terminal_id", id, "kind", kind, "project_id", projectID, "name", name, "dir", dir, "shell", shell)
	s.changed()
	return s.row(ctx, id)
}

func (s *Store) freeCodename(ctx context.Context, projectID string) (string, error) {
	for range codenameTries {
		name := codename()
		if _, err := s.rowByName(ctx, projectID, name); errors.Is(err, ErrNotFound) {
			return name, nil
		} else if err != nil {
			return "", err
		}
	}
	suffix, err := randomHex(2)
	return codename() + "-" + suffix, err
}

// The terminal flow's queries: they see KindShell alone, so an agent's row
// is not found, as any unknown id or name is.

// Get returns the terminal with id.
func (s *Store) Get(ctx context.Context, id string) (Terminal, error) {
	return s.ofKind(ctx, KindShell, id)
}

// GetByName returns the terminal named name, ignoring case, in the project
// with projectID.
func (s *Store) GetByName(ctx context.Context, projectID, name string) (Terminal, error) {
	return s.one(ctx, "t.kind = 'shell' AND t.project_id = ? AND t.name = ?", projectID, name)
}

// List returns the terminals of the project with projectID, or of every
// project when it is "", by project name and then name.
func (s *Store) List(ctx context.Context, projectID string) ([]Terminal, error) {
	return s.query(ctx, "t.kind = 'shell' AND (?1 = '' OR t.project_id = ?1)", projectID)
}

// The supervisor's process work, on rows of either kind: nothing outside
// this package reaches these.

// ofKind returns the row with id if it is of kind.
func (s *Store) ofKind(ctx context.Context, kind Kind, id string) (Terminal, error) {
	return s.one(ctx, "t.kind = ? AND t.id = ?", kind, id)
}

func (s *Store) row(ctx context.Context, id string) (Terminal, error) {
	return s.one(ctx, "t.id = ?", id)
}

func (s *Store) rowByName(ctx context.Context, projectID, name string) (Terminal, error) {
	return s.one(ctx, "t.project_id = ? AND t.name = ?", projectID, name)
}

// rows returns the rows of the project with projectID, or of every project
// when it is "".
func (s *Store) rows(ctx context.Context, projectID string) ([]Terminal, error) {
	return s.query(ctx, "?1 = '' OR t.project_id = ?1", projectID)
}

// inStates returns the rows of every project in one of states.
func (s *Store) inStates(ctx context.Context, states ...State) ([]Terminal, error) {
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(states)), ", ")
	args := make([]any, len(states))
	for i, st := range states {
		args[i] = st
	}
	return s.query(ctx, "t.state IN ("+marks+")", args...)
}

func (s *Store) one(ctx context.Context, where string, args ...any) (Terminal, error) {
	list, err := s.query(ctx, where, args...)
	if err != nil {
		return Terminal{}, err
	}
	if len(list) == 0 {
		return Terminal{}, fmt.Errorf("%w: %q", ErrNotFound, args[len(args)-1])
	}
	return list[0], nil
}

// Columns are a terminal's columns for ScanRow, in a query where t is the
// terminals and p their projects.
const Columns = `t.id, t.kind, t.name, t.project_id, p.name, t.dir, t.shell, t.state, t.pid, t.holder_pid,
	t.exit_code, t.error, t.created_at, t.updated_at, t.started_at, t.ended_at`

// ScanRow reads a row of Columns with scan, as sql.Rows.Scan; before lists
// where the columns selected before them go.
func ScanRow(scan func(dest ...any) error, before ...any) (Terminal, error) {
	var t Terminal
	var pid, holderPID, exitCode, started, ended sql.NullInt64
	var errText sql.NullString
	var created, updated int64
	if err := scan(append(before, &t.ID, &t.Kind, &t.Name, &t.ProjectID, &t.ProjectName, &t.Dir, &t.Shell, &t.State, &pid, &holderPID,
		&exitCode, &errText, &created, &updated, &started, &ended)...); err != nil {
		return Terminal{}, err
	}
	t.PID, t.HolderPID, t.Error = int(pid.Int64), int(holderPID.Int64), errText.String
	if exitCode.Valid {
		code := int(exitCode.Int64)
		t.ExitCode = &code
	}
	t.CreatedAt, t.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
	if started.Valid {
		t.StartedAt = time.UnixMilli(started.Int64)
	}
	if ended.Valid {
		t.EndedAt = time.UnixMilli(ended.Int64)
	}
	return t, nil
}

// query returns the rows matching where, a constant condition on t, the
// terminals, and p, their projects.
func (s *Store) query(ctx context.Context, where string, args ...any) ([]Terminal, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+Columns+`
		FROM terminals t JOIN projects p ON p.id = t.project_id
		WHERE `+where+`
		ORDER BY p.name, t.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Terminal
	for rows.Next() {
		t, err := ScanRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	logging.FromContext(ctx).Debug("terminals looked up", "where", where, "args", args, "count", len(list))
	return list, rows.Err()
}

// The state changes. Each names the states it leaves, so one that lost a
// race, such as an exit reported after the terminal was stopped, changes
// nothing; changed tells whether it applied.

// relaunch makes a terminal in one of from starting again, clearing what
// its last shell left.
func (s *Store) relaunch(ctx context.Context, id string, from ...State) (changed bool, err error) {
	return s.transition(ctx, id, StateStarting, from, "pid = NULL, holder_pid = NULL, exit_code = NULL, error = NULL, ended_at = NULL")
}

func (s *Store) running(ctx context.Context, id string, pid, holderPID int, at time.Time) (bool, error) {
	return s.transition(ctx, id, StateRunning, []State{StateStarting}, "pid = ?, holder_pid = ?, started_at = ?", pid, holderPID, at.UnixMilli())
}

func (s *Store) failed(ctx context.Context, id, reason string) (bool, error) {
	return s.transition(ctx, id, StateFailed, []State{StateStarting}, "pid = NULL, holder_pid = NULL, error = ?, ended_at = ?", reason, time.Now().UnixMilli())
}

func (s *Store) exited(ctx context.Context, id string, code int, at time.Time) (bool, error) {
	return s.transition(ctx, id, StateExited, []State{StateRunning}, "pid = NULL, holder_pid = NULL, exit_code = ?, ended_at = ?", code, at.UnixMilli())
}

func (s *Store) lost(ctx context.Context, id string) (bool, error) {
	return s.transition(ctx, id, StateLost, []State{StateRunning}, "pid = NULL, holder_pid = NULL, ended_at = ?", time.Now().UnixMilli())
}

// stopped records the intent to stop a live terminal, before its shell is
// signalled; the pids stay until the shell is gone.
func (s *Store) stopped(ctx context.Context, id string) (bool, error) {
	return s.transition(ctx, id, StateStopped, []State{StateStarting, StateRunning}, "")
}

// ended records that a stopped terminal's shell is gone.
func (s *Store) ended(ctx context.Context, id string, at time.Time) (bool, error) {
	return s.transition(ctx, id, StateStopped, []State{StateStopped}, "pid = NULL, holder_pid = NULL, ended_at = ?", at.UnixMilli())
}

func (s *Store) transition(ctx context.Context, id string, to State, from []State, set string, args ...any) (bool, error) {
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(from)), ", ")
	if set != "" {
		set = ", " + set
	}
	all := append([]any{to, time.Now().UnixMilli()}, args...)
	all = append(all, id)
	for _, st := range from {
		all = append(all, st)
	}
	n, err := db.Exec(ctx, s.db, "UPDATE terminals SET state = ?, updated_at = ?"+set+" WHERE id = ? AND state IN ("+marks+")", all...)
	if err != nil {
		return false, err
	}
	if n > 0 {
		logging.FromContext(ctx).Info("terminal state changed", "terminal_id", id, "state", to)
		s.changed()
	}
	return n > 0, err
}

// remove deletes the terminal with id.
func (s *Store) remove(ctx context.Context, id string) error {
	if _, err := db.Exec(ctx, s.db, "DELETE FROM terminals WHERE id = ?", id); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("terminal removed", "terminal_id", id)
	s.changed()
	return nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
