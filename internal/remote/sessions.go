package remote

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/hublink" //nolint:depguard // remote is the daemon's side of sessions (wdx doc HUB_TUNNEL); the plan's finish task allows it in .golangci.yml
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/nodeaudit"
	"github.com/srjn45/wdx/internal/sqlite"
)

// Session is a session open, either way.
type Session struct {
	ID        string
	Direction Direction
	// Node is the other node, named as it was when the session opened.
	Node                 hublink.NodeRef
	OpenedAt, LastUsedAt time.Time
	// IdleExpiresAt is when the session closes unused: by this node's
	// setting for an incoming one, by the host's timeout as it said at the
	// open for an outgoing one.
	IdleExpiresAt time.Time
	// idleTimeout is the host's, for an outgoing session.
	idleTimeout time.Duration
}

// newSessionID is a session's id: the host's, which the client names it by.
func newSessionID() string {
	return "sess-" + strings.ToLower(rand.Text()[:16])
}

const sessionColumns = "id, direction, node_id, node_name, opened_at, last_used_at, idle_timeout_ms"

// scanSession reads a row of sessionColumns.
func (r *Remote) scanSession(scan func(...any) error) (Session, error) {
	var s Session
	var opened, used int64
	var timeout sql.NullInt64
	if err := scan(&s.ID, &s.Direction, &s.Node.ID, &s.Node.Name, &opened, &used, &timeout); err != nil {
		return Session{}, err
	}
	s.OpenedAt, s.LastUsedAt = time.UnixMilli(opened).UTC(), time.UnixMilli(used).UTC()
	s.idleTimeout = time.Duration(timeout.Int64) * time.Millisecond
	if s.Direction == Incoming {
		s.idleTimeout = r.opts.IdleTimeout()
	}
	s.IdleExpiresAt = s.LastUsedAt.Add(s.idleTimeout)
	return s, nil
}

// Sessions is every session open, both ways, by when they opened.
func (r *Remote) Sessions(ctx context.Context) ([]Session, error) {
	return r.sessions(ctx, "1")
}

// SessionsWith is the sessions open with the node nodeID, both ways.
func (r *Remote) SessionsWith(ctx context.Context, nodeID string) ([]Session, error) {
	return r.sessions(ctx, "node_id = ?", nodeID)
}

