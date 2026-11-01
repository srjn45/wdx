package terminal

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/holder/holdertest"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/waittest"
)

// agentFixture is a fixture whose agent rows run sleep, and a project.
func agentFixture(t *testing.T) (fixture, *Supervisor, string) {
	t.Helper()
	f := newFixture(t)
	f.opts.Shell = runtest.LoginShell(t)
	f.opts.AgentLaunch = func(context.Context, Terminal) (AgentLaunch, error) {
		return AgentLaunch{Command: []string{"sleep", "60"}}, nil
	}
	return f, f.supervisor(t), f.project(t, "app")
}

func noLink(context.Context, db.Execer, string) error { return nil }

// TestSendAgentOnlyFindsAnAgentsRow: a shell's row must not take a message for
// an ACA, whose delivering is another.
func TestSendAgentOnlyFindsAnAgentsRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, s, p := agentFixture(t)
	row, err := s.Create(ctx, p, "shell", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SendAgent(ctx, row.ID, "hello", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("SendAgent: %v", err)
	}
}

func TestShellEntriesDoNotFindAnAgentsRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, s, p := agentFixture(t)
	row, err := s.CreateAgent(ctx, p, "helper", t.TempDir(), noLink)
	if err != nil {
		t.Fatal(err)
	}
	if row.Kind != KindAgent || row.State != StateRunning {
		t.Fatalf("row = %+v", row)
	}
	if _, err := s.store.Get(ctx, row.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get: %v", err)
	}
	if _, err := s.store.GetByName(ctx, p, "helper"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByName: %v", err)
	}
	if list, err := s.store.List(ctx, p); err != nil || len(list) != 0 {
		t.Errorf("List: %+v, %v", list, err)
	}
	if list, err := s.store.List(ctx, ""); err != nil || len(list) != 0 {
		t.Errorf("List of all: %+v, %v", list, err)
	}
	if _, err := s.Start(ctx, row.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Start: %v", err)
	}
	if _, _, err := s.Send(ctx, row.ID, "hello"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Send: %v", err)
	}
	if _, err := s.Remove(ctx, row.ID, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("Remove: %v", err)
	}
	if _, err := s.History(ctx, row.ID, nil, 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("History: %v", err)
	}
	// None of them touched it.
	if got, err := s.store.row(ctx, row.ID); err != nil || got.State != StateRunning || got.PID != row.PID {
		t.Fatalf("after the refused calls: %+v, %v", got, err)
	}
}

func TestAgentEntriesDoNotFindAShellsRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, s, p := agentFixture(t)
	term, err := s.Create(ctx, p, "shell", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartAgent(ctx, term.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("StartAgent: %v", err)
	}
	if _, err := s.RemoveAgent(ctx, term.ID, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("RemoveAgent: %v", err)
	}
	if _, err := s.AgentHistory(ctx, term.ID, nil, 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("AgentHistory: %v", err)
	}
	if got, err := s.store.Get(ctx, term.ID); err != nil || got.State != StateRunning {
		t.Fatalf("after the refused calls: %+v, %v", got, err)
	}
}

func TestNamesAreUniqueAcrossKinds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, s, p := agentFixture(t)
	dir := t.TempDir()
	if _, err := s.CreateAgent(ctx, p, "helper", dir, noLink); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, p, "HELPER", dir); err == nil {
		t.Fatal("a terminal took an agent's name")
	} else if taken, ok := errors.AsType[*NameTakenError](err); !ok || taken.Terminal.Kind != KindAgent {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Create(ctx, p, "shell", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAgent(ctx, p, "shell", dir, noLink); err == nil {
		t.Fatal("an agent took a terminal's name")
	} else if taken, ok := errors.AsType[*NameTakenError](err); !ok || taken.Terminal.Kind != KindShell {
		t.Fatalf("CreateAgent: %v", err)
	}
}

// TestLinkFailureLeavesNoRow: the two rows of an agent come together.
func TestLinkFailureLeavesNoRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, s, p := agentFixture(t)
	failure := errors.New("no link")
	_, err := s.CreateAgent(ctx, p, "helper", t.TempDir(), func(context.Context, db.Execer, string) error { return failure })
	if !errors.Is(err, failure) {
		t.Fatalf("CreateAgent: %v", err)
	}
	if list, err := s.store.rows(ctx, p); err != nil || len(list) != 0 {
		t.Fatalf("rows: %+v, %v", list, err)
	}
}

