package pages

import (
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/hub/account"
)

func TestSafeNext(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]string{
		"":                     "",
		"/devices?code=1":      "/devices?code=1",
		"/security":            "/security",
		"//evil.example/":      "",
		"/\\evil.example":      "",
		"https://evil.example": "",
		"evil.example":         "",
		"/a\r\nLocation: x":    "",
		"javascript:alert(1)":  "",
	} {
		if got := safeNext(raw); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestClientAddress(t *testing.T) {
	t.Parallel()
	proxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	for _, tc := range []struct {
		name, remote string
		forwarded    []string
		want         string
	}{
		{"no proxy", "203.0.113.9:4000", nil, "203.0.113.9"},
		{"an untrusted connection's header", "203.0.113.9:4000", []string{"198.51.100.1"}, "203.0.113.9"},
		{"a trusted proxy", "10.0.0.1:4000", []string{"198.51.100.1"}, "198.51.100.1"},
		{"the rightmost untrusted", "10.0.0.1:4000", []string{"192.0.2.1, 198.51.100.1, 10.0.0.2"}, "198.51.100.1"},
		{"over several headers", "10.0.0.1:4000", []string{"192.0.2.1", "198.51.100.1"}, "198.51.100.1"},
		{"all trusted", "10.0.0.1:4000", []string{"10.0.0.3, 10.0.0.2"}, "10.0.0.3"},
		{"no header", "10.0.0.1:4000", nil, "10.0.0.1"},
		{"a hop that is no address", "10.0.0.1:4000", []string{"198.51.100.1, garbage"}, "10.0.0.1"},
		{"IPv6", "[::1]:4000", []string{"2001:db8::1"}, "2001:db8::1"},
		{"IPv4 in IPv6", "[::ffff:10.0.0.1]:4000", []string{"198.51.100.1"}, "198.51.100.1"},
	} {
		r := &http.Request{RemoteAddr: tc.remote, Header: http.Header{"X-Forwarded-For": tc.forwarded}}
		if got := clientAddress(r, proxies); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

// Fly's proxy connects from 172.16.0.0/12 and sends X-Forwarded-For as the
// client, then the app's ingress IPv4 the client reached: with the range
// alone every client is the ingress, as the log of 2026-11-10 showed; with
// the ingress trusted too, the client is itself.
func TestClientAddressOnFly(t *testing.T) {
	t.Parallel()
	r := &http.Request{RemoteAddr: "172.19.12.233:52000", Header: http.Header{"X-Forwarded-For": {"203.0.113.9, 137.66.38.109"}}}
	rangeAlone := []netip.Prefix{netip.MustParsePrefix("172.16.0.0/12")}
	if got := clientAddress(r, rangeAlone); got != "137.66.38.109" {
		t.Errorf("with the range alone: %s, want the ingress, 137.66.38.109", got)
	}
	both := append(rangeAlone, netip.MustParsePrefix("137.66.38.109/32"))
	if got := clientAddress(r, both); got != "203.0.113.9" {
		t.Errorf("with the range and the ingress: %s, want the client, 203.0.113.9", got)
	}
}

func TestStreakWaits(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	for wrong, want := range map[int]time.Duration{
		4: 0, 5: time.Second, 6: 2 * time.Second, 7: 4 * time.Second,
		14: 512 * time.Second, 15: maxWait, 40: maxWait, 1000: maxWait,
	} {
		s := streak{wrong: wrong, last: at}
		if got := s.wait(at); got != want {
			t.Errorf("after %d wrong: %s, want %s", wrong, got, want)
		}
		if got := s.wait(at.Add(want)); got != 0 {
			t.Errorf("after %d wrong, once waited: %s", wrong, got)
		}
	}
}

// A streak is forgotten a day after its last wrong try, a window once its
// minute is over, so that memory does not grow with every name typed.
func TestLimitsSwept(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	l := newLimits(func() time.Time { return now })
	for range freeTries {
		l.wrong("Alice", "192.0.2.1")
	}
	if wait, first := l.try("192.0.2.1", "alice"); wait != time.Second || !first {
		t.Fatalf("locked out: %s, %v", wait, first)
	}
	if _, first := l.try("192.0.2.1", "ALICE"); first {
		t.Fatal("a second refusal is the first")
	}
	l.countTry("192.0.2.1")
	now = now.Add(forgetAfter)
	l.wrong("bob", "192.0.2.1")
	if _, ok := l.streaks[keyOf("alice", "192.0.2.1")]; ok || len(l.tries) != 0 || len(l.streaks) != 1 || len(l.attacks) != 1 {
		t.Fatalf("not swept: %v %v %v", l.streaks, l.attacks, l.tries)
	}
}

// An IPv6 client is counted by its /64: a machine is given the whole prefix
// and may pick a new address of it for every try.
func TestLimitsByIPv6Prefix(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	l := newLimits(func() time.Time { return now })
	for i := range triesPerMinute {
		if wait := l.takeTry(fmt.Sprintf("2001:db8:1:2::%x", i+1)); wait != 0 {
			t.Fatalf("try %d: wait %s", i+1, wait)
		}
	}
	if wait := l.takeTry("2001:db8:1:2:ffff::9"); wait == 0 {
		t.Error("another address of the /64 let through")
	}
	if wait := l.takeTry("2001:db8:1:3::1"); wait != 0 {
		t.Errorf("another /64 waits %s", wait)
	}
	for i := range freeTries {
		l.wrong("alice", fmt.Sprintf("2001:db8:5:6::%x", i+1))
	}
	if wait, _ := l.try("2001:db8:5:6::abc", "alice"); wait == 0 {
		t.Error("a username's streak from the /64 not counted")
	}
	for i := range loginStartsPerMinute {
		l.loginStart(fmt.Sprintf("2001:db8:7:8::%x", i+1))
	}
	if wait := l.loginStart("2001:db8:7:8::ffff"); wait == 0 {
		t.Error("login starts of the /64 not counted")
	}
	for _, a := range []string{"192.0.2.1", "::ffff:192.0.2.1", "not an address"} {
		if got := limitKey(a); got != a {
			t.Errorf("limitKey(%q) = %q", a, got)
		}
	}
}

// A burst from one address is let through only as far as its window allows:
// each check counts the try it lets through.
func TestTryCounted(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	l := newLimits(func() time.Time { return now })
	for i := range triesPerMinute + 5 {
		if wait := l.takeTry("192.0.2.1"); (wait == 0) != (i < triesPerMinute) {
			t.Fatalf("try %d: wait %s", i+1, wait)
		}
	}
	for i := range signUpsPerMinute + 1 {
		if wait := l.signUp("192.0.2.1", false); (wait == 0) != (i < signUpsPerMinute) {
			t.Fatalf("sign-up %d: wait %s", i+1, wait)
		}
	}
	for i := range loginStartsPerMinute + 1 {
		if wait := l.loginStart("192.0.2.1"); (wait == 0) != (i < loginStartsPerMinute) {
			t.Fatalf("login start %d: wait %s", i+1, wait)
		}
	}
}

// A streak is a username's from one address; the wrong tries of a username
// from every address are counted for the audit alone, once a day.
func TestStreakPerAddress(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	l := newLimits(func() time.Time { return now })
	attacked := 0
	for i := range account.AttackedTries + 10 {
		if l.wrong("alice", fmt.Sprintf("192.0.2.%d", i%2)).attacked {
			attacked++
			if i != account.AttackedTries-1 {
				t.Errorf("attacked at the %dth wrong try", i+1)
			}
		}
	}
	if attacked != 1 {
		t.Errorf("attacked %d times", attacked)
	}
	if wait, _ := l.try("192.0.2.0", "alice"); wait == 0 {
		t.Error("the address of the wrong tries is not locked out")
	}
	if wait, _ := l.try("198.51.100.1", "alice"); wait != 0 {
		t.Errorf("another address waits %s", wait)
	}
	l.passed("alice", "192.0.2.0")
	if wait, _ := l.try("192.0.2.1", "alice"); wait == 0 {
		t.Error("a success from one address ended the streak of another")
	}
}

// A username no user can have makes no streak; at most maxStreaks are kept,
// and a streak not made is said once a minute.
func TestStreaksBounded(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	l := newLimits(func() time.Time { return now })
	if c := l.wrong(strings.Repeat("a", 60_000), "192.0.2.1"); c != (counted{}) || len(l.streaks) != 0 || len(l.attacks) != 0 {
		t.Fatalf("a username no user can have: %+v, %d streaks", c, len(l.streaks))
	}
	for i := range maxStreaks {
		if c := l.wrong(fmt.Sprintf("u%d", i), "192.0.2.1"); c.full {
			t.Fatalf("full at %d", i)
		}
	}
	if c := l.wrong("one-more", "192.0.2.1"); !c.full {
		t.Error("a streak not made, not said")
	}
	if c := l.wrong("two-more", "192.0.2.1"); c.full {
		t.Error("said twice in a minute")
	}
	if len(l.streaks) != maxStreaks || len(l.attacks) != maxStreaks {
		t.Errorf("%d streaks, %d attacks, want %d", len(l.streaks), len(l.attacks), maxStreaks)
	}
	// A kept streak still counts.
	for range freeTries {
		l.wrong("u1", "192.0.2.1")
	}
	if wait, _ := l.try("192.0.2.2", "u1"); wait != 0 {
		t.Errorf("another address waits %s", wait)
	}
	if wait, _ := l.try("192.0.2.1", "u1"); wait == 0 {
		t.Error("a kept streak no longer counts")
	}
	now = now.Add(time.Minute)
	if c := l.wrong("three-more", "192.0.2.1"); !c.full {
		t.Error("not said again a minute later")
	}
}
