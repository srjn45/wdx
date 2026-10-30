package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/deps"
	"github.com/srjn45/wdx/internal/identity"
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
	cmd.AddCommand(newDepsListCmd())
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
		if d.Detail != nil {
			fmt.Fprintf(w, "%s: %s\n", st.Name(d.Name), *d.Detail)
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
				l.Dependencies = append(l.Dependencies, newDependencyView(c))
			}
			return printResult(cmd, l)
		},
	}
}
