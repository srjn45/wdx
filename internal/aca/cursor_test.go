package aca_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/runtest"
)

func TestCursorIsKnown(t *testing.T) {
	t.Parallel()
	got, ok := aca.ByID(aca.CursorID)
	if !ok || got.Program() != "cursor-agent" {
		t.Fatalf("ByID(%q) = %v, %v", aca.CursorID, got, ok)
	}
	if aca.Default().ID() != aca.ClaudeCodeID {
		t.Fatalf("the default is %q, want Claude Code first", aca.Default().ID())
	}
	if aca.CanBeginSession(aca.Cursor{}) {
		t.Fatal("Cursor can begin a session, though it has no sessions yet")
	}
}

func TestCursorCommand(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		launch aca.Launch
		want   []string
	}{
		{"ask passes nothing", aca.Launch{Permissions: aca.PermissionsAsk}, []string{"cursor-agent", "--trust"}},
		{"none is ask", aca.Launch{}, []string{"cursor-agent", "--trust"}},
		{"full", aca.Launch{Permissions: aca.PermissionsFull},
			[]string{"cursor-agent", "--trust", "--force", "--sandbox", "disabled", "--approve-mcps"}},
		{"model", aca.Launch{Model: "gpt-5.2"}, []string{"cursor-agent", "--trust", "--model", "gpt-5.2"}},
		{"no backup, no resume", aca.Launch{SessionID: "s-1", Dir: "/work"}, []string{"cursor-agent", "--trust"}},
		{"no backup, no note", aca.Launch{Note: "use wdx"}, []string{"cursor-agent", "--trust"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := aca.Cursor{}.Command(t.Context(), c.launch)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, c.want) {
				t.Fatalf("command = %q, want %q", got, c.want)
			}
		})
	}
}

// TestCursorGivesTheNote: a plugin in the backup, whose sessionStart hook prints the
// note as additional_context. The hook is run here as Cursor runs it, through sh.
func TestCursorGivesTheNote(t *testing.T) {
	t.Parallel()
	backup := filepath.Join(t.TempDir(), "it's here")
	note := "use wdx, it's \"first\""
	got, err := aca.Cursor{}.Command(t.Context(), aca.Launch{BackupDir: backup, Note: note, Model: "auto", Permissions: aca.PermissionsFull})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(backup, "cursor-plugin")
	want := []string{"env", "CURSOR_CONFIG_DIR=" + filepath.Join(backup, "cursor-config"), "cursor-agent", "--trust", "--model", "auto", "--force", "--sandbox", "disabled", "--approve-mcps", "--plugin-dir", dir}
	if !slices.Equal(got, want) {
		t.Fatalf("command = %q, want %q", got, want)
	}
	var manifest struct{ Name, Hooks string }
	readJSON(t, filepath.Join(dir, ".cursor-plugin", "plugin.json"), &manifest)
	if manifest.Name != "wdx" || manifest.Hooks != "hooks.json" {
		t.Fatalf("plugin.json = %+v", manifest)
	}
	var hooks struct {
		Version int
		Hooks   struct {
			SessionStart []struct{ Command string } `json:"sessionStart"`
		}
	}
	readJSON(t, filepath.Join(dir, manifest.Hooks), &hooks)
	if hooks.Version != 1 || len(hooks.Hooks.SessionStart) != 1 {
		t.Fatalf("hooks.json = %+v", hooks)
	}
	out, err := runHook(t, hooks.Hooks.SessionStart[0].Command)
	if err != nil {
		t.Fatalf("the hook: %v: %s", err, out)
	}
	var printed struct {
		AdditionalContext string `json:"additional_context"`
	}
	if err := json.Unmarshal(out, &printed); err != nil || printed.AdditionalContext != note {
		t.Fatalf("the hook printed %q (%v), want the note", out, err)
	}

	// A later start writes it again, with the note of that start.
	if _, err := (aca.Cursor{}).Command(t.Context(), aca.Launch{BackupDir: backup, Note: "another"}); err != nil {
		t.Fatal(err)
	}
	if out, _ := runHook(t, hooks.Hooks.SessionStart[0].Command); !bytes.Contains(out, []byte(`"another"`)) {
		t.Fatalf("the hook printed %q after the second start", out)
	}
}

