package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/style"
	"github.com/srjn45/wdx/internal/tide"
)

func newTideCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tide",
		Short: "Work in the terminal IDE: a project, an agent and terminals in one window",
		Long: "tide takes the whole window: the project on the left, an agent on the right\n" +
			"and the project's terminals below. Alt and an arrow move the focus between\n" +
			"them, C-] zooms the focused pane or leaves the zoom, C-\\ shows or hides the\n" +
			"terminals and C-q opens the menu: what acts on the thing selected first, such\n" +
			"as r to rename an agent, then on the left pane's list, then on tide and the\n" +
			"project, such as a new agent or terminal, p for the Project submenu (open,\n" +
			"switch, close, remove) and q to quit;\n" +
			"\\, ] and C-q send C-\\, C-] and C-q to the program in the focused\n" +
			"pane, so C-q \\ d detaches a wdx attach running there, and C-q then Alt and\n" +
			"an arrow, with or without Shift, sends that. Alt+Shift and ← or → switch to\n" +
			"the previous or next open project.\n" +
			"In the left pane's list Enter opens the row's tab and stays in the list;\n" +
			"Shift+Enter, or o where the terminal sends that as Enter, opens it and goes\n" +
			"to it; - closes the row's tab when it is open.\n" +
			"z, or a click on a section's heading, collapses the section or expands it.\n" +
			"The mouse focuses a pane, switches tabs and zooms with the [⤢] button;\n" +
			"dragging selects text in a pane and copies it on release.\n" +
			"The Code section lists a worktree's files: Enter shows one read-only and\n" +
			"highlighted, e edits it in the editor the editor setting names, and closing\n" +
			"that tab asks to save or discard. There n and N create a file or directory,\n" +
			"r renames and x deletes, each in a popup. / filters the left pane's list as you\n" +
			"type: Enter goes back to the list, Esc clears it.\n" +
			"Projects open from a local repository, are cloned or created, or reopen\n" +
			"when closed. The terminal pane has a tab for each of the project's\n" +
			"terminals, kept up to date as they change, and the agent pane a tab for each\n" +
			"agent opened, each live in its pane. tide needs the daemon running, runs in\n" +
			"the terminal it was started from and has no --json. One tide runs per home:\n" +
			"another is refused.",
		Example: examples(
			"Open tide", identity.Name+" tide",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "tide did not start",
			outputNote:    "Once you quit, that tide closed; it gives the window back as it was.",
			resultList:    annotation("closed"),
			errorList:     codeList(api.ErrorCodeTideRunning),
		},
		RunE: runTide,
	}
	// How an older release's C-q r tells the next run what to show: no one types
	// it, and every release must keep it, since an older one reloads into it.
	cmd.Flags().String(resumeFlag, "", "show what the `state` says, as the run before did")
	_ = cmd.Flags().MarkHidden(resumeFlag)
	completeFlag(cmd, resumeFlag, cobra.NoFileCompletions)
	return cmd
}

const resumeFlag = "resume"

// daemonVersionCheck returns how a view that stays open asks the daemon its
// version: at debug level on both sides, since it asks all the time. It has
// its own client so that the answer warns of nothing.
func daemonVersionCheck(cmd *cobra.Command) func(context.Context) (string, error) {
	inv := invocationOf(cmd)
	c := client.New(client.Options{Socket: identity.SocketPath(inv.env.Config.Home), DebugID: inv.debugID, Quiet: true})
	return func(ctx context.Context) (string, error) {
		ctx = logging.NewContext(ctx, logging.Quiet(logging.FromContext(ctx)))
		ctx, cancel := context.WithTimeout(ctx, tide.VersionCheckTimeout)
		defer cancel()
		v, err := c.Version(ctx)
		return v.Version, err
	}
}

// warnTideVersion warns on stderr that the daemon is of another version than
// the tide that ended, which ran for long enough for either to be the old one.
func warnTideVersion(cmd *cobra.Command, daemonVersion string) {
	st := stderrStyle(cmd)
	fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s the daemon is version %s but this tide was %s; open tide again if the daemon is the newer, or run %s if it is the older\n",
		identity.Name, st.Warning("warning:"), daemonVersion, identity.Version, st.Command(identity.Name+" daemon restart"))
}

