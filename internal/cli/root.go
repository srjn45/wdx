// Package cli defines the command tree.
package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/srjn45/warden/internal/identity"
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
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(env.Stderr, "%s: %v\n", identity.Name, err)
		return 1
	}
	return 0
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
	root.AddCommand(newVersionCmd(), newProjectCmd(identity.HomeDir(env.Getenv)))
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s (%s)\n", identity.Name, identity.Version, identity.Commit)
		},
	}
}
