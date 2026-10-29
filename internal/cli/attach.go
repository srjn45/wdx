package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/style"
	"github.com/srjn45/wdx/internal/termview"
)

// attachExitWait bounds how long attach waits for the daemon to record the
// exit it saw, for the terminal it prints.
const attachExitWait = 2 * time.Second

// The smallest terminal attach runs in: a pane and the status line.
const minCols, minRows = 10, 3

func newTerminalAttachCmd() *cobra.Command {
	var ref terminalRef
	var prefixKey string
	cmd := &cobra.Command{
		Use:   "attach (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] [--prefix-key <key>]",
		Short: "Show a terminal full-window and type into it, as tmux does",
		Long: "The terminal takes the whole window, above a status line, and what you type\n" +
			"goes to its shell. C-\\ is the prefix for attach's own keys: C-\\ d detaches,\n" +
			"leaving the terminal running, and gives the window back as it was; C-\\ [\n" +
			"and ] scroll through its history, as PageUp, PageDown and the mouse wheel\n" +
			"do at the shell; C-\\ ? lists them all. Several clients can attach to one\n" +
			"terminal; the last to attach or resize sets its size. It runs in the\n" +
			"terminal it was started from, not on stdin and stdout, which print the\n" +
			"result when it ends.",
		Example: examples(
			"Attach to a terminal of the project you are in", identity.Name+" terminal attach --name server",
			"With ctrl-] as the prefix", identity.Name+" terminal attach --id term-1a2b3c4d --prefix-key ctrl-]",
			"As JSON once it ends", identity.Name+" terminal attach --name server --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not attached",
			outputNote: "The terminal, once you detach or its shell exits.\n" +
				`With --json: {"result": "detached" or "exited", "terminal": {...}}`,
			resultList: annotation("detached", "exited"),
			errorList: codeList(api.ErrorCodeNotFound, api.ErrorCodeTerminalNotRunning, api.ErrorCodeAttachUnsupported,
				api.ErrorCodeConnectionLost, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			prefix, err := parsePrefixKey(prefixKey)
			if err != nil {
				return &usageError{err}
			}
			return attach(cmd, ref, prefix)
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().StringVar(&prefixKey, "prefix-key", `ctrl-\`, "`key` before attach's own keys: ctrl-<letter> but i and m, ctrl-\\ or ctrl-]")
	cmd.RegisterFlagCompletionFunc("prefix-key", cobra.FixedCompletions([]string{`ctrl-\`, "ctrl-]", "ctrl-b", "ctrl-a"}, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

// prefixKey is the key before attach's own keys.
type prefixKey struct {
	name  string // as Bubble Tea names the key pressed, ctrl+\
	label string // as the status line shows it, C-\
}

func parsePrefixKey(key string) (prefixKey, error) {
	// Terminals send them as the keys they stand for.
	if same, ok := map[string]string{"ctrl-i": "Tab", "ctrl-m": "Enter"}[key]; ok {
		return prefixKey{}, fmt.Errorf("--prefix-key %s is %s to a terminal: choose another letter", key, same)
	}
	rest, ok := strings.CutPrefix(key, "ctrl-")
	if ok && (rest == `\` || rest == "]" || len(rest) == 1 && rest[0] >= 'a' && rest[0] <= 'z') {
		return prefixKey{name: "ctrl+" + rest, label: "C-" + rest}, nil
	}
	return prefixKey{}, fmt.Errorf("--prefix-key %q is not ctrl-<letter>, ctrl-\\ or ctrl-]", key)
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

func attach(cmd *cobra.Command, ref terminalRef, prefix prefixKey) error {
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
	if size.Cols < minCols || size.Rows < minRows {
		return &usageError{fmt.Errorf("the terminal is %d×%d; attach needs at least %d×%d", size.Cols, size.Rows, minCols, minRows)}
	}
	size.Rows-- // the status line's

	session, err := holder.Attach(ctx, identity.HolderSocket(env.Config.Home, t.Id), t.Id, size, 0)
	if errors.Is(err, holder.ErrAttachUnsupported) {
		return &attachUnsupportedError{t}
	}
	if err != nil {
		return &connectionLostError{t, err}
	}
	defer session.Close()
	log.Info("attached", "terminal_id", t.Id, "cols", size.Cols, "rows", size.Rows,
		"history_lines", session.Snapshot.HistoryLines, "pos", session.Snapshot.Pos)

	fetch := func(from int64, count int) (termview.History, error) {
		h, err := c.GetTerminalHistory(ctx, "id:"+t.Id, "", "", &from, count)
		return termview.History{From: h.History.From, First: h.History.First, Lines: h.History.Lines}, err
	}
	view := newAttachView(termview.New(session, fetch), t, prefix, viewStyle(cmd))
	defer view.pane.Close()
	// A signal detaches, giving the window back as it was; the command ends
	// as after C-\ d.
	viewCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	ended, err := runAttachView(viewCtx, tty, env, view)
	if err != nil {
		log.Info("connection lost", "terminal_id", t.Id, "err", err)
		return &connectionLostError{t, err}
	}
	log.Info("attach ended", "terminal_id", t.Id, "result", ended, "vt_resets", view.pane.Resets())
	if ended == api.ResultExited {
		t = waitRecorded(ctx, cmd, t, session.Exit())
	} else if got, err := c.GetTerminal(ctx, "id:"+t.Id, "", ""); err == nil {
		t = got.Terminal
	}
	return printResult(cmd, attachResult{Result: ended, Terminal: t})
}

// viewStyle styles the attach view, which is on a terminal whatever stdout
// is: off only when color is turned off.
func viewStyle(cmd *cobra.Command) style.Style {
	env := invocationOf(cmd).env
	mode := colorMode(cmd, env)
	return style.New(mode == config.ColorAlways || mode == config.ColorAuto && !env.NoColor)
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
// does.
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
