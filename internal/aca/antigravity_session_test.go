package aca_test

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

// The conversations of testdata/antigravity/1.5.2/transcript: the first, resumed,
// then the one /clear began.
const (
	antigravityFirst   = "00000000-0000-0000-0000-000000000002"
	antigravityCleared = "00000000-0000-0000-0000-000000000003"
	antigravitySub     = "6a1c0d9e-2b7f-4c1e-9d3a-5f8b2e4c7a10"
)

// antigravityRecordedStarts is the SessionStart lines of the recording, which the
// recorder kept after the event's name, with the note folder it gave as the
// agent's folder in backup, and the project's path.
func antigravityRecordedStarts(t *testing.T, backup string) (lines []string, project string) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "antigravity", "1.5.2", "transcript", "hooks.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(nil, 1<<20)
	for s.Scan() {
		line, ok := strings.CutPrefix(s.Text(), "SessionStart ")
		if !ok {
			continue
		}
		var start struct {
			WorkspacePaths []string `json:"workspacePaths"`
		}
		if err := json.Unmarshal([]byte(line), &start); err != nil || len(start.WorkspacePaths) != 2 {
			t.Fatalf("recorded start %s: %v", line, err)
		}
		project = start.WorkspacePaths[1]
		lines = append(lines, strings.ReplaceAll(line, start.WorkspacePaths[0], filepath.Join(backup, "antigravity")))
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("%d starts recorded, want 2", len(lines))
	}
	return lines, project
}

func writeHooksFile(t *testing.T, backup string, lines ...string) {
	t.Helper()
	text := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(backup, "antigravity-hooks.jsonl"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The session is the latest conversation, past a subagent's that came after it and a
// line a stopped hook cut short, and Dir is where it started, without the agent's
// folder.
func TestAntigravityBackUpFindsTheSession(t *testing.T) {
	t.Parallel()
	backup, home := t.TempDir(), t.TempDir()
	lines, project := antigravityRecordedStarts(t, backup)
	sub := strings.ReplaceAll(lines[1], antigravityCleared, antigravitySub)
	writeHooksFile(t, backup, append(lines, sub, `{"conversationId":"`+antigravityFirst[:20])...)
	parent := filepath.Join(home, ".gemini", "antigravity-cli", "brain", antigravityCleared, ".system_generated", "subagents")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, antigravitySub+".json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := aca.Antigravity{}.BackUp(t.Context(), aca.Launch{UserHome: home, BackupDir: backup})
	if err != nil {
		t.Fatal(err)
	}
	if want := (aca.Backup{Session: antigravityCleared, Dir: project}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// A conversation started again comes last, by its last line.
func TestAntigravityBackUpTakesTheLastStarted(t *testing.T) {
	t.Parallel()
	backup := t.TempDir()
	lines, _ := antigravityRecordedStarts(t, backup)
	writeHooksFile(t, backup, lines[0], lines[1], lines[0])
	got, _ := aca.Antigravity{}.BackUp(t.Context(), aca.Launch{UserHome: t.TempDir(), BackupDir: backup})
	if got.Session != antigravityFirst {
		t.Fatalf("session %s, want %s", got.Session, antigravityFirst)
	}
}

// Before its first message an agent has no conversation of Antigravity's, and keeps
// wdx's.
func TestAntigravityBackUpBeforeTheFirstMessage(t *testing.T) {
	t.Parallel()
	got, err := aca.Antigravity{}.BackUp(t.Context(), aca.Launch{UserHome: t.TempDir(), BackupDir: t.TempDir()})
	if err != nil || got != (aca.Backup{}) {
		t.Fatalf("got %+v, %v; want nothing", got, err)
	}
}

func TestAntigravityResumesTheConversationItKeeps(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	conversations := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(conversations, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conversations, antigravityFirst+".db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for session, resumes := range map[string]bool{
		antigravityFirst: true,
		// wdx's own, before the first message, or one whose db is gone, which
		// Antigravity would replace with a new conversation.
		antigravityCleared: false,
		// Never a pattern to look for.
		"*": false,
	} {
		got, err := aca.Antigravity{}.Command(t.Context(), aca.Launch{Dir: "/work", SessionID: session, UserHome: home, BackupDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if gotResumes := slices.Equal(got[:2], []string{"agy", "--conversation=" + session}); gotResumes != resumes {
			t.Errorf("session %s: %q, resumes %v, want %v", session, got, gotResumes, resumes)
		}
	}
}

// The SessionStart hook Command writes appends the JSON Antigravity gives it on stdin
// to the backup's hooks file, a line each, whatever the backup's path holds.
func TestAntigravityHookKeepsEachConversation(t *testing.T) {
	t.Parallel()
	backup := filepath.Join(t.TempDir(), "it's a backup")
	if err := os.Mkdir(backup, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := (aca.Antigravity{}).Command(t.Context(), aca.Launch{Dir: "/work", UserHome: t.TempDir(), BackupDir: backup}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(backup, "antigravity", ".agents", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var hooks map[string]map[string][]struct{ Type, Command string }
	if err := json.Unmarshal(data, &hooks); err != nil {
		t.Fatalf("hooks.json %s: %v", data, err)
	}
	starts := hooks["wdx"]["SessionStart"]
	if len(starts) != 1 || starts[0].Type != "command" || len(hooks["wdx"]) != 1 {
		t.Fatalf("hooks.json %s, want one SessionStart command", data)
	}
	lines, _ := antigravityRecordedStarts(t, backup)
	for _, line := range lines {
		// As Antigravity runs it, given the event on stdin, here spread over lines.
		var event any
		_ = json.Unmarshal([]byte(line), &event)
		pretty, _ := json.MarshalIndent(event, "", "  ")
		cmd := exec.Command("sh", "-c", starts[0].Command)
		cmd.Stdin = strings.NewReader(string(pretty) + "\n")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("hook: %v", err)
		}
		if string(out) != "{}\n" {
			t.Errorf("hook printed %q, want {}", out)
		}
	}
	got, err := aca.Antigravity{}.BackUp(t.Context(), aca.Launch{UserHome: t.TempDir(), BackupDir: backup})
	if err != nil || got.Session != antigravityCleared {
		t.Fatalf("after the hook: %+v, %v; want %s", got, err, antigravityCleared)
	}
	kept, err := os.ReadFile(filepath.Join(backup, "antigravity-hooks.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(kept), "\n"); n != len(lines) {
		t.Fatalf("hooks file has %d lines, want %d: %s", n, len(lines), kept)
	}
}
