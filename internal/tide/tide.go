// Package tide is the terminal IDE, wdx tide: a project pane, an agent pane
// and a terminal pane in one window, which the keys and the mouse move the
// focus between, zoom and hide (docs/TIDE.md). It is a client of the
// daemon, as the cli is. The terminal pane shows the terminal of the tab
// selected, and the agent pane the agent made active in the project pane.
package tide

import (
	"cmp"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/style"
	"github.com/srjn45/wdx/internal/termview"
)

// tide's own keys, as Bubble Tea names them; every other key goes to the
// focused pane.
const (
	zoomKey     = "ctrl+]"
	terminalKey = `ctrl+\`
	menuKey     = "ctrl+q"
	closeTabKey = "-" // of the menu
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

	workDir, userHome, projectsDir, home string // from Options

	width, height  int
	layout         layout
	projects       *projectList
	root           screen                                                               // what the project pane shows: the project, or that there is none
	shown          string                                                               // the project shown, or "" before one is
	previous       string                                                               // the project shown before it, which the switcher selects first
	tab            int                                                                  // the terminal pane's tab shown, an index into terminals()
	following      *following                                                           // the stream of the project shown
	streams        int                                                                  // how many streams were opened, numbering them
	openStream     func(ctx context.Context, ref string) (*client.ProjectStream, error) // nil in tests that follow nothing
	selectedTab    map[string]string                                                    // the terminal id of the tab selected, by project id, for the run
	createdTab     map[string]string                                                    // the terminal created here in each project, selected once the stream brings it
	agentLists     map[string]*agentList                                                // the Agents list of each project shown, by project id, for the run
	view           viewState                                                            // how far what tide shows is from what the daemon keeps
	startView      *tideView                                                            // the view the daemon had at start; nil when it had none to read
	term           *attachment                                                          // the terminal the terminal pane shows
	agentTerm      *attachment                                                          // the active agent's terminal, which the agent pane shows
	attaches       int                                                                  // how many attachments were made, numbering them
	rests          int                                                                  // how many project switches rested, numbering them
	resting        bool                                                                 // a project switched to has not rested yet: nothing attaches
	tabRests       int                                                                  // how many agent tab switches rested, numbering them
	tabSwitching   bool                                                                 // an agent tab was switched to with the keys, and they have not rested yet
	tabResting     bool                                                                 // another was switched to soon after: no agent attaches
	mouseTo        pane                                                                 // the pane whose program got a press: its drag and release go too
	child          *childBox                                                            // the worktree C-q c asks for, while it does
	editorClose    *editorCloseBox                                                      // what closing an editor's tab asks, while it does
	editorKeys     map[string]string                                                    // what to type into an editor once its tab attaches, by terminal id
	land           *landBox                                                             // what L asks, while it is open
	confirm        *confirmBox                                                          // the confirmation of C-q x, while it is open
	taskBox        *taskAnswers                                                         // the answers to a plan's task that cannot go on, while open
	terminalNotice string                                                               // why the last terminal request failed, until the next key
	statusNotice   string                                                               // what the status line says in place of the keys, until the next key or click
	menu           bool                                                                 // the menu is open
	projectMenu    bool                                                                 // and shows its Project submenu
	switcher       *switcherBox                                                         // the project switcher, while it is open
	opening        []screen                                                             // [Open Project] and the screens above it, while that popup is open
	strip          int                                                                  // how many times the project strip was shown
	stripShown     bool                                                                 // the project strip is shown
	lastClick      click
	sel            *selection                // text selected with the mouse, while dragged and until the next click or key
	copy           func(text string) tea.Cmd // puts text on the clipboard; tests replace it

	versionOf     func(ctx context.Context) (string, error) // from Options
	daemonVersion string                                    // what the daemon last answered, "" when it did not
	resume        *Resume                                   // what to show first, until the projects are listed
	stripLinger   time.Duration                             // from Options
	sections      []section                                 // the left pane's, in the order set

	values      valuePane     // the agent info or the settings shown, if any
	settings    []api.Setting // what the daemon last said, while a settings tab reads them
	settingsErr string        // why it could not read them

	commit    *commitBox  // what C asks the agent, or s sends it, while it does
	comment   *commentBox // the comment c or e asks for, while it does
	lastFocus pane        // the pane in focus at the last update: coming to the list reads the worktrees
}

// content is what a pane shows.
type content interface {
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
	// Home is wdx's home, where the terminals' holders listen.
	Home string
	// DaemonVersion asks the daemon its version, which the status line
	// shows when it differs; nil asks nothing.
	DaemonVersion func(ctx context.Context) (string, error)
	// Resume is what the run before showed, when it reloaded.
	Resume *Resume
	// StripLinger is how long the project strip stays after the last switch;
	// zero is a second.
	StripLinger time.Duration
	// SectionOrder is the tide.section_order setting; "" is the default order.
	SectionOrder string
	// ViewSettle is how long what tide shows stays the same before it is sent to
	// the daemon; zero is half a second.
	ViewSettle time.Duration
}

// click is where and when a click on a frame's title happened.
type click struct {
	pane pane
	at   time.Time
	tab  int // the tab clicked, or -1
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
		home:        opts.Home,
		now:         time.Now,
		tick:        tea.Tick,
		layout:      newLayout(),
		projects:    projects,
		root:        noProjectScreen{},
		openStream:  opts.Client.ProjectEventsWithWorktrees,
		selectedTab: map[string]string{},
		createdTab:  map[string]string{},
		agentLists:  map[string]*agentList{},
		versionOf:   opts.DaemonVersion,
		resume:      opts.Resume,
		stripLinger: cmp.Or(opts.StripLinger, defaultStripLinger),
		sections:    sectionOrder(opts.SectionOrder),
		view:        viewState{settle: cmp.Or(opts.ViewSettle, defaultViewSettle), sentProject: map[string][]byte{}},
	}
	// tide starts on the project pane, under the switcher or [Open Project], and
	// shows the terminals of a project only once one is shown.
	m.copy = m.copyToClipboard
	m.layout.setFocus(project)
	m.layout.terminalOpen = false
	return m
}

func (m *Model) Init() tea.Cmd { return tea.Batch(m.readTideView(), m.checkDaemonVersion()) }

// Update handles msg, then attaches the terminal of the tab selected and the
// active agent if they are not, and gives them their panes' sizes.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := m.update(msg)
	m.keepCursorShown()
	values := m.valuesSync()
	diff := m.diffSync()
	plan := m.planSync()
	worktrees := m.worktreesSync()
	code := m.codeSync()
	filter := m.codeFilterSync()
	attach := m.attachSelected()
	tabs := m.tabsSync()
	view := m.viewSync()
	m.syncSize()
	return m, tea.Batch(cmd, values, diff, plan, worktrees, code, filter, attach, tabs, view)
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.sel = nil // the panes move
	case projectsListed:
		return m, m.listed(msg)
	case tabsSaved:
		m.tabsWereSaved(msg)
	case tideViewRead:
		return m, m.tideViewWasRead(msg)
	case viewSettled:
		return m, m.viewSettledFor(msg)
	case viewSent:
		return m, m.viewWasSent(msg)
	case daemonVersionMsg:
		m.daemonVersion = msg.version
		return m, m.tick(VersionCheckEvery, func(time.Time) tea.Msg { return checkDaemonVersion{} })
	case checkDaemonVersion:
		return m, m.checkDaemonVersion()
	case popScreen:
		if n := len(m.opening); n > 0 {
			m.log.Debug("screen popped", "screen", m.opening[n-1].title())
			m.opening = m.opening[:n-1]
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
		if b, ok := m.openTop().(*dirBrowser); ok {
			return m, b.update(msg)
		}
	case projectOpened:
		return m, m.opened(msg)
	case createCodeEntry, renameCodeEntry, removeCodeEntry:
		return m, m.changeCodeEntry(msg)
	case codeEntryChanged:
		return m, m.codeEntryChanged(msg)
	case renameAgentAsked:
		return m, m.sendAgentRename(msg)
	case agentRenamed:
		return m, m.agentRenamed(msg)
	case switchProject:
		return m, m.show(msg.name)
	case openCreate:
		m.switcher = nil
		return m, m.openProject()
	case closeSwitcher:
		m.switcher = nil
		m.log.Debug("switcher closed")
	case streamOpened:
		return m, m.streamOpened(msg)
	case projectEvent:
		return m, m.projectEvent(msg)
	case terminalCreated:
		m.terminalCreated(msg)
	case agentCreated:
		m.agentCreated(msg)
	case attachedMsg:
		return m, m.attached(msg)
	case tailMsg:
		return m, m.tailFetched(msg)
	case retryAttach:
		return m, m.retryAttach(msg)
	case rested:
		if msg.rests == m.rests {
			m.resting = false
		}
	case tabRested:
		if msg.rests == m.tabRests {
			m.tabSwitching, m.tabResting = false, false
		}
	case termview.EndedMsg:
		return m, m.ended(msg)
	case removed:
		m.removed(msg)
	case startedAgain:
		m.startedAgain(msg)
	case restarted:
		m.restarted(msg)
	case planRan:
		m.planRanMsg(msg)
	case planAnswered:
		m.planAnsweredMsg(msg)
	case projectClosed:
		return m, m.closed(msg)
	case projectRemoved:
		return m, m.projectWasRemoved(msg)
	case valueSaved:
		m.valueSavedMsg(msg)
	case settingsListed:
		m.settingsListed(msg)
	case settingSaved:
		m.settingSaved(msg)
	case planLoaded:
		m.planLoadedMsg(msg)
	case diffLoaded:
		return m, m.diffLoaded(msg)
	case fileHighlighted:
		m.fileHighlighted(msg)
	case editorOpened:
		m.editorOpened(msg)
	case codeLoaded:
		m.codeLoaded(msg)
	case codeMatched:
		m.codeMatchedMsg(msg)
	case commitSent:
		m.commitSent(msg)
	case landSettingsRead:
		m.landSettingsRead(msg)
	case landSettingsSaved:
		m.landSettingsSaved(msg)
	case landStarted:
		m.landStarted(msg)
	case verifyAnswered:
		m.verifyAnsweredMsg(msg)
	case commentAnswered:
		m.commentAnsweredMsg(msg)
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
		return m, m.click(msg)
	case tea.MouseMotionMsg:
		if cmd, ok := m.programMouse(msg); ok {
			return m, cmd
		}
		m.drag(msg.Mouse())
	case tea.MouseReleaseMsg:
		if cmd, ok := m.programMouse(msg); ok {
			return m, cmd
		}
		return m, m.release(msg.Mouse())
	case tea.MouseWheelMsg:
		return m, m.wheel(msg)
	case tea.PasteMsg:
		if m.child != nil && !m.menu {
			m.child.worktree += strings.Join(strings.Fields(msg.Content), "")
			return m, nil
		}
		if m.commit != nil && !m.menu {
			m.commit.text += msg.Content
			return m, nil
		}
		if m.land != nil && m.land.editing && !m.menu {
			m.land.check += strings.Join(strings.Split(msg.Content, "\n"), " ")
			return m, nil
		}
		if m.comment != nil && !m.menu {
			m.comment.text += strings.Join(strings.Split(msg.Content, "\n"), " ")
			return m, nil
		}
		if f, ok := m.openTop().(*fieldScreen); ok && !m.menu && m.confirm == nil {
			f.paste(msg.Content)
			return m, nil
		}
		if !m.popupOpen() && !m.menu && m.confirm == nil {
			return m, m.paneInput(m.layout.focus, msg)
		}
	default:
		// The panes' own messages: their output, the history they fetched.
		// Each names its pane, which drops those of another.
		var cmds []tea.Cmd
		for _, a := range []*attachment{m.term, m.agentTerm} {
			if a != nil && a.pane != nil {
				cmds = append(cmds, a.pane.Update(msg))
			}
		}
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

// pane returns what p shows: for the terminal pane, its tab shown.
func (m *Model) pane(p pane) content {
	switch p {
	case terminal:
		return terminalPane{m}
	case agent:
		return agentPane{m}
	}
	return projectPane{m}
}

// openTop is the screen on top of the [Open Project] popup; nil while it is
// closed.
func (m *Model) openTop() screen {
	if n := len(m.opening); n > 0 {
		return m.opening[n-1]
	}
	return nil
}

// popupOpen reports whether a project popup, the switcher or [Open Project],
// takes the keys.
func (m *Model) popupOpen() bool { return m.switcher != nil || len(m.opening) > 0 }

// show makes name the project shown, in the project pane, and closes the
// project popups. The first one shown brings the terminal pane.
func (m *Model) show(name string) tea.Cmd {
	if m.shown == "" {
		m.layout.terminalOpen = true
	}
	if name != m.shown && m.shown != "" {
		m.previous = m.shown
	}
	var follow, rest tea.Cmd
	if name != m.shown || m.following == nil {
		m.tab = 0
		follow = m.follow(name)
		rest = m.rest()
	}
	m.shown, m.switcher, m.opening = name, nil, nil
	m.root = &projectScreen{m: m, name: name}
	m.log.Info("project shown", "name", name)
	return tea.Batch(m.listProjects(), follow, rest)
}

// push puts s on top of the [Open Project] popup's stack.
func (m *Model) push(s screen) {
	m.opening = append(m.opening, s)
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

// toOpenProject leaves the screens above [Open Project] for it, opening it
// when it is closed, with notice saying what happened.
func (m *Model) toOpenProject(notice string) {
	if len(m.opening) == 0 {
		m.push(&openProjectScreen{projects: m.projects, notice: notice})
		return
	}
	m.opening = m.opening[:1]
	o := m.opening[0].(*openProjectScreen)
	o.notice, o.failure = notice, ""
}

// openProject opens the [Open Project] popup, unless it is open, and lists
// the projects again for it. Until they are first listed that is all it
// does: the answer decides which popup opens.
func (m *Model) openProject() tea.Cmd {
	if m.projects.loaded && len(m.opening) == 0 {
		m.push(&openProjectScreen{projects: m.projects})
	}
	return m.listProjects()
}

// openSwitcher opens the switcher, in place of [Open Project], and lists
// the projects again for it. Until they are first listed that is all it
// does: the answer decides which popup opens.
func (m *Model) openSwitcher() tea.Cmd {
	if m.projects.loaded {
		m.opening = nil
		m.switcher = newSwitcher(m.projects, m.previous)
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
		line, ok := m.menuLine(name) // of the menu shown, before it closes
		m.closeMenu()
		if altArrows[name] && m.programInFocus() {
			m.log.Info("alt key sent to the program", "key", name, "pane", m.layout.focus)
			return m.paneInput(m.layout.focus, msg)
		}
		if ok {
			return line.do()
		}
		return nil
	}
	m.terminalNotice, m.statusNotice = "", ""
	if m.child != nil {
		return m.childKey(msg)
	}
	if m.commit != nil {
		return m.commitKey(msg)
	}
	if m.land != nil {
		return m.landBoxKey(msg)
	}
	if m.comment != nil {
		return m.commentBoxKey(msg)
	}
	if m.confirm != nil {
		return m.confirmKey(name)
	}
	if m.taskBox != nil {
		return m.taskBoxKey(name)
	}
	if m.editorClose != nil {
		m.editorCloseKey(name)
		return nil
	}
	if m.switcher != nil && name != menuKey {
		return m.switcher.update(msg)
	}
	if s := m.openTop(); s != nil && name != menuKey {
		return s.update(msg)
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
		m.openMenu(false)
	default:
		d, ok := focusKeys[name]
		switch {
		case !ok && m.layout.focus == project && m.filterTyping():
			return m.filterKeyTyped(msg)
		case !ok && m.layout.focus == project && m.menuOffers(name):
			return m.menuAction(name)
		case !ok && m.layout.focus == project:
			return m.projectPaneKey(msg)
		case !ok:
			return m.paneInput(m.layout.focus, msg)
		case m.layout.focus == terminal && (d == left || d == right):
			m.switchTab(d)
		case m.layout.focus == agent && (d == left || d == right):
			return m.tabKey(d)
		case m.layout.move(d):
			m.changed("move")
		}
	}
	return nil
}

// projectPaneKey gives a key to the project pane's screen, once the projects
// are listed; until then r lists them again after a failure.
func (m *Model) projectPaneKey(msg tea.KeyPressMsg) tea.Cmd {
	l := m.projects
	switch {
	case l.loaded:
		return m.root.update(msg)
	case msg.String() == retryKey && l.err != nil && !l.listing:
		m.log.Info("projects listed again", "reason", "retry key")
		return m.listProjects()
	}
	return nil
}

// projectPane is the project pane as a content.
type projectPane struct{ m *Model }

func (p projectPane) draw(buf uv.Screen, area uv.Rectangle, st style.Style) {
	if !p.m.projects.loaded {
		p.m.projects.drawStatus(buf, area, st)
		return
	}
	p.m.root.draw(buf, area, st)
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
// switch tabs. A press inside a pane may start a selection, or go to the
// program in the terminal pane when it asked for the mouse.
func (m *Model) click(msg tea.MouseClickMsg) tea.Cmd {
	mouse := msg.Mouse()
	if m.menu || m.popupOpen() || m.child != nil || m.commit != nil || m.comment != nil || m.land != nil || m.editorClose != nil {
		return nil
	}
	if m.confirm == nil {
		if cmd, ok := m.programMouse(msg); ok {
			m.sel = nil
			m.layout.setFocus(m.mouseTo)
			m.changed("click")
			return cmd
		}
	}
	if mouse.Button != tea.MouseLeft {
		return nil
	}
	m.sel = nil
	frames, ok := m.frames()
	if !ok {
		return nil
	}
	at := uv.Pos(mouse.X, mouse.Y)
	// A title is on a border the frame above shares: the title wins.
	for _, p := range panes {
		if f, shown := frames[p]; shown && at.In(f) && at.Y == f.Min.Y {
			m.layout.setFocus(p)
			m.clickTitle(p, f, at)
			return nil
		}
	}
	for _, p := range panes {
		f, shown := frames[p]
		if !shown || !at.In(f) {
			continue
		}
		m.layout.setFocus(p)
		var cmd tea.Cmd
		if at.In(f.Inset(1)) {
			m.press(f.Inset(1), at)
			if s, ok := m.root.(*projectScreen); ok && p == project && m.projects.loaded {
				cmd = s.click(at.X-f.Min.X-1, at.Y-f.Min.Y-1, f.Dx()-2, f.Dy()-2)
			}
			if p == agent && m.confirm == nil {
				if m.textShown() {
					m.diffClick(at.Y - f.Min.Y - 1)
				}
				cmd = m.valueClick(at.X-f.Min.X-1, at.Y-f.Min.Y-1, f.Inset(1))
			}
		}
		m.changed("click")
		return cmd
	}
	return nil
}

// clickTitle handles a click on p's top border: its zoom button, a tab, a
// tab's close button, an arrow of hidden tabs, or a second click soon after the
// first, on the same thing, zooms. A button never counts towards a double-click:
// the next click may land on what took its place.
func (m *Model) clickTitle(p pane, f uv.Rectangle, at uv.Position) {
	now := m.now()
	on := label{tab: -1}
	for _, l := range m.labels(p, f) {
		if at.In(l.area) {
			on = l
			break
		}
	}
	switch {
	case on.zoom:
		m.lastClick = click{}
		m.layout.toggleZoom()
		m.changed("zoom button")
		return
	case on.closes:
		m.lastClick = click{}
		m.askCloseTab(m.openTabs()[on.tab].tab, "clicked")
		return
	case on.arrow:
		m.lastClick = click{}
		m.selectClicked(p, on.tab)
		return
	}
	double := m.lastClick.pane == p && m.lastClick.tab == on.tab && now.Sub(m.lastClick.at) <= doubleClick
	m.lastClick = click{pane: p, at: now, tab: on.tab}
	if on.tab >= 0 {
		m.selectClicked(p, on.tab)
	}
	if double {
		m.lastClick = click{}
		m.layout.toggleZoom()
		m.changed("double-click")
		return
	}
	m.changed("click")
}

// selectClicked selects the tab i of p.
func (m *Model) selectClicked(p pane, i int) {
	if p == agent {
		m.list().active = m.openTabs()[i].tab
		m.log.Debug("tab selected", "tab", m.list().active)
		return
	}
	m.selectTab(i)
}

// wheel goes to the pane under the pointer, whichever has the focus.
func (m *Model) wheel(msg tea.MouseWheelMsg) tea.Cmd {
	frames, ok := m.frames()
	if !ok {
		return nil
	}
	at := uv.Pos(msg.X, msg.Y)
	for _, p := range panes {
		if f, shown := frames[p]; shown && at.In(f.Inset(1)) {
			return m.paneWheel(p, msg, f.Inset(1))
		}
	}
	return nil
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
