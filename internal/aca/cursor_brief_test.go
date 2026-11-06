package aca_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

func cursorBrief(t *testing.T, backup string, limit int) (aca.Brief, string) {
	t.Helper()
	got, err := aca.Cursor{}.Brief(t.Context(), aca.Launch{BackupDir: backup}, limit)
	if err != nil {
		t.Fatal(err)
	}
	return got, strings.Join(got.Start, "\n") + "\n" + strings.Join(got.Rest, "\n")
}

// The sample's brief starts at its first request and tells its tool calls in a few
// words, CallDynamicTool by the tool it calls.
func TestCursorBriefOfTheSample(t *testing.T) {
	t.Parallel()
	backup := t.TempDir()
	newCursorStore(t, backup, "/work/repo", cursorSampleChat, 100).
		setRoot(readCursorMessages(t, filepath.Join(cursorSamples, "session", "store-messages.jsonl")))
	cursorBackUp(t, backup, cursorSampleChat, "/work/repo")
	got, text := cursorBrief(t, backup, 64<<10)
	lines := strings.Split(readFile(t, filepath.Join(backup, aca.TranscriptFile)), "\n")
	if got.From != 2 || !strings.HasPrefix(lines[got.From], "## User · ") {
		t.Errorf("from %d: %q", got.From, lines[got.From])
	}
	if got.Next != int64(len(lines)-1) || got.OmittedCount != 0 {
		t.Errorf("next = %d, want %d; omitted %d", got.Next, len(lines)-1, got.OmittedCount)
	}
	wantInOrder(t, text, "- Shell sleep 8; echo SLEPT (line ", "- CallDynamicTool Task (line ")
	checkGolden(t, text, filepath.Join(cursorSamples, "session", "brief.md"))
}

// Cursor's compaction keeps a summary, so the brief starts there.
func TestCursorBriefStartsAtTheCompaction(t *testing.T) {
	t.Parallel()
	backup := t.TempDir()
	store := newCursorStore(t, backup, "/work/repo", cursorCompactionChat, 100)
	store.setRoot(readCursorMessages(t, filepath.Join(cursorSamples, "compaction", "store-messages-before.jsonl")))
	cursorBackUp(t, backup, cursorCompactionChat, "/work/repo")
	store.setRoot(readCursorMessages(t, filepath.Join(cursorSamples, "compaction", "store-messages.jsonl")))
	cursorBackUp(t, backup, cursorCompactionChat, "/work/repo")
	got, text := cursorBrief(t, backup, 64<<10)
	if !strings.HasPrefix(got.Start[0], "## Compacted · ") {
		t.Fatalf("starts at %q:\n%s", got.Start, text)
	}
	wantInOrder(t, text, "Primary Request and Intent", "Reply with only AFTER.", "AFTER")
	wantNot(t, text, "## User · 2026-11-06T13:11")
}

// A restart's chat goes on from what came before, and a /clear's does not, whichever
// came first.
func TestCursorBriefIsBoundedByClearNotARestart(t *testing.T) {
	t.Parallel()
	session := func(id, request string, minute int) string {
		return "# Session " + id + " · cursor\n\n" + message("User", minute, request) + message("Assistant", minute, "done: "+request)
	}
	earlier := cursorChat{"h1", cursorChatA, "/work/repo", true, 100}
	chat := cursorChat{"h1", cursorChatB, "/work/repo", true, 200}
	later := cursorChat{"h1", cursorChatC, "/work/repo", true, 300}
	for _, c := range []struct {
		name     string
		chats    [][]cursorChat // between restarts
		from     string
		notFound []string
	}{
		{name: "restart", chats: [][]cursorChat{{earlier}, {chat}}, from: "the earlier request"},
		{name: "clear", chats: [][]cursorChat{{earlier, chat}}, from: "the chat's request", notFound: []string{"the earlier request"}},
		{name: "restart after clear", chats: [][]cursorChat{{earlier, chat}, {later}}, from: "the chat's request", notFound: []string{"the earlier request"}},
		{name: "clear after restart", chats: [][]cursorChat{{earlier}, {chat, later}}, from: "the later request", notFound: []string{"the earlier request", "the chat's request"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			transcript := session(cursorChatA, "the earlier request", 1) + session(cursorChatB, "the chat's request", 2) + session(cursorChatC, "the later request", 3)
			backup := cursorConfigOnly(t)
			if err := os.WriteFile(filepath.Join(backup, aca.TranscriptFile), []byte(transcript), 0o600); err != nil {
				t.Fatal(err)
			}
			for i, chats := range c.chats {
				if i > 0 {
					beginCursorSession(t, backup, cursorRestarted)
				}
				addCursorChats(t, backup, chats...)
			}
			got, text := cursorBrief(t, backup, 64<<10)
			if !strings.HasPrefix(text, "## User") || !strings.Contains(got.Start[2], c.from) {
				t.Errorf("starts at %q, want %q:\n%s", got.Start, c.from, text)
			}
			wantInOrder(t, text, c.from, "done: the later request")
			wantNot(t, text, c.notFound...)
		})
	}
}
