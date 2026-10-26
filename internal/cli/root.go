// Package cli defines the command tree.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/srjn45/warden/internal/identity"
)

// NewRoot builds the root command.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           identity.Name,
		Short:         "Manage AI coding agents across CLIs",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
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
