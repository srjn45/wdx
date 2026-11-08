package account

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/sqlite"
)

// Session is a browser's session.
type Session struct {
	ID     string
	UserID string
	// SecondFactor is when the second factor passed; zero until then, when
	// the session may do nothing but pass or enrol it.
	SecondFactor time.Time
	Address      string
	UserAgent    string
	CreatedAt    time.Time
	LastSeenAt   time.Time
	// ExpiresAt is when it ends unless used before: SessionIdle after it
	// was last seen, or SessionMax after it started, whichever is first.
	ExpiresAt time.Time
}

// Started is a session that has just started, or whose token was just
// replaced, with the token the browser keeps.
type Started struct {
	Session
	Token string
}

const sessionColumns = "id, user_id, second_factor_at, address, user_agent, created_at, last_seen_at, expires_at"

// live is the condition of a session that has not expired at ?1, the time
// in milliseconds, nor been idle since ?2.
const live = "expires_at > ?1 AND last_seen_at > ?2"

func (s *Store) liveArgs() (now, idleSince int64) {
	t := s.now()
	return ms(t), ms(t.Add(-SessionIdle))
}

func scanSession(row interface{ Scan(...any) error }) (Session, error) {
	var se Session
	var second sql.NullInt64
	var created, seen, expires int64
	if err := row.Scan(&se.ID, &se.UserID, &second, &se.Address, &se.UserAgent, &created, &seen, &expires); err != nil {
		return Session{}, err
	}
	se.SecondFactor = fromMS(second)
	se.CreatedAt = time.UnixMilli(created).UTC()
	se.LastSeenAt = time.UnixMilli(seen).UTC()
	se.ExpiresAt = time.UnixMilli(min(expires, seen+SessionIdle.Milliseconds())).UTC()
	return se, nil
}

// startSession starts a session of userID without its second factor.
func (s *Store) startSession(ctx context.Context, tx *sql.Tx, userID string, by By, userAgent string) (Started, error) {
	id, err := sqlite.NewID("ws")
	if err != nil {
		return Started{}, err
	}
	token, hash := newToken()
	now := s.now()
	if _, err := sqlite.Exec(ctx, tx, "INSERT INTO web_sessions ("+sessionColumns+", token_hash) VALUES (?, ?, NULL, ?, ?, ?, ?, ?, ?)",
		id, userID, by.Address, userAgent, ms(now), ms(now), ms(now.Add(SessionMax)), secret(hash)); err != nil {
		return Started{}, err
	}
	logging.FromContext(ctx).Info("session started", "user_id", userID, "session_id", id)
	se := Session{ID: id, UserID: userID, Address: by.Address, UserAgent: userAgent, CreatedAt: now.UTC(), LastSeenAt: now.UTC(), ExpiresAt: now.Add(SessionIdle).UTC()}
	return Started{Session: se, Token: token}, nil
}

// passSecondFactor gives the session sessionID of userID its second factor
// and a new token, so that a token seen before is worthless after, and
// records the login. It fails with ErrNoSession for a session that is not
// userID's or not live.
func (s *Store) passSecondFactor(ctx context.Context, tx *sql.Tx, by By, userID, sessionID string, details map[string]any) (Started, error) {
	now, idleSince := s.liveArgs()
	se, err := scanSession(tx.QueryRowContext(ctx, "SELECT "+sessionColumns+" FROM web_sessions WHERE "+live+" AND id = ?3 AND user_id = ?4", now, idleSince, sessionID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return Started{}, ErrNoSession
	}
	if err != nil {
		return Started{}, err
	}
	token, hash := newToken()
	if _, err := sqlite.Exec(ctx, tx, "UPDATE web_sessions SET token_hash = ?, second_factor_at = ?, last_seen_at = ? WHERE id = ? AND user_id = ?",
		secret(hash), now, now, sessionID, userID); err != nil {
		return Started{}, err
	}
	if _, err := sqlite.Exec(ctx, tx, "UPDATE users SET last_login_at = ? WHERE id = ?", now, userID); err != nil {
		return Started{}, err
	}
	details["session_id"] = sessionID
	if err := s.record(ctx, tx, by, userID, ActionLoggedIn, details); err != nil {
		return Started{}, err
	}
	logging.FromContext(ctx).Info("second factor passed", "user_id", userID, "session_id", sessionID)
	se.SecondFactor = time.UnixMilli(now).UTC()
	se.LastSeenAt = se.SecondFactor
	se.ExpiresAt = time.UnixMilli(min(now+SessionIdle.Milliseconds(), ms(se.CreatedAt.Add(SessionMax)))).UTC()
	return Started{Session: se, Token: token}, nil
}

// Session returns the live session whose token the browser sent, of a user
// neither disabled nor removed, or ErrNoSession. It marks the session seen,
// writing so at most once a minute.
func (s *Store) Session(ctx context.Context, token string) (Session, error) {
	now, idleSince := s.liveArgs()
	se, err := scanSession(s.db.QueryRowContext(ctx, "SELECT "+sessionColumns+" FROM web_sessions WHERE "+live+
		" AND token_hash = ?3 AND user_id IN (SELECT id FROM users WHERE disabled_at IS NULL AND removed_at IS NULL)", now, idleSince, hashOf(token)))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, err
	}
	if now-ms(se.LastSeenAt) >= seenEvery.Milliseconds() {
		if _, err := sqlite.Exec(ctx, s.db, "UPDATE web_sessions SET last_seen_at = ? WHERE id = ? AND user_id = ?", now, se.ID, se.UserID); err != nil {
			return Session{}, err
		}
		se.LastSeenAt = time.UnixMilli(now).UTC()
		se.ExpiresAt = time.UnixMilli(min(now+SessionIdle.Milliseconds(), ms(se.CreatedAt.Add(SessionMax)))).UTC()
	}
	return se, nil
}

// Sessions returns the live sessions of userID, the oldest first.
func (s *Store) Sessions(ctx context.Context, userID string) ([]Session, error) {
	now, idleSince := s.liveArgs()
	rows, err := s.db.QueryContext(ctx, "SELECT "+sessionColumns+" FROM web_sessions WHERE "+live+" AND user_id = ?3 ORDER BY created_at, id", now, idleSince, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Session
	for rows.Next() {
		se, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, se)
	}
	return list, rows.Err()
}

// EndSession ends the session sessionID of userID: logging out, or ending
// another browser's from the Security page. It fails with ErrNoSession for a
// session that is not userID's.
func (s *Store) EndSession(ctx context.Context, by By, userID, sessionID string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		n, err := sqlite.Exec(ctx, tx, "DELETE FROM web_sessions WHERE id = ? AND user_id = ?", sessionID, userID)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNoSession
		}
		logging.FromContext(ctx).Info("session ended", "user_id", userID, "session_id", sessionID)
		return s.record(ctx, tx, by, userID, ActionSessionEnded, map[string]any{"session_id": sessionID})
	})
}

// endSessions ends every session of userID but keep, if any, and returns how
// many it ended.
func endSessions(ctx context.Context, tx *sql.Tx, userID, keep string) (int64, error) {
	n, err := sqlite.Exec(ctx, tx, "DELETE FROM web_sessions WHERE user_id = ? AND id != ?", userID, keep)
	if err == nil && n > 0 {
		logging.FromContext(ctx).Info("sessions ended", "user_id", userID, "sessions", n)
	}
	return n, err
}
