package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/sqlite"
)

// Invite is an invite just made, with the token of its link.
type Invite struct {
	ID        string
	Token     string
	ExpiresAt time.Time
}

// CreateInvite makes an invite that lets one person sign up within life, at
// most MaxInviteLife.
func (s *Store) CreateInvite(ctx context.Context, by By, life time.Duration) (Invite, error) {
	if life <= 0 || life > MaxInviteLife {
		return Invite{}, &InvalidError{Field: "expires", Reason: fmt.Sprintf("must be more than 0 and at most %s", MaxInviteLife)}
	}
	id, err := sqlite.NewID("inv")
	if err != nil {
		return Invite{}, err
	}
	token, hash := newToken()
	now := s.now()
	expires := now.Add(life)
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := sqlite.Exec(ctx, tx, "INSERT INTO invites (id, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?)",
			id, secret(hash), ms(now), ms(expires)); err != nil {
			return err
		}
		logging.FromContext(ctx).Info("invite created", "invite_id", id, "expires_at", expires)
		return s.record(ctx, tx, by, "", ActionInvited, map[string]any{"invite_id": id, "expires_at": expires.UTC()})
	})
	if err != nil {
		return Invite{}, err
	}
	return Invite{ID: id, Token: token, ExpiresAt: time.UnixMilli(ms(expires)).UTC()}, nil
}

// CheckInvite fails with ErrInvalidInvite unless token is an invite neither
// used nor expired, as the sign-up page asks before it shows its form.
func (s *Store) CheckInvite(ctx context.Context, token string) error {
	var id string
	err := s.db.QueryRowContext(ctx, "SELECT id FROM invites WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?", hashOf(token), ms(s.now())).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		logging.FromContext(ctx).Info("invite refused")
		return ErrInvalidInvite
	}
	return err
}

// Reset is a password reset just made, with the token of its link.
type Reset struct {
	ID        string
	Token     string
	ExpiresAt time.Time
	// SecondFactor is true when the reset removes the factor and the
	// recovery codes too, so that the next login enrols a new one.
	SecondFactor bool
}

// CreateReset makes a password reset of userID that works for ResetLife,
// voiding any earlier one not used.
func (s *Store) CreateReset(ctx context.Context, by By, userID string, secondFactor bool) (Reset, error) {
	id, err := sqlite.NewID("rst")
	if err != nil {
		return Reset{}, err
	}
	token, hash := newToken()
	now := s.now()
	expires := now.Add(ResetLife)
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE id = ? AND removed_at IS NULL", userID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		voided, err := sqlite.Exec(ctx, tx, "DELETE FROM password_resets WHERE user_id = ? AND used_at IS NULL", userID)
		if err != nil {
			return err
		}
		if _, err := sqlite.Exec(ctx, tx, "INSERT INTO password_resets (id, user_id, token_hash, second_factor, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
			id, userID, secret(hash), secondFactor, ms(now), ms(expires)); err != nil {
			return err
		}
		logging.FromContext(ctx).Info("reset made", "reset_id", id, "user_id", userID, "second_factor", secondFactor, "voided", voided)
		return s.record(ctx, tx, by, userID, ActionResetMade, map[string]any{"reset_id": id, "second_factor": secondFactor, "expires_at": expires.UTC(), "voided": voided})
	})
	if err != nil {
		return Reset{}, err
	}
	return Reset{ID: id, Token: token, ExpiresAt: time.UnixMilli(ms(expires)).UTC(), SecondFactor: secondFactor}, nil
}

// pendingReset is a reset neither used nor expired.
type pendingReset struct {
	id, userID   string
	secondFactor bool
}

func (s *Store) findReset(ctx context.Context, q sqlite.Querier, token string) (pendingReset, error) {
	var r pendingReset
	err := q.QueryRowContext(ctx, "SELECT r.id, r.user_id, r.second_factor FROM password_resets r JOIN users u ON u.id = r.user_id "+
		"WHERE r.token_hash = ? AND r.used_at IS NULL AND r.expires_at > ? AND u.removed_at IS NULL", hashOf(token), ms(s.now())).Scan(&r.id, &r.userID, &r.secondFactor)
	if errors.Is(err, sql.ErrNoRows) {
		logging.FromContext(ctx).Info("reset refused")
		return pendingReset{}, ErrInvalidReset
	}
	return r, err
}

// CheckReset fails with ErrInvalidReset unless token is a reset neither used
// nor expired; secondFactor says whether it removes the factor too.
func (s *Store) CheckReset(ctx context.Context, token string) (secondFactor bool, err error) {
	r, err := s.findReset(ctx, s.db, token)
	return r.secondFactor, err
}

// UseReset sets the password of the user the reset token is for, ending
// every session of theirs, and with its second factor removes their factor
// and recovery codes, so that they enrol anew at the next login. It returns
// the user's id.
func (s *Store) UseReset(ctx context.Context, by By, token, password string) (string, error) {
	if err := checkPassword(password); err != nil {
		return "", err
	}
	if _, err := s.findReset(ctx, s.db, token); err != nil {
		return "", err
	}
	hash, err := s.argon.hash(password)
	if err != nil {
		return "", err
	}
	var r pendingReset
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if r, err = s.findReset(ctx, tx, token); err != nil {
			return err
		}
		if r.secondFactor {
			for _, q := range []string{"DELETE FROM totp_factors WHERE user_id = ?", "DELETE FROM recovery_codes WHERE user_id = ?"} {
				if _, err := sqlite.Exec(ctx, tx, q, r.userID); err != nil {
					return err
				}
			}
		}
		if _, err := sqlite.Exec(ctx, tx, "UPDATE users SET password_hash = ? WHERE id = ?", secret(hash), r.userID); err != nil {
			return err
		}
		ended, err := endSessions(ctx, tx, r.userID, "")
		if err != nil {
			return err
		}
		if _, err := sqlite.Exec(ctx, tx, "UPDATE password_resets SET used_at = ? WHERE id = ?", ms(s.now()), r.id); err != nil {
			return err
		}
		logging.FromContext(ctx).Info("reset used", "reset_id", r.id, "user_id", r.userID, "second_factor", r.secondFactor)
		return s.record(ctx, tx, by, r.userID, ActionResetUsed, map[string]any{"reset_id": r.id, "second_factor": r.secondFactor, "sessions_ended": ended})
	})
	return r.userID, err
}

// Expired is what DeleteExpired deleted.
type Expired struct {
	Sessions, Invites, Resets int64
}

// DeleteExpired deletes the sessions that have ended, and the invites not
// used and the resets past their expiry, as the hub does at startup and once
// an hour.
func (s *Store) DeleteExpired(ctx context.Context) (Expired, error) {
	var e Expired
	now, idleSince := s.liveArgs()
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		if e.Sessions, err = sqlite.Exec(ctx, tx, "DELETE FROM web_sessions WHERE NOT ("+live+")", now, idleSince); err != nil {
			return err
		}
		if e.Invites, err = sqlite.Exec(ctx, tx, "DELETE FROM invites WHERE used_at IS NULL AND expires_at <= ?", now); err != nil {
			return err
		}
		e.Resets, err = sqlite.Exec(ctx, tx, "DELETE FROM password_resets WHERE expires_at <= ?", now)
		return err
	})
	if err != nil {
		return Expired{}, err
	}
	logging.FromContext(ctx).Info("expired deleted", "sessions", e.Sessions, "invites", e.Invites, "resets", e.Resets)
	return e, nil
}
