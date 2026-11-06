package aca

import (
	"cmp"
	"context"
	"path/filepath"
	"slices"
)

// cursorRestartsFile records each restart wdx began, since Cursor's chats do not tell
// one a restart began from one /clear began: a line per restart, with the session wdx
// gave it and how many chats with a conversation the agent had by then. The first of
// them, in the order they were made, at or after that position is the restart's. The
// wdx doc CURSOR_PARITY, as Codex's codex-restarts.jsonl.
const cursorRestartsFile = "cursor-restarts.jsonl"

type cursorRestart struct {
	Session string `json:"session"`
	Chats   int    `json:"chats"`
}

// BeginSession implements AiCodingAgent. Its line is what makes session the one BackUp
// says the agent is in until the new chat's first message, so that the next start
// begins a chat rather than resume the one before, and what tells the brief that the
// chat which comes next is a restart's, not a /clear.
func (Cursor) BeginSession(_ context.Context, l Launch, session string) error {
	chats, err := cursorConversations(filepath.Join(l.BackupDir, cursorConfigDir))
	if err != nil {
		return err
	}
	return appendJSONLine(l.BackupDir, cursorRestartsFile, cursorRestart{Session: session, Chats: len(chats)})
}

// cursorReadRestarts is the restarts file's lines, oldest first.
func cursorReadRestarts(backupDir string) ([]cursorRestart, error) {
	return readJSONLines(filepath.Join(backupDir, cursorRestartsFile), func(r cursorRestart) bool { return r.Session != "" })
}

// cursorConversations is the agent's chats that have a conversation, in the order they
// were made. A chat with no message yet, as a start or /clear leaves it, is not one.
func cursorConversations(config string) ([]cursorChat, error) {
	chats, err := cursorChats(config)
	if err != nil {
		return nil, err
	}
	chats = slices.DeleteFunc(chats, func(c cursorChat) bool { return !c.HasConversation })
	cursorSortByMade(chats)
	return chats, nil
}

// cursorSortByMade sorts chats in the order they were made.
func cursorSortByMade(chats []cursorChat) {
	slices.SortStableFunc(chats, func(a, b cursorChat) int {
		return cmp.Or(cmp.Compare(a.CreatedAtMs, b.CreatedAtMs), cmp.Compare(a.UpdatedAtMs, b.UpdatedAtMs), cmp.Compare(a.ID, b.ID))
	})
}

// cursorAfterRestart is the chats of made, in the order they were made, that came
// after the latest restart, and that restart; all of them when there was none.
func cursorAfterRestart(made []cursorChat, restarts []cursorRestart) ([]cursorChat, *cursorRestart) {
	if len(restarts) == 0 {
		return made, nil
	}
	latest := restarts[len(restarts)-1]
	return made[min(latest.Chats, len(made)):], &latest
}

// cursorSinceClear is the chats from the latest /clear on, nil when there was none:
// every chat after the agent's first that is not a restart's came from /clear.
func cursorSinceClear(backupDir string) (map[string]bool, error) {
	made, err := cursorConversations(filepath.Join(backupDir, cursorConfigDir))
	if err != nil {
		return nil, err
	}
	restarts, err := cursorReadRestarts(backupDir)
	if err != nil {
		return nil, err
	}
	restarted := map[string]bool{}
	for _, r := range restarts {
		if r.Chats < len(made) {
			restarted[made[r.Chats].ID] = true
		}
	}
	latest := -1
	for i := 1; i < len(made); i++ {
		if !restarted[made[i].ID] {
			latest = i
		}
	}
	if latest < 0 {
		return nil, nil
	}
	since := map[string]bool{}
	for _, c := range made[latest:] {
		since[c.ID] = true
	}
	return since, nil
}

// Brief implements AiCodingAgent, reading no further back than the latest /clear, and
// from the latest compaction summary since, or else the first request: Cursor's
// summary is plain text. A restart's chat does not stop it.
func (Cursor) Brief(_ context.Context, l Launch, limit int) (Brief, error) {
	since, err := cursorSinceClear(l.BackupDir)
	if err != nil {
		return Brief{}, err
	}
	return readTranscriptBrief(l.BackupDir, since, limit)
}
