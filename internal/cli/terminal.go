package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

func newTerminalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "terminal",
		Short: "Manage terminals: shells that belong to a project and outlive the daemon",
		Long: "A terminal's shell runs in its own process, so it keeps running when the\n" +
			"daemon restarts or is upgraded. Closing its project stops it, and opening\n" +
			"the project again starts a fresh shell.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newTerminalCreateCmd(), newTerminalListCmd(), newTerminalStartCmd(), newTerminalRemoveCmd())
	return cmd
}

// projectScope is the project a terminal command works in: named by
// --project-id or --project-name, else the one the working directory is in.
type projectScope struct {
	id, name string
}

func (p *projectScope) addFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&p.id, "project-id", "", "`id` of the project (default: the one the working directory is in)")
	cmd.Flags().StringVar(&p.name, "project-name", "", "`name` of the project (default: the one the working directory is in)")
	cmd.MarkFlagsMutuallyExclusive("project-id", "project-name")
	cmd.RegisterFlagCompletionFunc("project-id", completeProjects("id", ""))
	cmd.RegisterFlagCompletionFunc("project-name", completeProjects("name", ""))
}

// given reports whether a project flag was given.
func (p projectScope) given() bool {
	return p.id != "" || p.name != ""
}

// ref is how the API names the project, "" when no flag names it.
func (p projectScope) ref() string {
	if p.id != "" {
		return "id:" + p.id
	}
	return p.name
}

// workDir is the directory the command runs in.
func workDir(cmd *cobra.Command) (string, error) {
	if dir := invocationOf(cmd).env.WorkDir; dir != "" {
		return dir, nil
	}
	return "", errors.New("the working directory is unknown; pass --project-id or --project-name")
}

// outsideHint tells what to do when no project holds the working directory.
func outsideHint(err error, scope projectScope, also string) error {
	if problemCode(err) != api.ErrorCodeNotFound || scope.given() {
		return err
	}
	return &hinted{err, "run it in a project's worktree, or pass --project-name or --project-id" + also}
}

func newTerminalCreateCmd() *cobra.Command {
	var scope projectScope
	var name string
	cmd := &cobra.Command{
		Use:   "create [--project-id <id> | --project-name <name>] [--name <name>]",
		Short: "Create a terminal and start its shell",
		Long: "Without a project flag, the terminal belongs to the project the working\n" +
			"directory is in, and its shell starts there. With one, it starts in the\n" +
			"working directory when that is in the project, else in the project's\n" +
			"clone path. The shell is terminal.shell, your login shell by default.\n\n" +
			"Terminals get a generated name such as swift-falcon unless --name is\n" +
			"given; names are unique within a project.",
		Example: examples(
			"Create a terminal in the project you are in", identity.Name+" terminal create",
			"Name it and pick the project", identity.Name+" terminal create --project-name app --name server",
			"As JSON", identity.Name+" terminal create --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not created",
			outputNote:    "The terminal, running.\n" + `With --json: {"result": "created", "terminal": {...}}`,
			resultList:    "created",
			errorList: codeList(api.ErrorCodeInvalidName, api.ErrorCodeNameTaken, api.ErrorCodeNotFound, api.ErrorCodeProjectClosed,
				api.ErrorCodeStartFailed, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := workDir(cmd)
			if err != nil {
				return err
			}
			r, err := clientOf(cmd).CreateTerminal(cmd.Context(), api.CreateTerminal{Project: optional(scope.ref()), Dir: dir, Name: optional(name)})
			if err != nil {
				return terminalHint(outsideHint(err, scope, ""))
			}
			return printResult(cmd, terminalResult(r))
		},
	}
	scope.addFlags(cmd)
	cmd.Flags().StringVar(&name, "name", "", "`name` of the terminal (default: a generated one)")
	cmd.RegisterFlagCompletionFunc("name", cobra.NoFileCompletions)
	return cmd
}

func newTerminalListCmd() *cobra.Command {
	var scope projectScope
	var all bool
	cmd := &cobra.Command{
		Use:   "list [--project-id <id> | --project-name <name> | --all]",
		Short: "List terminals",
		Long: "Without a flag, the terminals of the project the working directory is in.\n" +
			"Every state is listed: a terminal stays until it is removed.",
		Example: examples(
			"The terminals of the project you are in", identity.Name+" terminal list",
			"Every project's, as JSON", identity.Name+" terminal list --all --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The terminals by project, then name; an empty list when there are none.\n" +
				`With --json: {"terminals": [...]}`,
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
			list, err := clientOf(cmd).ListTerminals(cmd.Context(), scope.ref(), dir)
			if err != nil {
				return outsideHint(err, scope, ", or --all")
			}
			return printResult(cmd, terminalList{list, all})
		},
	}
	scope.addFlags(cmd)
	cmd.Flags().BoolVar(&all, "all", false, "list the terminals of every project")
	cmd.MarkFlagsMutuallyExclusive("all", "project-id")
	cmd.MarkFlagsMutuallyExclusive("all", "project-name")
	return cmd
}

// terminalRef is a terminal named on the command line by --id, or by
// --name in a project.
type terminalRef struct {
	id, name string
	scope    projectScope
}

func (r *terminalRef) addFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&r.id, "id", "", "`id` of the terminal")
	cmd.Flags().StringVar(&r.name, "name", "", "`name` of the terminal (case-insensitive)")
	cmd.MarkFlagsOneRequired("id", "name")
	cmd.MarkFlagsMutuallyExclusive("id", "name")
	cmd.RegisterFlagCompletionFunc("id", completeTerminals("id"))
	cmd.RegisterFlagCompletionFunc("name", completeTerminals("name"))
	r.scope.addFlags(cmd)
}

