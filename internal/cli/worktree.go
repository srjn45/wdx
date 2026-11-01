package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

func newWorktreeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "Read a project's worktrees: what is uncommitted, and what a PR would show",
		Long: "These commands only read, so they can run beside any git command: they\n" +
			"take no lock on the repository. wdx agent create --worktree creates a worktree.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newWorktreeCommentsCmd(), newWorktreeDiffCmd(), newWorktreeListCmd())
	return cmd
}

// worktreeScopeOf returns how to name the project: by its flags, else by the
// working directory.
func worktreeScopeOf(cmd *cobra.Command, scope projectScope) (ref, dir string, err error) {
	if scope.given() {
		return scope.ref(), "", nil
	}
	dir, err = workDir(cmd)
	return "", dir, err
}

func newWorktreeListCmd() *cobra.Command {
	var scope projectScope
	cmd := &cobra.Command{
		Use:   "list [--project-id <id> | --project-name <name>]",
		Short: "List every worktree git lists for a project, with its changes",
		Long: "Without a project flag, the project the working directory is in. Each\n" +
			"worktree says how far it is ahead of its base, the default branch, and\n" +
			"which files have uncommitted changes. One whose directory is gone is listed\n" +
			"as missing.",
		Example: examples(
			"The worktrees of the project you are in", identity.Name+" worktree list",
			"Another project's, as JSON", identity.Name+" worktree list --project-name app --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The worktrees, the clone first, named main.\n" + `With --json: {"worktrees": [...]}`,
			errorList:  codeList(api.ErrorCodeNotFound, api.ErrorCodeInvalidPath, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			ref, dir, err := worktreeScopeOf(cmd, scope)
			if err != nil {
				return err
			}
			list, err := clientOf(cmd).ListWorktrees(cmd.Context(), ref, dir)
			if err != nil {
				return outsideHint(err, scope, "")
			}
			return printResult(cmd, worktreeList(list))
		},
	}
	scope.addFlags(cmd)
	return cmd
}

func newWorktreeDiffCmd() *cobra.Command {
	var scope projectScope
	var name, path string
	var uncommitted bool
	cmd := &cobra.Command{
		Use:   "diff --name <worktree> [--uncommitted | --path <file>] [--project-id <id> | --project-name <name>]",
		Short: "Show what a worktree has committed, or what it has not",
		Long: "By default, what the worktree has committed since it left its base, as a\n" +
			"PR would show it: later commits on the base are not removals. With\n" +
			"--uncommitted, everything not committed, untracked files too. With --path,\n" +
			"the uncommitted changes of that file, with all of its lines.",
		Example: examples(
			"What a worktree has committed", identity.Name+" worktree diff --name auth-fix",
			"Everything not committed", identity.Name+" worktree diff --name auth-fix --uncommitted",
			"The uncommitted changes of a file", identity.Name+" worktree diff --name auth-fix --path internal/tide/tabs.go",
			"As JSON", identity.Name+" worktree diff --name auth-fix --project-name app --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The unified diff; nothing when nothing differs.\n" +
				`With --json: {"worktree": "...", "patch": "...", ...}`,
			errorList: codeList(api.ErrorCodeNotFound, api.ErrorCodeInvalidPath, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			ref, dir, err := worktreeScopeOf(cmd, scope)
			if err != nil {
				return err
			}
			if path != "" {
				d, err := clientOf(cmd).WorktreeFileDiff(cmd.Context(), name, path, ref, dir)
				if err != nil {
					return outsideHint(err, scope, "")
				}
				return printResult(cmd, worktreeFileDiff(d))
			}
			read := clientOf(cmd).WorktreeDiff
			if uncommitted {
				read = clientOf(cmd).WorktreeUncommittedDiff
			}
			d, err := read(cmd.Context(), name, ref, dir)
			if err != nil {
				return outsideHint(err, scope, "")
			}
			return printResult(cmd, worktreeDiff(d))
		},
	}
	scope.addFlags(cmd)
	cmd.Flags().StringVar(&name, "name", "", "`name` of the worktree, main for the clone")
	cmd.Flags().StringVar(&path, "path", "", "`file` to show the uncommitted changes of, relative to the worktree")
	cmd.Flags().BoolVar(&uncommitted, "uncommitted", false, "show everything not committed instead")
	cmd.MarkFlagsMutuallyExclusive("uncommitted", "path")
	cmd.MarkFlagRequired("name")
	cmd.RegisterFlagCompletionFunc("name", cobra.NoFileCompletions)
	cmd.RegisterFlagCompletionFunc("path", cobra.NoFileCompletions)
	return cmd
}

func newWorktreeCommentsCmd() *cobra.Command {
	var scope projectScope
	var name string
	cmd := &cobra.Command{
		Use:   "comments --name <worktree> [--project-id <id> | --project-name <name>]",
		Short: "List the comments left on a worktree's diffs",
		Long: "Comments are written in tide, and sent to an agent together as a review.\n" +
			"Each says whether it was sent.",
		Example: examples(
			"The comments on a worktree", identity.Name+" worktree comments --name auth-fix",
			"As JSON", identity.Name+" worktree comments --name auth-fix --project-name app --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The comments by file and line.\n" + `With --json: {"comments": [...]}`,
			errorList:  codeList(api.ErrorCodeNotFound, api.ErrorCodeInvalidPath, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			ref, dir, err := worktreeScopeOf(cmd, scope)
			if err != nil {
				return err
			}
			list, err := clientOf(cmd).WorktreeComments(cmd.Context(), name, ref, dir)
			if err != nil {
				return outsideHint(err, scope, "")
			}
			return printResult(cmd, worktreeComments(list))
		},
	}
	scope.addFlags(cmd)
	cmd.Flags().StringVar(&name, "name", "", "`name` of the worktree, main for the clone")
	cmd.MarkFlagRequired("name")
	cmd.RegisterFlagCompletionFunc("name", cobra.NoFileCompletions)
	return cmd
}

type worktreeComments api.WorktreeCommentList

func (l worktreeComments) text(w io.Writer, st style.Style) {
	if len(l.Comments) == 0 {
		fmt.Fprintln(w, "no comments")
		return
	}
	var table style.Table
	table.Row(st.Heading("FILE"), st.Heading("LINE"), st.Heading("STATE"), st.Heading("COMMENT"))
	for _, c := range l.Comments {
		table.Row(st.Name(c.File), fmt.Sprint(c.Line), string(c.State), c.Body)
	}
	table.Write(w)
}

type worktreeList api.WorktreeList

func (l worktreeList) text(w io.Writer, st style.Style) {
	var table style.Table
	table.Row(st.Heading("NAME"), st.Heading("BRANCH"), st.Heading("AHEAD"), st.Heading("CHANGED"), st.Heading("PATH"))
	for _, t := range l.Worktrees {
		branch := st.Muted("detached")
		if t.Branch != nil {
			branch = *t.Branch
		}
		ahead := st.Muted("-")
		if t.Missing {
			ahead = st.Warning("missing")
		} else if t.Ahead != nil {
			ahead = fmt.Sprint(*t.Ahead)
		}
		table.Row(st.Name(t.Name), branch, ahead, fmt.Sprint(len(t.Files)), t.Path)
	}
	table.Write(w)
}

type worktreeDiff api.WorktreeDiff

func (d worktreeDiff) text(w io.Writer, _ style.Style) { writePatch(w, d.Patch, d.Truncated) }

type worktreeFileDiff api.WorktreeFileDiff

func (d worktreeFileDiff) text(w io.Writer, _ style.Style) { writePatch(w, d.Patch, d.Truncated) }

func writePatch(w io.Writer, patch string, truncated bool) {
	fmt.Fprint(w, patch)
	if truncated {
		fmt.Fprintln(w, "\n(too long: the rest is left out)")
	}
}
