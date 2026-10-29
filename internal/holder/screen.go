package holder

import (
	"fmt"
	"io"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// screen is the terminal's screen as the shell's output drew it, kept by a
// headless vt: what an attaching client is shown first, and where the
// history's lines come from. Only screen uses the vt (docs/ATTACH.md). It is
// not safe for concurrent use.
type screen struct {
	vt     *vt.Emulator
	answer func([]byte)
	// What the vt has no getter for, tracked through its callbacks and
	// handlers.
	modes       map[ansi.Mode]bool // as the output last set them
	title       string
	cursorStyle *int // the DECSCUSR parameter, nil until the output sets one
	margins     [2]margins
	// scrolled holds the lines harvested from the vt's scrollback during a
	// write.
	scrolled []string
}

// margins are a screen's top and bottom margins, 0-based and exclusive at
// the bottom; zero values are the whole screen.
type margins struct {
	top, bottom int
}

const (
	// writePiece bounds what the vt parses between two harvests. A piece
	// pushes at most a screen's height of lines per 4 bytes (ED 2), so with
	// screens up to 256 rows it never overflows scrollbackLines.
	writePiece      = 1 << 10
	scrollbackLines = 1 << 16
)

// newScreen returns an empty screen of cols×rows. The vt answers programs'
// queries (cursor position, device attributes) through answer, called from
// another goroutine; nil drops the answers.
func newScreen(cols, rows int, answer func([]byte)) *screen {
	s := &screen{answer: answer}
	s.reset(cols, rows)
	return s
}

func (s *screen) reset(cols, rows int) {
	if s.vt != nil {
		s.close()
	}
	e := vt.NewEmulator(cols, rows)
	e.SetScrollbackSize(scrollbackLines)
	s.vt = e
	s.modes = map[ansi.Mode]bool{}
	s.title = ""
	s.cursorStyle = nil
	s.margins = [2]margins{}
	e.SetCallbacks(vt.Callbacks{
		EnableMode:  func(m ansi.Mode) { s.modes[m] = true },
		DisableMode: func(m ansi.Mode) { s.modes[m] = false },
		Title:       func(t string) { s.title = t },
		CursorStyle: func(vt.CursorStyle, bool) {
			// Read back: the callback is given "steady" as its blink.
			style, blink := e.CursorStyle()
			p := int(style)*2 + 1
			if !blink {
				p++
			}
			s.cursorStyle = &p
		},
	})
	// Handlers registered last run first; returning false hands the
	// sequence on to the vt's own.
	e.RegisterCsiHandler('r', func(params ansi.Params) bool {
		s.setMargins(params)
		return false
	})
	e.RegisterEscHandler('c', func() bool {
		s.margins = [2]margins{}
		return false
	})
	e.RegisterCsiHandler('J', func(params ansi.Params) bool {
		// ED 3 clears the vt's scrollback: harvest it first, since history
		// keeps what scrolled off however a program clears.
		if n, _, _ := params.Param(0, 0); n == 3 {
			s.harvest()
		}
		return false
	})
	// The vt writes answers to a pipe and blocks until they are read.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := e.Read(buf)
			if n > 0 && s.answer != nil {
				s.answer(append([]byte(nil), buf[:n]...))
			}
			if err != nil {
				return
			}
		}
	}()
}

// setMargins tracks DECSTBM as the vt applies it, to the current screen.
func (s *screen) setMargins(params ansi.Params) {
	rows := s.vt.Height()
	top, _, _ := params.Param(0, 1)
	top = max(top, 1)
	bottom, _, _ := params.Param(1, rows)
	if bottom < 1 {
		bottom = rows
	}
	if top >= bottom {
		return // the vt ignores it too
	}
	m := margins{top: top - 1, bottom: min(bottom, rows)}
	if m.top == 0 && m.bottom == rows {
		m = margins{}
	}
	s.margins[s.current()] = m
}

func (s *screen) current() int {
	if s.vt.IsAltScreen() {
		return 1
	}
	return 0
}

// write feeds the shell's output to the screen and returns the lines it
// scrolled off the main screen, oldest first, each rendered with its styles
// and links. reset is true when the vt failed on the output: the screen was
// reset to empty, and lines may be missing before it.
func (s *screen) write(p []byte) (lines []string, reset bool) {
	for len(p) > 0 {
		n := min(len(p), writePiece)
		if !s.feed(p[:n]) {
			reset = true
		}
		p = p[n:]
	}
	lines, s.scrolled = s.scrolled, nil
	return lines, reset
}

// feed writes one piece to the vt and harvests it; false means the vt
// panicked and was replaced by an empty one.
func (s *screen) feed(p []byte) (ok bool) {
	defer func() {
		if recover() != nil {
			s.reset(s.vt.Width(), s.vt.Height())
			ok = false
		}
	}()
	_, _ = s.vt.Write(p)
	s.harvest()
	return true
}

