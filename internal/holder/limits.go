package holder

import (
	"cmp"
	"context"
	"time"
)

// How long a holder and those who talk to it wait for each other: each other's
// processes, which a loaded machine can hold back.
const (
	// readyTimeout is how long a holder has to start the shell.
	readyTimeout = 5 * time.Second
	// dialTimeout bounds a dial and the hello that follows, unless ctx ends
	// sooner.
	dialTimeout = 5 * time.Second
	// writeTimeout bounds a write to a peer, so that one that stopped reading
	// cannot hold the writer back, and the wait for a peer's first frame.
	writeTimeout = 5 * time.Second
)

// Limits are those waits, when not the defaults: a test lengthens them so that
// a loaded machine slows it rather than fails it, or shortens the one it tests.
type Limits struct {
	Ready, Dial, Write time.Duration
}

func (l Limits) orDefaults() Limits {
	return Limits{
		Ready: cmp.Or(l.Ready, readyTimeout),
		Dial:  cmp.Or(l.Dial, dialTimeout),
		Write: cmp.Or(l.Write, writeTimeout),
	}
}

type limitsKey struct{}

// WithLimits returns ctx with the limits Start, Dial and Attach go by.
func WithLimits(ctx context.Context, l Limits) context.Context {
	return context.WithValue(ctx, limitsKey{}, l)
}

func limitsOf(ctx context.Context) Limits {
	l, _ := ctx.Value(limitsKey{}).(Limits)
	return l.orDefaults()
}
