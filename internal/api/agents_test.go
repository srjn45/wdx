package api

import "testing"

func TestTokenCount(t *testing.T) {
	t.Parallel()
	for n, want := range map[int64]string{
		0: "0", 999: "999", 1000: "1k", 150000: "150k", 999999: "999k",
		1000000: "1M", 1500000: "1.5M", 1999999: "1.9M", 2000000: "2M",
	} {
		if got := TokenCount(n); got != want {
			t.Errorf("TokenCount(%d) = %q, want %q", n, got, want)
		}
	}
}
