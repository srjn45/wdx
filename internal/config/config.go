// Package config holds the settings of wdx. They are loaded once when wdx
// starts and passed down as a value; nothing else reads the environment or
// the config file.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/editor"
	"github.com/srjn45/wdx/internal/identity"
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

// Source supplies raw setting values.
type Source interface {
	// Lookup returns the value of key and where it was found; ok is false
	// when the source does not set key.
	Lookup(key string) (value, from string, ok bool)
}

// envSource reads the settings that live in the environment, key "home"
// from $WDX_HOME. An empty variable is not set.
type envSource struct {
	getenv func(string) string
	table  []setting
}

func (e envSource) Lookup(key string) (string, string, bool) {
	i := slices.IndexFunc(e.table, func(s setting) bool { return s.key == key })
	if i < 0 || !e.table[i].fromEnv {
		return "", "", false
	}
	name := envVar(key)
	v := e.getenv(name)
	return v, name, v != ""
}

func envVar(key string) string {
	return identity.Env(strings.ToUpper(strings.ReplaceAll(key, ".", "_")))
}

// setting is one row of the table every setting is defined by.
type setting struct {
	key         string
	description string
	fromEnv     bool     // read from the environment instead of the config file
	required    bool     // has no default; Load fails when no source sets it
	def         string   // parsed by set like any other value
	choices     []string // the values to choose among, which set accepts; nil when it takes any
	set         func(c *Config, value string) error
	get         func(c Config) string
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

// settings is every setting: wdx's own, then one per AI coding agent.
var settings = slices.Concat(ownSettings, acaSettings())

var ownSettings = []setting{
	{
		key:         "home",
		description: "directory holding the config file, database, logs and cloned projects",
		// The config file lives in home, so home cannot come from it.
		fromEnv: true,
		def:     defaultHome(),
		set:     func(c *Config, v string) error { c.Home = v; return nil },
		get:     func(c Config) string { return c.Home },
	},
	{
		key:         "no_input",
		description: "never prompt, as --no-input does: true or false",
		def:         "false",
		choices:     []string{"true", "false"},
		set: func(c *Config, v string) (err error) {
			if c.NoInput, err = strconv.ParseBool(v); err != nil {
				return fmt.Errorf("%q is not true or false", v)
			}
			return nil
		},
		get: func(c Config) string { return strconv.FormatBool(c.NoInput) },
	},
	{
		key:         "color",
		description: "color text output: auto (only on a terminal, unless NO_COLOR is set), always or never",
		def:         "auto",
		choices:     colorChoices(),
		set: func(c *Config, v string) (err error) {
			c.Color, err = ParseColorMode(v)
			return err
		},
		get: func(c Config) string { return string(c.Color) },
	},
	{
		key:         "log.level",
		description: "lowest level logged: " + strings.Join(logLevels, ", "),
		def:         "info",
		choices:     logLevels,
		set: func(c *Config, v string) error {
			if !slices.Contains(logLevels, strings.ToLower(v)) {
				return fmt.Errorf("%q is not one of %s", v, strings.Join(logLevels, ", "))
			}
			return c.LogLevel.UnmarshalText([]byte(v))
		},
		get: func(c Config) string { return strings.ToLower(c.LogLevel.String()) },
	},
	{
		key:         "log.retention_days",
		description: "days of log files kept besides today's",
		def:         "7",
		set: func(c *Config, v string) error {
			days, err := strconv.Atoi(v)
			if err != nil || days < 1 {
				return fmt.Errorf("%q is not a whole number of at least 1", v)
			}
			c.LogRetentionDays = days
			return nil
		},
		get: func(c Config) string { return strconv.Itoa(c.LogRetentionDays) },
	},
	{
		key:         "otel.endpoint",
		description: "OpenTelemetry collector the daemon sends its metrics to over OTLP/HTTP, such as the one wdx observe starts; empty sends none",
		def:         "http://localhost:4318",
		set: func(c *Config, v string) error {
			if v != "" {
				u, err := url.Parse(v)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					return fmt.Errorf("%q is not an http or https URL", v)
				}
			}
			c.OtelEndpoint = v
			return nil
		},
		get: func(c Config) string { return c.OtelEndpoint },
	},
	{
		key:         "otel.interval_seconds",
		description: "seconds between two sends of the metrics",
		def:         "15",
		set: func(c *Config, v string) error {
			seconds, err := strconv.Atoi(v)
			if err != nil || seconds < 1 {
				return fmt.Errorf("%q is not a whole number of at least 1", v)
			}
			c.OtelInterval = time.Duration(seconds) * time.Second
			return nil
		},
		get: func(c Config) string { return strconv.Itoa(int(c.OtelInterval / time.Second)) },
	},
	{
		key:         "update.channel",
		description: "releases wdx update installs: stable, or dev for the newest release of either kind",
		def:         "stable",
		choices:     UpdateChannels,
		set: func(c *Config, v string) error {
			if !slices.Contains(UpdateChannels, v) {
				return fmt.Errorf("%q is not one of %s", v, strings.Join(UpdateChannels, ", "))
			}
			c.UpdateChannel = v
			return nil
		},
		get: func(c Config) string { return c.UpdateChannel },
	},
	{
		key:         "update.base_url",
		description: "where wdx update downloads releases, laid out as GitHub Releases; file://<repo>/dist for local builds",
		def:         identity.ReleasesURL,
		set: func(c *Config, v string) error {
			u, err := url.Parse(v)
			if err != nil || !slices.Contains([]string{"http", "https", "file"}, u.Scheme) || (u.Scheme == "file") != (u.Host == "") || (u.Scheme == "file" && u.Path == "") {
				return fmt.Errorf("%q is not an http, https or file URL", v)
			}
			c.UpdateBaseURL = strings.TrimSuffix(v, "/")
			return nil
		},
		get: func(c Config) string { return c.UpdateBaseURL },
	},
	{
		key:         "terminal.shell",
		description: "absolute path of the shell terminals run; empty runs the user's login shell, /bin/sh when the system names none",
		def:         "",
		set: func(c *Config, v string) error {
			if v != "" && !filepath.IsAbs(v) {
				return fmt.Errorf("%q is not an absolute path", v)
			}
			c.TerminalShell = v
			return nil
		},
		get: func(c Config) string { return c.TerminalShell },
	},
	{
		key:         "terminal.history_lines",
		description: "lines of each terminal's history kept on disk, which wdx terminal attach scrolls back through; 0 keeps none, since terminal output can hold secrets",
		def:         "100000",
		set: func(c *Config, v string) error {
			lines, err := strconv.ParseInt(v, 10, 64)
			if err != nil || lines < 0 {
				return fmt.Errorf("%q is not a whole number of at least 0", v)
			}
			c.TerminalHistoryLines = lines
			return nil
		},
		get: func(c Config) string { return strconv.FormatInt(c.TerminalHistoryLines, 10) },
	},
	{
		key:         "terminal.history_tail",
		description: "lines of a terminal's history wdx terminal history prints when not told how many; at most 10000",
		def:         "1000",
		set: func(c *Config, v string) error {
			lines, err := strconv.Atoi(v)
			if err != nil || lines < 1 || lines > 10_000 {
				return fmt.Errorf("%q is not a whole number from 1 to 10000", v)
			}
			c.TerminalHistoryTail = lines
			return nil
		},
		get: func(c Config) string { return strconv.Itoa(c.TerminalHistoryTail) },
	},
	{
		key:         "editor",
		description: "terminal editor tide edits a file with: " + strings.Join(editor.Names(), ", "),
		def:         editor.Default,
		choices:     editor.Names(),
		set: func(c *Config, v string) error {
			if _, ok := editor.Lookup(v); !ok {
				return fmt.Errorf("%q is not one of %s", v, strings.Join(editor.Names(), ", "))
			}
			c.Editor = v
			return nil
		},
		get: func(c Config) string { return c.Editor },
	},
	{
		key:         "tide.section_order",
		description: "the sections tide's left pane shows, in their order, a letter each: a agents, w worktrees, d docs, p plans, c code; those left out are hidden",
		def:         SectionLetters,
		set: func(c *Config, v string) error {
			if err := checkSectionOrder(v); err != nil {
				return err
			}
			c.TideSectionOrder = v
			return nil
		},
		get: func(c Config) string { return c.TideSectionOrder },
	},
	{
		key:         contextWarningKey,
		description: "tokens in an agent's context from which tide and wdx agent show its size as a warning, in yellow, and below which in green; a whole number below " + contextCriticalKey,
		def:         "200000",
		set:         func(c *Config, v string) error { return setTokens(&c.ContextWarning, v) },
		get:         func(c Config) string { return strconv.FormatInt(c.ContextWarning, 10) },
	},
	{
		key:         contextCriticalKey,
		description: "tokens in an agent's context from which tide and wdx agent show its size as critical, in red; a whole number above " + contextWarningKey,
		def:         "400000",
		set:         func(c *Config, v string) error { return setTokens(&c.ContextCritical, v) },
		get:         func(c Config) string { return strconv.FormatInt(c.ContextCritical, 10) },
	},
	{
		key:         "docs.git_path",
		description: "directory of each project's repository, relative to its main checkout, that the daemon keeps the project's docs written into, read-only, on every change, as wdx doc sync-to-git does once; empty for none",
		def:         "",
		set: func(c *Config, v string) error {
			if err := checkGitPath(v); err != nil {
				return err
			}
			c.DocsGitPath = v
			return nil
		},
		get: func(c Config) string { return c.DocsGitPath },
	},
	{
		key:         "plan.parallel",
		description: "the most tasks each plan runs at once, each an agent in a worktree of its own, unless wdx plan run --parallel says otherwise; from 1 to 20",
		def:         "3",
		set: func(c *Config, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 20 {
				return fmt.Errorf("%q is not a whole number from 1 to 20", v)
			}
			c.PlanParallel = n
			return nil
		},
		get: func(c Config) string { return strconv.Itoa(c.PlanParallel) },
	},
}

