package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

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
			"terminals and C-q opens the menu: o opens a project, p switches projects, t\n" +
			"creates a terminal, x removes the terminal of the tab selected after asking,\n" +
			"r reloads tide from the binary installed, keeping what it shows, and q quits;\n" +
			"\\, ] and C-q send C-\\, C-] and C-q to the program in the focused\n" +
			"pane, so C-q \\ d detaches a wdx attach running there. Alt+Shift and ← or → switch to the previous or next open\n" +
			"project.\n" +
			"The mouse focuses a pane, switches tabs and zooms with the [⤢] button;\n" +
			"dragging selects text in a pane and copies it on release.\n" +
			"Projects open from a local repository, are cloned or created, or reopen\n" +
			"when closed. The terminal pane has a tab for each of the project's\n" +
			"terminals, kept up to date as they change; for now a tab and the agent pane\n" +
			"hold placeholders. tide needs the daemon running, runs in the terminal it\n" +
			"was started from and has no --json.",
		Example: examples(
			"Open tide", identity.Name+" tide",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "tide did not start",
			outputNote:    "Once you quit, that tide closed; it gives the window back as it was.",
			resultList:    annotation("closed", "reloaded"),
		},
		RunE: runTide,
	}
	// How C-q r tells the next run what to show: no one types it, and a later
	// release must keep it, since an earlier one reloads into that release.
	cmd.Flags().String(resumeFlag, "", "show what the `state` says, as the run before did")
	_ = cmd.Flags().MarkHidden(resumeFlag)
	_ = cmd.RegisterFlagCompletionFunc(resumeFlag, cobra.NoFileCompletions)
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
	opts := tide.Options{Client: c, Style: viewStyle(cmd), WorkDir: env.WorkDir, UserHome: env.UserHome, ProjectsDir: identity.ProjectsDir(env.Config.Home), Home: env.Config.Home,
		DaemonVersion: daemonVersionCheck(cmd), Resume: resume, StripLinger: env.Timeouts.ProjectStrip}
	if env.Exec != nil {
		opts.Executable = env.Executable
	}
	model := tide.New(ctx, opts)
	p := tea.NewProgram(model, tea.WithInput(tty), tea.WithOutput(tty), tea.WithContext(ctx),
		tea.WithEnvironment(env.CallerTerm), tea.WithoutSignalHandler())
	log.Info("tide started")
	_, err = p.Run()
	killed := errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil // the command is ending
	if next := model.Reload(); next != nil && err == nil {
		state, err := next.String()
		if err != nil {
			return err
		}
		inv.replaceWith = tideArgs(cmd, env, state)
		return printResult(cmd, tideResult{Result: api.ResultReloaded})
	}
	if v := model.DaemonVersion(); style.VersionsDiffer(v) {
		warnTideVersion(cmd, v)
	}
	if err != nil && !killed {
		return err
	}
	return printResult(cmd, tideResult{Result: api.ResultClosed})
}

// tideArgs is the command line of the tide that replaces this one.
func tideArgs(cmd *cobra.Command, env Env, state string) []string {
	args := []string{env.Executable, "tide", "--" + resumeFlag, state}
	if f := cmd.Flags().Lookup("color"); f != nil && f.Changed {
		args = append(args, "--color", f.Value.String())
	}
	return args
}

// tideResult is how tide ended.
type tideResult struct {
	Result api.Result `json:"result"`
}

func (r tideResult) text(w io.Writer, st style.Style) {
	fmt.Fprintf(w, "tide %s\n", st.Success(string(r.Result)))
}
