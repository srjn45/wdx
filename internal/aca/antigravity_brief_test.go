package aca_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

func antigravityBrief(t *testing.T, backup string, limit int) (aca.Brief, string) {
	t.Helper()
	got, err := aca.Antigravity{}.Brief(t.Context(), aca.Launch{BackupDir: backup}, limit)
	if err != nil {
		t.Fatal(err)
	}
	return got, strings.Join(got.Start, "\n") + "\n" + strings.Join(got.Rest, "\n")
}

// The recording's second conversation, here a restart's, goes on from the first: the
// brief starts at the first's compaction, whose summary is readable.
func TestAntigravityBriefOfTheRecording(t *testing.T) {
	t.Parallel()
	home, backup := t.TempDir(), t.TempDir()
	antigravityRecordedHome(t, home)
	lines, _ := antigravityRecordedStarts(t, backup)
	writeHooksFile(t, backup, lines[0])
	beginAntigravitySession(t, backup, antigravityRestarted)
	writeHooksFile(t, backup, lines...)
	antigravityBackUp(t, home, backup)
	got, text := antigravityBrief(t, backup, 64<<10)
	transcript := strings.Split(readFile(t, filepath.Join(backup, aca.TranscriptFile)), "\n")
	if transcript[got.From] != "## Compacted · 2026-11-06T09:02:54Z" {
		t.Errorf("from %d: %q", got.From, transcript[got.From])
	}
	if got.Next != int64(len(transcript)-1) || got.OmittedCount != 0 {
		t.Errorf("next = %d, want %d; omitted %d", got.Next, len(transcript)-1, got.OmittedCount)
	}
	checkGolden(t, text, filepath.Join(antigravityRecording, "brief.md"))
}

// In the recording as it was, the second conversation came from /clear: the brief
// starts at its request.
func TestAntigravityBriefOfTheRecordingStartsAfterClear(t *testing.T) {
	t.Parallel()
	home, backup := t.TempDir(), t.TempDir()
	antigravityRecordedHome(t, home)
	lines, _ := antigravityRecordedStarts(t, backup)
	writeHooksFile(t, backup, lines...)
	antigravityBackUp(t, home, backup)
	_, text := antigravityBrief(t, backup, 64<<10)
	wantInOrder(t, text, "## User · 2026-11-06T09:03:16Z", "Reply VICTOR.", "VICTOR")
	wantNot(t, text, "notes.txt", "## Compacted")
}

// antigravityBackupOf is a backup holding transcript, with a copy of each of the
// user's conversations, as BackUp keeps only theirs, and two subagents' under the
// first.
func antigravityBackupOf(t *testing.T, transcript string, user ...string) string {
	t.Helper()
	backup := t.TempDir()
	if err := os.WriteFile(filepath.Join(backup, aca.TranscriptFile), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	subagents := filepath.Join(backup, "transcripts", antigravityFirst, "subagents")
	if err := os.MkdirAll(subagents, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{antigravitySub, antigravitySub2} {
		if err := os.WriteFile(filepath.Join(subagents, sub+".jsonl"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range user {
		if err := os.WriteFile(filepath.Join(backup, "transcripts", id+".jsonl"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return backup
}

// A restart's conversation goes on from what came before, and a /clear's does not,
// whichever came first; a subagent's is neither.
func TestAntigravityBriefIsBoundedByClearNotARestart(t *testing.T) {
	t.Parallel()
	conversation := func(id, request string, minute int) string {
		return "# Session " + id + " · antigravity\n\n" + message("User", minute, request) + message("Assistant", minute, "done: "+request)
	}
	for _, c := range []struct {
		name          string
		conversations [][]string // between restarts
		from          string
		notFound      []string
	}{
		{name: "restart", conversations: [][]string{{antigravityFirst}, {antigravityCleared}}, from: "the earlier request"},
		{name: "clear", conversations: [][]string{{antigravityFirst, antigravityCleared}}, from: "the cleared request", notFound: []string{"the earlier request"}},
		{name: "restart after clear", conversations: [][]string{{antigravityFirst, antigravityCleared}, {antigravityLater}}, from: "the cleared request", notFound: []string{"the earlier request"}},
		{name: "clear after restart", conversations: [][]string{{antigravityFirst}, {antigravityCleared, antigravityLater}}, from: "the later request", notFound: []string{"the earlier request", "the cleared request"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			transcript := conversation(antigravityFirst, "the earlier request", 1) + conversation(antigravityCleared, "the cleared request", 2) + conversation(antigravityLater, "the later request", 3)
			backup := antigravityBackupOf(t, transcript, antigravityFirst, antigravityCleared, antigravityLater)
			var starts []string
			for i, ids := range c.conversations {
				if i > 0 {
					beginAntigravitySession(t, backup, antigravityRestarted)
				}
				// A subagent after the first conversation is not a /clear, and one
				// straight after a restart is not the restart's.
				if i > 0 {
					starts = append(starts, antigravityStartLine(antigravitySub2))
				}
				for _, id := range ids {
					starts = append(starts, antigravityStartLine(id))
				}
				if i == 0 {
					starts = append(starts, antigravityStartLine(antigravitySub))
				}
				writeHooksFile(t, backup, starts...)
			}
			got, text := antigravityBrief(t, backup, 64<<10)
			if !strings.HasPrefix(text, "## User") || !strings.Contains(got.Start[2], c.from) {
				t.Errorf("starts at %q, want %q:\n%s", got.Start, c.from, text)
			}
			wantInOrder(t, text, c.from, "done: the later request")
			wantNot(t, text, c.notFound...)
		})
	}
}

// A system message is one of its own, which goes when it does not fit, not part of the
// request before it.
func TestAntigravityBriefReadsASystemMessageAsOne(t *testing.T) {
	t.Parallel()
	transcript := "# Session " + antigravityFirst + " · antigravity\n\n" +
		message("User", 1, "the first request") +
		message("System", 2, "a task reported "+strings.Repeat("x", 200)) +
		message("Assistant", 3, "the answer")
	backup := antigravityBackupOf(t, transcript, antigravityFirst)
	writeHooksFile(t, backup, antigravityStartLine(antigravityFirst))
	got, text := antigravityBrief(t, backup, 100)
	wantInOrder(t, text, "the first request", "the answer")
	wantNot(t, text, "a task reported")
	if got.OmittedCount == 0 {
		t.Errorf("nothing left out: %+v", got)
	}
}