// acaSettings are a setting for each AI coding agent wdx knows, made from its list,
// so that adding one adds its setting: aca.<id>.enabled.
func acaSettings() []setting {
	var rows []setting
	for _, id := range aca.IDs() {
		rows = append(rows, setting{
			key:         "aca." + id + ".enabled",
			description: "whether new agents may run " + id + ", when it is installed: true or false",
			def:         "true",
			choices:     []string{"true", "false"},
			set: func(c *Config, v string) error {
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
			get: func(c Config) string { return strconv.FormatBool(c.ACAEnabled[id]) },
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
// each valid alone: warning not below critical. It names the one set, critical
// when both are.
func thresholdsProblem(c Config) *Problem {
	_, warningSet := c.From[contextWarningKey]
	_, criticalSet := c.From[contextCriticalKey]
	if !warningSet || !criticalSet || c.ContextWarning < c.ContextCritical {
		return nil
	}
	key, reason := contextCriticalKey, fmt.Sprintf("%d is not above %s, %d", c.ContextCritical, contextWarningKey, c.ContextWarning)
	if c.From[contextCriticalKey] == "default" {
		key, reason = contextWarningKey, fmt.Sprintf("%d is not below %s, %d", c.ContextWarning, contextCriticalKey, c.ContextCritical)
	}
	value := c.Value(key)
	return &Problem{Key: key, From: c.From[key], Value: &value, Reason: reason}
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
	c, _ := resolve(settings, nil)
	return c
}

// Load returns the settings: home from $WDX_HOME, the others from the config
// file in home, each defaulting when not set. A missing file sets nothing. It
// fails with an *Error listing every problem: a file that cannot be read or
// parsed, an unknown key, a required setting missing, a value that does not
// parse.
func Load(getenv func(string) string) (Config, error) {
	return load(settings, getenv)
}

// LoadHome is Load for a home already known, as when the settings are read
// again after wdx started: home is the one setting of the environment.
func LoadHome(home string) (Config, error) {
	return load(settings, func(name string) string {
		if name == envVar("home") {
			return home
		}
		return ""
	})
}

func load(table []setting, getenv func(string) string) (Config, error) {
	env := envSource{getenv, table}
	home, _, ok := env.Lookup("home")
	if !ok {
		home = defaultHome()
	}
	file, problems := readFile(identity.ConfigPath(home), table)
	c, err := resolve(table, []Source{file, env})
	if e, ok := errors.AsType[*Error](err); ok {
		problems = append(problems, e.Problems...)
	}
	if len(problems) > 0 {
		return c, &Error{Problems: problems}
	}
	return c, nil
}

// resolve returns the settings from the defaults overridden by each source
// in turn, so a later source wins.
func resolve(table []setting, sources []Source) (Config, error) {
	c := Config{From: map[string]string{}}
	var problems []Problem
	for _, s := range table {
		value, from, found := s.def, "default", !s.required
		for _, src := range sources {
			if v, f, ok := src.Lookup(s.key); ok {
				value, from, found = v, f, true
			}
		}
		if !found {
			problems = append(problems, Problem{Key: s.key, Reason: "required"})
			continue
		}
		if err := s.set(&c, value); err != nil {
			if from == "default" {
				panic(fmt.Sprintf("config: default of %s: %v", s.key, err))
			}
			problems = append(problems, Problem{Key: s.key, From: from, Value: &value, Reason: err.Error()})
			continue
		}
		c.From[s.key] = from
	}
	if p := thresholdsProblem(c); p != nil {
		problems = append(problems, *p)
		delete(c.From, p.Key)
	}
	if len(problems) > 0 {
		return c, &Error{Problems: problems}
	}
	return c, nil
}

// fileSource holds the values of a config file by key, with where each was
// found.
type fileSource map[string]fileValue

type fileValue struct {
	value, from string
}

func (f fileSource) Lookup(key string) (string, string, bool) {
	v, ok := f[key]
	return v.value, v.from, ok
}

// unknownSetting is the reason of a problem with a key no setting has.
const unknownSetting = "unknown setting"

// readFile reads the config file at path, a YAML mapping whose nested keys
// make the dotted keys of table: "log: {level: debug}" sets log.level.
func readFile(path string, table []setting) (fileSource, []Problem) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		if pe, ok := errors.AsType[*fs.PathError](err); ok {
			err = pe.Err
		}
		return nil, []Problem{{From: path, Reason: "cannot be read: " + err.Error()}}
	}
	r, problems := parseFile(path, data, table)
	return r.values, problems
}

// parseFile reads data, the config file at path.
func parseFile(path string, data []byte, table []setting) (*fileReader, []Problem) {
	r := &fileReader{path: path, table: table, values: fileSource{}, nodes: map[string]*yaml.Node{}, lines: map[string]int{}}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return r, yamlProblems(path, err)
	}
	if len(doc.Content) == 0 {
		return r, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return r, []Problem{{From: at(path, root), Reason: "not a mapping of settings"}}
	}
	r.walk("", root)
	return r, r.problems
}

// fileReader collects the values of a config file and its problems.
type fileReader struct {
	path     string
	table    []setting
	values   fileSource
	nodes    map[string]*yaml.Node // the node of each value, as written
	unknown  []unknownKey          // the keys no setting has, nested ones flattened
	lines    map[string]int        // where each key was first seen
	problems []Problem
}

// unknownKey is a key of a config file that no setting has, with its value
// written as YAML on one line.
type unknownKey struct {
	key, value string
}

// addUnknown records key and the keys under it.
func (r *fileReader) addUnknown(key string, v *yaml.Node) {
	if v.Kind != yaml.MappingNode {
		r.unknown = append(r.unknown, unknownKey{key, flow(v)})
		return
	}
	for i := 0; i+1 < len(v.Content); i += 2 {
		r.addUnknown(key+"."+v.Content[i].Value, v.Content[i+1])
	}
}

func (r *fileReader) walk(prefix string, m *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		key := prefix + k.Value
		from := at(r.path, k)
		j := slices.IndexFunc(r.table, func(s setting) bool { return s.key == key })
		if line, ok := r.lines[key]; ok {
			r.problem(key, from, fmt.Sprintf("already set at line %d", line))
			continue
		}
		r.lines[key] = k.Line
		switch {
		case j >= 0 && r.table[j].fromEnv:
			r.problem(key, from, "set by "+envVar(key)+", not the config file")
		case j >= 0 && v.Kind == yaml.ScalarNode:
			if v.Tag != "!!null" {
				r.values[key] = fileValue{v.Value, from}
				r.nodes[key] = v
			}
		case j >= 0:
			r.problem(key, from, "not a single value")
		case v.Kind == yaml.MappingNode && slices.ContainsFunc(r.table, func(s setting) bool { return strings.HasPrefix(s.key, key+".") }):
			r.walk(key+".", v)
		default:
			r.problem(key, from, unknownSetting)
			r.addUnknown(key, v)
		}
	}
}

func (r *fileReader) problem(key, from, reason string) {
	r.problems = append(r.problems, Problem{Key: key, From: from, Reason: reason})
}

var yamlLine = regexp.MustCompile(`^line (\d+): `)

// yamlProblems turns a YAML error, "yaml: line 3: ...", into problems found
// at path:3.
func yamlProblems(path string, err error) []Problem {
	msgs := []string{strings.TrimPrefix(err.Error(), "yaml: ")}
	if te, ok := errors.AsType[*yaml.TypeError](err); ok {
		msgs = te.Errors
	}
	var problems []Problem
	for _, msg := range msgs {
		p := Problem{From: path, Reason: msg}
		if m := yamlLine.FindStringSubmatch(msg); m != nil {
			p.From, p.Reason = path+":"+m[1], msg[len(m[0]):]
		}
		problems = append(problems, p)
	}
	return problems
}

func at(path string, n *yaml.Node) string {
	return fmt.Sprintf("%s:%d", path, n.Line)
}

// Error lists every setting that is missing or invalid.
type Error struct {
	Problems []Problem
}

// Problem is a setting that is missing or invalid, or a config file that
// cannot be used.
type Problem struct {
	Key    string  `json:"key"`   // "" for the whole file
	From   string  `json:"from"`  // the variable or file and line; "" when missing
	Value  *string `json:"value"` // nil when missing or not a single value
	Reason string  `json:"reason"`
}

func (p Problem) String() string {
	parts := []string{p.From, p.Key, p.Reason}
	if p.From == envVar(p.Key) {
		parts[1] = "" // the variable names the key
	}
	return strings.Join(slices.DeleteFunc(parts, func(s string) bool { return s == "" }), ": ")
}

// OnlyUnknown is true when every problem is a key no setting has, which
// Rewrite moves out of the way.
func (e *Error) OnlyUnknown() bool {
	return !slices.ContainsFunc(e.Problems, func(p Problem) bool { return p.Reason != unknownSetting })
}

func (e *Error) Error() string {
	var msgs []string
	for _, p := range e.Problems {
		msgs = append(msgs, p.String())
	}
	return "invalid settings: " + strings.Join(msgs, "; ")
}

// Setting describes a setting for help and logs.
type Setting struct {
	Key         string
	EnvVar      string // the variable it is read from; "" for the config file
	Description string
	Default     string // "" when required or empty
	Required    bool
	Choices     []string // the values to choose among; nil when it takes any
}

// Settings describes every setting.
func Settings() []Setting {
	list := make([]Setting, 0, len(settings))
	for _, s := range settings {
		v := Setting{Key: s.key, Description: s.description, Default: s.def, Required: s.required, Choices: s.choices}
		if s.fromEnv {
			v.EnvVar = envVar(s.key)
		}
		list = append(list, v)
	}
	return list
}

// Value returns the setting key formatted as a source would give it.
func (c Config) Value(key string) string {
	for _, s := range settings {
		if s.key == key {
			return s.get(c)
		}
	}
	return ""
}
