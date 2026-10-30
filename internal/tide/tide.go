// Package tide is the terminal IDE, wdx tide: a project pane, an agent pane
// and a terminal pane in one window, which the keys and the mouse move the
// focus between, zoom and hide (docs/TIDE.md). It is a client of the
// daemon, as the cli is. For now its agent and terminal panes hold
// placeholders.
package tide

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
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
	tick   func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd // tea.Tick, which tests replace

	workDir, userHome, projectsDir string // from Options

	width, height  int
	layout         layout
	projects       *projectList
	stack          []screen                                                             // the project pane's, the root first
	shown          string                                                               // the project shown, or "" before one is
	previous       string                                                               // the project shown before it, which the switcher selects first
	agent          *placeholder                                                         // the agent pane's
	tab            int                                                                  // the terminal pane's tab shown, an index into terminals()
	following      *following                                                           // the stream of the project shown
	streams        int                                                                  // how many streams were opened, numbering them
	openStream     func(ctx context.Context, ref string) (*client.ProjectStream, error) // nil in tests that follow nothing
	selectedTab    map[string]string                                                    // the terminal id of the tab selected, by project id, for the run
	inputs         map[string]*placeholder                                              // what each terminal got, by id, until termview shows them
	confirm        *confirmBox                                                          // the confirmation of C-q x, while it is open
	terminalNotice string                                                               // why the last terminal request failed, until the next key
	statusNotice   string                                                               // what the status line says in place of the keys, until the next key or click
	menu           bool                                                                 // the menu is open
	switcher       *switcherBox                                                         // the project switcher, while it is open
	strip          int                                                                  // how many times the project strip was shown
	stripShown     bool                                                                 // the project strip is shown
	lastClick      click
	sel            *selection                // text selected with the mouse, while dragged and until the next click or key
	copy           func(text string) tea.Cmd // puts text on the clipboard; tests replace it
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
	// ProjectsDir is where projects are created by default, where the
	// directory browser starts to create one.
	ProjectsDir string
}

// click is where and when a click on a frame's title happened.
type click struct {
	pane pane
	at   time.Time
}

// New returns tide's window. Its requests to the daemon run with ctx,
// whose logger it logs to.
func New(ctx context.Context, opts Options) *Model {
	projects := &projectList{}
	m := &Model{
		ctx:         ctx,
		log:         logging.FromContext(ctx),
		st:          opts.Style,
		client:      opts.Client,
		workDir:     opts.WorkDir,
		userHome:    opts.UserHome,
		projectsDir: opts.ProjectsDir,
		now:         time.Now,
		tick:        tea.Tick,
		layout:      newLayout(),
		projects:    projects,
		stack:       []screen{&openProjectScreen{projects: projects}},
		agent:       &placeholder{name: "agent"},
		openStream:  opts.Client.ProjectEvents,
		selectedTab: map[string]string{},
		inputs:      map[string]*placeholder{},
	}
	// tide starts on the project pane, the switcher or [Open Project], and
	// shows the terminals of a project only once one is shown.
	m.copy = m.copyToClipboard
	m.layout.setFocus(project)
	m.layout.terminalOpen = false
	return m
}

