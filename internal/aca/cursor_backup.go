package aca

import (
	"context"
	"crypto/md5" //nolint:gosec // Cursor names its chat directories by an md5, not to keep a secret
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"modernc.org/sqlite"

	"github.com/srjn45/wdx/internal/atomicfile"
	"github.com/srjn45/wdx/internal/logging"
)

// A chat's store.db is SQLite in WAL mode, with the tables blobs (id, data), each
// blob named by the sha256 of its bytes, and meta, whose row '0' is the hex of a JSON
// object naming the latest root blob. The root is protobuf, whose field 1 lists the
// blobs of the context's messages in order, each JSON in the AI SDK's shape. Checked
// on cursor-agent 2026.11.05: the wdx doc CURSOR_PARITY.
const (
	cursorStoreFile = "store.db"
	// cursorRootMessages is the root's field that lists the messages.
	cursorRootMessages = 1
)

// cursorSnapshotsDir holds a snapshot of each chat's store, <id>.db, taken with
// SQLite's backup at each backup where the store changed, so that a chat Cursor
// prunes, migrates or breaks is still kept.
const cursorSnapshotsDir = "transcripts"

// errCursorStore is a store wdx cannot read: Cursor changed its format.
var errCursorStore = errors.New("cursor's chat store is not as wdx knows it")

// cursorStore is a chat's store.db, open to read.
type cursorStore struct {
	db *sql.DB
}

// openCursorStore opens the store at path to read, nil when there is none.
func openCursorStore(path string) (*cursorStore, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	// Read-only, it still takes part in the WAL Cursor writes, which immutable would not.
	db, err := sql.Open("sqlite", "file:"+(&url.URL{Path: path}).EscapedPath()+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return &cursorStore{db: db}, nil
}

func (s *cursorStore) Close() error { return s.db.Close() }

// cursorStoreMessage is one message of a chat's context, with the id of its blob.
type cursorStoreMessage struct {
	ID   string
	Data []byte
}

// root is the id of the store's latest root blob.
func (s *cursorStore) root(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (string, error) {
	var value string
	if err := q.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = '0'").Scan(&value); err != nil {
		return "", fmt.Errorf("%w: its meta: %w", errCursorStore, err)
	}
	data, err := hex.DecodeString(value)
	if err != nil {
		return "", fmt.Errorf("%w: its meta is not hex: %w", errCursorStore, err)
	}
	var meta struct {
		LatestRootBlobID string `json:"latestRootBlobId"`
	}
	if err := json.Unmarshal(data, &meta); err != nil || meta.LatestRootBlobID == "" {
		return "", fmt.Errorf("%w: its meta names no root", errCursorStore)
	}
	return meta.LatestRootBlobID, nil
}

// messages is the root it reads, the messages it lists, in order, but for the
// first listed[id] appearances of each id, which are not read, and how many times
// it lists each id. They are read in one transaction, so they are the root's even
// while Cursor writes.
func (s *cursorStore) messages(ctx context.Context, listed map[string]int) (string, []cursorStoreMessage, map[string]int, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", nil, nil, err
	}
	defer tx.Rollback()
	root, err := s.root(ctx, tx)
	if err != nil {
		return "", nil, nil, err
	}
	blob := func(id string) ([]byte, error) {
		var data []byte
		if err := tx.QueryRowContext(ctx, "SELECT data FROM blobs WHERE id = ?", id).Scan(&data); err != nil {
			return nil, fmt.Errorf("%w: blob %s: %w", errCursorStore, id, err)
		}
		return data, nil
	}
	data, err := blob(root)
	if err != nil {
		return "", nil, nil, err
	}
	ids, err := cursorRootMessageIDs(data)
	if err != nil {
		return "", nil, nil, err
	}
	var messages []cursorStoreMessage
	counts := map[string]int{}
	for _, id := range ids {
		counts[id]++
		if counts[id] <= listed[id] {
			continue
		}
		data, err := blob(id)
		if err != nil {
			return "", nil, nil, err
		}
		messages = append(messages, cursorStoreMessage{ID: id, Data: data})
	}
	return root, messages, counts, nil
}

