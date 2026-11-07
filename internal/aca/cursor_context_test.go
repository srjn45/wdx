package aca_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/srjn45/wdx/internal/aca"
)

// cursorArgv splits a command as Cursor does (string-argv, checked on 2026.11.05): on
// whitespace, an argument in single or double quotes taken whole without them, no
// escapes.
func cursorArgv(t *testing.T, command string) []string {
	t.Helper()
	var args []string
	rest := command
	for {
		rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
		if rest == "" {
			return args
		}
		if q := rest[0]; q == '"' || q == '\'' {
			end := strings.IndexByte(rest[1:], q)
			if end < 0 {
				t.Fatalf("unclosed quote in %q", command)
			}
			args = append(args, rest[1:1+end])
			rest = rest[2+end:]
			continue
		}
		end := strings.IndexFunc(rest, func(r rune) bool { return unicode.IsSpace(r) || r == '"' || r == '\'' })
		if end < 0 {
			end = len(rest)
		}
		args = append(args, rest[:end])
		rest = rest[end:]
	}
}

// cursorAgentStatusLine is the statusLine of the settings Command writes for the agent.
func cursorAgentStatusLine(t *testing.T, l aca.Launch) map[string]any {
	t.Helper()
	if _, err := (aca.Cursor{}).Command(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	var settings struct {
		StatusLine map[string]any `json:"statusLine"`
	}
	readJSON(t, filepath.Join(l.BackupDir, "cursor-config", "cli-config.json"), &settings)
	if settings.StatusLine["type"] != "command" {
		t.Fatalf("status line = %v", settings.StatusLine)
	}
	return settings.StatusLine
}

// cursorStatusLine runs the status line of the settings Command writes as Cursor runs
// it: split as Cursor splits it, no shell, with what it shows on its input. It returns
// what that printed.
func cursorStatusLine(t *testing.T, l aca.Launch, input []byte) string {
	t.Helper()
	command, _ := cursorAgentStatusLine(t, l)["command"].(string)
	args := cursorArgv(t, command)
	if len(args) == 0 {
		t.Fatalf("status line command %q", command)
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = strings.NewReader(string(input))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("status line %q: %v %s", command, err, out)
	}
	return string(out)
}

func cursorStatusInput(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "cursor", "2026.11.05", "status", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// cursorLaunch is a launch with a backup and a user home of its own.
func cursorLaunch(t *testing.T) aca.Launch {
	t.Helper()
	return aca.Launch{UserHome: t.TempDir(), BackupDir: filepath.Join(t.TempDir(), "backup"), Dir: "/work"}
}

// TestCursorContextSize reads the status line's input as 2026.11.05 gives it: the share
// of the window rounded to tokens mid-session, and null before the first message and
// after /clear, which is 0 tokens and no window.
func TestCursorContextSize(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		file string
		want aca.ContextSize
	}{
		{"before-first-message.json", aca.ContextSize{}},
		{"mid-session.json", aca.ContextSize{Tokens: 12_032, Window: 256_000}},
		{"after-clear.json", aca.ContextSize{}},
	} {
		l := cursorLaunch(t)
		if out := cursorStatusLine(t, l, cursorStatusInput(t, tc.file)); out != "" {
			t.Errorf("%s: status line printed %q", tc.file, out)
		}
		got, follow, err := aca.Cursor{}.ContextSize(l)
		if err != nil || got != tc.want || follow != nil {
			t.Errorf("%s: context size = %+v, %v, following %q; want %+v and nothing to follow", tc.file, got, err, follow, tc.want)
		}
	}
}

// TestCursorContextSizeUnknown: nothing is known before the status line first ran, or
// without a backup, nor from a file that does not parse, which is an error to log.
func TestCursorContextSizeUnknown(t *testing.T) {
	t.Parallel()
	if _, _, err := (aca.Cursor{}).ContextSize(aca.Launch{}); !errors.Is(err, aca.ErrNoContextSize) {
		t.Errorf("without a backup: %v", err)
	}
	l := cursorLaunch(t)
	if _, _, err := (aca.Cursor{}).ContextSize(l); !errors.Is(err, aca.ErrNoContextSize) {
		t.Errorf("before the status line ran: %v", err)
	}
	cursorStatusLine(t, l, []byte(`{"session_id": "s-1", "context_window": {`))
	if _, _, err := (aca.Cursor{}).ContextSize(l); err == nil || errors.Is(err, aca.ErrNoContextSize) {
		t.Errorf("a file that does not parse: %v", err)
	}
	cursorStatusLine(t, l, []byte(`{"session_id": "s-1"}`))
	if _, _, err := (aca.Cursor{}).ContextSize(l); !errors.Is(err, aca.ErrNoContextSize) {
		t.Errorf("no context window: %v", err)
	}
}

// writeCursorUserSettings writes the user's ~/.cursor/cli-config.json.
func writeCursorUserSettings(t *testing.T, home, settings string) {
	t.Helper()
	user := filepath.Join(home, ".cursor", "cli-config.json")
	if err := os.MkdirAll(filepath.Dir(user), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(user, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestCursorStatusLineRunsTheUsers: the agent's statusLine replaces the user's own, so
// theirs runs after wdx's on the same input, through the script, and what it prints is
// what shows; their padding, updateIntervalMs and timeoutMs are kept. A command of
// several, ending in a comment, with a quote of each kind, runs whole. The script's
// path is given as Cursor splits it: in double quotes when it has a space and a
// single quote, in single quotes when it has a double quote.
func TestCursorStatusLineRunsTheUsers(t *testing.T) {
	t.Parallel()
	for _, dir := range []string{"backup", "it's here", `say "hi"`} {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			l := cursorLaunch(t)
			l.BackupDir = filepath.Join(filepath.Dir(l.BackupDir), dir)
			theirs := filepath.Join(t.TempDir(), "theirs.json")
			settings, err := json.Marshal(map[string]any{"version": 1, "statusLine": map[string]any{
				"type": "command", "command": "cat > '" + theirs + `'; echo "their line" 'quoted' # their comment`,
				"padding": 1, "updateIntervalMs": 500, "timeoutMs": 3000,
			}})
			if err != nil {
				t.Fatal(err)
			}
			writeCursorUserSettings(t, l.UserHome, string(settings))
			input := cursorStatusInput(t, "mid-session.json")
			if out := cursorStatusLine(t, l, input); out != "their line quoted\n" {
				t.Errorf("status line printed %q", out)
			}
			if got, err := os.ReadFile(theirs); err != nil || string(got) != string(input) {
				t.Errorf("their input = %q, %v", got, err)
			}
			if got, _, err := (aca.Cursor{}).ContextSize(l); err != nil || got.Tokens != 12_032 {
				t.Errorf("context size = %+v, %v", got, err)
			}
			line := cursorAgentStatusLine(t, l)
			command, _ := line["command"].(string)
			if script := filepath.Join(l.BackupDir, "cursor-status.sh"); cursorArgv(t, command)[1] != script || !strings.HasPrefix(command, "sh ") {
				t.Errorf("command = %q, want sh and %q", command, script)
			}
			delete(line, "command")
			if want := map[string]any{"type": "command", "padding": 1.0, "updateIntervalMs": 500.0, "timeoutMs": 3000.0}; !equalJSON(line, want) {
				t.Errorf("status line = %v, want %v", line, want)
			}
		})
	}
}

// TestCursorStatusLineWithoutTheUsers: a user with no status line, or one that is not
// a command, gets wdx's alone, type and command, and nothing of theirs kept.
func TestCursorStatusLineWithoutTheUsers(t *testing.T) {
	t.Parallel()
	for name, settings := range map[string]string{
		"none":          `{"version": 1}`,
		"not a command": `{"statusLine": {"type": "static", "command": "echo no", "padding": 2}}`,
		"not an object": `{"statusLine": "echo no"}`,
		"no command":    `{"statusLine": {"type": "command", "padding": 2}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			l := cursorLaunch(t)
			writeCursorUserSettings(t, l.UserHome, settings)
			if out := cursorStatusLine(t, l, cursorStatusInput(t, "mid-session.json")); out != "" {
				t.Errorf("status line printed %q", out)
			}
			line := cursorAgentStatusLine(t, l)
			if command, _ := line["command"].(string); len(line) != 2 || !strings.HasPrefix(command, "sh ") {
				t.Errorf("status line = %v, want type and command alone", line)
			}
		})
	}
}

// TestCursorStatusLinePathCannotBeGiven: a backup whose path holds both kinds of
// quote cannot be an argument of a command Cursor splits, so the start fails.
func TestCursorStatusLinePathCannotBeGiven(t *testing.T) {
	t.Parallel()
	l := cursorLaunch(t)
	l.BackupDir = filepath.Join(filepath.Dir(l.BackupDir), `it's "here"`)
	if _, err := (aca.Cursor{}).Command(t.Context(), l); err == nil || !strings.Contains(err.Error(), "both kinds of quote") {
		t.Errorf("Command: %v, want the path refused", err)
	}
}

// TestCursorBeginSessionForgetsTheContextSize: the old chat's size is not the new
// session's, which tells its own once Cursor starts.
func TestCursorBeginSessionForgetsTheContextSize(t *testing.T) {
	t.Parallel()
	l := cursorLaunch(t)
	cursorStatusLine(t, l, cursorStatusInput(t, "mid-session.json"))
	if err := (aca.Cursor{}).BeginSession(t.Context(), l, "s-2"); err != nil {
		t.Fatal(err)
	}
	if got, _, err := (aca.Cursor{}).ContextSize(l); !errors.Is(err, aca.ErrNoContextSize) {
		t.Errorf("after a new session began: %+v, %v", got, err)
	}
}

// equalJSON compares two values as JSON.
func equalJSON(a, b any) bool {
	x, err := json.Marshal(a)
	if err != nil {
		return false
	}
	y, err := json.Marshal(b)
	return err == nil && string(x) == string(y)
}
