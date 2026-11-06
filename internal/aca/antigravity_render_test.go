package aca_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/logging"
)

// antigravityLine is a line of a transcript_full.jsonl: step index of a type, with
// the fields given, as JSON members.
func antigravityLine(index int, kind, fields string) string {
	return fmt.Sprintf(`{"step_index":%d,"source":"MODEL","type":%q,"status":"DONE","created_at":"2026-11-06T09:00:00Z",%s}`, index, kind, fields) + "\n"
}

// antigravityRender backs up a conversation of the lines, and returns the transcript
// after its heading and what the backup logged.
func antigravityRender(t *testing.T, lines ...string) (text, log string) {
	t.Helper()
	home, backup := t.TempDir(), t.TempDir()
	writeAntigravityTranscript(t, home, antigravityFirst, strings.Join(lines, ""))
	writeHooksFile(t, backup, antigravityStartLine(antigravityFirst))
	var logged bytes.Buffer
	ctx := logging.NewContext(t.Context(), logging.New(&logged, slog.LevelInfo))
	if _, err := (aca.Antigravity{}).BackUp(ctx, aca.Launch{UserHome: home, BackupDir: backup}); err != nil {
		t.Fatalf("backup: %v", err)
	}
	text = readFile(t, filepath.Join(backup, aca.TranscriptFile))
	heading := "# Session " + antigravityFirst + " · antigravity\n\n"
	if !strings.HasPrefix(text, heading) {
		t.Fatalf("transcript starts with no heading for the conversation:\n%s", text)
	}
	return strings.TrimPrefix(text, heading), logged.String()
}

const (
	antigravityUserAt      = "## User · 2026-11-06T09:00:00Z\n\nhi\n\n"
	antigravityAssistantAt = "## Assistant · 2026-11-06T09:00:00Z\n\n"
)

func TestAntigravityTranscriptOfEachStep(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		lines []string
		want  string
	}{
		{
			name:  "a request, without what Antigravity adds",
			lines: []string{antigravityUserStep},
			want:  antigravityUserAt,
		},
		{
			name:  "input it did not wrap",
			lines: []string{antigravityLine(0, "USER_INPUT", `"content":"plain"`)},
			want:  "## User · 2026-11-06T09:00:00Z\n\nplain\n\n",
		},
		{
			name:  "thinking, then the answer",
			lines: []string{antigravityLine(1, "PLANNER_RESPONSE", `"thinking":"hmm","content":"yes"`)},
			want:  antigravityAssistantAt + "### Thinking\n\nhmm\n\n### Text\n\nyes\n\n",
		},
		{
			name: "two calls, then their results in order, the second failed",
			lines: []string{
				antigravityLine(1, "PLANNER_RESPONSE", `"thinking":"hmm","tool_calls":[{"name":"run_command","args":{"CommandLine":"ls"}},{"name":"view_file","args":{"AbsolutePath":"/a"}}]`),
				antigravityLine(2, "GENERIC", `"content":"Created At: 2026-11-06T10:00:00+01:00\nCompleted At: 2026-11-06T10:00:01+01:00\nCommand output:\none\r\n"`),
				`{"step_index":3,"type":"GENERIC","status":"ERROR","created_at":"2026-11-06T09:00:00Z","error":"no such file","content":"Created At: 2026-11-06T10:00:00+01:00\nEncountered error in step execution: no such file"}` + "\n",
			},
			want: antigravityAssistantAt + "### Thinking\n\nhmm\n\n" +
				"### Tool: run_command\n\n```\n{\"CommandLine\":\"ls\"}\n```\n\n" +
				"### Tool: view_file\n\n```\n{\"AbsolutePath\":\"/a\"}\n```\n\n" +
				"### Result\n\n```\nCommand output:\none\n```\n\n" +
				"### Result (error)\n\n```\nEncountered error in step execution: no such file\n```\n\n",
		},
		{
			name:  "a failure that says only its error",
			lines: []string{`{"step_index":3,"type":"GENERIC","status":"ERROR","created_at":"2026-11-06T09:00:00Z","error":"no such file"}` + "\n"},
			want:  antigravityAssistantAt + "### Tool: unknown\n\n```\n\n```\n\n### Result (error)\n\n```\nno such file\n```\n\n",
		},
		{
			name:  "a result whose call was trimmed",
			lines: []string{antigravityUserStep, antigravityLine(1, "GENERIC", `"content":"out"`)},
			want:  antigravityUserAt + antigravityAssistantAt + "### Tool: unknown\n\n```\n\n```\n\n### Result\n\n```\nout\n```\n\n",
		},
		{
			name:  "a compaction, its summary without the instructions around it",
			lines: []string{antigravityLine(1, "CHECKPOINT", `"content":"# Resuming from a compaction\n\nYou are continuing.\n\n# User Requests\n1. hi\n\n<summary>\nDid it.\n</summary>\n\n# Conversation Logs\nStart with brain/x."`)},
			want:  "## Compacted · 2026-11-06T09:00:00Z\n\n# User Requests\n1. hi\n\nDid it.\n\n",
		},
		{
			name:  "a system message, a background task reporting back",
			lines: []string{antigravityLine(1, "SYSTEM_MESSAGE", `"content":"The following is a <SYSTEM_MESSAGE> not actually sent by the user.\n\n<SYSTEM_MESSAGE>\ntask done\n</SYSTEM_MESSAGE>"`)},
			want:  "## System · 2026-11-06T09:00:00Z\n\ntask done\n\n",
		},
		{
			name:  "an API error, skipped",
			lines: []string{antigravityUserStep, antigravityLine(1, "ERROR_MESSAGE", `"error":"API error (attempt 1): UNAVAILABLE (code 503)"`)},
			want:  antigravityUserAt,
		},
		{
			name: "the answer after a system message under a heading of its own",
			lines: []string{
				antigravityLine(1, "PLANNER_RESPONSE", `"content":"one"`),
				antigravityLine(2, "SYSTEM_MESSAGE", `"content":"done"`),
				antigravityLine(3, "PLANNER_RESPONSE", `"content":"two"`),
				antigravityLine(4, "PLANNER_RESPONSE", `"content":"three"`),
			},
			want: antigravityAssistantAt + "one\n\n## System · 2026-11-06T09:00:00Z\n\ndone\n\n" + antigravityAssistantAt + "two\n\nthree\n\n",
		},
	} {
		got, _ := antigravityRender(t, c.lines...)
		if got != c.want {
			t.Errorf("%s:\n%s\nwant:\n%s", c.name, got, c.want)
		}
	}
}

