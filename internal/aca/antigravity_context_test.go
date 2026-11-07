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

// antigravitySettings is what Command passes Antigravity with --settings.
func antigravitySettings(t *testing.T, command []string) map[string]any {
	t.Helper()
	if len(command) < 2 || command[len(command)-2] != "--settings" {
		t.Fatalf("command %q does not end with --settings", command)
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(command[len(command)-1]), &settings); err != nil {
		t.Fatalf("settings %q: %v", command[len(command)-1], err)
	}
	return settings
}

// antigravityAgentStatusLine is the statusLine of the settings Command passes: a
// command with stack_with_default, so that agy's own footer stays, and no type, which
// Antigravity's statusLine has not.
func antigravityAgentStatusLine(t *testing.T, l aca.Launch) map[string]any {
	t.Helper()
	command, err := (aca.Antigravity{}).Command(t.Context(), l)
	if err != nil {
		t.Fatal(err)
	}
	line, _ := antigravitySettings(t, command)["statusLine"].(map[string]any)
	if line["stack_with_default"] != true || line["command"] == "" {
		t.Fatalf("status line = %v", line)
	}
	if _, ok := line["type"]; ok {
		t.Errorf("status line = %v, has a type", line)
	}
	return line
}

// antigravityStatusLine runs the status line of the settings Command passes as
// Antigravity runs it: a shell, with what it shows on its input. It returns what that
// printed.
func antigravityStatusLine(t *testing.T, l aca.Launch, input []byte) string {
	t.Helper()
	command, _ := antigravityAgentStatusLine(t, l)["command"].(string)
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Stdin = strings.NewReader(string(input))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("status line %q: %v %s", command, err, out)
	}
	return string(out)
}

func antigravityStatusInput(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "antigravity", "1.5.2", "status", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// antigravityLaunch is a launch with a backup and a user home of its own.
func antigravityLaunch(t *testing.T) aca.Launch {
	t.Helper()
	return aca.Launch{UserHome: t.TempDir(), BackupDir: filepath.Join(t.TempDir(), "backup"), Dir: "/work"}
}

// TestAntigravityContextSize reads the status line's input as 1.5.2 gives it, Claude
// Code's JSON: before the first message no tokens and the window; after /compact the
// compaction's own request, until the next response.
func TestAntigravityContextSize(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		file string
		want aca.ContextSize
	}{
		{"before-first-message.json", aca.ContextSize{Tokens: 0, Window: 1_048_576}},
		{"mid-session.json", aca.ContextSize{Tokens: 11_206, Window: 1_048_576}},
		{"after-compact.json", aca.ContextSize{Tokens: 11_795, Window: 1_048_576}},
	} {
		l := antigravityLaunch(t)
		if out := antigravityStatusLine(t, l, antigravityStatusInput(t, tc.file)); out != "" {
			t.Errorf("%s: status line printed %q", tc.file, out)
		}
		got, follow, err := aca.Antigravity{}.ContextSize(l)
		if err != nil || got != tc.want || follow != nil {
			t.Errorf("%s: context size = %+v, %v, following %q; want %+v and nothing to follow", tc.file, got, err, follow, tc.want)
		}
	}
}

// TestAntigravityContextSizeUnknown: nothing is known before the status line first
// ran, or without a backup, nor from a file that does not parse, which is an error to
// log.
func TestAntigravityContextSizeUnknown(t *testing.T) {
	t.Parallel()
	if _, _, err := (aca.Antigravity{}).ContextSize(aca.Launch{}); !errors.Is(err, aca.ErrNoContextSize) {
		t.Errorf("without a backup: %v", err)
	}
	l := antigravityLaunch(t)
	if _, _, err := (aca.Antigravity{}).ContextSize(l); !errors.Is(err, aca.ErrNoContextSize) {
		t.Errorf("before the status line ran: %v", err)
	}
	antigravityStatusLine(t, l, []byte(`{"session_id": "s-1", "context_window": {`))
	if _, _, err := (aca.Antigravity{}).ContextSize(l); err == nil || errors.Is(err, aca.ErrNoContextSize) {
		t.Errorf("a file that does not parse: %v", err)
	}
	antigravityStatusLine(t, l, []byte(`{"session_id": "s-1"}`))
	if _, _, err := (aca.Antigravity{}).ContextSize(l); !errors.Is(err, aca.ErrNoContextSize) {
		t.Errorf("no context window: %v", err)
	}
}

