package aca_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

// message is a message as renderLine writes it, at minute n of an hour.
func message(name string, n int, text string) string {
	return fmt.Sprintf("## %s · 2026-11-01T13:%02d:00.000Z\n\n%s\n\n", name, n, text)
}

func toolCall(name, input, result string, failed bool) string {
	title := "Result"
	if failed {
		title = "Result (error)"
	}
	return fmt.Sprintf("### Tool: %s\n\n```\n%s\n```\n\n### %s\n\n```\n%s\n```\n\n", name, input, title, result)
}

func (b claudeBackup) brief(t *testing.T, limit int, transcript string) (aca.Brief, string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(b.launch.BackupDir, aca.TranscriptFile), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := aca.ClaudeCode{}.Brief(context.Background(), b.launch, limit)
	if err != nil {
		t.Fatal(err)
	}
	return got, strings.Join(got.Start, "\n") + "\n" + strings.Join(got.Rest, "\n")
}

func wantNot(t *testing.T, text string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if strings.Contains(text, part) {
			t.Errorf("%q in the brief:\n%s", part, text)
		}
	}
}

// With no compaction it starts at the first request, and has every message since, with
// no thinking and with each tool call as one line, saying the line it is at.
func TestBriefWithNoCompactionStartsAtTheFirstRequest(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	transcript := "# Session s-1\n\n" +
		message("Assistant", 0, "a greeting nobody asked for") +
		message("User", 1, "fix the login bug") +
		"## Assistant · 2026-11-01T13:02:00.000Z\n\n### Thinking\n\nprivate reasoning\n\nmore of it\n\n### Text\n\nLooking now.\n\n" +
		toolCall("Read", `{"file_path":"login.go","limit":3}`, "SECRET FILE BODY", false) +
		toolCall("Bash", `{"description":"run them","command":"go   test\n./..."}`, "FAIL", true) +
		message("Assistant", 3, "Fixed.")
	got, text := b.brief(t, 64<<10, transcript)
	lines := strings.Split(transcript, "\n")
	if lines[got.From] != "## User · 2026-11-01T13:01:00.000Z" {
		t.Errorf("from %d: %q", got.From, lines[got.From])
	}
	if got.Next != int64(len(lines)-1) {
		t.Errorf("next = %d, want %d", got.Next, len(lines)-1)
	}
	wantInOrder(t, text, "fix the login bug", "## Assistant", "Looking now.", "- Read login.go (line ", "- Bash go test ./... (line ", ", failed)", "Fixed.")
	wantNot(t, text, "a greeting", "private reasoning", "more of it", "### Text", "SECRET FILE BODY", "FAIL\n", `"limit"`)
	// The numbers are where --from reads the call.
	for _, l := range append(got.Start, got.Rest...) {
		var n int
		if name, ok := strings.CutPrefix(l, "- Read "); ok {
			if _, err := fmt.Sscanf(name[strings.Index(name, "(line "):], "(line %d)", &n); err != nil || lines[n] != "### Tool: Read" {
				t.Errorf("%q: line %d is %q, %v", l, n, lines[n], err)
			}
		}
	}
}

func TestBriefStartsAtTheLatestCompaction(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	_, text := b.brief(t, 64<<10, "# Session s-1\n\n"+
		message("User", 1, "the first request")+
		message("Compacted", 2, "an old summary")+
		message("Assistant", 3, "more")+
		message("Compacted", 4, "the latest summary")+
		message("User", 5, "and on"))
	wantInOrder(t, text, "## Compacted · 2026-11-01T13:04", "the latest summary", "and on")
	wantNot(t, text, "the first request", "an old summary", "more\n")
}

// A restart does not stop the brief, which would lose the original request; a /clear
// does, and the restarts since it go on from there.
func TestBriefLooksBackThroughRestartsToAClear(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b := newClaudeBackup(t)
	restart := func(session string) {
		t.Helper()
		if err := (aca.ClaudeCode{}).BeginSession(ctx, b.launch, session); err != nil {
			t.Fatal(err)
		}
		b.launch.SessionID = session
	}
	before := "# Session s-1\n\n" + message("User", 1, "the original request")
	restart("s-2")
	first := before + "# Session s-2\n\n" + message("User", 2, "you were restarted")
	restart("s-3")
	second := first + "# Session s-3\n\n" + message("User", 3, "you were restarted again")
	_, text := b.brief(t, 64<<10, second)
	wantInOrder(t, text, "the original request", "you were restarted", "you were restarted again")

	appendFile(t, filepath.Join(b.launch.BackupDir, "sessions.jsonl"), `{"session_id":"s-4","source":"clear"}`+"\n")
	b.launch.SessionID = "s-4"
	restart("s-5")
	got, text := b.brief(t, 64<<10, second)
	if len(got.Start) != 0 {
		t.Errorf("nothing since the /clear, but: %s", text)
	}
	got, text = b.brief(t, 64<<10, second+"# Session s-5\n\n"+message("User", 5, "since the clear")+message("Assistant", 6, "yes"))
	wantInOrder(t, text, "since the clear", "yes")
	wantNot(t, text, "the original request", "you were restarted")
	if got.From == 0 {
		t.Errorf("from = %d", got.From)
	}
}