func TestAntigravityTranscriptSkipsWhatItCannotRead(t *testing.T) {
	t.Parallel()
	got, log := antigravityRender(t, antigravityUserStep, "not json\n", antigravityLine(1, "NEW_KIND", `"content":"x"`), antigravityLine(2, "PLANNER_RESPONSE", `"content":"ok"`))
	if want := antigravityUserAt + antigravityAssistantAt + "ok\n\n"; got != want {
		t.Fatalf("transcript:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(log, `"msg":"transcript lines not rendered"`) || !strings.Contains(log, `"skipped":2`) {
		t.Fatalf("log does not say two lines were skipped:\n%s", log)
	}
}

// A conversation that comes back after another, as one resumed after a restart,
// is headed again.
func TestAntigravityTranscriptHeadsAConversationThatComesBack(t *testing.T) {
	t.Parallel()
	home, backup := t.TempDir(), t.TempDir()
	writeAntigravityTranscript(t, home, antigravityFirst, antigravityUserStep)
	writeAntigravityTranscript(t, home, antigravityCleared, antigravityUserStep)
	writeHooksFile(t, backup, antigravityStartLine(antigravityFirst), antigravityStartLine(antigravityCleared))
	antigravityBackUp(t, home, backup)
	appendFile(t, antigravityFile(home, antigravityFirst), antigravityLine(1, "PLANNER_RESPONSE", `"content":"back"`))
	antigravityBackUp(t, home, backup)
	heading := func(id string) string { return "# Session " + id + " · antigravity\n\n" }
	want := heading(antigravityFirst) + antigravityUserAt + heading(antigravityCleared) + antigravityUserAt +
		heading(antigravityFirst) + antigravityAssistantAt + "back\n\n"
	if got := readFile(t, filepath.Join(backup, aca.TranscriptFile)); got != want {
		t.Fatalf("transcript:\n%s\nwant:\n%s", got, want)
	}
}
