// Package settings is the engine behind a program's settings: a table of rows,
// each a setting, read from a config file and the environment, layered, checked,
// and written back to the file.
package settings

import (
	"fmt"
	"slices"
	"strings"
)

// Row is one setting of a Table, whose values C holds.
type Row[C any] struct {
	Key         string
	Description string
	FromEnv     bool     // read from the environment only; in the file it is an error
	Required    bool     // has no default; Resolve fails when no source sets it
	Secret      bool     // shown as [secret] wherever values are shown
	Default     string   // parsed by Set like any other value
	Choices     []string // the values to choose among, which Set accepts; nil when it takes any
	Set         func(c *C, value string) error
	Get         func(c C) string
}

// Table is every setting of a program.
type Table[C any] struct {
	Rows []Row[C]
	// EnvPrefix starts the name of each setting's environment variable.
	EnvPrefix string
	// AllFromEnv lets every row also come from the environment, over the file.
	AllFromEnv bool
	// Check returns the problems between settings each valid alone, given
	// where each was taken from; nil for none.
	Check func(c C, from map[string]string) []Problem
	// FileHeader is the comment a written config file starts with, whole lines.
	FileHeader string
	// DroppedHeading is the comment line that heads, at the end of a written
	// config file, the keys no row has.
	DroppedHeading string
}

// Source supplies raw setting values.
type Source interface {
	// Lookup returns the value of key and where it was found; ok is false
	// when the source does not set key.
	Lookup(key string) (value, from string, ok bool)
}

// Env is the environment as a source, getenv reading it: the rows marked
// FromEnv, or every row when the table is AllFromEnv, each from its variable.
// An empty variable is not set.
func (t Table[C]) Env(getenv func(string) string) Source {
	return envSource[C]{t, getenv}
}

type envSource[C any] struct {
	table  Table[C]
	getenv func(string) string
}

func (e envSource[C]) Lookup(key string) (string, string, bool) {
	row, ok := e.table.row(key)
	if !ok || !e.table.fromEnv(row) {
		return "", "", false
	}
	name := e.table.envVar(key)
	v := e.getenv(name)
	return v, name, v != ""
}

// envVar is the variable of key: "log.level" is the prefix and LOG_LEVEL.
func (t Table[C]) envVar(key string) string {
	return t.EnvPrefix + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

func (t Table[C]) fromEnv(r Row[C]) bool {
	return r.FromEnv || t.AllFromEnv
}

func (t Table[C]) row(key string) (Row[C], bool) {
	i := slices.IndexFunc(t.Rows, func(r Row[C]) bool { return r.Key == key })
	if i < 0 {
		return Row[C]{}, false
	}
	return t.Rows[i], true
}

// Resolve returns the settings from the defaults overridden by each source
// in turn, so a later source wins, and where each was taken from, by key:
// "default", the environment variable, or the config file and line. A key
// with a problem is left out of from, and one whose value does not parse
// keeps its default, so that what needs no settings runs on the rest. It
// fails with an *Error listing every problem: a required setting missing, a
// value that does not parse, and what the table's Check finds.
func (t Table[C]) Resolve(sources []Source) (C, map[string]string, error) {
	var c C
	from := map[string]string{}
	var problems []Problem
	for _, r := range t.Rows {
		value, source, found := r.Default, "default", !r.Required
		var byEnv bool
		for _, src := range sources {
			if v, f, ok := src.Lookup(r.Key); ok {
				_, byEnv = src.(envSource[C])
				value, source, found = v, f, true
			}
		}
		if !found {
			problems = append(problems, Problem{Key: r.Key, Reason: requiredSetting})
			continue
		}
		if err := r.Set(&c, value); err != nil {
			if source == "default" {
				panic(fmt.Sprintf("settings: default of %s: %v", r.Key, err))
			}
			shown := value
			if r.Secret {
				// The value may be the secret, mistyped.
				shown = secretShown
			}
			problems = append(problems, Problem{Key: r.Key, From: source, Value: &shown, Reason: err.Error(), byEnv: byEnv})
			if !r.Required {
				_ = r.Set(&c, r.Default)
			}
			continue
		}
		from[r.Key] = source
	}
	if t.Check != nil {
		for _, p := range t.Check(c, from) {
			problems = append(problems, p)
			delete(from, p.Key)
		}
	}
	if len(problems) > 0 {
		return c, from, &Error{Problems: problems}
	}
	return c, from, nil
}

// The reasons of a problem with a key no setting has, and of a required
// setting no source sets.
const (
	unknownSetting  = "unknown setting"
	requiredSetting = "required"
)

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
	byEnv  bool    // From is the variable of Key, which names it
}

func (p Problem) String() string {
	parts := []string{p.From, p.Key, p.Reason}
	if p.byEnv {
		parts[1] = ""
	}
	return strings.Join(slices.DeleteFunc(parts, func(s string) bool { return s == "" }), ": ")
}

// OnlyUnknown is true when every problem is a key no setting has, which
// Rewrite moves out of the way.
func (e *Error) OnlyUnknown() bool {
	return !slices.ContainsFunc(e.Problems, func(p Problem) bool { return p.Reason != unknownSetting })
}

// OnlyRewritable is true when every problem is a key no setting has or a
// required setting no source sets: a rewrite of the file moves the one out of
// the way and can set the other.
func (e *Error) OnlyRewritable() bool {
	return !slices.ContainsFunc(e.Problems, func(p Problem) bool { return p.Reason != unknownSetting && p.Reason != requiredSetting })
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
	EnvVar      string // the variable it is read from; "" for the config file only
	EnvOnly     bool   // read from the environment only, never from the config file
	Description string
	Default     string // "" when required or empty
	Required    bool
	Secret      bool
	Choices     []string // the values to choose among; nil when it takes any
}

// Describe describes every setting.
func (t Table[C]) Describe() []Setting {
	list := make([]Setting, 0, len(t.Rows))
	for _, r := range t.Rows {
		s := Setting{Key: r.Key, Description: r.Description, Default: r.Default, Required: r.Required, Secret: r.Secret, Choices: r.Choices, EnvOnly: r.FromEnv}
		if t.fromEnv(r) {
			s.EnvVar = t.envVar(r.Key)
		}
		list = append(list, s)
	}
	return list
}

// Value returns the setting key of c formatted as a source would give it.
func (t Table[C]) Value(c C, key string) string {
	if r, ok := t.row(key); ok {
		return r.Get(c)
	}
	return ""
}

// secretShown stands for the value of a secret setting.
const secretShown = "[secret]"

// Shown is Value as it may be shown: [secret] for a secret setting that is set.
func (t Table[C]) Shown(c C, key string) string {
	v := t.Value(c, key)
	if r, ok := t.row(key); ok && r.Secret && v != "" {
		return secretShown
	}
	return v
}
