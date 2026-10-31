package aca_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

func TestClaudeCodeDeliver(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, message, want string }{
		{"plain", "hello", "\x1b[200~hello\x1b[201~\r"},
		{"lines stay content", "one\ntwo", "\x1b[200~one\ntwo\x1b[201~\r"},
		{"cannot end the paste", "a\x1b[201~b", "\x1b[200~ab\x1b[201~\r"},
		{"nor by a marker its removal makes", "a\x1b[20\x1b[201~1~b", "\x1b[200~ab\x1b[201~\r"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var got bytes.Buffer
			if err := (aca.ClaudeCode{}).Deliver(&got, c.message); err != nil {
				t.Fatal(err)
			}
			if got.String() != c.want {
				t.Fatalf("got %q, want %q", got.String(), c.want)
			}
		})
	}
}

// failingWriter takes writes until it has none left.
type failingWriter struct{ writes int }

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.writes == 0 {
		return 0, errors.New("closed")
	}
	w.writes--
	return len(p), nil
}

func TestClaudeCodeDeliverFails(t *testing.T) {
	t.Parallel()
	for writes := range 2 {
		if err := (aca.ClaudeCode{}).Deliver(&failingWriter{writes: writes}, "hello"); err == nil {
			t.Fatalf("no error after %d writes", writes)
		}
	}
}

func TestClaudeCodeInterruptIsEsc(t *testing.T) {
	t.Parallel()
	if got := (aca.ClaudeCode{}).Interrupt(); string(got) != "\x1b" {
		t.Fatalf("got %q", got)
	}
}
