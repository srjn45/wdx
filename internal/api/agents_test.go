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

func TestContextText(t *testing.T) {
	t.Parallel()
	window, none := int64(200_000), int64(0)
	for _, tc := range []struct {
		context AgentContext
		want    string
	}{
		{AgentContext{Tokens: 70_086, Window: &window}, "70k of 200k (35%)"},
		{AgentContext{Tokens: 199_999, Window: &window}, "199k of 200k (99%)"},
		{AgentContext{Tokens: 0, Window: &window}, "0 of 200k (0%)"},
		{AgentContext{Tokens: 70_086}, "70k"},
		{AgentContext{Tokens: 70_086, Window: &none}, "70k"},
	} {
		if got := ContextText(tc.context); got != tc.want {
			t.Errorf("ContextText(%+v) = %q, want %q", tc.context, got, tc.want)
		}
	}
}
