package tide

import (
	"image/color"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// background is the background m shows the cell at with.
func background(t *testing.T, m *Model, at uv.Position) color.Color {
	t.Helper()
	buf, ok := m.paint(true)
	if !ok {
		t.Fatal("window too small")
	}
	return buf.CellAt(at.X, at.Y).Style.Bg
}

// inside is a cell inside p's frame, away from its edges and labels.
func inside(t *testing.T, m *Model, p pane) uv.Position {
	t.Helper()
	frames, ok := m.frames()
	if !ok {
		t.Fatal("no frames")
	}
	f := frames[p]
	return uv.Pos(f.Max.X-2, f.Max.Y-2)
}

// tide paints its own background: darker inside the focused pane, the base
// everywhere else, and the darker one follows the focus.
func TestBackgrounds(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(80, 24, running("agent-1"))
	m.st = style.New(true)
	base, focused := m.st.Base(), m.st.FocusedBase()
	m.layout.focus = project
	frames, _ := m.frames()
	checks := []struct {
		what string
		at   uv.Position
		want color.Color
	}{
		{"inside the focused pane", inside(t, m, project), focused},
		{"inside the agent pane", inside(t, m, agent), base},
		{"inside the terminal pane", inside(t, m, terminal), base},
		{"on the focused pane's border", frames[project].Min, base},
	}
	for _, c := range checks {
		if got := background(t, m, c.at); got != c.want {
			t.Fatalf("%s: background %v, want %v", c.what, got, c.want)
		}
	}
	m.layout.focus = agent
	if got := background(t, m, inside(t, m, agent)); got != focused {
		t.Fatalf("the agent pane focused: background %v, want %v", got, focused)
	}
	if got := background(t, m, inside(t, m, project)); got != base {
		t.Fatalf("the left pane unfocused: background %v, want %v", got, base)
	}
}

// What sets its own background keeps it: the status line's green.
func TestBackgroundKeptWhereSet(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(80, 24, running("agent-1"))
	m.st = style.New(true)
	got := background(t, m, uv.Pos(0, m.height-1))
	if got == nil || got == m.st.Base() || got == m.st.FocusedBase() {
		t.Fatalf("status line background %v, want its own", got)
	}
}

// A popup over the focused pane is all base, not part the focused pane's
// background.
func TestBackgroundUnderPopup(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(80, 24, running("agent-1"))
	m.st = style.New(true)
	m.layout.focus = agent
	middle := uv.Pos(m.width/2, m.height/2)
	if got := background(t, m, middle); got != m.st.FocusedBase() {
		t.Fatalf("the middle is not in the focused pane: background %v", got)
	}
	m.menu = true
	if got := background(t, m, middle); got != m.st.Base() {
		t.Fatalf("under the menu: background %v, want the base %v", got, m.st.Base())
	}
}

// With color off tide paints no background.
func TestBackgroundOff(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(80, 24, running("agent-1"))
	buf, _ := m.paint(true)
	for y := range buf.Height() {
		for x := range buf.Width() {
			if bg := buf.CellAt(x, y).Style.Bg; bg != nil {
				t.Fatalf("cell %d,%d has background %v with color off", x, y, bg)
			}
		}
	}
}

// A theme saved in the settings tab is painted at once, and color stays as it
// was: off, no theme paints anything.
func TestThemeSaved(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(80, 24, running("agent-1"))
	m.st = style.New(true)
	m.Update(settingSaved{setting: api.Setting{Key: themeSettingKey, Value: "slate"}})
	if got, want := background(t, m, inside(t, m, agent)), style.New(true).WithTheme("slate").Base(); got != want {
		t.Errorf("after the save: background %v, want slate's %v", got, want)
	}
	off := newAgentsModel(80, 24, running("agent-1"))
	off.Update(settingSaved{setting: api.Setting{Key: themeSettingKey, Value: "slate"}})
	if off.st.On() {
		t.Error("saving a theme turned color on")
	}
}
