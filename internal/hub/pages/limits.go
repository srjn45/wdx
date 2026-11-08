package pages

import (
	"strings"
	"sync"
	"time"
)

// The rate limits of the tries. They are kept in memory: the hub is one
// process, and a restart is its operator's act, not an attacker's.
const (
	// freeTries is how many wrong passwords or codes in a row a username has
	// before each next try waits.
	freeTries = 5
	// firstWait is the wait after the freeTries-th wrong try; each one after
	// doubles it, up to maxWait.
	firstWait = time.Second
	maxWait   = 15 * time.Minute
	// triesPerMinute is how many posts of a password, a code or a token an
	// address may make a minute, wrong invite and reset tokens counted.
	triesPerMinute = 20
	// signUpsPerMinute is how many sign-ups an address may post a minute.
	signUpsPerMinute = 10
	// loginStartsPerMinute is how many node logins an address may start a
	// minute, as many as sign-ups.
	loginStartsPerMinute = signUpsPerMinute
	// forgetAfter is how long after its last wrong try a username's streak
	// is kept, so that memory does not grow with every name ever typed.
	forgetAfter = 24 * time.Hour
)

// limits counts the tries by username and by address.
type limits struct {
	now func() time.Time

	mu      sync.Mutex
	streaks map[string]*streak // by username in lower case
	tries   map[string]*window // by address
	signUps map[string]*window // by address
	// loginStarts are the node logins started, by address.
	loginStarts map[string]*window
	swept       time.Time
}

// streak is a username's wrong tries in a row.
type streak struct {
	wrong int
	last  time.Time // of the last wrong try
	// reported is whether this lockout's first refusal was audited.
	reported bool
}

// window counts an address's tries in the minute from start.
type window struct {
	start time.Time
	n     int
}

func newLimits(now func() time.Time) *limits {
	return &limits{now: now, streaks: map[string]*streak{}, tries: map[string]*window{}, signUps: map[string]*window{}, loginStarts: map[string]*window{}}
}

// wait returns how long the streak makes its next try wait at now.
func (s *streak) wait(now time.Time) time.Duration {
	if s.wrong < freeTries {
		return 0
	}
	delay := maxWait
	if n := s.wrong - freeTries; n < 20 {
		delay = min(firstWait<<n, maxWait)
	}
	return max(s.last.Add(delay).Sub(now), 0)
}

// userWait returns how long username must wait before its next try, 0 when
// it may try now, and whether this refusal is the first of its lockout.
func (l *limits) userWait(username string) (wait time.Duration, first bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.streaks[strings.ToLower(username)]
	if s == nil {
		return 0, false
	}
	if wait = s.wait(l.now()); wait > 0 && !s.reported {
		s.reported, first = true, true
	}
	return wait, first
}

// wrong counts a wrong password or code of username.
func (l *limits) wrong(username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	key := strings.ToLower(username)
	s := l.streaks[key]
	if s == nil {
		s = &streak{}
		l.streaks[key] = s
	}
	s.wrong++
	s.last = now
	s.reported = false
}

// passed ends the streak of username: a try succeeded.
func (l *limits) passed(username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.streaks, strings.ToLower(username))
}

// tryWait returns how long address must wait before its next post of a
// password, a code or a token; 0 when it may post now.
func (l *limits) tryWait(address string) time.Duration {
	return l.windowWait(l.tries, address, triesPerMinute)
}

// countTry counts a post of a password, a code or a token, or a wrong token,
// from address.
func (l *limits) countTry(address string) { l.count(l.tries, address) }

// signUpWait returns how long address must wait before its next sign-up.
func (l *limits) signUpWait(address string) time.Duration {
	return l.windowWait(l.signUps, address, signUpsPerMinute)
}

func (l *limits) countSignUp(address string) { l.count(l.signUps, address) }

// loginStartWait returns how long address must wait before it starts its
// next node login.
func (l *limits) loginStartWait(address string) time.Duration {
	return l.windowWait(l.loginStarts, address, loginStartsPerMinute)
}

func (l *limits) countLoginStart(address string) { l.count(l.loginStarts, address) }

func (l *limits) windowWait(windows map[string]*window, address string, limit int) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	w := windows[address]
	if w == nil || !now.Before(w.start.Add(time.Minute)) || w.n < limit {
		return 0
	}
	return w.start.Add(time.Minute).Sub(now)
}

func (l *limits) count(windows map[string]*window, address string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	w := windows[address]
	if w == nil || !now.Before(w.start.Add(time.Minute)) {
		w = &window{start: now}
		windows[address] = w
	}
	w.n++
}

// sweep forgets, at most once a minute, the windows that have ended and the
// streaks not added to for forgetAfter.
func (l *limits) sweep(now time.Time) {
	if now.Sub(l.swept) < time.Minute {
		return
	}
	l.swept = now
	for _, windows := range []map[string]*window{l.tries, l.signUps, l.loginStarts} {
		for a, w := range windows {
			if !now.Before(w.start.Add(time.Minute)) {
				delete(windows, a)
			}
		}
	}
	for u, s := range l.streaks {
		if now.Sub(s.last) >= forgetAfter {
			delete(l.streaks, u)
		}
	}
}
