package clikit

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/style"
)

// Root is what a program's root command says of itself.
type Root struct {
	Use, Short, Long string
	NoInput          bool            // what --no-input defaults to: the no_input setting
	Color            style.ColorMode // what --color defaults to: the color setting
	Topics           []Topic
}

// NewRoot returns the root with the persistent flags --no-input, --json and
// --color, the commands, the help in wdx's layout with the topics, the help
// command, and no file completion where a command completes nothing else.
func NewRoot(r Root, commands ...*cobra.Command) *cobra.Command {
	root := &cobra.Command{
		Use:           r.Use,
		Short:         r.Short,
		Long:          r.Long,
		Args:          NoArgs,
		RunE:          func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().Bool("no-input", r.NoInput, "never prompt; fail where a question would be asked")
	root.PersistentFlags().Bool("json", false, "print the result as one JSON object; never prompts")
	color := r.Color
	root.PersistentFlags().Var(colorFlag{&color}, "color", "color text output in this `mode`: auto, always or never")
	CompleteFlag(root, "color", completeColor)
	root.AddCommand(commands...)
	setHelp(root, r.Topics)
	root.InitDefaultHelpCmd()
	setCompletions(root)
	return root
}

// NoArgs is the Args of a command with subcommands: an argument is a
// mistyped subcommand, and the closest is suggested.
func NoArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	err := &UsageError{fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())}
	if s := suggestions(cmd, args[0]); len(s) > 0 {
		return &Hinted{err, "did you mean " + cmd.CommandPath() + " " + s[0]}
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
