package cli

import (
	"strings"
	"testing"

	"github.com/srjn45/warden/internal/api"
	"github.com/srjn45/warden/internal/config"
)

const esc = "\x1b["

func TestColorAlways(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	newProject(t, home, "app")
	out := wdx(t, home, "project", "list", "--color", "always")
	if out.exit != api.ExitOK || !strings.Contains(out.stdout, "\x1b[1mapp\x1b[0m") || !strings.Contains(out.stdout, "\x1b[1;32mopen\x1b[0m") {
		t.Fatalf("want colored text, got %q", out.stdout)
	}
	out = wdx(t, home, "project", "get", "--name", "nope", "--color", "always")
	if out.exit != api.ExitRejected || !strings.HasPrefix(out.stderr, "\x1b[1;31mwdx:\x1b[0m project not found") || !strings.Contains(out.stderr, "\x1b[1;31m[not_found]\x1b[0m") {
		t.Fatalf("want a colored error, got %q", out.stderr)
	}
}

func TestColorNeverInJSON(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	newProject(t, home, "app")
	for _, args := range [][]string{
		{"project", "list", "--color", "always"},
		{"project", "get", "--name", "nope", "--color", "always"},
	} {
		out := wdx(t, home, append(args, "--json")...)
		if strings.Contains(out.stdout+out.stderr, esc) {
			t.Fatalf("%v: want no color with --json, got %q, %q", args, out.stdout, out.stderr)
		}
	}
}

func TestColorSources(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	for _, tc := range []struct {
		setting config.ColorMode
		noColor bool
		args    []string
		want    bool
	}{
		// The test's streams are not terminals.
		{config.ColorAuto, false, nil, false},
		{config.ColorAlways, false, nil, true},
		{config.ColorAlways, true, nil, true},
		{config.ColorAuto, true, []string{"--color", "always"}, true},
		{config.ColorAlways, false, []string{"--color", "never"}, false},
		{config.ColorNever, false, []string{"--color", "auto"}, false},
	} {
		args := append([]string{"version"}, tc.args...)
		out := wdxWith(t, home, func(env *Env) {
			env.Config.Color = tc.setting
			env.NoColor = tc.noColor
		}, args...)
		if got := strings.Contains(out.stdout, esc); out.exit != api.ExitOK || got != tc.want {
			t.Errorf("color %s, NoColor %v, %v: want colored %v, got %q", tc.setting, tc.noColor, tc.args, tc.want, out.stdout)
		}
	}
}

func TestColorInvalid(t *testing.T) {
	t.Parallel()
	v, exit := wdxJSON(t, t.TempDir(), "version", "--color", "purple")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	if !strings.Contains(v.Detail, `"purple" is not one of auto, always, never`) {
		t.Fatalf("unexpected error %+v", v)
	}
}

func TestUnknownCommandSuggestion(t *testing.T) {
	t.Parallel()
	for args, hint := range map[string]string{
		"projct":         "did you mean wdx project; see 'wdx --help'",
		"project crate":  "did you mean wdx project create; see 'wdx project --help'",
		"daemon stat":    "did you mean wdx daemon start; see 'wdx daemon --help'",
		"project zzzzzz": "see 'wdx project --help'",
	} {
		v, exit := wdxJSON(t, t.TempDir(), strings.Fields(args)...)
		wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
		if v.Hint == nil || *v.Hint != hint {
			t.Errorf("%s: want hint %q, got %+v", args, hint, v)
		}
	}
	out := wdx(t, t.TempDir(), "projct", "--color", "always")
	if !strings.Contains(out.stderr, "\x1b[36mhint:\x1b[0m did you mean \x1b[36mwdx project\x1b[0m; see '\x1b[36mwdx\x1b[0m \x1b[33m--help\x1b[0m'\n") {
		t.Fatalf("want the commands in the hint styled, got %q", out.stderr)
	}
}
