// Package deps knows the programs wdx needs besides itself, which features
// each one enables, and whether they are ready on this machine
// (docs/DEPS.md).
package deps

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/logging"
)

// Status is whether a dependency can be used.
type Status string

const (
	Ready    Status = "ready"
	Missing  Status = "missing"
	TooOld   Status = "too_old"   // installed, older than wdx needs
	NotReady Status = "not_ready" // installed, not usable: a daemon not answering, access denied
)

// Dependency is a program wdx needs, mandatory for its core or optional
// for some features.
type Dependency struct {
	Name      string
	Mandatory bool
	Features  []string
}

// Dependencies are every dependency, in the order they are listed.
var Dependencies = []Dependency{
	{Name: "git", Mandatory: true, Features: []string{"projects", "terminals"}},
	{Name: "clipboard", Features: []string{"tide copy"}},
	{Name: "container", Features: []string{"observe"}},
}

// MinGit is the oldest git wdx works with: it uses rev-parse
// --path-format=absolute.
var MinGit = [3]int{2, 31, 0}

// Probe is how a check looks at the machine; tests replace its parts.
type Probe struct {
	GOOS string
	// Session is the graphical session: "wayland", "x11", or "" for none
	// (a console, ssh). See Session.
	Session  string
	LookPath func(file string) (string, error)
	// Output runs a program and returns what it printed, stdout and
	// stderr together.
	Output   func(ctx context.Context, name string, args ...string) ([]byte, error)
	ReadFile func(path string) ([]byte, error)
	// Run runs a command to install, with stdin and its output going to
	// out; sudo may ask for a password on the terminal.
	Run func(ctx context.Context, args []string, stdin io.Reader, out io.Writer) error
}

// System is what the machine is, for choosing packages.
type System struct {
	OS             string // linux, darwin
	Distribution   string // the ID of /etc/os-release on Linux: ubuntu, debian, fedora...; "" when unknown
	PackageManager string // apt, brew, or "" when wdx cannot install here
	Session        string // wayland, x11, or ""
}

// Session returns the graphical session the variables of environ, as
// os.Environ gives them, describe: wayland, x11, or "" for none.
func Session(environ []string) string {
	vars := map[string]string{}
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		vars[k] = v
	}
	switch {
	case vars["XDG_SESSION_TYPE"] == "wayland" || vars["WAYLAND_DISPLAY"] != "":
		return "wayland"
	case vars["XDG_SESSION_TYPE"] == "x11" || vars["DISPLAY"] != "":
		return "x11"
	}
	return ""
}

// Detect returns what the machine is.
func Detect(p Probe) System {
	s := System{OS: p.GOOS, Session: p.Session}
	switch p.GOOS {
	case "darwin":
		if _, err := p.LookPath("brew"); err == nil {
			s.PackageManager = "brew"
		}
	case "linux":
		id, like := osRelease(p)
		s.Distribution = id
		if id == "ubuntu" || id == "debian" || strings.Contains(" "+like+" ", " debian ") || strings.Contains(" "+like+" ", " ubuntu ") {
			s.PackageManager = "apt"
		}
	}
	return s
}

// osRelease returns the ID and ID_LIKE of /etc/os-release.
func osRelease(p Probe) (id, like string) {
	b, err := p.ReadFile("/etc/os-release")
	if err != nil {
		return "", ""
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"'`)
		switch k {
		case "ID":
			id = v
		case "ID_LIKE":
			like = v
		}
	}
	return id, like
}

// Check is a dependency as found on the machine.
type Check struct {
	Dependency
	Status  Status
	Program string // the program found, as git or wl-copy; "" when missing
	Path    string // where it is; "" when missing
	Version string // "" when the program has none to tell
	Detail  string // why it is not ready, or what it is missing
}

// checkTimeout bounds each program a check runs.
const checkTimeout = 5 * time.Second

// CheckAll checks every dependency.
func CheckAll(ctx context.Context, p Probe) []Check {
	checks := make([]Check, 0, len(Dependencies))
	for _, d := range Dependencies {
		checks = append(checks, CheckOne(ctx, p, d))
	}
	return checks
}

// CheckOne checks the dependency d.
func CheckOne(ctx context.Context, p Probe, d Dependency) Check {
	var c Check
	switch d.Name {
	case "git":
		c = checkGit(ctx, p)
	case "clipboard":
		c = checkClipboard(p)
	case "container":
		c = checkContainer(ctx, p, "docker", "podman")
	}
	c.Dependency = d
	logging.FromContext(ctx).Info("dependency checked", "name", d.Name, "status", c.Status, "program", c.Program, "version", c.Version, "detail", c.Detail)
	return c
}