func (m *Model) Init() tea.Cmd { return m.listProjects() }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.sel = nil // the panes move
	case projectsListed:
		m.listed(msg)
	case popScreen:
		if len(m.stack) > 1 {
			m.stack = m.stack[:len(m.stack)-1]
			m.log.Debug("screen popped", "screen", m.top().title())
		}
	case pushScreen:
		m.push(msg.s)
	case cancelOpen:
		m.toOpenProject("")
	case reopenProject:
		return m, m.reopen(msg.name)
	case createProject:
		return m, m.create(msg.req)
	case recloneProject:
		return m, m.reclone(msg.name)
	case removeProject:
		return m, m.remove(msg.name)
	case browse:
		return m, m.browse(msg.create)
	case dirRead:
		// The browser may be gone: Esc does not wait for a listing.
		if b, ok := m.top().(*dirBrowser); ok {
			return m, b.update(msg)
		}
	case projectOpened:
		return m, m.opened(msg)
	case switchProject:
		if msg.closed {
			return m, m.reopen(msg.name)
		}
		return m, m.show(msg.name)
	case closeSwitcher:
		m.switcher = nil
		m.log.Debug("switcher closed")
	case streamOpened:
		return m, m.streamOpened(msg)
	case projectEvent:
		return m, m.projectEvent(msg)
	case terminalCreated:
		m.terminalCreated(msg)
	case removed:
		m.removed(msg)
	case reopenStream:
		if _, ok := m.current(msg.gen); ok {
			return m, m.open()
		}
	case hideStrip:
		if msg.shown == m.strip {
			m.stripShown = false
		}
	case tea.KeyPressMsg:
		return m, m.key(msg)
	case copyMissed:
		m.statusNotice = copyMissedNotice
	case tea.MouseClickMsg:
		m.statusNotice = ""
		m.click(msg.Mouse())
	case tea.MouseMotionMsg:
		m.drag(msg.Mouse())
	case tea.MouseReleaseMsg:
		return m, m.release(msg.Mouse())
	case tea.MouseWheelMsg:
		m.wheel(msg.Mouse())
	case tea.PasteMsg:
		if f, ok := m.top().(*fieldScreen); ok && m.layout.focus == project && m.switcher == nil && !m.menu {
			f.paste(msg.Content)
			return m, nil
		}
		m.pane(m.layout.focus).input(fmt.Sprintf("paste(%d)", len(msg.Content)))
	}
	return m, nil
}

// pane returns what p shows: for the terminal pane, its tab shown.
func (m *Model) pane(p pane) content {
	switch p {
	case terminal:
		return terminalPane{m}
	case agent:
		return m.agent
	}
	return projectPane{m}
}

// top is the project pane's screen on top.
func (m *Model) top() screen { return m.stack[len(m.stack)-1] }

// show makes name the project shown: the root of the project pane's stack,
// which the screens above it leave for. The first one shown brings the
// terminal pane.
func (m *Model) show(name string) tea.Cmd {
	if m.shown == "" {
		m.layout.terminalOpen = true
	}
	if name != m.shown && m.shown != "" {
		m.previous = m.shown
	}
	var follow tea.Cmd
	if name != m.shown || m.following == nil {
		m.tab = 0
		follow = m.follow(name)
	}
	m.shown, m.switcher = name, nil
	m.stack = []screen{&projectScreen{name: name}}
	m.log.Info("project shown", "name", name)
	return tea.Batch(m.listProjects(), follow)
}

// push puts s on top of the project pane's stack.
func (m *Model) push(s screen) {
	m.stack = append(m.stack, s)
	m.log.Debug("screen pushed", "screen", s.title())
}

// browse pushes the directory browser: to open a local repository, from
// tide's working directory, or to create a project, from where projects
// are created when it exists.
func (m *Model) browse(create bool) tea.Cmd {
	dir := cmp.Or(m.workDir, m.userHome)
	if create {
		dir = cmp.Or(existing(m.projectsDir), dir)
	}
	b, cmd := newDirBrowser(dir, m.userHome, create)
	m.push(b)
	m.log.Debug("directory browsed", "dir", dir)
	return cmd
}

// existing returns dir, or its closest parent that exists; "" for "".
func existing(dir string) string {
	for d := dir; d != ""; d = filepath.Dir(d) {
		if exists(d) {
			return d
		}
		if d == filepath.Dir(d) {
			break
		}
	}
	return ""
}

// toOpenProject leaves the screens above [Open Project] for it, pushing it
// when it is not on the stack, with notice saying what happened.
func (m *Model) toOpenProject(notice string) {
	for i, s := range m.stack {
		if o, ok := s.(*openProjectScreen); ok {
			m.stack = m.stack[:i+1]
			o.notice, o.failure = notice, ""
			return
		}
	}
	m.push(&openProjectScreen{projects: m.projects, notice: notice})
}

