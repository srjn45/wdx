package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

// The rest of a plan's lifecycle (docs/PLANS.md, step 4): a copy to run again or
// start from, and archiving one that is over.

func newPlanCopyCmd() *cobra.Command {
	var ref planRef
	var newName, folder string
	cmd := &cobra.Command{
		Use:   "copy (--id <id> | --name <name>) --new-name <name> [--folder <folder>] [--project-id <id> | --project-name <name>]",
		Short: "Create a draft from a plan's latest revision, to run again or start from",
		Long: "A plan runs once: running it again is a copy, then plan run. The plan can be\n" +
			"in any state, archived too. The copy is checked as plan create checks a new\n" +
			"plan, in the same project, and has none of the plan's history, run, branch or\n" +
			"worktrees. A plan of plans cannot be copied, as the plans it lists already\n" +
			"have a parent. It goes in --folder, or the plan's folder.",
		Example: examples(
			"Run a plan of the project you are in again", identity.Name+" plan copy --name sso --new-name sso-2",
			"In another folder, as JSON", identity.Name+" plan copy --name sso --new-name sso-2 --folder /later --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not copied",
			outputNote:    "The copy, at revision 1.\n" + `With --json: {"result": "created", "plan": {...}, "dropped": [], "moved": []}`,
			resultList:    string(api.ResultCreated),
			errorList: codeList(api.ErrorCodeInvalidArgument, api.ErrorCodeInvalidName, api.ErrorCodeInvalidPlan, api.ErrorCodeNameTaken,
				api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			body := api.CopyPlan{Name: newName}
			if cmd.Flags().Changed("folder") {
				body.Folder = &folder
			}
			r, err := clientOf(cmd).CopyPlan(cmd.Context(), path, project, dir, body)
			switch problemCode(err) {
			case api.ErrorCodeNameTaken, api.ErrorCodeInvalidName:
				return &hinted{err, "choose another --new-name"}
			}
			if err != nil {
				return ref.notFoundHint(err)
			}
			return printResult(cmd, planCopied{PlanResult: r, from: ref.name})
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().StringVar(&newName, "new-name", "", "`name` of the copy")
	addFolderFlag(cmd, &folder, "`folder` of the copy (default: the plan's)")
	_ = cmd.MarkFlagRequired("new-name")
	completeFlag(cmd, "new-name", cobra.NoFileCompletions)
	return cmd
}

// planCopied is a copy plan copy created; from is the name it was asked by, empty
// for an id.
type planCopied struct {
	api.PlanResult
	from string
}

func (r planCopied) text(w io.Writer, st style.Style) {
	from := ""
	if r.from != "" {
		from = " from " + st.Name(r.from)
	}
	fmt.Fprintf(w, "plan %s %s%s, with %s\n", st.Name(r.Plan.Name), st.Success("created"), from, planSize(r.Plan))
	planFields(w, st, r.Plan)
}

func newPlanArchiveCmd() *cobra.Command {
	return newPlanRunAnswerCmd("archive", "Archive a done or cancelled plan, leaving it out of plan list",
		"Nothing else of it changes: its branch, its worktree, its summaries and its\n"+
			"history stay, and plan list --all shows it.",
		api.ResultArchived, []api.ErrorCode{api.ErrorCodePlanNotFinished},
		func(c *client.Client, cmd *cobra.Command, path, project, dir string) (api.PlanResult, error) {
			return c.ArchivePlan(cmd.Context(), path, project, dir)
		})
}

func newPlanUnarchiveCmd() *cobra.Command {
	return newPlanRunAnswerCmd("unarchive", "Show an archived plan in plan list again", "",
		api.ResultUnarchived, []api.ErrorCode{api.ErrorCodePlanNotFinished},
		func(c *client.Client, cmd *cobra.Command, path, project, dir string) (api.PlanResult, error) {
			return c.UnarchivePlan(cmd.Context(), path, project, dir)
		})
}
