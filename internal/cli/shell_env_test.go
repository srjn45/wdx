package cli

import (
	"slices"
	"testing"
)

func TestShellEnv(t *testing.T) {
	t.Parallel()
	got := ShellEnv([]string{
		"PATH=/usr/bin:/bin",
		"LANG=en_US.UTF-8",
		"LC_ALL=C.UTF-8",
		"SSH_AUTH_SOCK=/run/user/1000/ssh-agent.socket",
		"DISPLAY=",
		"AWS_SECRET_ACCESS_KEY=secret",
		"HOME=/home/someone",
		"WDX_HOME=/elsewhere",
		"MY_PATH=/opt",
		"PATHS=/opt",
	})
	want := []string{
		"PATH=/usr/bin:/bin",
		"LANG=en_US.UTF-8",
		"LC_ALL=C.UTF-8",
		"SSH_AUTH_SOCK=/run/user/1000/ssh-agent.socket",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
