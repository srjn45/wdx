package account

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/sqlite"
)

// FactorTOTP is the second factor of a user with an authenticator app.
const FactorTOTP = "totp"

// User is an account.
type User struct {
	ID          string
	Username    string
	CreatedAt   time.Time
	LastLoginAt time.Time // zero when never
	DisabledAt  time.Time // zero when enabled
	// SecondFactor is FactorTOTP once an authenticator is confirmed, empty
	// before.
	SecondFactor string
	Sessions     int // the live browser sessions
	Nodes        int // the nodes not removed
}

// Disabled reports whether the user may not log in.
func (u User) Disabled() bool { return !u.DisabledAt.IsZero() }

// userColumns read a user, its live sessions counted with ?1 and ?2 as live
// has them.
const userColumns = "id, username, created_at, last_login_at, disabled_at, " +
	"(SELECT 'totp' FROM totp_factors f WHERE f.user_id = users.id AND f.confirmed_at IS NOT NULL), " +
	"(SELECT COUNT(*) FROM web_sessions w WHERE w.user_id = users.id AND w.expires_at > ?1 AND w.last_seen_at > ?2), " +
	"(SELECT COUNT(*) FROM nodes n WHERE n.user_id = users.id AND n.revoked_at IS NULL)"

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var created int64
	var login, disabled sql.NullInt64
	var factor sql.NullString
	if err := row.Scan(&u.ID, &u.Username, &created, &login, &disabled, &factor, &u.Sessions, &u.Nodes); err != nil {
		return User{}, err
	}
	u.CreatedAt = time.UnixMilli(created).UTC()
	u.LastLoginAt, u.DisabledAt, u.SecondFactor = fromMS(login), fromMS(disabled), factor.String
	return u, nil
}

// Users returns every user not removed, by username.
func (s *Store) Users(ctx context.Context) ([]User, error) {
	now, idleSince := s.liveArgs()
	rows, err := s.db.QueryContext(ctx, "SELECT "+userColumns+" FROM users WHERE removed_at IS NULL ORDER BY username", now, idleSince)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, u)
	}
	return list, rows.Err()
}

// User returns the user userID, or ErrNotFound when there is none or it was
// removed.
func (s *Store) User(ctx context.Context, userID string) (User, error) {
	return s.userWhere(ctx, "id = ?3", userID)
}

// UserByUsername returns the user named username whatever its case, or
// ErrNotFound when there is none or it was removed.
func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	return s.userWhere(ctx, "username = ?3", username)
}

