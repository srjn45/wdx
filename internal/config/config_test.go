package config

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/settings"
)

// env is a fake environment.
type env map[string]string

func (e env) getenv(key string) string { return e[key] }

// homeWith returns a home holding a config file with content, and the
// environment that points at it.
func homeWith(t *testing.T, content string) (string, env) {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, env{"WDX_HOME": home}
}

func loadErr(t *testing.T, content string) (*settings.Error, string) {
	t.Helper()
	home, e := homeWith(t, content)
	_, err := Load(e.getenv)
	cfgErr, ok := errors.AsType[*settings.Error](err)
	if !ok {
		t.Fatalf("want *Error, got %v", err)
	}
	return cfgErr, filepath.Join(home, "config.yaml")
}

func TestDefaults(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	c, err := Load(env{"WDX_HOME": home}.getenv)
	if err != nil {
		t.Fatal(err)
	}
	want := Default()
	want.Home, want.From["home"] = home, "WDX_HOME"
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("got %+v, want %+v", c, want)
	}
	d := Default()
	if !strings.HasSuffix(d.Home, "/.wdx") || d.NoInput || d.Color != ColorAuto || d.LogLevel != slog.LevelInfo || d.LogRetentionDays != 7 {
		t.Fatalf("unexpected defaults %+v", d)
	}
	for _, s := range Settings() {
		if d.From[s.Key] != "default" {
			t.Fatalf("%s: want from default, got %q", s.Key, d.From[s.Key])
		}
	}
}

func TestFileSettings(t *testing.T) {
	t.Parallel()
	home, e := homeWith(t, "# mine\nlog:\n  level: DEBUG\n  retention_days: 30\nno_input: true\notel:\n  endpoint: http://localhost:4318\n  interval_seconds: 5\ncolor: Always\nterminal:\n  shell: /bin/zsh\n  history_lines: 500\n  history_tail: 50\neditor: vi\ntide:\n  section_order: cwa\ndocs:\n  git_path: docs/wdx\nplan:\n  parallel: 5\naca:\n  claude_code:\n    enabled: false\ncontext:\n  threshold:\n    warning: 1000\n    critical: 2000\n")
	c, err := Load(e.getenv)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "config.yaml")
	want := Config{
		Home: home, NoInput: true, Color: ColorAlways, LogLevel: slog.LevelDebug, LogRetentionDays: 30,
		OtelEndpoint: "http://localhost:4318", OtelInterval: 5 * time.Second, TerminalShell: "/bin/zsh",
		TerminalHistoryLines: 500, TerminalHistoryTail: 50, Editor: "vi", TideSectionOrder: "cwa", TideTheme: "slate", DocsGitPath: "docs/wdx", PlanParallel: 5, ContextWarning: 1000, ContextCritical: 2000, UpdateChannel: "stable", UpdateBaseURL: "https://github.com/srjn45/wdx/releases/download",
		ACAEnabled: map[string]bool{"claude_code": false, "antigravity": true, "cursor": true, "codex": true},
		From: map[string]string{
			"tide.theme":                 "default",
			"aca.claude_code.enabled":    path + ":23",
			"aca.antigravity.enabled":    "default",
			"aca.cursor.enabled":         "default",
			"aca.codex.enabled":          "default",
			"tide.section_order":         path + ":16",
			"context.threshold.warning":  path + ":26",
			"context.threshold.critical": path + ":27",
			"docs.git_path":              path + ":18",
			"plan.parallel":              path + ":20",
			"update.channel":             "default",
			"update.base_url":            "default",
			"terminal.shell":             path + ":11",
			"terminal.history_lines":     path + ":12",
			"terminal.history_tail":      path + ":13",
			"editor":                     path + ":14",
			"home":                       "WDX_HOME",
			"no_input":                   path + ":5",
			"color":                      path + ":9",
			"log.level":                  path + ":3",
			"log.retention_days":         path + ":4",
			"otel.endpoint":              path + ":7",
			"otel.interval_seconds":      path + ":8",
		},
	}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("got %+v, want %+v", c, want)
	}
	if got := c.Value("log.level"); got != "debug" {
		t.Fatalf("want log.level formatted as debug, got %q", got)
	}
}

