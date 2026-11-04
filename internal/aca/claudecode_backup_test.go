package aca_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

// claudeBackup is a Claude Code home and an agent's backup in it.
type claudeBackup struct {
	launch aca.Launch
	dir    string // where Claude Code keeps this directory's transcripts
}

func newClaudeBackup(t *testing.T) claudeBackup {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "-work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "agent")
	if err := os.MkdirAll(backup, 0o700); err != nil {
		t.Fatal(err)
	}
	return claudeBackup{aca.Launch{Dir: "/work", SessionID: "s-1", UserHome: home, BackupDir: backup}, dir}
}

// jsonLine is one line of a transcript, in the shapes Claude Code 2.1.316 writes.
func jsonLine(t *testing.T, line map[string]any) string {
	t.Helper()
	data, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

func user(t *testing.T, text string) string {
	return jsonLine(t, map[string]any{"type": "user", "timestamp": "2026-11-01T13:09:49Z", "message": map[string]any{"role": "user", "content": text}})
}

func assistant(t *testing.T, id string, blocks ...map[string]any) string {
	return jsonLine(t, map[string]any{"type": "assistant", "timestamp": "2026-11-01T13:09:51Z", "message": map[string]any{"id": id, "role": "assistant", "content": blocks}})
}

func (b claudeBackup) transcript(id string) string { return filepath.Join(b.dir, id+".jsonl") }

func appendFile(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func (b claudeBackup) backUp(t *testing.T) string {
	t.Helper()
	backup, err := aca.ClaudeCode{}.BackUp(context.Background(), b.launch)
	if err != nil {
		t.Fatal(err)
	}
	return backup.Session
}

func (b claudeBackup) text(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(b.launch.BackupDir, aca.TranscriptFile))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func wantInOrder(t *testing.T, text string, parts ...string) {
	t.Helper()
	at := 0
	for _, part := range parts {
		i := strings.Index(text[at:], part)
		if i < 0 {
			t.Fatalf("%q not found in order in:\n%s", part, text)
		}
		at += i + len(part)
	}
}

func TestClaudeCodeBackUpWritesTheConversation(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	appendFile(t, b.transcript("s-1"), jsonLine(t, map[string]any{"type": "api-request-blob", "body": "SYSTEM PROMPT"})+
		user(t, "fix the login bug")+
		jsonLine(t, map[string]any{"type": "user", "isMeta": true, "message": map[string]any{"content": "CAVEAT"}})+
		jsonLine(t, map[string]any{"type": "user", "isSidechain": true, "message": map[string]any{"content": "SUBAGENT"}})+
		assistant(t, "m1", map[string]any{"type": "text", "text": "Looking \x1b[2mnow\x1b[22m."})+
		assistant(t, "m1", map[string]any{"type": "tool_use", "name": "Read", "input": map[string]any{"file_path": "login.go"}})+
		jsonLine(t, map[string]any{"type": "user", "message": map[string]any{"content": []map[string]any{{"type": "tool_result", "content": "a ``` fence", "is_error": true}}}}))

	if got := b.backUp(t); got != "s-1" {
		t.Errorf("session = %q", got)
	}

	text := b.text(t)
	wantInOrder(t, text, "# Session s-1", "## User · 2026-11-01T13:09:49Z", "fix the login bug",
		"## Assistant · 2026-11-01T13:09:51Z", "Looking now.", "### Tool: Read", `{"file_path":"login.go"}`,
		"### Result (error)", "````\na ``` fence\n````")
	if strings.Count(text, "## Assistant") != 1 {
		t.Errorf("one message has one heading:\n%s", text)
	}
	for _, kept := range []string{"SYSTEM PROMPT", "CAVEAT", "SUBAGENT", "\x1b"} {
		if strings.Contains(text, kept) {
			t.Errorf("%q in the text:\n%s", kept, text)
		}
	}
	raw, err := os.ReadFile(filepath.Join(b.launch.BackupDir, "transcripts", "s-1.jsonl"))
	if err != nil || !strings.Contains(string(raw), "SYSTEM PROMPT") {
		t.Errorf("the raw copy lacks what the text leaves out: %v", err)
	}
	for _, name := range []string{aca.TranscriptFile, "transcripts/s-1.jsonl", "rendered.json"} {
		info, err := os.Stat(filepath.Join(b.launch.BackupDir, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v, want 0600", name, info, err)
		}
	}
}

func TestClaudeCodeBackUpAddsOnlyWhatIsNew(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	appendFile(t, b.transcript("s-1"), user(t, "first"))
	b.backUp(t)
	b.backUp(t)
	if got := strings.Count(b.text(t), "first"); got != 1 {
		t.Errorf("first appears %d times:\n%s", got, b.text(t))
	}
	// A line still being written waits for its end.
	second := user(t, "second")
	appendFile(t, b.transcript("s-1"), second[:len(second)/2])
	b.backUp(t)
	if strings.Contains(b.text(t), "second") {
		t.Errorf("half a line was rendered:\n%s", b.text(t))
	}
	appendFile(t, b.transcript("s-1"), second[len(second)/2:])
	b.backUp(t)
	wantInOrder(t, b.text(t), "first", "second")
}

// A compaction and a clear keep what came before: the text only grows, and an
// agent is in the session the last start said.
func TestClaudeCodeBackUpGoesOnAcrossCompactAndClear(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	sessions := filepath.Join(b.launch.BackupDir, "sessions.jsonl")
	started := func(id, source string) {
		appendFile(t, sessions, jsonLine(t, map[string]any{"session_id": id, "source": source, "transcript_path": b.transcript(id)}))
	}
	started("s-1", "startup")
	appendFile(t, b.transcript("s-1"), user(t, "before compact"))
	b.backUp(t)
	started("s-1", "compact")
	appendFile(t, b.transcript("s-1"), jsonLine(t, map[string]any{"type": "user", "isCompactSummary": true, "timestamp": "t", "message": map[string]any{"content": "the summary"}}))
	b.backUp(t)
	started("s-2", "clear")
	appendFile(t, b.transcript("s-2"), user(t, "after clear"))

	if got := b.backUp(t); got != "s-2" {
		t.Errorf("session = %q, want the latest", got)
	}
	wantInOrder(t, b.text(t), "# Session s-1", "before compact", "## Compacted", "the summary", "# Session s-2", "after clear")

	// A hook's half-written line says nothing yet.
	appendFile(t, sessions, `{"session_id":"s-`)
	if got := b.backUp(t); got != "s-2" {
		t.Errorf("session = %q after half a line", got)
	}
}

func TestClaudeCodeBackUpKeepsWhatClaudeDeletes(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	appendFile(t, b.transcript("s-1"), user(t, "kept"))
	session := filepath.Join(b.dir, "s-1", "subagents")
	if err := os.MkdirAll(session, 0o700); err != nil {
		t.Fatal(err)
	}
	appendFile(t, filepath.Join(session, "agent-1.jsonl"), user(t, "sub"))
	b.backUp(t)
	if err := os.RemoveAll(b.dir); err != nil {
		t.Fatal(err)
	}
	b.backUp(t)
	wantInOrder(t, b.text(t), "kept")
	if _, err := os.Stat(filepath.Join(b.launch.BackupDir, "transcripts", "s-1", "subagents", "agent-1.jsonl")); err != nil {
		t.Errorf("subagent's transcript not kept: %v", err)
	}

	// Its start puts the transcript back, where a resume finds it.
	got, err := aca.ClaudeCode{}.Command(b.launch)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got[:3], []string{"claude", "--resume", "s-1"}) {
		t.Errorf("command = %q", got)
	}
	data, err := os.ReadFile(b.transcript("s-1"))
	if err != nil || !strings.Contains(string(data), "kept") {
		t.Errorf("transcript not put back: %v %s", err, data)
	}
}

func TestClaudeCodeBackUpSetsAsideAReplacedTranscript(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	appendFile(t, b.transcript("s-1"), user(t, "the old conversation, long enough"))
	b.backUp(t)
	if err := os.Remove(b.transcript("s-1")); err != nil {
		t.Fatal(err)
	}
	appendFile(t, b.transcript("s-1"), user(t, "new"))
	b.backUp(t)
	aside, err := filepath.Glob(filepath.Join(b.launch.BackupDir, "transcripts", "s-1.jsonl.replaced-*"))
	if err != nil || len(aside) != 1 {
		t.Fatalf("set aside: %v %v", aside, err)
	}
	data, err := os.ReadFile(aside[0])
	if err != nil || !strings.Contains(string(data), "old conversation") {
		t.Errorf("the old one: %v %s", err, data)
	}
	wantInOrder(t, b.text(t), "old conversation", "new")
}

// The settings' hook runs as Claude Code runs it: a shell, with the event on its
// input. That the real thing runs it is in docs/AGENT_RESUME.md.
func TestClaudeCodeSettingsRecordEachSession(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	b.launch.BackupDir = filepath.Join(filepath.Dir(b.launch.BackupDir), "it's here")
	got, err := aca.ClaudeCode{}.Command(b.launch)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"claude", "--session-id", "s-1", "--settings", filepath.Join(b.launch.BackupDir, "claude-settings.json")}; !slices.Equal(got, want) {
		t.Fatalf("command = %q, want %q", got, want)
	}
	data, err := os.ReadFile(got[4])
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct{ Command string }
		}
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", settings.Hooks["SessionStart"][0].Hooks[0].Command)
	cmd.Stdin = strings.NewReader(`{"session_id":"s-2"}`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook: %v %s", err, out)
	}
	if got := b.backUp(t); got != "s-2" {
		t.Errorf("session = %q", got)
	}
}

