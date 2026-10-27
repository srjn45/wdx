// Package cli defines the command tree.
package cli

import (
	"context"
	"fmt"
	"io"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

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
	Getenv         func(string) string
}

// Run executes the command line args and returns the process exit code.
func Run(ctx context.Context, args []string, env Env) int {
	start := time.Now()
	home := identity.HomeDir(env.Getenv)
	debugID := logging.NewDebugID()
	logFile, log := newLogger(env, home, debugID)
	defer logFile.Close()
	logReceived(log, args, env, home)

	root := newRoot(env)
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
	if wantsJSON(cmd, args) {
		writeJSON(env.Stdout, response)
		return exit
	}
	msg := v.Message
	if prefix := cmd.Annotations[failurePrefix]; prefix != "" {
		msg = prefix + ": " + msg
	}
	if v.Hint != nil {
		msg += "; " + *v.Hint
	}
	fmt.Fprintf(env.Stderr, "%s: %s (debug_id %s)\n", identity.Name, msg, debugID)
	return exit
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

func newRoot(env Env) *cobra.Command {
	root := &cobra.Command{
		Use:           identity.Name,
		Short:         "Manage AI coding agents across CLIs",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.CompletionOptions.HiddenDefaultCmd = true
	noInput, _ := strconv.ParseBool(env.Getenv(identity.Env("NO_INPUT")))
	root.PersistentFlags().Bool("no-input", noInput, "never prompt; fail where a question would be asked (or set "+identity.Env("NO_INPUT")+"=1)")
	root.PersistentFlags().Bool("json", false, "print the result as one JSON object; never prompts")
	root.AddCommand(newVersionCmd(), newProjectCmd(identity.HomeDir(env.Getenv)))
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
