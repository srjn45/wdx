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

func loadErr(t *testing.T, content string) (*Error, string) {
	t.Helper()
	home, e := homeWith(t, content)
	_, err := Load(e.getenv)
	cfgErr, ok := errors.AsType[*Error](err)
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
	home, e := homeWith(t, "# mine\nlog:\n  level: DEBUG\n  retention_days: 30\nno_input: true\notel:\n  endpoint: http://localhost:4318\n  interval_seconds: 5\ncolor: Always\nterminal:\n  shell: /bin/zsh\n  history_lines: 500\n")
	c, err := Load(e.getenv)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "config.yaml")
	want := Config{
		Home: home, NoInput: true, Color: ColorAlways, LogLevel: slog.LevelDebug, LogRetentionDays: 30,
		OtelEndpoint: "http://localhost:4318", OtelInterval: 5 * time.Second, TerminalShell: "/bin/zsh",
		TerminalHistoryLines: 500, UpdateChannel: "stable", UpdateBaseURL: "https://github.com/srjn45/wdx/releases/download",
		From: map[string]string{
			"update.channel":         "default",
			"update.base_url":        "default",
			"terminal.shell":         path + ":11",
			"terminal.history_lines": path + ":12",
			"home":                   "WDX_HOME",
			"no_input":               path + ":5",
			"color":                  path + ":9",
			"log.level":              path + ":3",
			"log.retention_days":     path + ":4",
			"otel.endpoint":          path + ":7",
			"otel.interval_seconds":  path + ":8",
		},
	}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("got %+v, want %+v", c, want)
	}
	if got := c.Value("log.level"); got != "debug" {
		t.Fatalf("want log.level formatted as debug, got %q", got)
	}
}

func TestEmptyAndNullValues(t *testing.T) {
	t.Parallel()
	for _, content := range []string{"", "# nothing\n", "log:\n  level:\n"} {
		_, e := homeWith(t, content)
		c, err := Load(e.getenv)
		if err != nil || c.LogLevel != slog.LevelInfo || c.From["log.level"] != "default" {
			t.Fatalf("%q: want defaults, got %+v, %v", content, c, err)
		}
	}
}

func TestOnlySettingsFromEnvironmentAreRead(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	c, err := Load(env{"WDX_HOME": home, "WDX_LOG_LEVEL": "debug", "WDX_NO_INPUT": "1"}.getenv)
	if err != nil || c.LogLevel != slog.LevelInfo || c.NoInput {
		t.Fatalf("want only WDX_HOME read, got %+v, %v", c, err)
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
	i := slices.IndexFunc(e.Problems, func(p Problem) bool { return p.Key == "log.level" })
	if p := e.Problems[i]; p.Value == nil || *p.Value != "loud" {
		t.Fatalf("unexpected problem %+v", p)
	}
}

func TestNotASingleValue(t *testing.T) {
	t.Parallel()
	for content, want := range map[string]string{
		"log:\n  level: [debug]\n": ":2: log.level: not a single value",
		"log:\n  level: {a: b}\n":  ":2: log.level: not a single value",
		"log: debug\n":             ":1: log: unknown setting",
	} {
		e, path := loadErr(t, content)
		if len(e.Problems) != 1 || e.Problems[0].String() != path+want {
			t.Fatalf("%q: want %s, got %v", content, want, e)
		}
	}
}

func TestBadFile(t *testing.T) {
	t.Parallel()
	for content, want := range map[string]string{
		"log: [":                            ":1: did not find expected node content",
		"- debug\n":                         ":1: not a mapping of settings",
		"no_input: true\nno_input: false\n": ":2: no_input: already set at line 1",
		"log:\n  level: info\nlog:\n  level: x\n": ":3: log: already set at line 1",
	} {
		e, path := loadErr(t, content)
		if len(e.Problems) != 1 || e.Problems[0].String() != path+want {
			t.Fatalf("%q: want %q, got %v", content, want, e)
		}
	}
}

func TestUnreadableFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "config.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := Load(env{"WDX_HOME": home}.getenv)
	if err == nil || !strings.Contains(err.Error(), "config.yaml: cannot be read: ") {
		t.Fatalf("want unreadable file reported, got %v", err)
	}
}

func TestRequiredSetting(t *testing.T) {
	t.Parallel()
	table := slices.Concat(settings, []setting{{
		key:      "token",
		required: true,
		set:      func(*Config, string) error { return nil },
		get:      func(Config) string { return "" },
	}})
	_, e := homeWith(t, "")
	_, err := load(table, e.getenv)
	cfgErr, ok := errors.AsType[*Error](err)
	if !ok || len(cfgErr.Problems) != 1 || cfgErr.Problems[0].Value != nil || err.Error() != "invalid settings: token: required" {
		t.Fatalf("want token required, got %v", err)
	}
	_, e = homeWith(t, "token: x\n")
	if _, err := load(table, e.getenv); err != nil {
		t.Fatalf("want token accepted, got %v", err)
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
	r, err := Rewrite(filepath.Join(t.TempDir(), "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	template := string(r.Content)
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
}
