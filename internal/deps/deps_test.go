package deps

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// fakeProbe is a machine with the programs in found, which print what out
// gives for "<name> <args>" (an error when the output starts with "!").
func fakeProbe(goos, session string, found map[string]bool, out map[string]string, osRelease string) Probe {
	return Probe{
		GOOS:    goos,
		Session: session,
		LookPath: func(file string) (string, error) {
			if found[file] {
				return "/usr/bin/" + file, nil
			}
			return "", exec.ErrNotFound
		},
		Output: func(_ context.Context, name string, args ...string) ([]byte, error) {
			o := out[strings.TrimPrefix(name, "/usr/bin/")+" "+strings.Join(args, " ")]
			if rest, failed := strings.CutPrefix(o, "!"); failed {
				return []byte(rest), errors.New("exit status 1")
			}
			return []byte(o), nil
		},
		ReadFile: func(string) ([]byte, error) {
			if osRelease == "" {
				return nil, errors.New("no such file")
			}
			return []byte(osRelease), nil
		},
	}
}

func TestSession(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		environ []string
		want    string
	}{
		{[]string{"XDG_SESSION_TYPE=wayland"}, "wayland"},
		{[]string{"WAYLAND_DISPLAY=wayland-0", "DISPLAY=:0"}, "wayland"},
		{[]string{"DISPLAY=:0"}, "x11"},
		{[]string{"XDG_SESSION_TYPE=x11"}, "x11"},
		{[]string{"XDG_SESSION_TYPE=tty", "TERM=xterm"}, ""},
	} {
		if got := Session(tc.environ); got != tc.want {
			t.Errorf("Session(%v) = %q, want %q", tc.environ, got, tc.want)
		}
	}
}

func TestDetect(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, goos, osRelease string
		brew                  bool
		want                  System
	}{
		{"ubuntu", "linux", "NAME=\"Ubuntu\"\nID=ubuntu\nID_LIKE=debian\n", false, System{OS: "linux", Distribution: "ubuntu", PackageManager: "apt"}},
		{"debian", "linux", "ID=debian\n", false, System{OS: "linux", Distribution: "debian", PackageManager: "apt"}},
		{"mint", "linux", "ID=linuxmint\nID_LIKE=\"ubuntu debian\"\n", false, System{OS: "linux", Distribution: "linuxmint", PackageManager: "apt"}},
		{"fedora", "linux", "ID=fedora\n", false, System{OS: "linux", Distribution: "fedora"}},
		{"no os-release", "linux", "", false, System{OS: "linux"}},
		{"macOS with brew", "darwin", "", true, System{OS: "darwin", PackageManager: "brew"}},
		{"macOS without brew", "darwin", "", false, System{OS: "darwin"}},
	} {
		got := Detect(fakeProbe(tc.goos, "", map[string]bool{"brew": tc.brew}, nil, tc.osRelease))
		if got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func check(t *testing.T, p Probe, name string) Check {
	t.Helper()
	for _, d := range Dependencies {
		if d.Name == name {
			return CheckOne(context.Background(), p, d)
		}
	}
	t.Fatalf("no dependency %s", name)
	return Check{}
}

func TestCheckGit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		found   bool
		out     string
		status  Status
		version string
	}{
		{"missing", false, "", Missing, ""},
		{"ready", true, "git version 2.43.0", Ready, "2.43.0"},
		{"apple's", true, "git version 2.39.3 (Apple Git-145)", Ready, "2.39.3"},
		{"exactly the oldest", true, "git version 2.31.0", Ready, "2.31.0"},
		{"too old", true, "git version 2.30.9", TooOld, "2.30.9"},
		{"broken", true, "!error while loading shared libraries", NotReady, ""},
	} {
		c := check(t, fakeProbe("linux", "", map[string]bool{"git": tc.found}, map[string]string{"git --version": tc.out}, ""), "git")
		if c.Status != tc.status || c.Version != tc.version || !c.Mandatory {
			t.Errorf("%s: got %+v", tc.name, c)
		}
	}
}

func TestCheckClipboard(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, goos, session string
		found               []string
		status              Status
		program, detail     string
	}{
		{"macOS", "darwin", "", []string{"pbcopy"}, Ready, "pbcopy", ""},
		{"wayland", "linux", "wayland", []string{"wl-copy", "xclip"}, Ready, "wl-copy", ""},
		{"wayland without wl-copy", "linux", "wayland", []string{"xclip"}, Missing, "", "wl-copy is not installed"},
		{"x11 with xsel", "linux", "x11", []string{"xsel"}, Ready, "xsel", ""},
		{"x11 without", "linux", "x11", nil, Missing, "", "none of xclip, xsel is installed"},
		{"no session", "linux", "", nil, Missing, "", "no graphical session"},
	} {
		found := map[string]bool{}
		for _, f := range tc.found {
			found[f] = true
		}
		c := check(t, fakeProbe(tc.goos, tc.session, found, nil, ""), "clipboard")
		if c.Status != tc.status || c.Program != tc.program || !strings.Contains(c.Detail, tc.detail) || c.Mandatory {
			t.Errorf("%s: got %+v", tc.name, c)
		}
	}
}

func TestCheckContainer(t *testing.T) {
	t.Parallel()
	denied := "!Client: Docker Engine\n Version: 27.0.3\n\npermission denied while trying to connect to the Docker daemon socket"
	for _, tc := range []struct {
		name            string
		found           []string
		out             map[string]string
		status          Status
		program, detail string
	}{
		{"missing", nil, nil, Missing, "", "neither docker nor podman is installed"},
		{"docker", []string{"docker", "podman"}, map[string]string{"docker --version": "Docker version 27.0.3, build 7d4bcd8"}, Ready, "docker", ""},
		{"docker denied", []string{"docker"}, map[string]string{"docker info": denied}, NotReady, "docker", "permission denied while trying"},
		{"podman", []string{"podman"}, map[string]string{"podman --version": "podman version 4.9.3"}, Ready, "podman", ""},
	} {
		found := map[string]bool{}
		for _, f := range tc.found {
			found[f] = true
		}
		c := check(t, fakeProbe("linux", "", found, tc.out, ""), "container")
		if c.Status != tc.status || c.Program != tc.program || !strings.Contains(c.Detail, tc.detail) {
			t.Errorf("%s: got %+v", tc.name, c)
		}
	}
}

// TestCheckAllHere checks this machine's git, which the tests need anyway.
func TestCheckAllHere(t *testing.T) {
	t.Parallel()
	p := Probe{GOOS: "linux", LookPath: exec.LookPath, Output: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}}
	c := check(t, p, "git")
	if c.Status != Ready || c.Version == "" {
		t.Fatalf("this machine's git: %+v", c)
	}
}
