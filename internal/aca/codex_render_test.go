package aca_test

import (
	"bytes"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/logging"
)

var updateGolden = flag.Bool("update", false, "write the golden files the tests compare with")

// The recording's sessions: the user's first conversation, its subagent, and the one
// /new started.
const (
	codexRecorded    = "01a1ac2e-5326-79f2-a76d-8e0f17f9008a"
	codexRecordedSub = "01a1ac2e-9699-7552-a43a-a80a8f9381fc"
	codexRecordedNew = "01a1ac2e-ffb6-7053-8caa-3326887dee23"
)

var codexRecording = filepath.Join("testdata", "codex", "0.163.0", "transcript")

// codexRecordedHome is a home where Codex keeps the recording's session files as they
// were in its directory from, sessions or first-run.
func codexRecordedHome(t *testing.T, home, from string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(codexRecording, from, "*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no recording in %s: %v", from, err)
	}
	day := filepath.Join(home, ".codex", "sessions", "2026", "11", "05")
	if err := os.MkdirAll(day, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(day, filepath.Base(file)), []byte(readFile(t, file)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// checkGolden compares got with the golden file, or writes it with -update.
func checkGolden(t *testing.T, got, golden string) {
	t.Helper()
	if *updateGolden {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if want := readFile(t, golden); got != want {
		out := filepath.Join(t.TempDir(), filepath.Base(golden))
		if err := os.WriteFile(out, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Fatalf("transcript is not %s (run with -update to accept it): got %s", golden, out)
	}
}

func TestCodexTranscriptOfTheRecording(t *testing.T) {
	t.Parallel()
	home, backup := t.TempDir(), t.TempDir()
	codexRecordedHome(t, home, "sessions")
	writeTurns(t, backup, codexRecorded, codexRecorded, codexRecordedSub, codexRecorded, codexRecorded, codexRecordedNew)
	got, err := aca.Codex{}.BackUp(t.Context(), aca.Launch{UserHome: home, BackupDir: backup})
	if err != nil || got != (aca.Backup{Session: codexRecordedNew, Dir: "/work/repo"}) {
		t.Fatalf("got %+v, %v", got, err)
	}
	checkGolden(t, readFile(t, filepath.Join(backup, aca.TranscriptFile)), filepath.Join(codexRecording, "transcript.md"))
}

// codexItem is the line Codex 0.163.0 writes when it finishes item in turn.
func codexItem(turn, item string) string {
	return `{"timestamp":"2026-11-05T12:00:00.000Z","ordinal":9,"type":"event_msg","payload":{"type":"item_completed","thread_id":"` +
		codexSession + `","turn_id":"` + turn + `","item":` + item + `,"started_at_ms":1793880000000,"completed_at_ms":1793880000000}}` + "\n"
}

// codexRender backs up the sample session with lines after it, and returns the
// transcript after its session heading.
func codexRender(t *testing.T, lines ...string) string {
	t.Helper()
	text, _ := codexRenderLogged(t, lines...)
	return text
}

// codexRenderLogged is codexRender, with what the backup logged.
func codexRenderLogged(t *testing.T, lines ...string) (text, log string) {
	t.Helper()
	home := codexHome(t, map[string]struct{ source, cwd string }{codexSession: {"user", ""}})
	for _, line := range lines {
		appendFile(t, codexFile(home, codexSession), line)
	}
	backup := t.TempDir()
	writeTurns(t, backup, codexSession)
	var logged bytes.Buffer
	ctx := logging.NewContext(t.Context(), logging.New(&logged, slog.LevelInfo))
	if _, err := (aca.Codex{}).BackUp(ctx, aca.Launch{UserHome: home, BackupDir: backup}); err != nil {
		t.Fatalf("backup: %v", err)
	}
	text = readFile(t, filepath.Join(backup, aca.TranscriptFile))
	heading := "# Session " + codexSession + " · codex\n\n"
	if !strings.HasPrefix(text, heading) {
		t.Fatalf("transcript starts with no heading for the session:\n%s", text)
	}
	return strings.TrimPrefix(text, heading), logged.String()
}

const (
	codexUser      = `{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"hi","text_elements":[]}]}`
	codexUserAt    = "## User · 2026-11-05T12:00:00.000Z\n\nhi\n\n"
	codexAssistant = "## Assistant · 2026-11-05T12:00:00.000Z\n\n"
)

func TestCodexTranscriptOfEachItem(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, item, want string
	}{
		{"an image the user gave", `{"type":"UserMessage","id":"u2","content":[{"type":"text","text":"look"},{"type":"local_image","path":"/x/a.png"}]}`,
			"## User · 2026-11-05T12:00:00.000Z\n\nlook\n\n[image]\n\n"},
		{"an answer", `{"type":"AgentMessage","id":"m1","content":[{"type":"Text","text":"hello"}],"phase":"final_answer"}`,
			codexAssistant + "hello\n\n"},
		{"encrypted reasoning", `{"type":"Reasoning","id":"r1","summary_text":[],"raw_content":[]}`, ""},
		{"a reasoning summary, then text", `{"type":"Reasoning","id":"r2","summary_text":["first","second"],"raw_content":[]}` + "\n" +
			`{"type":"AgentMessage","id":"m2","content":[{"type":"Text","text":"so"}],"phase":"final_answer"}`,
			codexAssistant + "### Thinking\n\nfirst\n\nsecond\n\n### Text\n\nso\n\n"},
		{"a failed command", `{"type":"CommandExecution","id":"e1","command":["/bin/sh","-lc","false"],"cwd":"file:///work/repo","status":"failed","aggregated_output":"","exit_code":1}`,
			codexAssistant + "### Tool: exec\n\n```\n{\"command\":[\"/bin/sh\",\"-lc\",\"false\"],\"cwd\":\"/work/repo\"}\n```\n\n### Result (error)\n\n```\n\nexit code 1\n```\n\n"},
		{"an MCP tool", `{"type":"McpToolCall","id":"c1","server":"wdx","tool":"list","arguments":{"all":true},"status":"completed","result":{"content":[{"type":"text","text":"two"},{"type":"image"}]}}`,
			codexAssistant + "### Tool: wdx.list\n\n```\n{\"all\":true}\n```\n\n### Result\n\n```\ntwo\n[image]\n```\n\n"},
		{"an MCP tool that failed", `{"type":"McpToolCall","id":"c2","server":"wdx","tool":"list","arguments":{},"status":"failed","result":{"content":[{"type":"text","text":"missing"}],"isError":true}}`,
			codexAssistant + "### Tool: wdx.list\n\n```\n{}\n```\n\n### Result (error)\n\n```\nmissing\n```\n\n"},
		{"changed files", `{"type":"FileChange","id":"f1","changes":{"/r/b.go":{"type":"update","unified_diff":"@@ -1 +1 @@\n-a\n+b\n","move_path":"/r/c.go"},"/r/a.go":{"type":"delete","content":""}},"status":"completed","stdout":"Success.\n","stderr":""}`,
			codexAssistant + "### Tool: apply_patch\n\n```\ndelete /r/a.go\nupdate /r/b.go -> /r/c.go\n@@ -1 +1 @@\n-a\n+b\n```\n\n### Result\n\n```\nSuccess.\n```\n\n"},
		{"a web search", `{"type":"Extension","kind":"web.search","id":"x1","query":"wdx","action":{"type":"search","query":"wdx"},"results":[{"type":"text_result","title":"wdx","url":"https://example.com"}]}`,
			codexAssistant + "### Tool: web.search\n\n```\n{\"action\":{\"type\":\"search\",\"query\":\"wdx\"},\"query\":\"wdx\"}\n```\n\n### Result\n\n```\nwdx https://example.com\n```\n\n"},
		{"a sleep", `{"type":"Extension","kind":"clock.sleep","id":"x2","durationMs":40000}`,
			codexAssistant + "### Tool: clock.sleep\n\n```\n{\"durationMs\":40000}\n```\n\n"},
		{"a plan", `{"type":"Plan","id":"p1","text":"# Do it\n\n- one"}`, codexAssistant + "### Plan\n\n# Do it\n\n- one\n\n"},
		{"an image viewed", `{"type":"ImageView","id":"i1","path":"file:///r/a.png"}`, codexAssistant + "[image]\n\n"},
		{"a compaction", `{"type":"ContextCompaction","id":"k1"}`,
			"## Compacted · 2026-11-05T12:00:00.000Z\n\n(Codex keeps its summary encrypted)\n\n"},
		{"a subagent", `{"type":"SubAgentActivity","id":"s1","kind":"started","agent_thread_id":"` + codexSubagent + `"}` + "\n" +
			`{"type":"CollabAgentToolCall","id":"s2","tool":"wait","status":"completed"}`, ""},
		{"escape codes", `{"type":"AgentMessage","id":"m3","content":[{"type":"Text","text":"\u001b[31mred\u001b[0m"}],"phase":"final_answer"}`,
			codexAssistant + "red\n\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var lines []string
			for _, item := range strings.Split(c.item, "\n") {
				lines = append(lines, codexItem("t1", item))
			}
			if got := codexRender(t, append([]string{codexItem("t0", codexUser)}, lines...)...); got != codexUserAt+c.want {
				t.Errorf("got\n%s\nwant\n%s", got, codexUserAt+c.want)
			}
		})
	}
}

// Each turn has its own Assistant heading, and a message the user steers into a turn
// starts another for what comes after it.
func TestCodexTranscriptHasAnAssistantHeadingPerTurn(t *testing.T) {
	t.Parallel()
	answer := func(id string) string {
		return `{"type":"AgentMessage","id":"` + id + `","content":[{"type":"Text","text":"` + id + `"}],"phase":"commentary"}`
	}
	got := codexRender(t,
		codexItem("t1", answer("a")), codexItem("t1", answer("b")),
		codexItem("t2", answer("c")),
		codexItem("t2", codexUser), codexItem("t2", answer("d")))
	want := codexAssistant + "a\n\nb\n\n" + codexAssistant + "c\n\n" + codexUserAt + codexAssistant + "d\n\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// What cannot be read is skipped, and what comes after it still rendered; an item
// already rendered is not written again.
func TestCodexTranscriptSkipsWhatItCannotRead(t *testing.T) {
	t.Parallel()
	got, log := codexRenderLogged(t,
		"not json\n",
		codexItem("t1", `{"type":"EnteredReviewMode","id":"v1"}`),
		codexItem("t1", `{"type":"AgentMessage","id":"m1","content":"not a list"}`),
		codexItem("t0", codexUser),
		codexItem("t0", codexUser))
	if got != codexUserAt {
		t.Errorf("got\n%s\nwant only the user's message, once", got)
	}
	if !strings.Contains(log, `"msg":"transcript lines not rendered","session_id":"`+codexSession+`","skipped":3}`) {
		t.Errorf("the three it could not read not logged:\n%s", log)
	}
}

// A session resumed goes on where its last backup stopped, under the same headings,
// with nothing written twice: the transcript is the one a single backup at the end
// writes.
func TestCodexTranscriptGoesOnInAResumedSession(t *testing.T) {
	t.Parallel()
	home, backup := t.TempDir(), t.TempDir()
	codexRecordedHome(t, home, "first-run")
	writeTurns(t, backup, codexRecorded, codexRecorded, codexRecordedSub, codexRecorded)
	codexBackUp(t, home, backup)
	first := readFile(t, filepath.Join(backup, aca.TranscriptFile))

	codexRecordedHome(t, home, "sessions")
	writeTurns(t, backup, codexRecorded, codexRecorded, codexRecordedSub, codexRecorded, codexRecorded, codexRecordedNew)
	codexBackUp(t, home, backup)
	got := readFile(t, filepath.Join(backup, aca.TranscriptFile))
	if !strings.HasPrefix(got, first) || len(got) == len(first) {
		t.Fatalf("the second backup did not add to the first's:\n%s", got)
	}
	checkGolden(t, got, filepath.Join(codexRecording, "transcript.md"))
}

// A session resumed after another was rendered goes on under a heading of its own
// again.
func TestCodexTranscriptHeadsASessionThatComesBack(t *testing.T) {
	t.Parallel()
	home := codexHome(t, map[string]struct{ source, cwd string }{codexEarlier: {"user", ""}, codexSession: {"user", ""}})
	backup := t.TempDir()
	appendFile(t, codexFile(home, codexEarlier), codexItem("t0", codexUser))
	writeTurns(t, backup, codexEarlier)
	codexBackUp(t, home, backup)
	appendFile(t, codexFile(home, codexSession), codexItem("t1", codexUser))
	writeTurns(t, backup, codexEarlier, codexSession)
	codexBackUp(t, home, backup)
	appendFile(t, codexFile(home, codexEarlier), codexItem("t2", codexUser))
	writeTurns(t, backup, codexEarlier, codexSession, codexEarlier)
	codexBackUp(t, home, backup)
	want := ""
	for _, id := range []string{codexEarlier, codexSession, codexEarlier} {
		want += "# Session " + id + " · codex\n\n" + codexUserAt
	}
	if got := readFile(t, filepath.Join(backup, aca.TranscriptFile)); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// A session file that was replaced is rendered again whole after its old copy: its
// text twice rather than any of it missing.
func TestCodexTranscriptRendersAReplacedSessionAgain(t *testing.T) {
	t.Parallel()
	home := codexHome(t, map[string]struct{ source, cwd string }{codexSession: {"user", ""}})
	appendFile(t, codexFile(home, codexSession), codexItem("t0", codexUser))
	backup := t.TempDir()
	writeTurns(t, backup, codexSession)
	codexBackUp(t, home, backup)
	first, _, _ := strings.Cut(readFile(t, codexFile(home, codexSession)), "\n")
	if err := os.WriteFile(codexFile(home, codexSession), []byte(first+"\n"+codexItem("t0", codexUser)), 0o600); err != nil {
		t.Fatal(err)
	}
	codexBackUp(t, home, backup)
	want := "# Session " + codexSession + " · codex\n\n" + codexUserAt + codexUserAt
	if got := readFile(t, filepath.Join(backup, aca.TranscriptFile)); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}
