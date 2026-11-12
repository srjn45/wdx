package aca_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

// statusLine runs the status line of the settings Command writes as Claude Code runs
// it: a shell, with what it shows on its input. It returns what that printed.
func statusLine(t *testing.T, l aca.Launch, input []byte) string {
	t.Helper()
	command, err := aca.ClaudeCode{}.Command(t.Context(), l)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(command[len(command)-1])
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		StatusLine struct{ Type, Command string } `json:"statusLine"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.StatusLine.Type != "command" {
		t.Fatalf("status line = %+v", settings.StatusLine)
	}
	cmd := exec.Command("/bin/sh", "-c", settings.StatusLine.Command)
	cmd.Stdin = strings.NewReader(string(input))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("status line: %v %s", err, out)
	}
	return string(out)
}

func statusInput(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "claude_code", "2.1.319", "status", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestClaudeCodeContextSize reads the status line's input as 2.1.319 gives it.
// Before the first message and after /clear it tells no tokens, and the window. The
// model's display name comes with it, at the file's time.
func TestClaudeCodeContextSize(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		file string
		want aca.ContextSize
	}{
		{"before-first-message.json", aca.ContextSize{Tokens: 0, Window: 1_000_000, Model: "Haiku 5.5"}},
		{"mid-session.json", aca.ContextSize{Tokens: 44_139, Window: 1_000_000, Model: "Haiku 5.5"}},
		{"after-clear.json", aca.ContextSize{Tokens: 0, Window: 1_000_000, Model: "Haiku 5.5"}},
	} {
		b := newClaudeBackup(t)
		if out := statusLine(t, b.launch, statusInput(t, tc.file)); out != "" {
			t.Errorf("%s: status line printed %q", tc.file, out)
		}
		info, err := os.Stat(filepath.Join(b.launch.BackupDir, "claude-status.json"))
		if err != nil {
			t.Fatal(err)
		}
		tc.want.ModelAt = info.ModTime()
		got, follow, err := aca.ClaudeCode{}.ContextSize(b.launch)
		if err != nil || got != tc.want || follow != nil {
			t.Errorf("%s: context size = %+v, %v, following %q; want %+v and nothing to follow", tc.file, got, err, follow, tc.want)
		}
	}
}

// TestClaudeCodeContextSizeModel: only a display name of the screen parser's form, a
// word and a version, is given as the model; anything else, or none, is "".
func TestClaudeCodeContextSizeModel(t *testing.T) {
	t.Parallel()
	for model, want := range map[string]string{
		`{"id": "claude-opus-5-5", "display_name": "Opus 5.5"}`:      "Opus 5.5",
		`{"id": "claude-fable-5-2", "display_name": "Fable 5"}`:      "Fable 5",
		`{"id": "claude-opus-5-5", "display_name": "Opus 5.5 (1M)"}`: "",
		`{"id": "claude-opus-5-5", "display_name": "claude-opus"}`:   "",
		`{"id": "claude-opus-5-5", "display_name": "Opus 5.5\nrm"}`:  "",
		`{"id": "claude-opus-5-5"}`:                                  "",
		`null`:                                                       "",
	} {
		b := newClaudeBackup(t)
		statusLine(t, b.launch, []byte(`{"model": `+model+`, "context_window": {"total_input_tokens": 3, "context_window_size": 200000}}`))
		got, _, err := aca.ClaudeCode{}.ContextSize(b.launch)
		if err != nil || got.Model != want || got.Tokens != 3 || (want == "") != got.ModelAt.IsZero() {
			t.Errorf("%s: context size = %+v, %v; want the model %q", model, got, err, want)
		}
	}
}

// TestClaudeCodeContextSizeUnknown: nothing is known before the status line first
// ran, nor from a file that does not parse, which is an error to log.
func TestClaudeCodeContextSizeUnknown(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	if _, _, err := (aca.ClaudeCode{}).ContextSize(b.launch); !errors.Is(err, aca.ErrNoContextSize) {
		t.Errorf("before the status line ran: %v", err)
	}
	statusLine(t, b.launch, []byte(`{"session_id": "s-1", "context_window": {`))
	if _, _, err := (aca.ClaudeCode{}).ContextSize(b.launch); err == nil || errors.Is(err, aca.ErrNoContextSize) {
		t.Errorf("a file that does not parse: %v", err)
	}
	statusLine(t, b.launch, []byte(`{"session_id": "s-1"}`))
	if _, _, err := (aca.ClaudeCode{}).ContextSize(b.launch); !errors.Is(err, aca.ErrNoContextSize) {
		t.Errorf("no context window: %v", err)
	}
}

// TestClaudeCodeStatusLineRunsTheUsers: the status line given with --settings replaces
// the user's own (checked on 2.1.319), so theirs runs after it on the same input, and
// what it prints is what shows. A command of several, ending in a comment, runs whole.
func TestClaudeCodeStatusLineRunsTheUsers(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	b.launch.BackupDir = filepath.Join(filepath.Dir(b.launch.BackupDir), "it's here")
	theirs := filepath.Join(t.TempDir(), "theirs.json")
	settings, err := json.Marshal(map[string]any{"statusLine": map[string]any{
		"type": "command", "command": "cat > '" + theirs + "'; echo their line # their comment",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.launch.UserHome, ".claude", "settings.json"), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	input := statusInput(t, "mid-session.json")
	if out := statusLine(t, b.launch, input); out != "their line\n" {
		t.Errorf("status line printed %q", out)
	}
	if got, err := os.ReadFile(theirs); err != nil || string(got) != string(input) {
		t.Errorf("their input = %q, %v", got, err)
	}
	if got, _, err := (aca.ClaudeCode{}).ContextSize(b.launch); err != nil || got.Tokens != 44_139 {
		t.Errorf("context size = %+v, %v", got, err)
	}
}

// TestClaudeCodeStatusLineWithoutTheUsers: settings that do not parse, or a status
// line that is not a command, leave only wdx's.
func TestClaudeCodeStatusLineWithoutTheUsers(t *testing.T) {
	t.Parallel()
	for _, settings := range []string{`{"statusLine": `, `{"statusLine": {"type": "static", "command": "echo no"}}`, `{}`} {
		b := newClaudeBackup(t)
		if err := os.MkdirAll(filepath.Join(b.launch.UserHome, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(b.launch.UserHome, ".claude", "settings.json"), []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
		if out := statusLine(t, b.launch, statusInput(t, "mid-session.json")); out != "" {
			t.Errorf("%s: status line printed %q", settings, out)
		}
	}
}

// TestClaudeCodeBeginSessionForgetsTheContextSize: the old session's size is not the
// new one's, which tells its own once Claude Code starts.
func TestClaudeCodeBeginSessionForgetsTheContextSize(t *testing.T) {
	t.Parallel()
	b := newClaudeBackup(t)
	statusLine(t, b.launch, statusInput(t, "mid-session.json"))
	if err := (aca.ClaudeCode{}).BeginSession(t.Context(), b.launch, "s-2"); err != nil {
		t.Fatal(err)
	}
	if got, _, err := (aca.ClaudeCode{}).ContextSize(b.launch); !errors.Is(err, aca.ErrNoContextSize) {
		t.Errorf("after a new session began: %+v, %v", got, err)
	}
}
