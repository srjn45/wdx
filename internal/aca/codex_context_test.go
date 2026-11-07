package aca_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

// codexContextLines is testdata/codex/0.163.0/session/context.jsonl: what Codex
// 0.163.0 appends to a session file as its turns go, trimmed and made up, after the
// sample's session_meta and turn_context lines. Its lines, counted from 1:
//
//	1 the first turn starts        8 token_count: 14137 of 258400
//	2 token_count with info null   9 the turn ends
//	3 the user's message          10 a turn that compacts starts
//	4 token_count: 14626          11 compacted
//	5 the turn ends               12 token_count: 7274, the summary's request
//	6 the second turn starts      13 the compaction item
//	7 the assistant's message     14 the turn ends
func codexContextLines(t *testing.T, n int) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "codex", "0.163.0", "session", "context.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(data), "\n")
	if n > len(lines) {
		t.Fatalf("context.jsonl has %d lines, not %d", len(lines), n)
	}
	return strings.Join(lines[:n], "")
}

// codexContextHome is a home where Codex keeps the sample session with the first n
// lines of codexContextLines after it, and the file it is in.
func codexContextHome(t *testing.T, n int) (home, file string) {
	t.Helper()
	home = codexHome(t, map[string]struct{ source, cwd string }{codexSession: {"user", ""}})
	file = codexFile(home, codexSession)
	appendFile(t, file, codexContextLines(t, n))
	return home, file
}

func codexContextSize(t *testing.T, l aca.Launch) (aca.ContextSize, []string, error) {
	t.Helper()
	return aca.Codex{}.ContextSize(l)
}

// TestCodexContextSize reads session files as 0.163.0 writes them: the last token_count
// line whose info is not null tells the last request's tokens and the window, which a
// compaction brings down to the summary's request. The session file is to be followed
// even before it tells anything.
func TestCodexContextSize(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		what  string
		lines int
		want  aca.ContextSize
		err   error
	}{
		{"before any token_count", 1, aca.ContextSize{}, aca.ErrNoContextSize},
		{"the first token_count, with info null", 2, aca.ContextSize{}, aca.ErrNoContextSize},
		{"the first response", 4, aca.ContextSize{Tokens: 14_626, Window: 258_400}, nil},
		{"mid-session", 9, aca.ContextSize{Tokens: 14_137, Window: 258_400}, nil},
		{"after a compaction", 14, aca.ContextSize{Tokens: 7_274, Window: 258_400}, nil},
	} {
		home, file := codexContextHome(t, tc.lines)
		got, follow, err := codexContextSize(t, aca.Launch{UserHome: home, SessionID: codexSession, BackupDir: t.TempDir()})
		if !errors.Is(err, tc.err) || got != tc.want {
			t.Errorf("%s: context size = %+v, %v; want %+v, %v", tc.what, got, err, tc.want, tc.err)
		}
		if !slices.Equal(follow, []string{file}) {
			t.Errorf("%s: following %q, want the session file", tc.what, follow)
		}
	}
}

// TestCodexContextSizeFurtherBackThanABlock: the last token_count line can be far
// from the end, behind a tool's long output, and a line can be longer than what is
// read at a time. The whole file is never read.
func TestCodexContextSizeFurtherBackThanABlock(t *testing.T) {
	t.Parallel()
	home, file := codexContextHome(t, 9)
	for _, size := range []int{100 << 10, 70 << 10, 300} {
		output, err := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{
			"type": "function_call_output", "call_id": "call_01a1abcc", "output": strings.Repeat("x", size),
		}})
		if err != nil {
			t.Fatal(err)
		}
		appendFile(t, file, string(output)+"\n")
	}
	got, follow, err := codexContextSize(t, aca.Launch{UserHome: home, SessionID: codexSession, BackupDir: t.TempDir()})
	if err != nil || got != (aca.ContextSize{Tokens: 14_137, Window: 258_400}) {
		t.Errorf("context size = %+v, %v", got, err)
	}
	if !slices.Equal(follow, []string{file}) {
		t.Errorf("following %q, want the session file", follow)
	}
}

// TestCodexContextSizeFromThePathsFile: the session is read where the backup's paths
// file says Codex keeps it, before the glob looks.
func TestCodexContextSizeFromThePathsFile(t *testing.T) {
	t.Parallel()
	_, kept := codexContextHome(t, 9)
	backup := t.TempDir()
	paths, err := json.Marshal(map[string]string{codexSession: kept})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "codex-sessions.json"), paths, 0o600); err != nil {
		t.Fatal(err)
	}
	got, follow, err := codexContextSize(t, aca.Launch{UserHome: t.TempDir(), SessionID: codexSession, BackupDir: backup})
	if err != nil || got != (aca.ContextSize{Tokens: 14_137, Window: 258_400}) {
		t.Errorf("context size = %+v, %v", got, err)
	}
	if !slices.Equal(follow, []string{kept}) {
		t.Errorf("following %q, want %s", follow, kept)
	}
}

// TestCodexContextSizeWithoutASession: nothing is known, and nothing followed, while
// the session is wdx's own id that Codex never kept, or Codex deleted the file the
// paths file names.
func TestCodexContextSizeWithoutASession(t *testing.T) {
	t.Parallel()
	home := codexHome(t, map[string]struct{ source, cwd string }{})
	backup := t.TempDir()
	if got, follow, err := codexContextSize(t, aca.Launch{UserHome: home, SessionID: codexSession, BackupDir: backup}); !errors.Is(err, aca.ErrNoContextSize) || follow != nil {
		t.Errorf("with no session file: %+v, %q, %v", got, follow, err)
	}
	paths, err := json.Marshal(map[string]string{codexSession: codexFile(home, codexSession)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "codex-sessions.json"), paths, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, follow, err := codexContextSize(t, aca.Launch{UserHome: home, SessionID: codexSession, BackupDir: backup}); !errors.Is(err, aca.ErrNoContextSize) || follow != nil {
		t.Errorf("with the session file gone: %+v, %q, %v", got, follow, err)
	}
}
