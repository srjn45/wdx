package tide

import "testing"

// Every border cell, joining two sides or more, has a character for any of
// its sides being the focused pane's, so heavy.
func TestBoxCharEveryMix(t *testing.T) {
	t.Parallel()
	for all := joins(0); all < 16; all++ {
		if all&(all-1) == 0 {
			continue // one side or none: no border cell joins so few
		}
		for heavy := joins(0); heavy < 16; heavy++ {
			if heavy&^all == 0 && boxChar(all, heavy) == "" {
				t.Errorf("joins %04b, heavy %04b: no character", all, heavy)
			}
		}
	}
}

func TestBoxChar(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		all, heavy joins
		want       string
	}{
		{joinUp | joinDown, 0, "│"},
		{joinUp | joinDown, joinUp | joinDown, "┃"},
		{joinDown | joinLeft | joinRight, joinDown | joinLeft, "┱"},
		{joinUp | joinLeft | joinRight, joinUp | joinLeft, "┹"},
		{joinUp | joinDown | joinLeft | joinRight, joinUp | joinDown | joinRight, "╊"},
	} {
		if got := boxChar(tc.all, tc.heavy); got != tc.want {
			t.Errorf("joins %04b, heavy %04b: %q, want %q", tc.all, tc.heavy, got, tc.want)
		}
	}
}
