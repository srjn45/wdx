package cli

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

// planNotRunningHint says what a plan that is not running or paused takes.
func planNotRunningHint(err error) error {
	return &hinted{err, "only a running or paused plan takes this: " + identity.Name + " plan status says what it is"}
}

// newPlanRunAnswerCmd is pause or resume: verb the command, done what it does,
// ask the client call.
func newPlanRunAnswerCmd(verb, short, long string, result api.Result, extraErrors []api.ErrorCode,
	ask func(c *client.Client, cmd *cobra.Command, path, project, dir string) (api.PlanResult, error)) *cobra.Command {
	var ref planRef
	errs := append([]api.ErrorCode{api.ErrorCodeNotFound, api.ErrorCodePlanNotRunning}, extraErrors...)
	cmd := &cobra.Command{
		Use:   verb + " (--id <id> | --name <name>) [--project-id <id> | --project-name <name>]",
		Short: short,
		Long:  long,
		Example: examples(
			"A plan of the project you are in", identity.Name+" plan "+verb+" --name sso",
			"By id, as JSON", identity.Name+" plan "+verb+" --id doc-1a2b3c4d --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not " + string(result),
			outputNote:    "The plan.\n" + `With --json: {"result": "` + string(result) + `", "plan": {...}}`,
			resultList:    annotation(string(result), string(api.ResultUnchanged)),
			errorList:     codeList(append(errs, api.ErrorCodeDaemonUnavailable)...),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			r, err := ask(clientOf(cmd), cmd, path, project, dir)
			switch problemCode(err) {
			case api.ErrorCodePlanNotRunning:
				return planNotRunningHint(err)
			case api.ErrorCodeProjectClosed:
				return &hinted{err, "open it first with " + identity.Name + " project open; its plans go on as they were"}
			}
			if err != nil {
				return ref.notFoundHint(err)
			}
			return printResult(cmd, planAnswered{PlanResult: r, verb: verb})
		},
	}
	ref.addFlags(cmd)
	return cmd
}

func newPlanPauseCmd() *cobra.Command {
	return newPlanRunAnswerCmd("pause", "Pause a running plan: nothing new starts, and tasks at work go on and land",
		"A plan paused while its project is closed stays paused when the project opens.",
		api.ResultPaused, nil,
		func(c *client.Client, cmd *cobra.Command, path, project, dir string) (api.PlanResult, error) {
			return c.PausePlan(cmd.Context(), path, project, dir)
		})
}

func newPlanResumeCmd() *cobra.Command {
	return newPlanRunAnswerCmd("resume", "Resume a paused plan, which starts what its pause held back",
		"Refused while its project is closed, as nothing could run: a plan running when\n"+
			"its project closed shows paused, and goes on by itself when the project opens.",
		api.ResultResumed, []api.ErrorCode{api.ErrorCodeProjectClosed},
		func(c *client.Client, cmd *cobra.Command, path, project, dir string) (api.PlanResult, error) {
			return c.ResumePlan(cmd.Context(), path, project, dir)
		})
}

// planAnswered is a plan paused, resumed, cancelled or given a new parallel.
type planAnswered struct {
	api.PlanResult
	verb     string
	parallel int
}

func (r planAnswered) text(w io.Writer, st style.Style) {
	name := st.Name(r.Plan.Name)
	unchanged := r.Result == api.ResultUnchanged
	switch {
	case r.verb == "pause" && unchanged:
		fmt.Fprintf(w, "plan %s was already paused\n", name)
	case r.verb == "pause":
		fmt.Fprintf(w, "plan %s %s: nothing new starts, and its tasks at work go on\n", name, st.Success("paused"))
	case r.verb == "resume" && unchanged:
		fmt.Fprintf(w, "plan %s was already running\n", name)
	case r.verb == "resume":
		fmt.Fprintf(w, "plan %s %s again\n", name, st.Success("running"))
	case r.verb == "cancel":
		fmt.Fprintf(w, "plan %s %s: its agents stop, its worktrees holding work stay, and so does %s\n",
			name, st.Success("cancelled"), st.Name(planBranch(r.Plan.Name)))
	case unchanged:
		fmt.Fprintf(w, "plan %s already runs up to %s at once\n", name, plural(r.parallel, "task"))
	default:
		fmt.Fprintf(w, "plan %s %s up to %s at once\n", name, st.Success("runs"), plural(r.parallel, "task"))
	}
}

// cancelPlanQuestion is the question plan cancel asks.
const cancelPlanQuestion = "cancel plan"

