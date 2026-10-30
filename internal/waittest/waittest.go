// Package waittest gives every wait in the tests one deadline: what is left
// of the test binary's -timeout, less a margin to report the failure. A wait
// so ends only when what it waits for really does not come, however loaded
// the machine is, rather than after a fixed time a busy machine may need.
package waittest

import (
	"testing"
	"time"
)

// margin is the time left, after a wait gives up, to report it and clean
// up before the test binary's -timeout panics.
const margin = 30 * time.Second

// fallback is how long a wait may take when the tests run without a
// -timeout (-timeout 0).
const fallback = 10 * time.Minute

// minimum keeps a wait meaningful when the -timeout is nearly spent.
const minimum = time.Second

// poll is how often For checks.
const poll = 10 * time.Millisecond

// deadliner is a test: *testing.T reports its binary's deadline.
type deadliner interface {
	Deadline() (time.Time, bool)
}

// Deadline is when a wait in t gives up.
func Deadline(t deadliner) time.Time {
	d, ok := t.Deadline()
	if !ok {
		return time.Now().Add(fallback)
	}
	return later(d.Add(-margin), time.Now().Add(minimum))
}

// Limit is how long from now a wait in t may take.
func Limit(t deadliner) time.Duration { return time.Until(Deadline(t)) }

// After returns a channel that gets a value at t's deadline, for a select.
func After(t deadliner) <-chan time.Time { return time.After(Limit(t)) }

// For checks done until it holds, failing t with what it waited for when
// the deadline comes first.
func For(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := Deadline(t)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("waited until the deadline for %s", what)
		}
		time.Sleep(poll)
	}
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