// TestCursorConfigFolder: with a backup, Cursor runs with a config folder of the
// agent's own, whose cli-config.json is the user's, key for key, with steering on,
// written anew at every start; the user's file is left as it was.
func TestCursorConfigFolder(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	user := filepath.Join(home, ".cursor", "cli-config.json")
	if err := os.MkdirAll(filepath.Dir(user), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"version": 1, "model": {"modelId": "gpt-5.2"}, "steering": false,
 "permissions": {"allow": ["Shell(ls)"], "deny": []}, "approvalMode": "allowlist"}`)
	if err := os.WriteFile(user, original, 0o600); err != nil {
		t.Fatal(err)
	}
	backup := t.TempDir()
	launch := aca.Launch{UserHome: home, BackupDir: backup, Model: "auto"}
	got, err := aca.Cursor{}.Command(t.Context(), launch)
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(backup, "cursor-config")
	want := []string{"env", "CURSOR_CONFIG_DIR=" + config, "cursor-agent", "--trust", "--model", "auto"}
	if !slices.Equal(got, want) {
		t.Fatalf("command = %q, want %q", got, want)
	}
	copied := filepath.Join(config, "cli-config.json")
	wantCopy := map[string]any{"version": 1.0, "model": map[string]any{"modelId": "gpt-5.2"}, "steering": true,
		"permissions": map[string]any{"allow": []any{"Shell(ls)"}, "deny": []any{}}, "approvalMode": "allowlist"}
	var agentSettings map[string]any
	readJSON(t, copied, &agentSettings)
	if !reflect.DeepEqual(agentSettings, wantCopy) {
		t.Fatalf("the agent's settings = %v, want %v", agentSettings, wantCopy)
	}
	if info, err := os.Stat(copied); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the agent's settings: %v, %v; want mode 0600, as they hold an account cache", info, err)
	}

	// The agent changes its copy, as --model does, and the user their file: the next
	// start copies the user's again.
	if err := os.WriteFile(copied, []byte(`{"model": {"modelId": "the agent's"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	original = []byte(`{"model": {"modelId": "the user's"}, "editor": {"vimMode": true}}`)
	if err := os.WriteFile(user, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (aca.Cursor{}).Command(t.Context(), launch); err != nil {
		t.Fatal(err)
	}
	agentSettings = nil
	readJSON(t, copied, &agentSettings)
	wantCopy = map[string]any{"model": map[string]any{"modelId": "the user's"}, "editor": map[string]any{"vimMode": true}, "steering": true}
	if !reflect.DeepEqual(agentSettings, wantCopy) {
		t.Fatalf("the agent's settings at the next start = %v, want %v", agentSettings, wantCopy)
	}
	if data, err := os.ReadFile(user); err != nil || !bytes.Equal(data, original) {
		t.Fatalf("the user's settings became %q (%v), want them as they were", data, err)
	}
}

// TestCursorConfigWithoutTheUsersSettings: a user file that is missing, or is not a
// JSON object, gives steering alone and does not fail the start; one that is there
// but unusable is logged.
func TestCursorConfigWithoutTheUsersSettings(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		user   *string
		logged bool
	}{
		{"missing", nil, false},
		{"not JSON", ptr(`{"model": `), true},
		{"an array", ptr(`[1, 2]`), true},
		{"null", ptr(`null`), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			if c.user != nil {
				user := filepath.Join(home, ".cursor", "cli-config.json")
				if err := os.MkdirAll(filepath.Dir(user), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(user, []byte(*c.user), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var logged bytes.Buffer
			ctx := logging.NewContext(t.Context(), logging.New(&logged, slog.LevelInfo))
			backup := t.TempDir()
			if _, err := (aca.Cursor{}).Command(ctx, aca.Launch{UserHome: home, BackupDir: backup}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(backup, "cursor-config", "cli-config.json"))
			if err != nil || string(data) != `{"steering":true}` {
				t.Fatalf("the agent's settings = %q (%v), want steering alone", data, err)
			}
			if got := strings.Contains(logged.String(), "user cursor settings not copied"); got != c.logged {
				t.Fatalf("logged = %v, want %v: %s", got, c.logged, logged.String())
			}
		})
	}
}

func ptr(s string) *string { return &s }

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func TestCursorDeliver(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, message, want string }{
		{"plain", "hello", "\x1b[200~hello\x1b[201~\r"},
		{"lines stay content", "one\ntwo", "\x1b[200~one\ntwo\x1b[201~\r"},
		{"cannot end the paste", "a\x1b[201~b", "\x1b[200~ab\x1b[201~\r"},
		{"nor by a marker its removal makes", "a\x1b[20\x1b[201~1~b", "\x1b[200~ab\x1b[201~\r"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var got bytes.Buffer
			if err := (aca.Cursor{}).Deliver(&got, c.message); err != nil {
				t.Fatal(err)
			}
			if got.String() != c.want {
				t.Fatalf("got %q, want %q", got.String(), c.want)
			}
		})
	}
	for writes := range 2 {
		if err := (aca.Cursor{}).Deliver(&failingWriter{writes: writes}, "hello"); err == nil {
			t.Fatalf("no error after %d writes", writes)
		}
	}
}

func TestCursorInterruptIsCtrlC(t *testing.T) {
	t.Parallel()
	if got := (aca.Cursor{}).Interrupt(); string(got) != "\x03" {
		t.Fatalf("got %q", got)
	}
}

// TestCursorModels: what cursor-agent 2026.10.19 printed, run through a stand-in for
// the login shell that prints it, so the real one never runs.
func TestCursorModels(t *testing.T) {
	t.Parallel()
	models, err := aca.Cursor{}.Models(context.Background(), aca.Launch{Shell: printingShell(t, "testdata/cursor/models.txt", 0)})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 270 {
		t.Fatalf("%d models, want 270", len(models))
	}
	want := map[int]aca.Model{
		0:   {ID: "auto", Name: "Auto", Default: true},
		1:   {ID: "gpt-5.3-codex-low", Name: "Codex 5.3 Low"},
		269: {ID: "deepseek-v41-flash-1017-max", Name: "DeepSeek V4.1 Flash 1017 Max"},
	}
	for i, m := range want {
		if models[i] != m {
			t.Errorf("model %d = %+v, want %+v", i, models[i], m)
		}
	}
	defaults := 0
	for _, m := range models {
		if m.Default {
			defaults++
		}
		if m.ID == "grok-4.7-low-fast" && m.Name != "Grok 4.7 Low Fast" {
			t.Errorf("name %q, want its zero-width spaces and double space gone", m.Name)
		}
	}
	if defaults != 1 {
		t.Errorf("%d defaults, want 1", defaults)
	}
}

func TestCursorModelsFail(t *testing.T) {
	t.Parallel()
	empty := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(empty, []byte("Available models\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, shell := range map[string]string{
		"no models": printingShell(t, empty, 0),
		"it fails":  printingShell(t, "testdata/cursor/models.txt", 1),
	} {
		if _, err := (aca.Cursor{}).Models(context.Background(), aca.Launch{Shell: shell}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// printingShell is a login shell that prints file, whatever it is asked to run, and
// exits with code.
func printingShell(t *testing.T, file string, code int) string {
	t.Helper()
	abs, err := filepath.Abs(file)
	if err != nil {
		t.Fatal(err)
	}
	shell := filepath.Join(t.TempDir(), "shell")
	script := fmt.Sprintf("#!/bin/sh\ncat '%s'\nexit %d\n", abs, code)
	if err := runtest.WriteExecutable(shell, script); err != nil {
		t.Fatal(err)
	}
	return shell
}

// runHook runs a hook's command as Cursor does, through sh.
func runHook(t *testing.T, command string) ([]byte, error) {
	t.Helper()
	return exec.Command("/bin/sh", "-c", command).CombinedOutput()
}
