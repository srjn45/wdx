package aca_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

// cursorChat is a chat's meta.json, from the sample in
// testdata/cursor/2026.11.05/session, with the fields a test changes.
type cursorChat struct {
	hash, id, cwd   string
	hasConversation bool
	updatedAtMs     int64
}

// cursorBackup is a backup whose config folder holds the chats, as Cursor keeps them.
func cursorBackup(t *testing.T, chats ...cursorChat) string {
	t.Helper()
	sample, err := os.ReadFile(filepath.Join("testdata", "cursor", "2026.11.05", "session", "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	backup := t.TempDir()
	for _, c := range chats {
		var meta map[string]any
		if err := json.Unmarshal(sample, &meta); err != nil {
			t.Fatal(err)
		}
		meta["cwd"], meta["hasConversation"], meta["updatedAtMs"] = c.cwd, c.hasConversation, c.updatedAtMs
		data, err := json.Marshal(meta)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(backup, "cursor-config", "chats", c.hash, c.id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "meta.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return backup
}

// cursorConfigOnly is a backup whose config folder holds no chats yet, as a start
// leaves it.
func cursorConfigOnly(t *testing.T) string {
	t.Helper()
	backup := t.TempDir()
	if err := os.MkdirAll(filepath.Join(backup, "cursor-config"), 0o700); err != nil {
		t.Fatal(err)
	}
	return backup
}

const (
	cursorChatA = "3f0c5a52-9a51-4c2e-9f39-2d2f8e4b7a01"
	cursorChatB = "8b1d2e43-6c7f-4a90-b1e2-5f3a9c0d4e02"
	cursorChatC = "c2e7f8a9-1b3d-4e5f-8a6b-7c9d0e1f2a03"
)

// The chat updated last that has a conversation is the session, and its directory
// where it is resumed; one with no message yet, as /clear leaves, and one whose
// meta.json cannot be read are passed over. With no transcript yet, it says so.
func TestCursorBackUpFindsTheChat(t *testing.T) {
	t.Parallel()
	backup := cursorBackup(t,
		cursorChat{"h1", cursorChatA, "/work/repo", true, 100},
		cursorChat{"h2", cursorChatB, "/work/other", true, 200},
		cursorChat{"h2", cursorChatC, "/work/other", false, 300},
	)
	broken := filepath.Join(backup, "cursor-config", "chats", "h3", "d4e5f6a7-0000-4000-8000-000000000004")
	if err := os.MkdirAll(broken, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "meta.json"), []byte(`{"hasConversation": tr`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := aca.Cursor{}.BackUp(t.Context(), aca.Launch{BackupDir: backup, SessionID: "wdx-own"})
	if !errors.Is(err, aca.ErrNotSupported) {
		t.Fatalf("err = %v, want ErrNotSupported", err)
	}
	if want := (aca.Backup{Session: cursorChatB, Dir: "/work/other"}); got != want {
		t.Fatalf("backup = %+v, want %+v", got, want)
	}
}

// Before the first message, no chat has a conversation, and the agent keeps wdx's id.
func TestCursorBackUpBeforeTheFirstMessage(t *testing.T) {
	t.Parallel()
	for name, backup := range map[string]string{
		"no config folder": t.TempDir(),
		"an empty chat":    cursorBackup(t, cursorChat{"h1", cursorChatA, "/work/repo", false, 100}),
		"no chats folder":  cursorConfigOnly(t),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := aca.Cursor{}.BackUp(t.Context(), aca.Launch{BackupDir: backup, SessionID: "wdx-own"})
			if !errors.Is(err, aca.ErrNotSupported) {
				t.Fatalf("err = %v, want ErrNotSupported", err)
			}
			if got != (aca.Backup{}) {
				t.Fatalf("backup = %+v, want none", got)
			}
		})
	}
}

// A chat of the agent's config folder made in l.Dir is resumed; wdx's own id, or a
// chat made in another directory, where --resume would bring up an empty chat with the
// same id, starts a new chat.
func TestCursorCommandResumes(t *testing.T) {
	t.Parallel()
	backup := cursorBackup(t,
		cursorChat{"h1", cursorChatA, "/work/repo", true, 100},
		cursorChat{"h2", cursorChatB, "/work/gone", true, 200},
		// The same id under a second directory's folder, as a resume elsewhere leaves.
		cursorChat{"h3", cursorChatA, "/work/elsewhere", false, 300},
	)
	for _, c := range []struct {
		name, session, dir string
		resume             bool
	}{
		{"its chat, in its directory", cursorChatA, "/work/repo", true},
		{"wdx's own id, before the first message", "wdx-own", "/work/repo", false},
		{"its chat, from another directory", cursorChatB, "/work/repo", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := aca.Cursor{}.Command(t.Context(), aca.Launch{BackupDir: backup, SessionID: c.session, Dir: c.dir, Note: "use wdx"})
			if err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(backup, "cursor-config")
			want := []string{"env", "CURSOR_CONFIG_DIR=" + config, "cursor-agent", "--trust"}
			if c.resume {
				want = append(want, "--resume", c.session)
			}
			want = append(want, "--plugin-dir", filepath.Join(backup, "cursor-plugin"))
			if !slices.Equal(got, want) {
				t.Fatalf("command = %q, want %q", got, want)
			}
		})
	}
}
