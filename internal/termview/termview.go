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
//
// The pane has an area, and the terminal a size, which the latest client
// to attach or resize sets: the screen is drawn at the terminal's size,
// cropped or padded to the area, until input to the program takes the
// size back.
//
// A pane made by NewHistory has neither session nor screen: it shows a
// terminal that is not running, from its history, read-only.
type Model struct {
	session    *holder.Session // nil for history alone
	screen     *screen.Screen  // nil for history alone
	cols, rows int             // the pane's area
	asked      *holder.Size    // the size sent, until the holder tells it back
	resets     int             // times the vt failed and started again
	back       scrollback
}

// OutputMsg carries output from the session to the pane that reads it.
type OutputMsg struct {
	pane *Model
	data []byte
	size holder.Size // the terminal's, which the output is drawn at
}

// EndedMsg tells that the session ended: Exit is how the shell ended, or
// Err why the connection did.
type EndedMsg struct {
	Pane *Model
	Exit *holder.Exit
	Err  error
}

// New returns a pane for s, its area the size of its snapshot. What is typed,
// encoded for the program, and the answers to the program's queries go to
// s. fetch fetches the history the pane scrolls back to beyond what it saw;
// nil scrolls through only that.
func New(s *holder.Session, fetch FetchFunc) *Model {
	m := &Model{session: s, cols: s.Snapshot.Cols, rows: s.Snapshot.Rows, back: newScrollback(s.Snapshot.HistoryLines, fetch)}
	// A failed write means the session is gone, which reading it tells.
	m.screen = screen.New(s.Snapshot.Cols, s.Snapshot.Rows, func(p []byte) { _, _ = s.Write(p) })
	return m
}

// NewHistory returns a pane showing history alone, read-only, for a
// terminal that is not running: tail is the end of its history, which the
// pane shows at its bottom, and fetch fetches what is above. Keys and
// mouse only scroll it.
func NewHistory(tail History, cols, rows int, fetch FetchFunc) *Model {
	m := &Model{cols: cols, rows: rows, back: newScrollback(tail.From+int64(len(tail.Lines)), fetch)}
	m.back.kept = tail.Lines
	m.back.first = tail.First
	m.Live()
	return m
}

// Init starts reading the session; a pane of history alone fetches what it
// shows and does not have.
func (m *Model) Init() tea.Cmd {
	if m.session == nil {
		return m.fetchNeeded()
	}
	return m.read
}