// harvest moves the vt's scrollback into scrolled.
func (s *screen) harvest() {
	sb := s.vt.Scrollback()
	lines := sb.Lines()
	for i, line := range lines {
		s.scrolled = append(s.scrolled, line.Render())
		lines[i] = nil // Clear keeps the array, which would keep the cells
	}
	sb.Clear()
}

// lines renders the main screen's lines, styles and links included, up to
// its last that is not blank; none while the alternate screen shows, which
// never goes to history.
func (s *screen) lines() []string {
	if s.vt.IsAltScreen() {
		return nil
	}
	var lines []string
	line := make(uv.Line, s.vt.Width())
	for y := range s.vt.Height() {
		for x := range line {
			line[x] = uv.Cell{}
			if c := s.vt.CellAt(x, y); c != nil {
				line[x] = *c
			}
		}
		lines = append(lines, trimmed(line).Render())
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// trimmed is line without its trailing blank cells, as the vt keeps lines
// in its scrollback.
func trimmed(line uv.Line) uv.Line {
	end := len(line)
	for end > 0 && (line[end-1].IsZero() || line[end-1].Equal(&uv.EmptyCell)) {
		end--
	}
	return line[:end]
}

// resize changes the screen's size; false means the vt panicked and was
// replaced by an empty screen of the new size.
func (s *screen) resize(cols, rows int) (ok bool) {
	defer func() {
		if recover() != nil {
			s.reset(cols, rows)
			ok = false
		}
	}()
	s.vt.Resize(cols, rows)
	s.margins = [2]margins{} // the vt resets both screens' margins
	return true
}

// snapshot returns the bytes that paint the screen on a terminal of its
// size: a soft reset, the cells, the margins, the modes the output set, the
// cursor and the title. The pen, character sets and left/right margins are
// not carried: programs set them again before they draw.
func (s *screen) snapshot() []byte {
	e := s.vt
	var b strings.Builder
	b.WriteString("\x1b[!p\x1b[H\x1b[2J") // soft reset, home, clear
	if e.IsAltScreen() {
		b.WriteString("\x1b[?1049h\x1b[H\x1b[2J")
	}
	line := make(uv.Line, e.Width())
	for y := range e.Height() {
		for x := range line {
			line[x] = uv.Cell{}
			if c := e.CellAt(x, y); c != nil {
				line[x] = *c
			}
		}
		fmt.Fprintf(&b, "\x1b[%d;1H", y+1)
		b.WriteString(line.Render())
	}
	m := s.margins[s.current()]
	if m != (margins{}) {
		fmt.Fprintf(&b, "\x1b[%d;%dr", m.top+1, m.bottom)
	}
	for mode, on := range s.modes {
		if skipMode(mode) {
			continue
		}
		b.WriteString(modeSequence(mode, on))
	}
	if s.cursorStyle != nil {
		fmt.Fprintf(&b, "\x1b[%d q", *s.cursorStyle)
	}
	p := e.CursorPosition()
	if s.modes[ansi.ModeOrigin] {
		p.Y -= m.top // the cursor moves within the margins
	}
	fmt.Fprintf(&b, "\x1b[%d;%dH", p.Y+1, p.X+1)
	if e.CursorHidden() {
		b.WriteString("\x1b[?25l")
	}
	if s.title != "" {
		b.WriteString(ansi.SetWindowTitle(s.title))
	}
	return []byte(b.String())
}

// skipMode reports the modes the snapshot does not set from the tracked
// ones: the alternate screen and the cursor's visibility come from the vt,
// and a snapshot is a whole frame, never inside a synchronized update.
func skipMode(m ansi.Mode) bool {
	switch m {
	case ansi.ModeAltScreen, ansi.ModeSaveCursor, ansi.ModeAltScreenSaveCursor,
		ansi.ModeTextCursorEnable, ansi.ModeSynchronizedOutput:
		return true
	}
	return false
}

func modeSequence(m ansi.Mode, on bool) string {
	final := 'l'
	if on {
		final = 'h'
	}
	if _, ok := m.(ansi.DECMode); ok {
		return fmt.Sprintf("\x1b[?%d%c", m.Mode(), final)
	}
	return fmt.Sprintf("\x1b[%d%c", m.Mode(), final)
}

// close ends the goroutine reading the vt's answers; the screen cannot be
// used after.
func (s *screen) close() {
	// Closing the pipe's writer rather than the vt: Close sets a field that
	// the reading goroutine reads without a lock.
	if w, ok := s.vt.InputPipe().(io.Closer); ok {
		_ = w.Close()
		return
	}
	_ = s.vt.Close()
}
