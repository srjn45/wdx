package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage agents: AI coding agents that belong to a project and outlive the daemon",
		Long: "An agent runs an AI coding agent, Claude Code for now, in its own process,\n" +
			"so it keeps running when the daemon restarts or is upgraded. Attach to talk\n" +
			"to it. Closing its project stops it, and opening the project again starts\n" +
			"it in the same session.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newAgentAttachCmd(), newAgentCreateCmd(), newAgentGetCmd(), newAgentListCmd(), newAgentRemoveCmd(), newAgentStartCmd())
	return cmd
}

func newAgentCreateCmd() *cobra.Command {
	var scope projectScope
	var name, parentID, parentName, worktree string
	cmd := &cobra.Command{
		Use:   "create [--project-id <id> | --project-name <name>] [--name <name>] [--parent-id <id> | --parent-name <name>] [--worktree <name>]",
		Short: "Create an agent and start its AI coding agent",
		Long: "Without a project flag, the agent belongs to the project the working\n" +
			"directory is in, and starts there. With one, it starts in the working\n" +
			"directory when that is in the project, else in the project's clone path.\n" +
			"It runs through your login shell, which finds the program.\n\n" +
			"Agents get a generated name such as swift-falcon unless --name is given;\n" +
			"names are unique within a project, among agents and terminals.\n\n" +
			"With a parent flag it is a child of that agent: it belongs to the parent's\n" +
			"project and starts in the parent's dir. Without one it is at the top level,\n" +
			"even when an agent creates it: an agent finds its own id in " + identity.Env("AGENT_ID") + ".\n\n" +
			"With --worktree it starts in that worktree of the project instead: main is\n" +
			"the clone, and any other name matches the last part of a worktree's path. A\n" +
			"name that matches none creates the worktree in <project root>/.worktrees/<name>,\n" +
			"on a new branch of that name from the HEAD of where it would otherwise start.\n" +
			"Removing the agent leaves the worktree.",
		Example: examples(
			"Create an agent in the project you are in", identity.Name+" agent create",
			"Name it and pick the project", identity.Name+" agent create --project-name app --name reviewer",
			"Create a child of an agent", identity.Name+" agent create --parent-name reviewer",
			"Create a child by the parent's id, as JSON", identity.Name+" agent create --parent-id agent-1a2b3c4d --json",
			"Create a child in a worktree of its own", identity.Name+" agent create --parent-name reviewer --worktree fix-login",
			"As JSON", identity.Name+" agent create --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not created",
			outputNote:    "The agent, running.\n" + `With --json: {"result": "created", "agent": {...}}, and "worktree_created": true when its worktree is new`,
			resultList:    "created",
			errorList: codeList(api.ErrorCodeAgentRemoved, api.ErrorCodeBranchExists, api.ErrorCodeInvalidName, api.ErrorCodeNameTaken,
				api.ErrorCodeNotFound, api.ErrorCodePathExists, api.ErrorCodeProjectClosed, api.ErrorCodeStartFailed,
				api.ErrorCodeWorktreeFailed, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := workDir(cmd)
			if err != nil {
				return err
			}
			parent := parentName
			if parentID != "" {
				parent = "id:" + parentID
			}
			r, err := clientOf(cmd).CreateAgent(cmd.Context(), api.CreateAgent{Project: optional(scope.ref()), Dir: dir, Name: optional(name), Parent: optional(parent), Worktree: optional(worktree)})
			if err != nil {
				if parentID == "" {
					err = outsideHint(err, scope, "")
				}
				return agentHint(err)
			}
			return printResult(cmd, agentResult(r))
		},
	}
	scope.addFlags(cmd)
	cmd.Flags().StringVar(&name, "name", "", "`name` of the agent (default: a generated one)")
	cmd.RegisterFlagCompletionFunc("name", cobra.NoFileCompletions)
	cmd.Flags().StringVar(&parentID, "parent-id", "", "`id` of the agent to create it under")
	cmd.Flags().StringVar(&parentName, "parent-name", "", "`name` of the agent to create it under (case-insensitive)")
	cmd.MarkFlagsMutuallyExclusive("parent-id", "parent-name")
	cmd.RegisterFlagCompletionFunc("parent-id", completeParents("id"))
	cmd.RegisterFlagCompletionFunc("parent-name", completeParents("name"))
	cmd.Flags().StringVar(&worktree, "worktree", "", "`name` of the worktree to start in, created when there is none (default: the parent's, else the one you are in, else main)")
	cmd.RegisterFlagCompletionFunc("worktree", cobra.NoFileCompletions)
	return cmd
}

func newAgentListCmd() *cobra.Command {
	var scope projectScope
	var all bool
	cmd := &cobra.Command{
		Use:   "list [--project-id <id> | --project-name <name> | --all]",
		Short: "List agents",
		Long: "Without a flag, the agents of the project the working directory is in.\n" +
			"Every state is listed: an agent stays until it is removed. Children are\n" +
			"indented under their parents, and a removed agent stays as long as it has\n" +
			"children.",
		Example: examples(
			"The agents of the project you are in", identity.Name+" agent list",
			"Every project's, as JSON", identity.Name+" agent list --all --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The agents by project, then name, each with its children; an empty list when there are none.\n" +
				`With --json: {"agents": [...]}, the children of each under "children"`,
			errorList: codeList(api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			var dir string
			if !all && !scope.given() {
				var err error
				if dir, err = workDir(cmd); err != nil {
					return err
				}
			}
			list, err := clientOf(cmd).ListAgents(cmd.Context(), scope.ref(), dir)
			if err != nil {
				return outsideHint(err, scope, ", or --all")
			}
			return printResult(cmd, agentList{list, all})
		},
	}
	scope.addFlags(cmd)
	cmd.Flags().BoolVar(&all, "all", false, "list the agents of every project")
	cmd.MarkFlagsMutuallyExclusive("all", "project-id")
	cmd.MarkFlagsMutuallyExclusive("all", "project-name")
	return cmd
}

// agentRef is an agent named on the command line by --id, or by --name in a
// project.
type agentRef struct {
	id, name string
	scope    projectScope
}

func (r *agentRef) addFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&r.id, "id", "", "`id` of the agent")
	cmd.Flags().StringVar(&r.name, "name", "", "`name` of the agent (case-insensitive)")
	cmd.MarkFlagsOneRequired("id", "name")
	cmd.MarkFlagsMutuallyExclusive("id", "name")
	cmd.RegisterFlagCompletionFunc("id", completeAgents("id"))
	cmd.RegisterFlagCompletionFunc("name", completeAgents("name"))
	r.scope.addFlags(cmd)
}

