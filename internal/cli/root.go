// Package cli defines the command tree.
package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/observe"
	"github.com/srjn45/wdx/internal/service"
	"github.com/srjn45/wdx/internal/style"
)

// Command annotations. Lists are space-separated.
const (
	// failurePrefix starts each error message in text mode, such as "not created".
	failurePrefix = "failurePrefix"
	// outputNote tells what the command prints on success.
	outputNote = "output"
	// resultList holds the results the command can return.
	resultList = "results"
	// errorList holds the error codes the command can return besides usage
	// and internal.
	errorList = "errors"
	// jsonRequires holds flags one of which --json requires, after
	// "<flag>:" when only together with that flag.
	jsonRequires = "jsonRequires"
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
	// NoColor is true when the terminal asks for no color: NO_COLOR is set
	// or TERM is dumb. It turns off color mode auto only.
	NoColor bool
	// Terminal holds the variables terminals take from the daemon's
	// environment, as KEY=value: TerminalEnv of it. Never logged.
	Terminal []string
	// WorkDir is the working directory, which names the project of the
	// terminal commands when no project flag does.
	WorkDir string
	// TTY opens the controlling terminal, which terminal attach shows a
	// terminal in; nil when there is none.
	TTY func() (*os.File, error)
}

// terminalVars are the variables of the daemon's environment that terminals
// inherit, LC_* too: what a login shell's profile expects to find, the
// locale, the ssh agent and the desktop session.
var terminalVars = []string{"PATH", "LANG", "SSH_AUTH_SOCK", "DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"}

// TerminalEnv returns the variables of environ, as os.Environ gives them,
// that terminals inherit. Empty ones are left out.
func TerminalEnv(environ []string) []string {
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
	start := time.Now()
	debugID := logging.NewDebugID()
	logFile, log := newLogger(env.Config, debugID)
	defer logFile.Close()
	// A shell asks on every Tab: worth logging only when debugging.
	completion := isCompletion(args)
	var printed bytes.Buffer
	if completion {
		log = logging.Quiet(log)
		env.Stdout = io.MultiWriter(env.Stdout, &printed)
	}
	logReceived(log, args, env)

	root := newRoot(env.Config)
	root.SetArgs(args)
	root.SetIn(env.Stdin)
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)
	wrapRun(root)
	inv := &invocation{env: env, debugID: debugID, quiet: completion}
	root.SetContext(logging.NewContext(context.WithValue(ctx, invocationKey{}, inv), log))
	cmd, err := root.ExecuteC()
	if completion {
		inv.response = completionResponse(printed.String())
	}
	if err == nil {
		logReturned(log, cmd, start, api.ExitOK, "", inv.response, nil)
		return api.ExitOK
	}
	// Cobra returns usage errors (bad flags or arguments) before RunE starts.
	if !inv.started {
		err = &usageError{err}
	}

	p, exit := problem(err)
	p.DebugId = debugID
	if p.Code == api.ErrorCodeUsage {
		p.Hint = withHelpHint(p.Hint, cmd)
	}
	logReturned(log, cmd, start, exit, p.Code, p, err)
	asJSON := wantsJSON(cmd, args)
	printError(env, asJSON, style.New(!asJSON && colorOn(cmd, env, env.Stderr)), p, cmd.Annotations[failurePrefix])
	return exit
}

// ConfigError reports the settings that failed to load, before any command
// runs, and returns the exit code.
func ConfigError(args []string, env Env, err error) int {
	p := api.NewProblem(api.ErrorCodeUsage, err.Error())
	hint := "fix or remove them"
	p.Hint = &hint
	if e, ok := errors.AsType[*config.Error](err); ok {
		p = settingsProblem(e)
	}
	asJSON := slices.Contains(args, "--json")
	// The color setting may be among those that failed.
	st := style.New(!asJSON && !env.NoColor && isTerminal(env.Stderr))
	printError(env, asJSON, st, p, "")
	return api.Codes[api.ErrorCodeUsage].Exit
}

