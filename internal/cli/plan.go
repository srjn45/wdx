package cli

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/style"
)

func newPlanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Manage plans: the work of a feature broken into tasks for agents",
		Long: "A plan is YAML: a goal, and tasks, each a prompt for an agent with the tasks\n" +
			"it waits for; or a big plan broken into smaller plans, each a PR. " + identity.Name + " help\n" +
			"plans says how to write one. Every change of a plan is a new revision, made on\n" +
			"the revision it was read at, as for docs.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newPlanArchiveCmd(), newPlanCopyCmd(), newPlanUnarchiveCmd(), newPlanCancelCmd(), newPlanCreateCmd(), newPlanGetCmd(), newPlanHistoryCmd(), newPlanListCmd(), newPlanPauseCmd(),
		newPlanRemoveCmd(), newPlanRenameCmd(), newPlanResumeCmd(), newPlanRunCmd(), newPlanSetCmd(), newPlanStatusCmd(), newPlanTaskCmd(),
		newPlanUpdateCmd())
	return cmd
}

// planRef is a plan named on the command line by --id, or by --name in a project.
type planRef struct {
	id, name string
	scope    projectScope
}

func (r *planRef) addFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&r.id, "id", "", "`id` of the plan")
	cmd.Flags().StringVar(&r.name, "name", "", "`name` of the plan (case-insensitive)")
	cmd.MarkFlagsOneRequired("id", "name")
	cmd.MarkFlagsMutuallyExclusive("id", "name")
	completeFlag(cmd, "id", completePlans("id"))
	completeFlag(cmd, "name", completePlans("name"))
	r.scope.addFlags(cmd)
}

// resolve returns how the API names the plan: its path, and the project or dir
// that names its project, if any.
func (r planRef) resolve(cmd *cobra.Command) (path, project, dir string, err error) {
	if r.id != "" {
		return "id:" + r.id, r.scope.ref(), "", nil
	}
	if r.scope.given() {
		return r.name, r.scope.ref(), "", nil
	}
	dir, err = workDir(cmd)
	return r.name, "", dir, err
}

// flag is how the command line named the plan, for a hint to repeat.
func (r planRef) flag() string {
	if r.id != "" {
		return "--id " + r.id
	}
	return "--name " + r.name
}

// notFoundHint tells where to look for a plan that was not found.
func (r planRef) notFoundHint(err error) error {
	if problemCode(err) != api.ErrorCodeNotFound || r.id != "" || r.scope.given() {
		return err
	}
	return &hinted{err, "names are unique within a project: run it in the plan's project, or pass --project-name or --project-id"}
}

// planSource names the file a plan's YAML came from, so that the problems the
// daemon found in it are printed as <file>:<line>: <message>, which editors and
// AIs jump to.
type planSource struct {
	err  error
	file string
}

func (e *planSource) Error() string { return e.err.Error() }
func (e *planSource) Unwrap() error { return e.err }

// sourced adds what an error saving a plan read from path needs: its file for an
// invalid_plan, which also gets the hint to the format.
func sourced(err error, path string) error {
	if problemCode(err) != api.ErrorCodeInvalidPlan {
		return err
	}
	file := path
	if path == "-" {
		file = "stdin"
	}
	return &planSource{&hinted{err, identity.Name + " help plans says how to write a plan"}, file}
}

// problemLines are the lines text prints for an invalid_plan read from a file: how
// many problems, then each at its line. None for any other error.
func problemLines(p api.Problem, err error) []string {
	source, ok := errors.AsType[*planSource](err)
	var problems []api.PlanProblem
	if !ok || p.Code != api.ErrorCodeInvalidPlan || !decodeDetail(p, "problems", &problems) || len(problems) == 0 {
		return nil
	}
	lines := []string{fmt.Sprintf("the plan in %s has %s", source.file, plural(len(problems), "problem"))}
	for _, pr := range problems {
		lines = append(lines, fmt.Sprintf("%s:%d: %s", source.file, pr.Line, pr.Message))
	}
	return lines
}

