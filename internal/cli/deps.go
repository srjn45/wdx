package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/deps"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/style"
)

func newDepsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deps",
		Short: "Show and install the programs wdx needs besides itself",
		Long: "git is mandatory: projects and their terminals need it. The others are\n" +
			"optional: without one, only the features it enables are missing.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newDepsListCmd(), newDepsInstallCmd())
	return cmd
}

// Session returns the graphical session the variables of environ, as
// os.Environ gives them, describe, for Env.Deps: wayland, x11 or "".
func Session(environ []string) string { return deps.Session(environ) }

// probe is how the deps commands look at the machine: env.Deps, its parts
// left nil taking the real ones.
func probe(env Env) deps.Probe {
	p := env.Deps
	if p.GOOS == "" {
		p.GOOS = runtime.GOOS
	}
	if p.LookPath == nil {
		p.LookPath = env.LookPath
	}
	if p.LookPath == nil {
		p.LookPath = exec.LookPath
	}
	if p.Output == nil {
		p.Output = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		}
	}
	if p.ReadFile == nil {
		p.ReadFile = os.ReadFile
	}
	if p.Run == nil {
		p.Run = func(ctx context.Context, args []string, stdin io.Reader, out io.Writer) error {
			c := exec.CommandContext(ctx, args[0], args[1:]...)
			c.Stdin, c.Stdout, c.Stderr = stdin, out, out
			return c.Run()
		}
	}
	return p
}

// systemView is the machine as deps sees it.
type systemView struct {
	OS             string  `json:"os"`
	Distribution   *string `json:"distribution"`
	PackageManager *string `json:"package_manager"`
	Session        *string `json:"session"`
}

// dependencyView is a dependency and its state on the machine.
type dependencyView struct {
	Name      string   `json:"name"`
	Mandatory bool     `json:"mandatory"`
	Features  []string `json:"features"`
	Status    string   `json:"status"`
	Program   *string  `json:"program"` // the program found, as wl-copy for clipboard
	Path      *string  `json:"path"`
	Version   *string  `json:"version"`
	Detail    *string  `json:"detail"` // why it is not ready
	// Install is how to install it here when it is not ready: for
	// container, docker's way; podman's is --name podman.
	Install *installView `json:"install"`
}

func newDependencyView(c deps.Check) dependencyView {
	return dependencyView{
		Name:      c.Name,
		Mandatory: c.Mandatory,
		Features:  c.Features,
		Status:    string(c.Status),
		Program:   optional(c.Program),
		Path:      optional(c.Path),
		Version:   optional(c.Version),
		Detail:    optional(c.Detail),
	}
}

type depsList struct {
	System       systemView       `json:"system"`
	Dependencies []dependencyView `json:"dependencies"`
}

func (l depsList) text(w io.Writer, st style.Style) {
	var t style.Table
	for _, d := range l.Dependencies {
		need := "optional"
		if d.Mandatory {
			need = "mandatory"
		}
		status := st.Success(d.Status)
		switch {
		case d.Status == string(deps.Ready):
		case d.Mandatory:
			status = st.Failure(d.Status)
		default:
			status = st.Warning(d.Status)
		}
		found := st.Muted("-")
		if d.Program != nil {
			found = *d.Program
			if d.Version != nil {
				found += " " + *d.Version
			}
		}
		t.Row(st.Name(d.Name), need, status, found, st.Muted(strings.Join(d.Features, ", ")))
	}
	t.Write(w)
	for _, d := range l.Dependencies {
		if d.Detail == nil {
			continue
		}
		fmt.Fprintf(w, "%s: %s\n", st.Name(d.Name), *d.Detail)
		if d.Install != nil && len(d.Install.Commands) > 0 {
			install := st.Command(identity.Name + " deps install --name " + d.Name)
			if d.Name == "container" {
				// Either provides it: the user picks.
				install = st.Command(identity.Name+" deps install --name docker") + " or " + st.Command("--name podman")
			}
			fmt.Fprintf(w, "  %s %s\n", st.Hint("install it:"), install)
		} else if d.Install != nil && d.Install.Instructions != nil {
			fmt.Fprintf(w, "  %s %s\n", st.Hint("install it:"), *d.Install.Instructions)
		}
	}
	sys := l.System.OS
	if l.System.Distribution != nil {
		sys = *l.System.Distribution
	}
	if l.System.PackageManager != nil {
		sys += ", installs with " + *l.System.PackageManager
	}
	if l.System.Session != nil {
		sys += ", " + *l.System.Session + " session"
	}
	fmt.Fprintf(w, "%s %s\n", st.Muted("system:"), sys)
}

func newDepsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the programs wdx needs, whether each is ready, and what it enables",
		Long: "A dependency is ready, missing, too_old (git before 2.31) or not_ready:\n" +
			"installed but not usable, as docker whose daemon does not answer. The\n" +
			"clipboard tool is the one for the session: wl-copy on Wayland, xclip or\n" +
			"xsel on X11, pbcopy on macOS.",
		Example: examples(
			"Every dependency", identity.Name+" deps list",
			"As JSON, for a script", identity.Name+" deps list --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "Each dependency: mandatory or optional, its status, the program and version found and the\n" +
				"features it enables; why it is not ready below; then the system.\n" +
				`With --json: {"system": {...}, "dependencies": [...]}`,
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := probe(invocationOf(cmd).env)
			sys := deps.Detect(p)
			l := depsList{System: systemView{
				OS:             sys.OS,
				Distribution:   optional(sys.Distribution),
				PackageManager: optional(sys.PackageManager),
				Session:        optional(sys.Session),
			}}
			for _, c := range deps.CheckAll(cmd.Context(), p) {
				v := newDependencyView(c)
				if c.Status != deps.Ready {
					name := c.Name
					if name == "container" {
						name = "docker"
					}
					plan, _ := deps.PlanFor(name, sys, userName()) // unsupported: its instructions
					v.Install = newInstallView(plan)
				}
				l.Dependencies = append(l.Dependencies, v)
			}
			return printResult(cmd, l)
		},
	}
}

// installView is how to install a dependency here: the commands, or the
// instructions when wdx cannot.
type installView struct {
	Commands     [][]string `json:"commands"`
	NeedsRoot    bool       `json:"needs_root"`
	After        []string   `json:"after"`
	Instructions *string    `json:"instructions"`
}

func newInstallView(p deps.Plan) *installView {
	v := &installView{Commands: p.Commands, NeedsRoot: p.NeedsRoot(), After: p.After, Instructions: optional(p.Instructions)}
	if v.Commands == nil {
		v.Commands = [][]string{}
	}
	if v.After == nil {
		v.After = []string{}
	}
	return v
}

// installResult is what wdx deps install did.
type installResult struct {
	Result     string         `json:"result"` // installed, or ready when nothing was needed
	Dependency dependencyView `json:"dependency"`
	Install    *installView   `json:"install"` // what ran; null when nothing did
}

func (r installResult) text(w io.Writer, st style.Style) {
	d := r.Dependency
	found := d.Name
	if d.Program != nil && *d.Program != d.Name {
		found += " (" + *d.Program + ")"
	}
	if r.Result == resultReady {
		fmt.Fprintf(w, "%s %s: nothing to install\n", found, st.Success("is ready"))
		return
	}
	fmt.Fprintf(w, "%s %s\n", found, st.Success("installed"))
	if d.Status != string(deps.Ready) && d.Detail != nil {
		fmt.Fprintf(w, "  %s %s\n", st.Warning("not ready yet:"), *d.Detail)
	}
	for _, a := range r.Install.After {
		fmt.Fprintf(w, "  %s %s\n", st.Hint("then:"), a)
	}
}

// installError is a failed wdx deps install: why, and how to do it by hand.
type installError struct {
	code   api.ErrorCode
	err    error
	plan   deps.Plan
	output string // the end of what the failing command printed
}

func (e *installError) Error() string { return e.err.Error() }
func (e *installError) Unwrap() error { return e.err }

// depsProblem returns the problem of an install error, if err is one: the
// hint is how to do it by hand, the details hold the plan.
func depsProblem(err error) (api.Problem, bool) {
	e, ok := errors.AsType[*installError](err)
	if !ok {
		return api.Problem{}, false
	}
	p := api.NewProblem(e.code, e.Error())
	details := map[string]any{"install": newInstallView(e.plan)}
	if e.output != "" {
		details["output"] = e.output
	}
	p.Details = &details
	if manual := e.plan.Manual(); manual != "" {
		hint := manual
		if len(e.plan.Commands) > 0 {
			hint = "install it yourself: " + manual
		}
		p.Hint = &hint
	}
	return p, true
}

// installQuestion is the question asked before running commands as root.
const installQuestion = "install with sudo"

// outputKept is how many lines of a failed command's output an error keeps.
const outputKept = 20

