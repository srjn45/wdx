package deps

import (
	"fmt"
	"strings"
)

// Installable are the names wdx deps install takes: a dependency, or for
// container the program that provides it.
var Installable = []string{"git", "clipboard", "docker", "podman"}

// DependencyOf returns the dependency the installable name provides.
func DependencyOf(name string) (Dependency, bool) {
	if name == "docker" || name == "podman" {
		name = "container"
	}
	for _, d := range Dependencies {
		if d.Name == name {
			return d, true
		}
	}
	return Dependency{}, false
}

// Plan is how to install a dependency on a system.
type Plan struct {
	// Commands run in order; one starting with sudo needs root.
	Commands [][]string
	// After is what is left to do once they ran, as "log in again".
	After []string
	// Instructions say how to install it by hand when wdx cannot.
	Instructions string
}

// NeedsRoot reports whether a command of the plan runs with sudo.
func (p Plan) NeedsRoot() bool {
	for _, c := range p.Commands {
		if len(c) > 0 && c[0] == "sudo" {
			return true
		}
	}
	return false
}

// Manual is how to do it by hand: the commands as one line to paste, or
// the instructions.
func (p Plan) Manual() string {
	if len(p.Commands) == 0 {
		return p.Instructions
	}
	lines := make([]string, len(p.Commands))
	for i, c := range p.Commands {
		lines[i] = strings.Join(c, " ")
	}
	return strings.Join(lines, " && ")
}

// NonInteractive returns the plan with sudo told never to ask for a
// password, for runs without a terminal.
func (p Plan) NonInteractive() Plan {
	q := p
	q.Commands = make([][]string, len(p.Commands))
	for i, c := range p.Commands {
		if len(c) > 0 && c[0] == "sudo" {
			c = append([]string{"sudo", "-n"}, c[1:]...)
		}
		q.Commands[i] = c
	}
	return q
}

// UnsupportedError is returned when wdx cannot install name on this
// system; Plan.Instructions says how to do it by hand.
type UnsupportedError struct {
	Name   string
	Reason string
	Plan   Plan
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("%s cannot be installed here: %s", e.Name, e.Reason)
}

// apt is how apt installs packages: the index refreshed first, as a stale
// one fails to find them.
func apt(packages ...string) [][]string {
	return [][]string{
		{"sudo", "apt-get", "update"},
		append([]string{"sudo", "apt-get", "install", "-y"}, packages...),
	}
}

// PlanFor returns how to install name, one of Installable, on sys, for the
// user named user. A *UnsupportedError says when wdx cannot.
func PlanFor(name string, sys System, user string) (Plan, error) {
	unsupported := func(reason, instructions string) (Plan, error) {
		p := Plan{Instructions: instructions}
		return p, &UnsupportedError{Name: name, Reason: reason, Plan: p}
	}
	if name == "clipboard" && sys.OS == "linux" && sys.Session == "" {
		return unsupported("no graphical session, so no clipboard a program can reach",
			"tide copies through the terminal (OSC 52) in terminals that take it: kitty, WezTerm, iTerm2, foot, Alacritty")
	}
	switch sys.PackageManager {
	case "apt":
		switch name {
		case "git":
			return Plan{Commands: apt("git")}, nil
		case "clipboard":
			if sys.Session == "wayland" {
				return Plan{Commands: apt("wl-clipboard")}, nil
			}
			return Plan{Commands: apt("xclip")}, nil
		case "docker":
			return Plan{
				Commands: append(apt("docker.io"), []string{"sudo", "usermod", "-aG", "docker", user}),
				After:    []string{"log out and in again, so the docker group applies to you"},
			}, nil
		case "podman":
			return Plan{Commands: apt("podman")}, nil
		}
	case "brew":
		switch name {
		case "git":
			return Plan{Commands: [][]string{{"brew", "install", "git"}}}, nil
		case "clipboard":
			return Plan{}, nil // pbcopy comes with macOS
		case "docker":
			return Plan{
				Commands: [][]string{{"brew", "install", "--cask", "docker"}},
				After:    []string{"open Docker once from Applications, so its daemon runs"},
			}, nil
		case "podman":
			return Plan{Commands: [][]string{
				{"brew", "install", "podman"},
				{"podman", "machine", "init"},
				{"podman", "machine", "start"},
			}}, nil
		}
	}
	if sys.OS == "darwin" {
		return unsupported("Homebrew is not installed",
			"install Homebrew from https://brew.sh, then run wdx deps install --name "+name+" again")
	}
	what := map[string]string{
		"git":       "git",
		"clipboard": "wl-clipboard on Wayland, xclip on X11",
		"docker":    "Docker: https://docs.docker.com/engine/install/",
		"podman":    "Podman: https://podman.io/docs/installation",
	}[name]
	distribution := sys.Distribution
	if distribution == "" {
		distribution = "this system"
	}
	return unsupported("wdx installs with apt (Ubuntu, Debian) and brew (macOS), not on "+distribution+" yet",
		"install "+what+" with your system's package manager")
}
