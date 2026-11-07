package tide

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/style"
)

// hex is c as #rrggbb, or "none".
func hex(c color.Color) string {
	if c == nil {
		return "none"
	}
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

// rowTint is the background of the left pane's row holding want, at its last
// cell inside the frame, which only a tint across the row reaches.
func rowTint(t *testing.T, m *Model, want string) string {
	t.Helper()
	buf, ok := m.paint(true)
	if !ok {
		t.Fatal("window too small")
	}
	frames, _ := m.frames()
	f := frames[project]
	for y := f.Min.Y + 1; y < f.Max.Y-1; y++ {
		var line strings.Builder
		for x := f.Min.X + 1; x < f.Max.X-1; x++ {
			line.WriteString(buf.CellAt(x, y).Content)
		}
		if strings.Contains(ansi.Strip(line.String()), want) {
			return hex(buf.CellAt(f.Max.X-2, y).Style.Bg)
		}
	}
	t.Fatalf("no row of %q:\n%s", want, text(m))
	return ""
}

// Each row's tint says what it is: an open tab, the tab shown, the cursor over
// both, the heading a band of its own; and the cursor's goes with the focus.
func TestRowTints(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(100, 41, demoAgents()...)
	m.st = style.New(true)
	const (
		heading = "#363d4e"
		shown   = "#363d4e"
		open    = "#2b313f"
		cursor  = "#264f78"
	)
	focused, base := hex(m.st.FocusedBase()), hex(m.st.Base())

	// swift-falcon is shown, with the cursor on it.
	m.Update(key('k', 0))
	for range 3 {
		m.Update(keyMsgs("up")[0])
	}
	m.Update(keyMsgs("enter")[0]) // bold-heron shown, swift-falcon left open
	m.Update(key('j', 0))         // the cursor on calm-otter
	for _, tc := range []struct{ row, want string }{
		{" Agents", heading},
		{"bold-heron", shown},
		{"swift-falcon", open},
		{"calm-otter", cursor},
		{"wise-owl", focused},
	} {
		if got := rowTint(t, m, tc.row); got != tc.want {
			t.Errorf("%s: tint %s, want %s", tc.row, got, tc.want)
		}
	}

	m.Update(key('k', 0)) // the cursor on the tab shown
	if got := rowTint(t, m, "bold-heron"); got != cursor {
		t.Errorf("the cursor on the tab shown: tint %s, want the cursor's %s", got, cursor)
	}
	m.Update(keyMsgs("alt+right")[0])
	if got := rowTint(t, m, "bold-heron"); got != shown {
		t.Errorf("the pane unfocused: tint %s, want the tab shown's %s", got, shown)
	}
	if got := rowTint(t, m, "wise-owl"); got != base {
		t.Errorf("the pane unfocused: wise-owl %s, want the base %s", got, base)
	}
}

// The cursor on a heading takes its tint over the heading's band.
func TestHeadingUnderCursor(t *testing.T) {
	t.Parallel()
	st := style.New(true)
	if got, want := sectionHeading(st, "▸− Agents", "4 ", true, 20), st.CursorRow(st.Name("▸− Agents         ")+st.Muted("4 ")); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := sectionHeading(style.New(false), "▸− Agents", "4 ", true, 20), "\x1b[7m▸− Agents         4 \x1b[0m"; got != want {
		t.Errorf("color off: got %q, want %q", got, want)
	}
}

// With color off the row's first two cells tell what the tints do.
func TestRowLead(t *testing.T) {
	t.Parallel()
	st := style.New(false)
	for _, tc := range []struct {
		look rowLook
		want string
	}{
		{rowLook{}, "  "},
		{rowLook{open: true}, "  "},
		{rowLook{active: true}, "▌ "},
		{rowLook{cursor: true}, " ▸"},
		{rowLook{cursor: true, active: true}, "▌▸"},
	} {
		if got := tc.look.lead(st); got != tc.want {
			t.Errorf("%+v: lead %q, want %q", tc.look, got, tc.want)
		}
		if got := tc.look.paint(st, "row"); got != "row" {
			t.Errorf("%+v: painted %q with color off", tc.look, got)
		}
	}
}
