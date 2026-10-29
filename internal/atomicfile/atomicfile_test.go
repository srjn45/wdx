package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantFile(t, path, "new", 0o600)
	wantOnly(t, dir, "config.yaml")
}

func TestCopy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src, dest := filepath.Join(dir, "src"), filepath.Join(dir, "dest")
	if err := os.WriteFile(src, []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Copy(dest, src, 0o755); err != nil {
		t.Fatal(err)
	}
	wantFile(t, dest, "binary", 0o755)
	wantOnly(t, dir, "dest", "src")
}

// TestFailureLeavesTarget fails to copy a file that is not there: the
// target keeps its content, and no temporary file is left.
func TestFailureLeavesTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dest := filepath.Join(dir, "dest")
	if err := os.WriteFile(dest, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Copy(dest, filepath.Join(dir, "missing"), 0o600); err == nil {
		t.Fatal("want an error")
	}
	if err := Write(filepath.Join(dir, "no", "such", "dir"), nil, 0o600); err == nil {
		t.Fatal("want an error")
	}
	wantFile(t, dest, "kept", 0o600)
	wantOnly(t, dir, "dest")
}

func wantFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != content || info.Mode().Perm() != mode {
		t.Fatalf("%s: got %q, %v; want %q, %v", path, b, info.Mode().Perm(), content, mode)
	}
}

func wantOnly(t *testing.T, dir string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if len(got) != len(names) {
		t.Fatalf("%s holds %v, want %v", dir, got, names)
	}
	for i := range got {
		if got[i] != names[i] {
			t.Fatalf("%s holds %v, want %v", dir, got, names)
		}
	}
}
