package aca_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

const (
	antigravityRestarted = "7b2d1e0f-3c80-4d2f-8e4b-6a9c3f5d8b21" // wdx's id for a restart, with no db of Antigravity's
	antigravityAgain     = "8c3e2f10-4d91-4e30-9f5c-7b0d4a6e9c32" // a second restart's
	antigravityLater     = "00000000-0000-0000-0000-000000000004"
	antigravitySub2      = "9d4f3a21-5ea2-4f41-8a6d-8c1e5b7fad43"
)

func beginAntigravitySession(t *testing.T, backup, session string) {
	t.Helper()
	if err := (aca.Antigravity{}).BeginSession(t.Context(), aca.Launch{BackupDir: backup}, session); err != nil {
		t.Fatal(err)
	}
}

func TestAntigravityCanBeginASession(t *testing.T) {
	t.Parallel()
	if !aca.CanBeginSession(aca.Antigravity{}) {
		t.Fatal("Antigravity cannot begin a session")
	}
}

// The line counts each conversation the hooks file names once, a subagent's among
// them, however often it started.
func TestAntigravityBeginSessionRecordsTheRestart(t *testing.T) {
	t.Parallel()
	backup := t.TempDir()
	beginAntigravitySession(t, backup, antigravityAgain)
	writeHooksFile(t, backup, antigravityStartLine(antigravityFirst), antigravityStartLine(antigravitySub), antigravityStartLine(antigravityFirst))
	beginAntigravitySession(t, backup, antigravityRestarted)
	want := `{"session":"` + antigravityAgain + `","conversations":0}` + "\n" + `{"session":"` + antigravityRestarted + `","conversations":2}` + "\n"
	if got := readFile(t, filepath.Join(backup, "antigravity-restarts.jsonl")); got != want {
		t.Fatalf("restarts file:\n%s\nwant:\n%s", got, want)
	}
}

// Until a conversation of the user's comes after the restart, the agent is in the
// restart's session: past the conversation before it started again, and a subagent's.
func TestAntigravityBackUpFollowsARestart(t *testing.T) {
	t.Parallel()
	backup, home := t.TempDir(), t.TempDir()
	lines, project := antigravityRecordedStarts(t, backup)
	writeHooksFile(t, backup, lines[0])
	beginAntigravitySession(t, backup, antigravityRestarted)
	if got := antigravityBackUp(t, home, backup); got != (aca.Backup{Session: antigravityRestarted}) {
		t.Fatalf("before any message, got %+v, want the restart's session", got)
	}
	writeAntigravitySubagent(t, home, antigravityFirst, antigravitySub, "")
	sub := strings.ReplaceAll(lines[1], antigravityCleared, antigravitySub)
	writeHooksFile(t, backup, lines[0], lines[0], sub)
	if got := antigravityBackUp(t, home, backup); got != (aca.Backup{Session: antigravityRestarted}) {
		t.Fatalf("after the old conversation and a subagent, got %+v, want the restart's session", got)
	}
	writeHooksFile(t, backup, lines[0], lines[0], sub, lines[1])
	if got := antigravityBackUp(t, home, backup); got != (aca.Backup{Session: antigravityCleared, Dir: project}) {
		t.Fatalf("after the first message, got %+v, want %s", got, antigravityCleared)
	}
}

// The second of two restarts with no message between is the session.
func TestAntigravityTwoRestartsWithNoMessageBetween(t *testing.T) {
	t.Parallel()
	backup := t.TempDir()
	writeHooksFile(t, backup, antigravityStartLine(antigravityFirst))
	beginAntigravitySession(t, backup, antigravityRestarted)
	beginAntigravitySession(t, backup, antigravityAgain)
	if got := antigravityBackUp(t, t.TempDir(), backup); got.Session != antigravityAgain {
		t.Fatalf("got %+v, want the second restart's session", got)
	}
	writeHooksFile(t, backup, antigravityStartLine(antigravityFirst), antigravityStartLine(antigravityCleared))
	if got := antigravityBackUp(t, t.TempDir(), backup); got.Session != antigravityCleared {
		t.Fatalf("got %+v, want %s", got, antigravityCleared)
	}
}

// A backup writes no restarts file.
func TestAntigravityBackUpWritesNoRestarts(t *testing.T) {
	t.Parallel()
	backup := t.TempDir()
	writeHooksFile(t, backup, antigravityStartLine(antigravityFirst), antigravityStartLine(antigravityCleared))
	if got := antigravityBackUp(t, t.TempDir(), backup); got.Session != antigravityCleared {
		t.Fatalf("got %+v, want %s", got, antigravityCleared)
	}
	if _, err := os.Stat(filepath.Join(backup, "antigravity-restarts.jsonl")); !os.IsNotExist(err) {
		t.Errorf("a backup wrote the restarts file: %v", err)
	}
}