func TestAgentLaunchFailureLeavesItFailed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	f.opts.Shell = runtest.LoginShell(t)
	f.opts.AgentLaunch = func(context.Context, Terminal) (AgentLaunch, error) { return AgentLaunch{}, errors.New("unknown ACA") }
	s := f.supervisor(t)
	_, err := s.CreateAgent(ctx, f.project(t, "app"), "helper", t.TempDir(), noLink)
	serr, ok := errors.AsType[*StartError](err)
	if !ok {
		t.Fatalf("CreateAgent: %v", err)
	}
	if serr.Terminal.State != StateFailed || serr.Terminal.Error != "unknown ACA" {
		t.Fatalf("row = %+v", serr.Terminal)
	}
}

// TestAgentLaunchAskedAtEachLaunch: an AI CLI may start differently the
// second time.
func TestAgentLaunchAskedAtEachLaunch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	f.opts.Shell = runtest.LoginShell(t)
	var asked []string
	f.opts.AgentLaunch = func(_ context.Context, row Terminal) (AgentLaunch, error) {
		asked = append(asked, row.ID)
		return AgentLaunch{Command: []string{"/bin/sh", "-c", "exit 3"}}, nil
	}
	s := f.supervisor(t)
	row, err := s.CreateAgent(ctx, f.project(t, "app"), "helper", t.TempDir(), noLink)
	if err != nil {
		t.Fatal(err)
	}
	waitRowState(t, s, row.ID, StateExited)
	if got, _ := s.store.row(ctx, row.ID); got.ExitCode == nil || *got.ExitCode != 3 {
		t.Fatalf("exit code = %v", got.ExitCode)
	}
	if _, err := s.StartAgent(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	waitRowState(t, s, row.ID, StateExited)
	if !slices.Equal(asked, []string{row.ID, row.ID}) {
		t.Fatalf("asked = %q", asked)
	}
}

// TestProjectAndDaemonWorkSeesBothKinds: stopping, relaunching and
// reconciling must not miss an agent's row.
func TestProjectAndDaemonWorkSeesBothKinds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, s, p := agentFixture(t)
	dir := t.TempDir()
	row, err := s.CreateAgent(ctx, p, "helper", dir, noLink)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, p, "shell", dir); err != nil {
		t.Fatal(err)
	}
	if live, err := s.Live(ctx, p); err != nil || len(live) != 2 {
		t.Fatalf("Live: %+v, %v", live, err)
	}
	stopped, err := s.StopProject(ctx, p)
	if err != nil || len(stopped) != 2 {
		t.Fatalf("StopProject: %+v, %v", stopped, err)
	}
	started, err := s.StartProject(ctx, p)
	if err != nil || len(started) != 2 {
		t.Fatalf("StartProject: %+v, %v", started, err)
	}

	// A new supervisor, as a new daemon has, adopts the agent's holder, and
	// the sweep keeps what it has on disk.
	s.Close()
	next := NewSupervisor(ctx, NewStore(f.db), f.opts)
	t.Cleanup(next.Close)
	if err := next.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if err := next.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := next.store.row(ctx, row.ID)
	if err != nil || got.State != StateRunning {
		t.Fatalf("after reconciling: %+v, %v", got, err)
	}
	conn, err := holder.Dial(holder.WithLimits(ctx, holdertest.Limits(t)), next.socket(row.ID), row.ID)
	if err != nil {
		t.Fatalf("the holder is gone: %v", err)
	}
	conn.Close()
	if _, err := next.RemoveAgent(ctx, row.ID, true); err != nil {
		t.Fatal(err)
	}
}

