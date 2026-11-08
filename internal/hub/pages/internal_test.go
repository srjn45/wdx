package pages

import (
	"net/http"
	"net/netip"
	"testing"
	"time"
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
		l.wrong("Alice")
	}
	l.countTry("192.0.2.1")
	if wait, first := l.userWait("alice"); wait != time.Second || !first {
		t.Fatalf("locked out: %s, %v", wait, first)
	}
	if _, first := l.userWait("ALICE"); first {
		t.Fatal("a second refusal is the first")
	}
	now = now.Add(forgetAfter)
	l.wrong("bob")
	if _, ok := l.streaks["alice"]; ok || len(l.tries) != 0 || len(l.streaks) != 1 {
		t.Fatalf("not swept: %v %v", l.streaks, l.tries)
	}
}