// printError writes p to stdout as JSON, or to stderr as text: what failed on
// the first line, then the hint and the debug id.
func printError(env Env, asJSON bool, st style.Style, p api.Problem, prefix string) {
	if asJSON {
		_ = writeJSON(env.Stdout, p)
		return
	}
	msg := p.Detail
	if prefix != "" {
		msg = prefix + ": " + msg
	}
	fmt.Fprintf(env.Stderr, "%s %s  %s\n", st.Failure(identity.Name+":"), msg, st.Failure("["+string(p.Code)+"]"))
	if p.Hint != nil {
		fmt.Fprintf(env.Stderr, "  %s %s\n", st.Hint("hint:"), hintText(st, *p.Hint))
	}
	if p.DebugId != "" {
		fmt.Fprintf(env.Stderr, "  %s\n", st.Muted("debug_id: "+p.DebugId))
	}
}

var commandOrFlag = regexp.MustCompile(identity.Name + `( [a-z][a-z-]*)*|--[a-z][a-z-]*`)

// hintText styles the command lines and flags in hint.
func hintText(st style.Style, hint string) string {
	return commandOrFlag.ReplaceAllStringFunc(hint, func(m string) string {
		if strings.HasPrefix(m, "--") {
			return st.Flag(m)
		}
		return st.Command(m)
	})
}

// colorOn reports whether cmd colors the text it writes to w: never with
// --json, else as --color or the color setting says.
func colorOn(cmd *cobra.Command, env Env, w io.Writer) bool {
	if jsonMode(cmd) {
		return false
	}
	mode := env.Config.Color
	if f := cmd.Flags().Lookup("color"); f != nil && f.Changed {
		mode = config.ColorMode(f.Value.String())
	}
	switch mode {
	case config.ColorAlways:
		return true
	case config.ColorNever:
		return false
	}
	return !env.NoColor && isTerminal(w)
}

// stdoutStyle styles what cmd prints to stdout.
func stdoutStyle(cmd *cobra.Command) style.Style {
	return style.New(colorOn(cmd, invocationOf(cmd).env, cmd.OutOrStdout()))
}

// stderrStyle styles what cmd prints to stderr.
func stderrStyle(cmd *cobra.Command) style.Style {
	return style.New(colorOn(cmd, invocationOf(cmd).env, cmd.ErrOrStderr()))
}

// colorFlag is the value of --color.
type colorFlag struct {
	mode *config.ColorMode
}

func (f colorFlag) String() string { return string(*f.mode) }
func (f colorFlag) Type() string   { return "string" }

func (f colorFlag) Set(v string) (err error) {
	m, err := config.ParseColorMode(v)
	if err != nil {
		return err
	}
	*f.mode = m
	return nil
}

// invocation is what Run learns from the command it executes, and what the
// command takes from Run.
type invocation struct {
	env      Env
	debugID  string
	started  bool // RunE was reached, so the command line was valid
	quiet    bool // the shell asks for completions; the daemon logs at debug
	response any  // what the command printed
	client   *client.Client
}

type invocationKey struct{}

func invocationOf(cmd *cobra.Command) *invocation {
	if inv, ok := cmd.Context().Value(invocationKey{}).(*invocation); ok {
		return inv
	}
	return &invocation{}
}

// clientOf returns the client of the daemon serving the configured home. A
// daemon of another version gets a warning on stderr, once.
func clientOf(cmd *cobra.Command) *client.Client {
	inv := invocationOf(cmd)
	if inv.client == nil {
		inv.client = client.New(client.Options{
			Socket:  identity.SocketPath(inv.env.Config.Home),
			DebugID: inv.debugID,
			Quiet:   inv.quiet,
			VersionMismatch: func(daemonVersion string) {
				st := stderrStyle(cmd)
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s the daemon is version %s but %s is %s; run %s\n",
					identity.Name, st.Warning("warning:"), daemonVersion, identity.Name, identity.Version, st.Command(identity.Name+" daemon restart"))
			},
		})
	}
	return inv.client
}

// wrapRun makes every RunE in the tree log its start, check the flags --json
// requires and turn a panic into an error.
func wrapRun(cmd *cobra.Command) {
	if run := cmd.RunE; run != nil {
		cmd.RunE = func(cmd *cobra.Command, args []string) (err error) {
			logStarted(cmd, args)
			if err := checkJSONRequires(cmd); err != nil {
				return err
			}
			invocationOf(cmd).started = true
			defer func() {
				if r := recover(); r != nil {
					err = &panicError{value: r, stack: debug.Stack()}
				}
			}()
			return run(cmd, args)
		}
	}
	for _, sub := range cmd.Commands() {
		wrapRun(sub)
	}
}

