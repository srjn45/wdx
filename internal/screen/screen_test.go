package screen

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// TestScreenSnapshot paints a fresh screen with another's snapshot and
// compares the two: output recorded from programs in a 100×30 PTY
// (testdata), and sequences that set what the snapshot carries. Both then
// leave the alternate screen and print text that sets nothing, which must
// look the same on both.
func TestScreenSnapshot(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"margins":        "\x1b[5;20r\x1b[10;3Hin margins",
		"origin":         "\x1b[5;20r\x1b[?6h\x1b[3;4Hrelative",
		"hidden cursor":  "text\x1b[?25l",
		"no autowrap":    "\x1b[?7l" + strings.Repeat("w", 120),
		"insert mode":    "\x1b[4hab\x1b[Dc",
		"cursor style":   "\x1b[6 q",
		"title":          "\x1b]0;a title\x07",
		"alt margins":    "main\x1b[?1049h\x1b[2;10ralt",
		"lr margins":     "\x1b[?69h\x1b[5;20s\x1b[3;10Hin margins",
		"origin lr":      "\x1b[?69h\x1b[5;20s\x1b[4;12r\x1b[?6h\x1b[2;3Hrelative",
		"pen":            "\x1b[1;31mbold red, not repeated",
		"link":           "\x1b]8;;https://example.com\x07linked",
		"line drawing":   "\x1b(0lqk",
		"shift out":      "\x1b)0\x0elqk",
		"main under alt": "shell\r\n\x1b[1m$ \x1b]8;;https://example.com\x07\x1b[?1049h\x1b[0;7mless",
	}
	files, err := filepath.Glob("testdata/*.out")
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
			a := New(100, 30, nil)
			defer a.Close()
			if _, reset := a.Write([]byte(out)); reset {
				t.Fatal("the vt reset")
			}
			b := New(100, 30, nil)
			defer b.Close()
			if _, reset := b.Write(a.Snapshot()); reset {
				t.Fatal("the vt reset on the snapshot")
			}
			for _, d := range screenDiffs(a, b) {
				t.Error(d)
			}
			const after = "\x1b[?1049lafter"
			a.Write([]byte(after))
			b.Write([]byte(after))
			for _, d := range screenDiffs(a, b) {
				t.Error("after: " + d)
			}
		})
	}
}

// screenDiffs lists how b differs from a: both screens' cells, cursors and
// pens, the alternate screen, margins, character sets, title and the modes
// a's output set.
func screenDiffs(a, b *Screen) []string {
	diffs := cellDiffs("main", a.vt.MainScreen(), b.vt.MainScreen())
	diffs = append(diffs, cursorDiffs("main", a.vt.MainScreen(), b.vt.MainScreen())...)
	if a.vt.IsAltScreen() {
		diffs = append(diffs, cellDiffs("alt", a.vt.AltScreen(), b.vt.AltScreen())...)
		diffs = append(diffs, cursorDiffs("alt", a.vt.AltScreen(), b.vt.AltScreen())...)
	}
	return append(diffs, stateDiffs(a, b)...)
}

func cellDiffs(name string, a, b *vt.Screen) []string {
	var diffs []string
	for y := range a.Height() {
		for x := range a.Width() {
			ca, cb := a.CellAt(x, y), b.CellAt(x, y)
			if ca == nil || cb == nil {
				if ca != cb {
					diffs = append(diffs, fmt.Sprintf("%s (%d,%d): %v vs %v", name, x, y, ca, cb))
				}
				continue
			}
			if ca.Content != cb.Content || ca.Width != cb.Width || !ca.Style.Equal(&cb.Style) || ca.Link != cb.Link {
				diffs = append(diffs, fmt.Sprintf("%s (%d,%d): %q %v %v vs %q %v %v", name, x, y, ca.Content, ca.Style, ca.Link, cb.Content, cb.Style, cb.Link))
			}
		}
	}
	return diffs
}

func cursorDiffs(name string, a, b *vt.Screen) []string {
	var diffs []string
	ca, cb := a.Cursor(), b.Cursor()
	if ca.Position != cb.Position {
		diffs = append(diffs, fmt.Sprintf("%s cursor at %v vs %v", name, ca.Position, cb.Position))
	}
	if !ca.Pen.Equal(&cb.Pen) || ca.Link != cb.Link {
		diffs = append(diffs, fmt.Sprintf("%s pen %v %v vs %v %v", name, ca.Pen, ca.Link, cb.Pen, cb.Link))
	}
	return diffs
}

