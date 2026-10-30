package tide

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/style"
)

// following is the stream of the project shown (docs/TIDE_B2.md): every
// event is the project whole, which replaces what tide had.
type following struct {
	gen     int    // which stream this is: the messages of an older one are dropped
	ref     string // the project's name, then "id:" and its id once known
	ctx     context.Context
	cancel  context.CancelFunc
	stream  *client.ProjectStream
	project *api.Project  // as last sent; nil until the first event
	err     error         // why the stream broke, while it is opened again
	wait    time.Duration // before it is opened again
}

// Messages of the stream, each of the stream gen.
type (
	streamOpened struct {
		gen    int
		stream *client.ProjectStream
		err    error
	}
	projectEvent struct {
		gen   int
		event client.ProjectEvent
		err   error
	}
	reopenStream struct{ gen int }
)

// How long tide waits before opening a broken stream again: twice as long
// each time, up to maxReopenWait.
const (
	firstReopenWait = 500 * time.Millisecond
	maxReopenWait   = 5 * time.Second
)

// follow follows the project name, leaving the stream of the one shown
// before.
func (m *Model) follow(name string) tea.Cmd {
	m.unfollow()
	if m.openStream == nil {
		return nil
	}
	m.streams++
	ctx, cancel := context.WithCancel(m.ctx)
	m.following = &following{gen: m.streams, ref: name, ctx: ctx, cancel: cancel}
	m.log.Info("project followed", "name", name)
	return m.open()
}

func (m *Model) unfollow() {
	if f := m.following; f != nil {
		f.cancel() // which ends the read waiting on it
		m.following = nil
	}
}

func (m *Model) open() tea.Cmd {
	f := m.following
	return func() tea.Msg {
		s, err := m.openStream(f.ctx, f.ref)
		return streamOpened{gen: f.gen, stream: s, err: err}
	}
}

// next waits for the stream's next event.
func (m *Model) next() tea.Cmd {
	f := m.following
	return func() tea.Msg {
		e, err := f.stream.Next()
		return projectEvent{gen: f.gen, event: e, err: err}
	}
}

// current returns f when it is the stream gen.
func (m *Model) current(gen int) (*following, bool) {
	f := m.following
	return f, f != nil && f.gen == gen
}

func (m *Model) streamOpened(msg streamOpened) tea.Cmd {
	f, ok := m.current(msg.gen)
	switch {
	case !ok:
		if msg.stream != nil {
			msg.stream.Close()
		}
		return nil
	case msg.err != nil:
		return m.broke(msg.err)
	}
	f.stream = msg.stream
	return m.next()
}

func (m *Model) projectEvent(msg projectEvent) tea.Cmd {
	f, ok := m.current(msg.gen)
	switch {
	case !ok:
		return nil
	case msg.err != nil:
		return m.broke(msg.err)
	case msg.event.Removed:
		return m.projectGone()
	}
	p := msg.event.Project
	f.project, f.ref, f.err, f.wait = &p, "id:"+p.Id, nil, 0
	m.terminalsChanged()
	return m.next()
}

// broke opens the stream again after a wait, unless the project is gone.
func (m *Model) broke(err error) tea.Cmd {
	f := m.following
	if f.stream != nil {
		f.stream.Close()
		f.stream = nil
	}
	if e, ok := errors.AsType[*client.ProblemError](err); ok && e.Problem.Code == api.ErrorCodeNotFound {
		return m.projectGone()
	}
	f.err = err
	f.wait = min(max(2*f.wait, firstReopenWait), maxReopenWait)
	m.log.Info("project stream broke", "ref", f.ref, "err", err, "wait_ms", f.wait.Milliseconds())
	gen := f.gen
	return m.tick(f.wait, func(time.Time) tea.Msg { return reopenStream{gen: gen} })
}

// projectGone leaves the project shown, removed elsewhere, for
// [Open Project].
func (m *Model) projectGone() tea.Cmd {
	name := m.shown
	m.log.Info("project shown was removed", "name", name)
	m.unfollow()
	m.shown = ""
	m.layout.terminalOpen = false
	m.stack = []screen{&openProjectScreen{projects: m.projects, notice: name + " was removed"}}
	m.focusProjectPane()
	return m.listProjects()
}

