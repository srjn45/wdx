package tide

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

// A TIDE on another node shows that node, its host, through a session (the
// wdx doc HUB_REMOTE_TIDE). What differs from a local one reads Options.Node;
// this file is what only a remote one has: following the session, and the
// message that replaces the window once it ends.

// defaultSessionLost is how long the host may be unreachable, without a
// stream into it opening, before the session counts as ended.
const defaultSessionLost = 30 * time.Second

// Link is a remote TIDE's session into its host: what each stream into it
// met, which the client reports as it dials, and how to open another session.
type Link struct {
	open    func(ctx context.Context, code string) error
	mu      sync.Mutex
	last    error
	changed chan struct{}
}

// NewLink returns a link whose sessions open, with the code typed, through open.
func NewLink(open func(ctx context.Context, code string) error) *Link {
	return &Link{open: open, changed: make(chan struct{}, 1)}
}

// Dialed records what opening a stream into the host met: nil when it opened.
func (l *Link) Dialed(err error) {
	l.mu.Lock()
	l.last = err
	l.mu.Unlock()
	select {
	case l.changed <- struct{}{}:
	default: // TIDE reads the last one anyway
	}
}

// next waits for the next stream's outcome, or ctx's end.
func (l *Link) next(ctx context.Context) (error, bool) {
	select {
	case <-l.changed:
	case <-ctx.Done():
		return nil, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.last, true
}

// The messages of following the session.
type (
	linkChanged struct {
		err error
	}
	// sessionLostFor ends the wait of the lost'th time the host was lost.
	sessionLostFor struct{ lost int }
	sessionOpened  struct{ err error }
)

// remoteState is what a remote TIDE knows of its session.
type remoteState struct {
	link      *Link
	lostAfter time.Duration
	lostSince time.Time     // when the host went unreachable; zero while it is not
	lost      int           // how many times it did, numbering the waits
	lostErr   error         // what the last stream met while it is
	ended     *sessionEnded // the message shown once the session ended; nil while it holds
}

// sessionEnded is the message that replaces the window: why the session
// ended, and the code that opens another.
type sessionEnded struct {
	why string
	// reasonKnown is true when why is the reason the host gave, not every
	// reason a session ends for.
	reasonKnown bool
	code        string
	busy        bool
	failure     string
}

// watchLink waits for the next stream's outcome.
func (m *Model) watchLink() tea.Cmd {
	l := m.remote.link
	if l == nil {
		return nil
	}
	return func() tea.Msg {
		err, ok := l.next(m.ctx)
		if !ok {
			return nil
		}
		return linkChanged{err: err}
	}
}

// linkWasChanged takes what a stream into the host met: a stream opened is a
// session that holds, session_closed one that ended, and a host unreachable
// is waited out for lostAfter before it counts as one that ended.
func (m *Model) linkWasChanged(msg linkChanged) tea.Cmd {
	r := &m.remote
	watch := m.watchLink()
	var p api.Problem
	if e, ok := errors.AsType[*client.ProblemError](msg.err); ok {
		p = e.Problem
	}
	switch {
	case msg.err == nil:
		if !r.lostSince.IsZero() || r.ended != nil {
			m.log.Info("session holds again", "node", m.node, "ended_shown", r.ended != nil)
		}
		r.lostSince, r.lostErr, r.ended = time.Time{}, nil, nil // the wait under way ends nothing
		return watch
	case p.Code == api.ErrorCodeSessionClosed:
		why, known := closedWhy(m.node, p)
		// Of the streams that met the session closed, only the first the
		// host refused knows why; the others may come first.
		if r.ended != nil && known && !r.ended.reasonKnown {
			m.log.Info("session ended, the reason known", "node", m.node, "why", why)
			r.ended.why, r.ended.reasonKnown = why, true
			return watch
		}
		m.sessionEnded(why, known)
		return watch
	case p.Code == api.ErrorCodeNodeOffline || p.Code == api.ErrorCodeHubUnreachable:
		r.lostErr = msg.err
		if !r.lostSince.IsZero() {
			return watch
		}
		r.lostSince = m.now()
		r.lost++
		lost := r.lost
		m.log.Info("host unreachable", "node", m.node, "code", p.Code, "wait_ms", r.lostAfter.Milliseconds())
		return tea.Batch(watch, m.tick(r.lostAfter, func(time.Time) tea.Msg { return sessionLostFor{lost: lost} }))
	}
	return watch // a failure of its own, which the request that met it shows
}

// sessionLostWaited ends the session when the host stayed unreachable
// through the wait.
func (m *Model) sessionLostWaited(msg sessionLostFor) {
	r := &m.remote
	if msg.lost != r.lost || r.lostSince.IsZero() {
		return
	}
	why := fmt.Sprintf("%s could not be reached for %s", m.node, r.lostAfter.Round(time.Second))
	if r.lostErr != nil {
		why += ": " + r.lostErr.Error()
	}
	m.sessionEnded(why, true)
}

// sessionEnded replaces the window with the message, unless it shows.
// reasonKnown says why is the reason, not a list of the possible ones.
func (m *Model) sessionEnded(why string, reasonKnown bool) {
	r := &m.remote
	if r.ended != nil {
		return
	}
	m.log.Info("session ended", "node", m.node, "why", why)
	r.ended = &sessionEnded{why: why, reasonKnown: reasonKnown}
	m.closeMenu()
	m.sel = nil
}

// closedWhy says why the session p reports closed did, as the host said
// when it did, and whether the host said it.
func closedWhy(node string, p api.Problem) (string, bool) {
	reason := ""
	if p.Details != nil {
		reason, _ = (*p.Details)["reason"].(string)
	}
	switch reason {
	case "closed":
		return "it was closed, on " + node + " or here", true
	case "idle":
		return "it was idle for too long", true
	case "restart":
		return "the daemon on " + node + " restarted", true
	case "remote_access_off":
		return "remote access was turned off", true
	case "logged_out":
		return "a node logged out of the hub", true
	}
	return "it was closed on " + node + " or here, the daemon on " + node + " restarted, it was idle for too long, remote access was turned off, or the network failed", false
}

// endedInput gives the message what the person does while it shows: keys
// and pastes, the mouse doing nothing. It reports whether it took msg.
func (m *Model) endedInput(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.endedKey(msg), true
	case tea.PasteMsg:
		if e := m.remote.ended; !e.busy {
			e.code += strings.Join(strings.Fields(msg.Content), "")
		}
		return nil, true
	case tea.MouseMsg:
		return nil, true
	}
	return nil, false
}

