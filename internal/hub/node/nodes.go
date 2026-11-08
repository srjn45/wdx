package node

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/hub/audit"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/sqlite"
)

// Node is a machine of an account.
type Node struct {
	ID        string
	UserID    string
	Name      string
	PublicKey ed25519.PublicKey
	OS        string
	Version   string // of wdx, as it last connected
	CreatedAt time.Time
	// LastSeenAt is when it was last connected; zero when never.
	LastSeenAt time.Time
	// CertExpiresAt is when its certificate ends; zero until the login that
	// made it is finished.
	CertExpiresAt time.Time
	// RemoteAccess and CodeSetup are what the node last said of itself, for
	// display: whether it allows sessions, and has an authenticator for them.
	RemoteAccess, CodeSetup bool
}

const nodeColumns = "id, user_id, name, public_key, os, version, created_at, last_seen_at, cert_expires_at, remote_access, code_setup"

func scanNode(row interface{ Scan(...any) error }) (Node, error) {
	var n Node
	var pub []byte
	var created int64
	var seen, expires sql.NullInt64
	if err := row.Scan(&n.ID, &n.UserID, &n.Name, &pub, &n.OS, &n.Version, &created, &seen, &expires, &n.RemoteAccess, &n.CodeSetup); err != nil {
		return Node{}, err
	}
	n.PublicKey = ed25519.PublicKey(pub)
	n.CreatedAt, n.LastSeenAt, n.CertExpiresAt = time.UnixMilli(created).UTC(), fromMS(seen), fromMS(expires)
	return n, nil
}

// nameTaken reports whether userID has a node named name, whatever its case.
func nameTaken(ctx context.Context, q sqlite.Querier, userID, name string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM nodes WHERE user_id = ? AND name = ? AND revoked_at IS NULL", userID, name).Scan(&n)
	return n > 0, err
}

// NameTaken reports whether userID has a node named name, whatever its case.
func (s *Store) NameTaken(ctx context.Context, userID, name string) (bool, error) {
	return nameTaken(ctx, s.db, userID, name)
}

// Authenticate returns the node id, as a certificate names it, when it may
// connect: it exists, is not revoked, its key is pub, and its user is
// neither disabled nor removed. It fails with ErrRevoked or ErrDisabled. It
// reads the database each time, so that a revocation holds at the next
// request.
func (s *Store) Authenticate(ctx context.Context, id string, pub ed25519.PublicKey) (Node, error) {
	log := logging.FromContext(ctx)
	var revoked, disabled, removed sql.NullInt64
	row := s.db.QueryRowContext(ctx, "SELECT n.id, n.user_id, n.name, n.public_key, n.os, n.version, n.created_at, n.last_seen_at, n.cert_expires_at, n.remote_access, n.code_setup, "+
		"n.revoked_at, u.disabled_at, u.removed_at FROM nodes n JOIN users u ON u.id = n.user_id WHERE n.id = ?", id)
	n, err := scanNode(withExtra{scan: row.Scan, extra: []any{&revoked, &disabled, &removed}})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		log.Info("node refused", "node_id", id, "reason", "no such node")
		return Node{}, ErrRevoked
	case err != nil:
		return Node{}, err
	case revoked.Valid || removed.Valid:
		log.Info("node refused", "node_id", id, "user_id", n.UserID, "reason", "revoked")
		return Node{}, ErrRevoked
	case !n.PublicKey.Equal(pub):
		log.Info("node refused", "node_id", id, "user_id", n.UserID, "reason", "another key")
		return Node{}, ErrRevoked
	case disabled.Valid:
		log.Info("node refused", "node_id", id, "user_id", n.UserID, "reason", "account disabled")
		return Node{}, ErrDisabled
	}
	return n, nil
}

// withExtra scans a row of a node's columns followed by extra.
type withExtra struct {
	scan  func(...any) error
	extra []any
}

func (w withExtra) Scan(dest ...any) error { return w.scan(append(dest, w.extra...)...) }

// Nodes returns the nodes of userID not removed, by name.
func (s *Store) Nodes(ctx context.Context, userID string) ([]Node, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+nodeColumns+" FROM nodes WHERE user_id = ? AND revoked_at IS NULL ORDER BY name", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, n)
	}
	return list, rows.Err()
}

// Node returns the node of userID that ref names, its name whatever its case
// or id: and its id, or ErrNotFound when userID has none such.
func (s *Store) Node(ctx context.Context, userID, ref string) (Node, error) {
	return s.node(ctx, s.db, userID, ref)
}

