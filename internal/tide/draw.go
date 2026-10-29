package tide

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/style"
)

func (m *Model) View() tea.View {
	view := tea.NewView(m.render())
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	view.WindowTitle = "tide"
	return view
}

// tooSmall is all a window too small for the panes shows.
const tooSmall = "window too small"

// render draws the window: the panes' frames and content, the menu over
// them and the status line below.
func (m *Model) render() string {
	if m.width < 1 || m.height < 1 {
		return ""
	}
	frames, ok := m.frames()
	if !ok {
		return ansi.Truncate(tooSmall, m.width, "")
	}
	buf := uv.NewScreenBuffer(m.width, m.height)
	m.drawBorders(buf, frames)
	for p, f := range frames {
		if p == agent && m.shown == "" {
			uv.NewStyledString(m.st.Muted("no project open")).Draw(buf, f.Inset(1))
		} else {
			m.pane(p).draw(buf, f.Inset(1), m.st)
		}
		for _, l := range m.labels(p, f) {
			uv.NewStyledString(l.styled).Draw(buf, l.area)
		}
	}
	if m.switcher {
		m.drawSwitcher(buf)
	}
	if m.menu {
		m.drawMenu(buf)
	}
	uv.NewStyledString(m.statusLine()).Draw(buf, uv.Rect(0, m.height-1, m.width, 1))
	return buf.Render()
}

// joins are the sides of a border cell its lines leave by.
type joins uint8

const (
	joinUp joins = 1 << iota
	joinDown
	joinLeft
	joinRight
)

var boxChars = map[joins]string{
	joinLeft | joinRight:                     "─",
	joinUp | joinDown:                        "│",
	joinDown | joinRight:                     "┌",
	joinDown | joinLeft:                      "┐",
	joinUp | joinRight:                       "└",
	joinUp | joinLeft:                        "┘",
	joinUp | joinDown | joinRight:            "├",
	joinUp | joinDown | joinLeft:             "┤",
	joinDown | joinLeft | joinRight:          "┬",
	joinUp | joinLeft | joinRight:            "┴",
	joinUp | joinDown | joinLeft | joinRight: "┼",
}

// border returns the joins of each cell of f's border.
func border(f uv.Rectangle) map[uv.Position]joins {
	cells := map[uv.Position]joins{}
	last := f.Max.Sub(uv.Pos(1, 1))
	for x := f.Min.X; x <= last.X; x++ {
		var j joins
		if x > f.Min.X {
			j |= joinLeft
		}
		if x < last.X {
			j |= joinRight
		}
		cells[uv.Pos(x, f.Min.Y)] |= j
		cells[uv.Pos(x, last.Y)] |= j
	}
	for y := f.Min.Y; y <= last.Y; y++ {
		var j joins
		if y > f.Min.Y {
			j |= joinUp
		}
		if y < last.Y {
			j |= joinDown
		}
		cells[uv.Pos(f.Min.X, y)] |= j
		cells[uv.Pos(last.X, y)] |= j
	}
	return cells
}

// drawBorders draws the frames' borders, joined where they meet, the
// focused frame's styled as focused.
func (m *Model) drawBorders(buf uv.Screen, frames map[pane]uv.Rectangle) {
	cells := map[uv.Position]joins{}
	for _, f := range frames {
		for at, j := range border(f) {
			cells[at] |= j
		}
	}
	focused := border(frames[m.layout.focus])
	for at, j := range cells {
		char := boxChars[j]
		if _, ok := focused[at]; ok {
			char = m.st.Focused(char)
		} else {
			char = m.st.Muted(char)
		}
		uv.NewStyledString(char).Draw(buf, uv.Rect(at.X, at.Y, 1, 1))
	}
}

// label is text on a frame's top border: the pane's title, a tab, the zoom
// button.
type label struct {
	styled string
	area   uv.Rectangle
	tab    int  // the tab a click selects, or -1
	zoom   bool // the zoom button
}

// zoomButton is the button a click zooms or un-zooms a pane with.
const zoomButton = "[⤢]"

