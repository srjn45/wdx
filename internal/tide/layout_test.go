package tide

import (
	"math/rand/v2"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestFrames(t *testing.T) {
	t.Parallel()
	docked := newLayout()
	hidden := layout{focus: agent, lastTop: agent}
	zoomed := layout{terminalOpen: true, zoomed: terminal, focus: terminal, lastTop: agent}
	for _, tc := range []struct {
		name          string
		layout        layout
		width, height int
		want          map[pane]uv.Rectangle
	}{
		{"docked", docked, 100, 40, map[pane]uv.Rectangle{
			project: uv.Rect(0, 0, 41, 31), agent: uv.Rect(40, 0, 60, 31), terminal: uv.Rect(0, 30, 100, 10),
		}},
		{"docked, odd size", docked, 81, 23, map[pane]uv.Rectangle{
			project: uv.Rect(0, 0, 33, 19), agent: uv.Rect(32, 0, 49, 19), terminal: uv.Rect(0, 18, 81, 5),
		}},
		{"the terminal's minimum rows", docked, 20, 8, map[pane]uv.Rectangle{
			project: uv.Rect(0, 0, 9, 6), agent: uv.Rect(8, 0, 12, 6), terminal: uv.Rect(0, 5, 20, 3),
		}},
		{"smallest docked", docked, 5, 5, map[pane]uv.Rectangle{
			project: uv.Rect(0, 0, 3, 3), agent: uv.Rect(2, 0, 3, 3), terminal: uv.Rect(0, 2, 5, 3),
		}},
		{"hidden", hidden, 100, 40, map[pane]uv.Rectangle{
			project: uv.Rect(0, 0, 41, 40), agent: uv.Rect(40, 0, 60, 40),
		}},
		{"zoomed", zoomed, 100, 40, map[pane]uv.Rectangle{terminal: uv.Rect(0, 0, 100, 40)}},
		{"smallest zoomed", zoomed, 3, 3, map[pane]uv.Rectangle{terminal: uv.Rect(0, 0, 3, 3)}},
		{"too narrow", docked, 4, 40, nil},
		{"too short", docked, 100, 4, nil},
		{"hidden, short", hidden, 100, 3, map[pane]uv.Rectangle{project: uv.Rect(0, 0, 41, 3), agent: uv.Rect(40, 0, 60, 3)}},
		{"too small to zoom", zoomed, 2, 40, nil},
		{"empty", docked, 0, 0, nil},
	} {
		got, ok := tc.layout.frames(tc.width, tc.height)
		if ok != (tc.want != nil) || len(got) != len(tc.want) {
			t.Errorf("%s: got %v, ok %v; want %v", tc.name, got, ok, tc.want)
			continue
		}
		for p, f := range tc.want {
			if got[p] != f {
				t.Errorf("%s: %s is %v, want %v", tc.name, p, got[p], f)
			}
		}
	}
}

// action is something a key or click does to the layout.
type action struct {
	name string
	do   func(*layout)
}

var (
	zoom       = action{"zoom", (*layout).toggleZoom}
	toggleTerm = action{"terminal", (*layout).toggleTerminal}
	moveUp     = action{"↑", func(l *layout) { l.move(up) }}
	moveDown   = action{"↓", func(l *layout) { l.move(down) }}
	moveLeft   = action{"←", func(l *layout) { l.move(left) }}
	moveRight  = action{"→", func(l *layout) { l.move(right) }}
)

func TestActions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		actions []action
		want    layout
	}{
		{"start", nil, layout{terminalOpen: true, focus: agent, lastTop: agent}},
		{"left", []action{moveLeft}, layout{terminalOpen: true, focus: project, lastTop: project}},
		{"left at the edge", []action{moveLeft, moveLeft, moveUp}, layout{terminalOpen: true, focus: project, lastTop: project}},
		{"down and back up", []action{moveLeft, moveDown, moveUp}, layout{terminalOpen: true, focus: project, lastTop: project}},
		{"down then sideways stays", []action{moveDown, moveLeft, moveRight, moveDown}, layout{terminalOpen: true, focus: terminal, lastTop: agent}},
		{"zoom and back", []action{zoom, zoom}, layout{terminalOpen: true, focus: agent, lastTop: agent}},
		{"zoom follows focus", []action{zoom, moveLeft}, layout{terminalOpen: true, zoomed: project, focus: project, lastTop: project}},
		{"zoom follows focus down", []action{zoom, moveDown}, layout{terminalOpen: true, zoomed: terminal, focus: terminal, lastTop: agent}},
		{"un-zoom the terminal docks it", []action{moveDown, zoom, zoom}, layout{terminalOpen: true, focus: terminal, lastTop: agent}},
		{"hide the focused terminal", []action{moveLeft, moveDown, toggleTerm}, layout{focus: project, lastTop: project}},
		{"hide leaves top focus", []action{toggleTerm}, layout{focus: agent, lastTop: agent}},
		{"no terminal below when hidden", []action{toggleTerm, moveDown}, layout{focus: agent, lastTop: agent}},
		{"open focuses it", []action{toggleTerm, toggleTerm}, layout{terminalOpen: true, focus: terminal, lastTop: agent}},
		{"open from a top zoom", []action{zoom, toggleTerm}, layout{terminalOpen: true, focus: terminal, lastTop: agent}},
		{"hide the zoomed terminal", []action{moveDown, zoom, toggleTerm}, layout{focus: agent, lastTop: agent}},
		{"zoom with the terminal hidden", []action{toggleTerm, zoom, toggleTerm}, layout{terminalOpen: true, focus: terminal, lastTop: agent}},
	} {
		l := newLayout()
		for _, a := range tc.actions {
			a.do(&l)
		}
		if l != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, l, tc.want)
		}
	}
}

