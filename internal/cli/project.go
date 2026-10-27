package cli

import (
	"bufio"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/srjn45/warden/internal/api"
	"github.com/srjn45/warden/internal/client"
	"github.com/srjn45/warden/internal/identity"
)

func newProjectCmd(home string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newProjectCreateCmd(home), newProjectListCmd(), newProjectGetCmd(), newProjectOpenCmd(home), newProjectCloseCmd(), newProjectRemoveCmd())
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
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not created",
			outputNote: "The created project. On a conflict answered with --open, the project opened instead,\n" +
				"with the conflict under \"conflict\".\n" +
				`With --json: {"result": ..., "project": {...}, "conflict": {...}}`,
			resultList: annotation("cloned", "created", "opened", "registered", "removed"),
			errorList: codeList(api.ErrorCodeCheckoutMissing, api.ErrorCodeCloneFailed, api.ErrorCodeInvalidName, api.ErrorCodeInvalidPath,
				api.ErrorCodeNameTaken, api.ErrorCodeRepoExists, api.ErrorCodeDaemonUnavailable),
			jsonRequires: annotation("open", "cancel"),
		},
		RunE: func(cmd *cobra.Command, _ []string) error { return runCreate(cmd, opts) },
	}
	cmd.Flags().StringVar(&opts.name, "name", "", "project name, also the repository directory name")
	cmd.Flags().StringVar(&opts.parent, "path", "", "directory to create the repository in (default "+identity.ProjectsDir(home)+")")
	cmd.Flags().BoolVar(&opts.openOnConflict, "open", false, "on a conflict, open the existing project without asking")
	cmd.Flags().BoolVar(&opts.cancelOnConflict, "cancel", false, "on a conflict, create nothing without asking")
	cmd.MarkFlagRequired("name")
	cmd.MarkFlagsMutuallyExclusive("open", "cancel")
	return cmd
}

const createConflictQuestion = "create conflict"

func runCreate(cmd *cobra.Command, opts createOptions) error {
	parent, err := absolute(opts.parent)
	if err != nil {
		return err
	}
	var source api.Source
	source.FromNewSource(api.NewSource{Type: api.NewSourceTypeNew, Path: optional(parent)})
	req := api.CreateProject{Source: source, Name: &opts.name}
	if opts.openOnConflict {
		req.OnConflict = ptr(api.CreateProjectOnConflictOpen)
		logAnswer(cmd, createConflictQuestion, "open", "--open")
	}

	answers := bufio.NewScanner(cmd.InOrStdin())
	for {
		r, conflict := clientOf(cmd).CreateProject(cmd.Context(), req)
		if conflict == nil {
			return printResult(cmd, projectResult(r))
		}
		retry := "choose another --name"
		switch problemCode(conflict) {
		case api.ErrorCodeNameTaken:
		case api.ErrorCodeRepoExists:
			retry = "choose another --name or --path"
		case api.ErrorCodeInvalidPath:
			return &hinted{conflict, "choose another --path"}
		case api.ErrorCodeInvalidName:
			return &hinted{conflict, retry}
		case api.ErrorCodeCheckoutMissing:
			if req.OnMissingCheckout, err = askMissingCheckout(cmd, conflict, ""); err != nil {
				return err
			}
			continue
		default:
			return conflict
		}

		switch {
		case opts.cancelOnConflict:
			logAnswer(cmd, createConflictQuestion, "cancel", "--cancel")
			return &hinted{conflict, retry}
		case !canPrompt(cmd):
			logUnanswered(cmd, createConflictQuestion)
			return &hinted{conflict, "pass --open or --cancel, or " + retry}
		}
		prompt := cmd.ErrOrStderr()
		fmt.Fprintln(prompt, conflict)
		fmt.Fprintln(prompt, "[o] open it")
		fmt.Fprintln(prompt, "[n] try a different name")
		fmt.Fprintln(prompt, "[c] cancel")
		choice, err := ask(prompt, answers, "choose [o/n/c]: ", "o", "n", "c")
		if err != nil {
			return err
		}
		logAnswer(cmd, createConflictQuestion, map[string]string{"o": "open", "n": "new name", "c": "cancel"}[choice], "prompt")
		switch choice {
		case "o":
			req.OnConflict = ptr(api.CreateProjectOnConflictOpen)
		case "n":
			name, err := ask(prompt, answers, "new name (empty to cancel): ")
			if err != nil {
				return err
			}
			logAnswer(cmd, "new name", name, "prompt")
			if name == "" {
				return &hinted{conflict, retry}
			}
			req.Name = &name
		default:
			return &hinted{conflict, retry}
		}
	}
}

