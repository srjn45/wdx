package cli

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/daemon"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/runtest"
)

func TestSettingsWrite(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := identity.ConfigPath(home)
	writeFile(t, path, "log:\n  level: debug\nold: 1\n")

	v, exit := wdxJSON(t, home, "settings", "write")
	if exit != api.ExitOK || v.Result != "written" || v.Settings.Path != path ||
		!slices.Contains(v.Settings.Added, "color") || slices.Contains(v.Settings.Added, "log.level") || !slices.Equal(v.Settings.Dropped, []string{"old"}) {
		t.Fatalf("want written with color added and old dropped, got exit %d, %+v", exit, v.Settings)
	}
	if got := readFile(t, path); !strings.Contains(got, "  level: debug\n") || !strings.HasSuffix(got, "\n# old: 1\n") {
		t.Fatalf("want level kept and old commented at the end, got\n%s", got)
	}

	v, exit = wdxJSON(t, home, "settings", "write")
	if exit != api.ExitOK || v.Result != "unchanged" || len(v.Settings.Added) != 0 || len(v.Settings.Dropped) != 0 {
		t.Fatalf("want unchanged, got exit %d, %+v", exit, v.Settings)
	}
}

func TestSettingsWriteTo(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := identity.ConfigPath(home)
	writeFile(t, path, "color: never\n")
	to := filepath.Join(t.TempDir(), "new", "config.yaml")

	v, exit := wdxJSON(t, home, "settings", "write", "--to", to)
	if exit != api.ExitOK || v.Result != "written" || v.Settings.Path != to {
		t.Fatalf("want written to %s, got exit %d, %+v", to, exit, v.Settings)
	}
	if got := readFile(t, to); !strings.Contains(got, "\ncolor: never\n") {
		t.Fatalf("want color kept, got\n%s", got)
	}
	if got := readFile(t, path); got != "color: never\n" {
		t.Fatalf("want the config file left alone, got %q", got)
	}
}

func TestSettingsWriteInvalid(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := identity.ConfigPath(home)
	writeFile(t, path, "color: loud\n")

	v, exit := wdxJSON(t, home, "settings", "write")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	if v.Details["settings"] == nil {
		t.Fatalf("want the problems in the details, got %+v", v.Details)
	}
	if got := readFile(t, path); got != "color: loud\n" {
		t.Fatalf("want the file left alone, got %q", got)
	}
}

func TestSettingsWriteText(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeFile(t, identity.ConfigPath(home), "old: 1\n")
	out := wdx(t, home, "settings", "write")
	if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "wrote "+identity.ConfigPath(home)+"\n") || !strings.Contains(out.stdout, "dropped: old (commented at the end)") {
		t.Fatalf("want what was written, got %+v", out)
	}
}

func TestRewritesSettings(t *testing.T) {
	t.Parallel()
	unknown := &config.Error{Problems: []config.Problem{{Key: "old", Reason: "unknown setting"}}}
	invalid := &config.Error{Problems: []config.Problem{{Key: "color", Reason: `"loud" is not one of auto, always, never`}}}
	for _, tc := range []struct {
		args []string
		err  error
		want bool
	}{
		{[]string{"settings", "write"}, unknown, true},
		{[]string{"--color", "never", "settings", "write", "--json"}, unknown, true},
		{[]string{"setting", "write"}, unknown, true},
		{[]string{"setting", "set", "--name", "color", "--value", "never"}, unknown, true},
		{[]string{"setting", "set", "--name", "color", "--value", "never"}, invalid, false},
		{[]string{"setting", "get", "--name", "color"}, unknown, false},
		{[]string{"version"}, unknown, false},
		{[]string{"settings", "write"}, invalid, false},
		{[]string{"settings", "write"}, errors.New("other"), false},
	} {
		if got := RewritesSettings(tc.args, tc.err); got != tc.want {
			t.Errorf("%v with %v: want %v, got %v", tc.args, tc.err, tc.want, got)
		}
	}
}

// withFileConfig runs a command on the settings of the config file of home,
// as main loads them.
func withFileConfig(t *testing.T, home string) func(*Env) {
	t.Helper()
	cfg, err := config.LoadHome(home)
	if err != nil {
		t.Fatal(err)
	}
	return func(env *Env) { env.Config = cfg }
}

