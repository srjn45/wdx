package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// rewriteOf rewrites the config file of a home holding content and writes
// the result back, returning it and the environment of the home.
func rewriteOf(t *testing.T, content string) (Rewritten, env) {
	t.Helper()
	home, e := homeWith(t, content)
	r, err := Rewrite(filepath.Join(home, "config.yaml"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(filepath.Join(home, "config.yaml"), r.Content); err != nil {
		t.Fatal(err)
	}
	return r, e
}

func fileKeys() []string {
	var keys []string
	for _, s := range Settings() {
		if s.EnvVar == "" {
			keys = append(keys, s.Key)
		}
	}
	return keys
}

func TestRewriteMissingFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(home, "config.yaml")
	r, err := Rewrite(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Changed || !slices.Equal(r.Added, fileKeys()) || len(r.Dropped) != 0 {
		t.Fatalf("want every setting added, got %+v", r)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(r.Content)), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			t.Fatalf("want every line commented, got %q in\n%s", line, r.Content)
		}
	}
	if err := WriteFile(path, r.Content); err != nil {
		t.Fatal(err)
	}
	c, err := Load(env{"WDX_HOME": home}.getenv)
	if err != nil {
		t.Fatal(err)
	}
	if c.From["log.level"] != "default" {
		t.Fatalf("want the defaults, got %+v", c.From)
	}
}

func TestRewriteKeepsValues(t *testing.T) {
	t.Parallel()
	content := "no_input: true\nlog:\n  level: debug\nupdate:\n  base_url: \"file:///repo/dist\"\nterminal: {shell: /bin/zsh}\n"
	home, e := homeWith(t, content)
	before, err := Load(e.getenv)
	if err != nil {
		t.Fatal(err)
	}
	r, e := rewriteOf(t, content)
	for _, line := range []string{"no_input: true\n", "log:\n", "  level: debug\n", "  # retention_days: 7\n", "  base_url: \"file:///repo/dist\"\n", "  shell: /bin/zsh\n", "# otel:\n", "#   interval_seconds: 15\n"} {
		if !strings.Contains(string(r.Content), line) {
			t.Fatalf("want %q in\n%s", line, r.Content)
		}
	}
	after, err := Load(e.getenv)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range fileKeys() {
		if before.Value(key) != after.Value(key) {
			t.Fatalf("%s: was %q, is %q", key, before.Value(key), after.Value(key))
		}
	}
	_ = home
}

func TestRewriteIsStable(t *testing.T) {
	t.Parallel()
	first, e := rewriteOf(t, "log:\n  level: warn\nold: 1\n")
	path := filepath.Join(e["WDX_HOME"], "config.yaml")
	second, err := Rewrite(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed || string(second.Content) != string(first.Content) || len(second.Added) != 0 || len(second.Dropped) != 0 {
		t.Fatalf("want a rewrite of a rewrite unchanged, got %+v\n%s", second, second.Content)
	}
}

func TestRewriteDropsUnknown(t *testing.T) {
	t.Parallel()
	r, e := rewriteOf(t, "old: {a: 1, b: [x, y]}\nlog:\n  level: debug\n  gone: 3\n")
	if !slices.Equal(r.Dropped, []string{"old.a", "old.b", "log.gone"}) {
		t.Fatalf("want old.a, old.b and log.gone dropped, got %v", r.Dropped)
	}
	if !strings.HasSuffix(string(r.Content), "\n"+droppedHeading+"\n# old.a: 1\n# old.b: [x, y]\n# log.gone: 3\n") {
		t.Fatalf("want the dropped keys commented at the end, got\n%s", r.Content)
	}
	if _, err := Load(e.getenv); err != nil {
		t.Fatalf("want the rewritten file to load, got %v", err)
	}

	// A later rewrite keeps them, and adds the keys dropped then.
	path := filepath.Join(e["WDX_HOME"], "config.yaml")
	if err := os.WriteFile(path, append([]byte("other: z\n"), r.Content...), 0o600); err != nil {
		t.Fatal(err)
	}
	again, err := Rewrite(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(again.Content), "# log.gone: 3\n# other: z\n") || !slices.Equal(again.Dropped, []string{"other"}) {
		t.Fatalf("want the earlier dropped keys kept and other added, got %v\n%s", again.Dropped, again.Content)
	}
}

func TestRewriteAdded(t *testing.T) {
	t.Parallel()
	// Listed commented, as an older template did, or set.
	r, _ := rewriteOf(t, "# no_input: false\n\n# log:\n#   # Lowest level logged.\n#   level: info\n#   retention_days: 7\n\notel:\n  endpoint: \"\"\n  # interval_seconds: 15\n")
	want := slices.DeleteFunc(fileKeys(), func(k string) bool {
		return slices.Contains([]string{"no_input", "log.level", "log.retention_days", "otel.endpoint", "otel.interval_seconds"}, k)
	})
	if !slices.Equal(r.Added, want) {
		t.Fatalf("want %v added, got %v", want, r.Added)
	}
}

func TestRewriteInvalid(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"value":    "log:\n  level: loud\n",
		"syntax":   "log: [\n",
		"home":     "home: /x\n",
		"repeated": "color: auto\ncolor: never\n",
	} {
		home, _ := homeWith(t, content)
		path := filepath.Join(home, "config.yaml")
		if _, err := Rewrite(path, nil); !errors.As(err, new(*Error)) {
			t.Errorf("%s: want *Error, got %v", name, err)
		}
		if b, _ := os.ReadFile(path); string(b) != content {
			t.Errorf("%s: want the file left alone, got %q", name, b)
		}
	}
}

