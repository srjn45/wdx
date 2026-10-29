package tide

import uv "github.com/charmbracelet/ultraviolet"

// pane is one of tide's three panes; none is no pane, as when nothing is
// zoomed.
type pane int

const (
	none pane = iota
	project
	agent
	terminal
)

// panes lists the panes in the order clicks are matched to them.
var panes = []pane{project, agent, terminal}

func (p pane) String() string {
	switch p {
	case project:
		return "project"
	case agent:
		return "agent"
	case terminal:
		return "terminal"
	}
	return "none"
}

// direction is where Alt and an arrow move the focus.
type direction int

const (
	up direction = iota
	down
	left
	right
)

// layout is what the panes' places and the focus follow from, together
// with the window's size (docs/TIDE.md).
type layout struct {
	terminalOpen bool // docked, rather than hidden
	zoomed       pane // the pane that takes the whole window, or none
	focus        pane // the pane that gets the keys
	lastTop      pane // the top pane focused last, which ↑ from the terminal returns to
}

func newLayout() layout {
	return layout{terminalOpen: true, focus: agent, lastTop: agent}
}

// shown reports whether p is on the screen.
func (l layout) shown(p pane) bool {
	if l.zoomed != none {
		return p == l.zoomed
	}
	return p == project || p == agent || p == terminal && l.terminalOpen
}

// setFocus focuses p, which must be shown.
func (l *layout) setFocus(p pane) {
	l.focus = p
	if p != terminal {
		l.lastTop = p
	}
}

// toggleZoom zooms the focused pane, or leaves the zoom.
func (l *layout) toggleZoom() {
	if l.zoomed != none {
		l.zoomed = none
		return
	}
	l.zoomed = l.focus
}

// toggleTerminal shows the terminal pane, focused, when it is hidden or
// covered by a zoom, and hides it otherwise.
func (l *layout) toggleTerminal() {
	if !l.shown(terminal) {
		l.terminalOpen, l.zoomed = true, none
		l.setFocus(terminal)
		return
	}
	l.terminalOpen = false
	if l.zoomed == terminal {
		l.zoomed = none
	}
	if l.focus == terminal {
		l.focus = l.lastTop
	}
}

// move moves the focus to the neighbour in direction d, the zoom going with
// it; false when there is no neighbour there.
func (l *layout) move(d direction) bool {
	to := l.neighbour(d)
	if to == none {
		return false
	}
	l.setFocus(to)
	if l.zoomed != none {
		l.zoomed = to
	}
	return true
}

func (l layout) neighbour(d direction) pane {
	switch {
	case l.focus == terminal && d == up:
		return l.lastTop
	case l.focus == terminal:
		return none
	case d == down && l.terminalOpen:
		return terminal
	case l.focus == project && d == right:
		return agent
	case l.focus == agent && d == left:
		return project
	}
	return none
}

// minFrame is the smallest side of a frame: its two borders and a cell.
const minFrame = 3

// frames returns the frame of each shown pane in an area of width × height
// cells. Neighbours share the border line between them. ok is false when
// the area is too small for every frame to hold a cell.
func (l layout) frames(width, height int) (frames map[pane]uv.Rectangle, ok bool) {
	frames = map[pane]uv.Rectangle{}
	if l.zoomed != none {
		frames[l.zoomed] = uv.Rect(0, 0, width, height)
	} else {
		top := height
		if l.terminalOpen {
			rows := max(height/4, minFrame)
			top = height - rows + 1
			frames[terminal] = uv.Rect(0, top-1, width, rows)
		}
		split := width * 2 / 5
		frames[project] = uv.Rect(0, 0, split+1, top)
		frames[agent] = uv.Rect(split, 0, width-split, top)
	}
	for _, f := range frames {
		// The terminal's minimum rows can push a frame off the top.
		if f.Min.X < 0 || f.Min.Y < 0 || f.Max.X > width || f.Max.Y > height || f.Dx() < minFrame || f.Dy() < minFrame {
			return nil, false
		}
	}
	return frames, true
}