func newPlanCancelCmd() *cobra.Command {
	var ref planRef
	var yes bool
	cmd := &cobra.Command{
		Use:   "cancel (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] [--yes]",
		Short: "Cancel a running or paused plan: its agents stop, and it cannot run again",
		Long: "Its worktrees holding work stay as ordinary worktrees, the others are\n" +
			"removed, and its branch stays: nothing done is lost. You are asked first, as\n" +
			"a cancelled plan cannot go on; --yes answers in advance, and with --json, or\n" +
			"without a terminal, it is required.",
		Example: examples(
			"Cancel a plan of the project you are in", identity.Name+" plan cancel --name sso",
			"From a script", identity.Name+" plan cancel --id doc-1a2b3c4d --yes --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not cancelled",
			outputNote:    "The plan, cancelled.\n" + `With --json: {"result": "cancelled", "plan": {...}}`,
			resultList:    string(api.ResultCancelled),
			errorList:     codeList(api.ErrorCodeCancelled, api.ErrorCodeNotFound, api.ErrorCodePlanNotRunning, api.ErrorCodeDaemonUnavailable),
			jsonRequires:  annotation("yes"),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			if yes {
				logAnswer(cmd, cancelPlanQuestion, "yes", "--yes")
			} else if err := confirmPlanCancel(cmd, ref, path, project, dir); err != nil {
				return err
			}
			r, err := clientOf(cmd).CancelPlan(cmd.Context(), path, project, dir)
			if problemCode(err) == api.ErrorCodePlanNotRunning {
				return planNotRunningHint(err)
			}
			if err != nil {
				return ref.notFoundHint(err)
			}
			return printResult(cmd, planAnswered{PlanResult: r, verb: "cancel"})
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().BoolVar(&yes, "yes", false, "cancel it without asking")
	return cmd
}

// confirmPlanCancel asks whether to cancel the plan; nil means yes.
func confirmPlanCancel(cmd *cobra.Command, ref planRef, path, project, dir string) error {
	if !canPrompt(cmd) {
		logUnanswered(cmd, cancelPlanQuestion)
		return &usageError{fmt.Errorf("cancelling a plan needs an answer, and none can be asked (%s): pass --yes", noPromptReason(cmd))}
	}
	p, err := clientOf(cmd).GetPlan(cmd.Context(), path, project, dir, 0)
	if err != nil {
		return ref.notFoundHint(err)
	}
	prompt, st := cmd.ErrOrStderr(), stderrStyle(cmd)
	question := fmt.Sprintf("cancel plan %s? its running agents stop and it cannot run again %s: ", st.Name(p.Plan.Name), st.Command("[y/N]"))
	answer, err := ask(prompt, bufio.NewScanner(cmd.InOrStdin()), question, "y", "n", "")
	if err != nil {
		return err
	}
	if answer != "y" {
		logAnswer(cmd, cancelPlanQuestion, "no", "prompt")
		return &client.ProblemError{Problem: api.NewProblem(api.ErrorCodeCancelled, "you answered no")}
	}
	logAnswer(cmd, cancelPlanQuestion, "yes", "prompt")
	return nil
}

func newPlanSetCmd() *cobra.Command {
	var ref planRef
	var parallel int
	cmd := &cobra.Command{
		Use:   "set (--id <id> | --name <name>) --parallel <n> [--project-id <id> | --project-name <name>]",
		Short: "Change how many tasks a running or paused plan runs at once",
		Long: "More start at once when it rises; tasks at work go on when it falls. The\n" +
			"setting plan.parallel is the number for every plan whose run sets none.",
		Example: examples(
			"Four tasks at once", identity.Name+" plan set --name sso --parallel 4",
			"As JSON", identity.Name+" plan set --name sso --parallel 1 --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not changed",
			outputNote:    "The plan.\n" + `With --json: {"result": "updated", "plan": {...}}`,
			resultList:    annotation(string(api.ResultUpdated), string(api.ResultUnchanged)),
			errorList:     codeList(api.ErrorCodeNotFound, api.ErrorCodePlanNotRunning, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if parallel < 1 || parallel > 20 {
				return &usageError{fmt.Errorf("--parallel %d is not from 1 to 20", parallel)}
			}
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			r, err := clientOf(cmd).SetPlanParallel(cmd.Context(), path, project, dir, parallel)
			if problemCode(err) == api.ErrorCodePlanNotRunning {
				return planNotRunningHint(err)
			}
			if err != nil {
				return ref.notFoundHint(err)
			}
			return printResult(cmd, planAnswered{PlanResult: r, verb: "set", parallel: parallel})
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().IntVar(&parallel, "parallel", 0, "the most tasks it runs at once, a `number` from 1 to 20")
	_ = cmd.MarkFlagRequired("parallel")
	completeFlag(cmd, "parallel", cobra.NoFileCompletions)
	return cmd
}

// newPlanTaskAnswerCmd is a person's answer to a task: verb the command, result
// what it returns, ask the client call.
func newPlanTaskAnswerCmd(verb, short, long string, result api.Result, extraErrors []api.ErrorCode, exampleFlags string,
	addFlags func(*cobra.Command), ask func(c *client.Client, cmd *cobra.Command, path, project, dir, task string) (api.PlanTaskResult, error)) *cobra.Command {
	var ref taskRef
	errs := append([]api.ErrorCode{api.ErrorCodeNotFound, api.ErrorCodePlanNotRunning, api.ErrorCodeTaskState}, extraErrors...)
	ex := []string{"A task of a plan of the project you are in", identity.Name + " plan task " + verb + " --plan sso --task schema"}
	if exampleFlags != "" {
		ex = append(ex, "With "+exampleFlags+", as JSON", identity.Name+" plan task "+verb+" --plan sso --task schema "+exampleFlags+" --json")
	} else {
		ex = append(ex, "Of another project, as JSON", identity.Name+" plan task "+verb+" --project-name app --plan sso --task schema --json")
	}
	cmd := &cobra.Command{
		Use:     verb + " --plan <name> --task <id> [--project-id <id> | --project-name <name>]",
		Short:   short,
		Long:    long,
		Example: examples(ex...),
		Args:    cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not " + string(result),
			outputNote:    "The task.\n" + `With --json: {"result": "` + string(result) + `", "task": {...}}`,
			resultList:    string(result),
			errorList:     codeList(append(errs, api.ErrorCodeDaemonUnavailable)...),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			r, err := ask(clientOf(cmd), cmd, path, project, dir, ref.task)
			switch problemCode(err) {
			case api.ErrorCodePlanNotRunning:
				return planNotRunningHint(err)
			case api.ErrorCodeTaskState:
				return &hinted{err, identity.Name + " plan task get says what it is"}
			case api.ErrorCodeAgentWorking:
				return &hinted{err, "stop or remove the agent at work in the task's worktree first"}
			case api.ErrorCodeNotCommitted:
				return &hinted{err, "commit what was done on the task's branch first"}
			case api.ErrorCodeWrongBranch:
				return &hinted{err, "switch the worktree back to the task's branch, with its commits"}
			}
			if err != nil {
				return err
			}
			return printResult(cmd, taskAnswered{PlanTaskResult: r, plan: path, verb: verb})
		},
	}
	ref.addFlags(cmd)
	_ = cmd.MarkFlagRequired("plan")
	_ = cmd.MarkFlagRequired("task")
	if addFlags != nil {
		addFlags(cmd)
	}
	return cmd
}

func newPlanTaskRetryCmd() *cobra.Command {
	var fresh bool
	return newPlanTaskAnswerCmd("retry", "Run a failed or land_failed task again, keeping its worktree and commits",
		"Its agent is resumed, told what went wrong (it ended, its landing conflicted, or\n"+
			"the check failed), and lands the task again with plan task done. With --fresh,\n"+
			"or when its worktree is gone, a new agent starts instead, named after the\n"+
			"attempt, told a previous attempt left its commits. It starts once the plan has\n"+
			"room under its parallel, and only while the plan runs.",
		api.ResultRetried, nil, "--fresh",
		func(cmd *cobra.Command) {
			cmd.Flags().BoolVar(&fresh, "fresh", false, "start a new agent even when the task's could be resumed")
		},
		func(c *client.Client, cmd *cobra.Command, path, project, dir, task string) (api.PlanTaskResult, error) {
			return c.RetryPlanTask(cmd.Context(), path, project, dir, task, fresh)
		})
}

func newPlanTaskLandCmd() *cobra.Command {
	return newPlanTaskAnswerCmd("land", "Land a land_failed task again, once you dealt with what stopped it",
		"For a conflict or a check you resolved yourself in the task's worktree: commit\n"+
			"it on the task's branch first. To have its agent do it, use plan task retry.\n"+
			"Refused while an agent is at work in the worktree.",
		api.ResultLanding, []api.ErrorCode{api.ErrorCodeAgentWorking, api.ErrorCodeNotCommitted, api.ErrorCodeWrongBranch}, "",
		nil,
		func(c *client.Client, cmd *cobra.Command, path, project, dir, task string) (api.PlanTaskResult, error) {
			return c.LandPlanTask(cmd.Context(), path, project, dir, task)
		})
}

func newPlanTaskSkipCmd() *cobra.Command {
	return newPlanTaskAnswerCmd("skip", "Skip a pending, failed or land_failed task: what waits for it goes on without it",
		"Its worktree, if it holds work, stays as an ordinary worktree on the task's\n"+
			"branch; if not, it goes back to the plan for another task.",
		api.ResultSkipped, nil, "",
		nil,
		func(c *client.Client, cmd *cobra.Command, path, project, dir, task string) (api.PlanTaskResult, error) {
			return c.SkipPlanTask(cmd.Context(), path, project, dir, task)
		})
}

// taskAnswered is a task the person retried, landed or skipped.
type taskAnswered struct {
	api.PlanTaskResult
	plan, verb string
}

func (r taskAnswered) text(w io.Writer, st style.Style) {
	task, plan := st.Name(r.Task.Id), st.Name(r.plan)
	switch r.verb {
	case "retry":
		how := "a new agent starts"
		if r.Task.Agent != nil {
			how = "its agent " + st.Name(r.Task.Agent.Name) + " resumes"
		}
		fmt.Fprintf(w, "task %s of %s %s: %s\n", task, plan, st.Success("retried"), how)
	case "land":
		fmt.Fprintf(w, "task %s of %s %s on %s\n", task, plan, st.Success("landing"), st.Name(planBranch(r.plan)))
	default:
		fmt.Fprintf(w, "task %s of %s %s: what waits for it goes on without it\n", task, plan, st.Success("skipped"))
	}
}

// LogValue measures the task's prompt and summary.
func (r taskAnswered) LogValue() slog.Value { return taskLogged(r.PlanTaskResult) }