func (s *Store) userWhere(ctx context.Context, where string, arg string) (User, error) {
	now, idleSince := s.liveArgs()
	u, err := scanUser(s.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE removed_at IS NULL AND "+where, now, idleSince, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// SignUp is what a person signing up gives.
type SignUp struct {
	// Invite is the token of the invite signed up from; empty for none,
	// when RegistrationOpen must be true.
	Invite           string
	RegistrationOpen bool
	Username         string
	Password         string
	UserAgent        string
}

// SignUp creates a user with a password, using up the invite, and starts its
// browser session, without its second factor: the browser goes on to enrol
// one.
func (s *Store) SignUp(ctx context.Context, by By, req SignUp) (User, Started, error) {
	log := logging.FromContext(ctx)
	if req.Invite == "" && !req.RegistrationOpen {
		log.Info("sign-up refused", "reason", "registration_closed")
		return User{}, Started{}, ErrRegistrationClosed
	}
	if err := checkUsername(req.Username); err != nil {
		return User{}, Started{}, err
	}
	if err := checkPassword(req.Password); err != nil {
		return User{}, Started{}, err
	}
	// The invite is checked before the slow hash, and again when used.
	if req.Invite != "" {
		if err := s.CheckInvite(ctx, req.Invite); err != nil {
			return User{}, Started{}, err
		}
	}
	hash, err := s.argon.hash(req.Password)
	if err != nil {
		return User{}, Started{}, err
	}
	id, err := sqlite.NewID("usr")
	if err != nil {
		return User{}, Started{}, err
	}
	now := s.now()
	var started Started
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		var taken int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE username = ?", req.Username).Scan(&taken); err != nil {
			return err
		}
		if taken > 0 {
			// A removed user's name is taken too: it is never given again.
			log.Info("sign-up refused", "reason", "username_taken")
			return ErrUsernameTaken
		}
		if _, err := sqlite.Exec(ctx, tx, "INSERT INTO users (id, username, password_hash, created_at) VALUES (?, ?, ?, ?)",
			id, req.Username, secret(hash), ms(now)); err != nil {
			return err
		}
		details := map[string]any{"invite_id": nil}
		if req.Invite != "" {
			var inviteID string
			err := tx.QueryRowContext(ctx, "UPDATE invites SET used_by = ?, used_at = ? WHERE token_hash = ? AND used_at IS NULL AND expires_at > ? RETURNING id",
				id, ms(now), hashOf(req.Invite), ms(now)).Scan(&inviteID)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrInvalidInvite
			}
			if err != nil {
				return err
			}
			log.Info("invite used", "invite_id", inviteID, "user_id", id)
			details["invite_id"] = inviteID
		}
		var err error
		if started, err = s.startSession(ctx, tx, id, by, req.UserAgent); err != nil {
			return err
		}
		return s.record(ctx, tx, by, id, ActionSignedUp, details)
	})
	if err != nil {
		return User{}, Started{}, err
	}
	log.Info("user signed up", "user_id", id, "username", req.Username)
	return User{ID: id, Username: req.Username, CreatedAt: now.UTC(), Sessions: 1}, started, nil
}

// Login checks the username and password and starts a browser session
// without its second factor, which the browser passes next, or enrols when
// the user has none. A username that does not exist takes as long as a wrong
// password and fails the same, with ErrWrongPassword; a disabled user fails
// with ErrDisabled, but only once the password is right.
func (s *Store) Login(ctx context.Context, by By, username, password, userAgent string) (User, Started, error) {
	log := logging.FromContext(ctx)
	var id string
	var hash sql.NullString
	var disabled sql.NullInt64
	err := s.db.QueryRowContext(ctx, "SELECT id, password_hash, disabled_at FROM users WHERE username = ? AND removed_at IS NULL", username).Scan(&id, &hash, &disabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return User{}, Started{}, err
	}
	if !hash.Valid {
		_, _ = matches(s.dummy(), password)
		log.Info("login refused", "reason", "password")
		return User{}, Started{}, ErrWrongPassword
	}
	ok, err := matches(hash.String, password)
	if err != nil {
		return User{}, Started{}, err
	}
	if !ok {
		log.Info("login refused", "reason", "password", "user_id", id)
		return User{}, Started{}, ErrWrongPassword
	}
	if disabled.Valid {
		log.Info("login refused", "reason", "disabled", "user_id", id)
		return User{}, Started{}, ErrDisabled
	}
	var started Started
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		started, err = s.startSession(ctx, tx, id, by, userAgent)
		return err
	})
	if err != nil {
		return User{}, Started{}, err
	}
	u, err := s.User(ctx, id)
	return u, started, err
}

// checkPasswordOf checks the password of userID, as the Security page asks
// for it before a change.
func (s *Store) checkPasswordOf(ctx context.Context, userID, password string) error {
	var hash sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE id = ? AND removed_at IS NULL", userID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !hash.Valid {
		return ErrWrongPassword
	}
	ok, err := matches(hash.String, password)
	if err != nil {
		return err
	}
	if !ok {
		logging.FromContext(ctx).Info("password refused", "user_id", userID)
		return ErrWrongPassword
	}
	return nil
}

