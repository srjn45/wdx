// Package termview is a Bubble Tea component that shows a terminal attached
// through its holder: a vt of its own, fed by the holder's snapshot and
// output, drawn into a pane, with keys and mouse encoded for the program in
// the terminal. wdx terminal attach runs one full-window; tide runs several
// (docs/ATTACH_VIEW.md).
package termview

import (
	"errors"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/screen"
)

// readSize bounds the output one message carries.
const readSize = 32 << 10

// Model is a pane showing a terminal through a session. Only the program's
// goroutine uses it: the one running Update.
type Model struct {
	session *holder.Session
	screen  *screen.Screen
	resets  int // times the vt failed and started again
	back    scrollback
}

// OutputMsg carries output from the session to the pane that reads it.
type OutputMsg struct {
	pane *Model
	data []byte
}

// EndedMsg tells that the session ended: Exit is how the shell ended, or
// Err why the connection did.
type EndedMsg struct {
	Pane *Model
	Exit *holder.Exit
	Err  error
}

// New returns a pane for s, at the size of its snapshot. What is typed,
// encoded for the program, and the answers to the program's queries go to
// s. fetch fetches the history the pane scrolls back to beyond what it saw;
// nil scrolls through only that.
func New(s *holder.Session, fetch FetchFunc) *Model {
	m := &Model{session: s, back: newScrollback(s.Snapshot.HistoryLines, fetch)}
	// A failed write means the session is gone, which reading it tells.
	m.screen = screen.New(s.Snapshot.Cols, s.Snapshot.Rows, func(p []byte) { _, _ = s.Write(p) })
	return m
}

// Init starts reading the session.
func (m *Model) Init() tea.Cmd {
	return m.read
}

func (m *Model) read() tea.Msg {
	buf := make([]byte, readSize)
	n, err := m.session.Read(buf)
	if n > 0 {
		return OutputMsg{pane: m, data: buf[:n]}
	}
	if errors.Is(err, io.EOF) {
		return EndedMsg{Pane: m, Exit: m.session.Exit()}
	}
	return EndedMsg{Pane: m, Err: err}
}

// Update handles the pane's own messages and the input given to it: keys,
// pastes and mouse events, at the pane's coordinates.
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case OutputMsg:
		if msg.pane != m {
			return nil
		}
		m.write(msg.data)
		return m.read
	case pageMsg:
		if msg.pane != m {
			return nil
		}
		if len(msg.h.Lines) == 0 && msg.err == nil {
			// Nothing kept there: history starts after it, if anywhere.
			m.back.first = max(m.back.first, min(msg.start+pageLines, m.back.next-int64(len(m.back.kept))))
		}
		m.back.fetched(msg)
		m.back.scroll(0)
		return m.fetchNeeded()
	case tea.KeyPressMsg:
		return m.key(msg)
	case tea.PasteMsg:
		m.Live()
		m.screen.Paste(msg.Content)
	case tea.MouseMsg:
		return m.mouse(msg)
	}
	return nil
}

// key scrolls with PageUp and PageDown when the program is on its main
// screen, or while scrolled back; any other key returns to the live
// screen and goes to the program.
func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	if msg.Mod == 0 && (msg.Code == tea.KeyPgUp || msg.Code == tea.KeyPgDown) && (m.Scrolled() || !m.screen.AltScreen()) {
		if msg.Code == tea.KeyPgUp {
			return m.ScrollPage(1)
		}
		return m.ScrollPage(-1)
	}
	m.Live()
	m.screen.SendKey(uv.KeyPressEvent{Text: msg.Text, Mod: msg.Mod, Code: msg.Code, ShiftedCode: msg.ShiftedCode, BaseCode: msg.BaseCode})
	return nil
}

// write feeds output to the screen and keeps what scrolls off it; a vt
// that fails on it starts again empty, and the program's next drawing
// fills it.
func (m *Model) write(p []byte) {
	lines, reset := m.screen.Write(p)
	if reset {
		m.reset()
	}
	m.back.add(lines)
}

