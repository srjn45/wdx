package config

import (
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// env is a fake environment.
type env map[string]string

func (e env) getenv(key string) string { return e[key] }

func TestDefaults(t *testing.T) {
	t.Parallel()
	c, err := Load(Env(env{}.getenv))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(c.Home, "/.wdx") || c.NoInput || c.LogLevel != slog.LevelInfo || c.LogRetentionDays != 7 {
		t.Fatalf("unexpected defaults %+v", c)
	}
	for _, s := range Settings() {
		if c.From[s.Key] != "default" {
			t.Fatalf("%s: want from default, got %q", s.Key, c.From[s.Key])
		}
	}
	if d := Default(); !reflect.DeepEqual(d, c) {
		t.Fatalf("Default %+v differs from Load %+v", d, c)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Parallel()
	c, err := Load(Env(env{
		"WDX_HOME":               "/data/wdx",
		"WDX_NO_INPUT":           "1",
		"WDX_LOG_LEVEL":          "DEBUG",
		"WDX_LOG_RETENTION_DAYS": "30",
	}.getenv))
	if err != nil {
		t.Fatal(err)
	}
	want := Config{Home: "/data/wdx", NoInput: true, LogLevel: slog.LevelDebug, LogRetentionDays: 30, From: map[string]string{
		"home":               "WDX_HOME",
		"no_input":           "WDX_NO_INPUT",
		"log.level":          "WDX_LOG_LEVEL",
		"log.retention_days": "WDX_LOG_RETENTION_DAYS",
	}}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("got %+v, want %+v", c, want)
	}
	if got := c.Value("log.level"); got != "debug" {
		t.Fatalf("want log.level formatted as debug, got %q", got)
	}
}

func TestLaterSourceWins(t *testing.T) {
	t.Parallel()
	c, err := Load(Env(env{"WDX_LOG_LEVEL": "warn"}.getenv), Env(env{"WDX_LOG_LEVEL": "error"}.getenv), Env(env{}.getenv))
	if err != nil || c.LogLevel != slog.LevelError {
		t.Fatalf("want error level, got %v, %v", c.LogLevel, err)
	}
}

func TestEveryBadValueReported(t *testing.T) {
	t.Parallel()
	_, err := Load(Env(env{
		"WDX_NO_INPUT":           "maybe",
		"WDX_LOG_LEVEL":          "loud",
		"WDX_LOG_RETENTION_DAYS": "0",
	}.getenv))
	e, ok := errors.AsType[*Error](err)
	if !ok || len(e.Problems) != 3 {
		t.Fatalf("want three problems, got %v", err)
	}
	for _, want := range []string{
		`WDX_NO_INPUT: "maybe" is not true or false`,
		`WDX_LOG_LEVEL: "loud" is not one of debug, info, warn, error`,
		`WDX_LOG_RETENTION_DAYS: "0" is not a whole number of at least 1`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("want %q in %q", want, err)
		}
	}
	if p := e.Problems[1]; p.Key != "log.level" || p.Value == nil || *p.Value != "loud" {
		t.Fatalf("unexpected problem %+v", p)
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
	_, err := load(table, Env(env{}.getenv))
	e, ok := errors.AsType[*Error](err)
	if !ok || len(e.Problems) != 1 || e.Problems[0].Name != "WDX_TOKEN" || e.Problems[0].Value != nil || err.Error() != "invalid settings: WDX_TOKEN: required" {
		t.Fatalf("want token required, got %v", err)
	}
	if _, err := load(table, Env(env{"WDX_TOKEN": "x"}.getenv)); err != nil {
		t.Fatalf("want token accepted, got %v", err)
	}
}

func TestSettingsDescribed(t *testing.T) {
	t.Parallel()
	for _, s := range Settings() {
		if s.Description == "" || s.EnvVar != "WDX_"+strings.ToUpper(strings.ReplaceAll(s.Key, ".", "_")) || (s.Default == "") != s.Required {
			t.Fatalf("incomplete setting %+v", s)
		}
	}
}
