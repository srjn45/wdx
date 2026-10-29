package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"strings"

	"github.com/srjn45/wdx/internal/logging"
)

// NewID returns a random id with prefix, as in proj-1a2b3c4d.
func NewID(prefix string) (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(b), nil
}

// Execer is satisfied by *sql.DB and *sql.Tx.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Exec runs a statement that changes rows, logs it at debug and returns
// the number of rows it changed.
func Exec(ctx context.Context, e Execer, query string, args ...any) (int64, error) {
	res, err := e.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	rows, err := res.RowsAffected()
	logging.FromContext(ctx).Debug("sql exec", "query", strings.Join(strings.Fields(query), " "), "args", values(args), "rows", rows)
	return rows, err
}

// values returns args as the database receives them: NULL for an invalid
// sql.NullString rather than the struct.
func values(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = a
		if v, ok := a.(driver.Valuer); ok {
			out[i], _ = v.Value()
		}
	}
	return out
}
