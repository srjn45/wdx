package settings

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// rewriteOf rewrites a config file holding content and writes the result
// back, returning it and the file's path.
func rewriteOf(t *testing.T, content string) (Rewritten, string) {
	t.Helper()
	path := fileWith(t, content)
	r, err := optionalToken().Rewrite(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, r.Content); err != nil {
		t.Fatal(err)
	}
	return r, path
}

// fileKeys are the keys of the test table the config file holds.
var fileKeys = []string{"name", "count", "mode", "log.level", "log.file", "token", "key"}

func values(t *testing.T, path string) map[string]string {
	t.Helper()
	table := optionalToken()
	c, _, err := load(t, table, path, nil)
	if err != nil {
		t.Fatalf("want the file to load, got %v", err)
	}
	got := map[string]string{}
	for _, key := range fileKeys {
		got[key] = table.Value(c, key)
	}
	return got
}

func TestRewriteMissingFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	r, err := optionalToken().Rewrite(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Changed || !slices.Equal(r.Added, fileKeys) || len(r.Dropped) != 0 {
		t.Fatalf("want every setting added, got %+v", r)
	}
	if !strings.HasPrefix(string(r.Content), "# test settings.\n\n# The name.\n# name: anon\n") {
		t.Fatalf("want the header, then the first setting, got\n%s", r.Content)
	}
	for line := range strings.Lines(string(r.Content)) {
		if line != "\n" && !strings.HasPrefix(line, "#") {
			t.Fatalf("want every line commented, got %q in\n%s", line, r.Content)
		}
	}
	if err := WriteFile(path, r.Content); err != nil {
		t.Fatal(err)
	}
	if _, from, err := load(t, optionalToken(), path, nil); err != nil || from["log.level"] != "default" {
		t.Fatalf("want the defaults, got %v, %v", from, err)
	}
}

func TestRewriteKeepsValues(t *testing.T) {
	t.Parallel()
	content := "count: 3\nlog:\n  level: debug\nname: \"a: b\"\n"
	before := values(t, fileWith(t, content))
	r, path := rewriteOf(t, content)
	for _, line := range []string{"count: 3\n", "log:\n", "  level: debug\n", "  # file: \"\"\n", "name: \"a: b\"\n", "# mode: fast\n"} {
		if !strings.Contains(string(r.Content), line) {
			t.Fatalf("want %q in\n%s", line, r.Content)
		}
	}
	after := values(t, path)
	for _, key := range fileKeys {
		if before[key] != after[key] {
			t.Fatalf("%s: was %q, is %q", key, before[key], after[key])
		}
	}
}

