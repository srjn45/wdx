// Package cli defines the command tree.
package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/srjn45/warden/internal/identity"
)

// failurePrefix is a command annotation: text that starts each of its error
// messages in text mode, such as "not created".
const failurePrefix = "failurePrefix"

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
	markStart(root, &started)
	root.SetContext(ctx)
	cmd, err := root.ExecuteC()
	if err == nil {
		return exitOK
	}
	if !started {
		err = &usageError{err}
	}

	v, exit := classify(err)
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

func markStart(cmd *cobra.Command, started *bool) {
	if run := cmd.RunE; run != nil {
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			*started = true
			return run(cmd, args)
		}
	}
	for _, sub := range cmd.Commands() {
		markStart(sub, started)
	}
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
	noInput, _ := strconv.ParseBool(env.Getenv(identity.Env("NO_INPUT")))
	root.PersistentFlags().Bool("no-input", noInput, "never prompt; fail where a question would be asked (or set "+identity.Env("NO_INPUT")+"=1)")
	root.PersistentFlags().Bool("json", false, "print the result as one JSON object; never prompts")
	root.AddCommand(newVersionCmd(), newProjectCmd(identity.HomeDir(env.Getenv)))
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return printResult(cmd, versionResponse{OK: true, Name: identity.Name, Version: identity.Version, Commit: identity.Commit})
		},
	}
}
