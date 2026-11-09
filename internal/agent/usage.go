package agent

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/sqlite"
)

// Usage is how much of one limit of an AI coding agent's account is used, as the ACA
// last told through one of its agents: the latest observation of a window and bucket
// (the wdx doc USAGE). It belongs to the ACA: every agent of it on this machine draws
// on the same account.
type Usage struct {
	ACA string // the ACA's id
	aca.Usage
	// Limited is whether the window is used up: UsedPercent at 100 or more, or the
	// ACA said so. It hides aca.Usage's Limited, which is only what the ACA said.
	Limited bool
	AgentID string // the agent it was read from, for the log; "" once that agent is gone
}

// Usage returns every row, by ACA, bucket and window, so that the rows of one bucket
// are together, the bucket every model draws on first as its name is empty.
func (s *Store) Usage(ctx context.Context) ([]Usage, error) {
	rows, err := s.queries.Query(ctx, "SELECT aca, window, bucket, used_percent, resets_at, limited, observed_at, agent_id FROM aca_usage ORDER BY aca, bucket, window")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Usage
	for rows.Next() {
		var u Usage
		var resets sql.NullInt64
		var observed int64
		var agent sql.NullString
		if err := rows.Scan(&u.ACA, &u.Window, &u.Bucket, &u.UsedPercent, &resets, &u.Limited, &observed, &agent); err != nil {
			return nil, err
		}
		if resets.Valid {
			u.ResetsAt = time.UnixMilli(resets.Int64)
		}
		u.ObservedAt = time.UnixMilli(observed)
		u.AgentID = agent.String
		list = append(list, u)
	}
	return list, rows.Err()
}

// setUsage records rows as what the ACA acaID told through the agent with id, at
// now, and reports whether anything changed. A row replaces the window's when its
// observation is newer, and is limited at 100 % or when the ACA says so, whatever
// the percentage and with or without a reset. One whose reset has passed says
// nothing of now and is skipped, Limited or not, as a limit past its reset is over,
// so that a stale file read late does not bring a window back. A window the rows
// no longer give is dropped once its reset has passed, as Claude Code drops it, or
// at once when it had none; kept until then, as of its last observation. No rows
// change nothing: the caller does not call with none.
func (s *Store) setUsage(ctx context.Context, acaID, agentID string, rows []aca.Usage, now time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	log := logging.FromContext(ctx)
	var changed bool
	keys := make([]string, 0, len(rows))
	args := []any{acaID, now.UnixMilli()}
	for _, u := range rows {
		keys = append(keys, "(?, ?)")
		args = append(args, u.Window, u.Bucket)
		if !u.ResetsAt.IsZero() && !u.ResetsAt.After(now) {
			log.Info("usage of a window past its reset skipped", "window", u.Window, "bucket", u.Bucket, "resets_at", u.ResetsAt)
			continue
		}
		var resets any
		if !u.ResetsAt.IsZero() {
			resets = u.ResetsAt.UnixMilli()
		}
		n, err := sqlite.Exec(ctx, tx, `
			INSERT INTO aca_usage (aca, window, bucket, used_percent, resets_at, limited, observed_at, agent_id)
			VALUES (?1, ?2, ?3, ?4, ?5, ?4 >= 100 OR ?8, ?6, NULLIF(?7, ''))
			ON CONFLICT (aca, window, bucket) DO UPDATE
			SET used_percent = excluded.used_percent, resets_at = excluded.resets_at, limited = excluded.limited, observed_at = excluded.observed_at, agent_id = excluded.agent_id
			WHERE excluded.observed_at > aca_usage.observed_at`,
			acaID, u.Window, u.Bucket, u.UsedPercent, resets, u.ObservedAt.UnixMilli(), agentID, u.Limited)
		if err != nil {
			return false, err
		}
		if n > 0 {
			changed = true
		}
	}
	n, err := sqlite.Exec(ctx, tx, "DELETE FROM aca_usage WHERE aca = ?1 AND (resets_at IS NULL OR resets_at <= ?2) AND (window, bucket) NOT IN (VALUES "+strings.Join(keys, ", ")+")", args...)
	if err != nil {
		return false, err
	}
	if n > 0 {
		log.Info("usage windows no longer told dropped", "aca", acaID, "count", n)
		changed = true
	}
	return changed, tx.Commit()
}
