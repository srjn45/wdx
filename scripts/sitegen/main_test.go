package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/runtest"
)

func TestMain(m *testing.M) {
	os.Exit(runtest.Main(m))
}

// TestReferenceCurrent fails when the website's reference pages are not what
// sitegen writes from this tree: a command's help, a setting, an error code or
// the changelog changed without make site-ref.
func TestReferenceCurrent(t *testing.T) {
	t.Parallel()
	const root = "../.."
	bin := filepath.Join(t.TempDir(), "wdx")
	if err := build(root, bin, runtest.OriginalEnviron()); err != nil { // the go command's caches are the machine's
		t.Fatal(err)
	}
	want, err := generate(bin, root)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, referenceDir)
	have := map[string]string{}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		name, _ := filepath.Rel(dir, path)
		have[name] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var stale []string
	for name, page := range want {
		if have[name] != page {
			stale = append(stale, name)
		}
	}
	for name := range have {
		if _, ok := want[name]; !ok {
			stale = append(stale, name+" (no longer written)")
		}
	}
	if len(stale) > 0 {
		slices.Sort(stale)
		t.Fatalf("the website's reference is stale; run make site-ref and commit %s. Pages that differ: %v", referenceDir, stale)
	}
}