// TestClaudeCodeSettingsTurnOffTheSandboxForFull: and the warning bypassing approvals
// starts with, which would stop the agent until a human answered it. With ask, the
// user's own settings decide both.
func TestClaudeCodeSettingsTurnOffTheSandboxForFull(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		permissions aca.Permissions
		want        string
	}{
		{aca.PermissionsFull, "map[enabled:false] true"},
		{aca.PermissionsAsk, "<nil> <nil>"},
	} {
		b := newClaudeBackup(t)
		b.launch.Permissions = tc.permissions
		got, err := aca.ClaudeCode{}.Command(b.launch)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(got[len(got)-1])
		if err != nil {
			t.Fatal(err)
		}
		var settings map[string]any
		if err := json.Unmarshal(data, &settings); err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprint(settings["sandbox"], " ", settings["skipDangerousModePermissionPrompt"]); got != tc.want {
			t.Errorf("%s: sandbox and the warning's skip = %s, want %s", tc.permissions, got, tc.want)
		}
	}
}

// TestClaudeCodeBackupSaysWhereItWorks follows an agent that moved into a worktree it
// made itself, which its transcript alone tells. A subagent elsewhere does not move it.
func TestClaudeCodeBackupSaysWhereItWorks(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	dirOf := func() string {
		t.Helper()
		backup, err := aca.ClaudeCode{}.BackUp(context.Background(), b.launch)
		if err != nil {
			t.Fatal(err)
		}
		return backup.Dir
	}
	if got := dirOf(); got != "" {
		t.Errorf("nothing written yet: %q", got)
	}
	line := func(cwd string, sidechain bool) string {
		return jsonLine(t, map[string]any{"type": "user", "cwd": cwd, "isSidechain": sidechain, "timestamp": "t", "message": map[string]any{"content": "hello"}})
	}
	appendFile(t, b.transcript(b.launch.SessionID), line("/r", false))
	if got := dirOf(); got != "/r" {
		t.Errorf("first: %q", got)
	}
	appendFile(t, b.transcript(b.launch.SessionID), line("/r/.worktrees/auth-fix", false))
	if got := dirOf(); got != "/r/.worktrees/auth-fix" {
		t.Errorf("moved: %q", got)
	}
	appendFile(t, b.transcript(b.launch.SessionID), line("/elsewhere", true))
	if got := dirOf(); got != "/r/.worktrees/auth-fix" {
		t.Errorf("a subagent moved it: %q", got)
	}
	// Nothing new: it remembers, as it reads only what is new.
	if got := dirOf(); got != "/r/.worktrees/auth-fix" {
		t.Errorf("read again: %q", got)
	}
}
