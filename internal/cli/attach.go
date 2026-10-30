package cli

import (
	"context"
	"encoding/json"
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
	var prefix string
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
			key, err := parsePrefixKey(prefix)
			if err != nil {
				return &usageError{err}
			}
			return attach(cmd, func(ctx context.Context) (attachTarget, error) {
				path, project, dir, err := ref.resolve(cmd)
				if err != nil {
					return attachTarget{}, err
				}
				got, err := clientOf(cmd).GetTerminal(ctx, path, project, dir)
				if err != nil {
					return attachTarget{}, ref.notFoundHint(err)
				}
				return terminalTarget(cmd, got.Terminal), nil
			}, key)
		},
	}
	ref.addFlags(cmd)
	addPrefixKeyFlag(cmd, &prefix)
	return cmd
}

func addPrefixKeyFlag(cmd *cobra.Command, prefix *string) {
	cmd.Flags().StringVar(prefix, "prefix-key", `ctrl-\`, "`key` before attach's own keys: ctrl-<letter> but i and m, ctrl-\\ or ctrl-]")
	cmd.RegisterFlagCompletionFunc("prefix-key", cobra.FixedCompletions([]string{`ctrl-\`, "ctrl-]", "ctrl-b", "ctrl-a"}, cobra.ShellCompDirectiveNoFileComp))
}

// attachTarget is what attach shows: a terminal, or an agent. Both are a
// holder to dial; they differ in what the API calls them.
type attachTarget struct {
	noun          string // "terminal" or "agent"
	holderID      string // the terminal row's
	name, project string
	state         string
	running       bool
	exitCode      *int
	value         any // the api.Terminal or api.Agent, as results and problems show it
	// reread returns the target as it is now.
	reread func(ctx context.Context) (attachTarget, error)
	// exited is the target with an exit the daemon has not recorded yet.
	exited func(code int) attachTarget
	// history reads count lines from line from on.
	history func(ctx context.Context, from int64, count int) (api.TerminalHistoryEnvelope, error)
}

func terminalTarget(cmd *cobra.Command, t api.Terminal) attachTarget {
	c := clientOf(cmd)
	ref := "id:" + t.Id
	return attachTarget{
		noun: "terminal", holderID: t.Id, name: t.Name, project: t.ProjectName,
		state: string(t.State), running: t.State == api.TerminalStateRunning, exitCode: t.ExitCode, value: t,
		reread: func(ctx context.Context) (attachTarget, error) {
			got, err := c.GetTerminal(ctx, ref, "", "")
			if err != nil {
				return attachTarget{}, err
			}
			return terminalTarget(cmd, got.Terminal), nil
		},
		exited: func(code int) attachTarget {
			t.ExitCode = &code
			return terminalTarget(cmd, t)
		},
		history: func(ctx context.Context, from int64, count int) (api.TerminalHistoryEnvelope, error) {
			return c.GetTerminalHistory(ctx, ref, "", "", &from, count)
		},
	}
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

// notRunningError is a target whose shell or AI coding agent is not running.
type notRunningError struct {
	target attachTarget
}

func (e *notRunningError) Error() string {
	return fmt.Sprintf("%s %s is %s, not running", e.target.noun, e.target.name, e.target.state)
}

// attachUnsupportedError is a target whose holder is too old to attach to.
type attachUnsupportedError struct {
	target attachTarget
}

func (e *attachUnsupportedError) Error() string {
	return fmt.Sprintf("%s %s was started by a wdx too old to attach to", e.target.noun, e.target.name)
}

// connectionLostError is an attach whose connection ended before the shell.
type connectionLostError struct {
	target attachTarget
	err    error
}

func (e *connectionLostError) Error() string {
	return fmt.Sprintf("the connection to %s %s was lost: %v", e.target.noun, e.target.name, e.err)
}

func (e *connectionLostError) Unwrap() error { return e.err }

// attachProblem returns the problem of an attach error, if err is one.
func attachProblem(err error) (api.Problem, bool) {
	var p api.Problem
	var target attachTarget
	var hint string
	if e, ok := errors.AsType[*notRunningError](err); ok {
		target = e.target
		if target.noun == "agent" {
			p = api.NewProblem(api.ErrorCodeAgentNotRunning, err.Error())
			hint = "start it with " + identity.Name + " agent start, or open its project"
		} else {
			p = api.NewProblem(api.ErrorCodeTerminalNotRunning, err.Error())
			hint = "start its shell with " + identity.Name + " terminal start, or open its project"
		}
	} else if e, ok := errors.AsType[*attachUnsupportedError](err); ok {
		target = e.target
		p = api.NewProblem(api.ErrorCodeAttachUnsupported, err.Error())
		hint = "close and open its project, or remove the terminal and create it again, to restart its shell"
	} else if e, ok := errors.AsType[*connectionLostError](err); ok {
		target = e.target
		p = api.NewProblem(api.ErrorCodeConnectionLost, err.Error())
		if target.noun == "agent" {
			hint = "attach again; it may still run: check " + identity.Name + " agent get"
		} else {
			hint = "attach again; the shell may still run: check " + identity.Name + " terminal get"
		}
	} else {
		return api.Problem{}, false
	}
	p.Details = &map[string]any{target.noun: target.value}
	p.Hint = &hint
	return p, true
}

// attachResult is how an attach ended.
type attachResult struct {
	Result api.Result
	target attachTarget
}

// MarshalJSON gives the result with the target under its own key, as the
// API's results do.
func (r attachResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"result": r.Result, r.target.noun: r.target.value})
}

func (r attachResult) text(w io.Writer, st style.Style) {
	t := r.target
	if r.Result == api.ResultExited {
		code := "?"
		if t.exitCode != nil {
			code = strconv.Itoa(*t.exitCode)
		}
		fmt.Fprintf(w, "%s %s in %s %s with code %s\n", t.noun, st.Name(t.name), st.Name(t.project), st.Success("exited"), code)
		return
	}
	fmt.Fprintf(w, "%s from %s %s in %s\n", st.Success("detached"), t.noun, st.Name(t.name), st.Name(t.project))
	id := t.holderID
	if a, ok := t.value.(api.Agent); ok {
		id = a.Id
	}
	fmt.Fprintf(w, "  %s %s\n", st.Muted("id    "), id)
}

// attach shows the target find returns.
func attach(cmd *cobra.Command, find func(context.Context) (attachTarget, error), prefix prefixKey) error {
	ctx := cmd.Context()
	log := logging.FromContext(ctx)
	t, err := find(ctx)
	if err != nil {
		return err
	}
	if !t.running {
		return &notRunningError{t}
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

	session, err := holder.Attach(ctx, identity.HolderSocket(env.Config.Home, t.holderID), t.holderID, size, 0)
	if errors.Is(err, holder.ErrAttachUnsupported) {
		return &attachUnsupportedError{t}
	}
	if err != nil {
		return &connectionLostError{t, err}
	}
	defer session.Close()
	log.Info("attached", "terminal_id", t.holderID, "cols", size.Cols, "rows", size.Rows,
		"history_lines", session.Snapshot.HistoryLines, "pos", session.Snapshot.Pos)

	fetch := func(from int64, count int) (termview.History, error) {
		h, err := t.history(ctx, from, count)
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
		log.Info("connection lost", "terminal_id", t.holderID, "err", err)
		return &connectionLostError{t, err}
	}
	log.Info("attach ended", "terminal_id", t.holderID, "result", ended, "vt_resets", view.pane.Resets())
	if ended == api.ResultExited {
		t = waitRecorded(ctx, t, session.Exit())
	} else if got, err := t.reread(ctx); err == nil {
		t = got
	}
	return printResult(cmd, attachResult{Result: ended, target: t})
}

// viewStyle styles the attach view, which is on a terminal whatever stdout
// is: off only when color is turned off.
func viewStyle(cmd *cobra.Command) style.Style {
	env := invocationOf(cmd).env
	mode := colorMode(cmd, env)
	return style.New(mode == config.ColorAlways || mode == config.ColorAuto && !env.NoColor)
}

// waitRecorded returns the target once the daemon has recorded the exit the
// session saw, or with that exit if it takes too long.
func waitRecorded(ctx context.Context, t attachTarget, exit *holder.Exit) attachTarget {
	deadline := time.Now().Add(attachExitWait)
	for {
		got, err := t.reread(ctx)
		if err == nil && !got.running {
			return got
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			if exit != nil {
				return t.exited(exit.Code)
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
