// Package tide is the terminal IDE, wdx tide: a project pane, an agent pane
// and a terminal pane in one window, which the keys and the mouse move the
// focus between, zoom and hide (docs/TIDE.md). It is a client of the
// daemon, as the cli is. For now its agent and terminal panes hold
// placeholders.
package tide

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/style"
)

// tide's own keys, as Bubble Tea names them; every other key goes to the
// focused pane.
const (
	zoomKey     = "ctrl+]"
	terminalKey = `ctrl+\`
	menuKey     = "ctrl+q"
)

// focusKeys are Alt and an arrow, whichever way the terminal sends them.
var focusKeys = map[string]direction{"alt+up": up, "alt+down": down, "alt+left": left, "alt+right": right}

// doubleClick is the longest time between the clicks of a double-click.
const doubleClick = 400 * time.Millisecond

// Model is tide's window.
type Model struct {
	ctx    context.Context // every request's, carrying the run's logger and debug id
	log    *slog.Logger
	st     style.Style
	client *client.Client
	now    func() time.Time

	workDir, userHome string // from Options

	width, height int
	layout        layout
	projects      *projectsPane
	content       map[pane]content // the project and agent panes'
	tabs          []*placeholder   // the terminal pane's
	tab           int              // the tab shown
	menu          bool             // the menu is open
	switcher      bool             // the project switcher is open
	lastClick     click
}

// content is what a pane shows.
type content interface {
	input(s string)
	draw(buf uv.Screen, area uv.Rectangle, st style.Style)
}

// Options are what tide needs from the cli.
type Options struct {
	Client   *client.Client
	Style    style.Style
	WorkDir  string // the directory tide was started in, where the directory browser starts
	UserHome string // shown as ~ in paths
}

// click is where and when a click on a frame's title happened.
type click struct {
	pane pane
	at   time.Time
}

// New returns tide's window. Its requests to the daemon run with ctx,
// whose logger it logs to.
func New(ctx context.Context, opts Options) *Model {
	projects := &projectsPane{}
	m := &Model{
		ctx:      ctx,
		log:      logging.FromContext(ctx),
		st:       opts.Style,
		client:   opts.Client,
		workDir:  opts.WorkDir,
		userHome: opts.UserHome,
		now:      time.Now,
		layout:   newLayout(),
		projects: projects,
		content:  map[pane]content{project: projects, agent: &placeholder{name: "agent"}},
		tabs:     []*placeholder{{name: "shell"}, {name: "tests"}, {name: "server"}},
	}
	// tide starts on the project pane: the switcher or [Open Project].
	m.layout.setFocus(project)
	return m
}

func (m *Model) Init() tea.Cmd { return m.listProjects() }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case projectsListed:
		m.listed(msg)
	case tea.KeyPressMsg:
		return m, m.key(msg)
	case tea.MouseClickMsg:
		m.click(msg.Mouse())
	case tea.MouseWheelMsg:
		m.wheel(msg.Mouse())
	case tea.PasteMsg:
		m.pane(m.layout.focus).input(fmt.Sprintf("paste(%d)", len(msg.Content)))
	}
	return m, nil
}

// pane returns what p shows: for the terminal pane, its tab shown.
func (m *Model) pane(p pane) content {
	if p == terminal {
		return m.tabs[m.tab]
	}
	return m.content[p]
}

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	name := msg.String()
	if m.menu {
		m.menu = false
		if name == "q" {
			m.log.Info("tide quit")
			return tea.Quit
		}
		return nil
	}
	if m.switcher && name == "esc" {
		m.switcher = false
		m.log.Debug("switcher closed")
		return nil
	}
	switch name {
	case zoomKey:
		m.layout.toggleZoom()
		m.changed("zoom")
	case terminalKey:
		m.layout.toggleTerminal()
		m.changed("toggle terminal")
	case menuKey:
		m.menu = true
	default:
		d, ok := focusKeys[name]
		switch {
		case !ok && name == retryKey && m.layout.focus == project && m.projects.err != nil && !m.projects.listing:
			m.log.Info("projects listed again", "reason", "retry key")
			return m.listProjects()
		case !ok:
			m.pane(m.layout.focus).input(name)
		case m.layout.focus == terminal && (d == left || d == right):
			m.switchTab(d)
		case m.layout.move(d):
			m.changed("move")
		}
	}
	return nil
}

// switchTab shows the terminal pane's tab to the left or right of the one
// shown, going round at the ends.
func (m *Model) switchTab(d direction) {
	step := 1
	if d == left {
		step = -1
	}
	m.tab = (m.tab + step + len(m.tabs)) % len(m.tabs)
	m.log.Debug("tab switched", "tab", m.tabs[m.tab].name)
}

// click focuses the pane clicked; on a frame's title it may also zoom or
// switch tabs.
func (m *Model) click(mouse tea.Mouse) {
	if m.menu || mouse.Button != tea.MouseLeft {
		return
	}
	frames, ok := m.frames()
	if !ok {
		return
	}
	at := uv.Pos(mouse.X, mouse.Y)
	// A title is on a border the frame above shares: the title wins.
	for _, p := range panes {
		if f, shown := frames[p]; shown && at.In(f) && at.Y == f.Min.Y {
			m.layout.setFocus(p)
			m.clickTitle(p, f, at)
			return
		}
	}
	for _, p := range panes {
		f, shown := frames[p]
		if !shown || !at.In(f) {
			continue
		}
		m.layout.setFocus(p)
		if at.In(f.Inset(1)) {
			m.pane(p).input(fmt.Sprintf("click %d,%d", at.X-f.Min.X-1, at.Y-f.Min.Y-1))
		}
		m.changed("click")
		return
	}
}

// clickTitle handles a click on p's top border: its zoom button, a tab,
// or a second click soon after the first zooms.
func (m *Model) clickTitle(p pane, f uv.Rectangle, at uv.Position) {
	now := m.now()
	double := m.lastClick.pane == p && now.Sub(m.lastClick.at) <= doubleClick
	m.lastClick = click{pane: p, at: now}
	for _, l := range m.labels(p, f) {
		if !at.In(l.area) {
			continue
		}
		switch {
		case l.zoom:
			m.lastClick = click{}
			m.layout.toggleZoom()
			m.changed("zoom button")
			return
		case l.tab >= 0:
			m.tab = l.tab
		}
	}
	if double {
		m.lastClick = click{}
		m.layout.toggleZoom()
		m.changed("double-click")
		return
	}
	m.changed("click")
}

// wheel goes to the pane under the pointer, whichever has the focus.
func (m *Model) wheel(mouse tea.Mouse) {
	frames, ok := m.frames()
	if !ok {
		return
	}
	at := uv.Pos(mouse.X, mouse.Y)
	for _, p := range panes {
		if f, shown := frames[p]; shown && at.In(f.Inset(1)) {
			m.pane(p).input(mouse.Button.String())
			return
		}
	}
}

// frames returns the panes' frames in the window above the status line.
func (m *Model) frames() (map[pane]uv.Rectangle, bool) {
	return m.layout.frames(m.width, m.height-1)
}

// changed logs the layout after action changed it.
func (m *Model) changed(action string) {
	l := m.layout
	m.log.Debug("layout changed", "action", action, "focus", l.focus.String(),
		"zoomed", l.zoomed.String(), "terminal_open", l.terminalOpen)
}