// snapshot writes a consistent copy of the store to dest, through a file beside it,
// so that dest is the old copy or the new one, never part of one.
func (s *cursorStore) snapshot(ctx context.Context, dest string) error {
	tmp := dest + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	err = conn.Raw(func(driverConn any) error {
		backuper, ok := driverConn.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return errors.New("the sqlite driver has no backup")
		}
		b, err := backuper.NewBackup(tmp)
		if err != nil {
			return err
		}
		_, err = b.Step(-1)
		return errors.Join(err, b.Finish())
	})
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// cursorRootMessageIDs is the ids of the messages a root blob lists, in order: each
// a sha256's bytes in field 1. A field of a wire type protobuf does not have, or one
// that runs past the end, is a format wdx does not know.
func cursorRootMessageIDs(root []byte) ([]string, error) {
	var ids []string
	for i := 0; i < len(root); {
		key, n := cursorVarint(root[i:])
		if n == 0 {
			return nil, fmt.Errorf("%w: its root has a broken field key", errCursorStore)
		}
		i += n
		field, wire := key>>3, key&7
		var size uint64
		switch wire {
		case 0:
			if _, n = cursorVarint(root[i:]); n == 0 {
				return nil, fmt.Errorf("%w: its root has a broken number", errCursorStore)
			}
			i += n
			if field == cursorRootMessages {
				return nil, fmt.Errorf("%w: its root lists a message that is not a blob's id", errCursorStore)
			}
			continue
		case 1:
			size = 8
		case 5:
			size = 4
		case 2:
			if size, n = cursorVarint(root[i:]); n == 0 {
				return nil, fmt.Errorf("%w: its root has a broken length", errCursorStore)
			}
			i += n
		default:
			return nil, fmt.Errorf("%w: its root has wire type %d", errCursorStore, wire)
		}
		if size > uint64(len(root)-i) { //nolint:gosec // i is at most len(root)
			return nil, fmt.Errorf("%w: its root ends inside field %d", errCursorStore, field)
		}
		value := root[i : i+int(size)]
		i += int(size)
		if field == cursorRootMessages {
			if wire != 2 {
				return nil, fmt.Errorf("%w: its root lists a message that is not a blob's id", errCursorStore)
			}
			ids = append(ids, hex.EncodeToString(value))
		}
	}
	return ids, nil
}

// cursorVarint is the protobuf varint b starts with and its length, 0 when b holds
// none.
func cursorVarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7f) << (7 * i)
		if b[i] < 0x80 {
			return v, i + 1
		}
	}
	return 0, 0
}

// cursorChatDir is where Cursor keeps the chat id made in dir, in config.
func cursorChatDir(config, dir, id string) string {
	sum := md5.Sum([]byte(dir)) //nolint:gosec // Cursor names its chat directories by an md5, not to keep a secret
	return filepath.Join(config, cursorChatsDir, hex.EncodeToString(sum[:]), id)
}

// cursorRestore puts the backup's snapshot of the chat l.SessionID back where Cursor
// keeps it when Cursor's store is gone, and says whether it did. Only a chat made in
// l.Dir is put back, as one resumed elsewhere comes up empty. Checked by hand on
// 2026.11.05: a snapshot put back as store.db, with its meta.json or without, is
// resumed with its whole history, and Cursor writes meta.json anew.
func cursorRestore(ctx context.Context, l Launch) (bool, error) {
	if l.BackupDir == "" || l.SessionID == "" {
		return false, nil
	}
	snapshot := filepath.Join(l.BackupDir, cursorSnapshotsDir, l.SessionID+".db")
	if _, err := os.Stat(snapshot); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	state, err := readCursorRendered(l.BackupDir)
	if err != nil {
		return false, err
	}
	if chat := state.Chats[l.SessionID]; chat == nil || chat.Dir != l.Dir {
		return false, nil
	}
	chat := cursorChatDir(filepath.Join(l.BackupDir, cursorConfigDir), l.Dir, l.SessionID)
	store := filepath.Join(chat, cursorStoreFile)
	if _, err := os.Stat(store); err == nil || !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	// A write-ahead log left without its store is another store's, which SQLite
	// would apply to the snapshot.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(store + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	if err := os.MkdirAll(chat, 0o700); err != nil {
		return false, err
	}
	if err := atomicfile.Copy(store, snapshot, 0o600); err != nil {
		return false, err
	}
	logging.FromContext(ctx).Info("agent chat put back from its backup", "session_id", l.SessionID, "path", store, "backup", snapshot)
	return true, nil
}