func waitRowState(t *testing.T, s *Supervisor, id string, want State) {
	t.Helper()
	var got Terminal
	var err error
	if !waittest.Until(t, func() bool {
		got, err = s.store.row(context.Background(), id)
		return err == nil && got.State == want
	}) {
		t.Fatalf("row %s: %+v, %v; want %s", id, got, err, want)
	}
}

func TestAMessageDeliveredIsClearedAndOffTheList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	s := f.supervisor(t)
	p := f.project(t, "app")
	term := create(t, s, p, "shell", t.TempDir())
	_, id, err := s.Send(ctx, term.ID, "secret text")
	if err != nil {
		t.Fatal(err)
	}
	waittest.For(t, "the message to be delivered", func() bool {
		list, err := s.Store().Undelivered(ctx, p)
		return err == nil && len(list) == 0
	})
	var text, state string
	if err := s.store.db.QueryRow("SELECT text, state FROM messages WHERE id = ?", id).Scan(&text, &state); err != nil {
		t.Fatal(err)
	}
	if text != "" || state != MessageDelivered {
		t.Errorf("text %q, state %q", text, state)
	}
	if _, err := s.Store().Message(ctx, id); !errors.Is(err, ErrMessageNotFound) {
		t.Errorf("a delivered one: %v", err)
	}
}

