package aca_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

// codexSession is the session of testdata/codex/0.163.0/session/rollout.jsonl: a real
// session file's session_meta and turn_context lines, with what is private left out.
const codexSession = "01a1abca-f520-78a3-82ad-bb32d731c004"

// codexHome is a home where Codex keeps the sample session as id, with thread_source
// and its last turn's cwd changed to the ones given.
func codexHome(t *testing.T, sessions map[string]struct{ source, cwd string }) string {
	t.Helper()
	sample, err := os.ReadFile(filepath.Join("testdata", "codex", "0.163.0", "session", "rollout.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	day := filepath.Join(home, ".codex", "sessions", "2026", "11", "05")
	if err := os.MkdirAll(day, 0o700); err != nil {
		t.Fatal(err)
	}
	for id, s := range sessions {
		text := strings.ReplaceAll(string(sample), codexSession, id)
		text = strings.Replace(text, `"thread_source":"user"`, `"thread_source":"`+s.source+`"`, 1)
		if last := strings.LastIndex(text, `"cwd":"/work/repo"`); last >= 0 && s.cwd != "" {
			text = text[:last] + `"cwd":"` + s.cwd + `"` + text[last+len(`"cwd":"/work/repo"`):]
		}
		if err := os.WriteFile(filepath.Join(day, "rollout-2026-11-05T13-59-27-"+id+".jsonl"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// writeTurns writes what Codex's notify reports, a line per turn, as Command has it
// kept: the threads in order, then a line a killed notify cut short.
func writeTurns(t *testing.T, dir string, threads ...string) {
	t.Helper()
	var text strings.Builder
	for _, id := range threads {
		line, _ := json.Marshal(map[string]any{"type": "agent-turn-complete", "thread-id": id, "cwd": "/work/repo",
			"client": "codex-tui", "input-messages": []string{"hello"}, "last-assistant-message": "hi"})
		text.Write(append(line, '\n'))
	}
	text.WriteString(`{"type":"agent-turn-complete","thread-`)
	if err := os.WriteFile(filepath.Join(dir, "codex-turns.jsonl"), []byte(text.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

const (
	codexNamer    = "01a1abcb-09a0-7d82-ba97-5e1e30578adf" // the thread naming the conversation, with no session file
	codexSubagent = "01a1ab88-462d-7000-8000-000000000001"
	codexEarlier  = "01a1ab80-0000-7000-8000-000000000002"
)

func TestCodexBackUpFindsTheSession(t *testing.T) {
	t.Parallel()
	home := codexHome(t, map[string]struct{ source, cwd string }{
		codexSession:  {"user", "/work/repo/sub"},
		codexEarlier:  {"user", ""},
		codexSubagent: {"subagent", ""},
	})
	backup := t.TempDir()
	// The user's thread, then the one Codex names it in and a subagent's, which come
	// after it but are not the conversation.
	writeTurns(t, backup, codexEarlier, codexSession, codexNamer, codexSubagent)
	got, err := aca.Codex{}.BackUp(t.Context(), aca.Launch{UserHome: home, BackupDir: backup})
	if err != nil {
		t.Fatal(err)
	}
	if want := (aca.Backup{Session: codexSession, Dir: "/work/repo/sub"}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// A thread whose session file is gone is passed over for the one before it.
func TestCodexBackUpPassesOverAGoneSession(t *testing.T) {
	t.Parallel()
	home := codexHome(t, map[string]struct{ source, cwd string }{codexEarlier: {"user", ""}})
	backup := t.TempDir()
	writeTurns(t, backup, codexEarlier, codexSession)
	got, err := aca.Codex{}.BackUp(t.Context(), aca.Launch{UserHome: home, BackupDir: backup})
	if err != nil || got.Session != codexEarlier || got.Dir != "/work/repo" {
		t.Fatalf("got %+v, %v; want %s in /work/repo", got, err, codexEarlier)
	}
}

// Before its first turn an agent has no session of Codex's, and keeps wdx's.
func TestCodexBackUpBeforeTheFirstTurn(t *testing.T) {
	t.Parallel()
	got, err := aca.Codex{}.BackUp(t.Context(), aca.Launch{UserHome: t.TempDir(), BackupDir: t.TempDir()})
	if err != nil || got != (aca.Backup{}) {
		t.Fatalf("got %+v, %v; want nothing", got, err)
	}
}

func TestCodexResumesTheSessionItKeeps(t *testing.T) {
	t.Parallel()
	home := codexHome(t, map[string]struct{ source, cwd string }{codexSession: {"user", ""}})
	for session, resumes := range map[string]bool{
		codexSession: true,
		// wdx's own, before Codex's first turn, or one Codex deleted.
		"6a1c0d9e-2b7f-4c1e-9d3a-5f8b2e4c7a10": false,
		// Never a pattern to look for.
		"*": false,
	} {
		got, err := aca.Codex{}.Command(aca.Launch{Dir: "/work/repo", SessionID: session, UserHome: home})
		if err != nil {
			t.Fatal(err)
		}
		if gotResumes := slices.Equal(got[:3], []string{"codex", "resume", session}); gotResumes != resumes {
			t.Errorf("session %s: %q, resumes %v, want %v", session, got, gotResumes, resumes)
		}
	}
}

// The notify Command gives Codex appends the JSON Codex passes it to the backup's
// turns file, whatever the backup's path holds.
func TestCodexNotifyKeepsEachTurn(t *testing.T) {
	t.Parallel()
	backup := filepath.Join(t.TempDir(), "it's a backup")
	if err := os.Mkdir(backup, 0o700); err != nil {
		t.Fatal(err)
	}
	command, err := aca.Codex{}.Command(aca.Launch{Dir: "/work/repo", UserHome: t.TempDir(), BackupDir: backup})
	if err != nil {
		t.Fatal(err)
	}
	var notify []string
	for i, word := range command {
		if value, ok := strings.CutPrefix(word, "notify="); ok && command[i-1] == "-c" {
			// A TOML array of basic strings is JSON too.
			if err := json.Unmarshal([]byte(value), &notify); err != nil {
				t.Fatalf("notify %s: %v", value, err)
			}
		}
	}
	if len(notify) == 0 {
		t.Fatalf("no notify in %q", command)
	}
	turn := `{"type":"agent-turn-complete","thread-id":"` + codexSession + `"}`
	for range 2 {
		// As Codex runs it: its words, then the turn.
		if out, err := exec.Command(notify[0], append(notify[1:], turn)...).CombinedOutput(); err != nil {
			t.Fatalf("notify: %v: %s", err, out)
		}
	}
	got, err := os.ReadFile(filepath.Join(backup, "codex-turns.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if want := turn + "\n" + turn + "\n"; string(got) != want {
		t.Fatalf("turns file %q, want %q", got, want)
	}
}

// With no backup, as in a test of the command, there is nothing to report to.
func TestCodexNotifiesOnlyWithABackup(t *testing.T) {
	t.Parallel()
	command, err := aca.Codex{}.Command(aca.Launch{Dir: "/work/repo", UserHome: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range command {
		if strings.HasPrefix(word, "notify=") {
			t.Fatalf("notify without a backup: %q", command)
		}
	}
}
