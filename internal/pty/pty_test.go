package pty

import (
	"io"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/term"
)

func TestStart(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("/bin/sh", "-c", "stty size; tty")
	master, err := Start(cmd, 91, 17)
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	out, _ := io.ReadAll(master) // ends with an error once the shell is gone
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Fields(string(out)); len(lines) != 3 || lines[0] != "17" || lines[1] != "91" || !strings.HasPrefix(lines[2], "/dev/") {
		t.Fatalf("got %q, want the size and a terminal", out)
	}
}

// TestOpen opens many PTYs at once, each its own: a stale number would
// open one twice.
func TestOpen(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for range 50 {
		master, slave, err := Open()
		if err != nil {
			t.Fatal(err)
		}
		defer master.Close()
		defer slave.Close()
		if seen[slave.Name()] {
			t.Fatalf("%s opened twice", slave.Name())
		}
		seen[slave.Name()] = true
		if err := Setsize(master, 40, 10); err != nil {
			t.Fatal(err)
		}
		if cols, rows, err := term.GetSize(int(slave.Fd())); err != nil || cols != 40 || rows != 10 {
			t.Fatalf("got %d×%d, %v", cols, rows, err)
		}
	}
}
