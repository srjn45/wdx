package tide

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/termview"
)

// The terminal pane shows the terminal of the tab selected and the agent pane
// the active agent, and only those are attached (docs/TIDE_B3.md,
// docs/TIDE_B4.md): what runs through a session with its holder, what does not
// from its history, read-only.

const (
	// attachRest is how long a project switched to stays before its
	// terminal attaches, so that tapping past projects attaches nothing.
	attachRest = 200 * time.Millisecond
	// tailLines is how much of its history a terminal that is not running
	// shows at first; it fetches more as it scrolls.
	tailLines = 1000
)

// historyFunc reads count lines of a history from line from on, or the last
// count lines when from is nil.
type historyFunc func(ctx context.Context, from *int64, count int) (api.TerminalHistoryEnvelope, error)

// attachment is the terminal a pane shows, an agent's being one too.
type attachment struct {
	gen     int    // which attachment this is, of either pane: the messages of an older one are dropped
	where   pane   // the pane it shows in
	id      string // the terminal's, which names its holder
	name    string
	live    bool // attached to a session; false shows its history alone
	history historyFunc
	ctx     context.Context
	cancel  context.CancelFunc
	session *holder.Session
	pane    *termview.Model // nil until attached, or the history fetched
	ended   bool            // the shell exited; the pane shows what it left until the stream says so
	err     error           // why the last try failed, while it is tried again
	tooOld  bool            // its holder cannot be attached to: not tried again
	wait    time.Duration   // before it is tried again
}

// Messages of an attachment, each of the attachment gen.
type (
	attachedMsg struct {
		gen     int
		session *holder.Session
		err     error
	}
	tailMsg struct {
		gen  int
		tail termview.History
		err  error
	}
	retryAttach struct{ gen int }
	// rested ends the rest after the project switch rests.
	rested struct{ rests int }
)

// attachedIn returns the attachment of the pane p, if any.
func (m *Model) attachedIn(p pane) *attachment {
	switch p {
	case terminal:
		return m.term
	case agent:
		return m.agentTerm
	}
	return nil
}

func (m *Model) setAttached(p pane, a *attachment) {
	if p == agent {
		m.agentTerm = a
		return
	}
	m.term = a
}

// attachSelected attaches the terminal of the tab selected and the active
// agent. Called after every message, it does nothing when all is as it should
// be.
func (m *Model) attachSelected() tea.Cmd {
	t, ok := m.selectedTerminal()
	attachTerminal := m.attachPane(terminal, t, ok, func(ctx context.Context, from *int64, count int) (api.TerminalHistoryEnvelope, error) {
		return m.client.GetTerminalHistory(ctx, "id:"+t.Id, "", "", from, count)
	})
	active, ok := m.activeAgent()
	attachAgent := m.attachPane(agent, terminalOf(active), ok, func(ctx context.Context, from *int64, count int) (api.TerminalHistoryEnvelope, error) {
		return m.client.GetAgentHistory(ctx, "id:"+active.Id, "", "", from, count)
	})
	return tea.Batch(attachTerminal, attachAgent)
}

// attachPane attaches t to p when it is not the one attached, or not as it
// should be: live while it runs, its history once it does not.
func (m *Model) attachPane(p pane, t api.Terminal, ok bool, history historyFunc) tea.Cmd {
	want := ok && !m.resting && t.State != api.TerminalStateStarting
	live := t.State == api.TerminalStateRunning
	if a := m.attachedIn(p); a != nil {
		if want && a.id == t.Id && a.live == live {
			return nil
		}
		m.detach(p)
	}
	if !want {
		return nil
	}
	m.attaches++
	ctx, cancel := context.WithCancel(m.ctx)
	a := &attachment{gen: m.attaches, where: p, id: t.Id, name: t.Name, live: live, history: history, ctx: ctx, cancel: cancel}
	m.setAttached(p, a)
	return m.tryAttach(a)
}

// tryAttach attaches a at its pane's size, or fetches the end of its
// history.
func (m *Model) tryAttach(a *attachment) tea.Cmd {
	cols, rows := m.paneArea(a.where, a.live)
	gen := a.gen
	if a.live {
		socket := identity.HolderSocket(m.home, a.id)
		m.log.Info("terminal attaching", "terminal_id", a.id, "cols", cols, "rows", rows)
		return func() tea.Msg {
			s, err := holder.Attach(a.ctx, socket, a.id, holder.Size{Cols: cols, Rows: rows}, 0)
			return attachedMsg{gen: gen, session: s, err: err}
		}
	}
	return func() tea.Msg {
		h, err := a.history(a.ctx, nil, tailLines)
		return tailMsg{gen: gen, tail: termview.History{From: h.History.From, First: h.History.First, Lines: h.History.Lines}, err: err}
	}
}

// attachment returns the attachment when it is gen.
func (m *Model) attachment(gen int) (*attachment, bool) {
	for _, a := range []*attachment{m.term, m.agentTerm} {
		if a != nil && a.gen == gen {
			return a, true
		}
	}
	return nil, false
}

