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
