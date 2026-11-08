// Package cli defines the command tree.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/deps"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/observe"
	"github.com/srjn45/wdx/internal/service"
	"github.com/srjn45/wdx/internal/style"
)

// Env is everything a command takes from the process it runs in.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Config         config.Config
	Executable     string // the wdx binary, run to start the daemon
	// Service runs the daemon when it is installed as a service of the OS
	// for the home; the daemon commands manage the process themselves when
	// it is nil.
	Service *service.Manager
	// LookPath finds the container runtime the observe commands run, as
	// exec.LookPath does.
	LookPath func(file string) (string, error)
	// ObservePorts are the ports of the observability stack;
	// observe.DefaultPorts when zero.
	ObservePorts observe.Ports
	// ObservePortCheck replaces trying a port of the stack before starting
	// it, for tests whose fake runtime listens on nothing.
	ObservePortCheck func(port int) error
	// NoColor is true when the terminal asks for no color: NO_COLOR is set
	// or TERM is dumb. It turns off color mode auto only.
	NoColor bool
	// ShellEnv holds the variables terminals take from the daemon's
	// environment, as KEY=value: ShellEnv of it. Never logged.
	ShellEnv []string
	// Environ is the environment, as os.Environ gives it, which the
	// processes the cli starts inherit: the daemon in the background and
	// the binary wdx update checks. Never logged.
	Environ []string
	// WorkDir is the working directory, which names the project of the
	// terminal commands when no project flag does.
	WorkDir string
	// UserHome is the user's home directory, which tide shows paths in as
	// ~; "" when unknown.
	UserHome string
	// TTY opens the controlling terminal, which terminal attach shows a
	// terminal in; it fails when there is none, and is nil in tests.
	TTY func() (*os.File, error)
	// CallerTerm holds the variables that say what the terminal wdx runs
	// in is (TERM, COLORTERM, TERM_PROGRAM), as KEY=value, for the attach
	// view to draw for it. Read by main, as ShellEnv is.
	CallerTerm []string
	// Deps is how the deps commands look at the machine; main sets the
	// session, and the parts left nil take the real ones.
	Deps deps.Probe
	// Timeouts are how long commands wait; tests shorten or lengthen them.
	Timeouts Timeouts

	client *client.Client // the run's, made by clientOf
}

// Timeouts bound how long commands wait for the daemon; a zero field takes
// its default.
type Timeouts struct {
	// Start is how long a started daemon gets to answer: startTimeout.
	Start time.Duration
	// StartExpired, when set, is what ends that time instead: a test gives
	// up at the moment it needs to rather than after a time it must guess.
	StartExpired <-chan time.Time
	// Completion is how long a tab completion waits: completionTimeout.
	Completion time.Duration
	// AttachExit is how long attach waits for the daemon to record an exit:
	// attachExitWait.
	AttachExit time.Duration
	// Holder are how long attach and a terminal's holder wait for each other.
	Holder holder.Limits
	// ProjectStrip is how long tide shows the project strip after a switch.
	ProjectStrip time.Duration
	// UpdatePreflight is how long wdx update gives the new binary to run on
	// the settings before installing it: update's preflightTimeout.
	UpdatePreflight time.Duration
}

// termVars are the variables that describe the terminal wdx runs in.
var termVars = []string{"TERM", "COLORTERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION"}

// CallerTerm returns the variables of environ that describe the terminal.
func CallerTerm(environ []string) []string {
	var vars []string
	for _, kv := range environ {
		if name, _, _ := strings.Cut(kv, "="); slices.Contains(termVars, name) {
			vars = append(vars, kv)
		}
	}
	return vars
}

// terminalVars are the variables of the daemon's environment that terminals
// inherit, LC_* too: what a login shell's profile expects to find, the
// locale, the ssh agent and the desktop session.
var terminalVars = []string{"PATH", "LANG", "SSH_AUTH_SOCK", "DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"}

// ShellEnv returns the variables of environ, as os.Environ gives them,
// that terminals inherit. Empty ones are left out.
func ShellEnv(environ []string) []string {
	var vars []string
	for _, kv := range environ {
		name, value, _ := strings.Cut(kv, "=")
		if value != "" && (slices.Contains(terminalVars, name) || strings.HasPrefix(name, "LC_")) {
			vars = append(vars, kv)
		}
	}
	return vars
}

// Run executes the command line args and returns the process exit code.
func Run(ctx context.Context, args []string, env Env) int {
	return clikit.Run(context.WithValue(ctx, envKey{}, &env), args, frameworkEnv(env, args), func() *cobra.Command { return newRoot(env.Config) })
}

// ConfigError reports the settings that failed to load, before any command
// runs, and returns the exit code.
func ConfigError(args []string, env Env, err error) int {
	return clikit.ConfigError(args, frameworkEnv(env, args), err)
}

// RewritesSettings is true when args run a command that writes the config
// file and err names only keys no setting has: such a command moves them out
// of the way, so it runs on the settings that loaded.
func RewritesSettings(args []string, err error) bool {
	return clikit.RewritesSettings(newRoot(config.Default()), args, err)
}

