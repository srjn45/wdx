package cli

import (
	"context"
	"errors"
	"os/exec"
	"strings"
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
		"system: ubuntu, installs with apt, wayland session",
	} {
		if !strings.Contains(out.stdout, want) {
			t.Errorf("no %q in:\n%s", want, out.stdout)
		}
	}
}
