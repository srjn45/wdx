package aca_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/aca"
)

var antigravityRecording = filepath.Join("testdata", "antigravity", "1.5.2", "transcript")

// antigravityFile is where Antigravity keeps the transcript of conversation id under
// home.
func antigravityFile(home, id string) string {
	return filepath.Join(home, ".gemini", "antigravity-cli", "brain", id, ".system_generated", "logs", "transcript_full.jsonl")
}

// antigravityRecordedHome is a home where Antigravity keeps the recording's
// transcripts, which the recorder kept without their .system_generated.
func antigravityRecordedHome(t *testing.T, home string) {
	t.Helper()
	for _, id := range []string{antigravityFirst, antigravityCleared} {
		writeAntigravityTranscript(t, home, id, readFile(t, filepath.Join(antigravityRecording, "brain", id, "logs", "transcript_full.jsonl")))
	}
}

func writeAntigravityTranscript(t *testing.T, home, id, text string) {
	t.Helper()
	file := antigravityFile(home, id)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// antigravityStartLine is a SessionStart line of the hooks file for conversation id.
func antigravityStartLine(id string) string {
	return `{"conversationId":"` + id + `","workspacePaths":["/work"]}`
}

// writeAntigravitySubagent has the brain of parent name sub as its subagent, with a
// transcript of text.
func writeAntigravitySubagent(t *testing.T, home, parent, sub, text string) {
	t.Helper()
	dir := filepath.Join(home, ".gemini", "antigravity-cli", "brain", parent, ".system_generated", "subagents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sub+".json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeAntigravityTranscript(t, home, sub, text)
}

func antigravityBackUp(t *testing.T, home, backup string) aca.Backup {
	t.Helper()
	got, err := aca.Antigravity{}.BackUp(t.Context(), aca.Launch{UserHome: home, BackupDir: backup})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	return got
}

func TestAntigravityTranscriptOfTheRecording(t *testing.T) {
	t.Parallel()
	home, backup := t.TempDir(), t.TempDir()
	antigravityRecordedHome(t, home)
	lines, project := antigravityRecordedStarts(t, backup)
	writeHooksFile(t, backup, lines...)
	if got := antigravityBackUp(t, home, backup); got != (aca.Backup{Session: antigravityCleared, Dir: project}) {
		t.Fatalf("got %+v", got)
	}
	checkGolden(t, readFile(t, filepath.Join(backup, aca.TranscriptFile)), filepath.Join(antigravityRecording, "transcript.md"))
}

const antigravityUserStep = `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-11-06T09:00:00Z","content":"<USER_REQUEST>\nhi\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-11-06T10:00:00+01:00.\n</ADDITIONAL_METADATA>"}` + "\n"

func TestAntigravityBackUpCopiesOnlyNewBytes(t *testing.T) {
	t.Parallel()
	home, backup := t.TempDir(), t.TempDir()
	// The second line is still being written.
	cut := `{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-06T09:00:01Z","con`
	writeAntigravityTranscript(t, home, antigravityFirst, antigravityUserStep+cut)
	writeHooksFile(t, backup, antigravityStartLine(antigravityFirst))
	antigravityBackUp(t, home, backup)
	copied := filepath.Join(backup, "transcripts", antigravityFirst+".jsonl")
	if got := readFile(t, copied); got != antigravityUserStep+cut {
		t.Fatalf("copy %q, want the transcript", got)
	}
	text := filepath.Join(backup, aca.TranscriptFile)
	if got := readFile(t, text); strings.Contains(got, "Assistant") {
		t.Fatalf("a line still being written was rendered:\n%s", got)
	}

	appendFile(t, antigravityFile(home, antigravityFirst), `tent":"hello"}`+"\n")
	antigravityBackUp(t, home, backup)
	whole := antigravityUserStep + cut + `tent":"hello"}` + "\n"
	if got := readFile(t, copied); got != whole {
		t.Fatalf("copy after the line ended:\n%s\nwant the transcript", got)
	}
	if aside, _ := filepath.Glob(copied + ".replaced-*"); len(aside) != 0 {
		t.Fatalf("a transcript that grew was taken as replaced: %q", aside)
	}
	if got := readFile(t, text); !strings.HasSuffix(got, "## Assistant · 2026-11-06T09:00:01Z\n\nhello\n\n") || strings.Count(got, "hi\n") != 1 {
		t.Fatalf("transcript after the line ended:\n%s", got)
	}

	// What Antigravity deletes stays as copied.
	if err := os.RemoveAll(filepath.Join(home, ".gemini")); err != nil {
		t.Fatal(err)
	}
	antigravityBackUp(t, home, backup)
	if got := readFile(t, copied); got != whole {
		t.Fatal("copy changed once the conversation was gone")
	}
}

// Once its brain is deleted and the conversation resumed, Antigravity writes its
// transcript again from the next step: the old copy is kept, and the text goes on
// without repeating a step.
func TestAntigravityBackUpGoesOnInAReplacedTranscript(t *testing.T) {
	t.Parallel()
	home, backup := t.TempDir(), t.TempDir()
	first := antigravityUserStep + antigravityLine(1, "PLANNER_RESPONSE", `"content":"one"`)
	writeAntigravityTranscript(t, home, antigravityFirst, first)
	writeHooksFile(t, backup, antigravityStartLine(antigravityFirst))
	antigravityBackUp(t, home, backup)
	// Its step 1 again, as a check that what is in the text is not written twice.
	again := antigravityLine(1, "PLANNER_RESPONSE", `"content":"one"`) + antigravityLine(2, "USER_INPUT", `"content":"<USER_REQUEST>\nagain\n</USER_REQUEST>"`)
	writeAntigravityTranscript(t, home, antigravityFirst, again)
	antigravityBackUp(t, home, backup)
	copied := filepath.Join(backup, "transcripts", antigravityFirst+".jsonl")
	aside, err := filepath.Glob(copied + ".replaced-*")
	if err != nil || len(aside) != 1 || readFile(t, aside[0]) != first {
		t.Fatalf("the first transcript was not set aside whole: %v %v", aside, err)
	}
	if readFile(t, copied) != again {
		t.Fatal("the new transcript was not copied")
	}
	want := "# Session " + antigravityFirst + " · antigravity\n\n" +
		"## User · 2026-11-06T09:00:00Z\n\nhi\n\n" +
		"## Assistant · 2026-11-06T09:00:00Z\n\none\n\n" +
		"## User · 2026-11-06T09:00:00Z\n\nagain\n\n"
	if got := readFile(t, filepath.Join(backup, aca.TranscriptFile)); got != want {
		t.Fatalf("transcript:\n%s\nwant:\n%s", got, want)
	}
}

func TestAntigravityBackUpCopiesSubagentsUnderTheirConversation(t *testing.T) {
	t.Parallel()
	home, backup := t.TempDir(), t.TempDir()
	const nested = "6a1c0d9e-2b7f-4c1e-9d3a-5f8b2e4c7a11"
	writeAntigravityTranscript(t, home, antigravityFirst, antigravityUserStep)
	writeAntigravitySubagent(t, home, antigravityFirst, antigravitySub, "sub\n")
	writeAntigravitySubagent(t, home, antigravitySub, nested, "nested\n")
	// A subagent fires the hook too.
	writeHooksFile(t, backup, antigravityStartLine(antigravityFirst), antigravityStartLine(antigravitySub), antigravityStartLine(nested))
	if got := antigravityBackUp(t, home, backup); got.Session != antigravityFirst {
		t.Fatalf("session %s, want the user's %s", got.Session, antigravityFirst)
	}
	subagents := filepath.Join(backup, "transcripts", antigravityFirst, "subagents")
	if got := readFile(t, filepath.Join(subagents, antigravitySub+".jsonl")); got != "sub\n" {
		t.Errorf("subagent's copy %q", got)
	}
	if got := readFile(t, filepath.Join(subagents, nested+".jsonl")); got != "nested\n" {
		t.Errorf("its subagent's copy %q", got)
	}
	entries, err := os.ReadDir(filepath.Join(backup, "transcripts"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := []string{antigravityFirst, antigravityFirst + ".jsonl"}; !slices.Equal(names, want) {
		t.Errorf("transcripts holds %q, want only the user's conversation", names)
	}
	if got := readFile(t, filepath.Join(backup, aca.TranscriptFile)); strings.Contains(got, antigravitySub) {
		t.Errorf("a subagent was rendered:\n%s", got)
	}
}

// writeAntigravityDB makes Antigravity's db of conversation id under home, in WAL
// mode as its are, holding row, and returns its path.
func writeAntigravityDB(t *testing.T, home, id, row string) string {
	t.Helper()
	path := filepath.Join(home, ".gemini", "antigravity-cli", "conversations", id+".db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{"CREATE TABLE IF NOT EXISTS steps (data TEXT)", "INSERT INTO steps VALUES ('" + row + "')"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// readAntigravityDB is the rows of the db at path.
func readAntigravityDB(t *testing.T, path string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT data FROM steps")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

// A snapshot is taken of a db that changed, and only then.
func TestAntigravityBackUpSnapshotsAChangedConversation(t *testing.T) {
	t.Parallel()
	home, backup := t.TempDir(), t.TempDir()
	writeHooksFile(t, backup, antigravityStartLine(antigravityFirst))
	writeAntigravityDB(t, home, antigravityFirst, "one")
	antigravityBackUp(t, home, backup)
	snapshot := filepath.Join(backup, "antigravity-conversations", antigravityFirst+".db")
	if got := readAntigravityDB(t, snapshot); !slices.Equal(got, []string{"one"}) {
		t.Fatalf("snapshot holds %q", got)
	}

	// Marked, so that a snapshot taken again shows, at the time it had.
	taken, err := os.Stat(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	marked := readFile(t, snapshot) + "marked"
	if err := os.WriteFile(snapshot, []byte(marked), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(snapshot, taken.ModTime(), taken.ModTime()); err != nil {
		t.Fatal(err)
	}
	antigravityBackUp(t, home, backup)
	if readFile(t, snapshot) != marked {
		t.Fatal("a snapshot was taken again of a db that did not change")
	}

	db := writeAntigravityDB(t, home, antigravityFirst, "two")
	// Its time, which a file system may keep too coarsely to tell from the first.
	later := taken.ModTime().Add(time.Second)
	for _, f := range []string{db, db + "-wal"} {
		if err := os.Chtimes(f, later, later); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	antigravityBackUp(t, home, backup)
	if got := readAntigravityDB(t, snapshot); !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("snapshot after a change holds %q", got)
	}
	if left, _ := filepath.Glob(snapshot + ".*"); len(left) != 0 {
		t.Fatalf("left beside the snapshot: %q", left)
	}
}

// Once Antigravity's db of the conversation is gone, Command puts the snapshot back,
// without the write-ahead log left of the old one, and resumes it.
func TestAntigravityResumesAGoneConversationFromItsSnapshot(t *testing.T) {
	t.Parallel()
	home, backup := t.TempDir(), t.TempDir()
	writeHooksFile(t, backup, antigravityStartLine(antigravityFirst))
	db := writeAntigravityDB(t, home, antigravityFirst, "one")
	antigravityBackUp(t, home, backup)
	if err := os.Remove(db); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db+"-wal", []byte("left"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := aca.Antigravity{}.Command(t.Context(), aca.Launch{Dir: "/work", SessionID: antigravityFirst, UserHome: home, BackupDir: backup})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got[:2], []string{"agy", "--conversation=" + antigravityFirst}) {
		t.Fatalf("command %q, want the conversation resumed", got)
	}
	if _, err := os.Stat(db + "-wal"); !os.IsNotExist(err) {
		t.Fatalf("the old write-ahead log is still there: %v", err)
	}
	if rows := readAntigravityDB(t, db); !slices.Equal(rows, []string{"one"}) {
		t.Fatalf("the db put back holds %q", rows)
	}

	// With neither, a new conversation.
	got, err = aca.Antigravity{}.Command(t.Context(), aca.Launch{Dir: "/work", SessionID: antigravityCleared, UserHome: home, BackupDir: backup})
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "--conversation") }) {
		t.Fatalf("command %q resumes a conversation with no db", got)
	}
}
