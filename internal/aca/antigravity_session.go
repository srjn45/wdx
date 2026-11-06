package aca

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
)

// Antigravity names its own conversations, so wdx learns which one an agent is in
// from a SessionStart hook, which Antigravity runs at the first message of each new
// conversation with the event's JSON on stdin: the hooks file of the agent's folder
// has it append each to antigravityHooksFile in the backup. It runs beside the user's
// own hooks. The wdx doc ANTIGRAVITY_PARITY.
const antigravityHooksFile = "antigravity-hooks.jsonl"

// antigravityDir is the folder of an agent's dir that Command adds to Antigravity's
// workspace: the launch note and the hooks.
const antigravityDir = "antigravity"

// antigravityHooks is the hooks file that has Antigravity append each SessionStart's
// JSON as one line to the hooks file in backup. The line is read whole before it is
// written, so a hook stopped while it reads leaves no part of one; "{}" says it
// changes nothing.
func antigravityHooks(backup string) ([]byte, error) {
	log := filepath.Join(backup, antigravityHooksFile)
	command := `line=$(tr -d '\n'); printf '%s\n' "$line" >> ` + shellQuote(log) + `; echo '{}'`
	return json.MarshalIndent(map[string]any{
		"wdx": map[string]any{
			"SessionStart": []map[string]string{{"type": "command", "command": command}},
		},
	}, "", "  ")
}

// antigravityHome is where Antigravity keeps its conversations under home.
func antigravityHome(home string) string {
	return filepath.Join(home, ".gemini", "antigravity-cli")
}

// antigravityKeeps says whether Antigravity keeps the conversation id under home: id
// is wdx's own until the first message, or Antigravity deleted it. Given one it does
// not keep, Antigravity starts a new conversation, so it is never passed.
func antigravityKeeps(home, id string) (bool, error) {
	if home == "" || !sessionUUID.MatchString(id) {
		return false, nil
	}
	_, err := os.Stat(filepath.Join(antigravityHome(home), "conversations", id+".db"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// BackUp implements AiCodingAgent: it copies what is new of the user's conversations
// and their subagents' to the backup, with a snapshot of each one's db that changed,
// appends what is new of the conversations to TranscriptFile as text, and returns the
// latest conversation of the hooks file that none of its others names as a subagent,
// and the workspace folder it started in that is not the agent's own. Before the
// first message there is none, and the agent keeps wdx's id. After a restart only a
// conversation that came after it counts: until one does, the agent is in the
// restart's session, which a start begins.
func (Antigravity) BackUp(ctx context.Context, l Launch) (Backup, error) {
	starts, err := antigravityStarts(filepath.Join(l.BackupDir, antigravityHooksFile))
	if err != nil {
		return Backup{}, err
	}
	var conversations []string
	subagent := map[string]bool{}
	for _, s := range starts {
		id := s.ConversationID
		if _, seen := subagent[id]; seen {
			continue
		}
		subagent[id] = antigravityIsSubagent(l.UserHome, id, starts)
		if !subagent[id] {
			conversations = append(conversations, id)
		}
	}
	restarts, err := antigravityReadRestarts(l.BackupDir)
	if err != nil {
		return Backup{}, err
	}
	replaced, copied := antigravityBackUpConversations(ctx, l, conversations)
	failed := errors.Join(copied, antigravityRenderConversations(ctx, l.BackupDir, conversations, replaced))
	since := 0
	if len(restarts) > 0 {
		since = restarts[len(restarts)-1].Conversations
	}
	first := antigravityFirstCame(starts)
	for _, s := range slices.Backward(starts) {
		if !subagent[s.ConversationID] && slices.Index(first, s.ConversationID) >= since {
			return Backup{Session: s.ConversationID, Dir: antigravityWorkDir(l.BackupDir, s.WorkspacePaths)}, failed
		}
	}
	if len(restarts) > 0 {
		return Backup{Session: restarts[len(restarts)-1].Session}, failed
	}
	return Backup{}, failed
}

// antigravityStart is what a SessionStart hook says of a conversation.
type antigravityStart struct {
	ConversationID string   `json:"conversationId"`
	WorkspacePaths []string `json:"workspacePaths"`
}

// antigravityStarts is the lines of the hooks file, in order. A line a stopped hook
// cut short is left out.
func antigravityStarts(path string) ([]antigravityStart, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var starts []antigravityStart
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		var s antigravityStart
		if json.Unmarshal(line, &s) == nil && sessionUUID.MatchString(s.ConversationID) {
			starts = append(starts, s)
		}
		if errors.Is(err, io.EOF) {
			return starts, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// antigravityIsSubagent says whether another of the conversations names id as its
// subagent, which Antigravity writes in the parent's brain.
func antigravityIsSubagent(home, id string, starts []antigravityStart) bool {
	for _, parent := range starts {
		if parent.ConversationID == id {
			continue
		}
		_, err := os.Stat(filepath.Join(antigravityHome(home), "brain", parent.ConversationID, ".system_generated", "subagents", id+".json"))
		if err == nil {
			return true
		}
	}
	return false
}

// antigravityWorkDir is the first of paths that is not the agent's folder in backup,
// under its name or its old one; "" when there is none.
func antigravityWorkDir(backup string, paths []string) string {
	for _, p := range paths {
		p = filepath.Clean(p)
		if p != filepath.Join(backup, antigravityDir) && p != filepath.Join(backup, antigravityOldDir) {
			return p
		}
	}
	return ""
}
