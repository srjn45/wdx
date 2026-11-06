package aca_test

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

const (
	codexLaterSubagent = "01a1abcb-1000-7000-8000-000000000003"
	codexDeeper        = "01a1abcb-2000-7000-8000-000000000004"
	codexEarlierSub    = "01a1ab81-0000-7000-8000-000000000005"
	codexOther         = "01a1abcb-3000-7000-8000-000000000006" // another conversation's
	codexOtherSub      = "01a1abcb-4000-7000-8000-000000000007"
	codexLateSub       = "01a1abcb-5000-7000-8000-000000000008" // its file not there yet
)

// codexFile is where codexHome keeps session id.
func codexFile(home, id string) string {
	return filepath.Join(home, ".codex", "sessions", "2026", "11", "05", "rollout-2026-11-05T13-59-27-"+id+".jsonl")
}

// writeCodexSubagent keeps in home a subagent's session file, the sample with its
// session_meta naming parent, and has the parent's file say it started it, as real ones
// do.
func writeCodexSubagent(t *testing.T, home, id, parent string) {
	t.Helper()
	sample, err := os.ReadFile(filepath.Join("testdata", "codex", "0.163.0", "session", "rollout.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(sample), codexSession, id)
	text = strings.Replace(text, `"id":"`+id+`",`, `"id":"`+id+`","forked_from_id":"`+parent+`","parent_thread_id":"`+parent+`",`, 1)
	text = strings.Replace(text, `"thread_source":"user"`, `"thread_source":"subagent"`, 1)
	if !strings.Contains(text, `"parent_thread_id":"`+parent+`"`) {
		t.Fatal("the sample's session_meta has no id to put the parent after")
	}
	if err := os.WriteFile(codexFile(home, id), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	codexStarted(t, home, parent, id)
}

// codexStarted appends to parent's session file the line Codex 0.163.0 writes when it
// starts subagent id.
func codexStarted(t *testing.T, home, parent, id string) {
	t.Helper()
	for _, line := range []string{
		`{"timestamp":"2026-11-05T12:04:31.819Z","ordinal":75,"type":"event_msg","payload":{"type":"item_completed","thread_id":"` + parent + `","turn_id":"01a1ab98-9dc3-7581-a077-d35ec22cc59b","item":{"type":"SubAgentActivity","model":"gpt-6-sol","reasoning_effort":"medium","id":"call_847e468ec101476f80d51988d2b34e2d","kind":"started","agent_thread_id":"` + id + `","agent_path":"/root/run_command"},"started_at_ms":1793880271819,"completed_at_ms":1793880271819}}`,
	} {
		appendFile(t, codexFile(home, parent), line+"\n")
	}
}

func codexBackUp(t *testing.T, home, backup string) {
	t.Helper()
	if _, err := (aca.Codex{}).BackUp(t.Context(), aca.Launch{UserHome: home, BackupDir: backup}); err != nil {
		t.Fatalf("backup: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCodexBackUpCopiesOnlyNewBytes(t *testing.T) {
	t.Parallel()
	home := codexHome(t, map[string]struct{ source, cwd string }{codexSession: {"user", ""}})
	backup := t.TempDir()
	writeTurns(t, backup, codexSession)
	codexBackUp(t, home, backup)
	copied := filepath.Join(backup, "transcripts", codexSession+".jsonl")
	if got, want := readFile(t, copied), readFile(t, codexFile(home, codexSession)); got != want {
		t.Fatalf("copy is %d bytes, want the session's %d", len(got), len(want))
	}

	// Marked where a copy made again would not keep it: before the tail that is
	// checked to be the session's.
	kept := strings.Replace(readFile(t, copied), `"timestamp"`, `"TIMESTAMP"`, 1)
	if err := os.WriteFile(copied, []byte(kept), 0o600); err != nil {
		t.Fatal(err)
	}
	line := `{"timestamp":"2026-11-05T13:02:00.000Z","ordinal":40,"type":"event_msg","payload":{"type":"token_count"}}` + "\n"
	appendFile(t, codexFile(home, codexSession), line)
	codexBackUp(t, home, backup)
	if got := readFile(t, copied); got != kept+line {
		t.Fatalf("copy after a new line:\n%s\nwant what was kept and the line", got)
	}

	// What Codex deletes stays as copied.
	if err := os.Remove(codexFile(home, codexSession)); err != nil {
		t.Fatal(err)
	}
	codexBackUp(t, home, backup)
	if got := readFile(t, copied); got != kept+line {
		t.Fatal("copy changed once the session was gone")
	}
}

func TestCodexBackUpKeepsAReplacedSession(t *testing.T) {
	t.Parallel()
	home := codexHome(t, map[string]struct{ source, cwd string }{codexSession: {"user", ""}})
	backup := t.TempDir()
	writeTurns(t, backup, codexSession)
	codexBackUp(t, home, backup)
	old := readFile(t, codexFile(home, codexSession))
	first, _, _ := strings.Cut(old, "\n")
	replaced := first + "\n" + `{"timestamp":"2026-11-05T14:00:00.000Z","type":"event_msg","payload":{"type":"token_count"}}` + "\n"
	if err := os.WriteFile(codexFile(home, codexSession), []byte(replaced), 0o600); err != nil {
		t.Fatal(err)
	}
	codexBackUp(t, home, backup)
	copied := filepath.Join(backup, "transcripts", codexSession+".jsonl")
	aside, err := filepath.Glob(copied + ".replaced-*")
	if err != nil || len(aside) != 1 {
		t.Fatalf("set aside: %v %v", aside, err)
	}
	if readFile(t, aside[0]) != old {
		t.Error("the replaced session's copy was not kept whole")
	}
	if readFile(t, copied) != replaced {
		t.Error("the new session file was not copied")
	}
}

func TestCodexBackUpCopiesSubagentsUnderTheirSession(t *testing.T) {
	t.Parallel()
	home := codexHome(t, map[string]struct{ source, cwd string }{
		codexEarlier: {"user", ""},
		codexSession: {"user", ""},
		codexOther:   {"user", ""},
	})
	writeCodexSubagent(t, home, codexEarlierSub, codexEarlier)
	writeCodexSubagent(t, home, codexLaterSubagent, codexSession)
	writeCodexSubagent(t, home, codexDeeper, codexLaterSubagent)
	writeCodexSubagent(t, home, codexOtherSub, codexOther)
	backup := t.TempDir()
	// The subagent's turn too, which is no session of its own.
	writeTurns(t, backup, codexEarlier, codexSession, codexNamer, codexLaterSubagent, codexEarlier)
	// Started, with no file yet: looked for at the next backup.
	codexStarted(t, home, codexDeeper, codexLateSub)
	codexBackUp(t, home, backup)
	if _, err := os.Stat(filepath.Join(backup, "transcripts", codexSession, "subagents", codexLateSub+".jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a subagent with no file copied: %v", err)
	}
	writeCodexSubagent(t, home, codexLateSub, codexDeeper)
	codexBackUp(t, home, backup)

	want := map[string]string{
		codexEarlier + ".jsonl":                                      codexEarlier,
		codexEarlier + "/subagents/" + codexEarlierSub + ".jsonl":    codexEarlierSub,
		codexSession + ".jsonl":                                      codexSession,
		codexSession + "/subagents/" + codexLaterSubagent + ".jsonl": codexLaterSubagent,
		codexSession + "/subagents/" + codexDeeper + ".jsonl":        codexDeeper,
		codexSession + "/subagents/" + codexLateSub + ".jsonl":       codexLateSub,
	}
	var got []string
	raw := filepath.Join(backup, "transcripts")
	err := filepath.WalkDir(raw, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(raw, path)
		got = append(got, rel)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := make([]string, 0, len(want))
	for path := range want {
		wantPaths = append(wantPaths, path)
	}
	slices.Sort(got)
	slices.Sort(wantPaths)
	if !slices.Equal(got, wantPaths) {
		t.Fatalf("copied %q, want %q", got, wantPaths)
	}
	for path, id := range want {
		if readFile(t, filepath.Join(raw, path)) != readFile(t, codexFile(home, id)) {
			t.Errorf("%s is not a copy of %s", path, id)
		}
	}
}

// An agent from before the copies has only its turns file, and its sessions are copied
// whole on its first backup.
func TestCodexBackUpCopiesAnEarlierAgentWhole(t *testing.T) {
	t.Parallel()
	home := codexHome(t, map[string]struct{ source, cwd string }{
		codexEarlier: {"user", ""},
		codexSession: {"user", "/work/repo/sub"},
	})
	for _, id := range []string{codexEarlier, codexSession} {
		for range 3 {
			appendFile(t, codexFile(home, id), `{"timestamp":"2026-11-05T13:02:00.000Z","type":"event_msg","payload":{"type":"token_count"}}`+"\n")
		}
	}
	backup := t.TempDir()
	writeTurns(t, backup, codexEarlier, codexEarlier, codexSession, codexSession)
	got, err := aca.Codex{}.BackUp(t.Context(), aca.Launch{UserHome: home, BackupDir: backup})
	if err != nil || got != (aca.Backup{Session: codexSession, Dir: "/work/repo/sub"}) {
		t.Fatalf("got %+v, %v; want %s in /work/repo/sub", got, err, codexSession)
	}
	for _, id := range []string{codexEarlier, codexSession} {
		if readFile(t, filepath.Join(backup, "transcripts", id+".jsonl")) != readFile(t, codexFile(home, id)) {
			t.Errorf("session %s not copied whole", id)
		}
	}
}

func TestCodexBackUpRecordsWhereEachSessionIs(t *testing.T) {
	t.Parallel()
	home := codexHome(t, map[string]struct{ source, cwd string }{codexSession: {"user", ""}})
	writeCodexSubagent(t, home, codexSubagent, codexSession)
	backup := t.TempDir()
	writeTurns(t, backup, codexSession, codexSubagent)
	codexBackUp(t, home, backup)
	var paths map[string]string
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(backup, "codex-sessions.json"))), &paths); err != nil {
		t.Fatal(err)
	}
	// Only the user's: a subagent's is never resumed.
	if want := map[string]string{codexSession: codexFile(home, codexSession)}; !maps.Equal(paths, want) {
		t.Fatalf("paths %v, want %v", paths, want)
	}
}

// goneWithACopy is a home and backup where Codex deleted codexSession's file after a
// backup copied it, codexEarlier's still there.
func goneWithACopy(t *testing.T) (home, backup string) {
	t.Helper()
	home = codexHome(t, map[string]struct{ source, cwd string }{
		codexEarlier: {"user", ""},
		codexSession: {"user", "/work/repo/sub"},
	})
	backup = t.TempDir()
	writeTurns(t, backup, codexEarlier, codexSession)
	codexBackUp(t, home, backup)
	if err := os.Remove(codexFile(home, codexSession)); err != nil {
		t.Fatal(err)
	}
	return home, backup
}

// A session Codex deleted is still the agent's, read from its copy, not passed over
// for the one before it.
func TestCodexBackUpReadsAGoneSessionFromItsCopy(t *testing.T) {
	t.Parallel()
	home, backup := goneWithACopy(t)
	got, err := aca.Codex{}.BackUp(t.Context(), aca.Launch{UserHome: home, BackupDir: backup})
	if err != nil {
		t.Fatal(err)
	}
	if want := (aca.Backup{Session: codexSession, Dir: "/work/repo/sub"}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestCodexResumesAGoneSessionFromItsCopy(t *testing.T) {
	t.Parallel()
	home, backup := goneWithACopy(t)
	copied := readFile(t, filepath.Join(backup, "transcripts", codexSession+".jsonl"))
	l := aca.Launch{Dir: "/work/repo", SessionID: codexSession, UserHome: home, BackupDir: backup}
	got, err := aca.Codex{}.Command(t.Context(), l)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got[:3], []string{"codex", "resume", codexSession}) {
		t.Fatalf("%q: want it resumed", got)
	}
	if put := readFile(t, codexFile(home, codexSession)); put != copied {
		t.Fatalf("put back %d bytes, want the copy's %d", len(put), len(copied))
	}

	// A copy the backup does not know the path of stays where it is.
	if err := os.Remove(codexFile(home, codexSession)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(backup, "codex-sessions.json")); err != nil {
		t.Fatal(err)
	}
	if got, err := (aca.Codex{}).Command(t.Context(), l); err != nil || got[1] == "resume" {
		t.Fatalf("%q, %v: want a new conversation", got, err)
	}
	if _, err := os.Stat(codexFile(home, codexSession)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("put back with no path known: %v", err)
	}
}