func (m *Model) attached(msg attachedMsg) tea.Cmd {
	a, ok := m.attachment(msg.gen)
	switch {
	case !ok:
		if msg.session != nil {
			msg.session.Close()
		}
		return nil
	case msg.err != nil:
		return m.attachFailed(a, msg.err)
	}
	a.session, a.err, a.wait = msg.session, nil, 0
	a.pane = termview.New(msg.session, fetcher(a))
	snap := msg.session.Snapshot
	m.log.Info("terminal attached", "terminal_id", a.id, "cols", snap.Cols, "rows", snap.Rows,
		"history_lines", snap.HistoryLines)
	return a.pane.Init()
}

func (m *Model) tailFetched(msg tailMsg) tea.Cmd {
	a, ok := m.attachment(msg.gen)
	switch {
	case !ok:
		return nil
	case msg.err != nil:
		return m.attachFailed(a, msg.err)
	}
	a.err, a.wait = nil, 0
	cols, rows := m.paneArea(a.where, false)
	a.pane = termview.NewHistory(msg.tail, cols, rows, fetcher(a))
	m.log.Debug("terminal history shown", "terminal_id", a.id, "lines", len(msg.tail.Lines))
	return a.pane.Init()
}

// fetcher fetches the history of a for its pane.
func fetcher(a *attachment) termview.FetchFunc {
	return func(from int64, count int) (termview.History, error) {
		h, err := a.history(a.ctx, &from, count)
		return termview.History{From: h.History.From, First: h.History.First, Lines: h.History.Lines}, err
	}
}

// attachFailed tries a again after a wait, twice as long each time as the
// stream does, unless its holder is too old to attach to.
func (m *Model) attachFailed(a *attachment, err error) tea.Cmd {
	a.err = err
	if errors.Is(err, holder.ErrAttachUnsupported) {
		a.tooOld = true
		m.log.Info("terminal not attached: its holder is too old", "terminal_id", a.id)
		return nil
	}
	a.wait = min(max(2*a.wait, firstReopenWait), maxReopenWait)
	m.log.Info("terminal not attached", "terminal_id", a.id, "live", a.live, "err", err, "wait_ms", a.wait.Milliseconds())
	gen := a.gen
	return m.tick(a.wait, func(time.Time) tea.Msg { return retryAttach{gen: gen} })
}

func (m *Model) retryAttach(msg retryAttach) tea.Cmd {
	a, ok := m.attachment(msg.gen)
	if !ok || a.pane != nil {
		return nil
	}
	return m.tryAttach(a)
}

// ended handles the end of the session attached: the shell exited, which
// the stream will tell, or the connection was lost while it may run, and
// it is attached again.
func (m *Model) ended(msg termview.EndedMsg) tea.Cmd {
	var a *attachment
	for _, shown := range []*attachment{m.term, m.agentTerm} {
		if shown != nil && shown.pane != nil && msg.Pane == shown.pane {
			a = shown
		}
	}
	if a == nil {
		return nil
	}
	if msg.Exit != nil {
		a.ended = true
		m.log.Info("terminal session ended: the shell exited", "terminal_id", a.id, "exit_code", msg.Exit.Code)
		return nil
	}
	err := msg.Err
	if err == nil {
		err = errors.New("the holder closed the connection")
	}
	m.log.Info("terminal connection lost", "terminal_id", a.id, "err", err)
	m.closePane(a)
	return m.attachFailed(a, err)
}

// detachAll leaves what the panes show, as tide quits.
func (m *Model) detachAll() {
	m.detach(terminal)
	m.detach(agent)
}

// detach leaves the terminal attached to p; it keeps running.
func (m *Model) detach(p pane) {
	a := m.attachedIn(p)
	if a == nil {
		return
	}
	a.cancel()
	m.closePane(a)
	m.setAttached(p, nil)
	m.log.Debug("terminal detached", "terminal_id", a.id)
}

func (m *Model) closePane(a *attachment) {
	if a.pane != nil {
		if r := a.pane.Resets(); r > 0 {
			m.log.Info("terminal pane's vt reset", "terminal_id", a.id, "resets", r)
		}
		a.pane.Close()
		a.pane = nil
	}
	if a.session != nil {
		a.session.Close()
		a.session = nil
	}
}

// rest holds the attaching of a project switched to until it rests.
func (m *Model) rest() tea.Cmd {
	m.rests++
	m.resting = true
	n := m.rests
	return m.tick(attachRest, func(time.Time) tea.Msg { return rested{rests: n} })
}

// paneArea is the size p gives a terminal: inside its frame, as docked when
// it is hidden, less the line saying its state when it is not live.
func (m *Model) paneArea(p pane, live bool) (cols, rows int) {
	l := m.layout
	if !l.shown(p) {
		l.zoomed = none
		l.terminalOpen = l.terminalOpen || p == terminal
	}
	cols, rows = 80, 24
	if frames, ok := l.frames(m.width, m.height-1); ok {
		inside := frames[p].Inset(1)
		cols, rows = inside.Dx(), inside.Dy()
	}
	if !live {
		rows--
	}
	return max(cols, 1), max(rows, 1)
}