// endedKey types the code into the message, opens a session with it on
// Enter, or quits on C-q.
func (m *Model) endedKey(msg tea.KeyPressMsg) tea.Cmd {
	e := m.remote.ended
	if e.busy {
		return nil
	}
	e.failure = ""
	switch key := msg.String(); {
	case key == menuKey:
		m.log.Info("tide quit", "on", "the session's end")
		m.unfollow()
		m.detachAll()
		return tea.Quit
	case key == "enter" && strings.TrimSpace(e.code) == "":
		e.failure = "type the code the app shows for " + m.node
	case key == "enter":
		e.busy = true
		code := strings.TrimSpace(e.code)
		m.log.Info("session asked for", "node", m.node, "code_bytes", len(code))
		return func() tea.Msg { return sessionOpened{err: m.remote.link.open(m.ctx, code)} }
	case key == "backspace" && e.code != "":
		r := []rune(e.code)
		e.code = string(r[:len(r)-1])
	case typed(msg):
		e.code += msg.Text
	}
	return nil
}

// sessionWasOpened goes on with the view TIDE had, through the new session:
// the project shown is followed again and its terminals attach again.
func (m *Model) sessionWasOpened(msg sessionOpened) tea.Cmd {
	r := &m.remote
	e := r.ended
	if e == nil {
		return nil
	}
	e.busy, e.code = false, ""
	if msg.err != nil {
		m.log.Info("session not opened", "node", m.node, "err", msg.err)
		e.failure = msg.err.Error()
		return nil
	}
	m.log.Info("session opened again", "node", m.node, "project", m.shown)
	r.ended, r.lostSince, r.lostErr = nil, time.Time{}, nil
	m.detachAll()
	cmds := []tea.Cmd{m.listProjects()}
	if m.shown != "" {
		cmds = append(cmds, m.follow(m.shown))
	}
	return tea.Batch(cmds...)
}

