package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

// planBranch is where a plan's tasks land.
func planBranch(name string) string { return "plan/" + name }

func newPlanRunCmd() *cobra.Command {
	var ref planRef
	var parallel int
	cmd := &cobra.Command{
		Use:   "run (--id <id> | --name <name>) [--parallel <n>] [--project-id <id> | --project-name <name>]",
		Short: "Run a draft plan: an agent for each task, landing on the plan's branch",
		Long: "Makes the branch plan/<name> at the tip of the project's default branch, then\n" +
			"starts an agent for every task whose afters are done, each in a worktree of\n" +
			"the plan's, up to --parallel at once (the setting plan.parallel when left\n" +
			"out). A task's agent says when it is done, with plan task done; its work is\n" +
			"then rebased onto the plan's branch, and what waited for it starts. Once\n" +
			"every task is done, one worktree is left on the plan's branch, to land as\n" +
			"any worktree. A plan runs once, and cannot be changed after.",
		Example: examples(
			"A plan of the project you are in", identity.Name+" plan run --name sso",
			"Two tasks at most at once, as JSON", identity.Name+" plan run --name sso --parallel 2 --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not started",
			outputNote:    "The plan, running, and its branch.\n" + `With --json: {"result": "started", "plan": {...}}`,
			resultList:    string(api.ResultStarted),
			errorList: codeList(api.ErrorCodeBranchExists, api.ErrorCodeInvalidArgument, api.ErrorCodeNotFound, api.ErrorCodePlanInUse,
				api.ErrorCodePlanStarted, api.ErrorCodeProjectClosed, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("parallel") && (parallel < 1 || parallel > 20) {
				return &usageError{fmt.Errorf("--parallel %d is not from 1 to 20", parallel)}
			}
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			r, err := clientOf(cmd).RunPlan(cmd.Context(), path, project, dir, parallel)
			switch problemCode(err) {
			case api.ErrorCodePlanStarted:
				return &hinted{err, "a plan runs once: copy it to run it again"}
			case api.ErrorCodePlanInUse:
				return &hinted{err, "a plan a plan of plans lists runs with it, in a later version"}
			case api.ErrorCodeBranchExists:
				return &hinted{err, "remove or rename the branch, or rename the plan"}
			case api.ErrorCodeProjectClosed:
				return &hinted{err, "open it first with " + identity.Name + " project open"}
			}
			if err != nil {
				return ref.notFoundHint(err)
			}
			return printResult(cmd, planStarted{PlanResult: r, parallel: parallel})
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().IntVar(&parallel, "parallel", 0, "the most tasks it runs at once, a `number` from 1 to 20 (default: the setting plan.parallel)")
	completeFlag(cmd, "parallel", cobra.NoFileCompletions)
	return cmd
}

// planStarted is a plan a command ran.
type planStarted struct {
	api.PlanResult
	parallel int
}

func (r planStarted) text(w io.Writer, st style.Style) {
	p := r.Plan
	fmt.Fprintf(w, "plan %s %s on %s", st.Name(p.Name), st.Success("running"), st.Name(planBranch(p.Name)))
	if r.parallel != 0 {
		fmt.Fprintf(w, ", up to %s at once", plural(r.parallel, "task"))
	}
	fmt.Fprintln(w)
	field := fieldPrinter(w, st, 8)
	field("project", p.ProjectName)
	field("tasks", planSize(p))
	field("id", st.Muted(p.Id))
}

func newPlanTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "A task of a running plan: say it is done, read it, or answer one that cannot go on",
		Args:  noArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newPlanTaskDoneCmd(), newPlanTaskGetCmd(), newPlanTaskLandCmd(), newPlanTaskRetryCmd(), newPlanTaskSkipCmd())
	return cmd
}

// taskRef is a plan's task named on the command line by --plan and --task.
type taskRef struct {
	plan, task string
	scope      projectScope
}

func (r *taskRef) addFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&r.plan, "plan", "", "`name` of the plan (case-insensitive)")
	cmd.Flags().StringVar(&r.task, "task", "", "`id` of the task in the plan")
	completeFlag(cmd, "plan", completePlans("name"))
	completeFlag(cmd, "task", cobra.NoFileCompletions)
	r.scope.addFlags(cmd)
}

// resolve returns how the API names the plan, and the project or dir that names
// its project.
func (r taskRef) resolve(cmd *cobra.Command) (path, project, dir string, err error) {
	return planRef{name: r.plan, scope: r.scope}.resolve(cmd)
}

