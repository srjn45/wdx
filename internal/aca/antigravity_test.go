package aca_test

import (
	"bytes"
	"errors"
	"io"
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
	if !aca.CanBeginSession(got) {
		t.Error("it cannot begin a session")
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
			got, err := aca.Antigravity{}.Command(t.Context(), c.launch)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("command = %q, want %q", got, c.want)
			}
		})
	}
}

// TestAntigravityGivesTheNote: in the agent's folder, as --add-dir makes it part of
// the workspace, written anew at every start, ending with where to work, as
// Antigravity creates new files in the first folder by path, which is the agent's.
func TestAntigravityGivesTheNote(t *testing.T) {
	t.Parallel()
	backup := t.TempDir()
	if err := os.WriteFile(filepath.Join(backup, "transcript.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(backup, "antigravity")
	for _, note := range []string{"use wdx", "use wdx, again"} {
		got, err := aca.Antigravity{}.Command(t.Context(), aca.Launch{Dir: "/work", BackupDir: backup, Note: note})
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"agy", "--add-dir", dir, "--settings", `{"queuedMessages":"queue","trustedWorkspaces":["/work"]}`}; !slices.Equal(got, want) {
			t.Fatalf("command = %q, want %q", got, want)
		}
		if got, want := folderHolds(t, dir), []string{".agents", "AGENTS.md"}; !slices.Equal(got, want) {
			t.Fatalf("the agent's folder holds %q, want %q", got, want)
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
	// With no note, the folder is still given, for its hooks, and an old note goes.
	got, err := aca.Antigravity{}.Command(t.Context(), aca.Launch{Dir: "/work", BackupDir: backup})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, dir) {
		t.Fatalf("command = %q, without the folder", got)
	}
	if got, want := folderHolds(t, dir), []string{".agents"}; !slices.Equal(got, want) {
		t.Fatalf("with no note the folder holds %q, want %q", got, want)
	}
}

// An agent started before the folder held its hooks has it as antigravity-note,
// moved at its next start; one a failed start left beside the new is removed.
func TestAntigravityMovesTheOldFolder(t *testing.T) {
	t.Parallel()
	for _, both := range []bool{false, true} {
		backup := t.TempDir()
		old := filepath.Join(backup, "antigravity-note")
		if err := os.MkdirAll(old, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(old, "kept"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if both {
			if err := os.Mkdir(filepath.Join(backup, "antigravity"), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := (aca.Antigravity{}).Command(t.Context(), aca.Launch{Dir: "/work", BackupDir: backup, Note: "use wdx"}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(old); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("both %v: the old folder is still there: %v", both, err)
		}
		want := []string{".agents", "AGENTS.md", "kept"}
		if both {
			want = []string{".agents", "AGENTS.md"}
		}
		if got := folderHolds(t, filepath.Join(backup, "antigravity")); !slices.Equal(got, want) {
			t.Errorf("both %v: the folder holds %q, want %q", both, got, want)
		}
	}
}

// folderHolds is the names in dir, in order.
func folderHolds(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// Deliver and Steer type the same keys: a paste, then alt+m, which gives the
// message to the running turn at its next step and submits it at rest.
func TestAntigravityDeliverAndSteer(t *testing.T) {
	t.Parallel()
	for _, send := range []struct {
		name string
		send func(io.Writer, string) error
	}{
		{"Deliver", aca.Antigravity{}.Deliver},
		{"Steer", aca.Antigravity{}.Steer},
	} {
		for _, c := range []struct{ name, message, want string }{
			{"plain", "hello", "\x1b[200~hello\x1b[201~\x1bm"},
			{"lines stay content", "one\ntwo", "\x1b[200~one\ntwo\x1b[201~\x1bm"},
			{"cannot end the paste", "a\x1b[201~b", "\x1b[200~ab\x1b[201~\x1bm"},
			{"nor by a marker its removal makes", "a\x1b[20\x1b[201~1~b", "\x1b[200~ab\x1b[201~\x1bm"},
		} {
			t.Run(send.name+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				var got bytes.Buffer
				if err := send.send(&got, c.message); err != nil {
					t.Fatal(err)
				}
				if got.String() != c.want {
					t.Fatalf("got %q, want %q", got.String(), c.want)
				}
			})
		}
	}
}

// alt+m is one write, after the paste's: its Esc read alone would interrupt the turn.
func TestAntigravityWritesAltMWhole(t *testing.T) {
	t.Parallel()
	for _, send := range []func(io.Writer, string) error{aca.Antigravity{}.Deliver, aca.Antigravity{}.Steer} {
		var w recordingWriter
		if err := send(&w, "hello"); err != nil {
			t.Fatal(err)
		}
		if want := []string{"\x1b[200~hello\x1b[201~", "\x1bm"}; !slices.Equal(w.writes, want) {
			t.Fatalf("wrote %q, want %q", w.writes, want)
		}
	}
}

func TestAntigravityDeliverFails(t *testing.T) {
	t.Parallel()
	for writes := range 2 {
		if err := (aca.Antigravity{}).Deliver(&failingWriter{writes: writes}, "hello"); err == nil {
			t.Fatalf("Deliver: no error after %d writes", writes)
		}
		if err := (aca.Antigravity{}).Steer(&failingWriter{writes: writes}, "hello"); err == nil {
			t.Fatalf("Steer: no error after %d writes", writes)
		}
	}
}

func TestAntigravityInterruptIsEsc(t *testing.T) {
	t.Parallel()
	if got := (aca.Antigravity{}).Interrupt(); string(got) != "\x1b" {
		t.Fatalf("got %q", got)
	}
}