func runTide(cmd *cobra.Command, _ []string) error {
	if jsonMode(cmd) {
		return &usageError{errors.New("tide is interactive and has no --json")}
	}
	env := invocationOf(cmd).env
	lock, err := lockTide(env.Config.Home)
	if err != nil {
		return err
	}
	defer lock.Close()
	logging.FromContext(cmd.Context()).Info("tide lock taken", "path", lock.Name())
	if env.TTY == nil {
		return &usageError{errors.New("no terminal to run tide in: run it in one")}
	}
	tty, err := env.TTY()
	if err != nil {
		return &usageError{fmt.Errorf("no terminal to run tide in: %w", err)}
	}
	defer tty.Close()

	ctx := cmd.Context()
	log := logging.FromContext(ctx)
	inv := invocationOf(cmd)
	var resume *tide.Resume
	if state, _ := cmd.Flags().GetString(resumeFlag); state != "" {
		r, err := tide.ParseResume(state)
		if err != nil {
			log.Info("tide state not resumed", "err", err) // it starts as it would without
		}
		resume = r
	}
	// The status line says when the daemon is of another version, and the
	// warning, which would land in the window, waits until tide gives it back.
	c := client.New(client.Options{Socket: identity.SocketPath(env.Config.Home), DebugID: inv.debugID})
	_, _ = io.WriteString(tty, titlePush)
	defer func() { _, _ = io.WriteString(tty, titlePop) }()
	opts := tide.Options{Client: c, Style: viewStyle(cmd).WithTheme(env.Config.TideTheme), WorkDir: env.WorkDir, UserHome: env.UserHome, ProjectsDir: identity.ProjectsDir(env.Config.Home), Home: env.Config.Home,
		DaemonVersion: daemonVersionCheck(cmd), Resume: resume, StripLinger: env.Timeouts.ProjectStrip,
		SectionOrder: env.Config.TideSectionOrder, ContextWarning: env.Config.ContextWarning, ContextCritical: env.Config.ContextCritical}
	// Tide's requests end with it: those it left in flight are cancelled once it
	// quits, and waited for, so that each is logged as answered or given up.
	requests, cancelRequests := context.WithCancel(ctx)
	defer cancelRequests()
	model := tide.New(requests, opts)
	p := tea.NewProgram(model, tea.WithInput(tty), tea.WithOutput(tty), tea.WithContext(ctx),
		tea.WithEnvironment(env.CallerTerm), tea.WithoutSignalHandler())
	log.Info("tide started")
	_, err = p.Run()
	cancelRequests()
	c.Wait()
	killed := errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil // the command is ending
	if v := model.DaemonVersion(); style.VersionsDiffer(v) {
		warnTideVersion(cmd, v)
	}
	if err != nil && !killed {
		return err
	}
	return printResult(cmd, tideResult{Result: api.ResultClosed})
}

// tideResult is how tide ended.
type tideResult struct {
	Result api.Result `json:"result"`
}

func (r tideResult) text(w io.Writer, st style.Style) {
	fmt.Fprintf(w, "tide %s\n", st.Success(string(r.Result)))
}

// tideRunningError is a tide that did not start, as another runs for the home.
type tideRunningError struct{ pid int }

func (e *tideRunningError) Error() string {
	if e.pid == 0 {
		return "a tide is already running for this home"
	}
	return fmt.Sprintf("a tide is already running for this home, as pid %d", e.pid)
}

// tideProblem returns the problem of a tide that did not start, if err is one.
func tideProblem(err error) (api.Problem, bool) {
	e, ok := errors.AsType[*tideRunningError](err)
	if !ok {
		return api.Problem{}, false
	}
	p := api.NewProblem(api.ErrorCodeTideRunning, err.Error())
	hint := "go to the window it runs in, or quit it there with C-q q"
	p.Hint = &hint
	if e.pid != 0 {
		p.Details = &map[string]any{"pid": e.pid}
	}
	return p, true
}

// lockTide takes the home's tide lock, held until the file returned is closed,
// and writes this process's pid into it. The operating system frees it when the
// process ends, killed or not, so no tide that died leaves it held.
func lockTide(home string) (*os.File, error) {
	path := identity.TideLockPath(home)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			b, _ := os.ReadFile(path)
			pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			return nil, &tideRunningError{pid: pid}
		}
		return nil, err
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