func (s *Store) node(ctx context.Context, q sqlite.Querier, userID, ref string) (Node, error) {
	where, arg := "name = ?", ref
	if id, ok := strings.CutPrefix(ref, "id:"); ok {
		where, arg = "id = ?", id
	}
	n, err := scanNode(q.QueryRowContext(ctx, "SELECT "+nodeColumns+" FROM nodes WHERE user_id = ? AND revoked_at IS NULL AND "+where, userID, arg))
	if errors.Is(err, sql.ErrNoRows) {
		logging.FromContext(ctx).Info("node not found", "user_id", userID, "ref", ref)
		return Node{}, ErrNotFound
	}
	return n, err
}

// Remove revokes the node of userID that ref names, as Node finds it, and
// cuts its connections off: it can never connect again, and its name is free.
// Removed by itself, as wdx hub logout does, it is recorded as logged out.
func (s *Store) Remove(ctx context.Context, by By, userID, ref string) (Node, error) {
	var n Node
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		if n, err = s.node(ctx, tx, userID, ref); err != nil {
			return err
		}
		if _, err := sqlite.Exec(ctx, tx, "UPDATE nodes SET revoked_at = ? WHERE id = ? AND user_id = ?", ms(s.now()), n.ID, userID); err != nil {
			return err
		}
		// by names the node that removed it, or none when the admin did.
		action, details := ActionRemoved, map[string]any{"name": n.Name}
		if by.NodeID == n.ID {
			action = ActionLoggedOut
		}
		return s.record(ctx, tx, by, userID, n.ID, action, details)
	})
	if err != nil {
		return Node{}, err
	}
	logging.FromContext(ctx).Info("node revoked", "node_id", n.ID, "user_id", userID, "by_node_id", by.NodeID)
	s.cutOff(ctx, ReasonRevoked, func(c *Conn) bool { return c.NodeID == n.ID })
	return n, nil
}

// CutOffUser closes the connections of every node of userID, with reason:
// ReasonDisabled once the user is disabled, ReasonRevoked once removed.
func (s *Store) CutOffUser(ctx context.Context, userID string, reason Reason) {
	s.cutOff(ctx, reason, func(c *Conn) bool { return c.UserID == userID })
}

// cutOff closes the connections match picks with reason, and records each
// node cut off.
func (s *Store) cutOff(ctx context.Context, reason Reason, match func(*Conn) bool) {
	log := logging.FromContext(ctx)
	for _, c := range s.registry.cut(match, reason) {
		log.Info("node cut off", "node_id", c.NodeID, "user_id", c.UserID, "reason", string(reason))
		err := s.inTx(ctx, func(tx *sql.Tx) error {
			return s.record(ctx, tx, By{Actor: audit.ActorHub}, c.UserID, c.NodeID, ActionCutOff, map[string]any{"reason": string(reason)})
		})
		if err != nil {
			log.Error("cut off not recorded", "node_id", c.NodeID, "err", err)
		}
	}
}

// Connected records that the node id connected, with the wdx version and the
// OS it said.
func (s *Store) Connected(ctx context.Context, id, version, os string) error {
	_, err := sqlite.Exec(ctx, s.db, "UPDATE nodes SET last_seen_at = ?, version = ?, os = ? WHERE id = ?", ms(s.now()), version, os, id)
	return err
}

// SetState records what the node id says of itself: whether it allows
// sessions, and has an authenticator set up for them.
func (s *Store) SetState(ctx context.Context, id string, remoteAccess, codeSetup bool) error {
	_, err := sqlite.Exec(ctx, s.db, "UPDATE nodes SET remote_access = ?, code_setup = ? WHERE id = ?", remoteAccess, codeSetup, id)
	if err == nil {
		logging.FromContext(ctx).Info("node state saved", "node_id", id, "remote_access", remoteAccess, "code_setup", codeSetup)
	}
	return err
}

// Seen records that the node id is connected now: at its close, and at most
// once a minute between, which its connection decides.
func (s *Store) Seen(ctx context.Context, id string) error {
	_, err := sqlite.Exec(ctx, s.db, "UPDATE nodes SET last_seen_at = ? WHERE id = ?", ms(s.now()), id)
	return err
}

// CertificateRenewed records that the node wrote the certificate it was sent,
// which ends at expires.
func (s *Store) CertificateRenewed(ctx context.Context, n Node, expires time.Time) error {
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := sqlite.Exec(ctx, tx, "UPDATE nodes SET cert_expires_at = ? WHERE id = ? AND revoked_at IS NULL", ms(expires), n.ID); err != nil {
			return err
		}
		return s.record(ctx, tx, By{Actor: audit.ActorHub}, n.UserID, n.ID, ActionCertificateRenewed, map[string]any{"expires_at": expires.UTC().Format(time.RFC3339)})
	})
	if err == nil {
		logging.FromContext(ctx).Info("certificate renewed", "node_id", n.ID, "cert_expires_at", expires.UTC())
	}
	return err
}
