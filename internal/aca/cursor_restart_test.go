package aca_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

const (
	cursorRestarted = "6a1c0d9e-2b7f-4c1e-9d3a-5f8b2e4c7a10" // wdx's id for a restart, with no chat of Cursor's
	cursorAgain     = "7b2d1e0f-3c80-4d2f-8e4b-6a9c3f5d8b21" // a second restart's
)

func beginCursorSession(t *testing.T, backup, session string) {
	t.Helper()
	if err := (aca.Cursor{}).BeginSession(t.Context(), aca.Launch{BackupDir: backup, SessionID: "wdx-own"}, session); err != nil {
		t.Fatal(err)
	}
}

func cursorSessionAfter(t *testing.T, backup string) aca.Backup {
	t.Helper()
	got, err := aca.Cursor{}.BackUp(t.Context(), aca.Launch{BackupDir: backup, SessionID: "wdx-own"})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestCursorCanBeginASession(t *testing.T) {
	t.Parallel()
	if !aca.CanBeginSession(aca.Cursor{}) {
		t.Fatal("Cursor cannot begin a session")
	}
}

// The line counts the chats that have a conversation, not one a start or /clear left
// with no message.
func TestCursorBeginSessionRecordsTheRestart(t *testing.T) {
	t.Parallel()
	backup := cursorConfigOnly(t)
	beginCursorSession(t, backup, cursorAgain)
	addCursorChats(t, backup,
		cursorChat{"h1", cursorChatA, "/work/repo", true, 100},
		cursorChat{"h1", cursorChatB, "/work/repo", true, 200},
		cursorChat{"h1", cursorChatC, "/work/repo", false, 300},
	)
	beginCursorSession(t, backup, cursorRestarted)
	want := `{"session":"` + cursorAgain + `","chats":0}` + "\n" + `{"session":"` + cursorRestarted + `","chats":2}` + "\n"
	if got := readFile(t, filepath.Join(backup, "cursor-restarts.jsonl")); got != want {
		t.Fatalf("restarts file:\n%s\nwant:\n%s", got, want)
	}
}

// Until a chat made after the restart has a message, the agent is in the restart's
// session, even when a chat from before it is updated; then it is in that chat.
func TestCursorBackUpFollowsARestart(t *testing.T) {
	t.Parallel()
	backup := cursorBackup(t, cursorChat{"h1", cursorChatA, "/work/repo", true, 100})
	beginCursorSession(t, backup, cursorRestarted)
	if got := cursorSessionAfter(t, backup); got != (aca.Backup{Session: cursorRestarted}) {
		t.Fatalf("before any chat, got %+v, want the restart's session", got)
	}
	addCursorChats(t, backup, cursorChat{"h1", cursorChatB, "/work/repo", false, 200})
	if got := cursorSessionAfter(t, backup); got != (aca.Backup{Session: cursorRestarted}) {
		t.Fatalf("before the new chat's message, got %+v, want the restart's session", got)
	}
	addCursorChats(t, backup, cursorChat{"h1", cursorChatA, "/work/repo", true, 300})
	if got := cursorSessionAfter(t, backup); got != (aca.Backup{Session: cursorRestarted}) {
		t.Fatalf("after the old chat was updated, got %+v, want the restart's session", got)
	}
	addCursorChats(t, backup, cursorChat{"h1", cursorChatB, "/work/repo/sub", true, 400})
	if got := cursorSessionAfter(t, backup); got != (aca.Backup{Session: cursorChatB, Dir: "/work/repo/sub"}) {
		t.Fatalf("after the first message, got %+v, want %s", got, cursorChatB)
	}
	// Chats count in the order they were made, not updated.
	addCursorChats(t, backup, cursorChat{"h1", cursorChatA, "/work/repo", true, 500})
	if got := cursorSessionAfter(t, backup); got != (aca.Backup{Session: cursorChatB, Dir: "/work/repo/sub"}) {
		t.Fatalf("after the old chat was updated again, got %+v, want %s", got, cursorChatB)
	}
}

// The second of two restarts with no chat between them is the session, and the chat
// that comes after both is the session once it has a message.
func TestCursorTwoRestartsWithNoChatBetween(t *testing.T) {
	t.Parallel()
	backup := cursorBackup(t, cursorChat{"h1", cursorChatA, "/work/repo", true, 100})
	beginCursorSession(t, backup, cursorRestarted)
	beginCursorSession(t, backup, cursorAgain)
	if got := cursorSessionAfter(t, backup); got.Session != cursorAgain {
		t.Fatalf("got %+v, want the second restart's session", got)
	}
	addCursorChats(t, backup, cursorChat{"h1", cursorChatB, "/work/repo", true, 200})
	if got := cursorSessionAfter(t, backup); got.Session != cursorChatB {
		t.Fatalf("got %+v, want %s", got, cursorChatB)
	}
}

// An agent never restarted, or from before restarts were recorded, is in the chat
// updated last, and a backup writes no restarts file.
func TestCursorWithNoRestartsFile(t *testing.T) {
	t.Parallel()
	backup := cursorBackup(t,
		cursorChat{"h1", cursorChatA, "/work/repo", true, 100},
		cursorChat{"h1", cursorChatB, "/work/repo", true, 200},
	)
	if got := cursorSessionAfter(t, backup); got.Session != cursorChatB {
		t.Fatalf("got %+v, want %s", got, cursorChatB)
	}
	if _, err := os.Stat(filepath.Join(backup, "cursor-restarts.jsonl")); !os.IsNotExist(err) {
		t.Errorf("a backup wrote the restarts file: %v", err)
	}
}