func TestRewriteIsStable(t *testing.T) {
	t.Parallel()
	first, path := rewriteOf(t, "log:\n  level: debug\nold: 1\n")
	second, err := optionalToken().Rewrite(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed || string(second.Content) != string(first.Content) || len(second.Added) != 0 || len(second.Dropped) != 0 {
		t.Fatalf("want a rewrite of a rewrite unchanged, got %+v\n%s", second, second.Content)
	}
}

func TestRewriteDropsUnknown(t *testing.T) {
	t.Parallel()
	r, path := rewriteOf(t, "old: {a: 1, b: [x, y]}\nlog:\n  level: debug\n  gone: 3\n")
	if !slices.Equal(r.Dropped, []string{"old.a", "old.b", "log.gone"}) {
		t.Fatalf("want old.a, old.b and log.gone dropped, got %v", r.Dropped)
	}
	if !strings.HasSuffix(string(r.Content), "\n# No longer read, kept for reference:\n# old.a: 1\n# old.b: [x, y]\n# log.gone: 3\n") {
		t.Fatalf("want the dropped keys commented at the end, got\n%s", r.Content)
	}
	if _, _, err := load(t, optionalToken(), path, nil); err != nil {
		t.Fatalf("want the rewritten file to load, got %v", err)
	}

	// A later rewrite keeps them, and adds the keys dropped then.
	if err := os.WriteFile(path, append([]byte("other: z\n"), r.Content...), 0o600); err != nil {
		t.Fatal(err)
	}
	again, err := optionalToken().Rewrite(path, nil)
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
	r, _ := rewriteOf(t, "# name: anon\n\n# log:\n#   # The log.level.\n#   level: info\n\nmode: fast\ncount: 2\n")
	if want := []string{"log.file", "token", "key"}; !slices.Equal(r.Added, want) {
		t.Fatalf("want %v added, got %v", want, r.Added)
	}
}

func TestRewriteInvalid(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"value":    "log:\n  level: loud\n",
		"syntax":   "log: [\n",
		"home":     "home: /x\n",
		"repeated": "mode: fast\nmode: slow\n",
		"required": "name: x\n",
	} {
		table := optionalToken()
		if name == "required" {
			table = testTable()
		}
		path := fileWith(t, content)
		if _, err := table.Rewrite(path, nil); !errors.As(err, new(*Error)) {
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
	if e, _ := loadErr(t, "old: 1\nmode: loud\n"); e.OnlyUnknown() {
		t.Fatalf("want an invalid value among them, got %v", e)
	}
}

func TestRewriteSet(t *testing.T) {
	t.Parallel()
	path := fileWith(t, "mode: fast\n")
	r, err := optionalToken().Rewrite(path, map[string]string{"mode": "slow", "name": "/bin/my: shell"})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, r.Content); err != nil {
		t.Fatal(err)
	}
	if got := values(t, path); got["mode"] != "slow" || got["name"] != "/bin/my: shell" {
		t.Fatalf("want the values set read back, got %v\n%s", got, r.Content)
	}
}

func TestRewriteSetInvalid(t *testing.T) {
	t.Parallel()
	path := fileWith(t, "")
	for _, set := range []map[string]string{{"mode": "nightly"}, {"no.such": "1"}, {"home": "/elsewhere"}} {
		var e *Error
		if _, err := optionalToken().Rewrite(path, set); !errors.As(err, &e) || e.OnlyUnknown() || e.Problems[0].From != setSource {
			t.Errorf("%v: want a problem from --value, got %v", set, err)
		}
	}
}

// setIn sets key in the config file at path, under a lock beside it.
func setIn(path, key, value string) (Rewritten, error) {
	return optionalToken().Set(path, filepath.Join(filepath.Dir(path), "config.lock"), key, value)
}

func TestSet(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "home", "config.yaml")
	if r, err := setIn(path, "mode", "slow"); err != nil || !r.Changed {
		t.Fatalf("want the file made, got %+v: %v", r, err)
	}
	r, err := setIn(path, "log.level", "debug")
	if err != nil || !r.Changed {
		t.Fatalf("want it changed, got %+v: %v", r, err)
	}
	if got := values(t, path); got["log.level"] != "debug" || got["mode"] != "slow" {
		t.Fatalf("want log.level set and mode kept, got %v", got)
	}
	if r, err = setIn(path, "log.level", "debug"); err != nil || r.Changed {
		t.Fatalf("want nothing changed the second time, got %+v: %v", r, err)
	}
}

// A setting under a section is written under its heading, and read back.
func TestSetNested(t *testing.T) {
	t.Parallel()
	path := fileWith(t, "mode: slow\n")
	if _, err := setIn(path, "log.level", "debug"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "\nlog:\n  # The log.level.\n  level: debug\n") {
		t.Errorf("written:\n%s", content)
	}
	if got := values(t, path); got["log.level"] != "debug" || got["mode"] != "slow" {
		t.Fatalf("read back %v", got)
	}
}

func TestSetInvalid(t *testing.T) {
	t.Parallel()
	for _, tc := range [][2]string{{"log.level", "loud"}, {"no.such", "1"}, {"home", "/elsewhere"}} {
		path := fileWith(t, "mode: slow\n")
		_, err := setIn(path, tc[0], tc[1])
		if _, ok := errors.AsType[*Error](err); !ok {
			t.Errorf("%v: want an *Error, got %v", tc, err)
		}
		if got, err := os.ReadFile(path); err != nil || string(got) != "mode: slow\n" {
			t.Errorf("%v: want the file left alone, got %q: %v", tc, got, err)
		}
	}
}

// Rewriting reads the file and writes it whole, so without the lock a set
// running at the same time as another loses the other's value.
func TestSetConcurrently(t *testing.T) {
	t.Parallel()
	want := map[string]string{"name": "n", "count": "4", "mode": "slow", "log.level": "debug", "log.file": "/f", "token": "t", "key": "k"}
	for range 5 {
		path := fileWith(t, "")
		var wg sync.WaitGroup
		for key, value := range want {
			wg.Go(func() {
				if _, err := setIn(path, key, value); err != nil {
					t.Errorf("%s: %v", key, err)
				}
			})
		}
		wg.Wait()
		got := values(t, path)
		for key, value := range want {
			if got[key] != value {
				t.Errorf("%s: want %q kept, got %q", key, value, got[key])
			}
		}
	}
}