func newProjectListCmd() *cobra.Command {
	var open, closed bool
	cmd := &cobra.Command{
		Use:   "list [--all | --open | --closed]",
		Short: "List projects",
		Long:  "List projects, without their worktrees. With no flag, every project is listed.",
		Args:  cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The projects sorted by name, each with its number of worktrees; an empty list when none match.\n" +
				`With --json: {"projects": [...]}`,
			errorList: codeList(api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			var state api.ProjectState
			if open {
				state = api.ProjectStateOpen
			} else if closed {
				state = api.ProjectStateClosed
			}
			list, err := clientOf(cmd).ListProjects(cmd.Context(), state)
			if err != nil {
				return err
			}
			return printResult(cmd, projectList{list, state})
		},
	}
	cmd.Flags().Bool("all", false, "list every project (the default)")
	cmd.Flags().BoolVar(&open, "open", false, "list only open projects")
	cmd.Flags().BoolVar(&closed, "closed", false, "list only closed projects")
	cmd.MarkFlagsMutuallyExclusive("all", "open", "closed")
	return cmd
}

func newProjectGetCmd() *cobra.Command {
	var ref projectRef
	cmd := &cobra.Command{
		Use:   "get (--id <id> | --name <name>)",
		Short: "Show a project and which of its checkouts are on disk",
		Long: "Show a project, open or closed, with its worktrees and whether each is\n" +
			"still on disk. A project with no checkout left on disk is orphaned.\n" +
			"Nothing is changed.",
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The project with its worktrees, \"present\" telling whether each is on disk and \"orphaned\"\n" +
				"that none is.\n" +
				`With --json: {"project": {...}}`,
			errorList: codeList(api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := clientOf(cmd).GetProject(cmd.Context(), ref.path())
			if err != nil {
				return err
			}
			return printResult(cmd, projectDetail(p))
		},
	}
	ref.addFlags(cmd)
	return cmd
}

// projectRef is a project named on the command line by --id or --name,
// exactly one of which is required.
type projectRef struct {
	id, name string
}

func (r *projectRef) addFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&r.id, "id", "", "project id")
	cmd.Flags().StringVar(&r.name, "name", "", "project name (case-insensitive)")
	cmd.MarkFlagsOneRequired("id", "name")
	cmd.MarkFlagsMutuallyExclusive("id", "name")
}