// panicError is a panic recovered while a command ran.
type panicError struct {
	value any
	stack []byte
}

func (e *panicError) Error() string { return fmt.Sprintf("panic: %v", e.value) }

// checkJSONRequires fails when --json is given without one of the flags that
// answer the command's prompts.
func checkJSONRequires(cmd *cobra.Command) error {
	rule, ok := jsonRule(cmd)
	if !ok || !jsonMode(cmd) || (rule.When != nil && !cmd.Flags().Changed(*rule.When)) {
		return nil
	}
	for _, name := range rule.Flags {
		if cmd.Flags().Changed(name) {
			return nil
		}
	}
	flags := "--" + strings.Join(rule.Flags, " or --")
	given := "--json"
	if rule.When != nil {
		given += " with --" + *rule.When
	}
	return &hinted{&usageError{fmt.Errorf("%s requires %s", given, flags)}, "pass " + flags}
}

func withHelpHint(hint *string, cmd *cobra.Command) *string {
	if cmd.Name() == "help" && cmd.HasParent() {
		cmd = cmd.Parent()
	}
	h := fmt.Sprintf("see '%s --help'", cmd.CommandPath())
	if hint != nil {
		h = *hint + "; " + h
	}
	return &h
}

// wantsJSON reports whether --json was given; args are checked too, since a
// usage error may stop cobra before it parses the flag.
func wantsJSON(cmd *cobra.Command, args []string) bool {
	if f := cmd.Flags().Lookup("json"); f != nil && f.Changed {
		return f.Value.String() == "true"
	}
	return slices.Contains(args, "--json")
}

func newRoot(cfg config.Config) *cobra.Command {
	root := &cobra.Command{
		Use:           identity.Name,
		Short:         "Manage AI coding agents across CLIs",
		Long:          "Every command but daemon, observe, settings and update is served by the daemon; " + identity.Name + " daemon start runs it.",
		Args:          noArgs,
		RunE:          func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().Bool("no-input", cfg.NoInput, "never prompt; fail where a question would be asked")
	root.PersistentFlags().Bool("json", false, "print the result as one JSON object; never prompts")
	color := cfg.Color
	root.PersistentFlags().Var(colorFlag{&color}, "color", "color text output in this `mode`: auto, always or never")
	root.RegisterFlagCompletionFunc("color", completeColor)
	root.AddCommand(newVersionCmd(), newProjectCmd(cfg.Home), newTerminalCmd(), newDaemonCmd(cfg.Home), newObserveCmd(), newSettingsCmd(), newUpdateCmd(cfg), newCompletionCmd(), newHolderCmd())
	setHelp(root)
	root.InitDefaultHelpCmd()
	setCompletions(root)
	return root
}

// noArgs rejects arguments: a command with subcommands takes none, so an
// argument is a mistyped subcommand, and the closest one is suggested.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	err := &usageError{fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())}
	if s := suggestions(cmd, args[0]); len(s) > 0 {
		return &hinted{err, "did you mean " + cmd.CommandPath() + " " + s[0]}
	}
	return err
}

// suggestions returns the subcommands of cmd that name may be a typo of.
func suggestions(cmd *cobra.Command, name string) []string {
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
	return cmd.SuggestionsFor(name)
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version of " + identity.Name + " and of the daemon",
		Example: examples(
			"Show the versions of "+identity.Name+" and of the daemon", identity.Name+" version",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The name, version and commit of " + identity.Name + " and of the daemon, null when it does not answer.\n" +
				`With --json: {"cli": {"name": ..., "version": ..., "commit": ...}, "daemon": {...}}`,
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r := versionResponse{CLI: api.Version{Name: identity.Name, Version: identity.Version, Commit: identity.Commit}}
			v, err := clientOf(cmd).Version(cmd.Context())
			if _, unavailable := errors.AsType[*client.UnavailableError](err); err != nil && !unavailable {
				return err
			}
			if err == nil {
				r.Daemon = &v
			}
			return printResult(cmd, r)
		},
	}
}
