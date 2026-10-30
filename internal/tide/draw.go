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
	view.Cursor = m.paneCursor()
	return view
}

// tooSmall is all a window too small for the panes shows.
const tooSmall = "window too small"

// render draws the window.
func (m *Model) render() string {
	if m.width < 1 || m.height < 1 {
		return ""
	}
	buf, ok := m.paint(true)
	if !ok {
		return ansi.Truncate(tooSmall, m.width, "")
	}
	return buf.Render()
}

// paint draws the window in a buffer: the panes' frames and content, the
// selection when highlight, the popups over them and the status line
// below. It fails when the window is too small for the panes.
func (m *Model) paint(highlight bool) (uv.ScreenBuffer, bool) {
	frames, ok := m.frames()
	if !ok {
		return uv.ScreenBuffer{}, false
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
	if highlight && m.sel != nil && m.sel.dragged {
		m.sel.highlight(buf)
	}
	if m.stripShown {
		m.drawStrip(buf)
	}
	if m.switcher != nil {
		m.drawPopup(buf, m.switcher.popup(m))
	}
	if s := m.openTop(); s != nil {
		m.drawScreenPopup(buf, s)
	}
	if m.confirm != nil {
		m.drawPopup(buf, m.confirmPopup())
	}
	if m.menu {
		m.drawMenu(buf)
	}
	uv.NewStyledString(m.statusLine()).Draw(buf, uv.Rect(0, m.height-1, m.width, 1))
	return buf, true
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
		add(" ["+m.root.title()+"] ", -1, m.st.Name)
		return labels
	case p != terminal:
		title := p.String()
		if a, ok := m.activeAgent(); ok && p == agent {
			title = a.Name
		}
		add(" "+title+" ", -1, m.st.Name)
		return labels
	}
	plain := func(s string) string { return s }
	for i, t := range m.terminals() {
		add(" ", -1, plain)
		styled := m.st.Muted
		if i == m.tab {
			styled = m.st.Name
		}
		// The mark keeps its state's color unless the tab is cut short.
		mark := stateMark(m.st, t.State)
		add("[● "+t.Name+"]", i, func(s string) string {
			if s != "[● "+t.Name+"]" {
				return styled(s)
			}
			return styled("[") + mark + styled(" "+t.Name+"]")
		})
	}
	add(" ", -1, plain)
	return labels
}

// drawMenu draws the menu in a popup.
func (m *Model) drawMenu(buf uv.Screen) {
	var body []string
	for _, it := range m.menuItems() {
		body = append(body, m.st.Command(it.key)+"  "+it.label)
	}
	m.drawPopup(buf, popup{title: "Menu", body: body, footer: m.st.Command("Esc") + " close"})
}

// popup is a box over the middle of the window: a title, a body and a
// footer, set apart by lines.
type popup struct {
	title  string
	body   []string
	footer string
}

// Around a popup's frame, its outer frame and its margin: a row costs about
// what two columns do on the screen, a terminal's cells being twice as tall
// as they are wide, so these leave it equidistant.
const (
	layerColumns = 2
	layerRows    = 1

	// What a popup takes around its body: its frame, then the outer frame
	// with its gap, then the margin.
	popupColumns = 4 + 2 + 2*layerColumns + 2*layerColumns
	popupRows    = 6 + 2 + 2*layerRows + 2*layerRows
)