// stateDiffs compares what is not the screens' cells and cursors.
func stateDiffs(a, b *Screen) []string {
	var diffs []string
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
	if ra, rb := a.region(), b.region(); ra != rb {
		diffs = append(diffs, fmt.Sprintf("margins %v vs %v", ra, rb))
	}
	if ga, gla, _ := a.vt.CharSets(); true {
		if gb, glb, _ := b.vt.CharSets(); ga != gb || gla != glb {
			diffs = append(diffs, fmt.Sprintf("character sets %q G%d vs %q G%d", ga, gla, gb, glb))
		}
	}
	if a.Title() != b.Title() {
		diffs = append(diffs, fmt.Sprintf("title %q vs %q", a.Title(), b.Title()))
	}
	ma, mb := a.vt.Modes(), b.vt.Modes()
	for m, setting := range ma {
		if !skipMode(m) && setting.IsSet() != mb[m].IsSet() {
			diffs = append(diffs, fmt.Sprintf("mode %v: %v vs %v", m, setting, mb[m]))
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
		{"region below the top kept out", "\x1b[2;3r\x1b[2;1H" + numbered(1, 5), nil},
		{"deleted lines kept out", "1\r\n2\x1b[H\x1b[2M", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := New(10, 3, nil)
			defer s.Close()
			got, reset := s.Write([]byte(tc.out))
			if reset {
				t.Fatal("the vt reset")
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestScreenAltScreenKeepsNothing: output scrolled off the alternate
// screen holds no memory once it scrolled off (docs/VT.md #1).
func TestScreenAltScreenKeepsNothing(t *testing.T) {
	t.Parallel()
	s := New(10, 3, nil)
	defer s.Close()
	s.Write([]byte("\x1b[?1049h" + strings.Repeat("alt\r\n", 50)))
	if sb := s.vt.AltScreen().Scrollback(); sb != nil && sb.Len() > 0 {
		t.Fatalf("want nothing kept, got %d lines", sb.Len())
	}
}

// TestScreenReset feeds the vt output it fails on: the screen starts again
// empty, draws what follows in the same write, and keeps working.
func TestScreenReset(t *testing.T) {
	t.Parallel()
	s := New(80, 24, nil)
	defer s.Close()
	s.FailOn('z')
	if _, reset := s.Write([]byte("before\x1b[z\x1b[2K\rafter")); !reset {
		t.Fatal("want the screen reset")
	}
	if got := strings.TrimRight(ansi.Strip(s.Render()), " \n"); got != "after" {
		t.Fatalf("got %q, want only the output after the failure", got)
	}
	if _, reset := s.Write([]byte(" more")); reset {
		t.Fatal("want the new vt to work")
	}
	if got := strings.TrimRight(ansi.Strip(s.Render()), " \n"); got != "after more" {
		t.Fatalf("got %q, want the output after the reset", got)
	}
}

// waitLimit bounds every wait for the vt's answers.
const waitLimit = 10 * time.Second

func TestScreenAnswers(t *testing.T) {
	t.Parallel()
	answers := make(chan []byte, 1)
	s := New(80, 24, func(p []byte) { answers <- p })
	defer s.Close()
	s.Write([]byte("\x1b[5;7H\x1b[6n"))
	select {
	case got := <-answers:
		if string(got) != "\x1b[5;7R" {
			t.Fatalf("got %q, want the cursor position", got)
		}
	case <-time.After(waitLimit):
		t.Fatal("no answer")
	}

	quiet := New(80, 24, nil)
	defer quiet.Close()
	done := make(chan struct{})
	go func() {
		quiet.Write([]byte("\x1b[6n\x1b[c"))
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
	s := New(80, 24, nil)
	defer s.Close()
	s.Write([]byte("\x1b[2;10rtext"))
	if !s.Resize(100, 30) {
		t.Fatal("the vt reset")
	}
	if r := s.region(); s.vt.Width() != 100 || s.vt.Height() != 30 || r != s.vt.Bounds() {
		t.Fatalf("got %d×%d with margins %v", s.vt.Width(), s.vt.Height(), r)
	}
	b := New(100, 30, nil)
	defer b.Close()
	b.Write(s.Snapshot())
	for _, d := range screenDiffs(s, b) {
		t.Error(d)
	}
}
