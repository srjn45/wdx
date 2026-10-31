package screen

import (
	"testing"
	"time"
)

func TestMotionWake(t *testing.T) {
	t.Parallel()
	const still = time.Second
	box := ScreenState{Activity: ActivityUnknown, InputBox: true}
	wants := func(t *testing.T, m *Motion, want time.Duration, wantOK bool) {
		t.Helper()
		if at, ok := m.Wake(); ok != wantOK || (ok && at != want) {
			t.Errorf("Wake() = %s, %v, want %s, %v", at, ok, want, wantOK)
		}
	}
	m := NewMotion(MotionRules{Still: still, Echo: 100 * time.Millisecond})
	wants(t, m, 0, false)
	m.Screen(time.Millisecond, true, box)
	wants(t, m, time.Millisecond+still, true)
	// Input holds the answer past the motion's time, which comes first.
	m.Input(500 * time.Millisecond)
	wants(t, m, time.Millisecond+still, true)
	m.Advance(time.Millisecond + still)
	wants(t, m, 500*time.Millisecond+still, true)
	m.Advance(500*time.Millisecond + still)
	wants(t, m, 0, false)
	if got := m.State(); got != (ScreenState{Activity: ActivityIdle, InputBox: true}) {
		t.Errorf("state = %+v", got)
	}
	// What the parser is sure of does not depend on the time.
	m.Screen(3*time.Second, true, ScreenState{Activity: ActivityWaiting})
	wants(t, m, 0, false)
}

// TestScreenTextOfWideAndDimCells: Text and Undimmed read the cells as a terminal
// shows them: a wide character takes two columns, one cell of text.
func TestScreenTextOfWideAndDimCells(t *testing.T) {
	t.Parallel()
	s := New(20, 3, nil)
	defer s.Close()
	s.Write([]byte("世界 \x1b[2mdim\x1b[22m end  \r\n\x1b[1;31mbold\x1b[0m"))
	if got, want := s.Text(), []string{"世界 dim end", "bold"}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Text = %q, want %q", got, want)
	}
	if got, want := s.Undimmed(), []string{"世界     end", "bold", ""}; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("Undimmed = %q, want %q", got, want)
	}
}
