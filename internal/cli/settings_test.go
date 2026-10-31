package cli

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/identity"
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

func TestSettingsWriteSet(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := identity.ConfigPath(home)
	writeFile(t, path, "color: never\nupdate:\n  channel: stable\n")

	v, exit := wdxJSON(t, home, "settings", "write", "--set", "update.channel=dev", "--set", "log.level=debug")
	if exit != api.ExitOK || v.Result != "written" {
		t.Fatalf("want written, got exit %d, %+v", exit, v.Settings)
	}
	got := readFile(t, path)
	for _, want := range []string{"\ncolor: never\n", "  channel: dev\n", "  level: debug\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q, got\n%s", want, got)
		}
	}

	v, exit = wdxJSON(t, home, "settings", "write", "--set", "update.channel=dev")
	if exit != api.ExitOK || v.Result != "unchanged" {
		t.Fatalf("want unchanged once it is set, got exit %d, %+v", exit, v.Settings)
	}
}

func TestSettingsWriteSetInvalid(t *testing.T) {
	t.Parallel()
	for _, set := range []string{"update.channel=nightly", "no.such=1", "home=/tmp/elsewhere", "update.channel", "=dev"} {
		home := t.TempDir()
		path := identity.ConfigPath(home)
		writeFile(t, path, "color: never\n")

		v, exit := wdxJSON(t, home, "settings", "write", "--set", set)
		wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
		if got := readFile(t, path); got != "color: never\n" {
			t.Errorf("--set %s: want the file left alone, got %q", set, got)
		}
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
		{[]string{"version"}, unknown, false},
		{[]string{"settings", "write"}, invalid, false},
		{[]string{"settings", "write"}, errors.New("other"), false},
	} {
		if got := RewritesSettings(tc.args, tc.err); got != tc.want {
			t.Errorf("%v with %v: want %v, got %v", tc.args, tc.err, tc.want, got)
		}
	}
}
