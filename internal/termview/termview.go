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
	"github.com/charmbracelet/x/vt"

	"github.com/srjn45/wdx/internal/holder"
)

// readSize bounds the output one message carries.
const readSize = 32 << 10

// Model is a pane showing a terminal through a session. Only the program's
// goroutine uses it: the one running Update.
type Model struct {
	session *holder.Session
	vt      *vt.Emulator
	modes   map[ansi.Mode]bool // the vt has no getter for them
	title   string
	resets  int // times the vt failed and started again
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

// New returns a pane for s, at the size of its snapshot. The vt's input,
// what is typed encoded for the program and its answers to the program's
// queries, goes to s.
func New(s *holder.Session) *Model {
	m := &Model{session: s}
	m.start(s.Snapshot.Cols, s.Snapshot.Rows)
	return m
}

func (m *Model) start(cols, rows int) {
	e := vt.NewEmulator(cols, rows)
	m.vt = e
	m.modes = map[ansi.Mode]bool{}
	e.SetCallbacks(vt.Callbacks{
		EnableMode:  func(mode ansi.Mode) { m.modes[mode] = true },
		DisableMode: func(mode ansi.Mode) { m.modes[mode] = false },
		Title:       func(t string) { m.title = t },
	})
	// The vt writes input to a pipe and blocks until it is read; a failed
	// write means the session is gone, which reading it tells.
	go func() { _, _ = io.Copy(m.session, e) }()
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
	case tea.KeyPressMsg:
		m.vt.SendKey(uv.KeyPressEvent{Text: msg.Text, Mod: msg.Mod, Code: msg.Code, ShiftedCode: msg.ShiftedCode, BaseCode: msg.BaseCode})
	case tea.PasteMsg:
		m.vt.Paste(msg.Content)
	case tea.MouseMsg:
		m.mouse(msg)
	}
	return nil
}

// write feeds output to the vt; one that fails on it starts again empty,
// and the program's next drawing fills it.
func (m *Model) write(p []byte) {
	defer func() {
		if recover() != nil {
			m.resets++
			m.restart()
		}
	}()
	_, _ = m.vt.Write(p)
}

func (m *Model) restart() {
	cols, rows := m.vt.Width(), m.vt.Height()
	m.close()
	m.start(cols, rows)
}

// mouse passes a mouse event to a program that asked for the mouse; on the
// alternate screen the wheel is sent as arrow keys otherwise, as terminals
// do, so that less and man scroll with it.
func (m *Model) mouse(msg tea.MouseMsg) {
	mouse := msg.Mouse()
	ev := uv.Mouse{X: mouse.X, Y: mouse.Y, Button: mouse.Button, Mod: mouse.Mod}
	if m.mouseWanted() {
		switch msg.(type) {
		case tea.MouseClickMsg:
			m.vt.SendMouse(uv.MouseClickEvent(ev))
		case tea.MouseReleaseMsg:
			m.vt.SendMouse(uv.MouseReleaseEvent(ev))
		case tea.MouseWheelMsg:
			m.vt.SendMouse(uv.MouseWheelEvent(ev))
		case tea.MouseMotionMsg:
			m.vt.SendMouse(uv.MouseMotionEvent(ev))
		}
		return
	}
	wheel, ok := msg.(tea.MouseWheelMsg)
	if !ok || !m.vt.IsAltScreen() {
		return
	}
	key := uv.KeyUp
	if wheel.Button == tea.MouseWheelDown {
		key = uv.KeyDown
	}
	for range wheelLines {
		m.vt.SendKey(uv.KeyPressEvent{Code: key})
	}
}

// wheelLines is how far a turn of the wheel scrolls.
const wheelLines = 3

// mouseWanted reports whether the program asked for mouse events.
func (m *Model) mouseWanted() bool {
	for _, mode := range []ansi.Mode{ansi.ModeMouseX10, ansi.ModeMouseNormal, ansi.ModeMouseHighlight, ansi.ModeMouseButtonEvent, ansi.ModeMouseAnyEvent} {
		if m.modes[mode] {
			return true
		}
	}
	return false
}

// SetSize resizes the pane and the terminal, which the latest client to
// resize sizes.
func (m *Model) SetSize(cols, rows int) error {
	if cols < 1 || rows < 1 || cols == m.vt.Width() && rows == m.vt.Height() {
		return nil
	}
	func() {
		defer func() {
			if recover() != nil {
				m.resets++
				m.close()
				m.start(cols, rows)
			}
		}()
		m.vt.Resize(cols, rows)
	}()
	return m.session.Resize(holder.Size{Cols: cols, Rows: rows})
}

// Size is the pane's size.
func (m *Model) Size() (cols, rows int) {
	return m.vt.Width(), m.vt.Height()
}

// Content is the pane's screen as styled lines.
func (m *Model) Content() string {
	return m.vt.Render()
}

// Text is the pane's screen as plain lines, without trailing spaces.
func (m *Model) Text() string {
	lines := strings.Split(m.vt.String(), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

// Cursor is where the program's cursor is in the pane, and its shape; nil
// when the program hid it.
func (m *Model) Cursor() *tea.Cursor {
	if m.vt.CursorHidden() {
		return nil
	}
	p := m.vt.CursorPosition()
	c := tea.NewCursor(p.X, p.Y)
	style, blink := m.vt.CursorStyle()
	c.Blink = blink
	switch style {
	case vt.CursorUnderline:
		c.Shape = tea.CursorUnderline
	case vt.CursorBar:
		c.Shape = tea.CursorBar
	}
	return c
}

// Title is the title the program set, "" until it sets one.
func (m *Model) Title() string {
	return m.title
}

// Resets is how many times the vt failed on the output and started again.
func (m *Model) Resets() int {
	return m.resets
}

// Close stops the pane; the session stays open, for its owner to close.
func (m *Model) Close() {
	m.close()
}

// close ends the goroutine copying the vt's input to the session.
func (m *Model) close() {
	// Closing the pipe's writer rather than the vt: Close sets a field that
	// the reading goroutine reads without a lock.
	if w, ok := m.vt.InputPipe().(io.Closer); ok {
		_ = w.Close()
		return
	}
	_ = m.vt.Close()
}