// resolve returns how the API names the agent: its path, and the project or
// dir that names its project, if any.
func (r agentRef) resolve(cmd *cobra.Command) (path, project, dir string, err error) {
	if r.id != "" {
		return "id:" + r.id, r.scope.ref(), "", nil
	}
	if r.scope.given() {
		return r.name, r.scope.ref(), "", nil
	}
	dir, err = workDir(cmd)
	return r.name, "", dir, err
}

// notFoundHint tells where to look for an agent that was not found.
func (r agentRef) notFoundHint(err error) error {
	if problemCode(err) != api.ErrorCodeNotFound || r.id != "" || r.scope.given() {
		return err
	}
	return &hinted{err, "names are unique within a project: run it in the agent's project, or pass --project-name or --project-id"}
}

func newAgentGetCmd() *cobra.Command {
	var ref agentRef
	cmd := &cobra.Command{
		Use:   "get (--id <id> | --name <name>) [--project-id <id> | --project-name <name>]",
		Short: "Show an agent",
		Long: "In any state, with why it failed when it did. Without a project flag, --name\n" +
			"is looked up in the project the working directory is in. Nothing is changed.",
		Example: examples(
			"Show an agent of the project you are in", identity.Name+" agent get --name reviewer",
			"By id, as JSON", identity.Name+" agent get --id agent-1a2b3c4d --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The agent.\n" + `With --json: {"agent": {...}}`,
			errorList:  codeList(api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			a, err := clientOf(cmd).GetAgent(cmd.Context(), path, project, dir)
			if err != nil {
				return ref.notFoundHint(err)
			}
			return printResult(cmd, agentDetail(a))
		},
	}
	ref.addFlags(cmd)
	return cmd
}

func newAgentStartCmd() *cobra.Command {
	var ref agentRef
	cmd := &cobra.Command{
		Use:   "start (--id <id> | --name <name>) [--project-id <id> | --project-name <name>]",
		Short: "Start an agent that exited, failed or was lost, in the same session",
		Long: "The agent keeps its id, name, dir and session, so its conversation comes\n" +
			"back. Agents stopped by closing their project start again when it is opened.",
		Example: examples(
			"Start an agent of the project you are in", identity.Name+" agent start --name reviewer",
			"By id, as JSON", identity.Name+" agent start --id agent-1a2b3c4d --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not started",
			outputNote:    "The agent, running.\n" + `With --json: {"result": "started", "agent": {...}}`,
			resultList:    "started",
			errorList: codeList(api.ErrorCodeNotFound, api.ErrorCodeProjectClosed, api.ErrorCodeStartFailed,
				api.ErrorCodeAgentRemoved, api.ErrorCodeAgentRunning, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			r, err := clientOf(cmd).StartAgent(cmd.Context(), path, project, dir)
			if err != nil {
				return agentHint(ref.notFoundHint(err))
			}
			return printResult(cmd, agentResult(r))
		},
	}
	ref.addFlags(cmd)
	return cmd
}

const stopAgentQuestion = "stop running agent"

func newAgentRemoveCmd() *cobra.Command {
	var ref agentRef
	var stopRunning, cancel bool
	cmd := &cobra.Command{
		Use:   "remove (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] [--stop-running | --cancel]",
		Short: "Delete an agent",
		Long: "If it is running, you are asked whether to stop it, which interrupts what\n" +
			"it is doing; --stop-running and --cancel answer in advance, and with --json\n" +
			"one of them is required. What the AI coding agent keeps of its session stays\n" +
			"on disk.\n\n" +
			"An agent with children stays in the list as removed, its children untouched,\n" +
			"until the last of them is removed.",
		Example: examples(
			"Remove an agent of the project you are in", identity.Name+" agent remove --name reviewer",
			"From a script: stop it if it is running", identity.Name+" agent remove --id agent-1a2b3c4d --stop-running --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not removed",
			outputNote:    "The removed agent, as it was before.\n" + `With --json: {"result": "removed", "agent": {...}}`,
			resultList:    "removed",
			errorList:     codeList(api.ErrorCodeNotFound, api.ErrorCodeAgentRemoved, api.ErrorCodeAgentRunning, api.ErrorCodeDaemonUnavailable),
			jsonRequires:  annotation("stop-running", "cancel"),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			if stopRunning {
				logAnswer(cmd, stopAgentQuestion, "stop", "--stop-running")
			}
			r, err := clientOf(cmd).DeleteAgent(cmd.Context(), path, project, dir, stopRunning)
			if problemCode(err) == api.ErrorCodeAgentRunning && !stopRunning {
				if err = askStopRunning(cmd, err, stopAgentQuestion, cancel, "stop it and remove it?", "it"); err != nil {
					return err
				}
				r, err = clientOf(cmd).DeleteAgent(cmd.Context(), path, project, dir, true)
			}
			if err != nil {
				return agentHint(ref.notFoundHint(err))
			}
			return printResult(cmd, agentResult(r))
		},
	}
	ref.addFlags(cmd)
	addStopRunningFlags(cmd, &stopRunning, &cancel, "it")
	return cmd
}

func newAgentAttachCmd() *cobra.Command {
	var ref agentRef
	var prefix string
	cmd := &cobra.Command{
		Use:   "attach (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] [--prefix-key <key>]",
		Short: "Show an agent full-window and talk to it, as tmux does",
		Long: "The agent takes the whole window, above a status line, and what you type\n" +
			"goes to it. C-\\ is the prefix for attach's own keys: C-\\ d detaches, leaving\n" +
			"the agent running, and gives the window back as it was; C-\\ ? lists them\n" +
			"all. It runs in the terminal it was started from, not on stdin and stdout,\n" +
			"which print the result when it ends.",
		Example: examples(
			"Attach to an agent of the project you are in", identity.Name+" agent attach --name reviewer",
			"With ctrl-] as the prefix", identity.Name+" agent attach --id agent-1a2b3c4d --prefix-key ctrl-]",
			"As JSON once it ends", identity.Name+" agent attach --name reviewer --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not attached",
			outputNote: "The agent, once you detach or it exits.\n" +
				`With --json: {"result": "detached" or "exited", "agent": {...}}`,
			resultList: annotation("detached", "exited"),
			errorList: codeList(api.ErrorCodeNotFound, api.ErrorCodeAgentNotRunning, api.ErrorCodeAttachUnsupported,
				api.ErrorCodeConnectionLost, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			key, err := parsePrefixKey(prefix)
			if err != nil {
				return &usageError{err}
			}
			return attach(cmd, func(ctx context.Context) (attachTarget, error) {
				path, project, dir, err := ref.resolve(cmd)
				if err != nil {
					return attachTarget{}, err
				}
				got, err := clientOf(cmd).GetAgent(ctx, path, project, dir)
				if err != nil {
					return attachTarget{}, ref.notFoundHint(err)
				}
				return agentTarget(cmd, got.Agent), nil
			}, key)
		},
	}
	ref.addFlags(cmd)
	addPrefixKeyFlag(cmd, &prefix)
	return cmd
}

func agentTarget(cmd *cobra.Command, a api.Agent) attachTarget {
	c := clientOf(cmd)
	ref := "id:" + a.Id
	return attachTarget{
		noun: "agent", holderID: a.TerminalId, name: a.Name, project: a.ProjectName,
		state: string(a.State), running: a.State == api.AgentStateRunning, exitCode: a.ExitCode, value: a,
		reread: func(ctx context.Context) (attachTarget, error) {
			got, err := c.GetAgent(ctx, ref, "", "")
			if err != nil {
				return attachTarget{}, err
			}
			return agentTarget(cmd, got.Agent), nil
		},
		exited: func(code int) attachTarget {
			a.ExitCode = &code
			return agentTarget(cmd, a)
		},
		history: func(ctx context.Context, from int64, count int) (api.TerminalHistoryEnvelope, error) {
			return c.GetAgentHistory(ctx, ref, "", "", &from, count)
		},
	}
}

// agentHint tells what to do about the errors creating or starting an agent
// can return.
func agentHint(err error) error {
	switch problemCode(err) {
	case api.ErrorCodeNameTaken, api.ErrorCodeInvalidName:
		return &hinted{err, "choose another --name or --worktree"}
	case api.ErrorCodeBranchExists:
		return &hinted{err, "a new worktree gets a new branch: choose another --worktree, or delete the branch"}
	case api.ErrorCodePathExists:
		return &hinted{err, "something else is in the worktree's place: choose another --worktree"}
	case api.ErrorCodeProjectClosed:
		return &hinted{err, "open the project first with " + identity.Name + " project open"}
	case api.ErrorCodeAgentRemoved:
		return &hinted{err, "it goes when its last child is removed; see them with " + identity.Name + " agent get"}
	case api.ErrorCodeAgentRunning:
		return &hinted{err, "it is already running; attach to it with " + identity.Name + " agent attach"}
	case api.ErrorCodeStartFailed:
		return &hinted{err, "check its dir, then run " + identity.Name + " agent start"}
	}
	return err
}

// agentResult is an agent a command changed.
type agentResult api.AgentResult

func (r agentResult) text(w io.Writer, st style.Style) {
	a := r.Agent
	in := "in"
	if r.Result == api.ResultRemoved {
		in = "from"
	}
	fmt.Fprintf(w, "%s agent %s %s %s\n", st.Success(string(r.Result)), st.Name(a.Name), in, st.Name(a.ProjectName))
	onWorktree := a.Worktree != nil && *a.Worktree != "main"
	width := 4
	switch {
	case onWorktree:
		width = 8
	case a.ParentAgentId != nil:
		width = 6
	}
	field := fieldPrinter(w, st, width)
	if a.ParentAgentId != nil {
		field("parent", st.Muted(*a.ParentAgentId))
	}
	if a.RemovedAt != nil {
		field("kept", "as removed until its children are gone")
	}
	if onWorktree {
		created := ""
		if r.WorktreeCreated != nil {
			created = " " + st.Muted("(new)")
		}
		field("worktree", st.Name(*a.Worktree)+created)
	}
	field("dir", a.Dir)
	field("aca", a.Aca)
	field("id", a.Id)
}

// agentList is a list of agents; all says they may belong to several
// projects.
type agentList struct {
	api.AgentList
	all bool
}

func (l agentList) text(w io.Writer, st style.Style) {
	if len(l.Agents) == 0 {
		fmt.Fprintln(w, "no agents")
		return
	}
	var table style.Table
	heading := []string{st.Heading("NAME"), st.Heading("STATE"), st.Heading("ACA"), st.Heading("DIR"), st.Heading("ID")}
	if l.all {
		heading = append([]string{st.Heading("PROJECT")}, heading...)
	}
	table.Row(heading...)
	for _, r := range agentTreeRows(l.Agents) {
		a := r.agent
		row := []string{st.Muted(r.guide) + st.Name(a.Name), agentStateText(st, a), a.Aca, a.Dir, st.Muted(a.Id)}
		if l.all {
			row = append([]string{st.Name(a.ProjectName)}, row...)
		}
		table.Row(row...)
	}
	table.Write(w)
}

// agentTreeRow is a line of a tree of agents: the guide drawn before its name.
type agentTreeRow struct {
	agent api.Agent
	guide string
}

// agentTreeRows returns the agents at the top level in list and their
// children, each before its children.
func agentTreeRows(list []api.Agent) []agentTreeRow {
	return agentChildRows(list, "", false)
}

// agentChildRows returns the rows of list, children of the same parent when
// nested, whose guides start with lead.
func agentChildRows(list []api.Agent, lead string, nested bool) []agentTreeRow {
	var rows []agentTreeRow
	for i, a := range list {
		guide, next := "", ""
		if nested {
			guide, next = lead+"├ ", lead+"│ "
			if i == len(list)-1 {
				guide, next = lead+"└ ", lead+"  "
			}
		}
		rows = append(rows, agentTreeRow{a, guide})
		if a.Children != nil {
			rows = append(rows, agentChildRows(*a.Children, next, true)...)
		}
	}
	return rows
}

// agentDetail is one agent, in any state.
type agentDetail api.AgentEnvelope

func (d agentDetail) text(w io.Writer, st style.Style) {
	a := d.Agent
	field := fieldPrinter(w, st, 8)
	fmt.Fprintf(w, "agent %s in %s\n", st.Name(a.Name), st.Name(a.ProjectName))
	field("state", agentStateText(st, a))
	if a.Error != nil {
		field("error", st.Failure(*a.Error))
	}
	if a.ParentAgentId != nil {
		field("parent", st.Muted(*a.ParentAgentId))
	}
	if a.Children != nil && len(*a.Children) > 0 {
		names := make([]string, 0, len(*a.Children))
		for _, c := range *a.Children {
			names = append(names, st.Name(c.Name))
		}
		field("children", strings.Join(names, ", "))
	}
	if a.Worktree != nil {
		field("worktree", st.Name(*a.Worktree))
	}
	field("dir", a.Dir)
	field("aca", a.Aca)
	field("session", a.SessionId)
	if a.Pid != nil {
		field("pid", strconv.Itoa(*a.Pid))
	}
	field("id", a.Id)
	field("created", a.CreatedAt.Local().Format(time.DateTime))
	if a.StartedAt != nil {
		field("started", a.StartedAt.Local().Format(time.DateTime))
	}
	if a.EndedAt != nil {
		field("ended", a.EndedAt.Local().Format(time.DateTime))
	}
	if a.RemovedAt != nil {
		field("removed", a.RemovedAt.Local().Format(time.DateTime))
	}
}

func agentStateText(st style.Style, a api.Agent) string {
	switch a.State {
	case api.AgentStateRunning, api.AgentStateStarting:
		return st.Success(string(a.State))
	case api.AgentStateExited:
		if a.ExitCode != nil {
			return fmt.Sprintf("exited (%d)", *a.ExitCode)
		}
	case api.AgentStateFailed, api.AgentStateLost:
		return st.Failure(string(a.State))
	}
	return st.Muted(string(a.State))
}