func newDepsInstallCmd() *cobra.Command {
	var name string
	var yes bool
	cmd := &cobra.Command{
		Use:   "install --name <dependency> [--yes]",
		Short: "Install a dependency with the system's package manager",
		Long: "On Ubuntu and Debian with apt, on macOS with brew. When installing needs\n" +
			"root, the commands are shown and you are asked before sudo runs them; sudo\n" +
			"then asks for your password. --yes answers in advance; without a terminal\n" +
			"sudo cannot ask, so it works only when no password is needed. docker and\n" +
			"podman each provide the container dependency. A dependency already ready\n" +
			"is left as it is. Every failure says why and how to install it yourself.",
		Example: examples(
			"Install the clipboard tool tide copies with", identity.Name+" deps install --name clipboard",
			"Install docker without being asked", identity.Name+" deps install --name docker --yes",
			"From a script, where sudo needs no password", identity.Name+" deps install --name git --yes --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not installed",
			outputNote: "The dependency as it is now, with what is left to do, as logging in again.\n" +
				`With --json: {"result": ..., "dependency": {...}, "install": {...}}`,
			resultList:   annotation(resultInstalled, resultReady),
			errorList:    codeList(api.ErrorCodeCancelled, api.ErrorCodeInstallFailed, api.ErrorCodePrivilegesRequired, api.ErrorCodeUnsupportedSystem),
			jsonRequires: annotation("yes"),
		},
		RunE: func(cmd *cobra.Command, _ []string) error { return runDepsInstall(cmd, name, yes) },
	}
	cmd.Flags().StringVar(&name, "name", "", "`dependency` to install: "+strings.Join(deps.Installable, ", "))
	cmd.Flags().BoolVar(&yes, "yes", false, "run the commands that need root without asking")
	_ = cmd.MarkFlagRequired("name")
	cmd.RegisterFlagCompletionFunc("name", cobra.FixedCompletions(deps.Installable, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func runDepsInstall(cmd *cobra.Command, name string, yes bool) error {
	if !slices.Contains(deps.Installable, name) {
		return &usageError{fmt.Errorf("no dependency %q: name one of %s", name, strings.Join(deps.Installable, ", "))}
	}
	ctx, env := cmd.Context(), invocationOf(cmd).env
	p := probe(env)
	if c := deps.CheckInstallable(ctx, p, name); c.Status == deps.Ready {
		return printResult(cmd, installResult{Result: resultReady, Dependency: newDependencyView(c)})
	}
	sys := deps.Detect(p)
	plan, err := deps.PlanFor(name, sys, userName())
	if err != nil {
		return &installError{code: api.ErrorCodeUnsupportedSystem, err: err, plan: plan}
	}
	log := logging.FromContext(ctx)
	log.Info("install planned", "name", name, "commands", plan.Commands, "needs_root", plan.NeedsRoot())

	interactive := canPrompt(cmd)
	if plan.NeedsRoot() {
		switch {
		case interactive && yes:
			logAnswer(cmd, installQuestion, "yes", "--yes")
		case interactive:
			prompt, st := cmd.ErrOrStderr(), stderrStyle(cmd)
			fmt.Fprintf(prompt, "installing %s runs, as root:\n", name)
			for _, c := range plan.Commands {
				fmt.Fprintf(prompt, "  %s\n", st.Command(strings.Join(c, " ")))
			}
			answer, err := ask(prompt, bufio.NewScanner(cmd.InOrStdin()), "run them? "+st.Command("[y/N]")+": ", "y", "n", "")
			if err != nil {
				return err
			}
			if answer != "y" {
				logAnswer(cmd, installQuestion, "no", "prompt")
				return &installError{code: api.ErrorCodeCancelled, err: errors.New("you answered no"), plan: plan}
			}
			logAnswer(cmd, installQuestion, "yes", "prompt")
		case !yes:
			logUnanswered(cmd, installQuestion)
			return &installError{code: api.ErrorCodePrivilegesRequired, err: fmt.Errorf("installing %s needs root and nothing answered whether to use sudo (%s): pass --yes", name, noPromptReason(cmd)), plan: plan}
		default:
			// No terminal: sudo must not ask, so it runs only when it needs
			// no password.
			logAnswer(cmd, installQuestion, "yes", "--yes")
			if _, err := p.Output(ctx, "sudo", "-n", "true"); err != nil {
				log.Info("sudo needs a password", "err", err)
				return &installError{code: api.ErrorCodePrivilegesRequired, err: errors.New("sudo needs a password, and without a terminal it cannot ask for it"), plan: plan}
			}
			plan = plan.NonInteractive()
		}
	}

	for _, c := range plan.Commands {
		var out bytes.Buffer
		var w io.Writer = &out
		var stdin io.Reader
		if interactive {
			// What the package manager prints is shown as it goes, and sudo
			// asks on the terminal.
			w, stdin = io.MultiWriter(cmd.ErrOrStderr(), &out), cmd.InOrStdin()
		}
		start := time.Now()
		err := p.Run(ctx, c, stdin, w)
		log.Info("install command ran", "command", c, "err", err, logging.Duration(start))
		log.Debug("install command output", "command", c, "output", out.String())
		if err != nil {
			return &installError{code: api.ErrorCodeInstallFailed, err: fmt.Errorf("%s failed: %w", strings.Join(c, " "), err), plan: plan, output: lastLines(out.String(), outputKept)}
		}
	}
	c := deps.CheckInstallable(ctx, p, name)
	return printResult(cmd, installResult{Result: resultInstalled, Dependency: newDependencyView(c), Install: newInstallView(plan)})
}

// lastLines returns the last n lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return strings.Join(lines[max(len(lines)-n, 0):], "\n")
}

// userName is the user running wdx, whom docker's group is given to.
func userName() string {
	u, err := user.Current()
	if err != nil {
		return "$USER"
	}
	return u.Username
}
