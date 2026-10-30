package cli

import (
	"errors"
	"fmt"
	"io"
	"sync/atomic"

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
	return &cobra.Command{
		Use:   "tide",
		Short: "Work in the terminal IDE: a project, an agent and terminals in one window",
		Long: "tide takes the whole window: the project on the left, an agent on the right\n" +
			"and the project's terminals below. Alt and an arrow move the focus between\n" +
			"them, C-] zooms the focused pane or leaves the zoom, C-\\ shows or hides the\n" +
			"terminals and C-q opens the menu: o opens a project, p switches projects and\n" +
			"q quits. Alt+Shift and ← or → switch to the previous or next open project.\n" +
			"The mouse focuses a pane, switches tabs and zooms with the [⤢] button.\n" +
			"Projects open from a local repository, are cloned or created, or reopen\n" +
			"when closed. For now the agent and terminal panes hold placeholders. tide\n" +
			"needs the daemon running, runs in the terminal it was started from and has\n" +
			"no --json.",
		Example: examples(
			"Open tide", identity.Name+" tide",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "tide did not start",
			outputNote:    "Once you quit, that tide closed; it gives the window back as it was.",
			resultList:    annotation("closed"),
		},
		RunE: runTide,
	}
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
	// A warning written while tide holds the window would land in it: it
	// waits until tide gives the window back.
	var daemonVersion atomic.Pointer[string]
	c := client.New(client.Options{
		Socket:          identity.SocketPath(env.Config.Home),
		DebugID:         inv.debugID,
		VersionMismatch: func(v string) { daemonVersion.Store(&v) },
	})
	_, _ = io.WriteString(tty, titlePush)
	defer func() { _, _ = io.WriteString(tty, titlePop) }()
	model := tide.New(ctx, tide.Options{Client: c, Style: viewStyle(cmd), WorkDir: env.WorkDir, UserHome: env.UserHome, ProjectsDir: identity.ProjectsDir(env.Config.Home)})
	p := tea.NewProgram(model, tea.WithInput(tty), tea.WithOutput(tty), tea.WithContext(ctx),
		tea.WithEnvironment(env.CallerTerm), tea.WithoutSignalHandler())
	log.Info("tide started")
	_, err = p.Run()
	if v := daemonVersion.Load(); v != nil {
		warnVersion(cmd, *v)
	}
	killed := errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil // the command is ending
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
