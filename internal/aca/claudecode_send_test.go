package aca_test

import (
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

// The paste, then Enter, which queues the message during a turn and submits it at
// rest; to steer, Ctrl+X Ctrl+S, which sends the queued message to the running turn
// now.
func TestClaudeCodeSteps(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, message, want string }{
		{"plain", "hello", "\x1b[200~hello\x1b[201~\r"},
		{"lines stay content", "one\ntwo", "\x1b[200~one\ntwo\x1b[201~\r"},
		{"cannot end the paste", "a\x1b[201~b", "\x1b[200~ab\x1b[201~\r"},
		{"nor by a marker its removal makes", "a\x1b[20\x1b[201~1~b", "\x1b[200~ab\x1b[201~\r"},
		{"a leading bang is given to the model", "!x", "\x1b[200~\\!x\x1b[201~\r"},
		{"even one a marker hid", "\x1b[201~!x", "\x1b[200~\\!x\x1b[201~\r"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := keys(typed(t, aca.ClaudeCode{}, c.message, false)); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
			if got, want := keys(typed(t, aca.ClaudeCode{}, c.message, true)), c.want+"\x18\x13"; got != want {
				t.Errorf("steered: got %q, want %q", got, want)
			}
		})
	}
}

// Ctrl+X Ctrl+S in the same write as Enter is lost, and Enter in the paste's is
// part of it, so each step is a write of its own.
func TestClaudeCodeStepsWriteApart(t *testing.T) {
	t.Parallel()
	if got, want := typed(t, aca.ClaudeCode{}, "hello", true), []string{"\x1b[200~hello\x1b[201~", "\r", "\x18\x13"}; !slices.Equal(got, want) {
		t.Fatalf("wrote %q, want %q", got, want)
	}
}

func TestClaudeCodeInterruptIsEsc(t *testing.T) {
	t.Parallel()
	if got := (aca.ClaudeCode{}).Interrupt(); string(got) != "\x1b" {
		t.Fatalf("got %q", got)
	}
}
