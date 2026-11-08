// Package config holds the settings of wdx, a table of the settings engine.
// They are loaded once when wdx starts and passed down as a value; nothing
// else reads the environment or the config file.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/editor"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/settings"
	"github.com/srjn45/wdx/internal/style"
)

// Config is every setting.
type Config struct {
	Home             string
	NoInput          bool
	Color            ColorMode
	LogLevel         slog.Level
	LogRetentionDays int
	OtelEndpoint     string
	OtelInterval     time.Duration
	UpdateChannel    string
	UpdateBaseURL    string
	TerminalShell    string // "" for the user's login shell
	// TerminalHistoryLines bounds each terminal's history; 0 keeps none.
	TerminalHistoryLines int64
	// TerminalHistoryTail is the history lines terminal history prints by
	// default.
	TerminalHistoryTail int
	// Editor is the editor files are edited with, one of editor.Names.
	Editor string
	// TideSectionOrder orders the sections of tide's left pane, a letter each.
	TideSectionOrder string
	// TideTheme is the theme tide paints in, one of style.ThemeNames.
	TideTheme string
	// ContextWarning and ContextCritical are the tokens in an agent's context from
	// which tide and the cli show its size as a warning, and as critical; warning is
	// below critical.
	ContextWarning, ContextCritical int64
	// DocsGitPath is the directory of each project's repository the daemon keeps
	// its docs written into; "" for none.
	DocsGitPath string
	// PlanParallel is the most tasks each plan runs at once, unless its run says.
	PlanParallel int
	// ACAEnabled says, by the id of each AI coding agent wdx knows, whether new
	// agents may run it.
	ACAEnabled map[string]bool

	// From tells where each setting was taken from, by key: "default", the
	// environment variable, or the config file and line.
	From map[string]string
}

// ColorMode is when text output is colored.
type ColorMode string

const (
	ColorAuto   ColorMode = "auto" // when writing to a terminal
	ColorAlways ColorMode = "always"
	ColorNever  ColorMode = "never"
)

// ColorModes is every ColorMode.
var ColorModes = []ColorMode{ColorAuto, ColorAlways, ColorNever}

// ParseColorMode returns the mode named v.
func ParseColorMode(v string) (ColorMode, error) {
	if m := ColorMode(strings.ToLower(v)); slices.Contains(ColorModes, m) {
		return m, nil
	}
	return "", fmt.Errorf("%q is not one of auto, always, never", v)
}

var logLevels = []string{"debug", "info", "warn", "error"}

// UpdateChannels are the release channels wdx update follows: stable
// releases only, or every release.
var UpdateChannels = []string{"stable", "dev"}

func colorChoices() []string {
	choices := make([]string, 0, len(ColorModes))
	for _, m := range ColorModes {
		choices = append(choices, string(m))
	}
	return choices
}

// table is every setting: wdx's own, then one per AI coding agent.
var table = settings.Table[Config]{
	Rows:      slices.Concat(ownSettings, acaSettings()),
	EnvPrefix: identity.Env(""),
	Check:     thresholdsProblem,
	FileHeader: "# " + identity.Name + " settings. " + identity.Name + " setting set changes one and applies it at once.\n" +
		"# After editing this file by hand run: " + identity.Name + " daemon restart\n" +
		"# Every setting is optional; uncomment one to change it. See " + identity.Name + " help settings.\n" +
		"# " + identity.Name + " update, " + identity.Name + " setting set and " + identity.Name + " setting write rewrite this file: they\n" +
		"# keep the values set and add new settings, but not comments of your own.\n",
	DroppedHeading: "# No longer read by " + identity.Name + ", kept for reference:",
}

