package aca_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/runtest"
)

// The shape of what Claude Code 2.1.316 writes when it moves a conversation to a background
// session: a link in the old transcript, and the new one starting with copies of the old
// one's messages under the same uuids.
func uuidLine(t *testing.T, kind, uuid, at, text string) string {
	t.Helper()
	return jsonLine(t, map[string]any{"type": kind, "uuid": uuid, "timestamp": at, "cwd": "/work", "message": map[string]any{"content": text}})
}

func continuedIn(t *testing.T, id string) string {
	t.Helper()
	return jsonLine(t, map[string]any{"type": "continued-in", "timestamp": "2026-11-01T12:50:51Z", "sessionId": "s-1", "continuedInSessionId": id})
}

func (b claudeBackup) continued(t *testing.T) {
	t.Helper()
	appendFile(t, b.transcript("s-1"),
		uuidLine(t, "user", "u-1", "2026-11-01T10:00:00Z", "the first request")+
			uuidLine(t, "user", "u-2", "2026-11-01T11:00:00Z", "a later request")+
			continuedIn(t, "s-2"))
	appendFile(t, b.transcript("s-2"),
		jsonLine(t, map[string]any{"type": "mode", "mode": "normal"})+
			uuidLine(t, "user", "u-1", "2026-11-01T10:00:00Z", "the first request")+
			uuidLine(t, "user", "u-2", "2026-11-01T11:00:00Z", "a later request")+
			uuidLine(t, "user", "u-3", "2026-11-01T13:00:00Z", "work done elsewhere"))
}

func TestClaudeCodeBackUpFollowsAConversationMovedElsewhere(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	b.continued(t)

	if got := b.backUp(t); got != "s-2" {
		t.Errorf("session = %q, want the one it went on in", got)
	}
	text := b.text(t)
	wantInOrder(t, text, "# Session s-1", "the first request", "a later request", "# Session s-2", "work done elsewhere")
	if n := strings.Count(text, "a later request"); n != 1 {
		t.Errorf("a copied message is there %d times:\n%s", n, text)
	}

	// What follows goes on being backed up, by what was recorded, from the session the
	// agent's row now names.
	b.launch.SessionID = "s-2"
	appendFile(t, b.transcript("s-2"), uuidLine(t, "user", "u-4", "2026-11-01T14:00:00Z", "more work"))
	if got := b.backUp(t); got != "s-2" {
		t.Errorf("session = %q on the next backup", got)
	}
	if text := b.text(t); strings.Count(text, "more work") != 1 || strings.Count(text, "a later request") != 1 {
		t.Errorf("after the next backup:\n%s", text)
	}
	if _, err := os.Stat(filepath.Join(b.launch.BackupDir, "transcripts", "s-2.jsonl")); err != nil {
		t.Errorf("no raw copy: %v", err)
	}
}

// A brief read after the move has the work done elsewhere, once.
func TestClaudeCodeBriefReachesAConversationMovedElsewhere(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	b.continued(t)
	b.backUp(t)
	brief, err := aca.ClaudeCode{}.Brief(context.Background(), b.launch, 1<<16)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(append(brief.Start, brief.Rest...), "\n")
	wantInOrder(t, text, "the first request", "work done elsewhere")
	if n := strings.Count(text, "a later request"); n != 1 {
		t.Errorf("a copied message is there %d times:\n%s", n, text)
	}
}