func TestEveryProblemReported(t *testing.T) {
	t.Parallel()
	e, path := loadErr(t, strings.Join([]string{
		"no_input: maybe",
		"log:",
		"  level: loud",
		"  retention_days: 0",
		"  colour: red",
		"home: /elsewhere",
		"logs: x",
		"no_input2:",
		"  a: b",
		"otel:",
		"  endpoint: localhost:4318",
		"  interval_seconds: 0.5",
		"color: purple",
		"update:",
		"  channel: beta",
		"  base_url: ftp://example.com",
		"terminal:",
		"  shell: zsh",
		"  history_lines: -1",
		"  history_tail: 20000",
		"editor: micro",
		"tide:",
		"  theme: light",
	}, "\n"))
	want := []string{
		path + `:1: no_input: "maybe" is not true or false`,
		path + `:3: log.level: "loud" is not one of debug, info, warn, error`,
		path + `:4: log.retention_days: "0" is not a whole number of at least 1`,
		path + ":5: log.colour: unknown setting",
		path + ":6: home: set by WDX_HOME, not the config file",
		path + ":7: logs: unknown setting",
		path + ":8: no_input2: unknown setting",
		path + `:11: otel.endpoint: "localhost:4318" is not an http or https URL`,
		path + `:12: otel.interval_seconds: "0.5" is not a whole number of at least 1`,
		path + `:13: color: "purple" is not one of auto, always, never`,
		path + `:15: update.channel: "beta" is not one of stable, dev`,
		path + `:16: update.base_url: "ftp://example.com" is not an http, https or file URL`,
		path + `:18: terminal.shell: "zsh" is not an absolute path`,
		path + `:19: terminal.history_lines: "-1" is not a whole number of at least 0`,
		path + `:20: terminal.history_tail: "20000" is not a whole number from 1 to 10000`,
		path + `:21: editor: "micro" is not one of vi, vim, nvim, nano, hx, emacs`,
		path + `:23: tide.theme: "light" is not one of slate`,
	}
	slices.Sort(want)
	var got []string
	for _, p := range e.Problems {
		got = append(got, p.String())
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	i := slices.IndexFunc(e.Problems, func(p settings.Problem) bool { return p.Key == "log.level" })
	if p := e.Problems[i]; p.Value == nil || *p.Value != "loud" {
		t.Fatalf("unexpected problem %+v", p)
	}
}

func TestSettingsDescribed(t *testing.T) {
	t.Parallel()
	for _, s := range Settings() {
		if s.Description == "" || (s.Required && s.Default != "") || (s.EnvVar != "") != (s.Key == "home") {
			t.Fatalf("incomplete setting %+v", s)
		}
	}
}

// TestTemplate checks the config file Rewrite writes when there is none:
// uncommented, it sets every setting the file can hold to its default.
func TestTemplate(t *testing.T) {
	t.Parallel()
	r, err := Rewrite(filepath.Join(t.TempDir(), "config.yaml"), nil)
	if err != nil {
		t.Fatal(err)
	}
	template := string(r.Content)
	header := "# wdx settings. wdx setting set changes one and applies it at once.\n" +
		"# After editing this file by hand run: wdx daemon restart\n" +
		"# Every setting is optional; uncomment one to change it. See wdx help settings.\n" +
		"# wdx update, wdx setting set and wdx setting write rewrite this file: they\n" +
		"# keep the values set and add new settings, but not comments of your own.\n" +
		"\n# Never prompt, as --no-input does: true or false.\n# no_input: false\n"
	if !strings.HasPrefix(template, header) {
		t.Errorf("want the template to start\n%s\ngot\n%s", header, template)
	}
	uncommented := regexp.MustCompile(`(?m)^# ( *[a-z_]+:.*)$`).ReplaceAllString(template, "$1")
	_, e := homeWith(t, uncommented)
	c, err := Load(e.getenv)
	if err != nil {
		t.Fatalf("the template does not load: %v\n%s", err, uncommented)
	}
	d := Default()
	for _, s := range Settings() {
		if s.EnvVar != "" {
			continue
		}
		if !strings.Contains(c.From[s.Key], "config.yaml:") || c.Value(s.Key) != d.Value(s.Key) {
			t.Errorf("%s: want the template to set its default %q, got %q from %q", s.Key, d.Value(s.Key), c.Value(s.Key), c.From[s.Key])
		}
	}
	home, _ := homeWith(t, "old: 1\n")
	r, err = Rewrite(filepath.Join(home, "config.yaml"), nil)
	if want := "\n\n# No longer read by wdx, kept for reference:\n# old: 1\n"; err != nil || !strings.HasSuffix(string(r.Content), want) {
		t.Errorf("want the template to end %q, got %v\n%s", want, err, r.Content)
	}
}

// A choice the panes offer must be a value the setting takes.
func TestChoicesAreAccepted(t *testing.T) {
	t.Parallel()
	offered := 0
	for _, s := range table.Rows {
		for _, choice := range s.Choices {
			offered++
			c := Default()
			if err := s.Set(&c, choice); err != nil {
				t.Errorf("%s: %v", s.Key, err)
			}
			if got := s.Get(c); got != choice {
				t.Errorf("%s: %q reads as %q", s.Key, choice, got)
			}
		}
	}
	if offered == 0 {
		t.Fatal("no setting offers a choice")
	}
	got := map[string][]string{}
	for _, s := range Settings() {
		if s.Choices != nil {
			got[s.Key] = s.Choices
		}
	}
	want := map[string][]string{
		"no_input": {"true", "false"}, "color": {"auto", "always", "never"},
		"log.level": {"debug", "info", "warn", "error"}, "update.channel": {"stable", "dev"}, "editor": {"vi", "vim", "nvim", "nano", "hx", "emacs"},
		"aca.claude_code.enabled": {"true", "false"}, "aca.antigravity.enabled": {"true", "false"}, "aca.cursor.enabled": {"true", "false"}, "aca.codex.enabled": {"true", "false"},
		"tide.theme": {"slate"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("choices %v", got)
	}
}

func TestSectionOrder(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"awdpc", "awc", "awcd", "dcwa", "d", "c", "wa", "pa", "ap"} {
		if err := checkSectionOrder(v); err != nil {
			t.Errorf("%q: %v", v, err)
		}
	}
	for v, why := range map[string]string{"": "at least one", "aa": "twice", "awx": "none of", "A": "none of", "a w": "none of", "pp": "twice", "p": "no section", "x": "none of"} {
		if err := checkSectionOrder(v); err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("%q: %v, want %q", v, err, why)
		}
	}
	if Default().TideSectionOrder != SectionLetters {
		t.Errorf("default %q", Default().TideSectionOrder)
	}
}

