// Package screen keeps a terminal's screen as its output draws it, in a
// headless vt: the holder's copy of each terminal (docs/ATTACH.md), and
// each attached pane's (docs/ATTACH_VIEW.md). It is the only package that
// uses the vt, whose bugs it works around (docs/VT.md).
package screen

import (
	"fmt"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// Screen is a terminal's screen. It is not safe for concurrent use.
type Screen struct {
	vt    *vt.Emulator
	input func([]byte)
	// cursorStyle is the DECSCUSR parameter the output last set, nil until
	// it sets one: the vt has a style either way.
	cursorStyle *int
	// scrolled holds the lines harvested from the vt's scrollback during a
	// write.
	scrolled []string
}

const (
	// writePiece bounds what the vt parses between two harvests. A piece
	// pushes at most a screen's height of lines per 4 bytes (ED 2), so with
	// screens up to 256 rows it never overflows scrollbackLines.
	writePiece      = 1 << 10
	scrollbackLines = 1 << 16
)

// New returns an empty screen of cols×rows. What the vt sends the program
// goes to input, called from another goroutine: its answers to the
// program's queries (cursor position, device attributes), and the keys,
// mouse events and pastes given to the screen. nil drops it.
func New(cols, rows int, input func([]byte)) *Screen {
	s := &Screen{input: input}
	s.Reset(cols, rows)
	return s
}

// Reset empties the screen, at cols×rows.
func (s *Screen) Reset(cols, rows int) {
	if s.vt != nil {
		s.Close()
	}
	e := vt.NewEmulator(cols, rows)
	e.SetScrollbackSize(scrollbackLines)
	s.vt = e
	s.cursorStyle = nil
	e.SetCallbacks(vt.Callbacks{
		CursorStyle: func(style vt.CursorStyle, blink bool) {
			p := int(style)*2 + 1
			if !blink {
				p++
			}
			s.cursorStyle = &p
		},
	})
	// Handlers registered last run first; returning false hands the
	// sequence on to the vt's own.
	e.RegisterCsiHandler('J', func(params ansi.Params) bool {
		// ED 3 clears the vt's scrollback: harvest it first, since history
		// keeps what scrolled off however a program clears.
		if n, _, _ := params.Param(0, 0); n == 3 {
			s.harvest()
		}
		return false
	})
	// The vt writes to a pipe and blocks until it is read.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := e.Read(buf)
			if n > 0 && s.input != nil {
				s.input(append([]byte(nil), buf[:n]...))
			}
			if err != nil {
				return
			}
		}
	}()
}

// current is the vt's screen that shows.
func (s *Screen) current() *vt.Screen {
	if s.vt.IsAltScreen() {
		return s.vt.AltScreen()
	}
	return s.vt.MainScreen()
}

// region is the scroll region of the screen that shows.
func (s *Screen) region() uv.Rectangle {
	return s.current().ScrollRegion()
}

// FailOn makes the vt panic on the CSI sequence ending in final until the
// screen is reset, as on input it fails on, which no known input does: for
// tests of what users of the screen do after a reset.
func (s *Screen) FailOn(final byte) {
	s.vt.RegisterCsiHandler(int(final), func(ansi.Params) bool { panic("the vt failed") })
}

// Write feeds output to the screen and returns the lines it scrolled off
// the main screen, oldest first, each rendered with its styles and links.
// reset is true when the vt failed on the output: the screen was reset to
// empty, and lines may be missing before it.
func (s *Screen) Write(p []byte) (lines []string, reset bool) {
	for len(p) > 0 {
		n := min(len(p), writePiece)
		if !s.feed(p[:n]) {
			reset = true
			s.feedBytes(p[:n])
		}
		p = p[n:]
	}
	lines, s.scrolled = s.scrolled, nil
	return lines, reset
}

// feed writes one piece to the vt and harvests it; false means the vt
// panicked and was replaced by an empty one.
func (s *Screen) feed(p []byte) (ok bool) {
	defer func() {
		if recover() != nil {
			s.Reset(s.vt.Width(), s.vt.Height())
			ok = false
		}
	}()
	_, _ = s.vt.Write(p)
	s.harvest()
	return true
}

// feedBytes writes a piece the vt failed on again, a byte at a time: what
// follows the sequence it fails on is drawn, not lost with the piece.
func (s *Screen) feedBytes(p []byte) {
	for i := range p {
		s.feed(p[i : i+1])
	}
}