// frameworkEnv is what the framework takes of env for the run of args.
func frameworkEnv(env Env, args []string) clikit.Env {
	var values []clikit.SettingValue
	for _, s := range config.Settings() {
		values = append(values, clikit.SettingValue{Key: s.Key, Value: env.Config.Value(s.Key), From: env.Config.From[s.Key]})
	}
	return clikit.Env{
		Stdin: env.Stdin, Stdout: env.Stdout, Stderr: env.Stderr,
		Program: clikit.Program{Name: identity.Name, Version: identity.Version, Commit: identity.Commit},
		Logs: clikit.Logs{
			Dir:           identity.LogsDir(env.Config.Home),
			Level:         env.Config.LogLevel,
			RetentionDays: env.Config.LogRetentionDays,
			Process:       processOf(args),
		},
		Settings: values,
		NoColor:  env.NoColor,
		WorkDir:  env.WorkDir,
		Codes: func(code string) (clikit.CodeInfo, bool) {
			info, ok := api.Codes[api.ErrorCode(code)]
			return info, ok
		},
		Problem: problem,
	}
}

// processOf names the process in the log: a holder, which the daemon starts as
// wdx holder and which outlives it, or a command someone ran.
func processOf(args []string) string {
	if len(args) > 0 && args[0] == "holder" {
		return "holder"
	}
	return "cli"
}

type envKey struct{}

// envOf returns the Env of the run cmd is part of.
func envOf(cmd *cobra.Command) *Env {
	if ctx := cmd.Context(); ctx != nil {
		if env, ok := ctx.Value(envKey{}).(*Env); ok {
			return env
		}
	}
	return &Env{}
}

// clientOf returns the client of the daemon serving the configured home. A
// daemon of another version gets a warning on stderr, once.
func clientOf(cmd *cobra.Command) *client.Client {
	env := envOf(cmd)
	if env.client == nil {
		inv := clikit.InvocationOf(cmd)
		env.client = client.New(client.Options{
			Socket:          identity.SocketPath(env.Config.Home),
			DebugID:         inv.DebugID,
			Quiet:           inv.Quiet,
			VersionMismatch: func(daemonVersion string) { warnVersion(cmd, daemonVersion) },
		})
	}
	return env.client
}

// warnVersion warns on stderr that the daemon is of another version.
func warnVersion(cmd *cobra.Command, daemonVersion string) {
	st := clikit.StderrStyle(cmd)
	fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s the daemon is version %s but %s is %s; run %s\n",
		identity.Name, st.Warning("warning:"), daemonVersion, identity.Name, identity.Version, st.Command(identity.Name+" daemon restart"))
}

func newRoot(cfg config.Config) *cobra.Command {
	return clikit.NewRoot(clikit.Root{
		Use:     identity.Name,
		Short:   "Manage AI coding agents across CLIs",
		Long:    "Every command but daemon, observe, setting and update is served by the daemon; " + identity.Name + " daemon start runs it.",
		NoInput: cfg.NoInput,
		Color:   cfg.Color,
		Topics:  topics,
	}, newVersionCmd(), newProjectCmd(cfg.Home), newTerminalCmd(), newAgentCmd(), newWorktreeCmd(), newMessageCmd(), newDaemonCmd(cfg.Home), newObserveCmd(), newSettingCmd(), newUpdateCmd(cfg), newCompletionCmd(), newHolderCmd(), newCodesignCmd(), newTideCmd(), newDepsCmd(), newDocCmd(), newPlanCmd(), newUsageCmd())
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version of " + identity.Name + " and of the daemon",
		Example: clikit.Examples(
			"Show the versions of "+identity.Name+" and of the daemon", identity.Name+" version",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote: "The name, version and commit of " + identity.Name + " and of the daemon, null when it does not answer.\n" +
				`With --json: {"cli": {"name": ..., "version": ..., "commit": ...}, "daemon": {...}}`,
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r := versionResponse{CLI: api.Version{Name: identity.Name, Version: identity.Version, Commit: identity.Commit}}
			v, err := clientOf(cmd).Version(cmd.Context())
			if err != nil && !unavailable(err) {
				return err
			}
			if err == nil {
				r.Daemon = &v
			}
			return clikit.PrintResult(cmd, r)
		},
	}
}

type versionResponse struct {
	CLI    api.Version  `json:"cli"`
	Daemon *api.Version `json:"daemon"` // null when no daemon answers
}

func (r versionResponse) Text(w io.Writer, st style.Style) {
	fmt.Fprintf(w, "%s %s %s\n", r.CLI.Name, st.Name(r.CLI.Version), st.Muted("("+r.CLI.Commit+")"))
	if r.Daemon == nil {
		fmt.Fprintf(w, "daemon %s\n", st.Warning("not running"))
		return
	}
	fmt.Fprintf(w, "daemon %s %s\n", st.Name(r.Daemon.Version), st.Muted("("+r.Daemon.Commit+")"))
}

// workDir is the directory the command runs in.
func workDir(cmd *cobra.Command) (string, error) {
	if dir := envOf(cmd).WorkDir; dir != "" {
		return dir, nil
	}
	return "", errors.New("the working directory is unknown; pass --project-id or --project-name")
}