var ownSettings = []settings.Row[Config]{
	{
		Key:         "home",
		Description: "directory holding the config file, database, logs and cloned projects",
		// The config file lives in home, so home cannot come from it.
		FromEnv: true,
		Default: defaultHome(),
		Set:     func(c *Config, v string) error { c.Home = v; return nil },
		Get:     func(c Config) string { return c.Home },
	},
	{
		Key:         "no_input",
		Description: "never prompt, as --no-input does: true or false",
		Default:     "false",
		Choices:     []string{"true", "false"},
		Set: func(c *Config, v string) (err error) {
			if c.NoInput, err = strconv.ParseBool(v); err != nil {
				return fmt.Errorf("%q is not true or false", v)
			}
			return nil
		},
		Get: func(c Config) string { return strconv.FormatBool(c.NoInput) },
	},
	{
		Key:         "color",
		Description: "color text output: auto (only on a terminal, unless NO_COLOR is set), always or never",
		Default:     "auto",
		Choices:     colorChoices(),
		Set: func(c *Config, v string) (err error) {
			c.Color, err = ParseColorMode(v)
			return err
		},
		Get: func(c Config) string { return string(c.Color) },
	},
	{
		Key:         "log.level",
		Description: "lowest level logged: " + strings.Join(logLevels, ", "),
		Default:     "info",
		Choices:     logLevels,
		Set: func(c *Config, v string) error {
			if !slices.Contains(logLevels, strings.ToLower(v)) {
				return fmt.Errorf("%q is not one of %s", v, strings.Join(logLevels, ", "))
			}
			return c.LogLevel.UnmarshalText([]byte(v))
		},
		Get: func(c Config) string { return strings.ToLower(c.LogLevel.String()) },
	},
	{
		Key:         "log.retention_days",
		Description: "days of log files kept besides today's",
		Default:     "7",
		Set: func(c *Config, v string) error {
			days, err := strconv.Atoi(v)
			if err != nil || days < 1 {
				return fmt.Errorf("%q is not a whole number of at least 1", v)
			}
			c.LogRetentionDays = days
			return nil
		},
		Get: func(c Config) string { return strconv.Itoa(c.LogRetentionDays) },
	},
	{
		Key:         "otel.endpoint",
		Description: "OpenTelemetry collector the daemon sends its metrics to over OTLP/HTTP, such as the one wdx observe starts; empty sends none",
		Default:     "http://localhost:4318",
		Set: func(c *Config, v string) error {
			if v != "" {
				u, err := url.Parse(v)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					return fmt.Errorf("%q is not an http or https URL", v)
				}
			}
			c.OtelEndpoint = v
			return nil
		},
		Get: func(c Config) string { return c.OtelEndpoint },
	},
	{
		Key:         "otel.interval_seconds",
		Description: "seconds between two sends of the metrics",
		Default:     "15",
		Set: func(c *Config, v string) error {
			seconds, err := strconv.Atoi(v)
			if err != nil || seconds < 1 {
				return fmt.Errorf("%q is not a whole number of at least 1", v)
			}
			c.OtelInterval = time.Duration(seconds) * time.Second
			return nil
		},
		Get: func(c Config) string { return strconv.Itoa(int(c.OtelInterval / time.Second)) },
	},
	{
		Key:         "update.channel",
		Description: "releases wdx update installs: stable, or dev for the newest release of either kind",
		Default:     "stable",
		Choices:     UpdateChannels,
		Set: func(c *Config, v string) error {
			if !slices.Contains(UpdateChannels, v) {
				return fmt.Errorf("%q is not one of %s", v, strings.Join(UpdateChannels, ", "))
			}
			c.UpdateChannel = v
			return nil
		},
		Get: func(c Config) string { return c.UpdateChannel },
	},
	{
		Key:         "update.base_url",
		Description: "where wdx update downloads releases, laid out as GitHub Releases; file://<repo>/dist for local builds",
		Default:     identity.ReleasesURL,
		Set: func(c *Config, v string) error {
			u, err := url.Parse(v)
			if err != nil || !slices.Contains([]string{"http", "https", "file"}, u.Scheme) || (u.Scheme == "file") != (u.Host == "") || (u.Scheme == "file" && u.Path == "") {
				return fmt.Errorf("%q is not an http, https or file URL", v)
			}
			c.UpdateBaseURL = strings.TrimSuffix(v, "/")
			return nil
		},
		Get: func(c Config) string { return c.UpdateBaseURL },
	},
	{
		Key:         "terminal.shell",
		Description: "absolute path of the shell terminals run; empty runs the user's login shell, /bin/sh when the system names none",
		Default:     "",
		Set: func(c *Config, v string) error {
			if v != "" && !filepath.IsAbs(v) {
				return fmt.Errorf("%q is not an absolute path", v)
			}
			c.TerminalShell = v
			return nil
		},
		Get: func(c Config) string { return c.TerminalShell },
	},
	{
		Key:         "terminal.history_lines",
		Description: "lines of each terminal's history kept on disk, which wdx terminal attach scrolls back through; 0 keeps none, since terminal output can hold secrets",
		Default:     "100000",
		Set: func(c *Config, v string) error {
			lines, err := strconv.ParseInt(v, 10, 64)
			if err != nil || lines < 0 {
				return fmt.Errorf("%q is not a whole number of at least 0", v)
			}
			c.TerminalHistoryLines = lines
			return nil
		},
		Get: func(c Config) string { return strconv.FormatInt(c.TerminalHistoryLines, 10) },
	},
	{
		Key:         "terminal.history_tail",
		Description: "lines of a terminal's history wdx terminal history prints when not told how many; at most 10000",
		Default:     "1000",
		Set: func(c *Config, v string) error {
			lines, err := strconv.Atoi(v)
			if err != nil || lines < 1 || lines > 10_000 {
				return fmt.Errorf("%q is not a whole number from 1 to 10000", v)
			}
			c.TerminalHistoryTail = lines
			return nil
		},
		Get: func(c Config) string { return strconv.Itoa(c.TerminalHistoryTail) },
	},
	{
		Key:         "editor",
		Description: "terminal editor TIDE edits a file with: " + strings.Join(editor.Names(), ", "),
		Default:     editor.Default,
		Choices:     editor.Names(),
		Set: func(c *Config, v string) error {
			if _, ok := editor.Lookup(v); !ok {
				return fmt.Errorf("%q is not one of %s", v, strings.Join(editor.Names(), ", "))
			}
			c.Editor = v
			return nil
		},
		Get: func(c Config) string { return c.Editor },
	},
	{
		Key:         "tide.section_order",
		Description: "the sections TIDE's left pane shows, in their order, a letter each: a agents, w worktrees, d docs, p plans, c code; those left out are hidden",
		Default:     SectionLetters,
		Set: func(c *Config, v string) error {
			if err := checkSectionOrder(v); err != nil {
				return err
			}
			c.TideSectionOrder = v
			return nil
		},
		Get: func(c Config) string { return c.TideSectionOrder },
	},
	{
		Key:         "tide.theme",
		Description: "the theme of colors TIDE paints its panes in: slate, for a dark terminal, is the only one so far; with color off TIDE paints none",
		Default:     style.ThemeNames()[0],
		Choices:     style.ThemeNames(),
		Set: func(c *Config, v string) error {
			if !slices.Contains(style.ThemeNames(), v) {
				return fmt.Errorf("%q is not one of %s", v, strings.Join(style.ThemeNames(), ", "))
			}
			c.TideTheme = v
			return nil
		},
		Get: func(c Config) string { return c.TideTheme },
	},
	{
		Key:         contextWarningKey,
		Description: "tokens in an agent's context from which TIDE and wdx agent show its size as a warning, in yellow, and below which in green; a whole number below " + contextCriticalKey,
		Default:     "200000",
		Set:         func(c *Config, v string) error { return setTokens(&c.ContextWarning, v) },
		Get:         func(c Config) string { return strconv.FormatInt(c.ContextWarning, 10) },
	},
	{
		Key:         contextCriticalKey,
		Description: "tokens in an agent's context from which TIDE and wdx agent show its size as critical, in red; a whole number above " + contextWarningKey,
		Default:     "400000",
		Set:         func(c *Config, v string) error { return setTokens(&c.ContextCritical, v) },
		Get:         func(c Config) string { return strconv.FormatInt(c.ContextCritical, 10) },
	},
	{
		Key:         "docs.git_path",
		Description: "directory of each project's repository, relative to its main checkout, that the daemon keeps the project's docs written into, read-only, on every change, as wdx doc sync-to-git does once; empty for none",
		Default:     "",
		Set: func(c *Config, v string) error {
			if err := checkGitPath(v); err != nil {
				return err
			}
			c.DocsGitPath = v
			return nil
		},
		Get: func(c Config) string { return c.DocsGitPath },
	},
	{
		Key:         "plan.parallel",
		Description: "the most tasks each plan runs at once, each an agent in a worktree of its own, unless wdx plan run --parallel says otherwise; from 1 to 20",
		Default:     "3",
		Set: func(c *Config, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 20 {
				return fmt.Errorf("%q is not a whole number from 1 to 20", v)
			}
			c.PlanParallel = n
			return nil
		},
		Get: func(c Config) string { return strconv.Itoa(c.PlanParallel) },
	},
}

