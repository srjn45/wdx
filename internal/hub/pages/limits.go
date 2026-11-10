package pages

import (
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/srjn45/wdx/internal/hub/account"
)

// The rate limits of the tries. They are kept in memory: the hub is one
// process, and a restart is its operator's act, not an attacker's.
const (
	// freeTries is how many wrong passwords or codes in a row a username has
	// from one address before each next try from it waits.
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
	// is kept, so that memory does not grow with every name ever typed. A
	// username's wrong tries from every address are counted over as long.
	forgetAfter = 24 * time.Hour
	// maxStreaks is how many usernames, and usernames with an address, the
	// limits keep a count of; when full, no new one is counted, and the
	// address limits still hold.
	maxStreaks = 10_000
)

// limits counts the tries by username and address and by address alone.
// Each check of a window counts the try it lets through in the same step, so
// that a burst cannot all pass the check before any is counted.
type limits struct {
	now func() time.Time

	mu sync.Mutex
	// streaks hold the wrong tries in a row of a username from an address:
	// a wrong password from one address never locks out another.
	streaks map[streakKey]*streak
	// attacks count a username's wrong tries from any address over a day,
	// for the audit alone: they refuse nothing.
	attacks map[string]*attack // by username in lower case
	tries   map[string]*window // by address
	signUps map[string]*window // by address
	// loginStarts are the node logins started, by address.
	loginStarts map[string]*window
	swept       time.Time
	// fullSaid is when wrong last said the streaks are full.
	fullSaid time.Time
}

type streakKey struct {
	username string // in lower case
	address  string
}

// streak is a username's wrong tries in a row from an address.
type streak struct {
	wrong int
	last  time.Time // of the last wrong try
	// reported is whether this lockout's first refusal was audited.
	reported bool
}

// attack is a username's wrong tries from any address in the day from
// start.
type attack struct {
	start    time.Time
	wrong    int
	reported bool
}

// window counts an address's tries in the minute from start.
type window struct {
	start time.Time
	n     int
}

func newLimits(now func() time.Time) *limits {
	return &limits{now: now, streaks: map[streakKey]*streak{}, attacks: map[string]*attack{},
		tries: map[string]*window{}, signUps: map[string]*window{}, loginStarts: map[string]*window{}}
}

func keyOf(username, address string) streakKey {
	return streakKey{username: strings.ToLower(username), address: limitKey(address)}
}

// limitKey is what the limits count address under: an IPv6 address by its
// /64, which one machine is given whole and may pick any address of, and
// any other as it is.
func limitKey(address string) string {
	a, err := netip.ParseAddr(address)
	if err != nil || !a.Is6() || a.Is4In6() {
		return address
	}
	return netip.PrefixFrom(a, 64).Masked().String()
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

// try returns how long address must wait before its next post of a
// password, a code or a token, of username when not empty, and whether this
// refusal is the first of the username's lockout from address; when it may
// post now, it returns 0 and counts the post against the address.
func (l *limits) try(address, username string) (wait time.Duration, first bool) {
	address = limitKey(address)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if wait = windowWait(l.tries, address, triesPerMinute, now); wait > 0 {
		return wait, false
	}
	if s := l.streaks[keyOf(username, address)]; username != "" && s != nil {
		if wait = s.wait(now); wait > 0 {
			first = !s.reported
			s.reported = true
			return wait, first
		}
	}
	l.count(l.tries, address, now)
	return 0, false
}

// takeTry is try with no username: a post of a token.
func (l *limits) takeTry(address string) time.Duration {
	wait, _ := l.try(address, "")
	return wait
}

// countTry counts a wrong token from address.
func (l *limits) countTry(address string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.count(l.tries, limitKey(address), l.now())
}

// signUp returns how long address must wait before its next sign-up, its
// tries counted too when it carries an invite; when it may sign up now, it
// returns 0 and counts the sign-up.
func (l *limits) signUp(address string, invite bool) time.Duration {
	address = limitKey(address)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	wait := windowWait(l.signUps, address, signUpsPerMinute, now)
	if invite {
		wait = max(wait, windowWait(l.tries, address, triesPerMinute, now))
	}
	if wait == 0 {
		l.count(l.signUps, address, now)
	}
	return wait
}

// loginStart returns how long address must wait before it starts its next
// node login; when it may start one now, it returns 0 and counts it.
func (l *limits) loginStart(address string) time.Duration {
	address = limitKey(address)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	wait := windowWait(l.loginStarts, address, loginStartsPerMinute, now)
	if wait == 0 {
		l.count(l.loginStarts, address, now)
	}
	return wait
}

// counted is what wrong counted.
type counted struct {
	// attacked is the username's wrong tries in a day reaching
	// account.AttackedTries, once a day.
	attacked bool
	// full is a streak not made, as maxStreaks are kept: said at most once
	// a minute.
	full bool
}

// wrong counts a wrong password or code of username from address. A
// username no user can have is not counted: the address limit holds it.
func (l *limits) wrong(username, address string) counted {
	var c counted
	if !account.ValidUsername(username) {
		return c
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	key := keyOf(username, address)
	s := l.streaks[key]
	if s == nil && len(l.streaks) < maxStreaks {
		s = &streak{}
		l.streaks[key] = s
	}
	if s != nil {
		s.wrong++
		s.last = now
		s.reported = false
	}
	a := l.attacks[key.username]
	if a == nil && len(l.attacks) < maxStreaks {
		a = &attack{start: now}
		l.attacks[key.username] = a
	}
	if a != nil {
		a.wrong++
		if a.wrong >= account.AttackedTries && !a.reported {
			a.reported, c.attacked = true, true
		}
	}
	if (s == nil || a == nil) && now.Sub(l.fullSaid) >= time.Minute {
		l.fullSaid, c.full = now, true
	}
	return c
}

// passed ends the streak of username from address: a try succeeded.
func (l *limits) passed(username, address string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.streaks, keyOf(username, address))
}

// windowWait returns how long address must wait at now before its next try
// of windows, which allow limit a minute.
func windowWait(windows map[string]*window, address string, limit int, now time.Time) time.Duration {
	w := windows[address]
	if w == nil || !now.Before(w.start.Add(time.Minute)) || w.n < limit {
		return 0
	}
	return w.start.Add(time.Minute).Sub(now)
}

func (l *limits) count(windows map[string]*window, address string, now time.Time) {
	l.sweep(now)
	w := windows[address]
	if w == nil || !now.Before(w.start.Add(time.Minute)) {
		w = &window{start: now}
		windows[address] = w
	}
	w.n++
}

// sweep forgets, at most once a minute, the windows that have ended, the
// streaks not added to for forgetAfter and the attacks counted over longer.
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
	for k, s := range l.streaks {
		if now.Sub(s.last) >= forgetAfter {
			delete(l.streaks, k)
		}
	}
	for u, a := range l.attacks {
		if now.Sub(a.start) >= forgetAfter {
			delete(l.attacks, u)
		}
	}
}
