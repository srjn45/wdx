package deps

import (
	"errors"
	"strings"
	"testing"
)

func TestPlanFor(t *testing.T) {
	t.Parallel()
	ubuntu := System{OS: "linux", Distribution: "ubuntu", PackageManager: "apt", Session: "wayland"}
	x11 := ubuntu
	x11.Session = "x11"
	console := ubuntu
	console.Session = ""
	mac := System{OS: "darwin", PackageManager: "brew"}
	for _, tc := range []struct {
		name string
		sys  System
		want string // Manual()
		root bool
	}{
		{"git", ubuntu, "sudo apt-get update && sudo apt-get install -y git", true},
		{"clipboard", ubuntu, "sudo apt-get update && sudo apt-get install -y wl-clipboard", true},
		{"clipboard", x11, "sudo apt-get update && sudo apt-get install -y xclip", true},
		{"docker", ubuntu, "sudo apt-get update && sudo apt-get install -y docker.io && sudo usermod -aG docker ann", true},
		{"podman", ubuntu, "sudo apt-get update && sudo apt-get install -y podman", true},
		{"git", mac, "brew install git", false},
		{"clipboard", mac, "", false},
		{"docker", mac, "brew install --cask docker", false},
		{"podman", mac, "brew install podman && podman machine init && podman machine start", false},
	} {
		p, err := PlanFor(tc.name, tc.sys, "ann")
		if err != nil || p.Manual() != tc.want || p.NeedsRoot() != tc.root {
			t.Errorf("%s on %+v: %q (root %v), %v", tc.name, tc.sys, p.Manual(), p.NeedsRoot(), err)
		}
	}
	if p, _ := PlanFor("docker", ubuntu, "ann"); len(p.After) != 1 || !strings.Contains(p.After[0], "log out") {
		t.Errorf("docker's after: %v", p.After)
	}

	for _, tc := range []struct {
		name   string
		sys    System
		reason string
	}{
		{"git", System{OS: "linux", Distribution: "fedora"}, "not on fedora yet"},
		{"git", System{OS: "darwin"}, "Homebrew is not installed"},
		{"clipboard", console, "no graphical session"},
	} {
		p, err := PlanFor(tc.name, tc.sys, "ann")
		e, ok := errors.AsType[*UnsupportedError](err)
		if !ok || !strings.Contains(e.Reason, tc.reason) || p.Instructions == "" || len(p.Commands) != 0 {
			t.Errorf("%s on %+v: %+v, %v", tc.name, tc.sys, p, err)
		}
	}
}

func TestNonInteractive(t *testing.T) {
	t.Parallel()
	p, _ := PlanFor("docker", System{OS: "linux", PackageManager: "apt", Session: "x11"}, "ann")
	q := p.NonInteractive()
	if got := q.Manual(); got != "sudo -n apt-get update && sudo -n apt-get install -y docker.io && sudo -n usermod -aG docker ann" {
		t.Fatalf("got %q", got)
	}
	if p.Commands[0][1] != "apt-get" {
		t.Fatal("NonInteractive changed the plan it was given")
	}
}
