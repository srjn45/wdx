package aca_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

func TestAntigravityIsKnown(t *testing.T) {
	t.Parallel()
	got, ok := aca.ByID("antigravity")
	if !ok || got.ID() != "antigravity" || got.Program() != "agy" {
		t.Fatalf("ByID(antigravity) = %v, %v", got, ok)
	}
	if aca.Default().ID() != aca.ClaudeCodeID {
		t.Errorf("the default is %s, want Claude Code still first", aca.Default().ID())
	}
	if aca.CanBeginSession(got) {
		t.Error("it can begin a session, with no BeginSession of its own")
	}
	if _, ok := got.ScreenParser().(aca.AntigravityScreenParser); !ok {
		t.Errorf("Antigravity's parser is %T", got.ScreenParser())
	}
}

// TestAntigravityCommand: the session id is not passed, as Antigravity names its
// own; every start trusts its dir and queues what is typed during a turn.
func TestAntigravityCommand(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		launch aca.Launch
		want   []string
	}{
		{"ask", aca.Launch{Dir: "/work", SessionID: "s-1"},
			[]string{"agy", "--settings", `{"queuedMessages":"queue","trustedWorkspaces":["/work"]}`}},
		{"model", aca.Launch{Dir: "/work", SessionID: "s-1", Model: "gemini-3.8-flash-low", Permissions: aca.PermissionsAsk},
			[]string{"agy", "--model", "gemini-3.8-flash-low", "--settings", `{"queuedMessages":"queue","trustedWorkspaces":["/work"]}`}},
		{"full", aca.Launch{Dir: "/work", SessionID: "s-1", Permissions: aca.PermissionsFull},
			[]string{"agy", "--dangerously-skip-permissions", "--settings", `{"enableTerminalSandbox":false,"queuedMessages":"queue","trustedWorkspaces":["/work"]}`}},
		{"note without a backup", aca.Launch{Dir: "/work", Note: "use wdx"},
			[]string{"agy", "--settings", `{"queuedMessages":"queue","trustedWorkspaces":["/work"]}`}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := aca.Antigravity{}.Command(c.launch)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("command = %q, want %q", got, c.want)
			}
		})
	}
}

// TestAntigravityGivesTheNote: in a folder holding only the note, as --add-dir makes
// it part of the workspace, written anew at every start, ending with where to work,
// as Antigravity creates new files in the first folder by path, which is the note's.
func TestAntigravityGivesTheNote(t *testing.T) {
	t.Parallel()
	backup := t.TempDir()
	if err := os.WriteFile(filepath.Join(backup, "transcript.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, note := range []string{"use wdx", "use wdx, again"} {
		got, err := aca.Antigravity{}.Command(aca.Launch{Dir: "/work", BackupDir: backup, Note: note})
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(backup, "antigravity-note")
		if want := []string{"agy", "--add-dir", dir, "--settings", `{"queuedMessages":"queue","trustedWorkspaces":["/work"]}`}; !slices.Equal(got, want) {
			t.Fatalf("command = %q, want %q", got, want)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name() != "AGENTS.md" {
			t.Fatalf("the note's folder holds %v", entries)
		}
		data, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
		if err != nil {
			t.Fatal(err)
		}
		want := note + "\n\nThe workspace folder `" + dir + "` holds only these instructions from wdx: never create or change files there." +
			" Your project is `/work`, where you were started: work and create files there.\n"
		if string(data) != want {
			t.Errorf("note = %q, want %q", data, want)
		}
	}
}

func TestAntigravityDeliver(t *testing.T) {
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
			if err := (aca.Antigravity{}).Deliver(&got, c.message); err != nil {
				t.Fatal(err)
			}
			if got.String() != c.want {
				t.Fatalf("got %q, want %q", got.String(), c.want)
			}
		})
	}
}

func TestAntigravityDeliverFails(t *testing.T) {
	t.Parallel()
	for writes := range 2 {
		if err := (aca.Antigravity{}).Deliver(&failingWriter{writes: writes}, "hello"); err == nil {
			t.Fatalf("no error after %d writes", writes)
		}
	}
}

func TestAntigravityInterruptIsEsc(t *testing.T) {
	t.Parallel()
	if got := (aca.Antigravity{}).Interrupt(); string(got) != "\x1b" {
		t.Fatalf("got %q", got)
	}
}
