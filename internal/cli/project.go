package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/srjn45/warden/internal/db"
	"github.com/srjn45/warden/internal/identity"
	"github.com/srjn45/warden/internal/project"
)

func newProjectCmd(home string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newProjectCreateCmd(home), newProjectOpenCmd(home), newProjectCloseCmd(home), newProjectRemoveCmd(home))
	return cmd
}

type createOptions struct {
	name, parent                     string
	openOnConflict, cancelOnConflict bool
}

func newProjectCreateCmd(home string) *cobra.Command {
	var opts createOptions
	cmd := &cobra.Command{
		Use:   "create --name <name> [--path <dir>]",
		Short: "Create a project in a new git repository",
		Long: "Create an empty git repository at <dir>/<name> and open it as a project.\n" +
			"<dir> defaults to " + identity.ProjectsDir(home) + ". An existing directory that\n" +
			"is not in a git repository is initialized in place.\n\n" +
			"If the name is taken or the directory is in a git repository, you are\n" +
			"asked to open that project, try another name or cancel; --open and\n" +
			"--cancel answer in advance, and with --json one of them is required.\n" +
			"Every failure starts with \"not created:\".",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{failurePrefix: "not created"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if jsonMode(cmd) && !opts.openOnConflict && !opts.cancelOnConflict {
				return &hinted{&usageError{errors.New("--json requires --open or --cancel")}, "pass --open or --cancel"}
			}
			return runCreate(cmd, home, opts)
		},
	}
	cmd.Flags().StringVar(&opts.name, "name", "", "project name, also the repository directory name")
	cmd.Flags().StringVar(&opts.parent, "path", "", "directory to create the repository in (default "+identity.ProjectsDir(home)+")")
	cmd.Flags().BoolVar(&opts.openOnConflict, "open", false, "on a conflict, open the existing project without asking")
	cmd.Flags().BoolVar(&opts.cancelOnConflict, "cancel", false, "on a conflict, create nothing without asking")
	cmd.MarkFlagRequired("name")
	cmd.MarkFlagsMutuallyExclusive("open", "cancel")
	return cmd
}

func runCreate(cmd *cobra.Command, home string, opts createOptions) error {
	ctx := cmd.Context()
	d, err := db.Open(ctx, identity.DBPath(home))
	if err != nil {
		return err
	}
	defer d.Close()

	store := project.NewStore(d, identity.ProjectsDir(home))
	answers := bufio.NewScanner(cmd.InOrStdin())
	for {
		o, conflict := store.Create(ctx, opts.name, opts.parent)
		if conflict == nil {
			return printResult(cmd, projectResponse("created", o.Project))
		}
		var open func() (project.Opened, error)
		retry := "choose another --name"
		if taken, ok := errors.AsType[*project.NameTakenError](conflict); ok {
			open = func() (project.Opened, error) { return store.Open(ctx, taken.Project.ID) }
		} else if exists, ok := errors.AsType[*project.RepoExistsError](conflict); ok {
			open = func() (project.Opened, error) { return store.OpenLocal(ctx, exists.Root, "") }
			retry = "choose another --name or --path"
		} else if _, ok := errors.AsType[*project.InvalidPathError](conflict); ok {
			return &hinted{conflict, "choose another --path"}
		} else if _, ok := errors.AsType[*project.InvalidNameError](conflict); ok {
			return &hinted{conflict, retry}
		} else {
			return conflict
		}

		var choice string
		switch {
		case opts.openOnConflict:
			choice = "o"
		case opts.cancelOnConflict:
			choice = "c"
		case !canPrompt(cmd):
			return &hinted{conflict, "pass --open or --cancel, or " + retry}
		default:
			prompt := cmd.ErrOrStderr()
			fmt.Fprintln(prompt, conflict)
			fmt.Fprintln(prompt, "[o] open it")
			fmt.Fprintln(prompt, "[n] try a different name")
			fmt.Fprintln(prompt, "[c] cancel")
			if choice, err = ask(prompt, answers, "choose [o/n/c]: ", "o", "n", "c"); err != nil {
				return err
			}
			if choice == "n" {
				if opts.name, err = ask(prompt, answers, "new name (empty to cancel): "); err != nil {
					return err
				}
				if opts.name != "" {
					continue
				}
				choice = "c"
			}
		}
		if choice == "c" {
			return &hinted{conflict, retry}
		}

		o, err = open()
		if missing, ok := errors.AsType[*project.CheckoutMissingError](err); ok {
			return resolveMissingCheckout(cmd, store, missing, "", missingCheckoutHint(missing.Project))
		}
		if err != nil {
			return err
		}
		r := openedResponse(o)
		v, _ := classify(conflict)
		r.Conflict = &v
		return printResult(cmd, r)
	}
}