// terminals returns the terminals of the project shown, by name.
func (m *Model) terminals() []api.Terminal {
	if f := m.following; f != nil && f.project != nil && f.project.Terminals != nil {
		return *f.project.Terminals
	}
	return nil
}

// terminalsChanged keeps the tab selected when its terminal is still
// there, or selects the one that took its place.
func (m *Model) terminalsChanged() {
	terms := m.terminals()
	p := m.following.project
	id := m.selectedTab[p.Id]
	for i, t := range terms {
		if t.Id == id {
			m.tab = i
			return
		}
	}
	m.tab = max(min(m.tab, len(terms)-1), 0)
	if len(terms) > 0 {
		m.selectedTab[p.Id] = terms[m.tab].Id
	}
}

// selectTab selects the tab i of the project shown.
func (m *Model) selectTab(i int) {
	terms := m.terminals()
	if i < 0 || i >= len(terms) {
		return
	}
	m.tab = i
	m.selectedTab[m.following.project.Id] = terms[i].Id
	m.log.Debug("tab selected", "terminal", terms[i].Name)
}

// selectedTerminal returns the terminal of the tab selected.
func (m *Model) selectedTerminal() (api.Terminal, bool) {
	terms := m.terminals()
	if m.tab < len(terms) {
		return terms[m.tab], true
	}
	return api.Terminal{}, false
}

// stateMark is a terminal's state as a symbol, which tells it with color
// off too.
func stateMark(st style.Style, state api.TerminalState) string {
	switch state {
	case api.TerminalStateRunning:
		return st.Success("●")
	case api.TerminalStateStarting:
		return st.Warning("◌")
	case api.TerminalStateFailed, api.TerminalStateLost:
		return st.Failure("✕")
	}
	return st.Muted("○") // exited, stopped
}

// terminalPane is what the terminal pane shows: the terminal of the tab
// selected, or why there is none.
type terminalPane struct{ m *Model }

// input is nothing: keys, pastes and the mouse reach the terminal as
// messages (terminalInput).
func (terminalPane) input(string) {}

func (p terminalPane) draw(buf uv.Screen, area uv.Rectangle, st style.Style) {
	m := p.m
	f := m.following
	var lines []string
	if m.terminalNotice != "" {
		lines = append(lines, st.Failure(wrap(m.terminalNotice, area.Dx())))
	}
	t, ok := m.selectedTerminal()
	switch {
	case f == nil || f.project == nil && f.err == nil:
		lines = append(lines, st.Muted("loading…"))
	case f.err != nil:
		// The pane may be three rows: what happens comes first.
		lines = append(lines, st.Failure("the project could not be followed, trying again…"), st.Muted(wrap(f.err.Error(), area.Dx())))
	case !ok:
		lines = append(lines, st.Muted("no terminals: ")+st.Command("C-q t")+st.Muted(" creates one"))
	case m.term != nil && m.term.id == t.Id && m.term.pane != nil:
		p.drawTerminal(buf, area, st, t, lines)
		return
	default:
		lines = append(lines, p.stateLine(st, t), st.Muted(tildePath(t.Dir, m.userHome)), m.attachState(t))
	}
	uv.NewStyledString(strings.Join(lines, "\n")).Draw(buf, area)
}

// drawTerminal draws the terminal t shown: the live screen, or under a
// line saying its state, its history; a notice goes over its top.
func (p terminalPane) drawTerminal(buf uv.Screen, area uv.Rectangle, st style.Style, t api.Terminal, notice []string) {
	a := p.m.term
	screen := area
	if !a.live {
		uv.NewStyledString(p.stateLine(st, t)).Draw(buf, uv.Rect(area.Min.X, area.Min.Y, area.Dx(), 1))
		screen.Min.Y++
	}
	uv.NewStyledString(a.pane.Content()).Draw(buf, screen)
	if len(notice) > 0 {
		uv.NewStyledString(strings.Join(notice, "\n")).Draw(buf, area)
	}
}

// stateLine says the terminal's name and state, and how it ended.
func (p terminalPane) stateLine(st style.Style, t api.Terminal) string {
	line := stateMark(st, t.State) + " " + st.Name(t.Name) + " " + st.Muted(string(t.State))
	switch {
	case t.ExitCode != nil:
		line += st.Muted(fmt.Sprintf(" · exit code %d", *t.ExitCode))
	case t.Error != nil:
		line += " " + st.Failure(*t.Error)
	}
	return line
}