// TestCatchUp is what a holder just connected to gets of what is pending. This one
// types what it gets at once, so what was sent may be pending or delivered by now, and
// attempts is what says it was sent.
func TestCatchUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, s, p := agentFixture(t)
	s.opts.AgentSession = func(context.Context, string) (string, error) { return "now", nil }
	row, err := s.CreateAgent(ctx, p, "helper", t.TempDir(), noLink)
	if err != nil {
		t.Fatal(err)
	}
	waitRowState(t, s, row.ID, StateRunning)
	conn, ok := s.currentConn(row.ID)
	if !ok {
		t.Fatal("no connection")
	}
	pid := conn.Hello().HolderPID
	add := func(session string, forwardedTo int) string {
		t.Helper()
		id, err := s.store.addMessage(ctx, row.ID, "text", false, session)
		if err != nil {
			t.Fatal(err)
		}
		if forwardedTo != 0 {
			if err := s.store.messageForwarded(ctx, id, forwardedTo); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	const oldHolder = 1
	never := add("old", 0)
	same := add("old", pid)
	resumed := add("now", oldHolder)
	fresh := add("old", oldHolder)
	review := add("old", oldHolder)
	if _, err := s.store.db.Exec(`INSERT INTO review_comments (id, project_id, worktree, file, line, side, view, quote, content_id, body, state, agent_id, created_at, sent_at, message_id)
		VALUES ('cmt-1', ?, '/w', 'f', 1, 'new', 'committed', '', '', 'b', 'sent', 'a', 1, 1, ?)`, p, review); err != nil {
		t.Fatal(err)
	}

	s.catchUp(ctx, row, conn)

	states := map[string]Message{}
	list, err := s.store.messages(ctx, "m.terminal_id = ?", row.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range list {
		states[m.ID] = m
	}
	for _, tc := range []struct {
		what, id string
		attempts int
	}{
		{"never forwarded", never, 1},
		{"its holder's", same, 2},
		{"for the conversation resumed", resumed, 2},
	} {
		if m := states[tc.id]; m.State == MessageHeld || m.Attempts != tc.attempts {
			t.Errorf("%s: %+v", tc.what, m)
		}
	}
	if m := states[fresh]; m.State != MessageHeld || m.Attempts != 1 {
		t.Errorf("for a conversation gone: %+v", m)
	}
	if a := states[never]; a.State == MessageDelivered && a.Text != "" {
		t.Errorf("delivered, with its text: %+v", a)
	}
	if _, kept := states[review]; kept {
		t.Error("a review's message was held, not given back")
	}
	var state string
	var message sql.NullString
	if err := s.store.db.QueryRow("SELECT state, message_id FROM review_comments WHERE id = 'cmt-1'").Scan(&state, &message); err != nil {
		t.Fatal(err)
	}
	if state != "draft" || message.Valid {
		t.Errorf("its comment: %s, %v", state, message)
	}

	// Held, a person decides: sent, in the conversation of now.
	if _, err := s.ResendMessage(ctx, "msg-nope"); !errors.Is(err, ErrMessageNotFound) {
		t.Errorf("resend of none: %v", err)
	}
	if m, err := s.ResendMessage(ctx, fresh); err != nil || m.State != MessagePending || m.SessionID != "now" || m.Attempts != 2 {
		t.Errorf("resent: %+v, %v", m, err)
	}
	// Or discarded.
	gone := add("old", oldHolder)
	s.catchUp(ctx, row, conn)
	if err := s.Store().DiscardMessage(ctx, gone); err != nil {
		t.Fatal(err)
	}
	if err := s.Store().DiscardMessage(ctx, gone); !errors.Is(err, ErrMessageNotFound) {
		t.Errorf("discarded twice: %v", err)
	}
}

// TestShellMessageIsHeldForAShellAnew: there is no conversation to go on with.
func TestShellMessageIsHeldForAShellAnew(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	s := f.supervisor(t)
	p := f.project(t, "app")
	term := create(t, s, p, "shell", t.TempDir())
	conn, ok := s.currentConn(term.ID)
	if !ok {
		t.Fatal("no connection")
	}
	id, err := s.store.addMessage(ctx, term.ID, "rm -rf build", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.messageForwarded(ctx, id, conn.Hello().HolderPID+1); err != nil {
		t.Fatal(err)
	}
	s.catchUp(ctx, term, conn)
	if m, err := s.Store().Message(ctx, id); err != nil || m.State != MessageHeld {
		t.Errorf("%+v, %v", m, err)
	}
}

// TestBeginSessionKeepsAReview: a message carried to a new session keeps its comments, as
// a message held would give them back; and it goes on to a holder it was not sent to.
func TestBeginSessionKeepsAReview(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, s, p := agentFixture(t)
	s.opts.AgentSession = func(context.Context, string) (string, error) { return "new", nil }
	row, err := s.CreateAgent(ctx, p, "helper", t.TempDir(), noLink)
	if err != nil {
		t.Fatal(err)
	}
	waitRowState(t, s, row.ID, StateRunning)
	conn, ok := s.currentConn(row.ID)
	if !ok {
		t.Fatal("no connection")
	}
	review, err := s.store.addMessage(ctx, row.ID, "text", false, "old")
	if err != nil {
		t.Fatal(err)
	}
	const oldHolder = 1
	if err := s.store.messageForwarded(ctx, review, oldHolder); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec(`INSERT INTO review_comments (id, project_id, worktree, file, line, side, view, quote, content_id, body, state, agent_id, created_at, sent_at, message_id)
		VALUES ('cmt-1', ?, '/w', 'f', 1, 'new', 'committed', '', '', 'b', 'sent', 'a', 1, 1, ?)`, p, review); err != nil {
		t.Fatal(err)
	}
	if err := s.store.BeginSession(ctx, s.store.db, row.ID, "new", "read your history"); err != nil {
		t.Fatal(err)
	}
	s.catchUp(ctx, row, conn)
	list, err := s.store.messages(ctx, "m.terminal_id = ?", row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Text != "read your history" || !list[0].Instant || list[1].ID != review || list[1].State == MessageHeld || list[1].SessionID != "new" {
		t.Errorf("messages = %+v", list)
	}
	var state string
	var message sql.NullString
	if err := s.store.db.QueryRow(`SELECT state, message_id FROM review_comments WHERE id = 'cmt-1'`).Scan(&state, &message); err != nil {
		t.Fatal(err)
	}
	if state != "sent" || message.String != review {
		t.Errorf("the comment is %s, of %q", state, message.String)
	}
}
