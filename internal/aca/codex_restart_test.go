package aca_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

const (
	codexRestarted = "6a1c0d9e-2b7f-4c1e-9d3a-5f8b2e4c7a10" // wdx's id for a restart, with no file of Codex's
	codexAgain     = "7b2d1e0f-3c80-4d2f-8e4b-6a9c3f5d8b21" // a second restart's
	codexLater     = "01a1ab90-0000-7000-8000-000000000003"
	codexNamer2    = "01a1ab91-0000-7000-8000-000000000004"
)

func beginCodexSession(t *testing.T, l aca.Launch, session string) {
	t.Helper()
	if err := (aca.Codex{}).BeginSession(t.Context(), l, session); err != nil {
		t.Fatal(err)
	}
}

func codexSessionAfter(t *testing.T, l aca.Launch) aca.Backup {
	t.Helper()
	got, err := aca.Codex{}.BackUp(t.Context(), l)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func codexFromNew(t *testing.T, backup string, user ...string) map[string]bool {
	t.Helper()
	got, err := aca.CodexNewThreads(backup, func(id string) bool { return slices.Contains(user, id) })
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestCodexCanBeginASession(t *testing.T) {
	t.Parallel()
	if !aca.CanBeginSession(aca.Codex{}) {
		t.Fatal("Codex cannot begin a session")
	}
}

// The line counts every thread the turns file names, the naming thread among them.
func TestCodexBeginSessionRecordsTheRestart(t *testing.T) {
	t.Parallel()
	backup := t.TempDir()
	l := aca.Launch{UserHome: t.TempDir(), BackupDir: backup, SessionID: codexSession}
	beginCodexSession(t, l, codexAgain)
	writeTurns(t, backup, codexEarlier, codexSession, codexNamer)
	beginCodexSession(t, l, codexRestarted)
	data, err := os.ReadFile(filepath.Join(backup, "codex-restarts.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"session":"` + codexAgain + `","threads":0}` + "\n" + `{"session":"` + codexRestarted + `","threads":3}` + "\n"
	if string(data) != want {
		t.Fatalf("restarts file:\n%s\nwant:\n%s", data, want)
	}
}

// Until a user thread comes after the restart, the agent is in the restart's session,
// however many threads that are not the user's come first.
func TestCodexBackUpFollowsARestart(t *testing.T) {
	t.Parallel()
	home := codexHome(t, map[string]struct{ source, cwd string }{
		codexEarlier:  {"user", ""},
		codexSession:  {"user", "/work/repo/sub"},
		codexSubagent: {"subagent", ""},
	})
	backup := t.TempDir()
	l := aca.Launch{UserHome: home, BackupDir: backup, SessionID: codexEarlier}
	writeTurns(t, backup, codexEarlier, codexNamer)
	beginCodexSession(t, l, codexRestarted)
	if got := codexSessionAfter(t, l); got != (aca.Backup{Session: codexRestarted}) {
		t.Fatalf("before any turn, got %+v, want the restart's session", got)
	}
	writeTurns(t, backup, codexEarlier, codexNamer, codexSubagent, codexNamer2)
	if got := codexSessionAfter(t, l); got != (aca.Backup{Session: codexRestarted}) {
		t.Fatalf("before the user's turn, got %+v, want the restart's session", got)
	}
	// Even a turn of the thread before the restart is not the new conversation.
	writeTurns(t, backup, codexEarlier, codexNamer, codexSubagent, codexNamer2, codexEarlier)
	if got := codexSessionAfter(t, l); got != (aca.Backup{Session: codexRestarted}) {
		t.Fatalf("after the old thread's turn, got %+v, want the restart's session", got)
	}
	writeTurns(t, backup, codexEarlier, codexNamer, codexSubagent, codexNamer2, codexSession)
	if got := codexSessionAfter(t, l); got != (aca.Backup{Session: codexSession, Dir: "/work/repo/sub"}) {
		t.Fatalf("after the first turn, got %+v, want %s", got, codexSession)
	}
	if got := codexFromNew(t, backup, codexEarlier, codexSession); len(got) != 0 {
		t.Errorf("from /new: %v, want none", got)
	}
}

// The second of two restarts with no turn between them is the session, and the thread
// that comes after both is a restart's.
func TestCodexTwoRestartsWithNoTurnBetween(t *testing.T) {
	t.Parallel()
	home := codexHome(t, map[string]struct{ source, cwd string }{codexEarlier: {"user", ""}, codexSession: {"user", ""}})
	backup := t.TempDir()
	l := aca.Launch{UserHome: home, BackupDir: backup, SessionID: codexEarlier}
	writeTurns(t, backup, codexEarlier)
	beginCodexSession(t, l, codexRestarted)
	beginCodexSession(t, l, codexAgain)
	if got := codexSessionAfter(t, l); got.Session != codexAgain {
		t.Fatalf("got %+v, want the second restart's session", got)
	}
	writeTurns(t, backup, codexEarlier, codexSession)
	if got := codexSessionAfter(t, l); got.Session != codexSession {
		t.Fatalf("got %+v, want %s", got, codexSession)
	}
	if got := codexFromNew(t, backup, codexEarlier, codexSession); len(got) != 0 {
		t.Errorf("from /new: %v, want none", got)
	}
}

// Of the user threads after the first, the restart's is the first at or after its
// position, past the threads that are not the user's, and every other came from /new.
func TestCodexThreadsFromNew(t *testing.T) {
	t.Parallel()
	backup := t.TempDir()
	l := aca.Launch{UserHome: t.TempDir(), BackupDir: backup, SessionID: codexEarlier}
	writeTurns(t, backup, codexNamer, codexEarlier)
	beginCodexSession(t, l, codexRestarted)
	writeTurns(t, backup, codexNamer, codexEarlier, codexNamer2, codexSubagent, codexSession, codexLater)
	got := codexFromNew(t, backup, codexEarlier, codexSession, codexLater)
	if len(got) != 1 || !got[codexLater] {
		t.Fatalf("from /new: %v, want only %s", got, codexLater)
	}
}

// An agent from before restarts were recorded: every user thread after the first came
// from /new, and the last is the session.
func TestCodexWithNoRestartsFile(t *testing.T) {
	t.Parallel()
	home := codexHome(t, map[string]struct{ source, cwd string }{codexEarlier: {"user", ""}, codexSession: {"user", ""}})
	backup := t.TempDir()
	writeTurns(t, backup, codexEarlier, codexNamer, codexSession)
	if got := codexSessionAfter(t, aca.Launch{UserHome: home, BackupDir: backup}); got.Session != codexSession {
		t.Fatalf("got %+v, want %s", got, codexSession)
	}
	got := codexFromNew(t, backup, codexEarlier, codexSession)
	if len(got) != 1 || !got[codexSession] {
		t.Fatalf("from /new: %v, want only %s", got, codexSession)
	}
	if _, err := os.Stat(filepath.Join(backup, "codex-restarts.jsonl")); !os.IsNotExist(err) {
		t.Errorf("a backup wrote the restarts file: %v", err)
	}
}
