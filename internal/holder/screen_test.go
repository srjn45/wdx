package holder

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestScreenSnapshot paints a fresh screen with another's snapshot and
// compares the two: output recorded from programs in a 100×30 PTY
// (testdata/screen), and sequences that set what the snapshot carries.
func TestScreenSnapshot(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"margins":       "\x1b[5;20r\x1b[10;3Hin margins",
		"origin":        "\x1b[5;20r\x1b[?6h\x1b[3;4Hrelative",
		"hidden cursor": "text\x1b[?25l",
		"no autowrap":   "\x1b[?7l" + strings.Repeat("w", 120),
		"insert mode":   "\x1b[4hab\x1b[Dc",
		"cursor style":  "\x1b[6 q",
		"title":         "\x1b]0;a title\x07",
		"alt margins":   "main\x1b[?1049h\x1b[2;10ralt",
	}
	files, err := filepath.Glob("testdata/screen/*.out")
	if err != nil || len(files) == 0 {
		t.Fatalf("no recordings: %v", err)
	}
	for _, f := range files {
		out, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		cases[strings.TrimSuffix(filepath.Base(f), ".out")] = string(out)
	}
	for name, out := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			a := newScreen(100, 30, nil)
			defer a.close()
			if _, reset := a.write([]byte(out)); reset {
				t.Fatal("the vt reset")
			}
			b := newScreen(100, 30, nil)
			defer b.close()
			if _, reset := b.write(a.snapshot()); reset {
				t.Fatal("the vt reset on the snapshot")
			}
			for _, d := range screenDiffs(a, b) {
				t.Error(d)
			}
		})
	}
}

// screenDiffs lists how b differs from a: cells, cursor, alternate screen,
// margins, title and the modes a's output set.
func screenDiffs(a, b *screen) []string {
	var diffs []string
	for y := range a.vt.Height() {
		for x := range a.vt.Width() {
			ca, cb := a.vt.CellAt(x, y), b.vt.CellAt(x, y)
			if ca == nil || cb == nil {
				if ca != cb {
					diffs = append(diffs, fmt.Sprintf("(%d,%d): %v vs %v", x, y, ca, cb))
				}
				continue
			}
			if ca.Content != cb.Content || ca.Width != cb.Width || !ca.Style.Equal(&cb.Style) || ca.Link != cb.Link {
				diffs = append(diffs, fmt.Sprintf("(%d,%d): %q %v %v vs %q %v %v", x, y, ca.Content, ca.Style, ca.Link, cb.Content, cb.Style, cb.Link))
			}
		}
	}
	if pa, pb := a.vt.CursorPosition(), b.vt.CursorPosition(); pa != pb {
		diffs = append(diffs, fmt.Sprintf("cursor at %v vs %v", pa, pb))
	}
	if a.vt.CursorHidden() != b.vt.CursorHidden() {
		diffs = append(diffs, "cursor visibility differs")
	}
	if sa, ba := a.vt.CursorStyle(); true {
		if sb, bb := b.vt.CursorStyle(); sa != sb || ba != bb {
			diffs = append(diffs, fmt.Sprintf("cursor style %v %v vs %v %v", sa, ba, sb, bb))
		}
	}
	if a.vt.IsAltScreen() != b.vt.IsAltScreen() {
		diffs = append(diffs, "alternate screen differs")
	}
	if ma, mb := a.margins[a.current()], b.margins[b.current()]; ma != mb {
		diffs = append(diffs, fmt.Sprintf("margins %v vs %v", ma, mb))
	}
	if a.title != b.title {
		diffs = append(diffs, fmt.Sprintf("title %q vs %q", a.title, b.title))
	}
	for m, on := range a.modes {
		if !skipMode(m) && b.modes[m] != on {
			diffs = append(diffs, fmt.Sprintf("mode %v: %v vs %v", m, on, b.modes[m]))
		}
	}
	return diffs
}

func TestScreenHistory(t *testing.T) {
	t.Parallel()
	numbered := func(from, to int) string {
		var b strings.Builder
		for i := from; i <= to; i++ {
			fmt.Fprintf(&b, "%d\r\n", i)
		}
		return b.String()
	}
	lines := func(from, to int) []string {
		var l []string
		for i := from; i <= to; i++ {
			l = append(l, fmt.Sprint(i))
		}
		return l
	}
	for _, tc := range []struct {
		name string
		out  string
		want []string
	}{
		{"scrolled off", "a\r\nb\r\nc\r\nd\r\ne", []string{"a", "b"}},
		{"styles and links kept", "\x1b[1;31mred\x1b[0m \x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\\r\n\r\n\r\n", []string{"\x1b[31;1mred\x1b[m \x1b]8;;https://example.com\x07link\x1b]8;;\x07"}},
		{"more than a piece", numbered(1, 1000), lines(1, 998)},
		{"alternate screen kept out", "\x1b[?1049h" + numbered(1, 50) + "\x1b[?1049l", nil},
		{"clear keeps the screen", "one\r\ntwo\x1b[H\x1b[2J\x1b[3J", []string{"one", "two"}},
		{"scrollback cleared", numbered(1, 5) + "\x1b[3J", lines(1, 3)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newScreen(10, 3, nil)
			defer s.close()
			got, reset := s.write([]byte(tc.out))
			if reset {
				t.Fatal("the vt reset")
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestScreenReset feeds the vt input it panics on: the screen starts
// again empty and keeps working.
func TestScreenReset(t *testing.T) {
	t.Parallel()
	s := newScreen(80, 24, nil)
	defer s.close()
	if _, reset := s.write([]byte("before\x1b[1;30r\x1b[30;1H\x1b[S")); !reset {
		t.Fatal("want the screen reset")
	}
	if c := s.vt.CellAt(0, 0); c == nil || c.Content != "" && c.Content != " " {
		t.Fatalf("want the screen empty, got %q", c.Content)
	}
	if _, reset := s.write([]byte("after")); reset {
		t.Fatal("want the new vt to work")
	}
	if got := s.vt.CellAt(0, 0).Content; got != "a" {
		t.Fatalf("got %q, want the output after the reset", got)
	}
}

func TestScreenAnswers(t *testing.T) {
	t.Parallel()
	answers := make(chan []byte, 1)
	s := newScreen(80, 24, func(p []byte) { answers <- p })
	defer s.close()
	s.write([]byte("\x1b[5;7H\x1b[6n"))
	select {
	case got := <-answers:
		if string(got) != "\x1b[5;7R" {
			t.Fatalf("got %q, want the cursor position", got)
		}
	case <-time.After(waitLimit):
		t.Fatal("no answer")
	}

	quiet := newScreen(80, 24, nil)
	defer quiet.close()
	done := make(chan struct{})
	go func() {
		quiet.write([]byte("\x1b[6n\x1b[c"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(waitLimit):
		t.Fatal("unanswered queries block the screen")
	}
}

func TestScreenResize(t *testing.T) {
	t.Parallel()
	s := newScreen(80, 24, nil)
	defer s.close()
	s.write([]byte("\x1b[2;10rtext"))
	if !s.resize(100, 30) {
		t.Fatal("the vt reset")
	}
	if s.vt.Width() != 100 || s.vt.Height() != 30 || s.margins != [2]margins{} {
		t.Fatalf("got %d×%d with margins %v", s.vt.Width(), s.vt.Height(), s.margins)
	}
	b := newScreen(100, 30, nil)
	defer b.close()
	b.write(s.snapshot())
	for _, d := range screenDiffs(s, b) {
		t.Error(d)
	}
}
