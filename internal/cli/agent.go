package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage agents: AI coding agents that belong to a project and outlive the daemon",
		Long: "An agent runs an AI coding agent, such as Claude Code, in its own process,\n" +
			"so it keeps running when the daemon restarts or is upgraded. Attach to talk\n" +
			"to it. Closing its project stops it, and opening the project again starts\n" +
			"it in the same session. " + identity.Name + " agent acas lists the AI coding agents\n" +
			"new agents can run.",
		Args: clikit.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newAgentAcasCmd(), newAgentAttachCmd(), newAgentCreateCmd(), newAgentGetCmd(), newAgentListCmd(), newAgentModelsCmd(), newAgentRemoveCmd(), newAgentRenameCmd(), newAgentRestartCmd(), newAgentSendCmd(), newAgentStartCmd(), newAgentTranscriptCmd())
	return cmd
}

func newAgentCreateCmd() *cobra.Command {
	var scope projectScope
	var name, parentID, parentName, worktree, prompt, acaID, model, permissions string
	cmd := &cobra.Command{
		Use:   "create [--prompt <text>] [--project-id <id> | --project-name <name>] [--name <name>] [--parent-id <id> | --parent-name <name>] [--worktree <name>] [--aca <id>] [--model <model>] [--permissions <mode>]",
		Short: "Create an agent and start its AI coding agent",
		Long: "Without a project flag, the agent belongs to the project the working\n" +
			"directory is in, and starts there. With one, it starts in the working\n" +
			"directory when that is in the project, else in the project's clone path.\n" +
			"It runs through your login shell, which finds the program.\n\n" +
			"Agents get a generated name such as swift-falcon unless --name is given;\n" +
			"names have letters, numbers and - only, and are unique within a project,\n" +
			"among agents and terminals.\n\n" +
			"With a parent flag it is a child of that agent: it belongs to the parent's\n" +
			"project and starts in the parent's dir. Without one it is at the top level,\n" +
			"even when an agent creates it: an agent finds its own id in " + identity.Env("AGENT_ID") + ".\n" +
			"With --node, --parent-id needs no project flag; --parent-name does, as a\n" +
			"name is unique only within its project.\n\n" +
			"With --worktree it starts in that worktree of the project instead: main is\n" +
			"the clone, and any other name matches the last part of a worktree's path. A\n" +
			"name that matches none creates the worktree in <project root>/.worktrees/<name>,\n" +
			"on a new branch of that name from the HEAD of where it would otherwise start.\n" +
			"Removing the agent leaves the worktree.\n\n" +
			"With --prompt it starts on that task: the first message in its inbox, as\n" +
			identity.Name + " agent send gives it, typed in once it is ready for it. A prompt that\n" +
			"cannot be sent leaves the agent running, and the result says why.\n\n" +
			"With --aca it runs that AI coding agent, which must be installed and enabled;\n" +
			"by default the first that is, as " + identity.Name + " agent acas lists them.\n\n" +
			"With --model its AI coding agent uses that model, named as its own model flag\n" +
			"takes it: " + identity.Name + " agent models lists them, and any other is passed as given.\n" +
			"--permissions full, the default, lets it do anything without asking, with\n" +
			"network access and no sandbox; ask leaves that to your settings for it. Both\n" +
			"are kept for every later start: restart and resume use them too.",
		Example: clikit.Examples(
			"Create an agent in the project you are in", identity.Name+" agent create",
			"Start one on a task", identity.Name+" agent create --name reviewer --prompt summarize",
			"Start a child on a task, in a worktree of its own", identity.Name+" agent create --parent-name reviewer --worktree fix-login --prompt fix",
			"Name it and pick the project", identity.Name+" agent create --project-name app --name reviewer",
			"Create a child of an agent", identity.Name+" agent create --parent-name reviewer",
			"Create a child by the parent's id, as JSON", identity.Name+" agent create --parent-id agent-1a2b3c4d --json",
			"Create a child in a worktree of its own", identity.Name+" agent create --parent-name reviewer --worktree fix-login",
			"Pick its AI coding agent", identity.Name+" agent create --aca claude_code",
			"Pick its model", identity.Name+" agent create --model sonnet",
			"Have it ask before acting, as your settings say", identity.Name+" agent create --permissions ask",
			"As JSON", identity.Name+" agent create --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not created",
			clikit.OutputNote:    "The agent, running.\n" + `With --json: {"result": "created", "agent": {...}}, and "worktree_created": true when its worktree is new`,
			clikit.Results:       "created",
			clikit.Errors: clikit.CodeList(api.ErrorCodeAcaUnavailable, api.ErrorCodeAgentRemoved, api.ErrorCodeBranchExists, api.ErrorCodeInvalidArgument, api.ErrorCodeInvalidName, api.ErrorCodeNameTaken,
				api.ErrorCodeNotFound, api.ErrorCodePathExists, api.ErrorCodeProjectClosed, api.ErrorCodeStartFailed,
				api.ErrorCodeWorktreeFailed, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed(clikit.PromptFlag) && strings.TrimSpace(prompt) == "" {
				return &clikit.UsageError{Err: errors.New("--prompt is empty")}
			}
			if cmd.Flags().Changed("aca") && acaID == "" {
				return &clikit.UsageError{Err: errors.New("--aca is empty")}
			}
			if cmd.Flags().Changed("model") && model == "" {
				return &clikit.UsageError{Err: errors.New("--model is empty")}
			}
			var mode *api.AgentPermissions
			if cmd.Flags().Changed("permissions") {
				if !api.AgentPermissions(permissions).Valid() {
					return &clikit.UsageError{Err: fmt.Errorf("--permissions is %q, not one of %s", permissions, strings.Join(agentPermissions, ", "))}
				}
				mode = ptr(api.AgentPermissions(permissions))
			}
			dir, err := createDir(cmd, parentID, parentName)
			if err != nil {
				return err
			}
			parent := parentName
			if parentID != "" {
				parent = "id:" + parentID
			}
			r, err := clientOf(cmd).CreateAgent(cmd.Context(), api.CreateAgent{Project: optional(scope.ref()), Dir: dir, Name: optional(name), Parent: optional(parent), Worktree: optional(worktree), Prompt: optional(prompt),
				Aca: optional(acaID), Model: optional(model), Permissions: mode})
			if err != nil {
				if parentID == "" {
					err = outsideHint(err, scope, "")
				}
				return agentHint(err)
			}
			return clikit.PrintResult(cmd, agentResult(r))
		},
	}
	scope.addFlags(cmd)
	cmd.Flags().StringVar(&name, "name", "", "`name` of the agent (default: a generated one)")
	clikit.CompleteFlag(cmd, "name", cobra.NoFileCompletions)
	cmd.Flags().StringVar(&parentID, "parent-id", "", "`id` of the agent to create it under")
	cmd.Flags().StringVar(&parentName, "parent-name", "", "`name` of the agent to create it under (case-insensitive)")
	cmd.MarkFlagsMutuallyExclusive("parent-id", "parent-name")
	clikit.CompleteFlag(cmd, "parent-id", completeParents("id"))
	clikit.CompleteFlag(cmd, "parent-name", completeParents("name"))
	cmd.Flags().StringVar(&worktree, "worktree", "", "`name` of the worktree to start in, created when there is none (default: the parent's, else the one you are in, else main)")
	clikit.CompleteFlag(cmd, "worktree", cobra.NoFileCompletions)
	cmd.Flags().StringVar(&prompt, clikit.PromptFlag, "", "`text` of its first task; it is submitted once it is ready")
	clikit.CompleteFlag(cmd, clikit.PromptFlag, cobra.NoFileCompletions)
	cmd.Flags().StringVar(&acaID, "aca", "", "`id` of the AI coding agent it runs (default: the first installed and enabled)")
	clikit.CompleteFlag(cmd, "aca", completeACAs)
	cmd.Flags().StringVar(&model, "model", "", "`model` its AI coding agent uses, on every start (default: its own default)")
	clikit.CompleteFlag(cmd, "model", completeModels)
	cmd.Flags().StringVar(&permissions, "permissions", "", "`mode`: full lets it do anything without asking, ask leaves that to your settings for it (default: full)")
	clikit.CompleteFlag(cmd, "permissions", cobra.FixedCompletions(agentPermissions, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

// agentPermissions are the modes --permissions takes.
var agentPermissions = []string{string(api.AgentPermissionsFull), string(api.AgentPermissionsAsk)}

func newAgentModelsCmd() *cobra.Command {
	var acaID string
	cmd := &cobra.Command{
		Use:   "models [--aca <id>]",
		Short: "List the models agent create --model takes",
		Long: "The models of an AI coding agent new agents can run, as it says: --aca's,\n" +
			"or by default the first installed and enabled. One marked default is what it\n" +
			"runs with no --model; with none marked, it runs what your own settings for\n" +
			"it say.",
		Example: clikit.Examples(
			"List them", identity.Name+" agent models",
			"List another AI coding agent's", identity.Name+" agent models --aca claude_code",
			"As JSON", identity.Name+" agent models --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote: "The AI coding agent's models, in its order.\n" +
				`With --json: {"aca": "claude_code", "models": [{"id": ..., "name": ..., "default": ...}, ...]}`,
			clikit.Errors: clikit.CodeList(api.ErrorCodeNotFound, api.ErrorCodeAcaUnavailable, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("aca") && acaID == "" {
				return &clikit.UsageError{Err: errors.New("--aca is empty")}
			}
			id, err := chosenACA(cmd.Context(), clientOf(cmd), acaID)
			if err != nil {
				return agentHint(err)
			}
			list, err := clientOf(cmd).ListAcaModels(cmd.Context(), id)
			if err != nil {
				return agentHint(err)
			}
			return clikit.PrintResult(cmd, acaModels(list))
		},
	}
	cmd.Flags().StringVar(&acaID, "aca", "", "`id` of the AI coding agent (default: the first installed and enabled)")
	clikit.CompleteFlag(cmd, "aca", completeACAs)
	return cmd
}

// chosenACA is id, or when "" the daemon's default; with none installed and enabled,
// the aca_unavailable the daemon gives a create.
func chosenACA(ctx context.Context, c *client.Client, id string) (string, error) {
	if id != "" {
		return id, nil
	}
	list, err := c.ListAcas(ctx)
	if err != nil {
		return "", err
	}
	if len(list.Acas) == 0 {
		p := api.NewProblem(api.ErrorCodeAcaUnavailable, "no AI coding agent is installed and enabled")
		p.Details = &map[string]any{"aca": nil, "reason": nil, "program": nil}
		return "", &client.ProblemError{Problem: p}
	}
	return list.Acas[0].Id, nil
}

func newAgentAcasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "acas",
		Short: "List the AI coding agents new agents can run",
		Long: "Those installed, as the login shell finds each one's program, and enabled by\n" +
			"its aca.<id>.enabled setting, in the order they are offered; the default is what\n" +
			"agent create runs with no --aca. " + identity.Name + " deps list says which are installed,\n" +
			"and " + identity.Name + " setting list which are enabled.",
		Example: clikit.Examples(
			"List them", identity.Name+" agent acas",
			"As JSON", identity.Name+" agent acas --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote: "The AI coding agents, the default first.\n" +
				`With --json: {"acas": [{"id": "claude_code", "default": true}, ...]}`,
			clikit.Errors: clikit.CodeList(api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			list, err := clientOf(cmd).ListAcas(cmd.Context())
			if err != nil {
				return err
			}
			return clikit.PrintResult(cmd, acaList(list))
		},
	}
	return cmd
}

// acaList is the AI coding agents new agents can run.
type acaList api.AcaList

func (l acaList) Text(w io.Writer, st style.Style) {
	if len(l.Acas) == 0 {
		fmt.Fprintln(w, "no AI coding agent installed and enabled")
		fmt.Fprintln(w, st.Muted(identity.Name+" deps list says which are installed, "+identity.Name+" setting list which are enabled"))
		return
	}
	fmt.Fprintln(w, "AI coding agents")
	var table style.Table
	table.Row(st.Heading("ID"))
	for _, a := range l.Acas {
		id := st.Name(a.Id)
		if a.Default {
			id += " " + st.Muted("(default)")
		}
		table.Row(id)
	}
	table.Write(w)
}

// acaModels is the models of an AI coding agent.
type acaModels api.AcaModelList

func (l acaModels) Text(w io.Writer, st style.Style) {
	fmt.Fprintf(w, "models of %s\n", st.Name(l.Aca))
	var table style.Table
	table.Row(st.Heading("ID"), st.Heading("NAME"))
	marked := false
	for _, m := range l.Models {
		name := m.Name
		if m.Default {
			name += " " + st.Muted("(default)")
			marked = true
		}
		table.Row(st.Name(m.Id), name)
	}
	table.Write(w)
	if !marked {
		fmt.Fprintln(w, st.Muted("with no --model, your own setting for it"))
	}
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
		Example: clikit.Examples(
			"The agents of the project you are in", identity.Name+" agent list",
			"Every project's, as JSON", identity.Name+" agent list --all --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote: "The agents by project, then name, each with its children; an empty list when there are none.\n" +
				`With --json: {"agents": [...]}, the children of each under "children"`,
			clikit.Errors: clikit.CodeList(api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
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
			return clikit.PrintResult(cmd, agentList{list, all, contextThresholdsOf(cmd)})
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
	clikit.CompleteFlag(cmd, "id", completeAgents("id"))
	clikit.CompleteFlag(cmd, "name", completeAgents("name"))
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
	return &clikit.Hinted{Err: err, Hint: "names are unique within a project: run it in the agent's project, or pass --project-name or --project-id"}
}

func newAgentGetCmd() *cobra.Command {
	var ref agentRef
	cmd := &cobra.Command{
		Use:   "get (--id <id> | --name <name>) [--project-id <id> | --project-name <name>]",
		Short: "Show an agent",
		Long: "In any state, with why it failed when it did. Without a project flag, --name\n" +
			"is looked up in the project the working directory is in. Nothing is changed.",
		Example: clikit.Examples(
			"Show an agent of the project you are in", identity.Name+" agent get --name reviewer",
			"By id, as JSON", identity.Name+" agent get --id agent-1a2b3c4d --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote: "The agent.\n" + `With --json: {"agent": {...}}`,
			clikit.Errors:     clikit.CodeList(api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
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
			return clikit.PrintResult(cmd, agentDetail{a, contextThresholdsOf(cmd), time.Now()})
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
			"back, in the directory it last worked in if that is still there, else its own.\n" +
			"Agents stopped by closing their project start again when it is opened.",
		Example: clikit.Examples(
			"Start an agent of the project you are in", identity.Name+" agent start --name reviewer",
			"By id, as JSON", identity.Name+" agent start --id agent-1a2b3c4d --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not started",
			clikit.OutputNote:    "The agent, running.\n" + `With --json: {"result": "started", "agent": {...}}`,
			clikit.Results:       "started",
			clikit.Errors: clikit.CodeList(api.ErrorCodeNotFound, api.ErrorCodeProjectClosed, api.ErrorCodeStartFailed,
				api.ErrorCodeAgentRemoved, api.ErrorCodeAgentRunning, api.ErrorCodeAgentBusy, api.ErrorCodeAcaUnavailable, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			r, err := clientOf(cmd).StartAgent(cmd.Context(), path, project, dir)
			if problemCode(err) == api.ErrorCodeAgentBusy {
				return &clikit.Hinted{Err: err, Hint: "wait for its background session, or start again in a new session with " + identity.Name + " agent restart --force"}
			}
			if err != nil {
				return agentHint(ref.notFoundHint(err))
			}
			return clikit.PrintResult(cmd, agentResult(r))
		},
	}
	ref.addFlags(cmd)
	return cmd
}

func newAgentRestartCmd() *cobra.Command {
	var ref agentRef
	var force bool
	cmd := &cobra.Command{
		Use:   "restart (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] [--force]",
		Short: "Start an agent again in a new session, which reads its history in brief",
		Long: "agent start goes on with the same conversation, whatever its size. This stops\n" +
			"the AI coding agent if it runs and starts a new session under the same id, in\n" +
			"the directory it last worked in if that is still there, else its own. Its first\n" +
			"message tells it to read agent transcript --brief. Messages not yet delivered\n" +
			"reach it after that one, general even if they were instant; one delivered just\n" +
			"before may come again. What the old session held only in memory goes with it.\n" +
			"A background session its AI coding agent moved it to is stopped too, and named,\n" +
			"as agent start and agent remove do.\n" +
			"Unless it is idle with an empty input box, and no such session is at work, it\n" +
			"takes --force, which can leave work half done.",
		Example: clikit.Examples(
			"Restart an idle agent of the project you are in", identity.Name+" agent restart --name reviewer",
			"In the middle of its work, as JSON", identity.Name+" agent restart --id agent-1a2b3c4d --force --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not restarted",
			clikit.OutputNote:    "The agent, running in its new session.\n" + `With --json: {"result": "restarted", "agent": {...}}, with stopped_sessions when it stopped any`,
			clikit.Results:       "restarted",
			clikit.Errors: clikit.CodeList(api.ErrorCodeNotFound, api.ErrorCodeProjectClosed, api.ErrorCodeStartFailed,
				api.ErrorCodeAgentRemoved, api.ErrorCodeAgentBusy, api.ErrorCodeAcaUnsupported, api.ErrorCodeAcaUnavailable, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			r, err := clientOf(cmd).RestartAgent(cmd.Context(), path, project, dir, force)
			if err != nil {
				return agentHint(ref.notFoundHint(err))
			}
			return clikit.PrintResult(cmd, agentResult(r))
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().BoolVar(&force, "force", false, "restart it even in the middle of its work")
	return cmd
}

func newAgentRenameCmd() *cobra.Command {
	var ref agentRef
	var newName string
	cmd := &cobra.Command{
		Use:   "rename (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] --new-name <name>",
		Short: "Give an agent a new name",
		Long: "It goes on running, in the same session. Names have letters, numbers and -\n" +
			"only, and are unique within a project, among agents and terminals. What\n" +
			"used the old name no longer finds it. A removed agent cannot be renamed.",
		Example: clikit.Examples(
			"Rename an agent of the project you are in", identity.Name+" agent rename --name reviewer --new-name auth-review",
			"By id, as JSON", identity.Name+" agent rename --id agent-1a2b3c4d --new-name auth-review --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not renamed",
			clikit.OutputNote:    "The agent, under its new name.\n" + `With --json: {"result": "renamed", "agent": {...}}`,
			clikit.Results:       "renamed",
			clikit.Errors: clikit.CodeList(api.ErrorCodeAgentRemoved, api.ErrorCodeInvalidName, api.ErrorCodeNameTaken,
				api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			r, err := clientOf(cmd).RenameAgent(cmd.Context(), path, project, dir, newName)
			switch problemCode(err) {
			case api.ErrorCodeNameTaken, api.ErrorCodeInvalidName:
				return &clikit.Hinted{Err: err, Hint: "choose another --new-name"}
			}
			if err != nil {
				return agentHint(ref.notFoundHint(err))
			}
			return clikit.PrintResult(cmd, agentResult(r))
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().StringVar(&newName, "new-name", "", "`name` to give the agent")
	cmd.MarkFlagRequired("new-name")
	clikit.CompleteFlag(cmd, "new-name", cobra.NoFileCompletions)
	return cmd
}

func newAgentSendCmd() *cobra.Command {
	var ref agentRef
	var prompt string
	var instant bool
	cmd := &cobra.Command{
		Use:   "send (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] --prompt <text> [--instant]",
		Short: "Give a running agent a message, submitted once it can take it",
		Long: "The message is queued for the agent, in the order sent. It waits while text\n" +
			"is in the agent's input box, a prompt is open or its screen cannot be read,\n" +
			"for as long as that lasts. During a turn it goes in, for the agent to take\n" +
			"up at its next step (Claude Code, Codex), or after the turn (Cursor,\n" +
			"Antigravity). This does not wait for it to go in or for a reply. A message\n" +
			"still queued when the agent ends is kept: it goes on when the agent is started\n" +
			"again in the same session or restarted, or else waits in wdx message list.\n\n" +
			"--instant goes before the other messages, and waits as they do. Claude Code\n" +
			"takes it at once, Codex at its next step. An AI coding agent that cannot take\n" +
			"a message into its running turn has the turn interrupted first, but not in a\n" +
			"pane too narrow to show that a turn is running.\n\n" +
			"A message whose first non-blank character is ! is given to the model as \\!,\n" +
			"never run as a shell command.",
		Example: clikit.Examples(
			"Give an agent of the project you are in a task", identity.Name+" agent send --name reviewer --prompt summarize",
			"Give it this first: Claude Code takes it at once", identity.Name+" agent send --name reviewer --prompt stop --instant",
			"By id, as JSON", identity.Name+" agent send --id agent-1a2b3c4d --prompt summarize --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not sent",
			clikit.OutputNote:    "The agent the message is queued for.\n" + `With --json: {"result": "queued", "agent": {...}}`,
			clikit.Results:       "queued",
			clikit.Errors: clikit.CodeList(api.ErrorCodeNotFound, api.ErrorCodeAgentNotRunning, api.ErrorCodeAgentRemoved,
				api.ErrorCodeSendFailed, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if prompt == "" {
				return &clikit.UsageError{Err: errors.New("--prompt is empty")}
			}
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			r, err := clientOf(cmd).SendAgent(cmd.Context(), path, project, dir, prompt, instant)
			if problemCode(err) == api.ErrorCodeAgentNotRunning {
				return &clikit.Hinted{Err: err, Hint: "start it with " + identity.Name + " agent start, or open its project"}
			}
			if err != nil {
				return agentHint(ref.notFoundHint(err))
			}
			return clikit.PrintResult(cmd, agentResult(r))
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().StringVar(&prompt, clikit.PromptFlag, "", "`text` to give it; it is submitted")
	cmd.Flags().BoolVar(&instant, "instant", false, "go before other messages; Claude Code takes it at once, and an AI coding agent that cannot take it into its turn is interrupted")
	cmd.MarkFlagRequired(clikit.PromptFlag)
	clikit.CompleteFlag(cmd, clikit.PromptFlag, cobra.NoFileCompletions)
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
		Example: clikit.Examples(
			"Remove an agent of the project you are in", identity.Name+" agent remove --name reviewer",
			"From a script: stop it if it is running", identity.Name+" agent remove --id agent-1a2b3c4d --stop-running --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not removed",
			clikit.OutputNote:    "The removed agent, as it was before.\n" + `With --json: {"result": "removed", "agent": {...}}`,
			clikit.Results:       "removed",
			clikit.Errors:        clikit.CodeList(api.ErrorCodeNotFound, api.ErrorCodeAgentRemoved, api.ErrorCodeAgentRunning, api.ErrorCodeDaemonUnavailable),
			clikit.JSONRequires:  clikit.Annotation("stop-running", "cancel"),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			if stopRunning {
				clikit.LogAnswer(cmd, stopAgentQuestion, "stop", "--stop-running")
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
			return clikit.PrintResult(cmd, agentResult(r))
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
		Example: clikit.Examples(
			"Attach to an agent of the project you are in", identity.Name+" agent attach --name reviewer",
			"With ctrl-] as the prefix", identity.Name+" agent attach --id agent-1a2b3c4d --prefix-key ctrl-]",
			"As JSON once it ends", identity.Name+" agent attach --name reviewer --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not attached",
			clikit.OutputNote: "The agent, once you detach or it exits.\n" +
				`With --json: {"result": "detached" or "exited", "agent": {...}}`,
			clikit.Results: clikit.Annotation("detached", "exited"),
			clikit.Errors: clikit.CodeList(api.ErrorCodeNotFound, api.ErrorCodeAgentNotRunning, api.ErrorCodeAttachUnsupported,
				api.ErrorCodeConnectionLost, api.ErrorCodeDaemonUnavailable, api.ErrorCodeHolderVersionUnsupported),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			key, err := parsePrefixKey(prefix)
			if err != nil {
				return &clikit.UsageError{Err: err}
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
		return &clikit.Hinted{Err: err, Hint: "choose another --name or --worktree"}
	case api.ErrorCodeBranchExists:
		return &clikit.Hinted{Err: err, Hint: "a new worktree gets a new branch: choose another --worktree, or delete the branch"}
	case api.ErrorCodePathExists:
		return &clikit.Hinted{Err: err, Hint: "something else is in the worktree's place: choose another --worktree"}
	case api.ErrorCodeWorktreeFailed:
		if detail(err, "reason") == "no_commit" {
			return &clikit.Hinted{Err: err, Hint: "commit once in the project, then create the worktree"}
		}
	case api.ErrorCodeProjectClosed:
		return &clikit.Hinted{Err: err, Hint: "open the project first with " + identity.Name + " project open"}
	case api.ErrorCodeAgentRemoved:
		return &clikit.Hinted{Err: err, Hint: "it goes when its last child is removed; see them with " + identity.Name + " agent get"}
	case api.ErrorCodeAgentBusy:
		return &clikit.Hinted{Err: err, Hint: "see what it is doing with " + identity.Name + " agent attach, or restart it anyway with --force"}
	case api.ErrorCodeAgentRunning:
		return &clikit.Hinted{Err: err, Hint: "it is already running; attach to it with " + identity.Name + " agent attach"}
	case api.ErrorCodeStartFailed:
		return &clikit.Hinted{Err: err, Hint: "check its dir, then run " + identity.Name + " agent start"}
	case api.ErrorCodeAcaUnavailable:
		return &clikit.Hinted{Err: err, Hint: acaUnavailableHint(err)}
	case api.ErrorCodeNotFound:
		if detail(err, "argument") == "aca" {
			return &clikit.Hinted{Err: err, Hint: identity.Name + " agent acas lists the AI coding agents that can be used"}
		}
	}
	return err
}

// acaUnavailableHint says how to make the AI coding agent of an aca_unavailable
// usable, by its reason.
func acaUnavailableHint(err error) string {
	switch detail(err, "reason") {
	case "not_installed":
		return "install " + detail(err, "program") + ", then check with " + identity.Name + " deps list"
	case "disabled":
		return "run " + identity.Name + " setting set --name aca." + detail(err, "aca") + ".enabled --value true"
	}
	return identity.Name + " deps list says which are installed, " + identity.Name + " setting list which are enabled"
}

// detail is the string under key in the details of err's problem, "" when none.
func detail(err error, key string) string {
	e, ok := errors.AsType[*client.ProblemError](err)
	if !ok || e.Problem.Details == nil {
		return ""
	}
	v, _ := (*e.Problem.Details)[key].(string)
	return v
}

// createDir is the dir agent create sends: with --node and a parent named by
// id none, as the host takes the project and dir from the parent; else the
// working directory, which --node needs a project flag for, a parent's name
// being unique only in its project.
func createDir(cmd *cobra.Command, parentID, parentName string) (string, error) {
	if nodeOf(cmd) != "" && parentID != "" {
		return "", nil
	}
	dir, err := workDir(cmd)
	if err != nil && nodeOf(cmd) != "" && parentName != "" {
		return "", &clikit.Hinted{Err: err, Hint: "or name the parent by --parent-id, which needs no project flag"}
	}
	return dir, err
}

// agentResult is an agent a command changed.
type agentResult api.AgentResult

func (r agentResult) Text(w io.Writer, st style.Style) {
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
	field := clikit.FieldPrinter(w, st, width)
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
	if r.StoppedSessions != nil {
		fmt.Fprintf(w, "%s %s\n", st.Warning("stopped its background session:"), strings.Join(*r.StoppedSessions, ", "))
	}
	if r.PromptError != nil {
		fmt.Fprintf(w, "%s %s\n", st.Warning("its prompt was not sent:"), *r.PromptError)
	}
}

// agentList is a list of agents; all says they may belong to several
// projects.
type agentList struct {
	api.AgentList
	all        bool
	thresholds contextThresholds
}

func (l agentList) Text(w io.Writer, st style.Style) {
	if len(l.Agents) == 0 {
		fmt.Fprintln(w, "no agents")
		return
	}
	var table style.Table
	heading := []string{st.Heading("NAME"), st.Heading("STATE"), st.Heading("ACTIVITY"), st.Heading("MODEL"), st.Heading("CONTEXT"), st.Heading("ACA"), st.Heading("DIR"), st.Heading("ID")}
	if l.all {
		heading = append([]string{st.Heading("PROJECT")}, heading...)
	}
	table.Row(heading...)
	for _, r := range agentTreeRows(l.Agents) {
		a := r.agent
		row := []string{st.Muted(r.guide) + st.Name(a.Name), agentStateText(st, a), agentActivityText(st, a), agentModelText(a), st.Context(agentContextTokens(a), l.thresholds.warning, l.thresholds.critical, agentContextSize(a)), a.Aca, a.Dir, st.Muted(a.Id)}
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
type agentDetail struct {
	api.AgentEnvelope
	thresholds contextThresholds
	now        time.Time // what the limit's reset is told from
}

// LogValue keeps the prompt's text out of the log.
func (d agentDetail) LogValue() slog.Value { return loggedAgents(d.AgentEnvelope) }

// LogValue keeps the prompts' text out of the log.
func (l agentList) LogValue() slog.Value { return loggedAgents(l.AgentList) }

// LogValue keeps the prompt's text out of the log.
func (r agentResult) LogValue() slog.Value { return loggedAgents(api.AgentResult(r)) }

// loggedAgents keeps the text of the agents' prompts out of the log.
func loggedAgents(v any) slog.Value {
	b, err := json.Marshal(v)
	if err != nil {
		return slog.StringValue("unreadable response")
	}
	return slog.AnyValue(api.AgentResponseLogged(b))
}

func (d agentDetail) Text(w io.Writer, st style.Style) {
	a := d.Agent
	field := clikit.FieldPrinter(w, st, 11)
	fmt.Fprintf(w, "agent %s in %s\n", st.Name(a.Name), st.Name(a.ProjectName))
	field("state", agentStateText(st, a))
	if a.Activity != nil {
		field("activity", agentActivityText(st, a))
	}
	if a.Limited != nil {
		field("limited", st.Failure(api.LimitedText(a.Aca, *a.Limited, d.now)))
	}
	if a.Prompt != nil {
		p := *a.Prompt
		field("prompt", promptKindText(st, p.Kind)+promptTitleText(st, p))
		field("question", p.Question)
		if p.Detail != nil {
			lines := strings.Split(*p.Detail, "\n")
			field("detail", lines[0])
			for _, line := range lines[1:] {
				field("", line)
			}
		}
		for i, option := range promptOptionLines(p) {
			if i == 0 {
				field("options", option)
			} else {
				field("", option)
			}
		}
	}
	if a.Draft != nil && *a.Draft {
		field("draft", "yes")
	}
	if a.Queued != nil && *a.Queued > 0 {
		field("queued", strconv.Itoa(*a.Queued))
	}
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
	if model := agentModelDetail(st, a); model != "" {
		field("model", model)
	}
	if a.Context != nil {
		field("context", st.Context(a.Context.Tokens, d.thresholds.warning, d.thresholds.critical, api.ContextText(*a.Context)))
	}
	field("permissions", string(a.Permissions))
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

// agentModelDetail is the model a last showed, and the one it was asked to use.
func agentModelDetail(st style.Style, a api.Agent) string {
	switch {
	case a.RequestedModel == nil:
		return agentModelText(a)
	case a.Model == nil:
		return st.Muted("asked for " + *a.RequestedModel)
	}
	return *a.Model + " " + st.Muted("(asked for "+*a.RequestedModel+")")
}

// agentModelText is the model a last showed; nothing before it has.
func agentModelText(a api.Agent) string {
	if a.Model == nil {
		return ""
	}
	return *a.Model
}

// agentContextTokens is how many tokens are in a's context, 0 before its AI coding
// agent has told.
func agentContextTokens(a api.Agent) int64 {
	if a.Context == nil {
		return 0
	}
	return a.Context.Tokens
}

// agentContextSize is how many tokens are in a's context; nothing before its AI
// coding agent has told.
func agentContextSize(a api.Agent) string {
	if a.Context == nil {
		return ""
	}
	return api.TokenCount(a.Context.Tokens)
}

// contextThresholds are the tokens in a context from which its size is a warning,
// and critical: the context.threshold settings.
type contextThresholds struct{ warning, critical int64 }

func contextThresholdsOf(cmd *cobra.Command) contextThresholds {
	cfg := envOf(cmd).Config
	return contextThresholds{warning: cfg.ContextWarning, critical: cfg.ContextCritical}
}

// agentActivityText is what a is doing, which a human may have to answer when it
// is waiting, then the kind of the prompt when it is read; nothing when it is not
// running. One limited by its account reads limited unless it is working, as a turn
// under way goes on (the wdx doc AGENT_LIMITED).
func agentActivityText(st style.Style, a api.Agent) string {
	working := a.Activity != nil && *a.Activity == api.AgentActivityWorking
	switch {
	case a.Limited != nil && !working:
		return st.Failure("limited")
	case a.Activity == nil:
		return ""
	case a.Prompt != nil:
		return promptKindText(st, a.Prompt.Kind)
	case *a.Activity == api.AgentActivityWaiting:
		return st.Warning(string(*a.Activity))
	case *a.Activity == api.AgentActivityUnknown:
		return st.Muted(string(*a.Activity))
	}
	return string(*a.Activity)
}

// promptKindText is a prompt's kind as a human reads it: yes/no or decision, as
// what waits for them.
func promptKindText(st style.Style, kind api.AgentPromptKind) string {
	if kind == api.AgentPromptKindYesNo {
		return st.Warning("yes/no")
	}
	return st.Warning(string(kind))
}

// promptTitleText is " · " and the prompt's title, "" without one.
func promptTitleText(st style.Style, p api.AgentPrompt) string {
	if p.Title == nil {
		return ""
	}
	return " · " + st.Name(*p.Title)
}

// promptOptionLines is a prompt's options, one a line, ❯ before the selected one
// and a box before each of a multi-select, ticked or not.
func promptOptionLines(p api.AgentPrompt) []string {
	lines := make([]string, 0, len(p.Options))
	for i, option := range p.Options {
		mark := "  "
		if p.Selected != nil && *p.Selected == i {
			mark = "❯ "
		}
		if p.Multi {
			mark += "[ ] "
			if slices.Contains(p.Ticked, i) {
				mark = strings.Replace(mark, "[ ]", "[✔]", 1)
			}
		}
		lines = append(lines, mark+option)
	}
	return lines
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