func (r *Remote) sessions(ctx context.Context, where string, args ...any) ([]Session, error) {
	rows, err := r.opts.DB.QueryContext(ctx, "SELECT "+sessionColumns+" FROM hub_sessions WHERE closed_at IS NULL AND "+where+" ORDER BY opened_at, id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Session{}
	for rows.Next() {
		s, err := r.scanSession(rows.Scan)
		if err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

// openSession is the session open of id, of the direction given: an
// *Error with CodeSessionNotFound when none is.
func (r *Remote) openSession(ctx context.Context, id string, d Direction) (Session, error) {
	s, err := r.scanSession(r.opts.DB.QueryRowContext(ctx, "SELECT "+sessionColumns+" FROM hub_sessions WHERE id = ? AND direction = ? AND closed_at IS NULL", id, d).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, refusal(CodeSessionNotFound, "no %s session %s is open", d, id)
	}
	return s, err
}

// outgoingTo is the session open from this node into nodeID, false when
// there is none.
func (r *Remote) outgoingTo(ctx context.Context, nodeID string) (Session, bool, error) {
	s, err := r.scanSession(r.opts.DB.QueryRowContext(ctx, "SELECT "+sessionColumns+" FROM hub_sessions WHERE direction = 'outgoing' AND node_id = ? AND closed_at IS NULL ORDER BY opened_at DESC LIMIT 1", nodeID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, false, nil
	}
	return s, err == nil, err
}

// Counts is how many sessions are open each way.
func (r *Remote) Counts(ctx context.Context) (incoming, outgoing int, err error) {
	err = r.opts.DB.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(direction = 'incoming'), 0), COALESCE(SUM(direction = 'outgoing'), 0)
		FROM hub_sessions WHERE closed_at IS NULL`).Scan(&incoming, &outgoing)
	return incoming, outgoing, err
}

// insertSession writes s, opened, in tx.
func insertSession(ctx context.Context, tx *sql.Tx, s Session) error {
	var timeout any
	if s.Direction == Outgoing {
		timeout = s.idleTimeout.Milliseconds()
	}
	_, err := sqlite.Exec(ctx, tx, "INSERT INTO hub_sessions ("+sessionColumns+") VALUES (?, ?, ?, ?, ?, ?, ?)",
		s.ID, s.Direction, s.Node.ID, s.Node.Name, s.OpenedAt.UnixMilli(), s.LastUsedAt.UnixMilli(), timeout)
	return err
}

// touch moves the session's last use to now.
func (r *Remote) touch(ctx context.Context, id string, now time.Time) error {
	_, err := sqlite.Exec(ctx, r.opts.DB, "UPDATE hub_sessions SET last_used_at = ? WHERE id = ? AND closed_at IS NULL", now.UnixMilli(), id)
	return err
}

// closeWhere closes the sessions open that where matches, for reason, with
// an audit row for each incoming one, and returns them as they were.
func (r *Remote) closeWhere(ctx context.Context, reason Reason, where string, args ...any) ([]Session, error) {
	log := logging.FromContext(ctx)
	tx, err := r.opts.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() // a no-op once committed
	rows, err := tx.QueryContext(ctx, "SELECT "+sessionColumns+" FROM hub_sessions WHERE closed_at IS NULL AND "+where+" ORDER BY opened_at, id", args...)
	if err != nil {
		return nil, err
	}
	var closed []Session
	for rows.Next() {
		s, err := r.scanSession(rows.Scan)
		if err != nil {
			rows.Close()
			return nil, err
		}
		closed = append(closed, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	now := r.opts.Clock.Now()
	for _, s := range closed {
		if _, err := sqlite.Exec(ctx, tx, "UPDATE hub_sessions SET closed_at = ?, closed_reason = ? WHERE id = ?", now.UnixMilli(), reason, s.ID); err != nil {
			return nil, err
		}
		if s.Direction == Incoming {
			if err := nodeaudit.Append(ctx, tx, nodeaudit.Entry{At: now, Action: nodeaudit.ActionSessionClosed, NodeID: s.Node.ID,
				Details: map[string]any{"session_id": s.ID, "reason": string(reason)}}); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for _, s := range closed {
		log.Info("session closed", "session_id", s.ID, "direction", s.Direction, "node_id", s.Node.ID, "reason", reason)
	}
	return closed, nil
}

// CloseSession closes the session open of id, either way, and returns it as
// it was. An outgoing one tells its host first, best effort: the host of
// an incoming one finds out at its next stream. It fails with
// CodeSessionNotFound.
func (r *Remote) CloseSession(ctx context.Context, id string) (Session, error) {
	s, err := r.scanSession(r.opts.DB.QueryRowContext(ctx, "SELECT "+sessionColumns+" FROM hub_sessions WHERE id = ? AND closed_at IS NULL", id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, refusal(CodeSessionNotFound, "no session %s is open", id)
	}
	if err != nil {
		return Session{}, err
	}
	if s.Direction == Outgoing {
		r.tellClosed(ctx, s)
	}
	closed, err := r.closeWhere(ctx, ReasonClosed, "id = ?", id)
	if err != nil {
		return Session{}, err
	}
	if len(closed) == 0 { // closed meanwhile
		return Session{}, refusal(CodeSessionNotFound, "no session %s is open", id)
	}
	return closed[0], nil
}

// CloseAll closes every session open, both ways, as CloseSession does, and
// returns them as they were.
func (r *Remote) CloseAll(ctx context.Context) ([]Session, error) {
	list, err := r.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range list {
		if s.Direction == Outgoing {
			r.tellClosed(ctx, s)
		}
	}
	closed, err := r.closeWhere(ctx, ReasonClosed, "1")
	if closed == nil {
		closed = []Session{}
	}
	return closed, err
}

// knownKey is the key pinned for nodeID, nil when none is.
func (r *Remote) knownKey(ctx context.Context, nodeID string) (ed25519.PublicKey, error) {
	var key []byte
	err := r.opts.DB.QueryRowContext(ctx, "SELECT public_key FROM hub_known_nodes WHERE node_id = ?", nodeID).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return key, err
}

// KeyKnown reports whether a key is pinned for nodeID, from an earlier
// session.
func (r *Remote) KeyKnown(ctx context.Context, nodeID string) (bool, error) {
	key, err := r.knownKey(ctx, nodeID)
	return key != nil, err
}

// checkPin fails with CodeNodeKeyMismatch when a key other than key is
// pinned for node, recording it in the audit chain; it reports whether one
// is pinned.
func (r *Remote) checkPin(ctx context.Context, node hublink.NodeRef, key ed25519.PublicKey) (bool, error) {
	pinned, err := r.knownKey(ctx, node.ID)
	if err != nil || pinned == nil {
		return false, err
	}
	if bytes.Equal(pinned, key) {
		return true, nil
	}
	logging.FromContext(ctx).Warn("node key mismatch", "node_id", node.ID, "node_name", node.Name)
	if err := nodeaudit.AppendOne(ctx, r.opts.DB, nodeaudit.Entry{At: r.opts.Clock.Now(), Action: nodeaudit.ActionNodeKeyMismatch, NodeID: node.ID}); err != nil {
		logging.FromContext(ctx).Error("node key mismatch not in the audit chain", "err", err)
	}
	return true, refusal(CodeNodeKeyMismatch, "node %s showed a key other than the one pinned for it in an earlier session: the hub, or someone between, may be standing in for it", node.Name)
}

// pin pins key for node in tx, when none is.
func pin(ctx context.Context, tx *sql.Tx, node hublink.NodeRef, key ed25519.PublicKey, now time.Time) error {
	n, err := sqlite.Exec(ctx, tx, "INSERT INTO hub_known_nodes (node_id, name, public_key, first_seen_at) VALUES (?, ?, ?, ?) ON CONFLICT (node_id) DO NOTHING",
		node.ID, node.Name, []byte(key), now.UnixMilli())
	if err == nil && n > 0 {
		logging.FromContext(ctx).Info("node key pinned", "node_id", node.ID, "node_name", node.Name)
	}
	return err
}

func (r *Remote) forgetKeys(ctx context.Context) error {
	n, err := sqlite.Exec(ctx, r.opts.DB, "DELETE FROM hub_known_nodes")
	if err == nil {
		logging.FromContext(ctx).Info("node keys forgotten", "keys", n)
	}
	return err
}

// keepIdle closes each incoming session once it has gone unused for the
// idle timeout, waking at the next to expire, until the Remote closes.
func (r *Remote) keepIdle() {
	defer close(r.done)
	log := logging.FromContext(r.ctx)
	for {
		next, err := r.expireIdle(r.ctx)
		if err != nil && r.ctx.Err() == nil {
			log.Error("idle sessions not closed", "err", err)
		}
		if r.opts.idleChecked != nil {
			r.opts.idleChecked()
		}
		var wake <-chan time.Time
		if !next.IsZero() {
			wake = r.opts.Clock.After(next.Sub(r.opts.Clock.Now()))
		}
		select {
		case <-r.ctx.Done():
			return
		case <-r.kicked:
		case <-wake:
		}
	}
}

// expireIdle closes the incoming sessions expired by now and returns when
// the next expires, zero when none is open.
func (r *Remote) expireIdle(ctx context.Context) (time.Time, error) {
	now := r.opts.Clock.Now()
	cutoff := now.Add(-r.opts.IdleTimeout()).UnixMilli()
	if _, err := r.closeWhere(ctx, ReasonIdle, "direction = 'incoming' AND last_used_at <= ?", cutoff); err != nil {
		return time.Time{}, err
	}
	var oldest sql.NullInt64
	if err := r.opts.DB.QueryRowContext(ctx, "SELECT MIN(last_used_at) FROM hub_sessions WHERE direction = 'incoming' AND closed_at IS NULL").Scan(&oldest); err != nil {
		return time.Time{}, err
	}
	if !oldest.Valid {
		return time.Time{}, nil
	}
	return time.UnixMilli(oldest.Int64).Add(r.opts.IdleTimeout()), nil
}
