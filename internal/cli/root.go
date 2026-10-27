// Package cli defines the command tree.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/warden/internal/config"
	"github.com/srjn45/warden/internal/identity"
	"github.com/srjn45/warden/internal/logging"
)

// Command annotations. Lists are space-separated.
const (
	// failurePrefix starts each error message in text mode, such as "not created".
	failurePrefix = "failurePrefix"
	// outputNote tells what the command prints on success.
	outputNote = "output"
	// actionList holds the actions the command can return.
	actionList = "actions"
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
}

// Run executes the command line args and returns the process exit code.
func Run(ctx context.Context, args []string, env Env) int {
	start := time.Now()
	debugID := logging.NewDebugID()
	logFile, log := newLogger(env.Config, debugID)
	defer logFile.Close()
	logReceived(log, args, env)

	root := newRoot(env.Config)
	root.SetArgs(args)
	root.SetIn(env.Stdin)
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)
	wrapRun(root)
	inv := &invocation{}
	root.SetContext(logging.NewContext(context.WithValue(ctx, invocationKey{}, inv), log))
	cmd, err := root.ExecuteC()
	if err == nil {
		logReturned(log, cmd, start, exitOK, inv.response, nil)
		return exitOK
	}
	// Cobra returns usage errors (bad flags or arguments) before RunE starts.
	if !inv.started {
		err = &usageError{err}
	}

	v, exit := classify(err)
	v.DebugID = debugID
	if v.Code == codeUsage {
		v.Hint = withHelpHint(v.Hint, cmd)
	}
	response := errorResponse{Error: v}
	logReturned(log, cmd, start, exit, response, err)
	printError(env, wantsJSON(cmd, args), response, cmd.Annotations[failurePrefix])
	return exit
}

// ConfigError reports the settings that failed to load, before any command
// runs, and returns the exit code.
func ConfigError(args []string, env Env, err error) int {
	hint := "fix or unset them"
	v := errorView{Code: codeUsage, Message: err.Error(), Hint: &hint}
	if e, ok := errors.AsType[*config.Error](err); ok {
		v.Details = map[string]any{"settings": e.Problems}
	}
	printError(env, slices.Contains(args, "--json"), errorResponse{Error: v}, "")
	return codes[codeUsage].exit
}

// printError writes r to stdout as JSON or to stderr as one line of text.
func printError(env Env, asJSON bool, r errorResponse, prefix string) {
	if asJSON {
		writeJSON(env.Stdout, r)
		return
	}
	v := r.Error
	msg := v.Message
	if prefix != "" {
		msg = prefix + ": " + msg
	}
	if v.Hint != nil {
		msg += "; " + *v.Hint
	}
	if v.DebugID != "" {
		msg += " (debug_id " + v.DebugID + ")"
	}
	fmt.Fprintf(env.Stderr, "%s: %s\n", identity.Name, msg)
}

// invocation is what Run learns from the command it executes.
type invocation struct {
	started  bool // RunE was reached, so the command line was valid
	response any  // what the command printed
}

type invocationKey struct{}

func invocationOf(cmd *cobra.Command) *invocation {
	if inv, ok := cmd.Context().Value(invocationKey{}).(*invocation); ok {
		return inv
	}
	return &invocation{}
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
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.CompletionOptions.HiddenDefaultCmd = true
	root.PersistentFlags().Bool("no-input", cfg.NoInput, "never prompt; fail where a question would be asked (or set "+config.EnvVar("no_input")+"=1)")
	root.PersistentFlags().Bool("json", false, "print the result as one JSON object; never prompts")
	root.AddCommand(newVersionCmd(), newProjectCmd(cfg.Home))
	root.InitDefaultHelpCmd()
	for _, cmd := range root.Commands() {
		if cmd.Name() == "help" {
			cmd.Hidden = true
		}
	}
	// cobra's template lists the help command even when it is hidden.
	root.SetUsageTemplate(strings.ReplaceAll(root.UsageTemplate(), `(or .IsAvailableCommand (eq .Name "help"))`, `.IsAvailableCommand`))
	setHelp(root)
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The name, version and commit of " + identity.Name + ".\n" + `With --json: {"ok": true, "name": ..., "version": ..., "commit": ...}`,
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return printResult(cmd, versionResponse{OK: true, Name: identity.Name, Version: identity.Version, Commit: identity.Commit})
		},
	}
}
