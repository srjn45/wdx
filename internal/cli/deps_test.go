package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/deps"
)

// ubuntu is an Ubuntu machine on Wayland with git 2.43.0, no wl-copy, and
// docker whose daemon refuses this user.
func ubuntu(env *Env) {
	found := map[string]bool{"git": true, "docker": true}
	env.Deps = deps.Probe{
		GOOS:    "linux",
		Session: "wayland",
		LookPath: func(file string) (string, error) {
			if found[file] {
				return "/usr/bin/" + file, nil
			}
			return "", exec.ErrNotFound
		},
		Output: func(_ context.Context, name string, args ...string) ([]byte, error) {
			switch name + " " + strings.Join(args, " ") {
			case "/usr/bin/git --version":
				return []byte("git version 2.43.0\n"), nil
			case "/usr/bin/docker --version":
				return []byte("Docker version 27.0.3, build 7d4bcd8\n"), nil
			}
			return []byte("permission denied while trying to connect to the Docker daemon socket\n"), errors.New("exit status 1")
		},
		ReadFile: func(string) ([]byte, error) { return []byte("ID=ubuntu\nID_LIKE=debian\n"), nil },
	}
}

func TestDepsList(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	v, exit := wdxJSONWith(t, home, ubuntu, "deps", "list")
	if exit != api.ExitOK || v.System == nil || v.System.OS != "linux" || *v.System.Distribution != "ubuntu" || *v.System.PackageManager != "apt" || *v.System.Session != "wayland" {
		t.Fatalf("exit %d, system %+v", exit, v.System)
	}
	want := []struct{ name, status, program string }{
		{"git", "ready", "git"},
		{"clipboard", "missing", ""},
		{"container", "not_ready", "docker"},
	}
	if len(v.Dependencies) != len(want) {
		t.Fatalf("dependencies %+v", v.Dependencies)
	}
	for i, w := range want {
		d := v.Dependencies[i]
		program := ""
		if d.Program != nil {
			program = *d.Program
		}
		if d.Name != w.name || d.Status != w.status || program != w.program || d.Mandatory != (w.name == "git") || len(d.Features) == 0 {
			t.Errorf("%s: got %+v", w.name, d)
		}
	}
	if d := v.Dependencies[1]; d.Detail == nil || *d.Detail != "wl-copy is not installed" {
		t.Errorf("clipboard detail %v", d.Detail)
	}
	if d := v.Dependencies[2]; d.Version == nil || *d.Version != "27.0.3" || d.Detail == nil || !strings.Contains(*d.Detail, "permission denied") {
		t.Errorf("container: %+v", d)
	}
}

func TestDepsListText(t *testing.T) {
	t.Parallel()
	out := wdxWith(t, newHome(t), ubuntu, "deps", "list")
	for _, want := range []string{
		"git        mandatory  ready      git 2.43.0     projects, terminals",
		"clipboard  optional   missing    -              tide copy",
		"clipboard: wl-copy is not installed",
		"container: permission denied",
		"install it: wdx deps install --name docker or --name podman",
		"system: ubuntu, installs with apt, wayland session",
	} {
		if !strings.Contains(out.stdout, want) {
			t.Errorf("no %q in:\n%s", want, out.stdout)
		}
	}
}

// machine is an Ubuntu machine on Wayland whose package manager the tests
// drive: what it installs gets found, and every command it ran is kept.
type machine struct {
	mu          sync.Mutex
	found       map[string]bool
	ran         []string
	noPassword  bool   // sudo -n works
	fails       string // a command containing it fails
	distrib     string
	installedBy map[string]string // package: the program it installs
}

func newMachine(found ...string) *machine {
	m := &machine{found: map[string]bool{}, distrib: "ubuntu", installedBy: map[string]string{
		"git": "git", "wl-clipboard": "wl-copy", "docker.io": "docker", "podman": "podman",
	}}
	for _, f := range found {
		m.found[f] = true
	}
	return m
}

func (m *machine) env(env *Env) {
	env.Deps = deps.Probe{
		GOOS:    "linux",
		Session: "wayland",
		LookPath: func(file string) (string, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.found[file] {
				return "/usr/bin/" + file, nil
			}
			return "", exec.ErrNotFound
		},
		Output: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "sudo" {
				if m.noPassword {
					return nil, nil
				}
				return []byte("sudo: a password is required\n"), errors.New("exit status 1")
			}
			return []byte("version 2.43.0\n"), nil
		},
		ReadFile: func(string) ([]byte, error) { return []byte("ID=" + m.distrib + "\n"), nil },
		Run: func(_ context.Context, args []string, _ io.Reader, out io.Writer) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			line := strings.Join(args, " ")
			m.ran = append(m.ran, line)
			fmt.Fprintf(out, "Reading package lists...\n")
			if m.fails != "" && strings.Contains(line, m.fails) {
				fmt.Fprintf(out, "E: Unable to locate package %s\n", args[len(args)-1])
				return errors.New("exit status 100")
			}
			if strings.Contains(line, "apt-get install") {
				m.found[m.installedBy[args[len(args)-1]]] = true
			}
			return nil
		},
	}
}