func newPlanCreateCmd() *cobra.Command {
	var scope projectScope
	var name, path, folder string
	cmd := &cobra.Command{
		Use:   "create --name <name> (--path <file> | --path -) [--folder <folder>] [--project-id <id> | --project-name <name>]",
		Short: "Create a plan from its YAML",
		Long: "The YAML is the file --path names, or stdin with --path -, which an AI uses\n" +
			"to send it with no file to write; " + identity.Name + " help plans says how to write it.\n" +
			"Every problem in it is reported with its line, and nothing is created. Names\n" +
			"have letters, numbers and - only, at most 95, and are unique among a\n" +
			"project's plans whatever their case; a doc may have the same name.\n\n" +
			"An after another path already covers is dropped, and said so: the plan is\n" +
			"stored without it.\n\n" +
			"Its folder, / by default, is only where tide shows it, cleaned and checked as\n" +
			"a doc's (" + identity.Name + " doc --help). A plan of plans and the plans it lists share\n" +
			"one: those it lists move to its folder, and moved names them.",
		Example: examples(
			"From a file, in the project you are in", identity.Name+" plan create --name sso --path sso.yaml",
			"In a folder", identity.Name+" plan create --name sso --path sso.yaml --folder /hub",
			"From stdin, as JSON", identity.Name+" plan create --project-name app --name sso --path - --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not created",
			outputNote: "The plan, at revision 1, each after dropped, and the plans it lists moved to its folder.\n" +
				`With --json: {"result": "created", "plan": {...}, "dropped": [{"entry": ..., "after": ..., "via": ...}, ...], "moved": [...]}`,
			resultList: string(api.ResultCreated),
			errorList: codeList(api.ErrorCodeInvalidArgument, api.ErrorCodeInvalidName, api.ErrorCodeInvalidPlan, api.ErrorCodeNameTaken,
				api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			content, err := readContent(cmd, path)
			if err != nil {
				return err
			}
			project, dir, err := docScope(cmd, scope)
			if err != nil {
				return err
			}
			body := api.CreatePlan{Project: optional(project), Dir: optional(dir), Name: name, Content: content}
			if cmd.Flags().Changed("folder") {
				body.Folder = &folder
			}
			r, err := clientOf(cmd).CreatePlan(cmd.Context(), body)
			switch problemCode(err) {
			case api.ErrorCodeNameTaken:
				return &hinted{err, "choose another --name, or change the plan with " + identity.Name + " plan update"}
			case api.ErrorCodeInvalidName:
				return &hinted{err, "choose another --name"}
			}
			if err != nil {
				return sourced(outsideHint(err, scope, ""), path)
			}
			return printResult(cmd, planSaved(r))
		},
	}
	scope.addFlags(cmd)
	cmd.Flags().StringVar(&name, "name", "", "`name` of the plan")
	cmd.Flags().StringVar(&path, "path", "", "`file` holding the plan's YAML, - for stdin")
	addFolderFlag(cmd, &folder, "`folder` tide shows it in, such as /hub (default: /, the top)")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("path")
	completeFlag(cmd, "name", cobra.NoFileCompletions)
	completeFlag(cmd, "path", completeFiles)
	return cmd
}

func newPlanGetCmd() *cobra.Command {
	var ref planRef
	var revision int64
	var out string
	var overwrite bool
	cmd := &cobra.Command{
		Use:   "get (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] [--revision <n>] [--out <file> [--overwrite]]",
		Short: "Print a plan's YAML, or write it to a file to change",
		Long: "The latest revision unless --revision is given, printed byte for byte. With\n" +
			"--out it is written to a file of yours to edit, then given back with plan\n" +
			"update --base and the revision it was read at; a file there is replaced only\n" +
			"with --overwrite. plan status shows what it defines.",
		Example: examples(
			"Print a plan of the project you are in", identity.Name+" plan get --name sso",
			"Write it to a file to change", identity.Name+" plan get --name sso --out sso.yaml",
			"Get it again after it changed", identity.Name+" plan get --name sso --out sso.yaml --overwrite",
			"An earlier revision, as JSON", identity.Name+" plan get --id doc-1a2b3c4d --revision 2 --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The YAML alone; with --out, the revision written and where.\n" +
				`With --json: {"plan": {...}, "revision": ..., "content": "..."}, without content with --out`,
			errorList: codeList(api.ErrorCodeNotFound, api.ErrorCodePathExists, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if overwrite && out == "" {
				return &usageError{errors.New("--overwrite is for --out")}
			}
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			if out, err = absolute(cmd, out); err != nil {
				return err
			}
			p, err := clientOf(cmd).GetPlan(cmd.Context(), path, project, dir, revision)
			if err != nil {
				return ref.notFoundHint(err)
			}
			r := planContent{Plan: p.Plan, Revision: p.Revision}
			if out == "" {
				r.Content = &p.Content
				return printResult(cmd, r)
			}
			if err := writeCopy(cmd.Context(), out, p.Content, overwrite); err != nil {
				return err
			}
			r.out = out
			return printResult(cmd, r)
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().Int64Var(&revision, "revision", 0, "the `revision` to get (default: the latest)")
	cmd.Flags().StringVar(&out, "out", "", "`file` to write it to, read-write, instead of printing it")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "with --out, replace the file if there is one")
	completeFlag(cmd, "revision", completePlanRevisions)
	completeFlag(cmd, "out", completeFiles)
	return cmd
}

func newPlanUpdateCmd() *cobra.Command {
	var ref planRef
	var file string
	var base int64
	cmd := &cobra.Command{
		Use:   "update (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] (--path <file> | --path -) --base <n>",
		Short: "Write a plan's YAML as a new revision",
		Long: "--base is the revision the change was made on, as plan get printed it. If the\n" +
			"plan has a newer revision, nothing is written: get it again, apply the change\n" +
			"to it and update with the new base, so that no change is lost. The YAML is\n" +
			"checked and reduced as in plan create; YAML that comes to the latest revision's\n" +
			"makes none.",
		Example: examples(
			"Write back a changed copy", identity.Name+" plan update --name sso --path sso.yaml --base 2",
			"From stdin, as JSON", identity.Name+" plan update --id doc-1a2b3c4d --path - --base 2 --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not updated",
			outputNote: "The plan's revision now, each after dropped, and the plans it lists moved to its folder.\n" +
				`With --json: {"result": "updated", "plan": {...}, "dropped": [...], "moved": [...]}`,
			resultList: annotation(string(api.ResultUpdated), string(api.ResultUnchanged)),
			errorList: codeList(api.ErrorCodeInvalidArgument, api.ErrorCodeInvalidPlan, api.ErrorCodeNotFound, api.ErrorCodePlanChanged,
				api.ErrorCodePlanStarted, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			content, err := readContent(cmd, file)
			if err != nil {
				return err
			}
			r, err := clientOf(cmd).UpdatePlanContent(cmd.Context(), path, project, dir, base, content)
			if problemCode(err) == api.ErrorCodePlanChanged {
				return planChangedHint(err, ref)
			}
			if err != nil {
				return sourced(ref.notFoundHint(err), file)
			}
			return printResult(cmd, planSaved(r))
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().StringVar(&file, "path", "", "`file` holding the new YAML, - for stdin")
	cmd.Flags().Int64Var(&base, "base", 0, "the `revision` the change was made on")
	_ = cmd.MarkFlagRequired("path")
	_ = cmd.MarkFlagRequired("base")
	completeFlag(cmd, "path", completeFiles)
	completeFlag(cmd, "base", completePlanRevisions)
	return cmd
}

// planChangedHint tells how to apply a change to a plan that moved on since its
// base.
func planChangedHint(err error, ref planRef) error {
	var p api.Plan
	if e, ok := errors.AsType[*client.ProblemError](err); !ok || !decodeDetail(e.Problem, "plan", &p) {
		return err
	}
	return &hinted{err, fmt.Sprintf("get it again with %s plan get %s --out <file> --overwrite, apply your change, and update with --base %d",
		identity.Name, ref.flag(), p.Revision)}
}

// planInUseHint tells how to free a plan a plan of plans lists.
func planInUseHint(err error) error {
	var parent api.Plan
	if e, ok := errors.AsType[*client.ProblemError](err); !ok || !decodeDetail(e.Problem, "parent", &parent) {
		return err
	}
	return &hinted{err, fmt.Sprintf("take it out of %s first: %s plan get --name %s --out <file>, then %s plan update",
		parent.Name, identity.Name, parent.Name, identity.Name)}
}

func newPlanListCmd() *cobra.Command {
	var scope projectScope
	var all bool
	cmd := &cobra.Command{
		Use:   "list [--project-id <id> | --project-name <name>] [--all]",
		Short: "List a project's plans",
		Long: "Without a project flag, the plans of the project the working directory is in.\n" +
			"The archived are left out unless --all is given.",
		Example: examples(
			"The plans of the project you are in", identity.Name+" plan list",
			"The archived too", identity.Name+" plan list --all",
			"Of another project, as JSON", identity.Name+" plan list --project-name app --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The plans by folder, then name, without their YAML; an empty list when there are none.\n" + `With --json: {"plans": [...]}`,
			errorList:  codeList(api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			project, dir, err := docScope(cmd, scope)
			if err != nil {
				return err
			}
			list, err := clientOf(cmd).ListPlans(cmd.Context(), project, dir, all)
			if err != nil {
				return outsideHint(err, scope, "")
			}
			return printResult(cmd, planList(list))
		},
	}
	scope.addFlags(cmd)
	cmd.Flags().BoolVar(&all, "all", false, "list the archived plans too")
	return cmd
}

func newPlanHistoryCmd() *cobra.Command {
	var ref planRef
	cmd := &cobra.Command{
		Use:   "history (--id <id> | --name <name>) [--project-id <id> | --project-name <name>]",
		Short: "List a plan's revisions",
		Long:  "Every revision is kept until the plan is removed; plan get --revision reads one.",
		Example: examples(
			"The revisions of a plan of the project you are in", identity.Name+" plan history --name sso",
			"By id, as JSON", identity.Name+" plan history --id doc-1a2b3c4d --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The revisions, latest first, without their YAML.\n" + `With --json: {"revisions": [...]}`,
			errorList:  codeList(api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			list, err := clientOf(cmd).ListPlanRevisions(cmd.Context(), path, project, dir)
			if err != nil {
				return ref.notFoundHint(err)
			}
			return printResult(cmd, docHistory(list))
		},
	}
	ref.addFlags(cmd)
	return cmd
}

func newPlanRenameCmd() *cobra.Command {
	var ref planRef
	var newName string
	cmd := &cobra.Command{
		Use:   "rename (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] --new-name <name>",
		Short: "Rename a plan",
		Long: "Its id, YAML and revisions stay as they are; changing only the case of its\n" +
			"name is a rename. A plan a plan of plans lists keeps its name until it is\n" +
			"taken out of it, so that the parent does not name a plan that is not there.",
		Example: examples(
			"Rename a plan of the project you are in", identity.Name+" plan rename --name sso --new-name sso-login",
			"By id, as JSON", identity.Name+" plan rename --id doc-1a2b3c4d --new-name sso-login --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not renamed",
			outputNote:    "The plan's name before and now.\n" + `With --json: {"result": "updated", "plan": {...}}`,
			resultList:    annotation(string(api.ResultUpdated), string(api.ResultUnchanged)),
			errorList: codeList(api.ErrorCodeInvalidName, api.ErrorCodeNameTaken, api.ErrorCodeNotFound, api.ErrorCodePlanInUse,
				api.ErrorCodePlanStarted, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			r, err := clientOf(cmd).RenamePlan(cmd.Context(), path, project, dir, newName)
			switch problemCode(err) {
			case api.ErrorCodeNameTaken, api.ErrorCodeInvalidName:
				return &hinted{err, "choose another --new-name"}
			case api.ErrorCodePlanInUse:
				return planInUseHint(err)
			}
			if err != nil {
				return ref.notFoundHint(err)
			}
			return printResult(cmd, planRenamed{r, cmp.Or(ref.name, ref.id)})
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().StringVar(&newName, "new-name", "", "the plan's new `name`")
	_ = cmd.MarkFlagRequired("new-name")
	completeFlag(cmd, "new-name", cobra.NoFileCompletions)
	return cmd
}

const removePlanQuestion = "remove plan"

func newPlanRemoveCmd() *cobra.Command {
	var ref planRef
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] [--yes]",
		Short: "Delete a plan and all its revisions",
		Long: "You are asked first, as nothing brings it back; --yes answers in advance, and\n" +
			"with --json, or without a terminal, it is required. A plan a plan of plans\n" +
			"lists stays until it is taken out of it; removing a plan of plans leaves the\n" +
			"plans it listed as they are, with no parent.",
		Example: examples(
			"Remove a plan of the project you are in", identity.Name+" plan remove --name sso",
			"From a script", identity.Name+" plan remove --id doc-1a2b3c4d --yes --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not removed",
			outputNote:    "The removed plan, as it was before.\n" + `With --json: {"result": "removed", "plan": {...}}`,
			resultList:    string(api.ResultRemoved),
			errorList:     codeList(api.ErrorCodeCancelled, api.ErrorCodeNotFound, api.ErrorCodePlanInUse, api.ErrorCodePlanStarted, api.ErrorCodeDaemonUnavailable),
			jsonRequires:  annotation("yes"),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			if yes {
				logAnswer(cmd, removePlanQuestion, "yes", "--yes")
			} else if err := confirmPlanRemove(cmd, ref, path, project, dir); err != nil {
				return err
			}
			r, err := clientOf(cmd).DeletePlan(cmd.Context(), path, project, dir)
			if problemCode(err) == api.ErrorCodePlanInUse {
				return planInUseHint(err)
			}
			if err != nil {
				return ref.notFoundHint(err)
			}
			return printResult(cmd, planRemoved(r))
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().BoolVar(&yes, "yes", false, "remove it without asking")
	return cmd
}

// confirmPlanRemove asks whether to remove the plan; nil means yes.
func confirmPlanRemove(cmd *cobra.Command, ref planRef, path, project, dir string) error {
	if !canPrompt(cmd) {
		logUnanswered(cmd, removePlanQuestion)
		return &usageError{fmt.Errorf("removing a plan needs an answer, and none can be asked (%s): pass --yes", noPromptReason(cmd))}
	}
	p, err := clientOf(cmd).GetPlan(cmd.Context(), path, project, dir, 0)
	if err != nil {
		return ref.notFoundHint(err)
	}
	prompt, st := cmd.ErrOrStderr(), stderrStyle(cmd)
	question := fmt.Sprintf("remove plan %s and its %s? %s: ", st.Name(p.Plan.Name), plural(int(p.Plan.Revision), "revision"), st.Command("[y/N]"))
	answer, err := ask(prompt, bufio.NewScanner(cmd.InOrStdin()), question, "y", "n", "")
	if err != nil {
		return err
	}
	if answer != "y" {
		logAnswer(cmd, removePlanQuestion, "no", "prompt")
		return &client.ProblemError{Problem: api.NewProblem(api.ErrorCodeCancelled, "you answered no")}
	}
	logAnswer(cmd, removePlanQuestion, "yes", "prompt")
	return nil
}

func newPlanStatusCmd() *cobra.Command {
	var ref planRef
	cmd := &cobra.Command{
		Use:   "status (--id <id> | --name <name>) [--project-id <id> | --project-name <name>]",
		Short: "Show what a plan defines and how its run goes: its goal, and its tasks or plans in DAG order",
		Long: "DAG order lists an entry after everything it waits for, keeping the YAML's\n" +
			"order where it is free to: a plan written in order reads as written. Once it\n" +
			"runs, each task's state, agent and worktree.",
		Example: examples(
			"A plan of the project you are in", identity.Name+" plan status --name sso",
			"By id, as JSON", identity.Name+" plan status --id doc-1a2b3c4d --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The plan's state, goal and constraints, then a row per task or plan.\n" +
				`With --json: {"plan": {...}, "revision": ..., "definition": {"goal": ..., "constraints": [...], "tasks": [...], "plans": [...]}, "run": {...}}`,
			errorList: codeList(api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			p, err := clientOf(cmd).GetPlan(cmd.Context(), path, project, dir, 0)
			if err != nil {
				return ref.notFoundHint(err)
			}
			return printResult(cmd, planStatus{Plan: p.Plan, Revision: p.Revision, Definition: p.Definition, Run: p.Run})
		},
	}
	ref.addFlags(cmd)
	return cmd
}

// completePlans completes --id or --name, as key says, with the plans of the
// project the command line names, else of the one the working directory is in.
func completePlans(key string) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		ctx, cancel := context.WithTimeout(cmd.Context(), cmp.Or(invocationOf(cmd).env.Timeouts.Completion, completionTimeout))
		defer cancel()
		var scope projectScope
		scope.id, _ = cmd.Flags().GetString("project-id")
		scope.name, _ = cmd.Flags().GetString("project-name")
		var dir string
		if !scope.given() {
			dir = invocationOf(cmd).env.WorkDir
		}
		list, err := clientOf(cmd).ListPlans(ctx, scope.ref(), dir, true)
		if err != nil {
			logging.FromContext(cmd.Context()).Info("no plans to complete", "err", err)
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var completions []cobra.Completion
		for _, p := range list.Plans {
			if key == "id" {
				completions = append(completions, cobra.CompletionWithDesc(p.Id, fmt.Sprintf("%s in %s", p.Name, p.ProjectName)))
			} else {
				completions = append(completions, cobra.CompletionWithDesc(p.Name, fmt.Sprintf("%s, %s", planSize(p), p.Id)))
			}
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	}
}

// completePlanRevisions completes --revision or --base with the revisions of the
// plan the command line names.
func completePlanRevisions(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	ctx, cancel := context.WithTimeout(cmd.Context(), cmp.Or(invocationOf(cmd).env.Timeouts.Completion, completionTimeout))
	defer cancel()
	var ref planRef
	ref.id, _ = cmd.Flags().GetString("id")
	ref.name, _ = cmd.Flags().GetString("name")
	ref.scope.id, _ = cmd.Flags().GetString("project-id")
	ref.scope.name, _ = cmd.Flags().GetString("project-name")
	if ref.id == "" && ref.name == "" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	path, project, dir, err := ref.resolve(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	list, err := clientOf(cmd).ListPlanRevisions(ctx, path, project, dir)
	if err != nil {
		logging.FromContext(cmd.Context()).Info("no revisions to complete", "err", err)
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var completions []cobra.Completion
	for _, r := range list.Revisions {
		completions = append(completions, cobra.CompletionWithDesc(strconv.FormatInt(r.Number, 10), r.CreatedAt.Local().Format(time.DateTime)))
	}
	return completions, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
}

// planSize is how many tasks a plan has, or plans for a plan of plans.
func planSize(p api.Plan) string {
	if p.Plans != nil {
		return plural(len(*p.Plans), "plan")
	}
	if p.Tasks != nil {
		return plural(int(*p.Tasks), "task")
	}
	return ""
}

// droppedLines writes a line for each after reduction dropped.
// archivedText says a plan is archived, after its state.
func archivedText(st style.Style, p api.Plan) string {
	if p.ArchivedAt == nil {
		return ""
	}
	return st.Muted(", archived")
}

// nameList is names joined for a sentence, each styled as a name.
func nameList(st style.Style, names []string) string {
	styled := make([]string, len(names))
	for i, n := range names {
		styled[i] = st.Name(n)
	}
	return strings.Join(styled, ", ")
}

func droppedLines(w io.Writer, st style.Style, dropped *[]api.PlanDropped) {
	if dropped == nil {
		return
	}
	for _, d := range *dropped {
		fmt.Fprintf(w, "%s %s after %s: %s already waits for it\n", st.Warning("dropped"), st.Name(d.Entry), st.Name(d.After), st.Name(d.Via))
	}
}

// planSaved is a plan a command created, or whose YAML it wrote.
type planSaved api.PlanResult

func (r planSaved) text(w io.Writer, st style.Style) {
	p := r.Plan
	switch r.Result {
	case api.ResultCreated:
		fmt.Fprintf(w, "plan %s %s, with %s\n", st.Name(p.Name), st.Success("created"), planSize(p))
	case api.ResultUnchanged:
		fmt.Fprintf(w, "%s is unchanged at revision %d\n", st.Name(p.Name), p.Revision)
	default:
		fmt.Fprintf(w, "%s is at revision %s, with %s\n", st.Name(p.Name), st.Success(strconv.FormatInt(p.Revision, 10)), planSize(p))
	}
	droppedLines(w, st, r.Dropped)
	if r.Moved != nil && len(*r.Moved) > 0 {
		fmt.Fprintf(w, "%s %s to %s, as it lists them\n", st.Success("moved"), nameList(st, *r.Moved), st.Name(p.Folder))
	}
	if r.Result == api.ResultCreated {
		planFields(w, st, p)
	}
}

func planFields(w io.Writer, st style.Style, p api.Plan) {
	field := fieldPrinter(w, st, 8)
	field("project", p.ProjectName)
	field("folder", p.Folder)
	field("state", planStateText(st, p.State)+archivedText(st, p))
	field("revision", strconv.FormatInt(p.Revision, 10))
	if p.Parent != nil {
		field("parent", st.Name(*p.Parent))
	}
	field("id", st.Muted(p.Id))
}

// planRemoved is a plan a command removed.
type planRemoved api.PlanResult

func (r planRemoved) text(w io.Writer, st style.Style) {
	fmt.Fprintf(w, "plan %s %s\n", st.Name(r.Plan.Name), st.Success("removed"))
	planFields(w, st, r.Plan)
}

// planRenamed is a plan a command renamed from the name or id it was given by.
type planRenamed struct {
	api.PlanResult
	from string
}

func (r planRenamed) text(w io.Writer, st style.Style) {
	if r.Result == api.ResultUnchanged {
		fmt.Fprintf(w, "plan %s already has that name\n", st.Name(r.Plan.Name))
		return
	}
	fmt.Fprintf(w, "plan %s renamed to %s\n", st.Name(r.from), st.Name(r.Plan.Name))
}

// planContent is a revision of a plan: its YAML, or where it was written.
type planContent struct {
	Plan     api.Plan `json:"plan"`
	Revision int64    `json:"revision"`
	Content  *string  `json:"content,omitempty"`
	out      string
}

func (r planContent) text(w io.Writer, st style.Style) {
	if r.Content != nil {
		fmt.Fprint(w, *r.Content)
		return
	}
	fmt.Fprintf(w, "revision %d of %s written to %s\n", r.Revision, st.Name(r.Plan.Name), r.out)
}

// LogValue measures the YAML: its prompts can hold secrets.
func (r planContent) LogValue() slog.Value { return planLogged(r) }

// planStatus is what a plan defines.
type planStatus struct {
	Plan       api.Plan           `json:"plan"`
	Revision   int64              `json:"revision"`
	Definition api.PlanDefinition `json:"definition"`
	Run        *api.PlanRun       `json:"run"`
}

func (r planStatus) text(w io.Writer, st style.Style) {
	p, d := r.Plan, r.Definition
	switch {
	case r.Run == nil:
		fmt.Fprintf(w, "%s: %s%s, %s\n", st.Name(p.Name), planStateText(st, p.State), archivedText(st, p), planSize(p))
	case d.Plans != nil:
		fmt.Fprintf(w, "%s: %s%s, %s done\n", st.Name(p.Name), runStateText(st, p, r.Run), archivedText(st, p), planProgress(p))
	default:
		fmt.Fprintf(w, "%s: %s, %s tasks done%s, on %s\n", st.Name(p.Name), runStateText(st, p, r.Run), planProgress(p), planFailed(st, p), st.Name(r.Run.Branch))
	}
	field := fieldPrinter(w, st, 11)
	field("goal", strings.TrimSpace(d.Goal))
	for _, c := range d.Constraints {
		field("constraint", c)
	}
	if p.Parent != nil {
		field("parent", st.Name(*p.Parent))
	}
	var table style.Table
	if d.Plans != nil {
		table.Row(st.Heading("PLAN"), st.Heading("STATE"), st.Heading("AFTER"), st.Heading("TASKS"))
		for _, c := range *d.Plans {
			table.Row(st.Name(c.Id), childState(st, c, *d.Plans, r.Run != nil), strings.Join(c.After, ", "), childTasks(c))
		}
	} else if d.Tasks != nil && r.Run == nil {
		table.Row(st.Heading("TASK"), st.Heading("AFTER"), st.Heading("STATE"))
		for _, t := range *d.Tasks {
			table.Row(st.Name(t.Id), strings.Join(t.After, ", "), "")
		}
	} else if d.Tasks != nil {
		table.Row(st.Heading("TASK"), st.Heading("STATE"), st.Heading("AFTER"), st.Heading("AGENT"), st.Heading("SLOT"))
		for _, t := range *d.Tasks {
			agent, slot := "", ""
			if t.Agent != nil {
				agent = t.Agent.Name
			}
			if t.SlotPath != nil {
				slot = filepath.Base(*t.SlotPath)
			}
			table.Row(st.Name(t.Id), taskState(st, t), strings.Join(t.After, ", "), agent, slot)
		}
	}
	table.Write(w)
}

// childState is the state of a plan a plan of plans lists, or what a draft waits
// for while its parent runs: the person, or the plans before it not done.
func childState(st style.Style, c api.PlanChild, all []api.PlanChild, parentRan bool) string {
	if c.Waiting {
		return st.Warning("waiting for you")
	}
	if c.State != api.PlanStateDraft || !parentRan {
		return planStateText(st, c.State)
	}
	var before []string
	for _, a := range c.After {
		i := slices.IndexFunc(all, func(o api.PlanChild) bool { return o.Id == a })
		if i < 0 || all[i].State != api.PlanStateDone {
			before = append(before, a)
		}
	}
	if len(before) == 0 {
		return planStateText(st, c.State)
	}
	return st.Muted("waiting for ") + nameList(st, before)
}

// childTasks is how far a plan a plan of plans lists is, as 2/4, or its size.
func childTasks(c api.PlanChild) string {
	if c.TasksDone != nil {
		return strconv.FormatInt(*c.TasksDone, 10) + "/" + strconv.FormatInt(c.Tasks, 10)
	}
	return plural(int(c.Tasks), "task")
}

// LogValue measures the goal, constraints and prompts: they are what agents are
// told, which can hold secrets.
func (r planStatus) LogValue() slog.Value { return planLogged(r) }

// planLogged is v, a response holding a plan's text, as logged.
func planLogged(v any) slog.Value {
	b, err := json.Marshal(v)
	if err != nil {
		return slog.StringValue("unloggable plan")
	}
	return slog.AnyValue(api.PlanResponseLogged(b))
}

// planList is a project's plans.
type planList api.PlanList

func (l planList) text(w io.Writer, st style.Style) {
	if len(l.Plans) == 0 {
		fmt.Fprintln(w, "no plans")
		return
	}
	var table style.Table
	table.Row(st.Heading("FOLDER"), st.Heading("NAME"), st.Heading("STATE"), st.Heading("TASKS"), st.Heading("REVISION"), st.Heading("UPDATED"))
	for _, p := range l.Plans {
		table.Row(p.Folder, st.Name(p.Name), planStateText(st, p.State)+archivedText(st, p), planProgress(p)+planFailed(st, p), strconv.FormatInt(p.Revision, 10), p.UpdatedAt.Local().Format(time.DateTime))
	}
	table.Write(w)
}