func newPlanTaskDoneCmd() *cobra.Command {
	var ref taskRef
	var agentID, summary string
	cmd := &cobra.Command{
		Use:   "done (--agent-id <id> | --plan <name> --task <id>) --summary <text> [--project-id <id> | --project-name <name>]",
		Short: "Say a task of a running plan is done, so that it lands on the plan's branch",
		Long: "A task's agent runs it once its work is committed on its branch, naming\n" +
			"itself with --agent-id $" + identity.Env("AGENT_ID") + "; a person names the plan and the task.\n" +
			"Refused while the task's worktree has changes or files not committed, or is\n" +
			"not on the task's branch. The summary is told to the tasks that wait for it.\n" +
			"The task's agent is then stopped, and the task lands.",
		Example: examples(
			"From the task's agent", identity.Name+" plan task done --agent-id agent-1a2b3c4d --summary Added_the_table",
			"From a person, as JSON", identity.Name+" plan task done --plan sso --task schema --summary Added_the_table --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not done",
			outputNote:    "The task, landing.\n" + `With --json: {"result": "landing", "task": {...}}`,
			resultList:    string(api.ResultLanding),
			errorList: codeList(api.ErrorCodeAgentWorking, api.ErrorCodeNotCommitted, api.ErrorCodeNotFound, api.ErrorCodePlanNotRunning, api.ErrorCodeTaskState,
				api.ErrorCodeWrongBranch, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(summary) == "" {
				return &usageError{errors.New("--summary is blank: the tasks after this one are told it")}
			}
			path, project, dir, task, err := ref.resolveDone(cmd, agentID)
			if err != nil {
				return err
			}
			r, err := clientOf(cmd).FinishPlanTask(cmd.Context(), path, project, dir, task, summary)
			switch problemCode(err) {
			case api.ErrorCodeNotCommitted:
				return &hinted{err, "commit your work on the task's branch first"}
			case api.ErrorCodeWrongBranch:
				return &hinted{err, "switch the worktree back to the task's branch, with its commits"}
			}
			if err != nil {
				return err
			}
			return printResult(cmd, taskDone{PlanTaskResult: r, plan: path})
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().StringVar(&agentID, "agent-id", "", "`id` of the task's agent, which an agent finds in $"+identity.Env("AGENT_ID"))
	cmd.Flags().StringVar(&summary, "summary", "", "what was done, in a few lines: `text` the tasks waiting for it are told")
	cmd.MarkFlagsOneRequired("agent-id", "plan")
	cmd.MarkFlagsMutuallyExclusive("agent-id", "task")
	cmd.MarkFlagsMutuallyExclusive("agent-id", "project-id")
	cmd.MarkFlagsMutuallyExclusive("agent-id", "project-name")
	cmd.MarkFlagsRequiredTogether("plan", "task")
	_ = cmd.MarkFlagRequired("summary")
	completeFlag(cmd, "agent-id", completeAgents("id"))
	completeFlag(cmd, "summary", cobra.NoFileCompletions)
	return cmd
}

// resolveDone returns the plan, its project and the task a task done names: by the
// task's agent, which the daemon is asked for, or by --plan and --task.
func (r taskRef) resolveDone(cmd *cobra.Command, agentID string) (path, project, dir, task string, err error) {
	if agentID == "" {
		path, project, dir, err = r.resolve(cmd)
		return path, project, dir, r.task, err
	}
	a, err := clientOf(cmd).GetAgent(cmd.Context(), "id:"+agentID, "", "")
	if err != nil {
		return "", "", "", "", err
	}
	if a.Agent.Task == nil {
		return "", "", "", "", &client.ProblemError{Problem: api.NewProblem(api.ErrorCodeNotFound,
			fmt.Sprintf("agent %s does no plan's task", a.Agent.Name))}
	}
	return a.Agent.Task.Plan, "id:" + a.Agent.ProjectId, "", a.Agent.Task.Task, nil
}

// taskDone is a task a command said is done.
type taskDone struct {
	api.PlanTaskResult
	plan string
}

func (r taskDone) text(w io.Writer, st style.Style) {
	fmt.Fprintf(w, "task %s of %s %s: %s on %s\n", st.Name(r.Task.Id), st.Name(r.plan), st.Success("done"), "landing", st.Name(planBranch(r.plan)))
}

// LogValue measures the task's prompt and summary.
func (r taskDone) LogValue() slog.Value { return taskLogged(r.PlanTaskResult) }

func newPlanTaskGetCmd() *cobra.Command {
	var ref taskRef
	cmd := &cobra.Command{
		Use:   "get --plan <name> --task <id> [--project-id <id> | --project-name <name>]",
		Short: "Show a plan's task: its state, agent and worktree, prompt and summary",
		Example: examples(
			"A task of a plan of the project you are in", identity.Name+" plan task get --plan sso --task schema",
			"Of another project, as JSON", identity.Name+" plan task get --project-name app --plan sso --task schema --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The task's state, agent, worktree and afters, then its prompt and, once done, its summary.\n" +
				`With --json: {"plan": {...}, "task": {...}}`,
			errorList: codeList(api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			c, err := clientOf(cmd).GetPlan(cmd.Context(), path, project, dir, 0)
			if err != nil {
				return planRef{name: ref.plan, scope: ref.scope}.notFoundHint(err)
			}
			var tasks []api.PlanTask
			if c.Definition.Tasks != nil {
				tasks = *c.Definition.Tasks
			}
			i := slices.IndexFunc(tasks, func(t api.PlanTask) bool { return t.Id == ref.task })
			if i < 0 {
				return &client.ProblemError{Problem: api.NewProblem(api.ErrorCodeNotFound, fmt.Sprintf("plan %s has no task %s", c.Plan.Name, ref.task))}
			}
			return printResult(cmd, planTask{Plan: c.Plan, Task: tasks[i]})
		},
	}
	ref.addFlags(cmd)
	_ = cmd.MarkFlagRequired("plan")
	_ = cmd.MarkFlagRequired("task")
	return cmd
}

// planTask is a task of a plan.
type planTask struct {
	Plan api.Plan     `json:"plan"`
	Task api.PlanTask `json:"task"`
}

func (r planTask) text(w io.Writer, st style.Style) {
	t := r.Task
	fmt.Fprintf(w, "%s of %s: %s\n", st.Name(t.Id), st.Name(r.Plan.Name), taskState(st, t))
	field := fieldPrinter(w, st, 8)
	if len(t.After) > 0 {
		field("after", strings.Join(t.After, ", "))
	}
	if t.Agent != nil {
		field("agent", st.Name(t.Agent.Name))
	}
	if t.SlotPath != nil {
		field("worktree", *t.SlotPath)
	}
	if t.Conflicts != nil {
		field("conflicts", strings.Join(*t.Conflicts, ", "))
	}
	if t.CheckTerminal != nil {
		field("check", "its output in the terminal "+st.Name(*t.CheckTerminal))
	}
	fmt.Fprintf(w, "\n%s\n%s\n", st.Heading("PROMPT"), strings.TrimRight(t.Prompt, "\n"))
	if t.Summary != nil {
		fmt.Fprintf(w, "\n%s\n%s\n", st.Heading("SUMMARY"), strings.TrimRight(*t.Summary, "\n"))
	}
}

// LogValue measures the task's prompt and summary.
func (r planTask) LogValue() slog.Value { return taskLogged(r) }

// taskLogged is v, a response holding a plan's task, as logged.
func taskLogged(v any) slog.Value {
	b, err := json.Marshal(v)
	if err != nil {
		return slog.StringValue("unloggable task")
	}
	return slog.AnyValue(api.PlanTaskResponseLogged(b))
}

// taskState is how a task's state reads: a pending task waits for its afters, or
// is ready; a failed one says why.
func taskState(st style.Style, t api.PlanTask) string {
	switch t.State {
	case api.PlanTaskStatePending:
		if len(t.WaitingFor) > 0 {
			return st.Muted("waiting for " + strings.Join(t.WaitingFor, ", "))
		}
		return "ready"
	case api.PlanTaskStateDone:
		return st.Success("done")
	case api.PlanTaskStateFailed, api.PlanTaskStateLandFailed:
		s := "failed"
		if t.State == api.PlanTaskStateLandFailed {
			s = "land failed"
		}
		if t.Failure != nil {
			s += ": " + strings.ReplaceAll(string(*t.Failure), "_", " ")
		}
		if t.CheckTerminal != nil {
			s += " (terminal " + *t.CheckTerminal + ")"
		}
		return st.Failure(s)
	}
	return string(t.State)
}

// planProgress is how far a plan's run is: 2/4 once it runs, its size before.
func planProgress(p api.Plan) string {
	if p.TasksDone != nil && p.Tasks != nil {
		return strconv.FormatInt(*p.TasksDone, 10) + "/" + strconv.FormatInt(*p.Tasks, 10)
	}
	return planSize(p)
}

// planFailed is how many of a plan's tasks wait for the person, as ", 1 failed",
// styled; "" for none.
func planFailed(st style.Style, p api.Plan) string {
	if p.TasksFailed == nil || *p.TasksFailed == 0 {
		return ""
	}
	return ", " + st.Failure(strconv.FormatInt(*p.TasksFailed, 10)+" failed")
}

// runStateText is a running plan's state as its run says it, with why it is
// paused when its project is closed.
func runStateText(st style.Style, p api.Plan, r *api.PlanRun) string {
	s := planStateText(st, p.State)
	if r != nil && r.PausedReason != nil && *r.PausedReason == api.PlanRunPausedReasonProjectClosed {
		s += " (project closed)"
	}
	return s
}

// planStateText is a plan's state, styled: a draft muted.
func planStateText(st style.Style, s api.PlanState) string {
	switch s {
	case api.PlanStateDraft:
		return st.Muted(string(s))
	case api.PlanStateDone:
		return st.Success(string(s))
	}
	return string(s)
}
