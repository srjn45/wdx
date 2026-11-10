package hubtest

import "testing"

// A code of digits is matched as a word of its own, never in a line's time,
// whose nanoseconds hold any six digits now and then.
func TestLeaked(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		line, secret string
		want         bool
	}{
		{`{"time":"2026-11-10T19:39:03.45866705Z","msg":"stream refused"}`, "586670", false},
		{`{"time":"2026-11-10T19:39:03Z","msg":"code 586670 checked"}`, "586670", true},
		{`{"msg":"id 1586670"}`, "586670", false},
		{`{"msg":"key abc-def"}`, "abc-def", true},
		{`{"msg":"key abcdef"}`, "abc-def", true},
		{`not json 586670`, "586670", true},
	} {
		if got := Leaked([]byte(tc.line), tc.secret); got != tc.want {
			t.Errorf("Leaked(%s, %q) = %v, want %v", tc.line, tc.secret, got, tc.want)
		}
	}
}
