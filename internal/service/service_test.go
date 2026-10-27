package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInstalled(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	for _, goos := range []string{"linux", "darwin", "windows"} {
		if m := Installed(goos, home); m != nil {
			t.Fatalf("%s: want none installed, got %+v", goos, m)
		}
	}

	touch(t, filepath.Join(home, ".config", "systemd", "user", "wdx.service"))
	if m := Installed("linux", home); m == nil || m.Name != "systemd" || strings.Join(m.Start, " ") != "systemctl --user start wdx.service" {
		t.Fatalf("want systemd, got %+v", m)
	}
	if m := Installed("darwin", home); m != nil {
		t.Fatalf("a systemd unit means nothing on macOS, got %+v", m)
	}

	touch(t, filepath.Join(home, "Library", "LaunchAgents", "com.github.srjn45.wdx.plist"))
	m := Installed("darwin", home)
	if m == nil || m.Name != "launchd" || m.Start[0] != "launchctl" || !strings.HasSuffix(m.Stop[3], "/com.github.srjn45.wdx") {
		t.Fatalf("want launchd, got %+v", m)
	}
}

func TestRun(t *testing.T) {
	t.Parallel()
	m := &Manager{Name: "fake"}
	if err := m.Run(context.Background(), []string{"true"}); err != nil {
		t.Fatal(err)
	}
	err := m.Run(context.Background(), []string{"sh", "-c", "echo unit not found >&2; exit 5"})
	if err == nil || !strings.Contains(err.Error(), "exit status 5: unit not found") {
		t.Fatalf("want the failure and its output, got %v", err)
	}
}