// drawPopup draws p in the middle of the window, over what is there, and
// returns where its body is. A heavy frame holds it, inside a muted outer
// frame and a blank margin, which go first when the window is small: the
// margin, then the outer frame. The body is cut at the bottom when the
// window is short.
func (m *Model) drawPopup(buf uv.Screen, p popup) uv.Rectangle {
	width := max(ansi.StringWidth(p.title), ansi.StringWidth(p.footer))
	for _, l := range p.body {
		width = max(width, ansi.StringWidth(l))
	}
	width = min(width+4, m.width)
	area := m.height - 1 // above the status line
	body := min(len(p.body), area-6)
	if width < 6 || body < 1 {
		return uv.Rectangle{}
	}
	height := body + 6 // borders, title, two separators, footer
	rule := func(left, fill, right string) string {
		return m.st.Focused(left + strings.Repeat(fill, width-2) + right)
	}
	row := func(text string) string {
		text = ansi.Truncate(text, width-4, "…")
		pad := strings.Repeat(" ", width-4-ansi.StringWidth(text))
		return m.st.Focused("┃") + " " + text + pad + " " + m.st.Focused("┃")
	}
	lines := []string{rule("┏", "━", "┓"), row(m.st.Heading(p.title)), rule("┣", "━", "┫")}
	for _, l := range p.body[:body] {
		lines = append(lines, row(l))
	}
	lines = append(lines, rule("┣", "━", "┫"), row(p.footer), rule("┗", "━", "┛"))

	// A frame costs a line on each side, and the gap inside it one layer.
	outerWidth, outerHeight := width+2+2*layerColumns, height+2+2*layerRows
	marginWidth, marginHeight := outerWidth+2*layerColumns, outerHeight+2*layerRows
	left, top := 2, 3 // where the body starts in lines
	outer := outerWidth <= m.width && outerHeight <= area
	if outer {
		gap, blank := strings.Repeat(" ", layerColumns), strings.Repeat(" ", width+2*layerColumns)
		edge := m.st.Muted("┃")
		framed := []string{m.st.Muted("┏" + strings.Repeat("━", width+2*layerColumns) + "┓"), edge + blank + edge}
		for _, l := range lines {
			framed = append(framed, edge+gap+l+gap+edge)
		}
		lines = append(framed, edge+blank+edge, m.st.Muted("┗"+strings.Repeat("━", width+2*layerColumns)+"┛"))
		width, height = outerWidth, outerHeight
		left, top = left+1+layerColumns, top+1+layerRows
	}
	if outer && marginWidth <= m.width && marginHeight <= area {
		gap, blank := strings.Repeat(" ", layerColumns), strings.Repeat(" ", marginWidth)
		spaced := []string{blank}
		for _, l := range lines {
			spaced = append(spaced, gap+l+gap)
		}
		lines = append(spaced, blank)
		width, height = marginWidth, marginHeight
		left, top = left+layerColumns, top+layerRows
	}
	x, y := (m.width-width)/2, (area-height)/2
	uv.NewStyledString(strings.Join(lines, "\n")).Draw(buf, uv.Rect(x, y, width, height))
	return uv.Rect(x+left, y+top, width-2*left, body)
}

// Most of the window [Open Project]'s popup takes for its body, so that it
// keeps its size from one of its screens to the next, and the least it asks
// for on a small window.
const (
	screenPopupColumns, screenPopupRows       = 60, 16
	screenPopupMinColumns, screenPopupMinRows = 30, 8
)

// drawScreenPopup draws s in a popup whose size depends on the window only.
func (m *Model) drawScreenPopup(buf uv.Screen, s screen) {
	columns := max(min(screenPopupColumns, m.width-popupColumns), screenPopupMinColumns)
	rows := max(min(screenPopupRows, m.height-1-popupRows), screenPopupMinRows)
	body := make([]string, rows)
	body[0] = strings.Repeat(" ", columns)
	footer := ""
	if h, ok := s.(hinter); ok {
		footer = h.hint()
	}
	if area := m.drawPopup(buf, popup{title: s.title(), body: body, footer: footer}); !area.Empty() {
		s.draw(buf, area, m.st)
	}
}

// defaultHint is what the status line says of the keys, when a screen has
// nothing else to say.
const defaultHint = "Alt+arrows move  C-] zoom  C-\\ terminal  C-q menu"

// statusLine says what has the focus on the left and what the keys do on
// the right, in a bar.
func (m *Model) statusLine() string {
	l := m.layout
	left := " "
	if m.shown != "" {
		left += m.st.BarName(m.shown) + " · "
	}
	left += m.st.BarName(l.focus.String())
	if t, ok := m.selectedTerminal(); ok && l.focus == terminal {
		left += " · " + t.Name
	}
	if a, ok := m.activeAgent(); ok && l.focus == agent {
		left += " · " + a.Name
	}
	if l.zoomed != none {
		left += " · zoomed"
	}
	state := style.StatusLive
	right := m.st.BarMuted(defaultHint)
	if h, ok := m.root.(hinter); ok && l.focus == project && m.projects.loaded {
		right = m.st.BarMuted(h.hint())
	}
	if s, ok := m.paneStatus(); ok {
		state = style.StatusScrolled
		if m.attachedIn(l.focus).pane.FetchErr() != nil {
			state = style.StatusFailed
		}
		right = s
	}
	if m.statusNotice != "" {
		right = m.statusNotice
	}
	switch {
	case m.menu:
		state = style.StatusPrefix
		right = m.menuHint()
	case m.confirm != nil:
		state = style.StatusPrefix
		right = "confirm: y remove  n or Esc keep"
	case m.switcher != nil:
		state = style.StatusPrefix
		right = "switcher: type to find a project"
	case len(m.opening) > 0:
		state = style.StatusPrefix
		right = "open project: Esc goes back"
	}
	right += " "
	line := left + " " + right
	if gap := m.width - ansi.StringWidth(left) - ansi.StringWidth(right); gap >= 1 {
		line = left + strings.Repeat(" ", gap) + right
	}
	return m.st.Bar(ansi.Truncate(line, m.width, ""), state)
}