func newProjectOpenCmd(home string) *cobra.Command {
	var local, gitURL, name string
	var reclone, remove bool
	cmd := &cobra.Command{
		Use:   "open (--local <path> | --git <url>)",
		Short: "Open a git repository as a project",
		Long: "Open a git repository as a project. A path inside a repository opens the\n" +
			"repository root. Worktrees and other clones of a known repository join\n" +
			"its project. Opening a known project marks it open again and drops its\n" +
			"checkouts that are no longer on disk.\n\n" +
			"--git opens the project with that origin, or clones it into\n" +
			identity.ProjectsDir(home) + "/<name> if it is unknown. If none of a known\n" +
			"project's checkouts are left, you are asked to clone it again or remove it;\n" +
			"--reclone and --remove answer in advance; with --json one of them is required.\n\n" +
			"Projects are named after their root directory or URL unless --name is\n" +
			"given. Names are unique.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if gitURL != "" && jsonMode(cmd) && !reclone && !remove {
				return &hinted{&usageError{errors.New("--json with --git requires --reclone or --remove")}, "pass --reclone or --remove"}
			}
			ctx := cmd.Context()
			d, err := db.Open(ctx, identity.DBPath(home))
			if err != nil {
				return err
			}
			defer d.Close()

			store := project.NewStore(d, identity.ProjectsDir(home))
			var o project.Opened
			if local != "" {
				o, err = store.OpenLocal(ctx, local, name)
			} else {
				o, err = store.OpenGit(ctx, gitURL, name, progress(cmd))
			}
			if missing, ok := errors.AsType[*project.CheckoutMissingError](err); ok {
				var choice string
				if reclone {
					choice = "c"
				} else if remove {
					choice = "r"
				}
				return resolveMissingCheckout(cmd, store, missing, choice, "pass --reclone or --remove")
			}
			if err != nil {
				return openHint(err)
			}
			return printResult(cmd, openedResponse(o))
		},
	}
	cmd.Flags().StringVar(&local, "local", "", "path to a local git repository")
	cmd.Flags().StringVar(&gitURL, "git", "", "clone URL of a git repository")
	cmd.Flags().StringVar(&name, "name", "", "project name (default: the repository directory or URL name)")
	cmd.Flags().BoolVar(&reclone, "reclone", false, "with --git, if no checkout is left, clone it again without asking")
	cmd.Flags().BoolVar(&remove, "remove", false, "with --git, if no checkout is left, remove the project without asking")
	cmd.MarkFlagsOneRequired("local", "git")
	cmd.MarkFlagsMutuallyExclusive("local", "git")
	cmd.MarkFlagsMutuallyExclusive("reclone", "remove")
	cmd.MarkFlagsMutuallyExclusive("local", "reclone")
	cmd.MarkFlagsMutuallyExclusive("local", "remove")
	return cmd
}

// resolveMissingCheckout clones the project again ("c") or removes it ("r"),
// asking when choice is empty. hint tells how to choose without a prompt.
func resolveMissingCheckout(cmd *cobra.Command, store *project.Store, missing *project.CheckoutMissingError, choice, hint string) error {
	ctx := cmd.Context()
	p := missing.Project
	prompt := cmd.ErrOrStderr()
	if choice == "" {
		if !canPrompt(cmd) {
			return &hinted{missing, hint}
		}
		fmt.Fprintf(prompt, "%s (was %s)\n", missing, p.ClonePath())
		fmt.Fprintf(prompt, "[c] clone again to %s\n", p.ClonePath())
		fmt.Fprintf(prompt, "[r] remove from %s\n", identity.Name)
		var err error
		if choice, err = ask(prompt, bufio.NewScanner(cmd.InOrStdin()), "choose [c/r]: ", "c", "r"); err != nil {
			return err
		}
	}
	if choice == "c" {
		o, err := store.Reclone(ctx, p.ID, progress(cmd))
		if err != nil {
			return err
		}
		return printResult(cmd, openedResponse(o))
	}
	removed, err := store.Remove(ctx, p.ID)
	if err != nil {
		return err
	}
	return printResult(cmd, projectResponse("removed", removed))
}

// progress is where git's clone output goes: the terminal, never JSON.
func progress(cmd *cobra.Command) io.Writer {
	if jsonMode(cmd) {
		return io.Discard
	}
	return cmd.ErrOrStderr()
}

// openHint tells what to do about the errors opening a project can return.
func openHint(err error) error {
	_, taken := errors.AsType[*project.NameTakenError](err)
	_, exists := errors.AsType[*project.PathExistsError](err)
	_, invalid := errors.AsType[*project.InvalidNameError](err)
	switch {
	case taken, exists, invalid:
		return &hinted{err, "choose another with --name"}
	case errors.Is(err, project.ErrNotGitRepo):
		return &hinted{err, "run git init there first, or use " + identity.Name + " project create"}
	}
	return err
}

func missingCheckoutHint(p project.Project) string {
	if p.OriginURL == "" {
		return fmt.Sprintf("remove it with %s project remove %s", identity.Name, p.Name)
	}
	return fmt.Sprintf("run %s project open --git %s with --reclone or --remove", identity.Name, p.OriginURL)
}

func newProjectCloseCmd(home string) *cobra.Command {
	return &cobra.Command{
		Use:   "close <name|id>",
		Short: "Close a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			d, err := db.Open(ctx, identity.DBPath(home))
			if err != nil {
				return err
			}
			defer d.Close()

			p, err := project.NewStore(d, identity.ProjectsDir(home)).Close(ctx, args[0])
			if err != nil {
				return err
			}
			return printResult(cmd, projectResponse("closed", p))
		},
	}
}

func newProjectRemoveCmd(home string) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name|id>",
		Short: "Delete a project and everything that belongs to it",
		Long: "Delete a project and everything that belongs to it from " + identity.Name + ".\n" +
			"Files on disk are not touched.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			d, err := db.Open(ctx, identity.DBPath(home))
			if err != nil {
				return err
			}
			defer d.Close()

			p, err := project.NewStore(d, identity.ProjectsDir(home)).Remove(ctx, args[0])
			if err != nil {
				return err
			}
			return printResult(cmd, projectResponse("removed", p))
		},
	}
}
