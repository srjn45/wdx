package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
	"github.com/srjn45/wdx/internal/termview"
)

// attachView is wdx terminal attach's window: the terminal's pane above a
// status line, and the prefix keys (docs/ATTACH_VIEW.md).
type attachView struct {
	pane     *termview.Model
	terminal api.Terminal
	prefix   prefixKey
	st       style.Style

	width, height int
	status        bool // the status line is shown
	pending       bool // the prefix was pressed
	help          bool // the list of keys is shown
	detached      bool
	ended         *termview.EndedMsg
}

func newAttachView(pane *termview.Model, t api.Terminal, prefix prefixKey, st style.Style) *attachView {
	cols, rows := pane.Size()
	return &attachView{pane: pane, terminal: t, prefix: prefix, st: st, width: cols, height: rows + 1, status: true}
}

// Sequences around the view: the calling terminal's title is kept and given
// back, which Bubble Tea leaves changed.
const (
	titlePush = "\x1b[22;0t"
	titlePop  = "\x1b[23;0t"
)

// runAttachView runs v full-window on tty until it detaches or the session
// ends, and says which; Bubble Tea gives the window back as it was.
func runAttachView(ctx context.Context, tty *os.File, env Env, v *attachView) (api.Result, error) {
	_, _ = io.WriteString(tty, titlePush)
	defer func() { _, _ = io.WriteString(tty, titlePop) }()
	p := tea.NewProgram(v, tea.WithInput(tty), tea.WithOutput(tty), tea.WithContext(ctx),
		tea.WithEnvironment(env.TermEnv), tea.WithoutSignalHandler())
	_, err := p.Run()
	switch {
	case errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil:
		return api.ResultDetached, nil // the command is ending: leave the terminal running
	case err != nil:
		return "", err
	case v.ended == nil:
		return api.ResultDetached, nil
	case v.ended.Err != nil:
		return "", v.ended.Err
	}
	return api.ResultExited, nil
}

func (v *attachView) Init() tea.Cmd {
	return v.pane.Init()
}

func (v *attachView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		return v, v.resize()
	case termview.EndedMsg:
		v.ended = &msg
		return v, tea.Quit
	case tea.KeyPressMsg:
		return v, v.key(msg)
	case tea.MouseMsg:
		if v.help || msg.Mouse().Y >= v.paneRows() {
			return v, nil
		}
	}
	return v, v.pane.Update(msg)
}

// key handles the prefix and the key after it; every other key goes to the
// pane.
func (v *attachView) key(msg tea.KeyPressMsg) tea.Cmd {
	name := msg.String()
	switch {
	case v.help:
		v.help = false
		return nil
	case v.pending:
		v.pending = false
		switch name {
		case "d":
			v.detached = true
			return tea.Quit
		case "[":
			return v.pane.ScrollPage(1)
		case "]":
			return v.pane.ScrollPage(-1)
		case "?":
			v.help = true
		case "s":
			v.status = !v.status
			return v.resize()
		case v.prefix.name:
			return v.pane.Update(msg) // pressed twice: sent once
		}
		return nil // no shortcut: nothing, as in tmux
	case name == v.prefix.name:
		v.pending = true
		return nil
	case name == "ctrl+?" || name == "ctrl+shift+/":
		// Told apart from Backspace only through the kitty keyboard protocol.
		v.help = true
		return nil
	}
	return v.pane.Update(msg)
}

func (v *attachView) paneRows() int {
	if v.status {
		return max(1, v.height-1)
	}
	return max(1, v.height)
}

func (v *attachView) resize() tea.Cmd {
	// A size that cannot be sent means the connection is gone, which
	// reading it tells.
	_ = v.pane.SetSize(v.width, v.paneRows())
	return nil
}

func (v *attachView) View() tea.View {
	content := v.pane.Content()
	if v.help {
		content = v.helpContent()
	}
	if v.status {
		content += "\n" + v.statusLine()
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	if !v.help {
		view.Cursor = v.pane.Cursor()
	}
	view.WindowTitle = v.pane.Title()
	if view.WindowTitle == "" {
		view.WindowTitle = v.terminal.Name + " · " + v.terminal.ProjectName
	}
	return view
}

// statusLine names the terminal on the left and says what the keys do now
// on the right, in a bar whose color tells the state.
func (v *attachView) statusLine() string {
	st := v.st
	left := " " + st.BarName(v.terminal.Name) + " · " + v.terminal.ProjectName
	var right string
	state := style.StatusLive
	switch {
	case v.pending:
		state = style.StatusPrefix
		right = v.prefix.label + " …  d detach  [ ] scroll  ? keys  s status  " + v.prefix.label + " send it"
	case v.pane.FetchErr() != nil:
		state = style.StatusFailed
		right = "history unavailable: " + v.pane.FetchErr().Error()
	case v.pane.Scrolled():
		state = style.StatusScrolled
		top, lines := v.pane.Position()
		right = fmt.Sprintf("history %d of %d · PgDn or %s ] down · any key: back", top+1, lines, v.prefix.label)
	default:
		right = st.BarMuted(v.prefix.label + " ? for keys")
	}
	right += " "
	line := left + " " + right
	if gap := v.width - ansi.StringWidth(left) - ansi.StringWidth(right); gap >= 1 {
		line = left + strings.Repeat(" ", gap) + right
	}
	return st.Bar(ansi.Truncate(line, v.width, ""), state)
}

// helpContent lists the keys over the pane.
func (v *attachView) helpContent() string {
	p := v.prefix.label
	lines := []string{
		"",
		"  " + v.st.Heading("Keys") + v.st.Muted(" (any key closes this)"),
		"",
		"  " + p + " d          detach, leaving the terminal running",
		"  " + p + " [  " + p + " ]    scroll a page up / down through its history",
		"  " + p + " ?          this list",
		"  " + p + " s          hide or show the status line",
		"  " + p + " " + p + "        send " + p + " to the terminal",
		"",
		"  PageUp, PageDown   scroll, at the shell (in less or vim they are theirs)",
		"  mouse wheel        scroll, at the shell; programs that use the mouse get it",
		"  any other key      back to the live screen, and to the terminal",
		"",
		"  Copy text with Shift+drag: attach has the mouse.",
	}
	rows := v.paneRows()
	for len(lines) < rows {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], v.width, "")
	}
	return strings.Join(lines[:rows], "\n")
}
