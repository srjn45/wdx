package terminal

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/sqlite"
)

// A message's state, as the table has them. docs/SEND.md.
const (
	MessagePending     = "pending"
	MessageDelivered   = "delivered"
	MessageHeld        = "held"
	MessageUnconfirmed = "unconfirmed"
)

// restartMessagePrefix starts the id of the first message of a restart, which the next
// restart replaces if it has not gone yet.
const restartMessagePrefix = "msg-restart-"

// Message is a message to a terminal or an agent. The table is its record: a
// holder's inbox only holds what is on its way, and ends with the holder.
type Message struct {
	ID          string
	TerminalID  string
	Name        string // the terminal's or agent's
	Text        string // "" once delivered
	Instant     bool
	State       string
	SessionID   string
	HolderPID   int
	Attempts    int
	CreatedAt   time.Time
	DeliveredAt time.Time
}

// ErrMessageNotFound is returned when no message has an id, or it is delivered.
var ErrMessageNotFound = errors.New("message not found")

const messageColumns = `m.id, m.terminal_id, t.name, m.text, m.instant, m.state, COALESCE(m.session_id, ''), COALESCE(m.holder_pid, 0), m.attempts, m.created_at, m.delivered_at`

func scanMessage(scan func(...any) error) (Message, error) {
	var m Message
	var created int64
	var delivered sql.NullInt64
	if err := scan(&m.ID, &m.TerminalID, &m.Name, &m.Text, &m.Instant, &m.State, &m.SessionID, &m.HolderPID, &m.Attempts, &created, &delivered); err != nil {
		return Message{}, err
	}
	m.CreatedAt = time.UnixMilli(created).UTC()
	if delivered.Valid {
		m.DeliveredAt = time.UnixMilli(delivered.Int64).UTC()
	}
	return m, nil
}

