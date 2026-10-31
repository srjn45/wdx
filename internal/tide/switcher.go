package tide

import (
	"slices"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/style"
)

// openCreateLabel is the switcher's way to [Open Project].
const openCreateLabel = "Open/Create Project"

// switcherBox is the project switcher, C-q p: the open projects in a popup,
// found by typing part of their names, and the way to [Open Project]
// (docs/TIDE.md).
type switcherBox struct {
	projects *projectList
	filter   string
	selected string // the name of the project selected; "" selects openCreateLabel
}

// switchProject asks tide to show the open project name.
type switchProject struct{ name string }

// closeSwitcher closes the switcher.
type closeSwitcher struct{}

// openCreate closes the switcher and opens [Open Project].
type openCreate struct{}

// newSwitcher returns the switcher with first selected, or the first
// project when first is not open.
func newSwitcher(projects *projectList, first string) *switcherBox {
	s := &switcherBox{projects: projects, selected: first}
	s.selectMatch()
	return s
}

// matches returns the open projects whose names match the filter, by name.
func (s *switcherBox) matches() []string {
	var matches []string
	for _, name := range s.projects.open() {
		if fuzzy(s.filter, name) {
			matches = append(matches, name)
		}
	}
	return matches
}

// selectMatch selects the first match, or openCreateLabel when none is left,
// unless the project selected is one.
func (s *switcherBox) selectMatch() {
	matches := s.matches()
	if slices.Contains(matches, s.selected) {
		return
	}
	s.selected = ""
	if len(matches) > 0 {
		s.selected = matches[0]
	}
}

// fuzzy reports whether the letters of filter appear in name in order,
// ignoring case.
func fuzzy(filter, name string) bool {
	rest := []rune(strings.ToLower(name))
	for _, r := range strings.ToLower(filter) {
		i := 0
		for i < len(rest) && rest[i] != r {
			i++
		}
		if i == len(rest) {
			return false
		}
		rest = rest[i+1:]
	}
	return true
}

// selection returns the matches and the index of the one selected, -1 for
// openCreateLabel. A project selected that is not among them any more, the
// projects listed since, gives way to the first.
func (s *switcherBox) selection() ([]string, int) {
	if s.selected != "" {
		s.selectMatch()
	}
	matches := s.matches()
	return matches, slices.Index(matches, s.selected)
}

func (s *switcherBox) update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	matches, i := s.selection()
	switch name := key.String(); {
	case name == "esc":
		return send(closeSwitcher{})
	case name == "up" && i == 0:
		s.selected = ""
	case name == "up" && i > 0:
		s.selected = matches[i-1]
	case name == "down" && i < len(matches)-1:
		s.selected = matches[i+1]
	case name == "enter" && i < 0:
		return send(openCreate{})
	case name == "enter":
		return send(switchProject{name: matches[i]})
	case name == "backspace" && s.filter != "":
		r := []rune(s.filter)
		s.filter = string(r[:len(r)-1])
		s.selectMatch()
	case typed(key):
		s.filter += key.Text
		s.selectMatch()
	}
	return nil
}

// typed reports whether msg types text, rather than being a key that does
// something.
func typed(msg tea.KeyPressMsg) bool {
	if msg.Text == "" || msg.Mod&^tea.ModShift != 0 {
		return false
	}
	for _, r := range msg.Text {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// switcherRows is the most projects the switcher lists at once.
const switcherRows = 10

// popup returns what the switcher shows, the selection kept in view.
func (s *switcherBox) popup(m *Model) popup {
	st := m.st
	width := 30
	for _, name := range s.projects.open() {
		width = max(width, ansi.StringWidth(name)+2)
	}
	width = min(width, max(m.width-8, 10))
	mark := func(text string, selected bool) string {
		text = ansi.Truncate(text, width-2, "…")
		if selected {
			return st.Focused("▸ ") + st.Name(text)
		}
		return "  " + text
	}
	filter := "> " + s.filter + "_"
	matches, i := s.selection()
	body := []string{filter + strings.Repeat(" ", max(width-ansi.StringWidth(filter), 0)), "", mark(openCreateLabel, i < 0), ""}
	if len(matches) == 0 && s.filter != "" {
		body = append(body, st.Muted("no project matches"))
	}
	// Room for the whole popup when the window has it, or at least the frame.
	area := m.height - 1 - len(body)
	rows := max(min(switcherRows, area-popupRows), min(3, area-6), 1)
	from := max(min(i-rows/2, len(matches)-rows), 0)
	for j := from; j < min(from+rows, len(matches)); j++ {
		body = append(body, mark(matches[j], j == i))
	}
	return popup{
		title:  "Switch Project",
		body:   body,
		footer: st.Command("↑↓") + " select  " + st.Command("enter") + " choose  " + st.Command("Esc") + " close",
	}
}

// defaultStripLinger is how long the project strip stays after the last switch.
const defaultStripLinger = time.Second

// hideStrip hides the project strip, unless a switch since showed it again.
type hideStrip struct{ shown int }

// switchKeys are Alt+Shift and an arrow: the previous or next open project.
var switchKeys = map[string]int{"alt+shift+left": -1, "alt+shift+right": 1}

// step shows the open project by steps from the one shown, going round,
// and shows the strip for a while.
func (m *Model) step(by int) tea.Cmd {
	open := m.projects.open()
	if len(open) == 0 {
		return nil
	}
	i := -1
	for j, name := range open {
		if name == m.shown {
			i = j
		}
	}
	switch {
	case i < 0 && by > 0:
		i = 0
	case i < 0:
		i = len(open) - 1
	default:
		i = (i + by + len(open)) % len(open)
	}
	m.strip++
	m.stripShown = true
	shown := m.strip
	hide := m.tick(m.stripLinger, func(time.Time) tea.Msg { return hideStrip{shown: shown} })
	if open[i] == m.shown {
		return hide
	}
	return tea.Batch(m.show(open[i]), hide)
}

// drawStrip draws the open projects in a row just above the status line,
// the one shown marked and kept in view.
func (m *Model) drawStrip(buf uv.Screen) {
	if m.height < 2 {
		return
	}
	var items []string
	current := -1
	for _, name := range m.projects.open() {
		if name == m.shown {
			current = len(items)
			items = append(items, m.st.BarName("▸ "+name+" ◂"))
		} else {
			items = append(items, "  "+name+"  ")
		}
	}
	// Scrolled sideways: the items before the one shown go while it would
	// not fit.
	width := func(items []string) int { return ansi.StringWidth(" " + strings.Join(items, " ")) }
	for current > 0 && width(items[:current+1]) > m.width {
		items, current = items[1:], current-1
	}
	row := uv.Rect(0, m.height-2, m.width, 1)
	line := " " + strings.Join(items, " ")
	line += strings.Repeat(" ", max(m.width-ansi.StringWidth(line), 0))
	uv.NewStyledString(m.st.Bar(ansi.Truncate(line, m.width, ""), style.StatusLive)).Draw(buf, row)
}
