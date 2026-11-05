package aca_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

func codexBrief(t *testing.T, backup string, limit int) (aca.Brief, string) {
	t.Helper()
	got, err := aca.Codex{}.Brief(t.Context(), aca.Launch{BackupDir: backup}, limit)
	if err != nil {
		t.Fatal(err)
	}
	return got, strings.Join(got.Start, "\n") + "\n" + strings.Join(got.Rest, "\n")
}

// codexBackupOf is a backup holding transcript, with a copy of each of the user's
// threads, as BackUp keeps only theirs.
func codexBackupOf(t *testing.T, transcript string, user ...string) string {
	t.Helper()
	backup := t.TempDir()
	if err := os.WriteFile(filepath.Join(backup, aca.TranscriptFile), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(backup, "transcripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, id := range user {
		if err := os.WriteFile(filepath.Join(backup, "transcripts", id+".jsonl"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return backup
}

// The recording's brief starts at the first request of the session /new began, and
// tells its tool call in a few words.
func TestCodexBriefOfTheRecording(t *testing.T) {
	t.Parallel()
	home, backup := t.TempDir(), t.TempDir()
	codexRecordedHome(t, home, "sessions")
	writeTurns(t, backup, codexRecorded, codexRecorded, codexRecordedSub, codexRecorded, codexRecorded, codexRecordedNew)
	if _, err := (aca.Codex{}).BackUp(t.Context(), aca.Launch{UserHome: home, BackupDir: backup}); err != nil {
		t.Fatal(err)
	}
	got, text := codexBrief(t, backup, 64<<10)
	lines := strings.Split(readFile(t, filepath.Join(backup, aca.TranscriptFile)), "\n")
	if lines[got.From] != "## User · 2026-11-05T14:48:46.522Z" || lines[got.From-2] != "# Session "+codexRecordedNew+" · codex" {
		t.Errorf("from %d: %q", got.From, lines[got.From])
	}
	if got.Next != int64(len(lines)-1) || got.OmittedCount != 0 {
		t.Errorf("next = %d, want %d; omitted %d", got.Next, len(lines)-1, got.OmittedCount)
	}
	checkGolden(t, text, filepath.Join(codexRecording, "brief.md"))
}

// A restart's thread goes on from what came before, and a /new's does not, whichever
// came first.
func TestCodexBriefIsBoundedByNewNotARestart(t *testing.T) {
	t.Parallel()
	session := func(id, request string, minute int) string {
		return "# Session " + id + " · codex\n\n" + message("User", minute, request) + message("Assistant", minute, "done: "+request)
	}
	for _, c := range []struct {
		name     string
		turns    [][]string // between restarts
		from     string
		notFound []string
	}{
		{name: "restart", turns: [][]string{{codexEarlier}, {codexSession}}, from: "the earlier request"},
		{name: "new", turns: [][]string{{codexEarlier, codexSession}}, from: "the session's request", notFound: []string{"the earlier request"}},
		{name: "restart after new", turns: [][]string{{codexEarlier, codexSession}, {codexLater}}, from: "the session's request", notFound: []string{"the earlier request"}},
		{name: "new after restart", turns: [][]string{{codexEarlier}, {codexSession, codexLater}}, from: "the later request", notFound: []string{"the earlier request", "the session's request"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			transcript := session(codexEarlier, "the earlier request", 1) + session(codexSession, "the session's request", 2) + session(codexLater, "the later request", 3)
			backup := codexBackupOf(t, transcript, codexEarlier, codexSession, codexLater)
			l := aca.Launch{UserHome: t.TempDir(), BackupDir: backup}
			var threads []string
			for i, turns := range c.turns {
				if i > 0 {
					beginCodexSession(t, l, codexRestarted)
				}
				// The thread naming the conversation is not the user's.
				threads = append(append(threads, turns...), codexNamer)
				writeTurns(t, backup, threads...)
			}
			got, text := codexBrief(t, backup, 64<<10)
			if !strings.HasPrefix(text, "## User") || !strings.Contains(got.Start[2], c.from) {
				t.Errorf("starts at %q, want %q:\n%s", got.Start, c.from, text)
			}
			wantInOrder(t, text, c.from, "done: the later request")
			wantNot(t, text, c.notFound...)
		})
	}
}

// A Codex compaction holds no summary to start from: the brief keeps the request before
// it and the latest messages that fit, as the compaction is one of them.
func TestCodexBriefReadsACompactionAsAMessage(t *testing.T) {
	t.Parallel()
	transcript := "# Session " + codexSession + " · codex\n\n" +
		message("User", 1, "the first request") +
		message("Assistant", 2, "an old answer "+strings.Repeat("x", 200)) +
		message("Compacted", 3, "(Codex keeps its summary encrypted)") +
		message("User", 4, "and on")
	backup := codexBackupOf(t, transcript, codexSession)
	writeTurns(t, backup, codexSession)
	got, text := codexBrief(t, backup, 200)
	if !strings.Contains(got.Start[2], "the first request") {
		t.Errorf("starts at %q", got.Start)
	}
	wantInOrder(t, text, "the first request", "## Compacted", "(Codex keeps its summary encrypted)", "and on")
	wantNot(t, text, "an old answer")
	if got.OmittedCount == 0 {
		t.Errorf("nothing left out: %+v", got)
	}
}
