package aca

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver, to snapshot Antigravity's conversations

	"github.com/srjn45/wdx/internal/atomicfile"
	"github.com/srjn45/wdx/internal/logging"
)

// antigravityRawDir holds Antigravity's own steps of each of the agent's
// conversations, with its subagents' under <conversation id>/subagents, as Codex's.
// The wdx doc ANTIGRAVITY_PARITY.
const antigravityRawDir = "transcripts"

// antigravitySnapshotsDir holds a copy of each of the agent's conversations' dbs, which
// Command puts back once Antigravity's is gone: given an id whose db it lacks,
// Antigravity starts a new conversation, and from the copy it resumes. The brain's
// files are not needed to resume.
const antigravitySnapshotsDir = "antigravity-conversations"

// antigravityBrain is where Antigravity keeps what it writes of conversation id but
// its db.
func antigravityBrain(home, id string) string {
	return filepath.Join(antigravityHome(home), "brain", id, ".system_generated")
}

// antigravityDB is Antigravity's db of conversation id.
func antigravityDB(home, id string) string {
	return filepath.Join(antigravityHome(home), "conversations", id+".db")
}

// antigravityTranscript is the file Antigravity appends each step of conversation id
// to, whole.
func antigravityTranscript(home, id string) string {
	return filepath.Join(antigravityBrain(home, id), "logs", "transcript_full.jsonl")
}

// antigravityBackUpConversations copies what is new of each of the user's
// conversations and their subagents' to the backup, and snapshots the db of each that
// changed. It says which conversations' transcripts were replaced. What Antigravity
// deleted leaves its copies as they are.
func antigravityBackUpConversations(ctx context.Context, l Launch, conversations []string) (replaced map[string]bool, _ error) {
	replaced = map[string]bool{}
	var failed []error
	for _, id := range conversations {
		var err error
		if replaced[id], err = antigravityCopyConversation(ctx, l, id); err != nil {
			failed = append(failed, fmt.Errorf("conversation %s: %w", id, err))
		}
		if err := antigravitySnapshot(ctx, l, id); err != nil {
			failed = append(failed, fmt.Errorf("snapshot conversation %s: %w", id, err))
		}
	}
	for _, err := range failed {
		logging.FromContext(ctx).Error("agent conversation not backed up", "err", err)
	}
	return replaced, errors.Join(failed...)
}

// antigravityCopyConversation copies the transcript of conversation id, then each
// subagent its brain names, and theirs in turn, all under it. It says whether the
// conversation's own transcript was replaced.
func antigravityCopyConversation(ctx context.Context, l Launch, id string) (replaced bool, err error) {
	raw := filepath.Join(l.BackupDir, antigravityRawDir)
	if err := os.MkdirAll(raw, 0o700); err != nil {
		return false, err
	}
	if replaced, err = copyNewBytes(ctx, antigravityTranscript(l.UserHome, id), filepath.Join(raw, id+".jsonl")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	queue := []string{id}
	seen := map[string]bool{id: true}
	for len(queue) > 0 {
		subagents, err := antigravitySubagents(l.UserHome, queue[0])
		queue = queue[1:]
		if err != nil {
			return replaced, err
		}
		for _, sub := range subagents {
			if seen[sub] {
				continue
			}
			seen[sub] = true
			queue = append(queue, sub)
			to := filepath.Join(raw, id, "subagents", sub+".jsonl")
			if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
				return replaced, err
			}
			if _, err := copyNewBytes(ctx, antigravityTranscript(l.UserHome, sub), to); err != nil && !errors.Is(err, os.ErrNotExist) {
				return replaced, err
			}
		}
	}
	return replaced, nil
}

// antigravitySubagents is the subagents the brain of conversation id names, a file
// each; none once its brain is gone.
func antigravitySubagents(home, id string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(antigravityBrain(home, id), "subagents"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var subagents []string
	for _, e := range entries {
		if sub, ok := strings.CutSuffix(e.Name(), ".json"); ok && sessionUUID.MatchString(sub) {
			subagents = append(subagents, sub)
		}
	}
	return subagents, nil
}

// antigravitySnapshot copies the db of conversation id to the backup when it changed
// since the last copy, which takes the time of the db's last change: a change made
// while it copies is taken at the next backup. VACUUM INTO reads the db in one
// transaction, which Antigravity can go on writing during. A db that is gone leaves
// its copy as it is.
func antigravitySnapshot(ctx context.Context, l Launch, id string) error {
	src := antigravityDB(l.UserHome, id)
	changed, err := antigravityChanged(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	dest := filepath.Join(l.BackupDir, antigravitySnapshotsDir, id+".db")
	if info, err := os.Stat(dest); err == nil && info.ModTime().Equal(changed) {
		return nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	// VACUUM INTO writes only a file that is not there.
	tmp := fmt.Sprintf("%s.%d.tmp", dest, time.Now().UnixNano())
	defer os.Remove(tmp)
	db, err := sql.Open("sqlite", "file:"+src+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "VACUUM INTO ?", tmp)
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	if err := os.Chtimes(tmp, changed, changed); err != nil {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("agent conversation snapshot taken", "session_id", id, "path", dest)
	return nil
}

// antigravityChanged is when the db at path last changed: its own time or its
// write-ahead log's, whichever is later. An empty log holds no change, and opening
// the db to snapshot it makes one.
func antigravityChanged(path string) (time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	changed := info.ModTime()
	if wal, err := os.Stat(path + "-wal"); err == nil && wal.Size() > 0 && wal.ModTime().After(changed) {
		changed = wal.ModTime()
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return time.Time{}, err
	}
	return changed, nil
}

// antigravityRestore puts the backup's copy of l.SessionID's db back where
// Antigravity keeps it, and says whether it did. A write-ahead log left without its
// db is removed first, as SQLite would apply it to the copy.
func antigravityRestore(l Launch) (bool, error) {
	if l.BackupDir == "" || l.UserHome == "" || !sessionUUID.MatchString(l.SessionID) {
		return false, nil
	}
	copied := filepath.Join(l.BackupDir, antigravitySnapshotsDir, l.SessionID+".db")
	if _, err := os.Stat(copied); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	at := antigravityDB(l.UserHome, l.SessionID)
	for _, left := range []string{at + "-wal", at + "-shm"} {
		if err := os.Remove(left); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(at), 0o700); err != nil {
		return false, err
	}
	if err := atomicfile.Copy(at, copied, 0o600); err != nil {
		return false, err
	}
	return true, nil
}
