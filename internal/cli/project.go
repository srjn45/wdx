package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/srjn45/warden/internal/db"
	"github.com/srjn45/warden/internal/identity"
	"github.com/srjn45/warden/internal/project"
)

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects",
	}
	cmd.AddCommand(newProjectOpenCmd(), newProjectCloseCmd(), newProjectRemoveCmd())
	return cmd
}

func newProjectOpenCmd() *cobra.Command {
	var local, name string
	cmd := &cobra.Command{
		Use:   "open --local <path>",
		Short: "Open a git repository as a project",
		Long: "Open a git repository as a project. A path inside a repository opens the\n" +
			"repository root. Worktrees and other clones of a known repository join\n" +
			"its project. Opening a known project marks it open again.\n\n" +
			"Projects are named after their root directory unless --name is given.\n" +
			"Names are unique.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			d, err := db.Open(ctx, identity.DBPath())
			if err != nil {
				return err
			}
			defer d.Close()

			p, err := project.NewStore(d).OpenLocal(ctx, local, name)
			if err != nil {
				return err
			}
			printProject(cmd.OutOrStdout(), "opened", p)
			return nil
		},
	}
	cmd.Flags().StringVar(&local, "local", "", "path to a local git repository")
	cmd.Flags().StringVar(&name, "name", "", "project name (default: the repository directory name)")
	cmd.MarkFlagRequired("local")
	return cmd
}

func newProjectCloseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "close <name|id>",
		Short: "Close a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			d, err := db.Open(ctx, identity.DBPath())
			if err != nil {
				return err
			}
			defer d.Close()

			p, err := project.NewStore(d).Close(ctx, args[0])
			if err != nil {
				return err
			}
			printProject(cmd.OutOrStdout(), "closed", p)
			return nil
		},
	}
}

func newProjectRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name|id>",
		Short: "Delete a project and everything that belongs to it",
		Long: "Delete a project and everything that belongs to it from " + identity.Name + ".\n" +
			"Files on disk are not touched.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			d, err := db.Open(ctx, identity.DBPath())
			if err != nil {
				return err
			}
			defer d.Close()

			p, err := project.NewStore(d).Remove(ctx, args[0])
			if err != nil {
				return err
			}
			printProject(cmd.OutOrStdout(), "removed", p)
			return nil
		},
	}
}

func printProject(w io.Writer, verb string, p project.Project) {
	origin := p.OriginURL
	if origin == "" {
		origin = "(none)"
	}
	fmt.Fprintf(w, "%s project %s (%s)\n", verb, p.Name, p.ID)
	fmt.Fprintf(w, "  origin:    %s\n", origin)
	for i, wt := range p.Worktrees {
		label := "worktrees:"
		if i > 0 {
			label = ""
		}
		fmt.Fprintf(w, "  %-10s %s\n", label, wt.Path)
	}
}