// writeAntigravityUserSettings writes the user's ~/.gemini/antigravity-cli/settings.json.
func writeAntigravityUserSettings(t *testing.T, home, settings string) {
	t.Helper()
	user := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	if err := os.MkdirAll(filepath.Dir(user), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(user, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestAntigravityStatusLineRunsTheUsers: the status line given with --settings
// replaces the user's own (checked on 1.5.2), so theirs runs after wdx's on the same
// input, and what it prints is what agy draws under its footer; their padding and
// timeout_ms are kept. A command of several, ending in a comment, with a quote of
// each kind, runs whole, from a backup whose path holds a quote.
func TestAntigravityStatusLineRunsTheUsers(t *testing.T) {
	t.Parallel()
	l := antigravityLaunch(t)
	l.BackupDir = filepath.Join(filepath.Dir(l.BackupDir), "it's here")
	theirs := filepath.Join(t.TempDir(), "theirs.json")
	settings, err := json.Marshal(map[string]any{"model": "Gemini 3.8 Flash (Low)", "statusLine": map[string]any{
		"command": "cat > '" + theirs + `'; echo "their line" 'quoted' # their comment`,
		"padding": 1, "timeout_ms": 3000,
	}})
	if err != nil {
		t.Fatal(err)
	}
	writeAntigravityUserSettings(t, l.UserHome, string(settings))
	input := antigravityStatusInput(t, "mid-session.json")
	if out := antigravityStatusLine(t, l, input); out != "their line quoted\n" {
		t.Errorf("status line printed %q", out)
	}
	if got, err := os.ReadFile(theirs); err != nil || string(got) != string(input) {
		t.Errorf("their input = %q, %v", got, err)
	}
	if got, _, err := (aca.Antigravity{}).ContextSize(l); err != nil || got.Tokens != 11_206 {
		t.Errorf("context size = %+v, %v", got, err)
	}
	line := antigravityAgentStatusLine(t, l)
	delete(line, "command")
	if want := map[string]any{"stack_with_default": true, "padding": 1.0, "timeout_ms": 3000.0}; !equalJSON(line, want) {
		t.Errorf("status line = %v, want %v", line, want)
	}
}

// TestAntigravityStatusLineWithoutTheUsers: a user with no settings, none that parse,
// no status line or one with no command gets wdx's alone, command and
// stack_with_default, and nothing of theirs kept.
func TestAntigravityStatusLineWithoutTheUsers(t *testing.T) {
	t.Parallel()
	for name, settings := range map[string]string{
		"no settings":   "",
		"not parsing":   `{"statusLine": `,
		"none":          `{"model": "Gemini 3.8 Flash (Low)"}`,
		"not an object": `{"statusLine": "echo no"}`,
		"no command":    `{"statusLine": {"padding": 2, "stack_with_default": false}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			l := antigravityLaunch(t)
			if settings != "" {
				writeAntigravityUserSettings(t, l.UserHome, settings)
			}
			if out := antigravityStatusLine(t, l, antigravityStatusInput(t, "mid-session.json")); out != "" {
				t.Errorf("status line printed %q", out)
			}
			if line := antigravityAgentStatusLine(t, l); len(line) != 2 {
				t.Errorf("status line = %v, want command and stack_with_default alone", line)
			}
		})
	}
}

// TestAntigravityBeginSessionForgetsTheContextSize: the old conversation's size is not
// the new session's, which tells its own once Antigravity starts.
func TestAntigravityBeginSessionForgetsTheContextSize(t *testing.T) {
	t.Parallel()
	l := antigravityLaunch(t)
	antigravityStatusLine(t, l, antigravityStatusInput(t, "mid-session.json"))
	if err := (aca.Antigravity{}).BeginSession(t.Context(), l, "s-2"); err != nil {
		t.Fatal(err)
	}
	if got, _, err := (aca.Antigravity{}).ContextSize(l); !errors.Is(err, aca.ErrNoContextSize) {
		t.Errorf("after a new session began: %+v, %v", got, err)
	}
}