// drawEnded draws the message over the whole window.
func (m *Model) drawEnded(buf uv.Screen) {
	e := m.remote.ended
	st := m.st
	area := uv.Rect(2, 1, max(m.width-4, 1), max(m.height-2, 1))
	width := area.Dx()
	lines := []string{st.Failure("session to " + m.node + " closed"), ""}
	lines = append(lines, strings.Split(wrap(e.why, width), "\n")...)
	lines = append(lines, "", wrap("Type the code the app shows for "+m.node+" to open a new session and go on, or C-q to quit.", width), "")
	field := "code for " + m.node + ": " + e.code + "_"
	lines = append(lines, field)
	switch {
	case e.busy:
		lines = append(lines, "", st.Muted("opening…"))
	case e.failure != "":
		lines = append(lines, "")
		for _, l := range strings.Split(wrap(e.failure, width), "\n") {
			lines = append(lines, st.Failure(l))
		}
	}
	uv.NewStyledString(strings.Join(lines, "\n")).Draw(buf, area)
}

// endedBar is the status line under the message.
func (m *Model) endedBar() string {
	return m.st.Bar(style.BarLine(m.width, " "+m.st.BarName(m.node), "Enter open a session  C-q quit", m.st.BarVersion("")), style.StatusFailed)
}

// windowTitle is the terminal window's title: the node a remote TIDE shows
// beside TIDE's name, so that it is never taken for a local one.
func (m *Model) windowTitle() string {
	if m.node == "" {
		return "TIDE"
	}
	return identity.Name + " tide · " + m.node
}

// hostPathField asks a remote TIDE for a path on the host, in place of the
// directory browser, which reads this machine's files: a repository to open
// or, to create a project, the directory to create it in, which the name
// field asks next.
func (m *Model) hostPathField(create bool) *fieldScreen {
	f := &fieldScreen{
		heading: localTitle,
		prompt:  "Absolute path on " + m.node + " of the git repository to open:",
		empty:   "type a path",
		working: "opening…",
		check:   m.hostPath,
		submit:  func(path string) tea.Msg { return createProject{req: localRequest(filepath.Clean(path))} },
	}
	if create {
		f.heading = createTitle
		f.prompt = "Absolute path on " + m.node + " of the directory to create the project in:"
		f.working, f.leads = "", true
		f.submit = func(dir string) tea.Msg {
			dir = filepath.Clean(dir)
			return pushScreen{&fieldScreen{
				heading: createTitle,
				prompt:  "Name of the project, created in " + dir + " on " + m.node + ":",
				empty:   "type a name",
				working: "creating…",
				submit:  func(name string) tea.Msg { return createProject{req: newRequest(dir, name)} },
			}}
		}
	}
	return f
}

// hostPath fails on a path that is not absolute: the host does not share
// this directory.
func (m *Model) hostPath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("type an absolute path: %s means nothing on %s", path, m.node)
	}
	return nil
}

// settingsElsewhere is the menu's line in place of settings in a remote TIDE.
func (m *Model) settingsElsewhere() menuItem {
	return menuItem{key: settingsKey, label: "settings are changed on " + m.node + " itself"}
}

// sessionLostAfter is the wait Options gives, or the default.
func sessionLostAfter(d time.Duration) time.Duration { return cmp.Or(d, defaultSessionLost) }