func TestOnlyUnknown(t *testing.T) {
	t.Parallel()
	if e, _ := loadErr(t, "old: 1\nlog: {gone: 2}\n"); !e.OnlyUnknown() {
		t.Fatalf("want only unknown keys, got %v", e)
	}
	if e, _ := loadErr(t, "old: 1\ncolor: loud\n"); e.OnlyUnknown() {
		t.Fatalf("want an invalid value among them, got %v", e)
	}
}

func TestRewriteSet(t *testing.T) {
	t.Parallel()
	home, e := homeWith(t, "update:\n  channel: stable\n")
	path := filepath.Join(home, "config.yaml")
	r, err := Rewrite(path, map[string]string{"update.channel": "dev", "terminal.shell": "/bin/my: shell"})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, r.Content); err != nil {
		t.Fatal(err)
	}
	c, err := Load(e.getenv)
	if err != nil {
		t.Fatalf("want the file to load, got %v\n%s", err, r.Content)
	}
	if c.UpdateChannel != "dev" || c.Value("terminal.shell") != "/bin/my: shell" {
		t.Fatalf("want the values set read back, got %q, %q\n%s", c.UpdateChannel, c.Value("terminal.shell"), r.Content)
	}
}

func TestRewriteSetInvalid(t *testing.T) {
	t.Parallel()
	home, _ := homeWith(t, "")
	path := filepath.Join(home, "config.yaml")
	for _, set := range []map[string]string{{"update.channel": "nightly"}, {"no.such": "1"}, {"home": "/elsewhere"}} {
		var e *Error
		if _, err := Rewrite(path, set); !errors.As(err, &e) || e.OnlyUnknown() || e.Problems[0].From != setSource {
			t.Errorf("%v: want a problem from --value, got %v", set, err)
		}
	}
}

func TestSet(t *testing.T) {
	t.Parallel()
	home, _ := homeWith(t, "color: never\n")
	r, err := Set(home, "log.level", "debug")
	if err != nil || !r.Changed {
		t.Fatalf("want it changed, got %+v: %v", r, err)
	}
	c, err := LoadHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if c.Value("log.level") != "debug" || c.Value("color") != "never" {
		t.Fatalf("want log.level set and color kept, got %s and %s", c.Value("log.level"), c.Value("color"))
	}
	if r, err = Set(home, "log.level", "debug"); err != nil || r.Changed {
		t.Fatalf("want nothing changed the second time, got %+v: %v", r, err)
	}
}

func TestSetInvalid(t *testing.T) {
	t.Parallel()
	for _, tc := range [][2]string{{"log.level", "loud"}, {"no.such", "1"}, {"home", "/elsewhere"}} {
		home, _ := homeWith(t, "color: never\n")
		_, err := Set(home, tc[0], tc[1])
		if _, ok := errors.AsType[*Error](err); !ok {
			t.Errorf("%v: want an *Error, got %v", tc, err)
		}
		if got, err := os.ReadFile(filepath.Join(home, "config.yaml")); err != nil || string(got) != "color: never\n" {
			t.Errorf("%v: want the file left alone, got %q: %v", tc, got, err)
		}
	}
}

// Rewriting reads the file and writes it whole, so without the lock a set
// running at the same time as another loses the other's value.
func TestSetConcurrently(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"no_input": "true", "color": "never", "log.level": "debug", "log.retention_days": "3",
		"otel.endpoint": "", "otel.interval_seconds": "5", "update.channel": "dev",
		"update.base_url": "https://example.com", "terminal.shell": "/bin/sh",
		"terminal.history_lines": "5", "terminal.history_tail": "5",
	}
	for range 5 {
		home, _ := homeWith(t, "")
		var wg sync.WaitGroup
		for key, value := range values {
			wg.Go(func() {
				if _, err := Set(home, key, value); err != nil {
					t.Errorf("%s: %v", key, err)
				}
			})
		}
		wg.Wait()
		c, err := LoadHome(home)
		if err != nil {
			t.Fatal(err)
		}
		for key, value := range values {
			if got := c.Value(key); got != value {
				t.Errorf("%s: want %q kept, got %q", key, value, got)
			}
		}
	}
}
