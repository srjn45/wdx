package tide

import (
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// switcherBox is the project switcher, C-q p: every project in a box over
// the window, found by typing part of its name (docs/TIDE.md).
type switcherBox struct {
	projects *projectList
	filter   string
	selected string // the name of the project selected
	opening  string // the closed project being reopened, while it is
	failure  string // why the last reopen failed
}

// switchProject asks tide to show the project name, reopening it first
// when closed.
type switchProject struct {
	name   string
	closed bool
}

// closeSwitcher closes the switcher.
type closeSwitcher struct{}

// newSwitcher returns the switcher with first selected, or the first
// project when first is "".
func newSwitcher(projects *projectList, first string) *switcherBox {
	s := &switcherBox{projects: projects, selected: first}
	s.keepSelection()
	return s
}

// matches returns the projects whose names match the filter, the open ones
// first, then the closed ones, each by name.
func (s *switcherBox) matches() []api.ProjectSummary {
	var open, closed []api.ProjectSummary
	for _, p := range s.projects.projects {
		switch {
		case !fuzzy(s.filter, p.Name):
		case p.State == api.ProjectStateOpen:
			open = append(open, p)
		default:
			closed = append(closed, p)
		}
	}
	return append(open, closed...)
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

// keepSelection selects the first match when the project selected is not
// among them, as after the filter changed or the projects were listed.
func (s *switcherBox) keepSelection() int {
	matches := s.matches()
	for i, p := range matches {
		if p.Name == s.selected {
			return i
		}
	}
	s.selected = ""
	if len(matches) > 0 {
		s.selected = matches[0].Name
	}
	return 0
}

func (s *switcherBox) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case projectOpened: // only a failure reaches the switcher
		s.opening, s.failure = "", msg.err.Error()
	case tea.KeyPressMsg:
		if s.opening != "" {
			return nil
		}
		s.failure = ""
		i, matches := s.keepSelection(), s.matches()
		switch key := msg.String(); {
		case key == "esc":
			return send(closeSwitcher{})
		case key == "up" && i > 0:
			s.selected = matches[i-1].Name
		case key == "down" && i < len(matches)-1:
			s.selected = matches[i+1].Name
		case key == "enter" && len(matches) > 0:
			p := matches[i]
			if p.State != api.ProjectStateOpen {
				s.opening = p.Name
			}
			return send(switchProject{name: p.Name, closed: p.State != api.ProjectStateOpen})
		case key == "backspace" && s.filter != "":
			r := []rune(s.filter)
			s.filter = string(r[:len(r)-1])
			s.keepSelection()
		case typed(msg):
			s.filter += msg.Text
			s.keepSelection()
		}
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
	for _, p := range s.projects.projects {
		width = max(width, ansi.StringWidth(p.Name)+12)
	}
	width = min(width, max(m.width-8, 10))
	filter := "> " + s.filter + "_"
	body := []string{filter + strings.Repeat(" ", max(width-ansi.StringWidth(filter), 0)), ""}
	i, matches := s.keepSelection(), s.matches()
	if len(matches) == 0 {
		body = append(body, st.Muted("no project matches"))
	}
	// Room for the frame, the filter and a message under the list.
	rows := max(min(switcherRows, m.height-1-6-2-3), 1)
	from := max(min(i-rows/2, len(matches)-rows), 0)
	for j := from; j < min(from+rows, len(matches)); j++ {
		p := matches[j]
		name := ansi.Truncate(p.Name, width-10, "…")
		pad := strings.Repeat(" ", max(width-8-ansi.StringWidth(name), 1))
		state := st.Muted(string(p.State))
		if j == i {
			body = append(body, st.Focused("▸ ")+st.Name(name)+pad+state)
		} else {
			body = append(body, "  "+name+pad+state)
		}
	}
	var message []string
	switch {
	case s.opening != "":
		message = strings.Split(st.Muted(wrap("opening "+s.opening+"…", width)), "\n")
	case s.failure != "":
		message = strings.Split(wrap(s.failure, width), "\n")
		for k, l := range message {
			message[k] = st.Failure(l)
		}
	}
	if len(message) > 0 {
		body = append(append(body, ""), message...)
	}
	return popup{
		title:  "Switch project",
		body:   body,
		footer: st.Command("↑↓") + " select  " + st.Command("enter") + " switch  " + st.Command("Esc") + " close",
	}
}

// stripLinger is how long the project strip stays after the last switch.
const stripLinger = time.Second

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
	hide := m.tick(stripLinger, func(time.Time) tea.Msg { return hideStrip{shown: shown} })
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