// labels returns what p's frame f shows on its top border, from its left
// end after the corner, and the zoom button at its right end when there is
// room for both.
func (m *Model) labels(p pane, f uv.Rectangle) []label {
	var labels []label
	x, end := f.Min.X+1, f.Max.X-1
	button := ansi.StringWidth(zoomButton)
	if f.Dx() >= button+8 {
		end -= button + 1
		labels = append(labels, label{styled: m.st.Muted(zoomButton), area: uv.Rect(end, f.Min.Y, button, 1), tab: -1, zoom: true})
		end--
	}
	add := func(text string, tab int, styled func(string) string) {
		width := min(ansi.StringWidth(text), end-x)
		if width <= 0 {
			return
		}
		labels = append(labels, label{styled: styled(ansi.Truncate(text, width, "")), area: uv.Rect(x, f.Min.Y, width, 1), tab: tab})
		x += width
	}
	switch {
	case p == project && m.projects.loaded:
		add(" ["+m.top().title()+"] ", -1, m.st.Name)
		return labels
	case p != terminal:
		add(" "+p.String()+" ", -1, m.st.Name)
		return labels
	}
	plain := func(s string) string { return s }
	for i, t := range m.tabs {
		add(" ", -1, plain)
		styled := m.st.Muted
		if i == m.tab {
			styled = m.st.Name
		}
		add("["+t.name+"]", i, styled)
	}
	add(" ", -1, plain)
	return labels
}

// drawMenu draws the menu in a box in the middle of the window.
func (m *Model) drawMenu(buf uv.Screen) {
	m.drawBox(buf, []string{
		m.st.Heading("tide"),
		"",
		m.st.Command("o") + "    open project",
		m.st.Command("p") + "    switch project",
		m.st.Command("q") + "    quit",
		m.st.Command("Esc") + "  close",
	})
}

// drawSwitcher draws the project switcher: for now, the open projects.
func (m *Model) drawSwitcher(buf uv.Screen) {
	lines := []string{m.st.Heading("switch project"), ""}
	for _, name := range m.projects.open() {
		lines = append(lines, m.st.Name(name))
	}
	m.drawBox(buf, append(lines, "", m.st.Command("Esc")+"  close"))
}

// drawBox draws lines in a box in the middle of the window.
func (m *Model) drawBox(buf uv.Screen, lines []string) {
	width := 0
	for _, l := range lines {
		width = max(width, ansi.StringWidth(l))
	}
	box := uv.Rect((m.width-width-4)/2, (m.height-1-len(lines)-2)/2, width+4, len(lines)+2)
	box = box.Intersect(uv.Rect(0, 0, m.width, m.height-1))
	if box.Dx() < minFrame || box.Dy() < minFrame {
		return
	}
	for at, j := range border(box) {
		uv.NewStyledString(m.st.Focused(boxChars[j])).Draw(buf, uv.Rect(at.X, at.Y, 1, 1))
	}
	inside := box.Inset(1)
	uv.NewStyledString("").Draw(buf, inside)         // clears it
	inside.Min.X = min(inside.Min.X+1, inside.Max.X) // a space after the border
	uv.NewStyledString(strings.Join(lines, "\n")).Draw(buf, inside)
}

// statusLine says what has the focus on the left and what the keys do on
// the right, in a bar.
func (m *Model) statusLine() string {
	l := m.layout
	left := " "
	if m.shown != "" {
		left += m.st.BarName(m.shown) + " · "
	}
	left += m.st.BarName(l.focus.String())
	if l.focus == terminal {
		left += " · " + m.tabs[m.tab].name
	}
	if l.zoomed != none {
		left += " · zoomed"
	}
	state := style.StatusLive
	right := m.st.BarMuted("Alt+arrows move  C-] zoom  C-\\ terminal  C-q menu")
	switch {
	case m.menu:
		state = style.StatusPrefix
		right = "menu: o open  p switch  q quit  Esc close"
	case m.switcher:
		state = style.StatusPrefix
		right = "switcher: Esc close"
	}
	right += " "
	line := left + " " + right
	if gap := m.width - ansi.StringWidth(left) - ansi.StringWidth(right); gap >= 1 {
		line = left + strings.Repeat(" ", gap) + right
	}
	return m.st.Bar(ansi.Truncate(line, m.width, ""), state)
}
