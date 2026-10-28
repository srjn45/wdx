// Package config holds the settings of wdx. They are loaded once when wdx
// starts and passed down as a value; nothing else reads the environment or
// the config file.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

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
	fromEnv     bool   // read from the environment instead of the config file
	required    bool   // has no default; Load fails when no source sets it
	def         string // parsed by set like any other value
	set         func(c *Config, value string) error
	get         func(c Config) string
}

var logLevels = []string{"debug", "info", "warn", "error"}

// UpdateChannels are the release channels wdx update follows: stable
// releases only, or every release.
var UpdateChannels = []string{"stable", "dev"}

var settings = []setting{
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
}

func defaultHome() string {
	home, err := os.UserHomeDir()
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
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, yamlProblems(path, err)
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, []Problem{{From: at(path, root), Reason: "not a mapping of settings"}}
	}
	r := fileReader{path: path, table: table, values: fileSource{}, lines: map[string]int{}}
	r.walk("", root)
	return r.values, r.problems
}

// fileReader collects the values of a config file and its problems.
type fileReader struct {
	path     string
	table    []setting
	values   fileSource
	lines    map[string]int // where each key was first seen
	problems []Problem
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
			}
		case j >= 0:
			r.problem(key, from, "not a single value")
		case v.Kind == yaml.MappingNode && slices.ContainsFunc(r.table, func(s setting) bool { return strings.HasPrefix(s.key, key+".") }):
			r.walk(key+".", v)
		default:
			r.problem(key, from, "unknown setting")
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
}

// Settings describes every setting.
func Settings() []Setting {
	list := make([]Setting, 0, len(settings))
	for _, s := range settings {
		v := Setting{Key: s.key, Description: s.description, Default: s.def, Required: s.required}
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
