// Package nodeaudit keeps the node's tamper-evident record of what matters
// about access to this machine, the table hub_audit: its authenticator code,
// remote access, and the sessions into it. It is the same chain as the hub's:
// one row per act, written in the act's own transaction, each row's hash
// chaining it to the one before, so that a row changed, removed or inserted
// is found by Verify (the wdx doc HUB_TUNNEL).
package nodeaudit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/srjn45/wdx/internal/logging"
)

// The actions recorded.
const (
	ActionCodeSetUp        = "code.set_up"
	ActionCodeReplaced     = "code.replaced"
	ActionCodeRecovered    = "code.recovered"
	ActionRecoveryKeyWrong = "recovery_key.wrong"
	ActionRemoteAccessOn   = "remote_access.on"
	ActionRemoteAccessOff  = "remote_access.off"
	// ActionCodeWrong is a code that failed CPace, ActionSessionOpened and
	// ActionSessionClosed a session into this node, with the reason it
	// closed, and ActionNodeKeyMismatch another node showing a key that is
	// not the one pinned for it.
	ActionCodeWrong       = "code.wrong"
	ActionSessionOpened   = "session.opened"
	ActionSessionClosed   = "session.closed"
	ActionNodeKeyMismatch = "node_key.mismatch"
)

// Entry is one act.
type Entry struct {
	At     time.Time
	Action string
	// NodeID is the other node the act was with, as a session's client;
	// empty for an act of this node alone.
	NodeID string
	// Details are what else the act records; nil records none. Never a
	// secret: the chain is shown to whoever verifies it.
	Details map[string]any
}

// Broken is the first row whose hash does not match.
type Broken struct {
	ID int64
	At time.Time
}

// Verification is what Verify found: how many rows match and the last of
// their hashes, in hex, empty for none, and the first row that does not, nil
// when every one does.
type Verification struct {
	Rows   int
	Head   string
	Broken *Broken
}

// Append adds e to the chain in tx, the transaction of the act itself, so
// that the act and its row are written together or not at all.
func Append(ctx context.Context, tx *sql.Tx, e Entry) error {
	details := []byte("{}")
	if e.Details != nil {
		var err error
		if details, err = json.Marshal(e.Details); err != nil {
			return err
		}
	}
	prev := make([]byte, sha256.Size)
	var id int64
	err := tx.QueryRowContext(ctx, "SELECT id, hash FROM hub_audit ORDER BY id DESC LIMIT 1").Scan(&id, &prev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// The id is hashed, so it is known before the row is written.
	r := row{id: id + 1, at: e.At.UnixMilli(), action: e.Action, nodeID: nullable(e.NodeID), details: string(details)}
	r.hash = r.compute(prev)
	if _, err := tx.ExecContext(ctx, "INSERT INTO hub_audit (id, at, action, node_id, details, hash) VALUES (?, ?, ?, ?, ?, ?)",
		r.id, r.at, r.action, r.nodeID, r.details, r.hash); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("audit appended", "audit_id", r.id, "action", r.action, "node_id", e.NodeID)
	return nil
}

// AppendOne adds e to the chain in a transaction of its own, for an act that
// writes nothing else to the database.
func AppendOne(ctx context.Context, db *sql.DB, e Entry) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // a no-op once committed
	if err := Append(ctx, tx, e); err != nil {
		return err
	}
	return tx.Commit()
}

// WrongCodes counts the wrong codes recorded since the last session opened
// into this node, and returns when the latest came, zero with none: what
// the owner is shown, so that guessing at the code is never unseen.
func WrongCodes(ctx context.Context, db *sql.DB) (int, time.Time, error) {
	var count int
	var latest sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT COUNT(*), MAX(at) FROM hub_audit
		WHERE action = ? AND id > (SELECT COALESCE(MAX(id), 0) FROM hub_audit WHERE action = ?)`,
		ActionCodeWrong, ActionSessionOpened).Scan(&count, &latest)
	if err != nil || !latest.Valid {
		return count, time.Time{}, err
	}
	return count, time.UnixMilli(latest.Int64), nil
}

// Verify walks the chain in order, recomputing each row's hash, and stops at
// the first that does not match.
func Verify(ctx context.Context, db *sql.DB) (Verification, error) {
	rows, err := db.QueryContext(ctx, "SELECT id, at, action, node_id, details, hash FROM hub_audit ORDER BY id")
	if err != nil {
		return Verification{}, err
	}
	defer rows.Close()
	var v Verification
	prev := make([]byte, sha256.Size)
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.at, &r.action, &r.nodeID, &r.details, &r.hash); err != nil {
			return Verification{}, err
		}
		if !bytes.Equal(r.compute(prev), r.hash) {
			v.Broken = &Broken{ID: r.id, At: time.UnixMilli(r.at).UTC()}
			break
		}
		v.Rows++
		v.Head = hex.EncodeToString(r.hash)
		prev = r.hash
	}
	if err := rows.Err(); err != nil {
		return Verification{}, err
	}
	log := logging.FromContext(ctx)
	if v.Broken != nil {
		log.Warn("audit chain broken", "rows", v.Rows, "audit_id", v.Broken.ID)
	} else {
		log.Info("audit chain verified", "rows", v.Rows, "head", v.Head)
	}
	return v, nil
}

// row is a row of hub_audit as it is stored.
type row struct {
	id      int64
	at      int64
	action  string
	nodeID  sql.NullString
	details string
	hash    []byte
}

// compute returns the row's hash: the SHA-256 of the previous row's hash,
// then each field in order, an integer as 8 bytes big-endian, a text as 1
// byte for NULL or not, its length as 8 bytes and its bytes.
func (r row) compute(prev []byte) []byte {
	h := sha256.New()
	h.Write(prev)
	integer := func(n int64) {
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n)) //nolint:gosec // the bits are hashed, not the number
		h.Write(b[:])
	}
	text := func(s sql.NullString) {
		if !s.Valid {
			h.Write([]byte{0})
			return
		}
		h.Write([]byte{1})
		integer(int64(len(s.String)))
		h.Write([]byte(s.String))
	}
	integer(r.id)
	integer(r.at)
	text(sql.NullString{String: r.action, Valid: true})
	text(r.nodeID)
	text(sql.NullString{String: r.details, Valid: true})
	return h.Sum(nil)
}

func nullable(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
