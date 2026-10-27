// Package cli defines the command tree.
package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srjn45/warden/internal/identity"
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
	root := newRoot(env)
	root.SetArgs(args)
	root.SetIn(env.Stdin)
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)

	// Cobra returns usage errors (bad flags or arguments) before RunE starts.
	started := false
	wrapRun(root, &started)
	root.SetContext(ctx)
	cmd, err := root.ExecuteC()
	if err == nil {
		return exitOK
	}
	if !started {
		err = &usageError{err}
	}

	v, exit := classify(err)
	if v.Code == codeUsage {
		v.Hint = withHelpHint(v.Hint, cmd)
	}
	if wantsJSON(cmd, args) {
		writeJSON(env.Stdout, errorResponse{Error: v})
		return exit
	}
	msg := v.Message
	if prefix := cmd.Annotations[failurePrefix]; prefix != "" {
		msg = prefix + ": " + msg
	}
	if v.Hint != nil {
		msg += "; " + *v.Hint
	}
	fmt.Fprintf(env.Stderr, "%s: %s\n", identity.Name, msg)
	return exit
}

// wrapRun makes every RunE in the tree check the flags --json requires and
// then set started.
func wrapRun(cmd *cobra.Command, started *bool) {
	if run := cmd.RunE; run != nil {
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			if err := checkJSONRequires(cmd); err != nil {
				return err
			}
			*started = true
			return run(cmd, args)
		}
	}
	for _, sub := range cmd.Commands() {
		wrapRun(sub, started)
	}
}

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