// acaSettings are a setting for each AI coding agent wdx knows, made from its list,
// so that adding one adds its setting: aca.<id>.enabled.
func acaSettings() []settings.Row[Config] {
	var rows []settings.Row[Config]
	for _, id := range aca.IDs() {
		rows = append(rows, settings.Row[Config]{
			Key:         "aca." + id + ".enabled",
			Description: "whether new agents may run " + id + ", when it is installed: true or false",
			Default:     "true",
			Choices:     []string{"true", "false"},
			Set: func(c *Config, v string) error {
				enabled, err := strconv.ParseBool(v)
				if err != nil {
					return fmt.Errorf("%q is not true or false", v)
				}
				c.ACAEnabled = maps.Clone(c.ACAEnabled)
				if c.ACAEnabled == nil {
					c.ACAEnabled = map[string]bool{}
				}
				c.ACAEnabled[id] = enabled
				return nil
			},
			Get: func(c Config) string { return strconv.FormatBool(c.ACAEnabled[id]) },
		})
	}
	return rows
}

// checkGitPath fails for a docs.git_path that is not a relative path inside the
// repository and outside .git, as far as the path alone says; the daemon checks
// the rest, as a symlink leading out, when it writes.
func checkGitPath(v string) error {
	clean := filepath.Clean(v)
	switch {
	case v == "":
		return nil
	case filepath.IsAbs(v):
		return fmt.Errorf("%q must be relative to the repository", v)
	case clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)):
		return fmt.Errorf("%q leads out of the repository", v)
	case clean == ".git" || strings.HasPrefix(clean, ".git"+string(filepath.Separator)):
		return fmt.Errorf("%q is in git's own directory", v)
	}
	return nil
}