// TestLayoutInvariants runs random actions at random sizes and checks,
// after each, what must always hold.
func TestLayoutInvariants(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(1, 2))
	actions := []action{zoom, toggleTerm, moveUp, moveDown, moveLeft, moveRight,
		{"click", func(l *layout) {
			if p := panes[rng.IntN(len(panes))]; l.shown(p) {
				l.setFocus(p)
			}
		}},
	}
	for run := range 100 {
		l := newLayout()
		var done []string
		for range 100 {
			a := actions[rng.IntN(len(actions))]
			a.do(&l)
			done = append(done, a.name)
			if err := checkLayout(l, rng.IntN(130), rng.IntN(50)); err != "" {
				t.Fatalf("run %d after %v: %s: %+v", run, done, err, l)
			}
			again := l
			again.toggleZoom()
			again.toggleZoom()
			if again != l {
				t.Fatalf("run %d after %v: zooming twice changed %+v to %+v", run, done, l, again)
			}
		}
	}
}

func checkLayout(l layout, width, height int) string {
	switch {
	case !l.shown(l.focus):
		return "the focus is on a pane not shown"
	case l.zoomed != none && l.zoomed != l.focus:
		return "the zoomed pane does not have the focus"
	case l.zoomed == terminal && !l.terminalOpen:
		return "the zoomed terminal is hidden"
	case l.lastTop != project && l.lastTop != agent:
		return "the last top pane is not a top pane"
	}
	frames, ok := l.frames(width, height)
	// Side by side, the top panes need 5 columns; over the terminal, 5 rows.
	leastWidth, leastHeight := 5, minFrame
	if l.terminalOpen {
		leastHeight = 5
	}
	if l.zoomed != none {
		leastWidth, leastHeight = minFrame, minFrame
	}
	if ok != (width >= leastWidth && height >= leastHeight) {
		return "frames at the wrong sizes"
	}
	if !ok {
		return ""
	}
	for _, p := range panes {
		if _, has := frames[p]; has != l.shown(p) {
			return p.String() + " has a frame iff it is shown: not so"
		}
	}
	for y := range height {
		for x := range width {
			at := uv.Pos(x, y)
			framed, inside := 0, 0
			for _, f := range frames {
				if at.In(f) {
					framed++
				}
				if at.In(f.Inset(1)) {
					inside++
				}
			}
			switch {
			case framed == 0:
				return "a cell outside every frame"
			case inside > 1 || inside == 1 && framed > 1:
				return "frames overlap inside their borders"
			}
		}
	}
	for _, f := range frames {
		if !f.In(uv.Rect(0, 0, width, height)) || f.Dx() < minFrame || f.Dy() < minFrame {
			return "a frame out of the area or too small"
		}
	}
	return ""
}
