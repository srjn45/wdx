package tide

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// copying makes m copy to the returned slice, never to the clipboard.
func copying(m *Model) *[]string {
	var copied []string
	m.copy = func(text string) tea.Cmd {
		copied = append(copied, text)
		return nil
	}
	return &copied
}

// reversed reports whether m shows the cell at x, y in reverse.
func reversed(m *Model, x, y int) bool {
	buf, _ := m.paint(true)
	return buf.CellAt(x, y).Style.Attrs&uv.AttrReverse != 0
}

func dragTo(m *Model, x, y int) {
	m.Update(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft})
}

func releaseAt(m *Model, x, y int) tea.Cmd {
	_, cmd := m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	return cmd
}

func TestSelect(t *testing.T) {
	t.Parallel()
	m, _ := newModel(80, 24)
	copied := copying(m)
	frames, _ := m.frames()
	in := frames[project].Inset(1)

	// Dragged into the agent pane and below the project pane, the selection
	// stops at the project pane's edges.
	m.Update(clickAt(in.Min.X, in.Min.Y))
	dragTo(m, in.Max.X+10, in.Min.Y)
	dragTo(m, in.Max.X+20, frames[terminal].Min.Y+2)
	if !reversed(m, in.Min.X, in.Min.Y) || reversed(m, in.Max.X, in.Min.Y) {
		t.Fatal("the selection is not highlighted inside the pane only")
	}
	releaseAt(m, in.Max.X+20, frames[terminal].Min.Y+2)
	if len(*copied) != 1 || !strings.HasPrefix((*copied)[0], "demo\nits detail comes later\n") {
		t.Fatalf("copied %q", *copied)
	}
	if strings.Contains((*copied)[0], "agent") || strings.Contains((*copied)[0], "shell") {
		t.Fatalf("copied another pane: %q", (*copied)[0])
	}
	if m.sel == nil {
		t.Fatal("the selection went on release")
	}

	// A key clears it.
	keys(m, "x")
	if m.sel != nil || reversed(m, in.Min.X, in.Min.Y) {
		t.Fatal("a key left the selection")
	}

	// Dragged backwards, left into the project pane, in the agent pane.
	a := frames[agent].Inset(1)
	m.Update(clickAt(a.Min.X+5, a.Min.Y+1))
	dragTo(m, 0, a.Min.Y)
	releaseAt(m, 0, a.Min.Y)
	if got := strings.Split((*copied)[1], "\n"); len(got) != 2 || got[0] != "agent" || got[1] != "46×17" {
		t.Fatalf("copied %q", got)
	}
}

func TestClickSelectsNothing(t *testing.T) {
	t.Parallel()
	m, _ := newModel(80, 24)
	copied := copying(m)
	m.Update(clickAt(5, 3))
	dragTo(m, 5, 3)
	if cmd := releaseAt(m, 5, 3); cmd != nil || len(*copied) != 0 || m.sel != nil {
		t.Fatalf("a click copied %q", *copied)
	}
	// A release with no press, as after a click on a title, does nothing.
	if cmd := releaseAt(m, 50, 0); cmd != nil {
		t.Fatal("a lone release did something")
	}
}
