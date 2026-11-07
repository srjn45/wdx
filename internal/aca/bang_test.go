package aca_test

import (
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

// The table of the wdx doc LEADING_BANG: a \ goes before the first ! when only
// whitespace comes before it, and before a sender's own \! too.
func TestEscapeBang(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, message, want string }{
		{"a leading bang", "!rm -rf build", `\!rm -rf build`},
		{"after spaces", "  !x", `  \!x`},
		{"after a newline", "\n!x", "\n\\!x"},
		{"after a tab", "\t!x", "\t\\!x"},
		{"not after text", "a !x", "a !x"},
		{"a sender's own escape gets another", `\!x`, `\\!x`},
		{"only a bang", "!", `\!`},
		{"empty", "", ""},
		{"only whitespace", " \n\t", " \n\t"},
		{"a bang later in the text", "touch !x", "touch !x"},
		{"a bang on a later line", "a\n!x", "a\n!x"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := aca.EscapeBang(c.message); got != c.want {
				t.Fatalf("escapeBang(%q) = %q, want %q", c.message, got, c.want)
			}
		})
	}
}
