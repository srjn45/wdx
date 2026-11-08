package settings

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// conf is what the test table's settings set.
type conf struct {
	Name, Mode, Level, File, Home, Token, Key string
	Count                                     int
}

var modes = []string{"fast", "slow"}

func row(key, def string, field func(c *conf) *string) Row[conf] {
	return Row[conf]{
		Key: key, Description: "the " + key, Default: def,
		Set: func(c *conf, v string) error { *field(c) = v; return nil },
		Get: func(c conf) string { return *field(&c) },
	}
}

// testTable is a table with a string, a number, a choice, a nested key, a
// FromEnv row, a Required row and a Secret row.
func testTable() Table[conf] {
	home := row("home", "/default/home", func(c *conf) *string { return &c.Home })
	home.FromEnv = true
	token := row("token", "", func(c *conf) *string { return &c.Token })
	token.Required, token.Default = true, ""
	key := row("key", "", func(c *conf) *string { return &c.Key })
	key.Secret = true
	level := row("log.level", "info", func(c *conf) *string { return &c.Level })
	level.Set = func(c *conf, v string) error {
		if !slices.Contains([]string{"debug", "info"}, v) {
			return fmt.Errorf("%q is not debug or info", v)
		}
		c.Level = v
		return nil
	}
	mode := row("mode", "fast", func(c *conf) *string { return &c.Mode })
	mode.Choices = modes
	mode.Set = func(c *conf, v string) error {
		if !slices.Contains(modes, v) {
			return fmt.Errorf("%q is not one of %s", v, strings.Join(modes, ", "))
		}
		c.Mode = v
		return nil
	}
	return Table[conf]{
		Rows: []Row[conf]{
			home,
			row("name", "anon", func(c *conf) *string { return &c.Name }),
			{
				Key: "count", Description: "the count", Default: "1",
				Set: func(c *conf, v string) (err error) {
					if c.Count, err = strconv.Atoi(v); err != nil || c.Count < 1 {
						return fmt.Errorf("%q is not a whole number of at least 1", v)
					}
					return nil
				},
				Get: func(c conf) string { return strconv.Itoa(c.Count) },
			},
			mode,
			level,
			row("log.file", "", func(c *conf) *string { return &c.File }),
			token,
			key,
		},
		EnvPrefix:      "TEST_",
		FileHeader:     "# test settings.\n",
		DroppedHeading: "# No longer read, kept for reference:",
	}
}

type env map[string]string

func (e env) getenv(key string) string { return e[key] }