// openProject opens [Open Project] over the project pane, focused, and
// lists the projects again for it.
func (m *Model) openProject() tea.Cmd {
	m.focusProjectPane()
	if _, open := m.top().(*openProjectScreen); !open {
		m.stack = append(m.stack, &openProjectScreen{projects: m.projects})
		m.log.Debug("screen pushed", "screen", m.top().title())
	}
	return m.listProjects()
}

// focusProjectPane focuses the project pane, the zoom going with it as it
// does with the focus keys.
func (m *Model) focusProjectPane() {
	m.layout.setFocus(project)
	if m.layout.zoomed != none {
		m.layout.zoomed = project
	}
	m.changed("project pane")
}

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	name := msg.String()
	m.sel = nil
	if m.menu {
		m.menu = false
		switch name {
		case "o":
			m.switcher = nil
			return m.openProject()
		case "p":
			m.switcher = newSwitcher(m.projects, m.previous)
			return m.listProjects()
		case "t":
			if _, ok := m.terminalDir(); ok {
				return m.createTerminal()
			}
		case "x":
			if r, ok := m.removable(); ok {
				m.confirm = &confirmBox{r: r}
				m.log.Info("removal asked", "kind", r.kind, "name", r.name)
			}
		case "q":
			m.log.Info("tide quit")
			m.unfollow() // the daemon ends the stream, and logs it
			return tea.Quit
		}
		return nil
	}
	m.terminalNotice, m.statusNotice = "", ""
	if m.confirm != nil {
		return m.confirmKey(name)
	}
	if m.switcher != nil && name != menuKey {
		return m.switcher.update(msg)
	}
	if by, ok := switchKeys[name]; ok {
		return m.step(by)
	}
	m.stripShown = false
	switch name {
	case zoomKey:
		m.layout.toggleZoom()
		m.changed("zoom")
	case terminalKey:
		if m.shown == "" {
			return nil // no project, no terminals
		}
		m.layout.toggleTerminal()
		m.changed("toggle terminal")
	case menuKey:
		m.menu = true
	default:
		d, ok := focusKeys[name]
		switch {
		case !ok && m.layout.focus == project:
			return m.projectPaneKey(msg)
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

// projectPaneKey gives a key to the screen on top of the project pane,
// once the projects are listed; until then r lists them again after a
// failure.
func (m *Model) projectPaneKey(msg tea.KeyPressMsg) tea.Cmd {
	l := m.projects
	switch {
	case l.loaded:
		return m.top().update(msg)
	case msg.String() == retryKey && l.err != nil && !l.listing:
		m.log.Info("projects listed again", "reason", "retry key")
		return m.listProjects()
	}
	return nil
}

// projectPane is the project pane as a content: its screens take keys, not
// inputs.
type projectPane struct{ m *Model }

func (projectPane) input(string) {}

func (p projectPane) draw(buf uv.Screen, area uv.Rectangle, st style.Style) {
	if !p.m.projects.loaded {
		p.m.projects.drawStatus(buf, area, st)
		return
	}
	p.m.top().draw(buf, area, st)
}

// switchTab shows the terminal pane's tab to the left or right of the one
// shown, going round at the ends.
func (m *Model) switchTab(d direction) {
	n := len(m.terminals())
	if n == 0 {
		return
	}
	step := 1
	if d == left {
		step = -1
	}
	m.selectTab((m.tab + step + n) % n)
}

// click focuses the pane clicked; on a frame's title it may also zoom or
// switch tabs. A press inside a pane may start a selection.
func (m *Model) click(mouse tea.Mouse) {
	if m.menu || mouse.Button != tea.MouseLeft {
		return
	}
	m.sel = nil
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
			m.press(f.Inset(1), at)
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
			m.selectTab(l.tab)
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
