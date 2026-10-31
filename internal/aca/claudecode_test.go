package aca_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

func TestByID(t *testing.T) {
	t.Parallel()
	known, ok := aca.ByID("claude_code")
	if !ok || known.ID() != "claude_code" {
		t.Errorf("ByID(claude_code) = %v, %v", known, ok)
	}
	if _, ok := known.ScreenParser().(aca.ClaudeCodeScreenParser); !ok {
		t.Errorf("Claude Code's parser is %T", known.ScreenParser())
	}
	if got, ok := aca.ByID("no_such_aca"); ok {
		t.Errorf("ByID(no_such_aca) = %v", got)
	}
}

func TestClaudeCodeStartsASessionWithoutATranscript(t *testing.T) {
	t.Parallel()
	got, err := aca.ClaudeCode{}.Command(aca.Launch{Dir: "/work", SessionID: "s-1", UserHome: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"claude", "--session-id", "s-1"}; !slices.Equal(got, want) {
		t.Errorf("command = %q, want %q", got, want)
	}
}

func TestClaudeCodeResumesASessionWithATranscript(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "-work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s-1.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := aca.ClaudeCode{}.Command(aca.Launch{Dir: "/work", SessionID: "s-1", UserHome: home})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"claude", "--resume", "s-1"}; !slices.Equal(got, want) {
		t.Errorf("command = %q, want %q", got, want)
	}
}

func TestClaudeCodeIgnoresAnotherSessionsTranscript(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "-work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s-2.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := aca.ClaudeCode{}.Command(aca.Launch{Dir: "/work", SessionID: "s-1", UserHome: home})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"claude", "--session-id", "s-1"}; !slices.Equal(got, want) {
		t.Errorf("command = %q, want %q", got, want)
	}
}
