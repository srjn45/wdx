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