// find returns the first of programs on the PATH.
func find(p Probe, programs ...string) (name, path string, ok bool) {
	for _, name := range programs {
		if path, err := p.LookPath(name); err == nil {
			return name, path, true
		}
	}
	return "", "", false
}

// output runs a program for a check, for at most checkTimeout.
func output(ctx context.Context, p Probe, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	b, err := p.Output(ctx, name, args...)
	return strings.TrimSpace(string(b)), err
}

var versionRE = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

// version returns the first version number in s, as 2.43.0.
func version(s string) string { return versionRE.FindString(s) }

// older reports whether version v is before min.
func older(v string, min [3]int) bool {
	var got [3]int
	for i, part := range strings.SplitN(v, ".", 3) {
		got[i], _ = strconv.Atoi(part)
	}
	for i := range got {
		if got[i] != min[i] {
			return got[i] < min[i]
		}
	}
	return false
}

func checkGit(ctx context.Context, p Probe) Check {
	name, path, ok := find(p, "git")
	if !ok {
		return Check{Status: Missing}
	}
	c := Check{Status: Ready, Program: name, Path: path}
	out, err := output(ctx, p, path, "--version")
	if err != nil {
		c.Status, c.Detail = NotReady, fmt.Sprintf("git --version failed: %v", err)
		return c
	}
	c.Version = version(out)
	if older(c.Version, MinGit) {
		c.Status, c.Detail = TooOld, fmt.Sprintf("wdx needs git %d.%d or later", MinGit[0], MinGit[1])
	}
	return c
}

// ClipboardTools are the programs that can set the clipboard in session
// on goos, in the order tide tries them.
func ClipboardTools(goos, session string) []string {
	switch {
	case goos == "darwin":
		return []string{"pbcopy"}
	case session == "wayland":
		return []string{"wl-copy"}
	case session == "x11":
		return []string{"xclip", "xsel"}
	}
	return []string{"wl-copy", "xclip", "xsel"}
}

func checkClipboard(p Probe) Check {
	tools := ClipboardTools(p.GOOS, p.Session)
	name, path, ok := find(p, tools...)
	if ok {
		return Check{Status: Ready, Program: name, Path: path}
	}
	c := Check{Status: Missing, Detail: tools[0] + " is not installed"}
	if len(tools) > 1 {
		c.Detail = "none of " + strings.Join(tools, ", ") + " is installed"
	}
	if p.GOOS == "linux" && p.Session == "" {
		c.Detail += "; no graphical session, so only terminals that take OSC 52 copy"
	}
	return c
}

// CheckInstallable checks the dependency the installable name provides;
// for docker or podman, that program only.
func CheckInstallable(ctx context.Context, p Probe, name string) Check {
	d, _ := DependencyOf(name)
	if name != "docker" && name != "podman" {
		return CheckOne(ctx, p, d)
	}
	c := checkContainer(ctx, p, name)
	c.Dependency = d
	logging.FromContext(ctx).Info("dependency checked", "name", name, "status", c.Status, "version", c.Version, "detail", c.Detail)
	return c
}

func checkContainer(ctx context.Context, p Probe, programs ...string) Check {
	name, path, ok := find(p, programs...)
	if !ok {
		if len(programs) == 1 {
			return Check{Status: Missing, Detail: programs[0] + " is not installed"}
		}
		return Check{Status: Missing, Detail: "neither docker nor podman is installed"}
	}
	c := Check{Status: Ready, Program: name, Path: path}
	if out, err := output(ctx, p, path, "--version"); err == nil {
		c.Version = version(out)
	}
	// docker installed is not enough: its daemon must answer, to this user.
	if out, err := output(ctx, p, path, "info"); err != nil {
		c.Status, c.Detail = NotReady, failure(out, err)
	}
	return c
}

// failure is why a program failed, in one line: the line of its output
// that says so, as docker info prints much before its error.
func failure(out string, err error) string {
	var last string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if strings.Contains(l, "rror") || strings.Contains(l, "denied") || strings.Contains(l, "annot connect") {
			return l
		}
		if l != "" {
			last = l
		}
	}
	if last != "" {
		return last
	}
	return err.Error()
}
