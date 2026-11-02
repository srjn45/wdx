package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateEntry(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	c := Checkouts{Clone: dir, Paths: []string{dir}}
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := CreateEntry(t.Context(), c, "main", "src/new.go", false); err != nil || got != "src/new.go" {
		t.Fatalf("file: %q, %v", got, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "src/new.go")); err != nil || len(b) != 0 {
		t.Errorf("the file holds %q, %v", b, err)
	}
	if got, err := CreateEntry(t.Context(), c, "main", "pkg", true); err != nil || got != "pkg" {
		t.Fatalf("directory: %q, %v", got, err)
	}
	if info, err := os.Stat(filepath.Join(dir, "pkg")); err != nil || !info.IsDir() {
		t.Errorf("the directory: %v, %v", info, err)
	}
	for _, path := range []string{"file", "src/new.go", "pkg"} {
		for _, directory := range []bool{false, true} {
			if _, err := CreateEntry(t.Context(), c, "main", path, directory); !errors.As(err, new(*ExistsError)) {
				t.Errorf("%s again, directory %v: %v", path, directory, err)
			}
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "file")); string(b) != "one\n" {
		t.Errorf("the file there was changed: %q", b)
	}
	if _, err := CreateEntry(t.Context(), c, "main", "missing/new", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("in a directory not there: %v", err)
	}
	if _, err := CreateEntry(t.Context(), c, "main", "file/new", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("in a file: %v", err)
	}
	for _, path := range []string{"../out", "/abs", "", ".", ".git/x", "src/.GIT"} {
		if _, err := CreateEntry(t.Context(), c, "main", path, false); !isInvalidPath(err) && !errors.As(err, new(*InvalidNameError)) {
			t.Errorf("%q: %v", path, err)
		}
	}
	if _, err := CreateEntry(t.Context(), c, "main", ".git", true); !errors.As(err, new(*InvalidPathError)) {
		t.Errorf(".git: %v", err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "out")); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateEntry(t.Context(), c, "main", "out/new", false); !isInvalidPath(err) {
		t.Errorf("through a link out of the worktree: %v", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("written outside: %v", entries)
	}
	if _, err := CreateEntry(t.Context(), c, "nope", "x", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown worktree: %v", err)
	}
}

func TestRenameEntry(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	c := Checkouts{Clone: dir, Paths: []string{dir}}
	if err := os.MkdirAll(filepath.Join(dir, "src/inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "src/a.go", "a\n")
	write(t, dir, "src/b.go", "b\n")
	if got, err := RenameEntry(t.Context(), c, "main", "src/a.go", "c.go"); err != nil || got != "src/c.go" {
		t.Fatalf("file: %q, %v", got, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "src/c.go")); err != nil || string(b) != "a\n" {
		t.Errorf("renamed: %q, %v", b, err)
	}
	if got, err := RenameEntry(t.Context(), c, "main", "src", "lib"); err != nil || got != "lib" {
		t.Fatalf("directory: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "lib/inner")); err != nil {
		t.Errorf("what the directory holds: %v", err)
	}
	if _, err := RenameEntry(t.Context(), c, "main", "lib/c.go", "b.go"); !errors.As(err, new(*ExistsError)) {
		t.Errorf("onto another file: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "lib/b.go")); string(b) != "b\n" {
		t.Errorf("the other file: %q", b)
	}
	for _, name := range []string{"", "x/y", "..", ".", ".git"} {
		if _, err := RenameEntry(t.Context(), c, "main", "lib/c.go", name); !errors.As(err, new(*InvalidNameError)) {
			t.Errorf("to %q: %v", name, err)
		}
	}
	if _, err := RenameEntry(t.Context(), c, "main", "lib/gone", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("not there: %v", err)
	}
	for _, path := range []string{".", "", ".git", "../x"} {
		if _, err := RenameEntry(t.Context(), c, "main", path, "x"); !isInvalidPath(err) {
			t.Errorf("%q: %v", path, err)
		}
	}
}

func TestRemoveEntry(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	c := Checkouts{Clone: dir, Paths: []string{dir}}
	if err := os.MkdirAll(filepath.Join(dir, "src/inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "src/inner/a.go", "a\n")
	write(t, dir, "target", "kept\n")
	if err := os.Symlink("target", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveEntry(t.Context(), c, "main", "file"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveEntry(t.Context(), c, "main", "src"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveEntry(t.Context(), c, "main", "link"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"file", "src", "link"} {
		if _, err := os.Lstat(filepath.Join(dir, path)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s still there: %v", path, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "target")); string(b) != "kept\n" {
		t.Errorf("the link's target: %q", b)
	}
	if err := RemoveEntry(t.Context(), c, "main", "gone"); !errors.Is(err, ErrNotFound) {
		t.Errorf("not there: %v", err)
	}
	for _, path := range []string{".", "", ".git", ".git/HEAD", "../x"} {
		if err := RemoveEntry(t.Context(), c, "main", path); !isInvalidPath(err) {
			t.Errorf("%q: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".git/HEAD")); err != nil {
		t.Errorf("the repository: %v", err)
	}
}