func TestDepsInstall(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	m := newMachine("git")
	m.noPassword = true
	v, exit := wdxJSONWith(t, home, m.env, "deps", "install", "--name", "clipboard", "--yes")
	if exit != api.ExitOK || v.Result != "installed" {
		t.Fatalf("exit %d, %+v", exit, v)
	}
	// Without a terminal sudo must not ask.
	if want := []string{"sudo -n apt-get update", "sudo -n apt-get install -y wl-clipboard"}; !slices.Equal(m.ran, want) {
		t.Fatalf("ran %q", m.ran)
	}

	// Ready: nothing runs.
	m.ran = nil
	v, exit = wdxJSONWith(t, home, m.env, "deps", "install", "--name", "clipboard", "--yes")
	if exit != api.ExitOK || v.Result != "ready" || len(m.ran) != 0 {
		t.Fatalf("exit %d, %+v, ran %q", exit, v, m.ran)
	}
}

func TestDepsInstallPrompt(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	m := newMachine()
	out := wdxWith(t, home, func(env *Env) { m.env(env); answering(t, "y")(env) }, "deps", "install", "--name", "git")
	if out.exit != api.ExitOK || !strings.Contains(out.stdout, "git installed") || !strings.Contains(out.stderr, "run them? [y/N]") || !strings.Contains(out.stderr, "sudo apt-get install -y git") {
		t.Fatalf("%+v", out)
	}
	// On a terminal sudo asks for the password itself.
	if want := []string{"sudo apt-get update", "sudo apt-get install -y git"}; !slices.Equal(m.ran, want) {
		t.Fatalf("ran %q", m.ran)
	}

	// --yes on a terminal runs them without asking; sudo still may.
	m = newMachine()
	out = wdxWith(t, home, func(env *Env) { m.env(env); answering(t)(env) }, "deps", "install", "--name", "git", "--yes")
	if out.exit != api.ExitOK || strings.Contains(out.stderr, "run them?") || len(m.ran) != 2 || m.ran[1] != "sudo apt-get install -y git" {
		t.Fatalf("--yes: %+v, ran %q", out, m.ran)
	}

	m = newMachine()
	out = wdxWith(t, home, func(env *Env) { m.env(env); answering(t, "n")(env) }, "deps", "install", "--name", "docker")
	if out.exit != api.ExitRejected || !strings.Contains(out.stderr, "[cancelled]") || !strings.Contains(out.stderr, "install it yourself: sudo apt-get update && sudo apt-get install -y docker.io && sudo usermod -aG docker") || len(m.ran) != 0 {
		t.Fatalf("no: %+v, ran %q", out, m.ran)
	}
}

func TestDepsInstallFails(t *testing.T) {
	t.Parallel()
	home := newHome(t)

	// sudo needs a password and there is no terminal: the commands are given.
	m := newMachine()
	v, exit := wdxJSONWith(t, home, m.env, "deps", "install", "--name", "git", "--yes")
	wantError(t, v, exit, api.ErrorCodePrivilegesRequired, api.ExitRejected)
	if v.Hint == nil || *v.Hint != "install it yourself: sudo apt-get update && sudo apt-get install -y git" || v.Details["install"] == nil || len(m.ran) != 0 {
		t.Fatalf("%+v, ran %q", v, m.ran)
	}

	// The package manager fails: why, its output, and how to do it by hand.
	m = newMachine()
	m.noPassword, m.fails = true, "install -y podman"
	v, exit = wdxJSONWith(t, home, m.env, "deps", "install", "--name", "podman", "--yes")
	wantError(t, v, exit, api.ErrorCodeInstallFailed, api.ExitSystem)
	if !strings.Contains(v.Detail, "sudo -n apt-get install -y podman failed: exit status 100") || !strings.Contains(fmt.Sprint(v.Details["output"]), "Unable to locate package podman") || v.Hint == nil {
		t.Fatalf("%+v", v)
	}

	// A system wdx cannot install on: the instructions.
	m = newMachine()
	m.distrib = "fedora"
	v, exit = wdxJSONWith(t, home, m.env, "deps", "install", "--name", "git", "--yes")
	wantError(t, v, exit, api.ErrorCodeUnsupportedSystem, api.ExitRejected)
	if v.Hint == nil || !strings.Contains(*v.Hint, "package manager") {
		t.Fatalf("%+v", v)
	}

	// --json needs --yes; a name must be one wdx knows.
	for _, args := range [][]string{{"--name", "git"}, {"--name", "vim", "--yes"}} {
		v, exit = wdxJSONWith(t, home, newMachine().env, append([]string{"deps", "install"}, args...)...)
		wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	}
}