const (
	contextWarningKey  = "context.threshold.warning"
	contextCriticalKey = "context.threshold.critical"
)

// setTokens sets tokens to v, a whole number of at least 1.
func setTokens(tokens *int64, v string) error {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 1 {
		return fmt.Errorf("%q is not a whole number of at least 1", v)
	}
	*tokens = n
	return nil
}

// thresholdsProblem is what is wrong with the context thresholds of c together,
// each valid alone, given where each was taken from: warning not below critical.
// It names the one set, critical when both are.
func thresholdsProblem(c Config, from map[string]string) []settings.Problem {
	_, warningSet := from[contextWarningKey]
	_, criticalSet := from[contextCriticalKey]
	if !warningSet || !criticalSet || c.ContextWarning < c.ContextCritical {
		return nil
	}
	key, value, reason := contextCriticalKey, c.ContextCritical, fmt.Sprintf("%d is not above %s, %d", c.ContextCritical, contextWarningKey, c.ContextWarning)
	if from[contextCriticalKey] == "default" {
		key, value, reason = contextWarningKey, c.ContextWarning, fmt.Sprintf("%d is not below %s, %d", c.ContextWarning, contextCriticalKey, c.ContextCritical)
	}
	shown := strconv.FormatInt(value, 10)
	return []settings.Problem{{Key: key, From: from[key], Value: &shown, Reason: reason}}
}

// SectionLetters are the letters tide.section_order takes, in their default order.
const SectionLetters = "awdpc"

// sectionNames name the letters in tide.section_order's errors.
const sectionNames = "a (agents), w (worktrees), d (docs), p (plans), c (code)"

// checkSectionOrder refuses an order that is empty, has a letter twice or one that
// names no section, or names only plans, which tide does not show yet. It is not a
// list of choices: five sections make hundreds of orders.
func checkSectionOrder(v string) error {
	if v == "" {
		return fmt.Errorf("give at least one of %s", sectionNames)
	}
	seen := map[rune]bool{}
	for _, r := range v {
		switch {
		case !strings.ContainsRune(SectionLetters, r):
			return fmt.Errorf("%q has %q, which is none of %s", v, r, sectionNames)
		case seen[r]:
			return fmt.Errorf("%q has %q twice", v, r)
		}
		seen[r] = true
	}
	if v == "p" {
		return fmt.Errorf("%q shows no section until plans come: add another of %s", v, sectionNames)
	}
	return nil
}

func defaultHome() string {
	home, err := os.UserHomeDir() //nolint:forbidigo // the default in the settings table, which help and Default show

	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, "."+identity.Name)
}

// Default returns the defaults of the optional settings; required ones are
// left zero.
func Default() Config {
	c, from, _ := table.Resolve(nil)
	c.From = from
	return c
}

// Load returns the settings: home from $WDX_HOME, the others from the config
// file in home, each defaulting when not set. A missing file sets nothing. It
// fails with a *settings.Error listing every problem: a file that cannot be
// read or parsed, an unknown key, a required setting missing, a value that
// does not parse.
func Load(getenv func(string) string) (Config, error) {
	env := table.Env(getenv)
	home, _, ok := env.Lookup("home")
	if !ok {
		home = defaultHome()
	}
	file, problems := table.File(identity.ConfigPath(home))
	c, from, err := table.Resolve([]settings.Source{file, env})
	c.From = from
	if e, ok := errors.AsType[*settings.Error](err); ok {
		problems = append(problems, e.Problems...)
	}
	if len(problems) > 0 {
		return c, &settings.Error{Problems: problems}
	}
	return c, nil
}

// LoadHome is Load for a home already known, as when the settings are read
// again after wdx started: home is the one setting of the environment.
func LoadHome(home string) (Config, error) {
	return Load(func(name string) string {
		if name == identity.Env("HOME") {
			return home
		}
		return ""
	})
}

// Settings describes every setting.
func Settings() []settings.Setting { return table.Describe() }

// Value returns the setting key formatted as a source would give it.
func (c Config) Value(key string) string { return table.Value(c, key) }
