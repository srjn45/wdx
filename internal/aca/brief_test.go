package aca_test

import (
	"testing"
)

// A compaction starts the brief only in a session of an ACA whose compactions keep a
// summary: Codex keeps its summary encrypted, so there it is one more message, and the
// brief keeps the request before it.
func TestBriefStartsAtACompactionOnlyWhereItHasASummary(t *testing.T) {
	t.Parallel()
	conversation := message("User", 1, "the first request") +
		message("Compacted", 2, "the summary") +
		message("User", 3, "and on")
	t.Run("claude_code", func(t *testing.T) {
		t.Parallel()
		_, text := newClaudeBackup(t).brief(t, 64<<10, "# Session s-1 · claude_code\n\n"+conversation)
		wantInOrder(t, text, "## Compacted", "the summary", "and on")
		wantNot(t, text, "the first request")
	})
	t.Run("codex", func(t *testing.T) {
		t.Parallel()
		_, text := newClaudeBackup(t).brief(t, 64<<10, "# Session s-1 · codex\n\n"+conversation)
		wantInOrder(t, text, "the first request", "## Compacted", "the summary", "and on")
	})
}

// Codex's tool calls are told in a few words as Claude's are: exec by its command line,
// the last of the shell's arguments, apply_patch, which is not JSON, by its first line,
// and an MCP tool by the same keys as Claude's tools.
func TestBriefTellsCodexToolsInAFewWords(t *testing.T) {
	t.Parallel()
	transcript := "# Session s-1 · codex\n\n" +
		message("User", 1, "do it") +
		"## Assistant · 2026-11-01T13:02:00.000Z\n\n" +
		toolCall("exec", `{"command":["/usr/bin/zsh","-lc","echo hello"],"cwd":"/work/repo"}`, "hello", false) +
		toolCall("apply_patch", "update /r/b.go -> /r/c.go\n@@ -1 +1 @@\n-a\n+b", "Success.", false) +
		toolCall("github.search_issues", `{"repo":"srjn45/wdx","query":"brief bug","limit":3}`, "none", false) +
		toolCall("notes.read", `{"name":"plan"}`, "the plan", false)
	_, text := newClaudeBackup(t).brief(t, 64<<10, transcript)
	wantInOrder(t, text, "- exec echo hello (line ", "- apply_patch update /r/b.go -> /r/c.go (line ",
		"- github.search_issues brief bug (line ", "- notes.read plan (line ")
	wantNot(t, text, "/usr/bin/zsh", "@@", "srjn45/wdx")
}

// Antigravity's tools name their input with a capital: a command by its command line,
// and a file by its path, whatever comes first.
func TestBriefTellsAntigravityToolsInAFewWords(t *testing.T) {
	t.Parallel()
	transcript := "# Session s-1 · antigravity\n\n" +
		message("User", 1, "do it") +
		"## Assistant · 2026-11-01T13:02:00.000Z\n\n" +
		toolCall("run_command", `{"Cwd":"/work","CommandLine":"echo hello"}`, "hello", false) +
		toolCall("view_file", `{"StartLine":1,"AbsolutePath":"/work/a.go"}`, "a", false) +
		toolCall("write_file", `{"Content":"alpha","Path":"/work/b.txt"}`, "Created file.", false) +
		toolCall("write_to_file", `{"CodeContent":"beta","TargetFile":"/work/c.txt"}`, "Created file.", false)
	_, text := newClaudeBackup(t).brief(t, 64<<10, transcript)
	wantInOrder(t, text, "- run_command echo hello (line ", "- view_file /work/a.go (line ",
		"- write_file /work/b.txt (line ", "- write_to_file /work/c.txt (line ")
	wantNot(t, text, "/work\n", "alpha", "beta")
}