// syncSize gives what each pane shows its size, when the pane shows: a hidden
// pane leaves it as it was.
func (m *Model) syncSize() {
	if _, ok := m.frames(); !ok {
		return
	}
	for _, a := range []*attachment{m.term, m.agentTerm} {
		if a == nil || a.pane == nil || !m.layout.shown(a.where) {
			continue
		}
		// A size that cannot be sent means the session is gone, which reading
		// it tells.
		_ = a.pane.SetSize(m.paneArea(a.where, a.live))
	}
}

// paneInput gives a key, a paste or a mouse event to what the pane p shows.
func (m *Model) paneInput(p pane, msg tea.Msg) tea.Cmd {
	a := m.attachedIn(p)
	if a == nil || a.pane == nil || a.ended {
		return nil
	}
	return a.pane.Update(msg)
}

// programMouse gives a mouse event to the program in the agent or terminal
// pane, at the pane's coordinates, when it asked for the mouse: a press inside
// the pane, and the drag and release that follow it. It reports whether the
// program took it.
func (m *Model) programMouse(msg tea.MouseMsg) (tea.Cmd, bool) {
	frames, ok := m.frames()
	if !ok {
		return nil, false
	}
	mouse := msg.Mouse()
	at := uv.Pos(mouse.X, mouse.Y)
	p := m.mouseTo
	if _, pressed := msg.(tea.MouseClickMsg); pressed {
		p = none
		for _, q := range []pane{agent, terminal} {
			if f, shown := frames[q]; shown && at.In(f.Inset(1)) {
				p = q
			}
		}
	}
	a := m.attachedIn(p)
	if a == nil || a.pane == nil || !a.live || a.ended || !a.pane.MouseWanted() {
		m.mouseTo = none
		return nil, false
	}
	f, shown := frames[p]
	if !shown {
		return nil, false
	}
	area := f.Inset(1)
	mouse.X = min(max(at.X, area.Min.X), area.Max.X-1) - area.Min.X
	mouse.Y = min(max(at.Y, area.Min.Y), area.Max.Y-1) - area.Min.Y
	var out tea.Msg
	switch msg.(type) {
	case tea.MouseClickMsg:
		m.mouseTo = p
		out = tea.MouseClickMsg(mouse)
	case tea.MouseMotionMsg:
		out = tea.MouseMotionMsg(mouse)
	case tea.MouseReleaseMsg:
		m.mouseTo = none
		out = tea.MouseReleaseMsg(mouse)
	default:
		return nil, false
	}
	return a.pane.Update(out), true
}

// paneWheel gives a turn of the wheel over the pane p to it: to the program,
// or scrolling its history.
func (m *Model) paneWheel(p pane, msg tea.MouseWheelMsg, area uv.Rectangle) tea.Cmd {
	a := m.attachedIn(p)
	if a == nil || a.pane == nil {
		return nil
	}
	msg.X, msg.Y = msg.X-area.Min.X, msg.Y-area.Min.Y
	return a.pane.Update(msg)
}

// paneCursor is the program's cursor in the window, while its pane has the
// focus and shows the live screen, with nothing over it.
func (m *Model) paneCursor() *tea.Cursor {
	a := m.attachedIn(m.layout.focus)
	if a == nil || a.pane == nil || !a.live || m.menu || m.confirm != nil || m.popupOpen() {
		return nil
	}
	frames, ok := m.frames()
	f, shown := frames[a.where]
	if !ok || !shown {
		return nil
	}
	c := a.pane.Cursor()
	if c == nil {
		return nil
	}
	c.X += f.Min.X + 1
	c.Y += f.Min.Y + 1
	return c
}

// paneStatus is what the status line says of the pane focused while it is
// scrolled back or cannot fetch history.
func (m *Model) paneStatus() (string, bool) {
	a := m.attachedIn(m.layout.focus)
	if a == nil || a.pane == nil {
		return "", false
	}
	if a.pane.FetchErr() != nil {
		return "history could not be fetched: scroll to try again", true
	}
	if !a.pane.Scrolled() {
		return "", false
	}
	top, lines := a.pane.Position()
	s := fmt.Sprintf("history %d of %d · PgDn down", top+1, lines)
	if a.live {
		s += " · any key: back"
	}
	return s, true
}

// attachState is what the pane p says of the terminal t while it does not
// show it: why, and what happens.
func (m *Model) attachState(p pane, t api.Terminal) string {
	a := m.attachedIn(p)
	switch {
	case t.State == api.TerminalStateStarting:
		return m.st.Muted("starting…")
	case a == nil || a.id != t.Id:
		return m.st.Muted("attaching…")
	case a.tooOld:
		return m.st.Failure("it was started by a wdx too old to attach to") + "\n" +
			m.st.Muted("close and open its project, or remove the "+p.String()+" and create it again, to restart it")
	case a.err != nil && a.live:
		return m.st.Failure("could not attach, trying again…") + "\n" + m.st.Muted(a.err.Error())
	case a.err != nil:
		return m.st.Failure("its history could not be read, trying again…") + "\n" + m.st.Muted(a.err.Error())
	}
	return m.st.Muted("attaching…")
}