// path is how the API names the project.
func (r projectRef) path() string {
	if r.id != "" {
		return "id:" + r.id
	}
	return r.name
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
		Annotations: map[string]string{
			outputNote: "The project, with the checkouts dropped because they are no longer on disk under \"dropped\".\n" +
				`With --json: {"result": ..., "project": {...}, "dropped": [...]}`,
			resultList: annotation("cloned", "opened", "registered", "removed"),
			errorList: codeList(api.ErrorCodeCheckoutMissing, api.ErrorCodeCloneFailed, api.ErrorCodeInvalidArgument, api.ErrorCodeInvalidName, api.ErrorCodeInvalidPath,
				api.ErrorCodeNameTaken, api.ErrorCodeNotGitRepo, api.ErrorCodePathExists, api.ErrorCodeProjectMismatch, api.ErrorCodeDaemonUnavailable),
			jsonRequires: "git:" + annotation("reclone", "remove"),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			var source api.Source
			if cmd.Flags().Changed("local") {
				path, err := absolute(local)
				if err != nil {
					return err
				}
				source.FromLocalSource(api.LocalSource{Type: api.LocalSourceTypeLocal, Path: path})
			} else {
				source.FromGitSource(api.GitSource{Type: api.GitSourceTypeGit, Url: gitURL})
			}
			req := api.CreateProject{Source: source, Name: optional(name)}
			if reclone {
				req.OnMissingCheckout = ptr(api.CreateProjectOnMissingCheckoutReclone)
				logAnswer(cmd, missingCheckoutQuestion, "reclone", "--reclone")
			} else if remove {
				req.OnMissingCheckout = ptr(api.CreateProjectOnMissingCheckoutRemove)
				logAnswer(cmd, missingCheckoutQuestion, "remove", "--remove")
			}
			for {
				r, err := clientOf(cmd).CreateProject(cmd.Context(), req)
				if err == nil {
					return printResult(cmd, projectResult(r))
				}
				if problemCode(err) != api.ErrorCodeCheckoutMissing || req.OnMissingCheckout != nil {
					return openHint(err)
				}
				if req.OnMissingCheckout, err = askMissingCheckout(cmd, err, "pass --reclone or --remove"); err != nil {
					return err
				}
			}
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

const missingCheckoutQuestion = "missing checkout"

// askMissingCheckout asks whether to clone the project of the
// checkout_missing problem in err again or to remove it. hint tells how to
// answer without a prompt; the project's own hint when empty.
func askMissingCheckout(cmd *cobra.Command, err error, hint string) (*api.CreateProjectOnMissingCheckout, error) {
	var problem api.Problem
	if e, ok := errors.AsType[*client.ProblemError](err); ok {
		problem = e.Problem
	}
	p, _ := detailProject(problem, "project")
	if !canPrompt(cmd) {
		logUnanswered(cmd, missingCheckoutQuestion)
		if hint == "" {
			hint = missingCheckoutHint(p)
		}
		return nil, &hinted{err, hint}
	}
	var clonePath string
	if problem.Details != nil {
		clonePath, _ = (*problem.Details)["clone_path"].(string)
	}
	prompt := cmd.ErrOrStderr()
	fmt.Fprintf(prompt, "%s (was %s)\n", err, clonePath)
	fmt.Fprintf(prompt, "[c] clone again to %s\n", clonePath)
	fmt.Fprintf(prompt, "[r] remove from %s\n", identity.Name)
	choice, err := ask(prompt, bufio.NewScanner(cmd.InOrStdin()), "choose [c/r]: ", "c", "r")
	if err != nil {
		return nil, err
	}
	if choice == "r" {
		logAnswer(cmd, missingCheckoutQuestion, "remove", "prompt")
		return ptr(api.CreateProjectOnMissingCheckoutRemove), nil
	}
	logAnswer(cmd, missingCheckoutQuestion, "reclone", "prompt")
	return ptr(api.CreateProjectOnMissingCheckoutReclone), nil
}

// openHint tells what to do about the errors opening a project can return.
func openHint(err error) error {
	switch problemCode(err) {
	case api.ErrorCodeNameTaken, api.ErrorCodePathExists, api.ErrorCodeInvalidName:
		return &hinted{err, "choose another with --name"}
	case api.ErrorCodeNotGitRepo:
		return &hinted{err, "run git init there first, or use " + identity.Name + " project create"}
	}
	return err
}

func missingCheckoutHint(p api.Project) string {
	if p.Origin == nil {
		return fmt.Sprintf("remove it with %s project remove --id %s", identity.Name, p.Id)
	}
	return fmt.Sprintf("run %s project open --git %s with --reclone or --remove", identity.Name, *p.Origin)
}

func newProjectCloseCmd() *cobra.Command {
	var ref projectRef
	cmd := &cobra.Command{
		Use:   "close (--id <id> | --name <name>)",
		Short: "Close a project",
		Long: "Mark a project closed. Nothing on disk is touched; open it again with\n" +
			identity.Name + " project open.",
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The closed project.\n" + `With --json: {"result": "closed", "project": {...}}`,
			resultList: "closed",
			errorList:  codeList(api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := clientOf(cmd).UpdateProject(cmd.Context(), ref.path(), api.ProjectStateClosed)
			if err != nil {
				return err
			}
			return printResult(cmd, projectResult(r))
		},
	}
	ref.addFlags(cmd)
	return cmd
}

func newProjectRemoveCmd() *cobra.Command {
	var ref projectRef
	cmd := &cobra.Command{
		Use:   "remove (--id <id> | --name <name>)",
		Short: "Delete a project and everything that belongs to it",
		Long: "Delete a project and everything that belongs to it from " + identity.Name + ".\n" +
			"Files on disk are not touched.",
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The removed project.\n" + `With --json: {"result": "removed", "project": {...}}`,
			resultList: "removed",
			errorList:  codeList(api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := clientOf(cmd).DeleteProject(cmd.Context(), ref.path())
			if err != nil {
				return err
			}
			return printResult(cmd, projectResult(r))
		},
	}
	ref.addFlags(cmd)
	return cmd
}

// absolute makes path absolute against the working directory, which the
// daemon does not share; "" stays "".
func absolute(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	return filepath.Abs(path)
}

func ptr[T any](v T) *T {
	return &v
}

// optional is nil for the zero value, a pointer to v otherwise.
func optional[T comparable](v T) *T {
	var zero T
	if v == zero {
		return nil
	}
	return &v
}
