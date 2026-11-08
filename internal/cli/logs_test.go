package cli

import "testing"

func TestProcessOf(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"holder", "--id", "term-1a2b3c4d"}, "holder"},
		{[]string{"agent", "list"}, "cli"},
		{[]string{"terminal", "create", "--name", "holder"}, "cli"},
		{nil, "cli"},
	} {
		if got := processOf(c.args); got != c.want {
			t.Errorf("processOf(%q) = %q, want %q", c.args, got, c.want)
		}
	}
}
