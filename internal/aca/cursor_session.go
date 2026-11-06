package aca

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Cursor keeps each chat in chats/<md5 of its directory>/<id>/ of its config folder,
// with a meta.json beside its store. With a config folder per agent, the agent's chats
// are the only ones there, so the one it is in is the one updated last: that follows
// /clear and Cursor's own /resume. The wdx doc CURSOR_RESUME.
const cursorChatsDir = "chats"

// cursorChatMeta is what wdx reads of a chat's meta.json.
type cursorChatMeta struct {
	Cwd             string `json:"cwd"`
	HasConversation bool   `json:"hasConversation"`
	UpdatedAtMs     int64  `json:"updatedAtMs"`
}

// cursorChat is a chat of an agent's config folder.
type cursorChat struct {
	ID   string
	Path string // its folder
	cursorChatMeta
}

// BackUp implements AiCodingAgent: it appends to TranscriptFile what is new in each
// of the agent's chats and snapshots each store that changed (cursor_render.go), and
// says the session is the chat that has a conversation and was updated last, and its
// directory, where it must be resumed. Before the first message there is none, and the
// agent keeps wdx's id, which Command starts as a new chat.
func (Cursor) BackUp(ctx context.Context, l Launch) (Backup, error) {
	chats, err := cursorChats(filepath.Join(l.BackupDir, cursorConfigDir))
	if err != nil {
		return Backup{}, err
	}
	slices.SortStableFunc(chats, func(a, b cursorChat) int { return cmp.Compare(a.UpdatedAtMs, b.UpdatedAtMs) })
	// A chat with no message yet, as /clear leaves it, has nothing to keep.
	chats = slices.DeleteFunc(chats, func(c cursorChat) bool { return !c.HasConversation })
	failed := cursorBackUpChats(ctx, l.BackupDir, chats, time.Now())
	if len(chats) == 0 {
		return Backup{}, failed
	}
	latest := chats[len(chats)-1]
	return Backup{Session: latest.ID, Dir: latest.Cwd}, failed
}

// cursorChats is every chat in config whose meta.json can be read. One that cannot,
// as Cursor writes it, is passed over.
func cursorChats(config string) ([]cursorChat, error) {
	root := filepath.Join(config, cursorChatsDir)
	dirs, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var chats []cursorChat
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		ids, err := os.ReadDir(filepath.Join(root, d.Name()))
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if !id.IsDir() {
				continue
			}
			path := filepath.Join(root, d.Name(), id.Name())
			meta, err := cursorReadChatMeta(path)
			if err != nil {
				continue
			}
			chats = append(chats, cursorChat{ID: id.Name(), Path: path, cursorChatMeta: meta})
		}
	}
	return chats, nil
}

func cursorReadChatMeta(chat string) (cursorChatMeta, error) {
	var meta cursorChatMeta
	data, err := os.ReadFile(filepath.Join(chat, "meta.json"))
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, fmt.Errorf("%s: %w", filepath.Join(chat, "meta.json"), err)
	}
	return meta, nil
}

// cursorResumes says whether the chat id is in config, made in dir. A chat resumed
// from another directory comes up empty, under a second folder with the same id, so
// one made elsewhere is not resumed: the start begins a new chat.
func cursorResumes(config, id, dir string) (bool, error) {
	chats, err := cursorChats(config)
	if err != nil {
		return false, err
	}
	for _, c := range chats {
		if c.ID == id && c.Cwd == dir {
			return true, nil
		}
	}
	return false, nil
}
