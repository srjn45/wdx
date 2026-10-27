// Package logging writes structured logs as JSON lines, one file per day, and
// carries the logger of a command through its context.
package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"regexp"
	"time"
)

// New returns a logger that writes records at level and above to w as JSON
// lines, with UTC timestamps and URL credentials redacted.
func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(&redacting{w}, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey && len(groups) == 0 {
				a.Value = slog.TimeValue(a.Value.Time().UTC())
			}
			return a
		},
	}))
}

// credentials matches the user info of a URL, such as a token in
// https://token@github.com/org/repo.
var credentials = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s"]+@`)

// redacting removes URL credentials from every line before it is written, so
// none reaches the file whichever value carries them.
type redacting struct {
	w io.Writer
}

func (r *redacting) Write(p []byte) (int, error) {
	if _, err := r.w.Write(credentials.ReplaceAll(p, []byte("${1}redacted@"))); err != nil {
		return 0, err
	}
	return len(p), nil
}

type contextKey struct{}

var discard = slog.New(slog.DiscardHandler)

// NewContext returns a copy of ctx that carries l.
func NewContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, contextKey{}, l)
}

// FromContext returns the logger ctx carries, or one that discards everything.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(contextKey{}).(*slog.Logger); ok {
		return l
	}
	return discard
}

// NewDebugID returns a random id that ties together the logs of one command.
func NewDebugID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return "dbg-" + hex.EncodeToString(b)
}

// Duration is the time elapsed since start, as an attribute.
func Duration(start time.Time) slog.Attr {
	return slog.Int64("duration_ms", time.Since(start).Milliseconds())
}