func TestGitPath(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"", "docs", "docs/wdx", "a/../docs", ".github/docs"} {
		if err := checkGitPath(v); err != nil {
			t.Errorf("%q: %v", v, err)
		}
	}
	for v, why := range map[string]string{"/abs": "relative", "..": "leads out", "../docs": "leads out", "a/../../x": "leads out", ".git": "git's own", ".git/docs": "git's own"} {
		if err := checkGitPath(v); err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("%q: %v, want %q", v, err, why)
		}
	}
}

func TestContextThresholds(t *testing.T) {
	t.Parallel()
	if d := Default(); d.ContextWarning != 200_000 || d.ContextCritical != 400_000 {
		t.Fatalf("defaults %d, %d", d.ContextWarning, d.ContextCritical)
	}
	home, e := homeWith(t, "context:\n  threshold:\n    warning: 1000\n    critical: 1001\n")
	if c, err := Load(e.getenv); err != nil || c.ContextWarning != 1000 || c.ContextCritical != 1001 || c.From[contextWarningKey] != filepath.Join(home, "config.yaml")+":3" {
		t.Fatalf("set: %+v %v", c, err)
	}
	for _, tc := range []struct{ content, key, why string }{
		{"context:\n  threshold:\n    warning: 0\n", contextWarningKey, "at least 1"},
		{"context:\n  threshold:\n    critical: -5\n", contextCriticalKey, "at least 1"},
		{"context:\n  threshold:\n    warning: 200k\n", contextWarningKey, "not a whole number"},
		{"context:\n  threshold:\n    warning: 1.5\n", contextWarningKey, "not a whole number"},
		// Not below: equal fails too, and each names the one set.
		{"context:\n  threshold:\n    warning: 400000\n", contextWarningKey, "400000 is not below context.threshold.critical, 400000"},
		{"context:\n  threshold:\n    critical: 100000\n", contextCriticalKey, "100000 is not above context.threshold.warning, 200000"},
		{"context:\n  threshold:\n    warning: 500\n    critical: 300\n", contextCriticalKey, "300 is not above context.threshold.warning, 500"},
	} {
		cfgErr, _ := loadErr(t, tc.content)
		if len(cfgErr.Problems) != 1 || cfgErr.Problems[0].Key != tc.key || !strings.Contains(cfgErr.Problems[0].Reason, tc.why) {
			t.Errorf("%q: %+v, want %s %q", tc.content, cfgErr.Problems, tc.key, tc.why)
		}
	}
}