func (m *Model) read() tea.Msg {
	buf := make([]byte, readSize)
	n, err := m.session.Read(buf)
	if n > 0 {
		return OutputMsg{pane: m, data: buf[:n], size: m.session.Size()}
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
		m.follow(msg.size)
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
		m.back.scroll(0, m.bottom())
		if msg.err != nil {
			return nil // the next scroll tries again
		}
		return m.fetchNeeded()
	case tea.KeyPressMsg:
		return m.key(msg)
	case tea.PasteMsg:
		if m.session == nil {
			return nil
		}
		m.Live()
		m.takeSize()
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
	if msg.Mod == 0 && (msg.Code == tea.KeyPgUp || msg.Code == tea.KeyPgDown) && (m.session == nil || m.Scrolled() || !m.screen.AltScreen()) {
		if msg.Code == tea.KeyPgUp {
			return m.ScrollPage(1)
		}
		return m.ScrollPage(-1)
	}
	if m.session == nil {
		return nil
	}
	m.Live()
	m.takeSize()
	m.screen.SendKey(uv.KeyPressEvent{Text: msg.Text, Mod: msg.Mod, Code: msg.Code, ShiftedCode: msg.ShiftedCode, BaseCode: msg.BaseCode})
	return nil
}

// write feeds output to the screen and keeps what scrolls off it; a vt
// that fails on it starts again empty, and the program's next drawing
// fills it.
func (m *Model) write(p []byte) {
	lines, reset := m.screen.Write(p)
	m.back.add(lines)
	if reset {
		m.reset()
	}
}

// follow resizes the screen to the terminal's size, as another client set
// it; the sizes the holder tells before the one the pane asked for are
// past.
func (m *Model) follow(size holder.Size) {
	if m.asked != nil {
		if size != *m.asked {
			return
		}
		m.asked = nil
	}
	m.resize(size)
}

// takeSize makes the pane's area the terminal's size again, as input to
// the program does after another client resized it.
func (m *Model) takeSize() {
	// A size that cannot be sent means the session is gone, which reading
	// it tells.
	_ = m.askSize()
}

func (m *Model) askSize() error {
	size := holder.Size{Cols: m.cols, Rows: m.rows}
	if w, h := m.screen.Size(); w == size.Cols && h == size.Rows {
		return nil
	}
	m.resize(size)
	m.asked = &size
	return m.session.Resize(size)
}

func (m *Model) resize(size holder.Size) {
	if w, h := m.screen.Size(); w == size.Cols && h == size.Rows {
		return
	}
	if !m.screen.Resize(size.Cols, size.Rows) {
		m.reset()
	}
}

// reset marks where the vt failed and started again, with the line the
// holder writes into history then.
func (m *Model) reset() {
	m.resets++
	m.back.add([]string{holder.MarkInterrupted})
}

// mouse passes a mouse event to a program that asked for the mouse.
// Otherwise the wheel scrolls the history over the main screen, and is
// sent as arrow keys over the alternate screen, as terminals do, so that
// less and man scroll with it.
func (m *Model) mouse(msg tea.MouseMsg) tea.Cmd {
	mouse := msg.Mouse()
	ev := uv.Mouse{X: mouse.X, Y: mouse.Y, Button: mouse.Button, Mod: mouse.Mod}
	if m.session != nil && m.MouseWanted() {
		m.takeSize()
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
	if m.session == nil || m.Scrolled() || !m.screen.AltScreen() {
		if wheel.Button == tea.MouseWheelDown {
			return m.ScrollUp(-wheelLines)
		}
		return m.ScrollUp(wheelLines)
	}
	key := uv.KeyUp
	if wheel.Button == tea.MouseWheelDown {
		key = uv.KeyDown
	}
	m.takeSize()
	for range wheelLines {
		m.screen.SendKey(uv.KeyPressEvent{Code: key})
	}
	return nil
}

// wheelLines is how far a turn of the wheel scrolls.
const wheelLines = 3

// MouseWanted reports whether the program asked for mouse events.
func (m *Model) MouseWanted() bool {
	if m.screen == nil {
		return false
	}
	for _, mode := range []ansi.Mode{ansi.ModeMouseX10, ansi.ModeMouseNormal, ansi.ModeMouseHighlight, ansi.ModeMouseButtonEvent, ansi.ModeMouseAnyEvent} {
		if m.screen.Mode(mode) {
			return true
		}
	}
	return false
}

// SetSize sets the pane's area, and makes it the terminal's size.
func (m *Model) SetSize(cols, rows int) error {
	if cols < 1 || rows < 1 || cols == m.cols && rows == m.rows {
		return nil
	}
	m.cols, m.rows = cols, rows
	if m.session == nil {
		// Kept at the bottom, or on the lines it shows.
		m.back.scroll(0, m.bottom())
		return nil
	}
	return m.askSize()
}

// Size is the pane's area.
func (m *Model) Size() (cols, rows int) {
	return m.cols, m.rows
}

// TerminalSize is the terminal's size, which another client may have set.
func (m *Model) TerminalSize() (cols, rows int) {
	if m.screen == nil {
		return m.cols, m.rows
	}
	return m.screen.Size()
}

// Content is what the pane shows, as styled lines: the screen, or the
// history when scrolled back.
func (m *Model) Content() string {
	if m.Scrolled() || m.session == nil {
		return m.scrolledContent()
	}
	return m.fit(m.screen.Render())
}

// fit crops or pads a rendered screen to the pane's area.
func (m *Model) fit(rendered string) string {
	if w, h := m.screen.Size(); w == m.cols && h == m.rows {
		return rendered
	}
	lines := strings.Split(rendered, "\n")
	fitted := make([]string, m.rows)
	for i := range fitted {
		if i < len(lines) {
			fitted[i] = ansi.Truncate(lines[i], m.cols, "")
		}
	}
	return strings.Join(fitted, "\n")
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
	if m.screen == nil {
		return nil
	}
	sc := m.screen.Cursor()
	if sc.Hidden || m.Scrolled() || sc.X >= m.cols || sc.Y >= m.rows {
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
	if m.screen == nil {
		return ""
	}
	return m.screen.Title()
}

// Resets is how many times the vt failed on the output and started again.
func (m *Model) Resets() int {
	return m.resets
}

// Close stops the pane; the session stays open, for its owner to close.
func (m *Model) Close() {
	if m.screen != nil {
		m.screen.Close()
	}
}
