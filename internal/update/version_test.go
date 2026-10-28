package update

import "testing"

func TestNewer(t *testing.T) {
	t.Parallel()
	// Each is newer than the one before it.
	order := []string{
		"dev",
		"0.1.0",
		"0.1.0-3-g46901b2",
		"0.1.1-dev.1",
		"0.1.1-dev.2",
		"0.1.1-dev.2-1-gabc1234",
		"0.1.1-dev.10",
		"0.1.1",
		"v0.2.0",
		"0.10.0",
		"1.0.0",
	}
	for i, a := range order {
		for j, b := range order {
			if got := newer(a, b); got != (i > j) {
				t.Errorf("newer(%q, %q) = %v", a, b, got)
			}
		}
	}
}
