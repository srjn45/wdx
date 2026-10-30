package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
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
	cmd.AddCommand(newTerminalAttachCmd(), newTerminalCreateCmd(), newTerminalGetCmd(), newTerminalHistoryCmd(),
		newTerminalListCmd(), newTerminalStartCmd(), newTerminalRemoveCmd())
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

func newTerminalGetCmd() *cobra.Command {
	var ref terminalRef
	cmd := &cobra.Command{
		Use:   "get (--id <id> | --name <name>) [--project-id <id> | --project-name <name>]",
		Short: "Show a terminal",
		Long: "In any state, with why it failed when it did. Without a project flag, --name\n" +
			"is looked up in the project the working directory is in. Nothing is changed.",
		Example: examples(
			"Show a terminal of the project you are in", identity.Name+" terminal get --name server",
			"By id, as JSON", identity.Name+" terminal get --id term-1a2b3c4d --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The terminal.\n" + `With --json: {"terminal": {...}}`,
			errorList:  codeList(api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			t, err := clientOf(cmd).GetTerminal(cmd.Context(), path, project, dir)
			if err != nil {
				return ref.notFoundHint(err)
			}
			return printResult(cmd, terminalDetail(t))
		},
	}
	ref.addFlags(cmd)
	return cmd
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

// askStopRunning asks whether to stop the terminals and agents the problem
// in err says are running, them in the hints; nil means stop them. cancel
// answers no in advance.
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
	terminals, agents := detailRunning(err)
	line := func(name, state string, pid *int) {
		text := ""
		if pid != nil {
			text = fmt.Sprintf(" (pid %d)", *pid)
		}
		fmt.Fprintf(prompt, "  %s %s%s\n", st.Name(name), state, st.Muted(text))
	}
	for _, t := range terminals {
		line(t.Name, string(t.State), t.Pid)
	}
	for _, a := range agents {
		line(a.Name, string(a.State), a.Pid)
	}
	if len(terminals) > 0 {
		fmt.Fprintln(prompt, st.Warning("stopping a shell ends the programs running in it"))
	}
	if len(agents) > 0 {
		fmt.Fprintln(prompt, st.Warning("stopping an agent interrupts what it is doing"))
	}
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

// detailRunning returns the terminals and agents the problem in err involves:
// those under details.terminals and details.agents, or the one under
// details.terminal or details.agent.
func detailRunning(err error) ([]api.Terminal, []api.Agent) {
	e, ok := errors.AsType[*client.ProblemError](err)
	if !ok || e.Problem.Details == nil {
		return nil, nil
	}
	var terminals []api.Terminal
	var agents []api.Agent
	decodeDetail(e.Problem, "terminals", &terminals)
	decodeDetail(e.Problem, "agents", &agents)
	var t api.Terminal
	if decodeDetail(e.Problem, "terminal", &t) {
		terminals = append(terminals, t)
	}
	var a api.Agent
	if decodeDetail(e.Problem, "agent", &a) {
		agents = append(agents, a)
	}
	return terminals, agents
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

// terminalResult is a terminal a command changed.
type terminalResult api.TerminalResult

func (r terminalResult) text(w io.Writer, st style.Style) {
	t := r.Terminal
	in := "in"
	if r.Result == api.ResultRemoved {
		in = "from"
	}
	fmt.Fprintf(w, "%s terminal %s %s %s\n", st.Success(string(r.Result)), st.Name(t.Name), in, st.Name(t.ProjectName))
	field := fieldPrinter(w, st, 6)
	field("dir", t.Dir)
	field("shell", t.Shell)
	field("id", t.Id)
}

// terminalList is a list of terminals; all says they may belong to several
// projects.
type terminalList struct {
	api.TerminalList
	all bool
}

func (l terminalList) text(w io.Writer, st style.Style) {
	if len(l.Terminals) == 0 {
		fmt.Fprintln(w, "no terminals")
		return
	}
	var table style.Table
	heading := []string{st.Heading("NAME"), st.Heading("STATE"), st.Heading("DIR"), st.Heading("ID")}
	if l.all {
		heading = append([]string{st.Heading("PROJECT")}, heading...)
	}
	table.Row(heading...)
	for _, t := range l.Terminals {
		row := []string{st.Name(t.Name), terminalStateText(st, t), t.Dir, st.Muted(t.Id)}
		if l.all {
			row = append([]string{st.Name(t.ProjectName)}, row...)
		}
		table.Row(row...)
	}
	table.Write(w)
}

// terminalDetail is one terminal, in any state.
type terminalDetail api.TerminalEnvelope

func (d terminalDetail) text(w io.Writer, st style.Style) {
	t := d.Terminal
	field := fieldPrinter(w, st, 8)
	fmt.Fprintf(w, "terminal %s in %s\n", st.Name(t.Name), st.Name(t.ProjectName))
	field("state", terminalStateText(st, t))
	if t.Error != nil {
		field("error", st.Failure(*t.Error))
	}
	field("dir", t.Dir)
	field("shell", t.Shell)
	if t.Pid != nil {
		field("pid", strconv.Itoa(*t.Pid))
	}
	field("id", t.Id)
	field("created", t.CreatedAt.Local().Format(time.DateTime))
	if t.StartedAt != nil {
		field("started", t.StartedAt.Local().Format(time.DateTime))
	}
	if t.EndedAt != nil {
		field("ended", t.EndedAt.Local().Format(time.DateTime))
	}
}

func terminalStateText(st style.Style, t api.Terminal) string {
	switch t.State {
	case api.TerminalStateRunning, api.TerminalStateStarting:
		return st.Success(string(t.State))
	case api.TerminalStateExited:
		if t.ExitCode != nil {
			return fmt.Sprintf("exited (%d)", *t.ExitCode)
		}
	case api.TerminalStateFailed, api.TerminalStateLost:
		return st.Failure(string(t.State))
	}
	return st.Muted(string(t.State))
}