// Over the limit the start stays, the oldest messages after it go, and the newest stays.
func TestBriefOverItsLimit(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	padding := strings.Repeat("x", 100)
	transcript := "# Session s-1\n\n" + message("User", 1, "the first request"+padding)
	for i := 2; i < 12; i++ {
		transcript += message("Assistant", i, fmt.Sprintf("message %d %s", i, padding))
	}
	got, text := b.brief(t, 500, transcript)
	wantInOrder(t, text, "the first request", "message 11")
	wantNot(t, text, "message 2 ", "message 3 ")
	lines := strings.Split(transcript, "\n")
	if got.OmittedCount == 0 || lines[got.OmittedFrom] != "## Assistant · 2026-11-01T13:02:00.000Z" {
		t.Fatalf("left out %d from %d", got.OmittedCount, got.OmittedFrom)
	}
	next := lines[got.OmittedFrom+got.OmittedCount]
	if !strings.HasPrefix(got.Rest[0], next) || !strings.HasPrefix(next, "## Assistant") {
		t.Errorf("left out up to %q, kept from %q", next, got.Rest[0])
	}
	// A start and a message that do not fit stay all the same.
	_, text = b.brief(t, 1, transcript)
	wantInOrder(t, text, "the first request", "message 11")
}

// Message text is not fenced, and a conversation can be about this very format. So a
// quoted heading moves nothing, and an unbalanced fence swallows nothing.
func TestBriefIsNotFooledByWhatAMessageQuotes(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	quoted := "before the quote\n\n## Compacted · 2026-11-01T13:00:00.000Z\n\nnot a summary\n\n```\nan unbalanced fence\n\n### Tool: Bash\n\nno fence under it\n\n### Result\n\nno call to answer\n\n## User · someday\n\nthe end of the quote"
	transcript := "# Session s-1\n\n" +
		message("User", 1, "the first request") +
		message("Assistant", 2, quoted) +
		toolCall("Read", `{"file_path":"f.go"}`, "```` a result quoting a fence\n## Compacted · 2026-11-01T13:59:00.000Z", false) +
		message("Assistant", 3, "after it all")
	_, text := b.brief(t, 64<<10, transcript)
	wantInOrder(t, text, "the first request", "before the quote", "not a summary", "an unbalanced fence",
		"### Tool: Bash", "no fence under it", "### Result", "no call to answer", "the end of the quote", "- Read f.go (line ", "after it all")
	wantNot(t, text, "a result quoting")
}

// What the backup writes is what the brief reads: thinking goes and the text after it
// stays, even with the two on separate lines of the transcript, as Claude Code writes them.
func TestBriefReadsWhatTheBackupWrites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b := newClaudeBackup(t)
	think := map[string]any{"type": "thinking", "thinking": "private reasoning"}
	appendFile(t, b.transcript("s-1"), user(t, "the request")+
		assistant(t, "m1", think)+
		assistant(t, "m1", map[string]any{"type": "text", "text": "The reply."})+
		assistant(t, "m2", think)+
		assistant(t, "m2", map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "ls"}})+
		jsonLine(t, map[string]any{"type": "user", "timestamp": "2026-11-01T13:09:52Z", "message": map[string]any{"content": []map[string]any{{"type": "tool_result", "content": "the output"}}}})+
		assistant(t, "m3", map[string]any{"type": "text", "text": "Done."}))
	b.backUp(t)
	got, err := aca.ClaudeCode{}.Brief(ctx, b.launch, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(got.Start, "\n") + "\n" + strings.Join(got.Rest, "\n")
	wantInOrder(t, text, "the request", "The reply.", "- Bash ls (line ", "Done.")
	wantNot(t, text, "private reasoning", "the output", "### ")
}

// A hook killed while writing leaves half a value: what comes after must still be read,
// or a new session would not stick.
func TestBeginSessionComesAfterAHalfWrittenLine(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b := newClaudeBackup(t)
	sessions := filepath.Join(b.launch.BackupDir, "sessions.jsonl")
	appendFile(t, sessions, `{"session_id":"s-1","source":"startup"}`+"\n"+`{"session_id":"s-`)
	if err := (aca.ClaudeCode{}).BeginSession(ctx, b.launch, "s-2"); err != nil {
		t.Fatal(err)
	}
	if got := b.backUp(t); got != "s-2" {
		t.Errorf("session = %q", got)
	}
}

// The new session is what a backup says it is in, the row following, or a start would
// silently resume the old one. An agent from before the hook keeps its old session listed.
func TestBeginSessionSticks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b := newClaudeBackup(t)
	appendFile(t, b.transcript("s-1"), user(t, "kept"))
	b.backUp(t)
	if err := (aca.ClaudeCode{}).BeginSession(ctx, b.launch, "s-2"); err != nil {
		t.Fatal(err)
	}
	if got := b.backUp(t); got != "s-2" {
		t.Errorf("session = %q", got)
	}
	appendFile(t, b.transcript("s-1"), user(t, "late"))
	b.backUp(t)
	wantInOrder(t, b.text(t), "kept", "late")

	b.launch.SessionID = "s-2"
	command, err := aca.ClaudeCode{}.Command(b.launch)
	if err != nil {
		t.Fatal(err)
	}
	if command[1] != "--session-id" || command[2] != "s-2" {
		t.Errorf("a session with no transcript starts: %q", command)
	}
}