func (s *Store) messages(ctx context.Context, where string, args ...any) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+messageColumns+" FROM messages m JOIN terminals t ON t.id = m.terminal_id WHERE "+where+" ORDER BY m.created_at, m.rowid", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Message
	for rows.Next() {
		m, err := scanMessage(rows.Scan)
		if err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// Undelivered returns the project's messages still waiting, pending or held.
func (s *Store) Undelivered(ctx context.Context, projectID string) ([]Message, error) {
	return s.messages(ctx, "t.project_id = ? AND m.state IN ('pending', 'held')", projectID)
}

// Message returns the message with id, unless it was delivered.
func (s *Store) Message(ctx context.Context, id string) (Message, error) {
	list, err := s.messages(ctx, "m.id = ? AND m.state != 'delivered'", id)
	if err != nil {
		return Message{}, err
	}
	if len(list) == 0 {
		return Message{}, fmt.Errorf("%w: %s", ErrMessageNotFound, id)
	}
	return list[0], nil
}

// addMessage records a message before it goes anywhere: safe once stored. Not
// sqlite.Exec, here and wherever text is written, which logs what it is given: a message
// can hold secrets.
func (s *Store) addMessage(ctx context.Context, terminalID, text string, instant bool, session string) (string, error) {
	return s.addMessageIn(ctx, s.db, terminalID, text, instant, session)
}

// AddFirstMessage adds what a new agent on terminalID is told first, in session, in
// e, the transaction that adds the agent: the holder gets it once it is up, as any
// message sent before. It is instant, as BeginSession's, so that nothing sent later
// overtakes it while the agent starts.
func (s *Store) AddFirstMessage(ctx context.Context, e sqlite.Execer, terminalID, session, text string) error {
	_, err := s.addMessageIn(ctx, e, terminalID, text, true, session)
	return err
}

func (s *Store) addMessageIn(ctx context.Context, e sqlite.Execer, terminalID, text string, instant bool, session string) (string, error) {
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := "msg-" + hex.EncodeToString(raw)
	_, err := e.ExecContext(ctx, `INSERT INTO messages (id, terminal_id, text, instant, state, session_id, created_at) VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?)`,
		id, terminalID, text, instant, MessagePending, session, time.Now().UnixMilli())
	if err != nil {
		return "", err
	}
	logging.FromContext(ctx).Info("message stored", "message_id", id, "terminal_id", terminalID, "prompt_bytes", len(text), "instant", instant)
	return id, nil
}

// BeginSession prepares what is waiting for the agent on terminalID for its new session,
// in e, the transaction that gives it that session: first goes ahead of what is pending,
// and the first message of a restart that never got there goes. Pending messages go on
// to the new session, which SEND.md otherwise holds them from, as a human asked for this
// and it is the same agent going on; those held stay held. They become general, or they
// would interrupt the agent reading its history. first is instant so that nothing sent
// later overtakes it while the agent starts.
func (s *Store) BeginSession(ctx context.Context, e sqlite.Querier, terminalID, session, first string) error {
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	id := restartMessagePrefix + hex.EncodeToString(raw)
	if _, err := sqlite.Exec(ctx, e, "DELETE FROM messages WHERE terminal_id = ? AND id LIKE ? AND state IN (?, ?)", terminalID, restartMessagePrefix+"%", MessagePending, MessageHeld); err != nil {
		return err
	}
	carried, err := sqlite.Exec(ctx, e, "UPDATE messages SET session_id = ?, holder_pid = NULL, instant = 0 WHERE terminal_id = ? AND state = ?", session, terminalID, MessagePending)
	if err != nil {
		return err
	}
	created := time.Now().UnixMilli()
	var oldest sql.NullInt64
	if err := e.QueryRowContext(ctx, "SELECT MIN(created_at) FROM messages WHERE terminal_id = ? AND state = ?", terminalID, MessagePending).Scan(&oldest); err != nil {
		return err
	}
	if oldest.Valid {
		created = oldest.Int64 - 1
	}
	if _, err := e.ExecContext(ctx, `INSERT INTO messages (id, terminal_id, text, instant, state, session_id, created_at) VALUES (?, ?, ?, 1, ?, ?, ?)`,
		id, terminalID, first, MessagePending, session, created); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("messages moved to the new session", "terminal_id", terminalID, "session_id", session, "message_id", id, "carried", carried)
	return nil
}

func (s *Store) messageForwarded(ctx context.Context, id string, holderPID int) error {
	_, err := sqlite.Exec(ctx, s.db, "UPDATE messages SET holder_pid = ?, attempts = attempts + 1, forwarded_at = ? WHERE id = ?", holderPID, time.Now().UnixMilli(), id)
	return err
}

// messageUnconfirmed ends a message handed to a holder too old to say what became of
// it. It is not sent again: it may well have arrived.
func (s *Store) messageUnconfirmed(ctx context.Context, id string, holderPID int) error {
	_, err := sqlite.Exec(ctx, s.db, "UPDATE messages SET state = ?, text = '', holder_pid = ?, attempts = attempts + 1, forwarded_at = ? WHERE id = ?", MessageUnconfirmed, holderPID, time.Now().UnixMilli(), id)
	return err
}

// messagesDelivered records what a holder says it typed, and clears the text: it has
// done its work and may hold secrets. Told twice, nothing changes.
func (s *Store) messagesDelivered(ctx context.Context, ids []string) error {
	changed := false
	now := time.Now().UnixMilli()
	for _, id := range ids {
		n, err := sqlite.Exec(ctx, s.db, "UPDATE messages SET state = ?, text = '', delivered_at = ? WHERE id = ? AND state != ?", MessageDelivered, now, id, MessageDelivered)
		if err != nil {
			return err
		}
		if n > 0 {
			changed = true
			logging.FromContext(ctx).Info("message delivered", "message_id", id)
		}
	}
	if changed {
		s.changed()
	}
	return nil
}

func (s *Store) pendingMessages(ctx context.Context, terminalID string) ([]Message, error) {
	return s.messages(ctx, "m.terminal_id = ? AND m.state = ?", terminalID, MessagePending)
}

// holdMessage leaves a message for a human. One that carries a review goes instead,
// which gives its comments back to be sent again.
func (s *Store) holdMessage(ctx context.Context, id string) error {
	var linked int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM review_comments WHERE message_id = ?", id).Scan(&linked); err != nil {
		return err
	}
	var err error
	if linked > 0 {
		_, err = sqlite.Exec(ctx, s.db, "DELETE FROM messages WHERE id = ?", id)
	} else {
		_, err = sqlite.Exec(ctx, s.db, "UPDATE messages SET state = ? WHERE id = ?", MessageHeld, id)
	}
	if err == nil {
		logging.FromContext(ctx).Info("message held", "message_id", id, "review", linked > 0)
		s.changed()
	}
	return err
}

// DiscardMessage deletes a message still waiting.
func (s *Store) DiscardMessage(ctx context.Context, id string) error {
	n, err := sqlite.Exec(ctx, s.db, "DELETE FROM messages WHERE id = ? AND state IN ('pending', 'held')", id)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrMessageNotFound, id)
	}
	logging.FromContext(ctx).Info("message discarded", "message_id", id)
	s.changed()
	return nil
}

// sendOne hands a stored message to conn's holder, and records what became of it.
func (s *Store) sendOne(ctx context.Context, kind Kind, conn *holder.Conn, m Message) error {
	var err error
	if kind == KindAgent {
		err = conn.SendAgentMessage(m.ID, m.Text, m.Instant)
	} else {
		err = conn.SendMessage(m.ID, m.Text)
	}
	if err != nil {
		return err
	}
	if !conn.Confirms() {
		return s.messageUnconfirmed(ctx, m.ID, conn.Hello().HolderPID)
	}
	return s.messageForwarded(ctx, m.ID, conn.Hello().HolderPID)
}
