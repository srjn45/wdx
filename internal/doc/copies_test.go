package doc

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// copyOf returns the copy at rel under the copies' directory, failing the test
// unless it is read-only.
func (f fixture) copyOf(t *testing.T, rel string) string {
	t.Helper()
	path := filepath.Join(f.dir, rel)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o444 {
		t.Errorf("%s has mode %o, want 0444", rel, mode)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (f fixture) noCopy(t *testing.T, rel string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(f.dir, rel)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s: got %v, want no file", rel, err)
	}
}

func TestCopies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.project(t, "wdx")
	d := f.create(t, p, New{Name: "auth", Format: "md", Content: []byte("one")})
	if got := f.copyOf(t, "wdx/auth.md"); got != "one" {
		t.Errorf("created: %q", got)
	}
	_, r, err := f.store.Content(ctx, d.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(f.dir, "wdx/auth.md")); !info.ModTime().Equal(r.CreatedAt) {
		t.Errorf("modified at %v, want the revision's %v", info.ModTime(), r.CreatedAt)
	}

	if _, err := f.store.Update(ctx, d.ID, 1, []byte("two")); err != nil {
		t.Fatal(err)
	}
	if got := f.copyOf(t, "wdx/auth.md"); got != "two" {
		t.Errorf("updated: %q", got)
	}

	if _, err := f.store.Rename(ctx, d.ID, "Auth"); err != nil {
		t.Fatal(err)
	}
	if got := f.copyOf(t, "wdx/Auth.md"); got != "two" {
		t.Errorf("renamed in another case: %q", got)
	}
	if _, err := f.store.Rename(ctx, d.ID, "auth-rfc"); err != nil {
		t.Fatal(err)
	}
	f.noCopy(t, "wdx/Auth.md")
	if got := f.copyOf(t, "wdx/auth-rfc.md"); got != "two" {
		t.Errorf("renamed: %q", got)
	}

	if _, err := f.store.Remove(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	f.noCopy(t, "wdx/auth-rfc.md")

	f.create(t, p, New{Name: "notes", Content: []byte("plain")})
	if got := f.copyOf(t, "wdx/notes"); got != "plain" {
		t.Errorf("no format: %q", got)
	}
}

func TestCopyProjectDirs(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for name, dir := range map[string]string{
		"wdx":       "wdx",
		"a/b":       "a%2Fb",
		".hidden":   "%2Ehidden",
		"..":        "%2E.",
		"my repo":   "my%20repo",
		"next.js":   "next.js",
		"über_proj": "%C3%BCber_proj",
	} {
		f.create(t, f.project(t, name), New{Name: "doc", Format: "md", Content: []byte(name)})
		if got := f.copyOf(t, filepath.Join(dir, "doc.md")); got != name {
			t.Errorf("project %q: copy holds %q", name, got)
		}
	}
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 7 {
		t.Errorf("%d directories, want one per project, all inside the copies' directory", len(entries))
	}
}

func TestCopyFailureFailsNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	// A file where the directory should be: no copy can be written.
	if err := os.WriteFile(f.dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	d := f.create(t, f.project(t, "wdx"), New{Name: "auth", Content: []byte("one")})
	if _, err := f.store.Update(ctx, d.ID, 1, []byte("two")); err != nil {
		t.Errorf("update: %v", err)
	}
	if _, err := f.store.Rename(ctx, d.ID, "other"); err != nil {
		t.Errorf("rename: %v", err)
	}
	if _, err := f.store.Remove(ctx, d.ID); err != nil {
		t.Errorf("remove: %v", err)
	}
}

func TestCatchUpCopies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.project(t, "wdx")
	f.create(t, p, New{Name: "missing", Format: "md", Content: []byte("missing")})
	f.create(t, p, New{Name: "stale", Format: "md", Content: []byte("new")})
	f.create(t, p, New{Name: "current", Format: "md", Content: []byte("current")})

	if err := os.Remove(filepath.Join(f.dir, "wdx/missing.md")); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(f.dir, "wdx/stale.md")
	if err := os.Chmod(stale, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A copy whose modified time is its revision's is current, whatever it holds:
	// catching up trusts the time rather than reading every file.
	current := filepath.Join(f.dir, "wdx/current.md")
	info, err := os.Stat(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(current, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(current, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	// What no doc matches stays: were the database lost, it is all that is left.
	orphan := filepath.Join(f.dir, "gone-project", "doc.md")
	if err := os.MkdirAll(filepath.Dir(orphan), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphan, []byte("orphan"), 0o444); err != nil {
		t.Fatal(err)
	}

	if err := f.store.CatchUpCopies(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.copyOf(t, "wdx/missing.md"); got != "missing" {
		t.Errorf("missing: %q", got)
	}
	if got := f.copyOf(t, "wdx/stale.md"); got != "new" {
		t.Errorf("stale: %q", got)
	}
	if b, _ := os.ReadFile(current); string(b) != "untouched" {
		t.Errorf("a current copy was written again: %q", b)
	}
	if b, err := os.ReadFile(orphan); err != nil || string(b) != "orphan" {
		t.Errorf("a copy no doc matches: %q, %v", b, err)
	}
}

func TestRemoveProjectCopies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	f.create(t, f.project(t, "a/b"), New{Name: "one", Content: []byte("1")})
	f.create(t, f.project(t, "kept"), New{Name: "two", Content: []byte("2")})

	f.store.RemoveProjectCopies(ctx, "a/b")
	f.noCopy(t, "a%2Fb")
	if got := f.copyOf(t, "kept/two"); got != "2" {
		t.Errorf("another project's copy: %q", got)
	}
	f.store.RemoveProjectCopies(ctx, "never-had-docs")
}

// A revision's time survives the trip through the file's modified time, which
// is all catching up compares.
func TestCopyTimeRoundTrip(t *testing.T) {
	t.Parallel()
	made := time.UnixMilli(1_762_000_000_123)
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, made, made); err != nil {
		t.Fatal(err)
	}
	if !current(path, made) || current(path, made.Add(time.Millisecond)) {
		t.Error("current does not tell the revision's time from another")
	}
}
