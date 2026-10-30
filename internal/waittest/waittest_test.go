package waittest

import (
	"testing"
	"time"
)

type fixed struct {
	at time.Time
	ok bool
}

func (f fixed) Deadline() (time.Time, bool) { return f.at, f.ok }

func TestDeadline(t *testing.T) {
	t.Parallel()
	now := time.Now()
	for _, tc := range []struct {
		name     string
		t        fixed
		from, to time.Duration // the deadline is between now+from and now+to
	}{
		{"the -timeout less the margin", fixed{now.Add(10 * time.Minute), true}, 9*time.Minute + 29*time.Second, 9*time.Minute + 31*time.Second},
		{"no -timeout", fixed{}, fallback - time.Second, fallback + time.Second},
		{"the -timeout nearly spent", fixed{now.Add(5 * time.Second), true}, minimum - 100*time.Millisecond, minimum + time.Second},
	} {
		got := Deadline(tc.t)
		if got.Before(now.Add(tc.from)) || got.After(now.Add(tc.to)) {
			t.Errorf("%s: deadline in %s", tc.name, got.Sub(now))
		}
	}
}

func TestFor(t *testing.T) {
	t.Parallel()
	start := time.Now()
	For(t, "a moment", func() bool { return time.Since(start) > 30*time.Millisecond })
}