// fileWith returns the path of a config file holding content.
func fileWith(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// load reads the file at path and the environment e, as a program does.
func load(t *testing.T, table Table[conf], path string, e env) (conf, map[string]string, error) {
	t.Helper()
	file, problems := table.File(path)
	c, from, err := table.Resolve([]Source{file, table.Env(e.getenv)})
	if e, ok := errors.AsType[*Error](err); ok {
		problems = append(problems, e.Problems...)
	}
	if len(problems) > 0 {
		return c, from, &Error{Problems: problems}
	}
	return c, from, nil
}

// loadErr loads a file holding content, the token not required, and returns its error.
func loadErr(t *testing.T, content string) (*Error, string) {
	t.Helper()
	path := fileWith(t, content)
	_, _, err := load(t, optionalToken(), path, nil)
	e, ok := errors.AsType[*Error](err)
	if !ok {
		t.Fatalf("want *Error, got %v", err)
	}
	return e, path
}

// optionalToken is the test table with its token not required, for the tests
// that do not set it.
func optionalToken() Table[conf] {
	table := testTable()
	i := slices.IndexFunc(table.Rows, func(r Row[conf]) bool { return r.Key == "token" })
	table.Rows[i].Required = false
	return table
}

func TestFileSettings(t *testing.T) {
	t.Parallel()
	path := fileWith(t, "# mine\nlog:\n  level: debug\ncount: 3\nmode: slow\ntoken: x\n")
	c, from, err := load(t, testTable(), path, env{"TEST_HOME": "/h"})
	if err != nil {
		t.Fatal(err)
	}
	want := conf{Home: "/h", Name: "anon", Count: 3, Mode: "slow", Level: "debug", Token: "x"}
	if c != want {
		t.Fatalf("got %+v, want %+v", c, want)
	}
	wantFrom := map[string]string{
		"home": "TEST_HOME", "name": "default", "count": path + ":4", "mode": path + ":5",
		"log.level": path + ":3", "log.file": "default", "token": path + ":6", "key": "default",
	}
	if !maps.Equal(from, wantFrom) {
		t.Fatalf("from %v, want %v", from, wantFrom)
	}
}

func TestEmptyAndNullValues(t *testing.T) {
	t.Parallel()
	for _, content := range []string{"", "# nothing\n", "log:\n  level:\n"} {
		c, from, err := load(t, optionalToken(), fileWith(t, content), nil)
		if err != nil || c.Level != "info" || from["log.level"] != "default" {
			t.Fatalf("%q: want defaults, got %+v, %v", content, c, err)
		}
	}
}

func TestOnlySettingsFromEnvironmentAreRead(t *testing.T) {
	t.Parallel()
	c, from, err := load(t, optionalToken(), fileWith(t, ""), env{"TEST_HOME": "/h", "TEST_LOG_LEVEL": "debug", "TEST_NAME": "x"})
	if err != nil || c.Home != "/h" || c.Level != "info" || c.Name != "anon" || from["home"] != "TEST_HOME" {
		t.Fatalf("want only TEST_HOME read, got %+v, %v", c, err)
	}
}

// With AllFromEnv every row comes from the environment too, over the file,
// whose keys stay allowed; a FromEnv row's key in the file is still an error.
func TestAllFromEnv(t *testing.T) {
	t.Parallel()
	table := optionalToken()
	table.AllFromEnv = true
	path := fileWith(t, "log:\n  level: info\nname: file\ncount: 2\n")
	c, from, err := load(t, table, path, env{"TEST_LOG_LEVEL": "debug", "TEST_NAME": "env"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Level != "debug" || c.Name != "env" || c.Count != 2 || from["log.level"] != "TEST_LOG_LEVEL" || from["count"] != path+":4" {
		t.Fatalf("want the variables over the file, got %+v from %v", c, from)
	}
	if _, _, err := load(t, table, fileWith(t, "home: /x\n"), nil); err == nil || !strings.Contains(err.Error(), "home: set by TEST_HOME, not the config file") {
		t.Fatalf("want home refused in the file, got %v", err)
	}
	// The variable names the key of its problem.
	if _, _, err := load(t, table, fileWith(t, ""), env{"TEST_COUNT": "0"}); err == nil || err.Error() != `invalid settings: TEST_COUNT: "0" is not a whole number of at least 1` {
		t.Fatalf("want the variable's problem, got %v", err)
	}
}

func TestEnvPrefix(t *testing.T) {
	t.Parallel()
	table := optionalToken()
	table.EnvPrefix, table.AllFromEnv = "OTHER_HUB_", true
	c, from, err := load(t, table, fileWith(t, ""), env{"OTHER_HUB_LOG_LEVEL": "debug", "TEST_NAME": "x"})
	if err != nil || c.Level != "debug" || c.Name != "anon" || from["log.level"] != "OTHER_HUB_LOG_LEVEL" {
		t.Fatalf("want variables named by the prefix, got %+v from %v, %v", c, from, err)
	}
	// Read from its variable and the file both, so not from the environment only.
	if s := table.Describe(); s[slices.IndexFunc(s, func(s Setting) bool { return s.Key == "log.level" })].EnvVar != "OTHER_HUB_LOG_LEVEL" || s[0].Key != "home" || !s[0].EnvOnly || s[1].EnvOnly {
		t.Fatalf("described %+v", s)
	}
}

func TestEveryProblemReported(t *testing.T) {
	t.Parallel()
	e, path := loadErr(t, strings.Join([]string{
		"count: 0",
		"log:",
		"  level: loud",
		"  colour: red",
		"home: /elsewhere",
		"logs: x",
		"mode2:",
		"  a: b",
		"mode: purple",
	}, "\n"))
	want := []string{
		path + `:1: count: "0" is not a whole number of at least 1`,
		path + `:3: log.level: "loud" is not debug or info`,
		path + ":4: log.colour: unknown setting",
		path + ":5: home: set by TEST_HOME, not the config file",
		path + ":6: logs: unknown setting",
		path + ":7: mode2: unknown setting",
		path + `:9: mode: "purple" is not one of fast, slow`,
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
		"log: [":             ":1: did not find expected node content",
		"- debug\n":          ":1: not a mapping of settings",
		"name: a\nname: b\n": ":2: name: already set at line 1",
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
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	_, _, err := load(t, optionalToken(), path, nil)
	if err == nil || !strings.Contains(err.Error(), "config.yaml: cannot be read: ") {
		t.Fatalf("want unreadable file reported, got %v", err)
	}
}

func TestRequiredSetting(t *testing.T) {
	t.Parallel()
	_, _, err := load(t, testTable(), fileWith(t, ""), nil)
	e, ok := errors.AsType[*Error](err)
	if !ok || len(e.Problems) != 1 || e.Problems[0].Value != nil || err.Error() != "invalid settings: token: required" {
		t.Fatalf("want token required, got %v", err)
	}
	if _, _, err := load(t, testTable(), fileWith(t, "token: x\n"), nil); err != nil {
		t.Fatalf("want token accepted, got %v", err)
	}
}

// Check's problems are reported, and their keys taken out of from.
func TestCheck(t *testing.T) {
	t.Parallel()
	table := optionalToken()
	table.Check = func(c conf, from map[string]string) []Problem {
		if c.Mode != "slow" || c.Count < 2 {
			return nil
		}
		v := c.Mode
		return []Problem{{Key: "mode", From: from["mode"], Value: &v, Reason: "slow takes a count of 1"}}
	}
	path := fileWith(t, "mode: slow\ncount: 2\n")
	_, from, err := load(t, table, path, nil)
	if err == nil || err.Error() != "invalid settings: "+path+":1: mode: slow takes a count of 1" {
		t.Fatalf("want the check's problem, got %v", err)
	}
	if _, ok := from["mode"]; ok || from["count"] != path+":2" {
		t.Fatalf("want mode taken out of from, got %v", from)
	}
	if _, _, err := load(t, table, fileWith(t, "mode: slow\n"), nil); err != nil {
		t.Fatalf("want no problem, got %v", err)
	}
}

func TestDescribe(t *testing.T) {
	t.Parallel()
	got := testTable().Describe()
	var keys []string
	for _, s := range got {
		keys = append(keys, s.Key)
	}
	if !slices.Equal(keys, []string{"home", "name", "count", "mode", "log.level", "log.file", "token", "key"}) {
		t.Fatalf("keys %v", keys)
	}
	byKey := map[string]Setting{}
	for _, s := range got {
		byKey[s.Key] = s
	}
	if s := byKey["home"]; s.EnvVar != "TEST_HOME" || !s.EnvOnly || s.Default != "/default/home" || s.Description != "the home" {
		t.Errorf("home %+v", s)
	}
	if s := byKey["name"]; s.EnvVar != "" || s.EnvOnly || s.Required || s.Secret || s.Choices != nil {
		t.Errorf("name %+v", s)
	}
	if !byKey["token"].Required || !byKey["key"].Secret || !slices.Equal(byKey["mode"].Choices, modes) {
		t.Errorf("token %+v, key %+v, mode %+v", byKey["token"], byKey["key"], byKey["mode"])
	}
}

func TestShown(t *testing.T) {
	t.Parallel()
	table := testTable()
	c := conf{Key: "s3cret", Name: "n", Count: 2}
	if got := table.Shown(c, "key"); got != "[secret]" {
		t.Errorf("a set secret shown as %q", got)
	}
	if got := table.Value(c, "key"); got != "s3cret" {
		t.Errorf("a secret's value %q", got)
	}
	if got := table.Shown(conf{}, "key"); got != "" {
		t.Errorf("an empty secret shown as %q", got)
	}
	if got := table.Shown(c, "name"); got != "n" {
		t.Errorf("name shown as %q", got)
	}
	if got := table.Shown(c, "count"); got != "2" {
		t.Errorf("count shown as %q", got)
	}
}

// A secret setting that does not parse is reported without its value, which
// may be the secret mistyped.
func TestInvalidSecretNotShown(t *testing.T) {
	t.Parallel()
	table := optionalToken()
	i := slices.IndexFunc(table.Rows, func(r Row[conf]) bool { return r.Key == "key" })
	table.Rows[i].Set = func(*conf, string) error { return errors.New("is not 32 bytes") }
	_, _, err := load(t, table, fileWith(t, "key: hunter2\n"), nil)
	e, ok := errors.AsType[*Error](err)
	if !ok || len(e.Problems) != 1 || e.Problems[0].Value == nil || *e.Problems[0].Value != "[secret]" {
		t.Fatalf("want the key's problem with its value shown as [secret], got %v", err)
	}
	if strings.Contains(fmt.Sprintf("%+v", e.Problems), "hunter2") {
		t.Errorf("the secret is in the problem: %+v", e.Problems)
	}
}
