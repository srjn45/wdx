package clikittest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLogLinesLeaveALineBeingWritten(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "wdx.log")
	line := `{"time":"2026-11-06T11:06:24Z","level":"INFO","msg":"m","debug_id":"dbg-1","process":"daemon"}` + "\n"
	half := len(line) / 2
	if err := os.WriteFile(path, []byte(line+line[:half]), 0o600); err != nil {
		t.Fatal(err)
	}
	var f logFile
	f.readMore(t, path, nil)
	if len(f.lines) != 1 {
		t.Fatalf("read %d lines with the second half written, want 1", len(f.lines))
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(line[half:]); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	f.readMore(t, path, nil)
	if len(f.lines) != 2 {
		t.Fatalf("read %d lines once the second was whole, want 2", len(f.lines))
	}
}
