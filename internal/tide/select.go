package tide

import (
	"context"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// selection is text selected by dragging the mouse inside a pane, which it
// never leaves: the terminal's own selection (Shift-drag) crosses the
// panes, as it knows nothing of them.
type selection struct {
	area     uv.Rectangle // the inside of the pane pressed in
	from, to uv.Position  // where the drag started and where it is, in the window
	dragged  bool         // the pointer left the cell pressed: something is selected
	done     bool         // the button was released
}

// press starts a selection at a press inside a pane.
func (m *Model) press(area uv.Rectangle, at uv.Position) {
	m.sel = &selection{area: area, from: at, to: at}
}

// drag extends the selection to the pointer, kept inside its pane.
func (m *Model) drag(mouse tea.Mouse) {
	s := m.sel
	if s == nil || s.done || mouse.Button != tea.MouseLeft {
		return
	}
	s.to = s.clamp(uv.Pos(mouse.X, mouse.Y))
	s.dragged = s.dragged || s.to != s.from
}

// release ends the selection and copies its text, as terminals do on Linux.
// A release where the press was selects nothing: it was a click.
func (m *Model) release(mouse tea.Mouse) tea.Cmd {
	s := m.sel
	if s == nil || s.done {
		return nil
	}
	if mouse.Button == tea.MouseLeft {
		m.drag(mouse)
	}
	if !s.dragged {
		m.sel = nil
		return nil
	}
	s.done = true
	text := m.selectedText()
	if text == "" {
		return nil
	}
	m.log.Info("selection copied", "chars", len([]rune(text)), "lines", strings.Count(text, "\n")+1)
	return m.copy(text)
}

func (s *selection) clamp(at uv.Position) uv.Position {
	return uv.Pos(
		min(max(at.X, s.area.Min.X), s.area.Max.X-1),
		min(max(at.Y, s.area.Min.Y), s.area.Max.Y-1),
	)
}

// rows calls fn with each row of the selection and its first and last
// column: the rows between whole, as a terminal selects text.
func (s *selection) rows(fn func(y, x0, x1 int)) {
	start, end := s.from, s.to
	if end.Y < start.Y || end.Y == start.Y && end.X < start.X {
		start, end = end, start
	}
	for y := start.Y; y <= end.Y; y++ {
		x0, x1 := s.area.Min.X, s.area.Max.X-1
		if y == start.Y {
			x0 = start.X
		}
		if y == end.Y {
			x1 = end.X
		}
		fn(y, x0, x1)
	}
}

// highlight shows the selection in buf, in reverse.
func (s *selection) highlight(buf uv.Screen) {
	s.rows(func(y, x0, x1 int) {
		for x := x0; x <= x1; x++ {
			if c := buf.CellAt(x, y); c != nil {
				r := *c
				r.Style.Attrs |= uv.AttrReverse
				buf.SetCell(x, y, &r)
			}
		}
	})
}

// selectedText returns the text selected, as the pane shows it: a line per
// row, without trailing spaces.
func (m *Model) selectedText() string {
	buf, ok := m.paint(false)
	if !ok {
		return ""
	}
	var lines []string
	m.sel.rows(func(y, x0, x1 int) {
		var line strings.Builder
		for x := x0; x <= x1; x++ {
			c := buf.CellAt(x, y)
			switch {
			case c == nil:
				line.WriteByte(' ')
			case c.Width == 0: // the rest of a wide character
			default:
				line.WriteString(c.Content)
			}
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
	})
	return strings.Join(lines, "\n")
}

// clipboardTimeout is how long a clipboard tool may take.
const clipboardTimeout = 2 * time.Second

// copyToClipboard puts text on the clipboard two ways: OSC 52 through the
// terminal, which works over ssh but which GNOME Terminal and Terminal.app
// ignore, and the system's own tool, which works locally.
func (m *Model) copyToClipboard(text string) tea.Cmd {
	return tea.Batch(tea.SetClipboard(text), func() tea.Msg {
		if !systemCopy(m.ctx, m.log, text) {
			return copyMissed{}
		}
		return nil
	})
}

// copyMissed says no clipboard tool took a copy. OSC 52 may have, but no
// terminal says whether it did.
type copyMissed struct{}

// copyMissedNotice is what the status line says then, until the next key
// or click.
const copyMissedNotice = "no clipboard tool, the copy may be lost: wdx deps install --name clipboard"

// clipboardTools are the commands that set the clipboard from their input,
// tried in order.
func clipboardTools() [][]string {
	if runtime.GOOS == "darwin" {
		return [][]string{{"pbcopy"}}
	}
	return [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}
}

// systemCopy sets the clipboard with the first tool that is installed and
// works, and reports whether one did.
func systemCopy(ctx context.Context, log *slog.Logger, text string) bool {
	for _, tool := range clipboardTools() {
		if _, err := exec.LookPath(tool[0]); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(ctx, clipboardTimeout)
		// No output is read: the tools leave a process behind serving the
		// clipboard, which would hold a pipe open.
		cmd := exec.CommandContext(ctx, tool[0], tool[1:]...)
		cmd.Stdin = strings.NewReader(text)
		err := cmd.Run()
		cancel()
		if err == nil {
			log.Info("clipboard set", "tool", tool[0])
			return true
		}
		log.Info("clipboard tool failed", "tool", tool[0], "err", err)
	}
	log.Info("no clipboard tool worked", "tools", len(clipboardTools()))
	return false
}