// A backup made before links were followed, with the old transcript rendered to its end
// and the agent gone on to a restart's session, finds the link and keeps that session.
func TestClaudeCodeBackUpFindsALinkOfAnEarlierBackup(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	appendFile(t, b.transcript("s-1"), uuidLine(t, "user", "u-1", "2026-11-01T10:00:00Z", "the first request")+continuedIn(t, "s-2"))
	b.backUp(t)
	state := filepath.Join(b.launch.BackupDir, "rendered.json")
	data, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	// Written by a version with no links: nothing of where it scanned to.
	before := strings.Split(string(data), `,"scanned"`)[0] + "}"
	if err := os.WriteFile(state, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions := filepath.Join(b.launch.BackupDir, "sessions.jsonl")
	if err := os.WriteFile(sessions, []byte(`{"session_id":"s-1"}`+"\n"+`{"session_id":"s-3","source":"wdx_restart"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	appendFile(t, b.transcript("s-3"), uuidLine(t, "user", "u-3", "2026-11-01T14:00:00Z", "after the restart"))
	appendFile(t, b.transcript("s-2"), jsonLine(t, map[string]any{"type": "user", "uuid": "u-2", "timestamp": "2026-11-01T13:00:00Z", "cwd": "/a-worktree", "message": map[string]any{"content": "work done elsewhere"}}))

	b.launch.SessionID = "s-3"
	if got := b.backUp(t); got != "s-3" {
		t.Errorf("session = %q, want the restart's", got)
	}
	backup, err := aca.ClaudeCode{}.BackUp(context.Background(), b.launch)
	if err != nil {
		t.Fatal(err)
	}
	if backup.Dir != "/work" {
		t.Errorf("dir = %q: older work took the agent back to where it was", backup.Dir)
	}
	if got := b.backUp(t); got != "s-3" {
		t.Errorf("session = %q on the next backup", got)
	}
	if !strings.Contains(b.text(t), "work done elsewhere") {
		t.Errorf("the work is not there:\n%s", b.text(t))
	}
}

// A message quoting the link is not one.
func TestClaudeCodeBackUpIgnoresAQuotedLink(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	appendFile(t, b.transcript("s-1"), uuidLine(t, "user", "u-1", "2026-11-01T10:00:00Z", `{"type":"continued-in","continuedInSessionId":"s-9"}`))
	if got := b.backUp(t); got != "s-1" {
		t.Errorf("session = %q", got)
	}
}

// fakeClaudeShell is a login shell that answers as claude does and records what it ran.
func fakeClaudeShell(t *testing.T, agents string) (shell, ran string) {
	t.Helper()
	dir := t.TempDir()
	ran = filepath.Join(dir, "ran")
	shell = filepath.Join(dir, "shell")
	script := "#!/bin/sh\necho \"$2\" >> '" + ran + "'\ncase \"$2\" in\n" +
		"'claude agents --json') echo 'welcome back'; cat <<'EOF'\n" + agents + "\nEOF\n;;\nesac\n"
	if err := runtest.WriteExecutable(shell, script); err != nil {
		t.Fatal(err)
	}
	return shell, ran
}

func TestClaudeCodeDetachedOnlyFindsItsOwnRunningSessions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b := newClaudeBackup(t)
	shell, ran := fakeClaudeShell(t, `[
		{"id":"s-2","sessionId":"s-2","kind":"background","pid":7,"status":"busy"},
		{"id":"other","sessionId":"other","kind":"background","pid":8,"status":"idle"},
		{"id":"s-1","sessionId":"s-1","kind":"interactive","pid":9,"status":"idle"}
	]`)
	b.launch.Shell = shell

	// No link, no process: an ordinary restart runs nothing.
	got, err := aca.ClaudeCode{}.Detached(ctx, b.launch)
	if err != nil || got != nil {
		t.Fatalf("with no link: %v, %v", got, err)
	}
	if _, err := os.Stat(ran); !os.IsNotExist(err) {
		t.Errorf("ran something with no link: %v", err)
	}

	b.continued(t)
	b.backUp(t)
	got, err = aca.ClaudeCode{}.Detached(ctx, b.launch)
	if err != nil {
		t.Fatal(err)
	}
	want := []aca.DetachedSession{{ID: "s-2", Session: "s-2", Working: true}}
	if !slices.Equal(got, want) {
		t.Fatalf("detached = %+v, want %+v", got, want)
	}
	if err := (aca.ClaudeCode{}).StopDetached(ctx, b.launch, got[0]); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ran)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "claude stop s-2\n") {
		t.Errorf("ran:\n%s", data)
	}
	if err := (aca.ClaudeCode{}).StopDetached(ctx, b.launch, aca.DetachedSession{ID: "s-2; rm -rf x"}); err == nil {
		t.Error("an id with shell in it was run")
	}
}

// A session with no process is one Claude Code only remembers.
func TestClaudeCodeDetachedLeavesASessionWithNoProcess(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	b.launch.Shell, _ = fakeClaudeShell(t, `[{"id":"s-2","sessionId":"s-2","kind":"background","state":"done"}]`)
	b.continued(t)
	b.backUp(t)
	got, err := aca.ClaudeCode{}.Detached(context.Background(), b.launch)
	if err != nil || len(got) != 0 {
		t.Errorf("detached = %+v, %v", got, err)
	}
}