// harvest moves the vt's scrollback into scrolled.
func (s *Screen) harvest() {
	sb := s.vt.Scrollback()
	lines := sb.Lines()
	for i, line := range lines {
		s.scrolled = append(s.scrolled, line.Render())
		lines[i] = nil // Clear keeps the array, which would keep the cells
	}
	sb.Clear()
}

// Lines renders the main screen's lines, styles and links included, up to
// its last that is not blank; none while the alternate screen shows, which
// never goes to history.
func (s *Screen) Lines() []string {
	if s.vt.IsAltScreen() {
		return nil
	}
	var lines []string
	line := make(uv.Line, s.vt.Width())
	for y := range s.vt.Height() {
		row(line, s.vt.MainScreen(), y)
		lines = append(lines, trimmed(line).Render())
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Text is the plain text of the lines of the screen that shows, alternate or
// main, without styles or links and without trailing spaces, up to its last
// line that is not blank.
func (s *Screen) Text() []string {
	lines := s.lines(false)
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Undimmed is Text with the dim cells blank, line for line: every row of the
// screen, so an index into Text is one here.
func (s *Screen) Undimmed() []string {
	return s.lines(true)
}

// lines reads the cells where they are, not through a copy of each as uv.Line
// does: this runs under the holder's screen lock, on every read of a program
// that keeps moving.
func (s *Screen) lines(blankDim bool) []string {
	scr := s.current()
	width := s.vt.Width()
	lines := make([]string, 0, s.vt.Height())
	var line strings.Builder
	for y := range s.vt.Height() {
		line.Reset()
		for x := range width {
			c := scr.CellAt(x, y)
			switch {
			case c == nil:
			case blankDim && c.Style.Attrs&uv.AttrFaint != 0:
				line.WriteByte(' ')
			default:
				line.WriteString(c.Content)
			}
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
	}
	return lines
}

// row copies row y of scr into line.
func row(line uv.Line, scr *vt.Screen, y int) {
	for x := range line {
		line[x] = uv.Cell{}
		if c := scr.CellAt(x, y); c != nil {
			line[x] = *c
		}
	}
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

// Resize changes the screen's size; false means the vt panicked and was
// replaced by an empty screen of the new size.
func (s *Screen) Resize(cols, rows int) (ok bool) {
	defer func() {
		if recover() != nil {
			s.Reset(cols, rows)
			ok = false
		}
	}()
	s.vt.Resize(cols, rows)
	return true
}

// Size is the screen's size.
func (s *Screen) Size() (cols, rows int) {
	return s.vt.Width(), s.vt.Height()
}

// Render is the screen as styled lines.
func (s *Screen) Render() string {
	return s.vt.Render()
}

// CellAt is the cell at x, y; nil outside the screen.
func (s *Screen) CellAt(x, y int) *uv.Cell {
	return s.vt.CellAt(x, y)
}

// Title is the title the output set, "" until it sets one.
func (s *Screen) Title() string {
	return s.vt.Title()
}

// Mode reports whether mode m is set.
func (s *Screen) Mode(m ansi.Mode) bool {
	return s.vt.Modes()[m].IsSet()
}

// AltScreen reports whether the alternate screen shows.
func (s *Screen) AltScreen() bool {
	return s.vt.IsAltScreen()
}

// Cursor is where the screen's cursor is and how it looks.
type Cursor struct {
	X, Y   int
	Hidden bool
	Shape  CursorShape
	Blink  bool
}

// CursorShape is the shape a program gave the cursor.
type CursorShape int

const (
	CursorBlock CursorShape = iota
	CursorUnderline
	CursorBar
)

// Cursor is the screen's cursor.
func (s *Screen) Cursor() Cursor {
	p := s.vt.CursorPosition()
	style, blink := s.vt.CursorStyle()
	shape := CursorBlock
	switch style {
	case vt.CursorUnderline:
		shape = CursorUnderline
	case vt.CursorBar:
		shape = CursorBar
	}
	return Cursor{X: p.X, Y: p.Y, Hidden: s.vt.CursorHidden(), Shape: shape, Blink: blink}
}

// SendKey sends a key to the program, encoded as the modes it set ask.
func (s *Screen) SendKey(k uv.KeyPressEvent) {
	s.vt.SendKey(k)
}

// SendMouse sends a mouse event to the program, encoded as the modes it
// set ask.
func (s *Screen) SendMouse(m uv.MouseEvent) {
	s.vt.SendMouse(m)
}

// Paste sends text to the program as a paste, bracketed when it asked.
func (s *Screen) Paste(text string) {
	s.vt.Paste(text)
}

// Snapshot returns the bytes that paint the screen on a terminal of its
// size: a soft reset, the main screen's cells and cursor, then, when it
// shows, the alternate screen's, so that leaving it shows the main screen;
// the modes the output set, the margins, the cursor with its pen and link,
// the character sets and the title. The character set GR invokes is not
// carried: it applies only to bytes UTF-8 output never sends alone.
func (s *Screen) Snapshot() []byte {
	e := s.vt
	var b strings.Builder
	b.WriteString("\x1b[!p\x1b[H\x1b[2J") // soft reset, home, clear
	paint(&b, e.MainScreen())
	if e.IsAltScreen() {
		// Entering the alternate screen saves the main screen's cursor,
		// which leaving it restores.
		pen(&b, e.MainScreen().Cursor(), uv.Position{})
		b.WriteString("\x1b[?1049h\x1b[0m\x1b]8;;\x07\x1b[H\x1b[2J")
		paint(&b, e.AltScreen())
	}
	modes := e.Modes()
	for mode, setting := range modes {
		if skipMode(mode) || !setting.IsSet() && !setting.IsReset() {
			continue
		}
		b.WriteString(modeSequence(mode, setting.IsSet()))
	}
	r := s.region()
	if r.Min.Y != 0 || r.Max.Y != e.Height() {
		fmt.Fprintf(&b, "\x1b[%d;%dr", r.Min.Y+1, r.Max.Y)
	}
	if r.Min.X != 0 || r.Max.X != e.Width() {
		fmt.Fprintf(&b, "\x1b[%d;%ds", r.Min.X+1, r.Max.X)
	}
	if s.cursorStyle != nil {
		fmt.Fprintf(&b, "\x1b[%d q", *s.cursorStyle)
	}
	var origin uv.Position
	if modes[ansi.ModeOrigin].IsSet() {
		origin = r.Min // the cursor moves within the margins
	}
	pen(&b, s.current().Cursor(), origin)
	if e.CursorHidden() {
		b.WriteString("\x1b[?25l")
	}
	charSets(&b, e)
	if t := e.Title(); t != "" {
		b.WriteString(ansi.SetWindowTitle(t))
	}
	return []byte(b.String())
}

// paint writes the cells of scr, a vt screen of the screen's size.
func paint(b *strings.Builder, scr *vt.Screen) {
	line := make(uv.Line, scr.Width())
	for y := range scr.Height() {
		row(line, scr, y)
		fmt.Fprintf(b, "\x1b[%d;1H", y+1)
		b.WriteString(line.Render())
	}
}

// pen moves the cursor to where c is, relative to origin, and sets its pen
// and link: what the program writes next is drawn with them.
func pen(b *strings.Builder, c vt.Cursor, origin uv.Position) {
	fmt.Fprintf(b, "\x1b[%d;%dH", c.Y-origin.Y+1, c.X-origin.X+1)
	b.WriteString(c.Pen.String())
	if c.Link.URL != "" {
		b.WriteString(ansi.SetHyperlink(c.Link.URL, c.Link.Params))
	}
}

// charSets designates the character sets that are not USASCII, and invokes
// the one GL holds.
func charSets(b *strings.Builder, e *vt.Emulator) {
	sets, gl, _ := e.CharSets()
	for g, set := range sets {
		if set != 'B' {
			fmt.Fprintf(b, "\x1b%c%c", "()*+"[g], set)
		}
	}
	switch gl {
	case 1:
		b.WriteString("\x0e") // SO
	case 2:
		b.WriteString("\x1bn") // LS2
	case 3:
		b.WriteString("\x1bo") // LS3
	}
}

// skipMode reports the modes the snapshot does not set as such: it enters
// the alternate screen and hides the cursor itself, and a snapshot is a
// whole frame, never inside a synchronized update.
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

// Close ends the goroutine reading what the vt sends; the screen cannot be
// used after.
func (s *Screen) Close() {
	_ = s.vt.Close()
}
