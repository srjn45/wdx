package cli

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/srjn45/warden/internal/db"
	"github.com/srjn45/warden/internal/identity"
	"github.com/srjn45/warden/internal/project"
)

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects",
	}
	cmd.AddCommand(newProjectCreateCmd(), newProjectOpenCmd(), newProjectCloseCmd(), newProjectRemoveCmd())
	return cmd
}

func newProjectCreateCmd() *cobra.Command {
	var name, parent string
	cmd := &cobra.Command{
		Use:   "create --name <name> [--path <dir>]",
		Short: "Create a project in a new git repository",
		Long: "Create an empty git repository at <dir>/<name> and open it as a project.\n" +
			"<dir> defaults to " + identity.ProjectsDir() + ". An existing directory that\n" +
			"is not in a git repository is initialized in place.\n\n" +
			"If the name is taken or the directory is in a git repository, you are\n" +
			"asked to open that project or choose another name.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			d, err := db.Open(ctx, identity.DBPath())
			if err != nil {
				return err
			}
			defer d.Close()

			store := project.NewStore(d, identity.ProjectsDir())
			answers := bufio.NewScanner(cmd.InOrStdin())
			for {
				o, err := store.Create(ctx, name, parent)
				if err == nil {
					printProject(cmd.OutOrStdout(), "created", o.Project)
					return nil
				}
				var open func() (project.Opened, error)
				if taken, ok := errors.AsType[*project.NameTakenError](err); ok {
					open = func() (project.Opened, error) { return store.Open(ctx, taken.Project.ID) }
				} else if exists, ok := errors.AsType[*project.RepoExistsError](err); ok {
					open = func() (project.Opened, error) { return store.OpenLocal(ctx, exists.Root, "") }
				} else {
					return err
				}
				if !isTerminal(cmd.InOrStdin()) {
					return fmt.Errorf("%w; run in a terminal to open it, or choose another --name", err)
				}

				prompt := cmd.ErrOrStderr()
				fmt.Fprintln(prompt, err)
				fmt.Fprintln(prompt, "[o] open it")
				fmt.Fprintln(prompt, "[n] try a different name")
				fmt.Fprintln(prompt, "[c] cancel")
				choice, err := ask(prompt, answers, "choose [o/n/c]: ", "o", "n", "c")
				if err != nil {
					return err
				}
				switch choice {
				case "c":
					return errCancelled
				case "n":
					if name, err = ask(prompt, answers, "new name (empty to cancel): "); err != nil {
						return err
					}
					if name == "" {
						return errCancelled
					}
					continue
				}
				o, err = open()
				if missing, ok := errors.AsType[*project.CheckoutMissingError](err); ok {
					return resolveMissingCheckout(cmd, store, missing)
				}
				if err != nil {
					return err
				}
				printOpened(cmd.OutOrStdout(), o)
				return nil
			}
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "project name, also the repository directory name")
	cmd.Flags().StringVar(&parent, "path", "", "directory to create the repository in (default "+identity.ProjectsDir()+")")
	cmd.MarkFlagRequired("name")
	return cmd
}

var errCancelled = errors.New("cancelled")

// ask prompts until one of choices is given; with no choices any answer,
// even an empty one, is accepted.
func ask(prompt io.Writer, answers *bufio.Scanner, question string, choices ...string) (string, error) {
	for {
		fmt.Fprint(prompt, question)
		if !answers.Scan() {
			return "", cmp.Or(answers.Err(), errors.New("no answer given"))
		}
		answer := strings.TrimSpace(answers.Text())
		if len(choices) == 0 {
			return answer, nil
		}
		if answer = strings.ToLower(answer); slices.Contains(choices, answer) {
			return answer, nil
		}
	}
}

func newProjectOpenCmd() *cobra.Command {
	var local, gitURL, name string
	cmd := &cobra.Command{
		Use:   "open (--local <path> | --git <url>)",
		Short: "Open a git repository as a project",
		Long: "Open a git repository as a project. A path inside a repository opens the\n" +
			"repository root. Worktrees and other clones of a known repository join\n" +
			"its project. Opening a known project marks it open again and drops its\n" +
			"checkouts that are no longer on disk.\n\n" +
			"--git opens the project with that origin, or clones it into\n" +
			identity.ProjectsDir() + "/<name> if it is unknown. If none of a known\n" +
			"project's checkouts are left, you are asked to clone it again or remove it.\n\n" +
			"Projects are named after their root directory or URL unless --name is\n" +
			"given. Names are unique.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			d, err := db.Open(ctx, identity.DBPath())
			if err != nil {
				return err
			}
			defer d.Close()

			store := project.NewStore(d, identity.ProjectsDir())
			var o project.Opened
			if local != "" {
				o, err = store.OpenLocal(ctx, local, name)
			} else {
				o, err = store.OpenGit(ctx, gitURL, name, cmd.ErrOrStderr())
			}
			if missing, ok := errors.AsType[*project.CheckoutMissingError](err); ok {
				return resolveMissingCheckout(cmd, store, missing)
			}
			if err != nil {
				return err
			}
			printOpened(cmd.OutOrStdout(), o)
			return nil
		},
	}
	cmd.Flags().StringVar(&local, "local", "", "path to a local git repository")
	cmd.Flags().StringVar(&gitURL, "git", "", "clone URL of a git repository")
	cmd.Flags().StringVar(&name, "name", "", "project name (default: the repository directory or URL name)")
	cmd.MarkFlagsOneRequired("local", "git")
	cmd.MarkFlagsMutuallyExclusive("local", "git")
	return cmd
}

// resolveMissingCheckout asks whether to clone the project again or remove it.
func resolveMissingCheckout(cmd *cobra.Command, store *project.Store, missing *project.CheckoutMissingError) error {
	ctx := cmd.Context()
	p := missing.Project
	if !isTerminal(cmd.InOrStdin()) {
		return fmt.Errorf("%w; run in a terminal to clone it again, or remove it with %s project remove %s",
			missing, identity.Name, p.Name)
	}

	prompt := cmd.ErrOrStderr()
	fmt.Fprintf(prompt, "%s (was %s)\n", missing, p.ClonePath())
	fmt.Fprintf(prompt, "[c] clone again to %s\n", p.ClonePath())
	fmt.Fprintf(prompt, "[r] remove from %s\n", identity.Name)
	choice, err := ask(prompt, bufio.NewScanner(cmd.InOrStdin()), "choose [c/r]: ", "c", "r")
	if err != nil {
		return err
	}
	if choice == "c" {
		o, err := store.Reclone(ctx, p.ID, prompt)
		if err != nil {
			return err
		}
		printOpened(cmd.OutOrStdout(), o)
		return nil
	}
	removed, err := store.Remove(ctx, p.ID)
	if err != nil {
		return err
	}
	printProject(cmd.OutOrStdout(), "removed", removed)
	return nil
}

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
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

			p, err := project.NewStore(d, identity.ProjectsDir()).Close(ctx, args[0])
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

			p, err := project.NewStore(d, identity.ProjectsDir()).Remove(ctx, args[0])
			if err != nil {
				return err
			}
			printProject(cmd.OutOrStdout(), "removed", p)
			return nil
		},
	}
}

func printOpened(w io.Writer, o project.Opened) {
	verb := "opened"
	if o.Cloned {
		verb = "cloned and opened"
	}
	printProject(w, verb, o.Project)
	for _, wt := range o.Dropped {
		fmt.Fprintf(w, "  dropped missing checkout %s\n", wt.Path)
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
