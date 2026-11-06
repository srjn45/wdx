package aca_test

import (
	"bufio"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

var cursorSamples = filepath.Join("testdata", "cursor", "2026.11.05")

// The session sample's chat, and the compaction sample's, as their store meta says.
const (
	cursorSampleChat     = "c403b1d1-f4f9-4bf6-956a-325afc418643"
	cursorCompactionChat = "da4b97f7-e3ab-4eaa-a43b-510817f1172e"
)

// cursorSampleMessage is a line of a sample's store-messages*.jsonl: a message blob
// and its id, or, on the first line, the store's meta.
type cursorSampleMessage struct {
	ID      string          `json:"id"`
	Message json.RawMessage `json:"message"`
}

// readCursorMessages is the messages of a sample file, in order.
func readCursorMessages(t *testing.T, file string) []cursorSampleMessage {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var messages []cursorSampleMessage
	lines := bufio.NewScanner(f)
	lines.Buffer(nil, 1<<20)
	for lines.Scan() {
		var m cursorSampleMessage
		if err := json.Unmarshal(lines.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		if m.ID != "" {
			messages = append(messages, m)
		}
	}
	if err := lines.Err(); err != nil {
		t.Fatal(err)
	}
	return messages
}

// cursorStore is a chat's store.db as Cursor 2026.11.05 keeps it: in WAL mode, a blob
// for each message, and a root blob whose field 1 lists them, which meta names.
type cursorStore struct {
	t  *testing.T
	db *sql.DB
	n  int
}

// newCursorStore is the store of chat id made in dir, in the backup's config folder,
// beside its meta.json.
func newCursorStore(t *testing.T, backup, dir, id string, updatedAtMs int64) *cursorStore {
	t.Helper()
	sum := md5.Sum([]byte(dir))
	chat := filepath.Join(backup, "cursor-config", "chats", hex.EncodeToString(sum[:]), id)
	if err := os.MkdirAll(chat, 0o700); err != nil {
		t.Fatal(err)
	}
	meta, err := json.Marshal(map[string]any{"schemaVersion": 1, "hasConversation": true, "updatedAtMs": updatedAtMs, "cwd": dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chat, "meta.json"), meta, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(chat, "store.db")+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range []string{
		"CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB)",
		"CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)",
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return &cursorStore{t: t, db: db}
}

// setRoot makes the store's latest root list messages, in order, after the system
// prompt's field that a real root has besides.
func (s *cursorStore) setRoot(messages []cursorSampleMessage) {
	s.t.Helper()
	var root []byte
	for _, m := range messages {
		id, err := hex.DecodeString(m.ID)
		if err != nil || len(id) != 32 {
			s.t.Fatalf("message id %q: %v", m.ID, err)
		}
		root = append(root, 1<<3|2, 32)
		root = append(root, id...)
		if _, err := s.db.Exec("INSERT OR IGNORE INTO blobs VALUES (?, ?)", m.ID, []byte(m.Message)); err != nil {
			s.t.Fatal(err)
		}
	}
	// field 26, a number, as Cursor's roots end with.
	root = append(root, 26<<3|0x80, 26>>4, 0xa8, 0xf6, 0x01)
	s.setRawRoot(root)
}

// setRawRoot makes root the store's latest root.
func (s *cursorStore) setRawRoot(root []byte) {
	s.t.Helper()
	s.n++
	id := strings.Repeat("f", 62) + hex.EncodeToString([]byte{byte(s.n)})
	meta, err := json.Marshal(map[string]any{"agentId": "x", "latestRootBlobId": id})
	if err != nil {
		s.t.Fatal(err)
	}
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO blobs VALUES (?, ?)", []any{id, root}},
		{"INSERT OR REPLACE INTO meta VALUES ('0', ?)", []any{hex.EncodeToString(meta)}},
	} {
		if _, err := s.db.Exec(stmt.sql, stmt.args...); err != nil {
			s.t.Fatal(err)
		}
	}
}

// cursorBackUp backs up the backup and checks it found chat in dir.
func cursorBackUp(t *testing.T, backup, chat, dir string) string {
	t.Helper()
	got, err := aca.Cursor{}.BackUp(t.Context(), aca.Launch{BackupDir: backup, SessionID: "wdx-own"})
	if err != nil {
		t.Fatal(err)
	}
	if want := (aca.Backup{Session: chat, Dir: dir}); got != want {
		t.Fatalf("backup = %+v, want %+v", got, want)
	}
	return readFile(t, filepath.Join(backup, aca.TranscriptFile))
}

func TestCursorTranscriptOfTheSample(t *testing.T) {
	t.Parallel()
	backup := t.TempDir()
	newCursorStore(t, backup, "/work/repo", cursorSampleChat, 100).
		setRoot(readCursorMessages(t, filepath.Join(cursorSamples, "session", "store-messages.jsonl")))
	got := cursorBackUp(t, backup, cursorSampleChat, "/work/repo")
	checkGolden(t, got, filepath.Join(cursorSamples, "session", "transcript.md"))
}

// A backup after a turn adds only its messages: the text is the same as one backup of
// the whole chat gives.
func TestCursorTranscriptGrows(t *testing.T) {
	t.Parallel()
	messages := readCursorMessages(t, filepath.Join(cursorSamples, "session", "store-messages.jsonl"))
	whole := t.TempDir()
	newCursorStore(t, whole, "/work/repo", cursorSampleChat, 100).setRoot(messages)
	want := cursorBackUp(t, whole, cursorSampleChat, "/work/repo")

	backup := t.TempDir()
	store := newCursorStore(t, backup, "/work/repo", cursorSampleChat, 100)
	// Up to the end of a turn, and then up to the middle of one, waiting for its result.
	for _, n := range []int{6, 10, len(messages)} {
		store.setRoot(messages[:n])
		cursorBackUp(t, backup, cursorSampleChat, "/work/repo")
	}
	if got := readFile(t, filepath.Join(backup, aca.TranscriptFile)); got != want {
		t.Fatalf("transcript grown turn by turn:\n%s\nwant:\n%s", got, want)
	}
}

// A compaction drops the messages it summarizes from the context: they stay in the
// text, and the summary follows them under Compacted, without Cursor's pointer to its
// own transcript.
func TestCursorTranscriptOfACompaction(t *testing.T) {
	t.Parallel()
	backup := t.TempDir()
	store := newCursorStore(t, backup, "/work/repo", cursorCompactionChat, 100)
	store.setRoot(readCursorMessages(t, filepath.Join(cursorSamples, "compaction", "store-messages-before.jsonl")))
	cursorBackUp(t, backup, cursorCompactionChat, "/work/repo")
	store.setRoot(readCursorMessages(t, filepath.Join(cursorSamples, "compaction", "store-messages.jsonl")))
	got := cursorBackUp(t, backup, cursorCompactionChat, "/work/repo")
	checkGolden(t, got, filepath.Join(cursorSamples, "compaction", "transcript.md"))
}

// A message the context lists again, as after a rewind that keeps it, is in the text
// once.
func TestCursorTranscriptRendersAMessageOnce(t *testing.T) {
	t.Parallel()
	messages := readCursorMessages(t, filepath.Join(cursorSamples, "session", "store-messages.jsonl"))
	backup := t.TempDir()
	store := newCursorStore(t, backup, "/work/repo", cursorSampleChat, 100)
	store.setRoot(messages[:6])
	once := cursorBackUp(t, backup, cursorSampleChat, "/work/repo")
	store.setRoot(slices.Concat(messages[:4], messages[4:6]))
	if got := cursorBackUp(t, backup, cursorSampleChat, "/work/repo"); got != once {
		t.Fatalf("transcript after the same messages again:\n%s\nwant:\n%s", got, once)
	}
}

// Each chat is under its own heading, in the order they were last updated, and the
// session is the one updated last.
func TestCursorTranscriptOfTwoChats(t *testing.T) {
	t.Parallel()
	messages := readCursorMessages(t, filepath.Join(cursorSamples, "session", "store-messages.jsonl"))
	backup := t.TempDir()
	newCursorStore(t, backup, "/work/repo", cursorChatB, 200).setRoot(messages[6:9])
	newCursorStore(t, backup, "/work/repo", cursorChatA, 100).setRoot(messages[:7])
	got := cursorBackUp(t, backup, cursorChatB, "/work/repo")
	a, b := strings.Index(got, "# Session "+cursorChatA+" · cursor\n"), strings.Index(got, "# Session "+cursorChatB+" · cursor\n")
	if a != 0 || b < a {
		t.Fatalf("chat %s at %d and %s at %d, want the first at 0 and the second after it:\n%s", cursorChatA, a, cursorChatB, b, got)
	}
}

// A root of a format wdx does not know stops the render with an error, and the text
// keeps what it had.
func TestCursorTranscriptOfAStoreItCannotRead(t *testing.T) {
	t.Parallel()
	messages := readCursorMessages(t, filepath.Join(cursorSamples, "session", "store-messages.jsonl"))
	for name, root := range map[string][]byte{
		"a wire type protobuf has not": {1<<3 | 3},
		"a message that is a number":   {1 << 3, 7},
		"a message with no blob":       {1<<3 | 2, 2, 0xab, 0xcd},
		"a field past the end":         {1<<3 | 2, 32, 0xab},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			backup := t.TempDir()
			store := newCursorStore(t, backup, "/work/repo", cursorSampleChat, 100)
			store.setRoot(messages[:6])
			before := cursorBackUp(t, backup, cursorSampleChat, "/work/repo")
			store.setRawRoot(root)
			if _, err := (aca.Cursor{}).BackUp(t.Context(), aca.Launch{BackupDir: backup}); err == nil {
				t.Fatal("backed up a store it cannot read")
			}
			if got := readFile(t, filepath.Join(backup, aca.TranscriptFile)); got != before {
				t.Fatalf("transcript changed to:\n%s", got)
			}
		})
	}
}

// cursorMessages is messages as a store keeps them, each given its id.
func cursorMessages(t *testing.T, messages ...string) []cursorSampleMessage {
	t.Helper()
	out := make([]cursorSampleMessage, 0, len(messages))
	for i, m := range messages {
		if !json.Valid([]byte(m)) {
			t.Fatalf("message %d is not JSON: %s", i, m)
		}
		out = append(out, cursorSampleMessage{ID: strings.Repeat("0", 62) + hex.EncodeToString([]byte{byte(i + 1)}), Message: json.RawMessage(m)})
	}
	return out
}

// What the samples do not show: a tool that failed, an image, times in other offsets
// and one that cannot be read, which leave the time as it was.
func TestCursorTranscriptSmallCases(t *testing.T) {
	t.Parallel()
	request := func(timestamp, query string) string {
		text, _ := json.Marshal("<timestamp>" + timestamp + "</timestamp>\n<user_query>\n" + query + "\n</user_query>")
		return `{"role":"user","content":[{"type":"text","text":` + string(text) + `}]}`
	}
	backup := t.TempDir()
	newCursorStore(t, backup, "/work/repo", cursorChatA, 100).setRoot(cursorMessages(t,
		request("Saturday, Nov 7, 2026, 9:05 PM (UTC-7)", "Read the file."),
		`{"role":"assistant","content":[{"type":"tool-call","toolCallId":"t1","toolName":"Read","args":{"path":"/work/repo/gone"}}]}`,
		`{"role":"tool","content":[{"type":"tool-result","toolCallId":"t1","toolName":"Read","result":"File not found"}],`+
			`"providerOptions":{"cursor":{"highLevelToolCallResult":{"isError":true}}}}`,
		`{"role":"assistant","content":[{"type":"reasoning","text":"It is gone."},{"type":"text","text":"Gone."}]}`,
		`{"role":"user","content":[{"type":"text","text":"<timestamp>Sunday, Nov 8, 2026, 4:30 AM (UTC)</timestamp>\n<user_query>\nThis one.\n</user_query>"},{"type":"image","image":"x"}]}`,
		request("Sunday, Nov 8, 2026, 10:01 AM (UTC+5:30)", "And again."),
		request("Sunday, Nov 8, 2026, 10:02", "A time it cannot read."),
	))
	got := cursorBackUp(t, backup, cursorChatA, "/work/repo")
	want := "# Session " + cursorChatA + " · cursor\n\n" +
		"## User · 2026-11-07T21:05:00-07:00\n\nRead the file.\n\n" +
		"## Assistant · 2026-11-07T21:05:00-07:00\n\n### Tool: Read\n\n```\n{\"path\":\"/work/repo/gone\"}\n```\n\n" +
		"### Result (error)\n\n```\nFile not found\n```\n\n" +
		"### Thinking\n\nIt is gone.\n\n### Text\n\nGone.\n\n" +
		"## User · 2026-11-08T04:30:00Z\n\nThis one.\n\n[image]\n\n" +
		"## User · 2026-11-08T10:01:00+05:30\n\nAnd again.\n\n" +
		"## User · 2026-11-08T10:01:00+05:30\n\nA time it cannot read.\n\n"
	if got != want {
		t.Fatalf("transcript:\n%s\nwant:\n%s", got, want)
	}
}

// A time earlier than the chat's, as a clock moved back, leaves it as it was, as the
// brief needs its times never to go back.
func TestCursorTranscriptTimeNeverGoesBack(t *testing.T) {
	t.Parallel()
	request := func(timestamp, query string) string {
		text, _ := json.Marshal("<timestamp>" + timestamp + "</timestamp>\n<user_query>\n" + query + "\n</user_query>")
		return `{"role":"user","content":[{"type":"text","text":` + string(text) + `}]}`
	}
	backup := t.TempDir()
	newCursorStore(t, backup, "/work/repo", cursorChatA, 100).setRoot(cursorMessages(t,
		request("Friday, Nov 6, 2026, 10:02 AM (UTC+1)", "First."),
		request("Friday, Nov 6, 2026, 9:58 AM (UTC+1)", "Second."),
	))
	got := cursorBackUp(t, backup, cursorChatA, "/work/repo")
	if want := "## User · 2026-11-06T10:02:00+01:00\n\nSecond."; !strings.Contains(got, want) {
		t.Fatalf("transcript:\n%s\nwant it to hold %q", got, want)
	}
}

// cursorSnapshotRoot is the root the snapshot of chat id in the backup names.
func cursorSnapshotRoot(t *testing.T, backup, id string) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(backup, "transcripts", id+".db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow("SELECT value FROM meta WHERE key = '0'").Scan(&value); err != nil {
		t.Fatal(err)
	}
	data, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		Root string `json:"latestRootBlobId"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	return meta.Root
}

// A backup snapshots the store when it changed, and only then.
func TestCursorBackUpSnapshotsAChangedStore(t *testing.T) {
	t.Parallel()
	messages := readCursorMessages(t, filepath.Join(cursorSamples, "session", "store-messages.jsonl"))
	backup := t.TempDir()
	store := newCursorStore(t, backup, "/work/repo", cursorSampleChat, 100)
	store.setRoot(messages[:6])
	cursorBackUp(t, backup, cursorSampleChat, "/work/repo")
	first := cursorSnapshotRoot(t, backup, cursorSampleChat)

	snapshot := filepath.Join(backup, "transcripts", cursorSampleChat+".db")
	if err := os.WriteFile(snapshot, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	cursorBackUp(t, backup, cursorSampleChat, "/work/repo")
	if got := readFile(t, snapshot); got != "kept" {
		t.Fatalf("an unchanged store was snapshot again")
	}
	store.setRoot(messages)
	cursorBackUp(t, backup, cursorSampleChat, "/work/repo")
	if got := cursorSnapshotRoot(t, backup, cursorSampleChat); got == first {
		t.Fatalf("snapshot names root %s, the one before the change", got)
	}
}

// Command puts the snapshot back when Cursor's store is gone, with its chat folder or
// alone, and resumes it; never over a store Cursor has, nor for a chat made in another
// directory, which a resume would bring up empty.
func TestCursorCommandPutsBackTheSnapshot(t *testing.T) {
	t.Parallel()
	messages := readCursorMessages(t, filepath.Join(cursorSamples, "session", "store-messages.jsonl"))
	sum := md5.Sum([]byte("/work/repo"))
	for _, c := range []struct {
		name    string
		lose    func(t *testing.T, chat string)
		dir     string
		restore bool
	}{
		{"its chat folder gone", func(t *testing.T, chat string) { removeAll(t, chat) }, "/work/repo", true},
		{"its store gone, a write-ahead log left", func(t *testing.T, chat string) {
			removeAll(t, filepath.Join(chat, "store.db"))
			if err := os.WriteFile(filepath.Join(chat, "store.db-wal"), []byte("another store's"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "/work/repo", true},
		{"its store there", func(*testing.T, string) {}, "/work/repo", false},
		{"from another directory", func(t *testing.T, chat string) { removeAll(t, chat) }, "/work/elsewhere", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			backup := t.TempDir()
			store := newCursorStore(t, backup, "/work/repo", cursorSampleChat, 100)
			store.setRoot(messages)
			cursorBackUp(t, backup, cursorSampleChat, "/work/repo")
			store.db.Close()
			chat := filepath.Join(backup, "cursor-config", "chats", hex.EncodeToString(sum[:]), cursorSampleChat)
			before := readFile(t, filepath.Join(chat, "store.db"))
			c.lose(t, chat)
			_, statErr := os.Stat(filepath.Join(chat, "store.db"))
			had := statErr == nil
			got, err := aca.Cursor{}.Command(t.Context(), aca.Launch{BackupDir: backup, SessionID: cursorSampleChat, Dir: c.dir})
			if err != nil {
				t.Fatal(err)
			}
			resumed := slices.Contains(got, "--resume")
			if c.dir == "/work/repo" && !resumed {
				t.Fatalf("command = %q, want it to resume", got)
			}
			if c.dir != "/work/repo" && resumed {
				t.Fatalf("command = %q, want a new chat", got)
			}
			kept := filepath.Join(chat, "store.db")
			_, err = os.Stat(kept)
			switch {
			case c.restore && readFile(t, kept) == readFile(t, filepath.Join(backup, "transcripts", cursorSampleChat+".db")):
				if _, err := os.Stat(kept + "-wal"); err == nil {
					t.Fatal("another store's write-ahead log left beside the snapshot")
				}
			case c.restore:
				t.Fatal("the store put back is not the snapshot")
			case had && readFile(t, kept) != before:
				t.Fatal("Cursor's store was replaced")
			case !had && err == nil:
				t.Fatal("snapshot put back for a chat made elsewhere")
			}
		})
	}
}

func removeAll(t *testing.T, path string) {
	t.Helper()
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
}
