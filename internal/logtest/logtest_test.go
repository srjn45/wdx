package logtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/identity"
)

func TestTail(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir := identity.LogsDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var day1, day2 strings.Builder
	for i := range 3 {
		fmt.Fprintf(&day1, `{"time":"2026-10-29T10:00:0%d.123456Z","level":"INFO","msg":"first %d","debug_id":"dbg-1"}`+"\n", i, i)
	}
	fmt.Fprintf(&day2, `{"time":"2026-10-30T09:15:00.5Z","level":"ERROR","msg":"failed","err":"boom","n":3}`+"\nnot json\n")
	for name, s := range map[string]string{"wdx-2026-10-29.log": day1.String(), "wdx-2026-10-30.log": day2.String()} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Tail(home, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`10:00:02.123 INFO first 2 debug_id="dbg-1"`,
		`09:15:00.5 ERROR failed err="boom" n=3`,
		"not json",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if lines, err := Tail(t.TempDir(), 3); err != nil || len(lines) != 0 {
		t.Fatalf("no logs: %v, %v", lines, err)
	}
}

// fataler records what Scan failed with.
type fataler struct{ failed string }

func (f *fataler) Helper() {}

func (f *fataler) Fatalf(format string, args ...any) { f.failed = fmt.Sprintf(format, args...) }

func TestScan(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "hub.log")
	if err := os.WriteFile(path, []byte("one\ntwo\n"+strings.Repeat("x", 100)+"\nafter\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var lines []string
	var f fataler
	Scan(&f, path, 200, func(line []byte) { lines = append(lines, string(line)) })
	if f.failed != "" || len(lines) != 4 || lines[3] != "after" {
		t.Fatalf("want the four lines, got %q, failed %q", lines, f.failed)
	}

	lines, f = nil, fataler{}
	Scan(&f, path, 50, func(line []byte) { lines = append(lines, string(line)) })
	if !strings.Contains(f.failed, path) || !strings.Contains(f.failed, "too long") || len(lines) != 2 {
		t.Fatalf("want the line over the limit failing with the file named, got %q, failed %q", lines, f.failed)
	}

	// A directory opens, and fails at the first read.
	f = fataler{}
	Scan(&f, dir, 50, func([]byte) { t.Error("a line read from a directory") })
	if !strings.Contains(f.failed, dir) {
		t.Fatalf("want a failed read naming the file, got %q", f.failed)
	}
}