// reset counts a vt that failed and started again: what it scrolled off
// before is only in the holder's history.
func (m *Model) reset() {
	m.resets++
	m.back.restart()
}

// mouse passes a mouse event to a program that asked for the mouse.
// Otherwise the wheel scrolls the history over the main screen, and is
// sent as arrow keys over the alternate screen, as terminals do, so that
// less and man scroll with it.
func (m *Model) mouse(msg tea.MouseMsg) tea.Cmd {
	mouse := msg.Mouse()
	ev := uv.Mouse{X: mouse.X, Y: mouse.Y, Button: mouse.Button, Mod: mouse.Mod}
	if m.mouseWanted() {
		switch msg.(type) {
		case tea.MouseClickMsg:
			m.screen.SendMouse(uv.MouseClickEvent(ev))
		case tea.MouseReleaseMsg:
			m.screen.SendMouse(uv.MouseReleaseEvent(ev))
		case tea.MouseWheelMsg:
			m.screen.SendMouse(uv.MouseWheelEvent(ev))
		case tea.MouseMotionMsg:
			m.screen.SendMouse(uv.MouseMotionEvent(ev))
		}
		return nil
	}
	wheel, ok := msg.(tea.MouseWheelMsg)
	if !ok {
		return nil
	}
	if m.Scrolled() || !m.screen.AltScreen() {
		if wheel.Button == tea.MouseWheelDown {
			return m.ScrollUp(-wheelLines)
		}
		return m.ScrollUp(wheelLines)
	}
	key := uv.KeyUp
	if wheel.Button == tea.MouseWheelDown {
		key = uv.KeyDown
	}
	for range wheelLines {
		m.screen.SendKey(uv.KeyPressEvent{Code: key})
	}
	return nil
}

// wheelLines is how far a turn of the wheel scrolls.
const wheelLines = 3

// mouseWanted reports whether the program asked for mouse events.
func (m *Model) mouseWanted() bool {
	for _, mode := range []ansi.Mode{ansi.ModeMouseX10, ansi.ModeMouseNormal, ansi.ModeMouseHighlight, ansi.ModeMouseButtonEvent, ansi.ModeMouseAnyEvent} {
		if m.screen.Mode(mode) {
			return true
		}
	}
	return false
}

// SetSize resizes the pane and the terminal, which the latest client to
// resize sizes.
func (m *Model) SetSize(cols, rows int) error {
	if w, h := m.Size(); cols < 1 || rows < 1 || cols == w && rows == h {
		return nil
	}
	if !m.screen.Resize(cols, rows) {
		m.reset()
	}
	return m.session.Resize(holder.Size{Cols: cols, Rows: rows})
}

// Size is the pane's size.
func (m *Model) Size() (cols, rows int) {
	return m.screen.Size()
}

// Content is what the pane shows, as styled lines: the screen, or the
// history when scrolled back.
func (m *Model) Content() string {
	if m.Scrolled() {
		return m.scrolledContent()
	}
	return m.screen.Render()
}

// Text is what the pane shows as plain lines, without trailing spaces.
func (m *Model) Text() string {
	lines := strings.Split(ansi.Strip(m.Content()), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

// Cursor is where the program's cursor is in the pane, and its shape; nil
// when the program hid it.
func (m *Model) Cursor() *tea.Cursor {
	sc := m.screen.Cursor()
	if sc.Hidden || m.Scrolled() {
		return nil
	}
	c := tea.NewCursor(sc.X, sc.Y)
	c.Blink = sc.Blink
	switch sc.Shape {
	case screen.CursorUnderline:
		c.Shape = tea.CursorUnderline
	case screen.CursorBar:
		c.Shape = tea.CursorBar
	}
	return c
}

// Title is the title the program set, "" until it sets one.
func (m *Model) Title() string {
	return m.screen.Title()
}

// Resets is how many times the vt failed on the output and started again.
func (m *Model) Resets() int {
	return m.resets
}

// Close stops the pane; the session stays open, for its owner to close.
func (m *Model) Close() {
	m.screen.Close()
}