func TestSettingList(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := identity.ConfigPath(home)
	writeFile(t, path, "log:\n  level: debug\n")

	out := wdxWith(t, home, withFileConfig(t, home), "setting", "list", "--json")
	var v struct {
		Settings []settingValue `json:"settings"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &v); err != nil || out.exit != api.ExitOK {
		t.Fatalf("want the settings, got %+v: %v", out, err)
	}
	if len(v.Settings) != len(config.Settings()) {
		t.Fatalf("want every setting, got %+v", v.Settings)
	}
	byKey := map[string]settingValue{}
	for _, s := range v.Settings {
		byKey[s.Key] = s
	}
	if s := byKey["log.level"]; s.Value != "debug" || s.Default != "info" || s.From != path+":2" || s.Description == "" {
		t.Errorf("want log.level from the file, got %+v", s)
	}
	if s := byKey["color"]; s.Value != "auto" || s.From != "default" {
		t.Errorf("want color by default, got %+v", s)
	}
	if s := byKey["home"]; s.Value != home {
		t.Errorf("want the home shown, got %+v", s)
	}

	text := wdxWith(t, home, withFileConfig(t, home), "setting", "list")
	if text.exit != api.ExitOK || !strings.Contains(text.stdout, "log.level") || !strings.Contains(text.stdout, path+":2") {
		t.Fatalf("want the settings in text, got %+v", text)
	}
}

func TestSettingGet(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeFile(t, identity.ConfigPath(home), "update:\n  channel: dev\n")

	v, exit := wdxJSONWith(t, home, withFileConfig(t, home), "setting", "get", "--name", "update.channel")
	if exit != api.ExitOK || v.Setting == nil || v.Setting.Value != "dev" || v.Setting.Default != "stable" {
		t.Fatalf("want update.channel dev, got exit %d, %+v", exit, v.Setting)
	}

	text := wdxWith(t, home, withFileConfig(t, home), "setting", "get", "--name", "update.channel")
	if text.exit != api.ExitOK || !strings.HasPrefix(text.stdout, "update.channel dev\n") {
		t.Fatalf("want the value on the first line, got %+v", text)
	}

	v, exit = wdxJSON(t, home, "setting", "get", "--name", "no.such")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	if v.Hint == nil || !strings.Contains(*v.Hint, "setting list") {
		t.Fatalf("want a hint at the list, got %+v", v.Hint)
	}
}

func TestSettingSet(t *testing.T) {
	t.Parallel()
	home := t.TempDir() // no daemon serves it
	path := identity.ConfigPath(home)
	writeFile(t, path, "color: never\n")

	v, exit := wdxJSON(t, home, "setting", "set", "--name", "update.channel", "--value", "dev")
	if exit != api.ExitOK || v.Result != "updated" || v.Reload != "not_running" || v.Setting == nil || v.Setting.Value != "dev" || v.Setting.From == "default" {
		t.Fatalf("want updated with no daemon to tell, got exit %d, %+v", exit, v)
	}
	got := readFile(t, path)
	for _, want := range []string{"\ncolor: never\n", "  channel: dev\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q, got\n%s", want, got)
		}
	}

	v, exit = wdxJSON(t, home, "setting", "set", "--name", "update.channel", "--value", "dev")
	if exit != api.ExitOK || v.Result != "unchanged" {
		t.Fatalf("want unchanged once it is set, got exit %d, %+v", exit, v)
	}

	// A value may be empty, as otel.endpoint's turning the metrics off.
	v, exit = wdxJSON(t, home, "setting", "set", "--name", "otel.endpoint", "--value", "")
	if exit != api.ExitOK || v.Setting == nil || v.Setting.Value != "" || !strings.Contains(readFile(t, path), `endpoint: ""`) {
		t.Fatalf("want it empty, got exit %d, %+v\n%s", exit, v.Setting, readFile(t, path))
	}
}

func TestSettingSetCreatesFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	v, exit := wdxJSON(t, home, "setting", "set", "--name", "color", "--value", "always")
	if exit != api.ExitOK || v.Result != "updated" || !strings.Contains(readFile(t, identity.ConfigPath(home)), "\ncolor: always\n") {
		t.Fatalf("want the file written, got exit %d, %+v", exit, v)
	}
}

func TestSettingSetInvalid(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--name", "update.channel", "--value", "nightly"},
		{"--name", "no.such", "--value", "1"},
		{"--name", "home", "--value", "/tmp/elsewhere"},
		{"--name", "color"},
		{"--value", "never"},
	} {
		home := t.TempDir()
		path := identity.ConfigPath(home)
		writeFile(t, path, "color: never\n")

		v, exit := wdxJSON(t, home, append([]string{"setting", "set"}, args...)...)
		wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
		if got := readFile(t, path); got != "color: never\n" {
			t.Errorf("%v: want the file left alone, got %q", args, got)
		}
	}
}

func TestSettingSetMovesUnknownKeys(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := identity.ConfigPath(home)
	writeFile(t, path, "old: 1\n")

	v, exit := wdxJSON(t, home, "setting", "set", "--name", "color", "--value", "never")
	if exit != api.ExitOK || !slices.Equal(v.DroppedSettings, []string{"old"}) {
		t.Fatalf("want old dropped, got exit %d, %+v", exit, v)
	}
	if got := readFile(t, path); !strings.HasSuffix(got, "\n# old: 1\n") {
		t.Fatalf("want old commented at the end, got\n%s", got)
	}
}

func TestSettingSetApplied(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	v, exit := wdxJSON(t, home, "setting", "set", "--name", "color", "--value", "never")
	if exit != api.ExitOK || v.Reload != "applied" {
		t.Fatalf("want applied, got exit %d, %+v", exit, v)
	}
	out := wdx(t, home, "setting", "set", "--name", "color", "--value", "always")
	if out.exit != api.ExitOK || out.stdout != "set color to always\n  the daemon applied it\n" {
		t.Fatalf("want what was set, got %+v", out)
	}
}

// A daemon that does not take the file leaves it saved, with a warning.
func TestSettingSetNotApplied(t *testing.T) {
	t.Parallel()
	home := shortHome(t)
	logs := logging.NewDailyFile(identity.LogsDir(home), 7, time.Now)
	ctx, stop := context.WithCancel(context.Background())
	d, err := daemon.Listen(ctx, daemon.Options{
		Home: home, Log: logging.New(logs, slog.LevelDebug).With("process", "daemon"),
		Executable: wdxBinary, TerminalHistoryLines: 1,
		Reload: func() (daemon.Settings, error) { return daemon.Settings{}, errors.New("cannot read it") },
	})
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- d.Serve(ctx) }()
	t.Cleanup(func() {
		stop()
		if err := <-served; err != nil {
			t.Errorf("daemon: %v", err)
		}
		logs.Close()
	})

	out := wdx(t, home, "setting", "set", "--name", "color", "--value", "never", "--json")
	var v jsonOutput
	if err := json.Unmarshal([]byte(out.stdout), &v); err != nil || out.exit != api.ExitOK || v.Reload != "not_applied" {
		t.Fatalf("want not_applied, got %+v: %v", out, err)
	}
	if !strings.Contains(out.stderr, "cannot read it") || !strings.Contains(out.stderr, "daemon restart") {
		t.Fatalf("want a warning naming the restart, got %q", out.stderr)
	}
	if got := readFile(t, identity.ConfigPath(home)); !strings.Contains(got, "\ncolor: never\n") {
		t.Fatalf("want the value saved, got\n%s", got)
	}
}

// A real daemon, started from the file, takes what is set without restarting.
func TestSettingTakesEffectInDaemon(t *testing.T) {
	t.Parallel()
	home := shortHome(t)
	t.Cleanup(func() {
		removeTerminals(home)
		if pid, _ := daemon.Running(home); pid != 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	v, exit := wdxJSON(t, home, "daemon", "start")
	if exit != api.ExitOK || v.Daemon == nil {
		t.Fatalf("want started, got exit %d, %+v", exit, v)
	}
	pid := v.Daemon.Pid
	newProject(t, home, "app")
	daemonDebugLines := func() int {
		n := 0
		for _, l := range logLines(t, home, nil) {
			if l.Pid == pid && l.Level == "DEBUG" {
				n++
			}
		}
		return n
	}
	if n := daemonDebugLines(); n != 0 {
		t.Fatalf("want the daemon at info as the file says, got %d debug lines", n)
	}

	if v, exit = wdxJSON(t, home, "setting", "set", "--name", "log.level", "--value", "debug"); exit != api.ExitOK || v.Reload != "applied" {
		t.Fatalf("want applied, got exit %d, %+v", exit, v)
	}
	if v, exit = wdxJSON(t, home, "project", "list"); exit != api.ExitOK {
		t.Fatalf("list: exit %d, %+v", exit, v)
	}
	if daemonDebugLines() == 0 {
		t.Fatal("want the daemon logging at debug once log.level is set")
	}

	shell := filepath.Join(t.TempDir(), "shell")
	if err := runtest.WriteExecutable(shell, testShell); err != nil {
		t.Fatal(err)
	}
	if v, exit = wdxJSON(t, home, "setting", "set", "--name", "terminal.shell", "--value", shell); exit != api.ExitOK || v.Reload != "applied" {
		t.Fatalf("want applied, got exit %d, %+v", exit, v)
	}
	if v, exit = wdxJSONWith(t, home, in(t.TempDir()), "terminal", "create", "--project-name", "app", "--name", "t"); exit != api.ExitOK || v.Terminal == nil || v.Terminal.Shell != shell {
		t.Fatalf("want the terminal on %s, got exit %d, %+v", shell, exit, v.Terminal)
	}

	if v, _ = wdxJSON(t, home, "daemon", "status"); v.Pid == nil || *v.Pid != pid {
		t.Fatalf("want the same daemon %d, got %+v", pid, v.Pid)
	}
}

func TestCompleteSettings(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	keys := func(args ...string) []string {
		var list []string
		for _, line := range complete(t, home, args...) {
			key, _, _ := strings.Cut(line, "\t")
			list = append(list, key)
		}
		return list
	}
	got := keys("setting", "get", "--name", "")
	if !slices.Contains(got, "log.level") || !slices.Contains(got, "home") {
		t.Errorf("want every setting for get, got %v", got)
	}
	got = keys("setting", "set", "--name", "")
	if !slices.Contains(got, "log.level") || slices.Contains(got, "home") {
		t.Errorf("want the settings that can be set for set, got %v", got)
	}
}
