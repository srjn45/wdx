package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// rewriteOf rewrites the config file of a home holding content and writes
// the result back, returning it and the environment of the home.
func rewriteOf(t *testing.T, content string) (Rewritten, env) {
	t.Helper()
	home, e := homeWith(t, content)
	r, err := Rewrite(filepath.Join(home, "config.yaml"))
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
	r, err := Rewrite(path)
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
	second, err := Rewrite(path)
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
	again, err := Rewrite(path)
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
		if _, err := Rewrite(path); !errors.As(err, new(*Error)) {
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
