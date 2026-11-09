package aca_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

// settled stands in the writes typed records for each time Submit called settle.
const settled = "<settle>"

// typed runs the steps on message as the holder does, Paste, Submit and, when now,
// SendNow, and returns each write apart, with settled where Submit asked to settle.
func typed(t *testing.T, a aca.AiCodingAgent, message string, now bool) []string {
	t.Helper()
	var w recordingWriter
	if err := a.Paste(&w, message); err != nil {
		t.Fatalf("Paste: %v", err)
	}
	if err := a.Submit(&w, func() { w.writes = append(w.writes, settled) }); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if now {
		if err := a.SendNow(&w); err != nil {
			t.Fatalf("SendNow: %v", err)
		}
	}
	return w.writes
}

// keys is what writes typed into the terminal, settle left out.
func keys(writes []string) string {
	var b strings.Builder
	for _, w := range writes {
		if w != settled {
			b.WriteString(w)
		}
	}
	return b.String()
}

// recordingWriter keeps each write apart.
type recordingWriter struct{ writes []string }

func (w *recordingWriter) Write(p []byte) (int, error) {
	w.writes = append(w.writes, string(p))
	return len(p), nil
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

// Each step fails when its terminal's input does, at any of its writes.
func TestStepsFail(t *testing.T) {
	t.Parallel()
	for _, a := range aca.All() {
		if err := a.Paste(&failingWriter{}, "hello"); err == nil {
			t.Errorf("%s: Paste: no error", a.ID())
		}
		var w recordingWriter
		if err := a.Submit(&w, func() {}); err != nil {
			t.Fatalf("%s: Submit: %v", a.ID(), err)
		}
		for writes := range len(w.writes) {
			if err := a.Submit(&failingWriter{writes: writes}, func() {}); err == nil {
				t.Errorf("%s: Submit: no error after %d writes", a.ID(), writes)
			}
		}
		w = recordingWriter{}
		if err := a.SendNow(&w); err != nil {
			t.Fatalf("%s: SendNow: %v", a.ID(), err)
		}
		if len(w.writes) > 0 && a.SendNow(&failingWriter{}) == nil {
			t.Errorf("%s: SendNow: no error", a.ID())
		}
	}
}

// The steps wait for nothing: only Cursor's Submit asks the holder to settle,
// between its two Enters.
func TestOnlyCursorSettles(t *testing.T) {
	t.Parallel()
	for _, a := range aca.All() {
		settles := 0
		if err := a.Submit(&recordingWriter{}, func() { settles++ }); err != nil {
			t.Fatal(err)
		}
		if want := map[bool]int{true: 1, false: 0}[a.ID() == aca.CursorID]; settles != want {
			t.Errorf("%s: Submit settled %d times, want %d", a.ID(), settles, want)
		}
	}
}
