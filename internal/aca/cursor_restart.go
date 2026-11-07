package aca

import (
	"cmp"
	"context"
	"errors"
	"os"
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
// chat which comes next is a restart's, not a /clear. What the old chat last gave its
// status line goes, or its context size would come back as the new session's.
func (Cursor) BeginSession(_ context.Context, l Launch, session string) error {
	if err := os.Remove(filepath.Join(l.BackupDir, cursorStatusFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
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
// every chat after the agent's first that is not a restart's came from /clear. Every
// chat in the agent's config folder is the user's, as nobody else's are there.
func cursorSinceClear(backupDir string) (map[string]bool, error) {
	made, err := cursorConversations(filepath.Join(backupDir, cursorConfigDir))
	if err != nil {
		return nil, err
	}
	restarts, err := cursorReadRestarts(backupDir)
	if err != nil {
		return nil, err
	}
	first := make([]string, len(made))
	for i, c := range made {
		first[i] = c.ID
	}
	var at []int
	for _, r := range restarts {
		at = append(at, r.Chats)
	}
	everyChat := func(string) bool { return true }
	return sinceTheLatest(first, sessionsTheUserBegan(first, at, everyChat)), nil
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
