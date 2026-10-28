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

// Quiet returns a logger that writes what l would at debug level instead,
// errors excepted: for runs too frequent to be worth reading, such as tab
// completions.
func Quiet(l *slog.Logger) *slog.Logger {
	return slog.New(quiet{l.Handler()})
}

type quiet struct {
	slog.Handler
}

func (q quiet) Enabled(ctx context.Context, level slog.Level) bool {
	return q.Handler.Enabled(ctx, demote(level))
}

func (q quiet) Handle(ctx context.Context, r slog.Record) error {
	r.Level = demote(r.Level)
	return q.Handler.Handle(ctx, r)
}

func (q quiet) WithAttrs(attrs []slog.Attr) slog.Handler {
	return quiet{q.Handler.WithAttrs(attrs)}
}

func (q quiet) WithGroup(name string) slog.Handler {
	return quiet{q.Handler.WithGroup(name)}
}

func demote(level slog.Level) slog.Level {
	if level < slog.LevelError {
		return min(level, slog.LevelDebug)
	}
	return level
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

var debugIDPattern = regexp.MustCompile(`^dbg-[0-9a-f]{12}$`)

// IsDebugID reports whether s has the form NewDebugID returns, so an id
// received from another process can be trusted in the logs.
func IsDebugID(s string) bool {
	return debugIDPattern.MatchString(s)
}

// Duration is the time elapsed since start, as an attribute.
func Duration(start time.Time) slog.Attr {
	return slog.Int64("duration_ms", time.Since(start).Milliseconds())
}
