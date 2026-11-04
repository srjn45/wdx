package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/waittest"
)

func (f fixture) trashed(id string) string {
	return filepath.Join(identity.TrashAgentsDir(f.home), id)
}

func TestTranscriptReadsTheBackup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "")
	f.aca.Conversation = filepath.Join(t.TempDir(), "conversation")
	if err := os.WriteFile(f.aca.Conversation, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := f.create(t, "helper")

	r, err := f.agents.Transcript(ctx, a.ID, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(identity.AgentDir(f.home, a.ID), aca.TranscriptFile); r.Path != want || r.From != 1 || r.Next != 3 || !slices.Equal(r.Lines, []string{"two", "three"}) {
		t.Errorf("tail = %+v", r)
	}
	from := int64(1)
	r, err = f.agents.Transcript(ctx, a.ID, &from, 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.From != 1 || r.Next != 3 || !slices.Equal(r.Lines, []string{"two"}) {
		t.Errorf("from = %+v", r)
	}
	from = 9
	if r, err = f.agents.Transcript(ctx, a.ID, &from, 1); err != nil || r.From != 3 || len(r.Lines) != 0 {
		t.Errorf("past the end = %+v, %v", r, err)
	}
	if _, err := f.agents.Transcript(ctx, "agent-nothing", nil, 1); err == nil {
		t.Error("an unknown agent has a transcript")
	}
}

func TestATranscriptNotYetWrittenIsEmpty(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "")
	a := f.create(t, "helper")
	r, err := f.agents.Transcript(context.Background(), a.ID, nil, 10)
	if err != nil || r.Next != 0 || r.Lines == nil || len(r.Lines) != 0 {
		t.Errorf("= %+v, %v", r, err)
	}
}

// A clear starts another session: the next start goes on with that one.
func TestAStartGoesOnWithTheLatestSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "exit 0")
	f.aca.Session = filepath.Join(t.TempDir(), "session")
	a := f.create(t, "helper")
	waitExited(t, f, a.ID)
	if err := os.WriteFile(f.aca.Session, []byte("after-clear\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.agents.Start(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	sessions, err := f.aca.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{a.SessionID, "after-clear"}; !slices.Equal(sessions, want) {
		t.Errorf("sessions = %q, want %q", sessions, want)
	}
	got, err := f.agents.Store().Get(ctx, a.ID)
	if err != nil || got.SessionID != "after-clear" {
		t.Errorf("the agent's session = %q, %v", got.SessionID, err)
	}
}

func waitExited(t *testing.T, f fixture, id string) {
	t.Helper()
	waittest.For(t, "the agent to exit", func() bool {
		a, err := f.agents.Store().Get(context.Background(), id)
		return err == nil && !a.Terminal.State.Live()
	})
}

func TestARemovedAgentsBackupGoesToTheTrash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "")
	f.aca.Conversation = filepath.Join(t.TempDir(), "conversation")
	if err := os.WriteFile(f.aca.Conversation, []byte("said\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := f.create(t, "helper")
	if _, err := f.agents.Transcript(ctx, a.ID, nil, 1); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.aca.Conversation, []byte("said\nat the very end\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.agents.Remove(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(identity.AgentDir(f.home, a.ID)); !os.IsNotExist(err) {
		t.Errorf("the backup stayed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(f.trashed(a.ID), aca.TranscriptFile))
	if err != nil || !strings.Contains(string(data), "at the very end") {
		t.Errorf("in the trash: %q, %v", data, err)
	}
	var record map[string]any
	data, err = os.ReadFile(filepath.Join(f.trashed(a.ID), "removed.json"))
	if err != nil || json.Unmarshal(data, &record) != nil {
		t.Fatalf("removed.json: %q, %v", data, err)
	}
	for key, want := range map[string]string{"id": a.ID, "name": "helper", "dir": f.dir, "aca": "claude_code", "session_id": a.SessionID, "project_name": "app"} {
		if record[key] != want {
			t.Errorf("%s = %v, want %q", key, record[key], want)
		}
	}
	if record["removed_at"] == nil {
		t.Error("no removed_at")
	}
}

// A tombstone is an agent still: its backup stays with its row.
func TestATombstoneKeepsItsBackup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "")
	parent := f.create(t, "parent")
	child, err := f.agents.Create(ctx, f.project, "child", f.dir, parent.ID, Launch{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.agents.Remove(ctx, parent.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(identity.AgentDir(f.home, parent.ID)); err != nil {
		t.Errorf("a tombstone lost its backup: %v", err)
	}
	if _, _, err := f.agents.Remove(ctx, child.ID, true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{parent.ID, child.ID} {
		if _, err := os.Stat(f.trashed(id)); err != nil {
			t.Errorf("%s not in the trash: %v", id, err)
		}
	}
}

func TestSweepMovesABackupWithNoRow(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "")
	dir := identity.AgentDir(f.home, "agent-gone")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := f.backups.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.trashed("agent-gone"), "removed.json")); err != nil {
		t.Errorf("not in the trash: %v", err)
	}
}

func TestTheTrashExpires(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "")
	old, fresh := f.trashed("agent-old"), f.trashed("agent-fresh")
	write := func(path string, at time.Time) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(map[string]any{"removed_at": at})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "removed.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(old, time.Now().Add(-DefaultTrashRetention-time.Hour))
	write(fresh, time.Now().Add(-DefaultTrashRetention+time.Hour))
	if err := f.backups.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("past its time: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("before its time: %v", err)
	}
}

// One timer takes what expires with nothing else happening.
func TestTheTrashExpiresOnItsOwn(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "")
	b := NewBackups(context.Background(), f.agents.Store(), f.home, nil, time.Millisecond)
	t.Cleanup(b.Close)
	dir := identity.AgentDir(f.home, "agent-gone")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := b.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	waittest.For(t, "the trash to expire", func() bool {
		_, err := os.Stat(f.trashed("agent-gone"))
		return os.IsNotExist(err)
	})
}
