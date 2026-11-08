// Package audit keeps the hub's tamper-evident record of every act on its
// accounts: one row per act, written in the act's own transaction, each row's
// hash chaining it to the one before, so that a row changed, removed or
// inserted is found by Verify.
package audit

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

// Actor is who did an act.
type Actor string

// The actors.
const (
	ActorUser  Actor = "user"  // the user, from a browser
	ActorAdmin Actor = "admin" // the operator, on the admin socket
	ActorHub   Actor = "hub"   // the hub itself
)

// Entry is one act.
type Entry struct {
	At time.Time
	// UserID is the user the act was on; empty for none, as an invite.
	UserID string
	// NodeID is the node the act was by or on; empty for none.
	NodeID  string
	Actor   Actor
	Action  string // such as user.signed_up
	Address string // the client's address; empty for none
	// Details are what else the act records; nil records none. Never a
	// secret: the chain is shown to whoever verifies it.
	Details map[string]any
}

// Chain is how long the chain is and where it ends.
type Chain struct {
	Rows int
	Head string // the last row's hash in hex; empty for no rows
}

// Broken is the first row whose hash does not match.
type Broken struct {
	ID int64
	At time.Time
}

// Verification is what Verify found: the rows that match and the last of
// their hashes, and the first row that does not, nil when every one does.
type Verification struct {
	Chain
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
	err := tx.QueryRowContext(ctx, "SELECT id, hash FROM audit ORDER BY id DESC LIMIT 1").Scan(&id, &prev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// The id is hashed, so it is known before the row is written.
	r := row{
		id: id + 1, at: e.At.UnixMilli(), userID: nullable(e.UserID), actor: string(e.Actor),
		action: e.Action, address: nullable(e.Address), details: string(details), nodeID: nullable(e.NodeID),
	}
	r.hash = r.compute(prev)
	if _, err := tx.ExecContext(ctx, "INSERT INTO audit (id, at, user_id, actor, action, address, details, node_id, hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.id, r.at, r.userID, r.actor, r.action, r.address, r.details, r.nodeID, r.hash); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("audit appended", "audit_id", r.id, "action", r.action, "user_id", e.UserID, "node_id", e.NodeID, "actor", r.actor)
	return nil
}

// Verify walks the chain in order, recomputing each row's hash, and stops at
// the first that does not match.
func Verify(ctx context.Context, db *sql.DB) (Verification, error) {
	rows, err := db.QueryContext(ctx, "SELECT id, at, user_id, actor, action, address, details, node_id, hash FROM audit ORDER BY id")
	if err != nil {
		return Verification{}, err
	}
	defer rows.Close()
	var v Verification
	prev := make([]byte, sha256.Size)
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.at, &r.userID, &r.actor, &r.action, &r.address, &r.details, &r.nodeID, &r.hash); err != nil {
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

// Head returns how long the chain is and where it ends, without verifying
// it.
func Head(ctx context.Context, db *sql.DB) (Chain, error) {
	var h Chain
	var hash []byte
	err := db.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM audit), (SELECT hash FROM audit ORDER BY id DESC LIMIT 1)").Scan(&h.Rows, &hash)
	if err != nil {
		return Chain{}, err
	}
	if hash != nil {
		h.Head = hex.EncodeToString(hash)
	}
	return h, nil
}

// row is a row of the audit table as it is stored.
type row struct {
	id              int64
	at              int64
	userID, address sql.NullString
	nodeID          sql.NullString
	actor, action   string
	details         string
	hash            []byte
}

// compute returns the row's hash: the SHA-256 of the previous row's hash,
// then each field in order, an integer as 8 bytes big-endian, a text as 1
// byte for NULL or not, its length as 8 bytes and its bytes. node_id, added
// after rows were written without it, is hashed last and only when it is not
// NULL, so that those rows keep their hashes; a node_id set or cleared on a
// row still changes its hash.
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
	text(r.userID)
	text(valid(r.actor))
	text(valid(r.action))
	text(r.address)
	text(valid(r.details))
	if r.nodeID.Valid {
		text(r.nodeID)
	}
	return h.Sum(nil)
}

func nullable(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func valid(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