// resolve returns how the API names the terminal: its path, and the project
// or dir that names its project, if any.
func (r terminalRef) resolve(cmd *cobra.Command) (path, project, dir string, err error) {
	if r.id != "" {
		return "id:" + r.id, r.scope.ref(), "", nil
	}
	if r.scope.given() {
		return r.name, r.scope.ref(), "", nil
	}
	dir, err = workDir(cmd)
	return r.name, "", dir, err
}

// notFoundHint tells where to look for a terminal that was not found.
func (r terminalRef) notFoundHint(err error) error {
	if problemCode(err) != api.ErrorCodeNotFound || r.id != "" || r.scope.given() {
		return err
	}
	return &hinted{err, "names are unique within a project: run it in the terminal's project, or pass --project-name or --project-id"}
}

func newTerminalStartCmd() *cobra.Command {
	var ref terminalRef
	cmd := &cobra.Command{
		Use:   "start (--id <id> | --name <name>) [--project-id <id> | --project-name <name>]",
		Short: "Start a fresh shell for a terminal that exited, failed or was lost",
		Long: "The terminal keeps its id, name and dir; its scrollback and the programs\n" +
			"that ran in it are gone. Terminals stopped by closing their project start\n" +
			"again when it is opened.",
		Example: examples(
			"Start a terminal of the project you are in", identity.Name+" terminal start --name server",
			"By id, as JSON", identity.Name+" terminal start --id term-1a2b3c4d --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not started",
			outputNote:    "The terminal, running.\n" + `With --json: {"result": "started", "terminal": {...}}`,
			resultList:    "started",
			errorList: codeList(api.ErrorCodeNotFound, api.ErrorCodeProjectClosed, api.ErrorCodeStartFailed,
				api.ErrorCodeTerminalRunning, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			r, err := clientOf(cmd).StartTerminal(cmd.Context(), path, project, dir)
			if err != nil {
				return terminalHint(ref.notFoundHint(err))
			}
			return printResult(cmd, terminalResult(r))
		},
	}
	ref.addFlags(cmd)
	return cmd
}

const stopTerminalQuestion = "stop running terminal"

func newTerminalRemoveCmd() *cobra.Command {
	var ref terminalRef
	var stopRunning, cancel bool
	cmd := &cobra.Command{
		Use:   "remove (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] [--stop-running | --cancel]",
		Short: "Delete a terminal",
		Long: "If its shell is running, you are asked whether to stop it, which ends the\n" +
			"programs in it; --stop-running and --cancel answer in advance, and with\n" +
			"--json one of them is required.",
		Example: examples(
			"Remove a terminal of the project you are in", identity.Name+" terminal remove --name server",
			"From a script: stop it if it is running", identity.Name+" terminal remove --id term-1a2b3c4d --stop-running --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not removed",
			outputNote:    "The removed terminal, as it was before.\n" + `With --json: {"result": "removed", "terminal": {...}}`,
			resultList:    "removed",
			errorList:     codeList(api.ErrorCodeNotFound, api.ErrorCodeTerminalRunning, api.ErrorCodeDaemonUnavailable),
			jsonRequires:  annotation("stop-running", "cancel"),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			if stopRunning {
				logAnswer(cmd, stopTerminalQuestion, "stop", "--stop-running")
			}
			r, err := clientOf(cmd).DeleteTerminal(cmd.Context(), path, project, dir, stopRunning)
			if problemCode(err) == api.ErrorCodeTerminalRunning && !stopRunning {
				if err = askStopRunning(cmd, err, stopTerminalQuestion, cancel, "stop it and remove it?", "it"); err != nil {
					return err
				}
				r, err = clientOf(cmd).DeleteTerminal(cmd.Context(), path, project, dir, true)
			}
			if err != nil {
				return ref.notFoundHint(err)
			}
			return printResult(cmd, terminalResult(r))
		},
	}
	ref.addFlags(cmd)
	addStopRunningFlags(cmd, &stopRunning, &cancel, "its shell")
	return cmd
}

// addStopRunningFlags adds --stop-running and --cancel, which answer
// whether to stop what is running.
func addStopRunningFlags(cmd *cobra.Command, stopRunning, cancel *bool, what string) {
	cmd.Flags().BoolVar(stopRunning, "stop-running", false, "if "+what+" is running, stop it without asking")
	cmd.Flags().BoolVar(cancel, "cancel", false, "if "+what+" is running, change nothing without asking")
	cmd.MarkFlagsMutuallyExclusive("stop-running", "cancel")
}

// askStopRunning asks whether to stop the terminals the problem in err
// says are running, them in the hints; nil means stop them. cancel answers
// no in advance.
func askStopRunning(cmd *cobra.Command, err error, question string, cancel bool, confirm, them string) error {
	if cancel {
		logAnswer(cmd, question, "cancel", "--cancel")
		return &hinted{err, "pass --stop-running to stop " + them}
	}
	if !canPrompt(cmd) {
		logUnanswered(cmd, question)
		return &hinted{err, "pass --stop-running or --cancel"}
	}
	prompt, st := cmd.ErrOrStderr(), stderrStyle(cmd)
	fmt.Fprintln(prompt, err)
	for _, t := range detailTerminals(err) {
		pid := ""
		if t.Pid != nil {
			pid = fmt.Sprintf(" (pid %d)", *t.Pid)
		}
		fmt.Fprintf(prompt, "  %s %s%s\n", st.Name(t.Name), t.State, st.Muted(pid))
	}
	fmt.Fprintln(prompt, st.Warning("stopping a shell ends the programs running in it"))
	choice, aerr := ask(prompt, bufio.NewScanner(cmd.InOrStdin()), confirm+" "+st.Command("[y/n]")+": ", "y", "n")
	if aerr != nil {
		return aerr
	}
	if choice == "n" {
		logAnswer(cmd, question, "cancel", "prompt")
		return &hinted{err, "pass --stop-running to stop " + them}
	}
	logAnswer(cmd, question, "stop", "prompt")
	return nil
}

// detailTerminals returns the terminals the problem in err involves: those
// under details.terminals, or the one under details.terminal.
func detailTerminals(err error) []api.Terminal {
	e, ok := errors.AsType[*client.ProblemError](err)
	if !ok || e.Problem.Details == nil {
		return nil
	}
	var list []api.Terminal
	if decodeDetail(e.Problem, "terminals", &list) {
		return list
	}
	var t api.Terminal
	if decodeDetail(e.Problem, "terminal", &t) {
		return []api.Terminal{t}
	}
	return nil
}

// terminalHint tells what to do about the errors creating or starting a
// terminal can return.
func terminalHint(err error) error {
	switch problemCode(err) {
	case api.ErrorCodeNameTaken, api.ErrorCodeInvalidName:
		return &hinted{err, "choose another --name"}
	case api.ErrorCodeProjectClosed:
		return &hinted{err, "open the project first with " + identity.Name + " project open"}
	case api.ErrorCodeTerminalRunning:
		return &hinted{err, "it is already running; remove it with " + identity.Name + " terminal remove --stop-running to start over"}
	case api.ErrorCodeStartFailed:
		return &hinted{err, "check its dir and terminal.shell, then run " + identity.Name + " terminal start"}
	}
	return err
}

func newHolderCmd() *cobra.Command {
	var o holder.Options
	cmd := &cobra.Command{
		Use:    "holder --id <id> --project <name> --dir <dir> --shell <path>",
		Short:  "Hold a terminal's PTY and shell; the daemon runs it",
		Hidden: true,
		Args:   cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "Nothing: it reports to the daemon on fd 3 and on its socket.",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			env := invocationOf(cmd).env
			o.Socket = identity.HolderSocket(env.Config.Home, o.ID)
			// The daemon starts it with exactly the variables the shell gets.
			o.Env = env.Terminal
			o.Ready = os.NewFile(3, "ready")
			ctx := logging.NewContext(cmd.Context(), logging.FromContext(cmd.Context()).With("terminal_id", o.ID))
			return holder.Run(ctx, o)
		},
	}
	cmd.Flags().StringVar(&o.ID, "id", "", "`id` of the terminal")
	cmd.Flags().StringVar(&o.Project, "project", "", "`name` of its project")
	cmd.Flags().StringVar(&o.Dir, "dir", "", "`dir` the shell starts in")
	cmd.Flags().StringVar(&o.Shell, "shell", "", "`path` of the shell")
	for _, name := range strings.Fields("id project dir shell") {
		cmd.MarkFlagRequired(name)
	}
	return cmd
}
