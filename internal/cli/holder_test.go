package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/holder/holdertest"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/waittest"
)

// startHolder starts the wdx binary's holder running a shell that waits for a
// byte and exits, as the daemon starts it, with the ACA id given.
func startHolder(t *testing.T, id, acaID string) (home string, conn *holder.Conn) {
	t.Helper()
	home = shortHome(t)
	shell := filepath.Join(t.TempDir(), "shell")
	if err := runtest.WriteExecutable(shell, "#!/bin/sh\nprintf 'running\\n'\nread reply\nexit 0\n"); err != nil {
		t.Fatal(err)
	}
	ctx := holder.WithLimits(context.Background(), holdertest.Limits(t))
	if _, err := holder.Start(ctx, holder.StartOptions{
		Executable: wdxBinary, Home: home, ID: id, Project: "app", Dir: t.TempDir(), Shell: shell, ACA: acaID,
		Env: []string{"PATH=/usr/bin:/bin"},
	}); err != nil {
		t.Fatal(err)
	}
	conn, err := holder.Dial(ctx, identity.HolderSocket(home, id), id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return home, conn
}

// runsItsTerminal attaches, types what ends the shell, and ends the holder.
func runsItsTerminal(t *testing.T, home, id string, conn *holder.Conn) {
	t.Helper()
	ctx := holder.WithLimits(context.Background(), holdertest.Limits(t))
	s, err := holder.Attach(ctx, identity.HolderSocket(home, id), id, holder.Size{Cols: 80, Rows: 24}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var seen strings.Builder
	buf := make([]byte, 4096)
	for !strings.Contains(seen.String(), "running") {
		n, err := s.Read(buf)
		if err != nil {
			t.Fatalf("read: %v, after %q", err, seen.String())
		}
		seen.Write(buf[:n])
	}
	if _, err := s.Write([]byte("\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-conn.Exited():
	case <-waittest.After(t):
		t.Fatal("the shell did not exit")
	}
	if err := conn.Release(); err != nil {
		t.Fatal(err)
	}
}

func holderLogs(t *testing.T, home string) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(identity.LogsDir(home), "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		all.Write(b)
	}
	return all.String()
}

func TestHolderUnknownACAStillRunsTheTerminal(t *testing.T) {
	t.Parallel()
	const id = "term-00000301"
	home, conn := startHolder(t, id, "no_such_aca")
	runsItsTerminal(t, home, id, conn)
	logs := holderLogs(t, home)
	if !strings.Contains(logs, "screen not read: this holder does not know the aca") || !strings.Contains(logs, "no_such_aca") {
		t.Errorf("no word of why the screen is not read:\n%s", logs)
	}
	if strings.Contains(logs, "screen watch started") {
		t.Errorf("a watch with no parser:\n%s", logs)
	}
}

func TestHolderKnownACAWatchesTheScreen(t *testing.T) {
	t.Parallel()
	const id = "term-00000302"
	home, conn := startHolder(t, id, "claude_code")
	runsItsTerminal(t, home, id, conn)
	if logs := holderLogs(t, home); !strings.Contains(logs, "screen watch started") {
		t.Errorf("no watch:\n%s", logs)
	}
}

func TestHolderNoACAWatchesNothing(t *testing.T) {
	t.Parallel()
	const id = "term-00000303"
	home, conn := startHolder(t, id, "")
	runsItsTerminal(t, home, id, conn)
	if logs := holderLogs(t, home); strings.Contains(logs, "screen watch started") || strings.Contains(logs, "screen not read") {
		t.Errorf("a shell's screen is watched:\n%s", logs)
	}
}
