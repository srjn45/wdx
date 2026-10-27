// Package config holds the settings of wdx. They are loaded once when wdx
// starts and passed down as a value; nothing else reads the environment.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/srjn45/warden/internal/identity"
)

// Config is every setting.
type Config struct {
	Home             string
	NoInput          bool
	LogLevel         slog.Level
	LogRetentionDays int

	// From tells where each setting was taken from, by key: "default" or
	// the name the source knows it by, such as "WDX_HOME".
	From map[string]string
}

// Source supplies raw setting values.
type Source interface {
	// Lookup returns the value of key and the name the source knows it by;
	// ok is false when the source does not set key.
	Lookup(key string) (value, name string, ok bool)
}

type envSource func(string) string

// Env is the source that reads key "log.level" from $WDX_LOG_LEVEL. An empty
// variable is not set.
func Env(getenv func(string) string) Source {
	return envSource(getenv)
}

func (getenv envSource) Lookup(key string) (string, string, bool) {
	name := EnvVar(key)
	v := getenv(name)
	return v, name, v != ""
}

// EnvVar is the environment variable that sets key.
func EnvVar(key string) string {
	return identity.Env(strings.ToUpper(strings.ReplaceAll(key, ".", "_")))
}

// setting is one row of the table every setting is defined by.
type setting struct {
	key         string
	description string
	required    bool   // has no default; Load fails when no source sets it
	def         string // parsed by set like any other value
	set         func(c *Config, value string) error
	get         func(c Config) string
}

var logLevels = []string{"debug", "info", "warn", "error"}

var settings = []setting{
	{
		key:         "home",
		description: "directory holding the database, logs and cloned projects",
		def:         defaultHome(),
		set:         func(c *Config, v string) error { c.Home = v; return nil },
		get:         func(c Config) string { return c.Home },
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
	c, _ := load(settings)
	return c
}

// Load returns the settings from the defaults overridden by each source in
// turn, so a later source wins. It fails with an *Error listing every
// required setting missing and every value that does not parse.
func Load(sources ...Source) (Config, error) {
	return load(settings, sources...)
}

func load(table []setting, sources ...Source) (Config, error) {
	c := Config{From: map[string]string{}}
	var problems []Problem
	for _, s := range table {
		value, from, found := s.def, "default", !s.required
		for _, src := range sources {
			if v, name, ok := src.Lookup(s.key); ok {
				value, from, found = v, name, true
			}
		}
		if !found {
			problems = append(problems, Problem{Key: s.key, Name: EnvVar(s.key), Reason: "required"})
			continue
		}
		if err := s.set(&c, value); err != nil {
			if from == "default" {
				panic(fmt.Sprintf("config: default of %s: %v", s.key, err))
			}
			problems = append(problems, Problem{Key: s.key, Name: from, Value: &value, Reason: err.Error()})
			continue
		}
		c.From[s.key] = from
	}
	if len(problems) > 0 {
		return c, &Error{Problems: problems}
	}
	return c, nil
}

// Error lists every setting that is missing or invalid.
type Error struct {
	Problems []Problem
}

// Problem is a setting that is missing or invalid.
type Problem struct {
	Key    string  `json:"key"`
	Name   string  `json:"name"`  // what the source calls it, such as "WDX_LOG_LEVEL"
	Value  *string `json:"value"` // nil when missing
	Reason string  `json:"reason"`
}

func (e *Error) Error() string {
	var msgs []string
	for _, p := range e.Problems {
		msgs = append(msgs, p.Name+": "+p.Reason)
	}
	return "invalid settings: " + strings.Join(msgs, "; ")
}

// Setting describes a setting for help and logs.
type Setting struct {
	Key         string
	EnvVar      string
	Description string
	Default     string // "" when required
	Required    bool
}

// Settings describes every setting.
func Settings() []Setting {
	list := make([]Setting, 0, len(settings))
	for _, s := range settings {
		list = append(list, Setting{Key: s.key, EnvVar: EnvVar(s.key), Description: s.description, Default: s.def, Required: s.required})
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
