package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/style"
)

// doubleKeyWait is how long a detach key waits for a second press, which
// sends it to the shell instead.
const doubleKeyWait = 500 * time.Millisecond

// attachExitWait bounds how long attach waits for the daemon to record the
// exit it saw, for the terminal it prints.
const attachExitWait = 2 * time.Second

func newTerminalAttachCmd() *cobra.Command {
	var ref terminalRef
	var history, detachKey string
	cmd := &cobra.Command{
		Use:   "attach (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] [--history <lines> | --history all] [--detach-key <key>]",
		Short: "Show a terminal's shell here and type into it",
		Long: "This terminal shows the terminal's screen, with its history above it to scroll\n" +
			"back through, and what you type goes to its shell. Press the detach key\n" +
			"(ctrl-\\ by default) to leave it running; press it twice to send it to the\n" +
			"shell. Several terminals can attach to one; the last to attach or resize\n" +
			"sets its size. It talks to the terminal it runs in, not to stdin and stdout,\n" +
			"which print the result when it ends.",
		Example: examples(
			"Attach to a terminal of the project you are in", identity.Name+" terminal attach --name server",
			"With all of its history, detaching with ctrl-]", identity.Name+" terminal attach --id term-1a2b3c4d --history all --detach-key ctrl-]",
			"As JSON once it ends", identity.Name+" terminal attach --name server --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not attached",
			outputNote: "Once you detach or the shell exits, the terminal.\n" +
				`With --json: {"result": "detached", "terminal": {...}}`,
			resultList: annotation("detached", "exited"),
			errorList: codeList(api.ErrorCodeNotFound, api.ErrorCodeTerminalNotRunning, api.ErrorCodeAttachUnsupported,
				api.ErrorCodeConnectionLost, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			key, err := parseDetachKey(detachKey)
			if err != nil {
				return &usageError{err}
			}
			lines, err := parseHistory(history, invocationOf(cmd).env.Config.TerminalAttachHistory)
			if err != nil {
				return &usageError{err}
			}
			return attach(cmd, ref, lines, key)
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().StringVar(&history, "history", "", "`lines` of history to show before the screen, or all (default: terminal.attach_history)")
	cmd.Flags().StringVar(&detachKey, "detach-key", "ctrl-\\", "`key` that detaches: ctrl-<letter>, ctrl-\\, ctrl-], ctrl-^, ctrl-_ or none")
	cmd.RegisterFlagCompletionFunc("history", cobra.FixedCompletions([]string{"all"}, cobra.ShellCompDirectiveNoFileComp))
	cmd.RegisterFlagCompletionFunc("detach-key", cobra.FixedCompletions([]string{"ctrl-\\", "ctrl-]", "ctrl-^", "ctrl-_", "none"}, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

// parseDetachKey returns the byte the key sends, or -1 for none.
func parseDetachKey(key string) (int, error) {
	switch key {
	case "none":
		return -1, nil
	case `ctrl-\`:
		return 0x1c, nil
	case "ctrl-]":
		return 0x1d, nil
	case "ctrl-^":
		return 0x1e, nil
	case "ctrl-_":
		return 0x1f, nil
	}
	if letter, ok := strings.CutPrefix(key, "ctrl-"); ok && len(letter) == 1 && letter[0] >= 'a' && letter[0] <= 'z' {
		return int(letter[0] - 'a' + 1), nil
	}
	return 0, fmt.Errorf("--detach-key %q is not ctrl-<letter>, ctrl-\\, ctrl-], ctrl-^, ctrl-_ or none", key)
}

// parseHistory returns the history lines --history asks for: def when not
// given, -1 for all.
func parseHistory(value string, def int) (int, error) {
	switch value {
	case "":
		return def, nil
	case "all":
		return -1, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("--history %q is not a number of lines or all", value)
	}
	return n, nil
}

// terminalNotRunningError is a terminal whose shell is not running.
type terminalNotRunningError struct {
	terminal api.Terminal
}

func (e *terminalNotRunningError) Error() string {
	return fmt.Sprintf("terminal %s is %s, not running", e.terminal.Name, e.terminal.State)
}

// attachUnsupportedError is a terminal whose holder is too old to attach to.
type attachUnsupportedError struct {
	terminal api.Terminal
}

func (e *attachUnsupportedError) Error() string {
	return fmt.Sprintf("terminal %s was started by a wdx too old to attach to", e.terminal.Name)
}

// connectionLostError is an attach whose connection ended before the shell.
type connectionLostError struct {
	terminal api.Terminal
	err      error
}

func (e *connectionLostError) Error() string {
	return fmt.Sprintf("the connection to terminal %s was lost: %v", e.terminal.Name, e.err)
}

func (e *connectionLostError) Unwrap() error { return e.err }

// attachProblem returns the problem of an attach error, if err is one.
func attachProblem(err error) (api.Problem, bool) {
	var p api.Problem
	var t api.Terminal
	var hint string
	if e, ok := errors.AsType[*terminalNotRunningError](err); ok {
		p, t = api.NewProblem(api.ErrorCodeTerminalNotRunning, err.Error()), e.terminal
		hint = "start its shell with " + identity.Name + " terminal start, or open its project"
	} else if e, ok := errors.AsType[*attachUnsupportedError](err); ok {
		p, t = api.NewProblem(api.ErrorCodeAttachUnsupported, err.Error()), e.terminal
		hint = "close and open its project, or remove the terminal and create it again, to restart its shell"
	} else if e, ok := errors.AsType[*connectionLostError](err); ok {
		p, t = api.NewProblem(api.ErrorCodeConnectionLost, err.Error()), e.terminal
		hint = "attach again; the shell may still run: check " + identity.Name + " terminal get"
	} else {
		return api.Problem{}, false
	}
	p.Details = &map[string]any{"terminal": t}
	p.Hint = &hint
	return p, true
}

// attachResult is how an attach ended.
type attachResult api.TerminalResult

func (r attachResult) text(w io.Writer, st style.Style) {
	t := r.Terminal
	if r.Result == api.ResultExited {
		code := "?"
		if t.ExitCode != nil {
			code = strconv.Itoa(*t.ExitCode)
		}
		fmt.Fprintf(w, "terminal %s in %s %s with code %s\n", st.Name(t.Name), st.Name(t.ProjectName), st.Success("exited"), code)
		return
	}
	fmt.Fprintf(w, "%s from terminal %s in %s\n", st.Success("detached"), st.Name(t.Name), st.Name(t.ProjectName))
	fmt.Fprintf(w, "  %s %s\n", st.Muted("id    "), t.Id)
}

func attach(cmd *cobra.Command, ref terminalRef, history, key int) error {
	ctx := cmd.Context()
	log := logging.FromContext(ctx)
	path, project, dir, err := ref.resolve(cmd)
	if err != nil {
		return err
	}
	c := clientOf(cmd)
	got, err := c.GetTerminal(ctx, path, project, dir)
	if err != nil {
		return ref.notFoundHint(err)
	}
	t := got.Terminal
	if t.State != api.TerminalStateRunning {
		return &terminalNotRunningError{t}
	}
	env := invocationOf(cmd).env
	if env.TTY == nil {
		return &usageError{errors.New("no terminal to attach in: run it in one")}
	}
	tty, err := env.TTY()
	if err != nil {
		return &usageError{fmt.Errorf("no terminal to attach in: %w", err)}
	}
	defer tty.Close()
	fd, err := ttyFD(tty)
	if err != nil {
		return err
	}
	size, err := ttySize(fd)
	if err != nil {
		return &usageError{fmt.Errorf("%s is not a terminal: %w", tty.Name(), err)}
	}

	session, err := holder.Attach(ctx, identity.HolderSocket(env.Config.Home, t.Id), t.Id, size, history)
	if errors.Is(err, holder.ErrAttachUnsupported) {
		return &attachUnsupportedError{t}
	}
	if err != nil {
		return &connectionLostError{t, err}
	}
	defer session.Close()
	log.Info("attached", "terminal_id", t.Id, "cols", size.Cols, "rows", size.Rows,
		"history_lines", len(session.History.Lines), "pos", session.Snapshot.Pos)

	ended, err := attached(ctx, tty, fd, session, key)
	if err != nil {
		log.Info("connection lost", "terminal_id", t.Id, "err", err.Error())
		return &connectionLostError{t, err}
	}
	log.Info("attach ended", "terminal_id", t.Id, "result", ended)
	if ended == api.ResultExited {
		t = waitRecorded(ctx, cmd, t, session.Exit())
	} else if got, err := c.GetTerminal(ctx, "id:"+t.Id, "", ""); err == nil {
		t = got.Terminal
	}
	return printResult(cmd, attachResult{Result: ended, Terminal: t})
}

// waitRecorded returns the terminal once the daemon has recorded the exit
// the session saw, or with that exit if it takes too long.
func waitRecorded(ctx context.Context, cmd *cobra.Command, t api.Terminal, exit *holder.Exit) api.Terminal {
	deadline := time.Now().Add(attachExitWait)
	for {
		got, err := clientOf(cmd).GetTerminal(ctx, "id:"+t.Id, "", "")
		if err == nil && got.Terminal.State != api.TerminalStateRunning {
			return got.Terminal
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			if exit != nil {
				t.ExitCode = &exit.Code
			}
			return t
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// ttyFD returns the descriptor of tty without making it blocking, as Fd
// does: its reads must stay interruptible.
func ttyFD(tty *os.File) (int, error) {
	raw, err := tty.SyscallConn()
	if err != nil {
		return 0, err
	}
	fd := -1
	if err := raw.Control(func(f uintptr) { fd = int(f) }); err != nil {
		return 0, err
	}
	return fd, nil
}

func ttySize(fd int) (holder.Size, error) {
	cols, rows, err := term.GetSize(fd)
	return holder.Size{Cols: cols, Rows: rows}, err
}

// Sequences written around an attached terminal: its title is kept and
// given back, and on leaving whatever the program inside set is undone:
// the alternate screen, hidden cursor, mouse and paste modes, keypad and
// cursor keys, margins and style.
const (
	ttyEnter = "\x1b[22;0t"
	ttyLeave = "\x1b[?1049l\x1b[?25h\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?2004l\x1b[?1004l" +
		"\x1b[?1l\x1b>\x1b7\x1b[r\x1b8\x1b[m\x1b[23;0t"
)

// attached shows the session on tty in raw mode, and sends what is typed,
// until the detach key, the shell's exit or the connection's end.
func attached(ctx context.Context, tty *os.File, fd int, s *holder.Session, key int) (api.Result, error) {
	state, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	defer func() {
		// The prompt after it starts on a line of its own, at the bottom.
		_, _ = io.WriteString(tty, ttyLeave+"\x1b[9999;1H\r\n")
		_ = term.Restore(fd, state)
	}()
	var b strings.Builder
	b.WriteString(ttyEnter)
	for _, line := range s.History.Lines {
		b.WriteString(line + "\r\n")
	}
	// What is on the screen scrolls up, into the terminal's scrollback,
	// rather than being cleared by the screen painted over it.
	b.WriteString(strings.Repeat("\r\n", s.Snapshot.Rows))
	if _, err := io.WriteString(tty, b.String()); err != nil {
		return "", err
	}

	output := make(chan error, 1)
	go func() {
		_, err := io.Copy(tty, s)
		output <- err
	}()
	input := make(chan error, 1)
	stop := make(chan struct{})
	go func() { input <- forwardInput(tty, s, key, stop) }()
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	inputDone := false
	defer func() {
		close(stop)
		if !inputDone {
			_ = tty.SetReadDeadline(time.Now()) // ends the read in forwardInput
			if err := <-input; err != nil {
				logging.FromContext(ctx).Debug("input ended", "err", err.Error())
			}
		}
	}()
	for {
		select {
		case err := <-output:
			if err == nil || errors.Is(err, io.EOF) {
				return api.ResultExited, nil
			}
			return "", err
		case err := <-input:
			inputDone = true
			if errors.Is(err, errDetached) {
				return api.ResultDetached, nil
			}
			return "", err
		case <-winch:
			if size, err := ttySize(fd); err == nil {
				if err := s.Resize(size); err != nil {
					return "", err
				}
			}
		case <-ctx.Done():
			return api.ResultDetached, nil
		}
	}
}

var errDetached = errors.New("detached")

// forwardInput sends what is typed on tty to the session until the detach
// key, or until stop. The key detaches unless pressed again within
// doubleKeyWait, which sends it once.
func forwardInput(tty *os.File, s *holder.Session, key int, stop <-chan struct{}) error {
	buf := make([]byte, 4096)
	pending := false
	for {
		n, err := tty.Read(buf)
		if pending && errors.Is(err, os.ErrDeadlineExceeded) {
			select {
			case <-stop:
				return nil
			default:
				return errDetached
			}
		}
		if err != nil {
			select {
			case <-stop:
				return nil
			default:
				return err
			}
		}
		var send []byte
		for _, c := range buf[:n] {
			switch {
			case int(c) != key:
				if pending {
					return errDetached
				}
				send = append(send, c)
			case pending:
				send = append(send, c)
				pending = false
			default:
				pending = true
			}
		}
		if len(send) > 0 {
			if _, err := s.Write(send); err != nil {
				return err
			}
		}
		var deadline time.Time
		if pending {
			deadline = time.Now().Add(doubleKeyWait)
		}
		if err := tty.SetReadDeadline(deadline); err != nil && pending {
			return errDetached // a terminal without deadlines detaches at once
		}
	}
}

func newTerminalHistoryCmd() *cobra.Command {
	var ref terminalRef
	var tail, count int
	var from int64
	var plain bool
	cmd := &cobra.Command{
		Use:   "history (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] [--tail <lines> | --from <line> [--count <lines>]] [--plain]",
		Short: "Print the lines that scrolled off a terminal's screen",
		Long: "History is kept on disk for the terminal's life, across restarts of its shell,\n" +
			"up to terminal.history_lines, and is there in any state. Lines keep their\n" +
			"colors when the output gets color; --plain, or output that is not a terminal,\n" +
			"strips them. Lines are numbered from 0, which --from counts in.",
		Example: examples(
			"The last lines of a terminal of the project you are in", identity.Name+" terminal history --name server",
			"The last 50, without colors", identity.Name+" terminal history --name server --tail 50 --plain",
			"From line 1200 on, as JSON", identity.Name+" terminal history --id term-1a2b3c4d --from 1200 --count 100 --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The lines, oldest first.\n" + `With --json: {"history": {"from": ..., "next": ..., "first": ..., "lines": [...]}}`,
			errorList:  codeList(api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			n := invocationOf(cmd).env.Config.TerminalAttachHistory
			var start *int64
			switch {
			case cmd.Flags().Changed("from"):
				start, n = &from, 1000
				if cmd.Flags().Changed("count") {
					n = count
				}
			case cmd.Flags().Changed("tail"):
				n = tail
			case n == 0:
				n = 1000 // attach shows none by default, but history prints some
			}
			if n < 1 || n > 10_000 {
				return &usageError{fmt.Errorf("%d lines is not between 1 and 10000", n)}
			}
			h, err := clientOf(cmd).GetTerminalHistory(cmd.Context(), path, project, dir, start, n)
			if err != nil {
				return ref.notFoundHint(err)
			}
			return printResult(cmd, historyResult{h, plain})
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().IntVar(&tail, "tail", 0, "the last `lines` (default: terminal.attach_history)")
	cmd.Flags().Int64Var(&from, "from", 0, "number of the first `line`")
	cmd.Flags().IntVar(&count, "count", 1000, "`lines` from --from on")
	cmd.Flags().BoolVar(&plain, "plain", false, "strip colors and other escape codes")
	cmd.MarkFlagsMutuallyExclusive("tail", "from")
	cmd.MarkFlagsMutuallyExclusive("tail", "count")
	for _, name := range []string{"tail", "from", "count"} {
		cmd.RegisterFlagCompletionFunc(name, cobra.NoFileCompletions)
	}
	return cmd
}

// historyResult is lines of a terminal's history.
type historyResult struct {
	api.TerminalHistoryEnvelope
	plain bool
}

func (r historyResult) text(w io.Writer, st style.Style) {
	for _, line := range r.History.Lines {
		if r.plain || !st.On() {
			line = ansi.Strip(line)
		}
		fmt.Fprintln(w, line)
	}
}

// LogValue keeps the lines out of the log: terminal output can hold
// secrets.
func (r historyResult) LogValue() slog.Value {
	h := r.History
	return slog.GroupValue(slog.Group("history",
		"from", h.From, "next", h.Next, "first", h.First, "lines", len(h.Lines)))
}
