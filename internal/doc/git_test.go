package doc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// gitFiles returns the names in dir and what each holds.
func gitFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = string(b)
	}
	return files
}

func TestSyncToGit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.project(t, "wdx")
	other := f.project(t, "other")
	auth := f.create(t, p, New{Name: "auth", Format: "md", Content: []byte("auth\n")})
	f.create(t, p, New{Name: "notes", Content: []byte("notes")})
	f.create(t, other, New{Name: "theirs", Format: "md"})
	root := t.TempDir()

	r, err := f.store.SyncToGit(ctx, p, root, "docs/wdx")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "docs", "wdx")
	if r.Path != dir || !slices.Equal(r.Written, []string{"auth.md", "notes"}) || len(r.Removed) != 0 || len(r.Skipped) != 0 {
		t.Errorf("first sync: %+v", r)
	}
	files := gitFiles(t, dir)
	if files["auth.md"] != "auth\n" || files["notes"] != "notes" || files[ownedList] != ownedHeader+"auth.md\nnotes\n" || len(files) != 3 {
		t.Errorf("files: %q", files)
	}
	for name := range files {
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || info.Mode().Perm() != 0o444 {
			t.Errorf("%s: want read-only, got %v, %v", name, info.Mode(), err)
		}
	}

	// A person's file stays, a renamed doc moves and a removed one goes.
	if err := os.WriteFile(filepath.Join(dir, "mine.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Rename(ctx, auth.ID, "Auth"); err != nil {
		t.Fatal(err)
	}
	notes, _ := f.store.GetByName(ctx, p, "notes")
	if _, err := f.store.Remove(ctx, notes.ID); err != nil {
		t.Fatal(err)
	}
	r, err = f.store.SyncToGit(ctx, p, root, "docs/wdx")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.Written, []string{"Auth.md"}) || !slices.Equal(r.Removed, []string{"auth.md", "notes"}) {
		t.Errorf("second sync: %+v", r)
	}
	files = gitFiles(t, dir)
	if files["Auth.md"] != "auth\n" || files["mine.md"] != "mine" || files[ownedList] != ownedHeader+"Auth.md\n" || len(files) != 3 {
		t.Errorf("after a rename in case and a removal: %q", files)
	}
}

func TestSyncToGitSkipsAPersonsFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.project(t, "wdx")
	f.create(t, p, New{Name: "auth", Format: "md", Content: []byte("from wdx")})
	f.create(t, p, New{Name: "design", Format: "md"})
	root := t.TempDir()
	dir := filepath.Join(root, "docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.md"), []byte("a person's"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := f.store.SyncToGit(ctx, p, root, "docs")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.Skipped, []string{"auth.md"}) || !slices.Equal(r.Written, []string{"design.md"}) {
		t.Errorf("sync: %+v", r)
	}
	files := gitFiles(t, dir)
	if files["auth.md"] != "a person's" || files[ownedList] != ownedHeader+"design.md\n" {
		t.Errorf("files: %q", files)
	}
}

func TestSyncToGitRefusesPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.project(t, "wdx")
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"", "/abs", "..", "../x", "a/../../x", ".git", ".git/docs", "out", "out/docs", "file", "file/docs"} {
		var invalid *InvalidPathError
		if _, err := f.store.SyncToGit(ctx, p, root, dir); !errors.As(err, &invalid) {
			t.Errorf("%q: want an *InvalidPathError, got %v", dir, err)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("written outside the repository: %v", entries)
	}
	if _, err := f.store.SyncToGit(ctx, p, root, "a/./b/../docs"); err != nil {
		t.Errorf("a path inside, not clean: %v", err)
	}
}

func TestSyncToGitIgnoresListedPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.project(t, "wdx")
	root := t.TempDir()
	dir := filepath.Join(root, "docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(root, "keep")
	if err := os.WriteFile(keep, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// A list edited to name a file elsewhere must not remove it.
	if err := os.WriteFile(filepath.Join(dir, ownedList), []byte(strings.Join([]string{"../keep", "# a comment", ""}, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SyncToGit(ctx, p, root, "docs"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("a file outside the directory was removed: %v", err)
	}
}
