package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

func newWorktreeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "Read a project's worktrees, what is uncommitted and what a PR would show, land them and remove them",
		Long: "All but land, rebase and remove only read, so they can run beside any git command: they take\n" +
			"no lock on the repository. wdx agent create --worktree creates a worktree.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newWorktreeCommentsCmd(), newWorktreeDiffCmd(), newWorktreeLandCmd(), newWorktreeListCmd(), newWorktreeRebaseCmd(), newWorktreeRemoveCmd())
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
	completeFlag(cmd, "name", cobra.NoFileCompletions)
	completeFlag(cmd, "path", cobra.NoFileCompletions)
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
	completeFlag(cmd, "name", cobra.NoFileCompletions)
	return cmd
}

func newWorktreeLandCmd() *cobra.Command {
	var scope projectScope
	var name, method string
	cmd := &cobra.Command{
		Use:   "land --name <worktree> [--method <method>] [--project-id <id> | --project-name <name>]",
		Short: "Land a worktree's commits on its base, with no merge commit or through a PR",
		Long: "Rebases it if the base has moved on, undoing that on a conflict; runs the\n" +
			"project's check in a terminal named land-<worktree>, which stays if it fails;\n" +
			"moves the base forward to the commit checked, and pushes it if the project\n" +
			"says so. On any failure the base is where it was. This waits for the end, and\n" +
			"leaving does not stop it. wdx project landing says how a project lands.\n\n" +
			"By PR it runs the check, pushes the branch and opens a PR with gh, or adds to\n" +
			"the one open. It never rebases or forces, and the base stays where it is.",
		Example: examples(
			"Land a worktree", identity.Name+" worktree land --name auth-fix",
			"Open a PR this once", identity.Name+" worktree land --name auth-fix --method pr",
			"As JSON", identity.Name+" worktree land --name auth-fix --project-name app --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "What landed and on what, or the PR opened.\n" + `With --json: {"result": "landed", "worktree": "...", ...}`,
			resultList: "landed, pr_opened",
			errorList: codeList(api.ErrorCodeAgentWorking, api.ErrorCodeBaseMoved, api.ErrorCodeCheckFailed, api.ErrorCodeDependencyMissing, api.ErrorCodeDetachedHead, api.ErrorCodeFastForwardFailed,
				api.ErrorCodeInvalidPath, api.ErrorCodeLandNotConfigured, api.ErrorCodeLandingRunning, api.ErrorCodeNoLocalBase,
				api.ErrorCodeNotCommitted, api.ErrorCodeNotFound, api.ErrorCodeNothingToLand, api.ErrorCodePrFailed, api.ErrorCodeProjectClosed, api.ErrorCodePushFailed,
				api.ErrorCodeRebaseFailed, api.ErrorCodeWorktreeBusy, api.ErrorCodeWorktreeInPlan, api.ErrorCodeWorktreeIsClone, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkLandingMethod(method); err != nil {
				return err
			}
			ref, dir, err := worktreeScopeOf(cmd, scope)
			if err != nil {
				return err
			}
			c := clientOf(cmd)
			started, err := c.LandWorktree(cmd.Context(), name, ref, dir, method)
			if err != nil {
				if problemCode(err) == api.ErrorCodeLandNotConfigured {
					return &hinted{err, "say how with " + identity.Name + " project landing --check <command> or --no-check"}
				}
				return outsideHint(inPlanHint(err), scope, "")
			}
			l, err := waitLanding(cmd, started)
			if err != nil {
				return err
			}
			if l.State == api.WorktreeLandingStateFailed {
				return landingProblem(l)
			}
			result := api.ResultLanded
			if l.State == api.WorktreeLandingStateOpened {
				result = api.ResultPrOpened
			}
			return printResult(cmd, worktreeLanded{
				Result: result, Worktree: name, Base: l.Base, Commit: l.Commit, Pushed: l.Pushed, PushError: l.PushError, PullRequest: l.PullRequest,
			})
		},
	}
	scope.addFlags(cmd)
	cmd.Flags().StringVar(&name, "name", "", "`name` of the worktree")
	cmd.Flags().StringVar(&method, "method", "", "`method` for this landing, rebase or pr; what the project says when not given")
	cmd.MarkFlagRequired("name")
	completeFlag(cmd, "name", cobra.NoFileCompletions)
	completeFlag(cmd, "method", landingMethods)
	return cmd
}

// inPlanHint says where a slot of a plan going on goes: the plan lands its tasks.
func inPlanHint(err error) error {
	var p api.Plan
	if e, ok := errors.AsType[*client.ProblemError](err); !ok || e.Problem.Code != api.ErrorCodeWorktreeInPlan || !decodeDetail(e.Problem, "plan", &p) {
		return err
	}
	return &hinted{err, fmt.Sprintf("it is a slot of plan %s, which lands its tasks on %s: %s plan status --name %s",
		p.Name, planBranch(p.Name), identity.Name, p.Name)}
}

// landingMethods completes how a worktree can land.
func landingMethods(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return []string{"rebase", "pr"}, cobra.ShellCompDirectiveNoFileComp
}

// checkLandingMethod is a usage error unless method is empty or a way to land.
func checkLandingMethod(method string) error {
	if method != "" && method != "rebase" && method != "pr" {
		return &usageError{fmt.Errorf("--method must be rebase or pr, not %q", method)}
	}
	return nil
}

// waitLanding follows the project until the landing started has ended. The daemon
// keeps how it ended, so that following only now misses nothing.
func waitLanding(cmd *cobra.Command, started api.WorktreeLandResult) (api.WorktreeLanding, error) {
	stream, err := clientOf(cmd).ProjectEventsWithWorktrees(cmd.Context(), "id:"+started.ProjectId)
	if err != nil {
		return api.WorktreeLanding{}, err
	}
	defer stream.Close()
	for {
		event, err := stream.Next()
		if err != nil {
			return api.WorktreeLanding{}, fmt.Errorf("stopped following the landing, which may go on: %w", err)
		}
		if event.Removed {
			return api.WorktreeLanding{}, fmt.Errorf("the project was removed during the landing")
		}
		if event.Project.WorktreeStatuses == nil {
			continue
		}
		for _, w := range *event.Project.WorktreeStatuses {
			if w.Landing == nil || w.Landing.Id != started.Landing.Id {
				continue
			}
			if w.Landing.State == api.WorktreeLandingStateLanded || w.Landing.State == api.WorktreeLandingStateOpened || w.Landing.State == api.WorktreeLandingStateFailed {
				return *w.Landing, nil
			}
		}
	}
}

// landingProblem is a landing that failed, as the error the code says.
func landingProblem(l api.WorktreeLanding) error {
	code := api.ErrorCode("")
	if l.Code != nil {
		code = api.ErrorCode(*l.Code)
	}
	detail := ""
	if l.Detail != nil {
		detail = *l.Detail
	}
	p := api.NewProblem(code, detail)
	details := map[string]any{"conflicts": l.Conflicts}
	if l.Terminal != nil {
		details["terminal"] = *l.Terminal
	}
	p.Details = &details
	var err error = &client.ProblemError{Problem: p}
	switch code {
	case api.ErrorCodeRebaseFailed:
		err = &hinted{err, "nothing changed; ask an agent to rebase with " + identity.Name + " agent send, then land again"}
	case api.ErrorCodeCheckFailed:
		if l.Terminal != nil {
			err = &hinted{err, "read its output in terminal " + *l.Terminal}
		}
	case api.ErrorCodeBaseMoved:
		err = &hinted{err, "land again: it rebases and checks again"}
	case api.ErrorCodePushFailed:
		err = &hinted{err, "nothing was forced: bring the branch up to date with origin, then land again"}
	}
	return err
}

type worktreeLanded struct {
	Result    api.Result `json:"result"`
	Worktree  string     `json:"worktree"`
	Base      string     `json:"base"`
	Commit    *string    `json:"commit"`
	Pushed    bool       `json:"pushed"`
	PushError *string    `json:"push_error"`

	PullRequest *api.WorktreePullRequest `json:"pull_request"`
}

func (r worktreeLanded) text(w io.Writer, st style.Style) {
	if r.PullRequest != nil {
		fmt.Fprintf(w, "%s worktree %s onto %s\n%s\n", st.Success(fmt.Sprintf("PR #%d open:", r.PullRequest.Number)), st.Name(r.Worktree), st.Name(r.Base), r.PullRequest.Url)
		return
	}
	fmt.Fprintln(w, st.Success("landed")+" worktree "+st.Name(r.Worktree)+" on "+st.Name(r.Base))
	if r.Pushed {
		fmt.Fprintln(w, "pushed to origin")
	}
	if r.PushError != nil {
		fmt.Fprintln(w, st.Warning("not pushed: ")+*r.PushError)
	}
}

func newWorktreeRebaseCmd() *cobra.Command {
	var scope projectScope
	var name string
	cmd := &cobra.Command{
		Use:   "rebase --name <worktree> [--project-id <id> | --project-name <name>]",
		Short: "Rebase a worktree onto its base, the default branch or a stacked plan's plan below",
		Long: "Landing's rebase on its own: no check, and the base does not move. A\n" +
			"conflict undoes it, leaving everything as it was, and lists the files; files\n" +
			"not committed refuse it, as nothing is stashed. Either is for an agent to\n" +
			"deal with: this sends nothing to one. A branch on origin is pushed again once\n" +
			"rebased, over its commit there before the rebase only, never over another's.\n\n" +
			"A stacked plan's worktree asks gh whether the PR of the plan below has merged:\n" +
			"if so, only the plan's own commits move onto the default branch, and its PR\n" +
			"goes there too, once the merge is pulled; if not, they move onto the plan\n" +
			"below's branch as it is now.",
		Example: examples(
			"Rebase a worktree", identity.Name+" worktree rebase --name auth-fix",
			"As JSON", identity.Name+" worktree rebase --name auth-fix --project-name app --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "What it was rebased onto, or that it was up to date.\n" + `With --json: {"result": "rebased", "worktree": "...", ...}`,
			resultList: "rebased, up_to_date",
			errorList: codeList(api.ErrorCodeAgentWorking, api.ErrorCodeBaseNotPulled, api.ErrorCodeDependencyMissing, api.ErrorCodeDetachedHead, api.ErrorCodeInvalidPath, api.ErrorCodeNoLocalBase,
				api.ErrorCodeNotCommitted, api.ErrorCodeNotFound, api.ErrorCodeProjectClosed, api.ErrorCodePushFailed, api.ErrorCodeRebaseFailed,
				api.ErrorCodeWorktreeBusy, api.ErrorCodeWorktreeInPlan, api.ErrorCodeWorktreeIsClone, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			ref, dir, err := worktreeScopeOf(cmd, scope)
			if err != nil {
				return err
			}
			r, err := clientOf(cmd).RebaseWorktree(cmd.Context(), name, ref, dir)
			if err != nil {
				return outsideHint(inPlanHint(rebaseHint(err)), scope, "")
			}
			return printResult(cmd, worktreeRebased(r))
		},
	}
	scope.addFlags(cmd)
	cmd.Flags().StringVar(&name, "name", "", "`name` of the worktree")
	cmd.MarkFlagRequired("name")
	completeFlag(cmd, "name", cobra.NoFileCompletions)
	return cmd
}

// rebaseHint says what to do about a rebase refused or undone: an agent's work, which
// the CLI never asks for itself.
func rebaseHint(err error) error {
	switch problemCode(err) {
	case api.ErrorCodeRebaseFailed:
		return &hinted{err, "nothing changed; ask an agent to rebase, resolving the conflicts, with " + identity.Name + " agent send"}
	case api.ErrorCodeNotCommitted:
		return &hinted{err, "nothing is stashed; ask an agent to commit with " + identity.Name + " agent send, then rebase again"}
	case api.ErrorCodePushFailed:
		return &hinted{err, "the rebase is kept, and nothing on origin was overwritten: see what was pushed there since"}
	case api.ErrorCodeBaseNotPulled:
		var below, base string
		e, _ := errors.AsType[*client.ProblemError](err)
		if decodeDetail(e.Problem, "below", &below) && decodeDetail(e.Problem, "base", &base) {
			return &hinted{err, fmt.Sprintf("pull %s: %s's PR is merged on GitHub", base, below)}
		}
	}
	return err
}

type worktreeRebased api.WorktreeRebased

func (r worktreeRebased) text(w io.Writer, st style.Style) {
	if r.Result == api.ResultUpToDate {
		fmt.Fprintln(w, st.Success("up to date:")+" worktree "+st.Name(r.Worktree)+" already sits on "+st.Name(r.Base))
		return
	}
	fmt.Fprintln(w, st.Success("rebased")+" worktree "+st.Name(r.Worktree)+" onto "+st.Name(r.Base))
	if r.Pushed {
		fmt.Fprintln(w, "pushed to origin")
	}
}

func newWorktreeRemoveCmd() *cobra.Command {
	var scope projectScope
	var name string
	cmd := &cobra.Command{
		Use:   "remove --name <worktree> [--project-id <id> | --project-name <name>]",
		Short: "Remove a worktree that landed, with its branch",
		Long: "Only one with nothing ahead of its base and nothing uncommitted, so no\n" +
			"commit can be lost and nothing is asked; ignored files go with its directory.\n" +
			"One whose directory is gone can always be removed: only git's entry goes.\n" +
			"The branch goes with it if git says it is merged. Agents running there are\n" +
			"sent where to go on, and terminals running there are left as they are.",
		Example: examples(
			"Remove a worktree that landed", identity.Name+" worktree remove --name auth-fix",
			"As JSON", identity.Name+" worktree remove --name auth-fix --project-name app --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "What went, and who was told.\n" + `With --json: {"result": "removed", "worktree": "...", ...}`,
			resultList: "removed",
			errorList: codeList(api.ErrorCodeNotFound, api.ErrorCodeInvalidPath, api.ErrorCodeWorktreeInPlan, api.ErrorCodeWorktreeIsClone,
				api.ErrorCodeWorktreeNotLanded, api.ErrorCodeWorktreeNotRemoved, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			ref, dir, err := worktreeScopeOf(cmd, scope)
			if err != nil {
				return err
			}
			r, err := clientOf(cmd).RemoveWorktree(cmd.Context(), name, ref, dir)
			if err != nil {
				return outsideHint(inPlanHint(err), scope, "")
			}
			return printResult(cmd, worktreeRemoved(r))
		},
	}
	scope.addFlags(cmd)
	cmd.Flags().StringVar(&name, "name", "", "`name` of the worktree")
	cmd.MarkFlagRequired("name")
	completeFlag(cmd, "name", cobra.NoFileCompletions)
	return cmd
}

type worktreeRemoved api.WorktreeRemoved

func (r worktreeRemoved) text(w io.Writer, st style.Style) {
	fmt.Fprintln(w, st.Success("removed")+" worktree "+st.Name(r.Worktree))
	if r.BranchDeleted != nil {
		fmt.Fprintln(w, "branch "+st.Name(*r.BranchDeleted)+" deleted")
	}
	if r.BranchKept != nil {
		fmt.Fprintln(w, "branch "+st.Name(*r.BranchKept)+" kept")
	}
	if len(r.Notified) > 0 {
		fmt.Fprintln(w, "told: "+strings.Join(r.Notified, ", "))
	}
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

// loggedWorktree keeps a file's contents and what is said of them out of the log.
func loggedWorktree(v any) slog.Value {
	b, err := json.Marshal(v)
	if err != nil {
		return slog.StringValue("unreadable response")
	}
	return slog.AnyValue(api.WorktreeResponseLogged(b))
}

// LogValue keeps what it holds out of the log.
func (l worktreeList) LogValue() slog.Value { return loggedWorktree(l) }

// LogValue keeps what it holds out of the log.
func (d worktreeDiff) LogValue() slog.Value { return loggedWorktree(d) }

// LogValue keeps what it holds out of the log.
func (d worktreeFileDiff) LogValue() slog.Value { return loggedWorktree(d) }

// LogValue keeps what it holds out of the log.
func (l worktreeComments) LogValue() slog.Value { return loggedWorktree(l) }