// ChangePassword sets the password of userID, the current one given, and
// ends every other session of theirs than sessionID.
func (s *Store) ChangePassword(ctx context.Context, by By, userID, sessionID, current, password string) error {
	if err := checkPassword(password); err != nil {
		return err
	}
	if err := s.checkPasswordOf(ctx, userID, current); err != nil {
		return err
	}
	hash, err := s.argon.hash(password)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := sqlite.Exec(ctx, tx, "UPDATE users SET password_hash = ? WHERE id = ? AND removed_at IS NULL", secret(hash), userID); err != nil {
			return err
		}
		ended, err := endSessions(ctx, tx, userID, sessionID)
		if err != nil {
			return err
		}
		logging.FromContext(ctx).Info("password changed", "user_id", userID)
		return s.record(ctx, tx, by, userID, ActionPasswordChanged, map[string]any{"sessions_ended": ended})
	})
}

// Disable keeps userID from logging in and ends their sessions; changed is
// false when they were disabled already. Their nodes are refused from then
// on; cutting off their connections is the caller's.
func (s *Store) Disable(ctx context.Context, by By, userID string) (changed bool, err error) {
	return s.setDisabled(ctx, by, userID, true)
}

// Enable lets userID log in again; changed is false when they were not
// disabled.
func (s *Store) Enable(ctx context.Context, by By, userID string) (changed bool, err error) {
	return s.setDisabled(ctx, by, userID, false)
}

func (s *Store) setDisabled(ctx context.Context, by By, userID string, disable bool) (changed bool, err error) {
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		var disabled sql.NullInt64
		err := tx.QueryRowContext(ctx, "SELECT disabled_at FROM users WHERE id = ? AND removed_at IS NULL", userID).Scan(&disabled)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if disabled.Valid == disable {
			return nil
		}
		changed = true
		if !disable {
			if _, err := sqlite.Exec(ctx, tx, "UPDATE users SET disabled_at = NULL WHERE id = ?", userID); err != nil {
				return err
			}
			logging.FromContext(ctx).Info("user enabled", "user_id", userID)
			return s.record(ctx, tx, by, userID, ActionEnabled, nil)
		}
		if _, err := sqlite.Exec(ctx, tx, "UPDATE users SET disabled_at = ? WHERE id = ?", ms(s.now()), userID); err != nil {
			return err
		}
		ended, err := endSessions(ctx, tx, userID, "")
		if err != nil {
			return err
		}
		logging.FromContext(ctx).Info("user disabled", "user_id", userID)
		return s.record(ctx, tx, by, userID, ActionDisabled, map[string]any{"sessions_ended": ended})
	})
	return changed, err
}

// Remove marks userID removed, keeping the row so that the username is never
// given again, and deletes their password hash, factor, recovery codes,
// sessions, resets, the invite they used, their node logins, their nodes and
// their docs, with every revision; cutting off the nodes' connections is the
// caller's.
func (s *Store) Remove(ctx context.Context, by By, userID string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		n, err := sqlite.Exec(ctx, tx, "UPDATE users SET removed_at = ?, password_hash = NULL WHERE id = ? AND removed_at IS NULL", ms(s.now()), userID)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		for _, q := range []string{
			"DELETE FROM totp_factors WHERE user_id = ?",
			"DELETE FROM recovery_codes WHERE user_id = ?",
			"DELETE FROM web_sessions WHERE user_id = ?",
			"DELETE FROM password_resets WHERE user_id = ?",
			"DELETE FROM invites WHERE used_by = ?",
			"DELETE FROM node_logins WHERE user_id = ?",
			"DELETE FROM nodes WHERE user_id = ?",
			"DELETE FROM docs WHERE owner_id = ?",
		} {
			if _, err := sqlite.Exec(ctx, tx, q, userID); err != nil {
				return err
			}
		}
		logging.FromContext(ctx).Info("user removed", "user_id", userID)
		return s.record(ctx, tx, by, userID, ActionRemoved, nil)
	})
}

// RecordLockout records that the rate limit of username refused its first
// try: with the user's id when there is such a user, and nothing typed.
func (s *Store) RecordLockout(ctx context.Context, by By, username string) error {
	var id string
	err := s.db.QueryRowContext(ctx, "SELECT id FROM users WHERE username = ? AND removed_at IS NULL", username).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		logging.FromContext(ctx).Info("user locked out", "user_id", id)
		return s.record(ctx, tx, by, id, ActionLockedOut, nil)
	})
}
