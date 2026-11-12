package agent

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/aca/acatest"
	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/db/dbtest"
	"github.com/srjn45/wdx/internal/fswatch"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/terminal"
	"github.com/srjn45/wdx/internal/waittest"
)

// syncBuffer is a log written by several goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// cursorAPI stands in for Cursor's API, answering every call with status and body,
// and counts the calls.
func cursorAPI(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

// cursorLogin writes a login file as Cursor's CLI keeps it, and returns its path.
func cursorLogin(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"accessToken": "the-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCursorUsageIsFetchedOnAChange: a change to a Cursor agent's backup, as its status
// line writes, fetches its account's usage from Cursor's API, and the answer, kept in
// the backup, is read and recorded as Cursor's.
func TestCursorUsageIsFetchedOnAChange(t *testing.T) {
	t.Parallel()
	var logged syncBuffer
	ctx := logging.NewContext(context.Background(), logging.New(&logged, slog.LevelDebug))
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("logged:\n%s", logged.String())
		}
	})
	resets := time.Now().Add(20 * 24 * time.Hour).Truncate(time.Millisecond)
	srv, calls := cursorAPI(t, http.StatusOK, fmt.Sprintf(`{"billingCycleEnd": "%d", "planUsage": {"totalPercentUsed": 41.6, "autoPercentUsed": 50, "apiPercentUsed": 0}}`, resets.UnixMilli()))
	cursor := aca.Cursor{Endpoint: srv.URL, AuthFile: cursorLogin(t)}

	path := filepath.Join(t.TempDir(), "test.db")
	dbtest.Seed(t, path)
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	for _, statement := range []string{
		`INSERT INTO projects (id, name, state, created_at, updated_at) VALUES ('proj-app', 'app', 'open', 0, 0)`,
		`INSERT INTO terminals (id, project_id, name, dir, shell, state, kind, created_at, updated_at) VALUES ('term-a', 'proj-app', 'a', '/r', 'sh', 'running', 'agent', 0, 0)`,
		`INSERT INTO agents (id, terminal_id, aca, session_id) VALUES ('agent-a', 'term-a', 'cursor', 's')`,
	} {
		if _, err := d.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	home := runtest.Dir(t)
	backup := identity.AgentDir(home, "agent-a")
	if err := os.MkdirAll(backup, 0o700); err != nil {
		t.Fatal(err)
	}
	store := NewStore(d)
	watchCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	reads := NewStatusReads(watchCtx, store, home, NewACAs([]aca.AiCodingAgent{cursor}, acatest.LookProgram, 0), fswatch.New(watchCtx), time.Millisecond, time.Hour)
	status := func(tokens int) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(backup, "cursor-status.json"), fmt.Appendf(nil, `{"session_id": "s", "context_window": {"total_input_tokens": %d, "context_window_size": 200000}}`, tokens), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	waitTokens := func(want int64) {
		t.Helper()
		waittest.For(t, fmt.Sprint("the context at ", want), func() bool {
			a, err := store.Get(ctx, "agent-a")
			return err == nil && a.Context != nil && a.Context.Tokens == want
		})
	}

	// A watch's first read is no change: an agent idle since the daemon started asks
	// nothing. Its stop waits for a fetch that runs.
	status(10)
	stop := reads.Watch("term-a")
	waitTokens(10)
	stop()
	if strings.Contains(logged.String(), `"msg":"usage fetch`) || calls.Load() != 0 {
		t.Errorf("a fetch began with no change (%d calls reached the API)", calls.Load())
	}

	// The first read says the watch is there, so that the next write is a change.
	status(15)
	stop = reads.Watch("term-a")
	t.Cleanup(stop)
	waitTokens(15)
	status(20)
	var got []Usage
	waittest.For(t, "Cursor's usage recorded", func() bool {
		got, err = store.Usage(ctx)
		return err == nil && len(got) == 3
	})
	if text := usageText(got); text != "cursor month/ 42; cursor month/api ; cursor month/auto 50; " {
		t.Errorf("usage = %s", text)
	}
	if !got[0].ResetsAt.Equal(resets) || got[0].AgentID != "agent-a" {
		t.Errorf("usage = %+v", got[0])
	}
	stop()
	if calls.Load() != 1 {
		t.Errorf("fetched %d times within the interval", calls.Load())
	}
	if !strings.Contains(logged.String(), `"msg":"usage fetched","terminal_id":"term-a","agent_id":"agent-a","status":200`) {
		t.Error("the fetch was not logged")
	}
}

// cursorFetch is a usage fetch of Cursor against srv, once an interval.
func cursorFetch(t *testing.T, srv *httptest.Server, interval time.Duration) (*usageFetch, aca.Launch) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f := &usageFetch{ctx: ctx, fetcher: aca.Cursor{Endpoint: srv.URL, AuthFile: cursorLogin(t)}, client: srv.Client(), interval: interval}
	return f, aca.Launch{BackupDir: t.TempDir()}
}

// TestUsageFetchOnceAnInterval: a fetch does not start while one runs, nor within the
// interval since the last began.
func TestUsageFetchOnceAnInterval(t *testing.T) {
	t.Parallel()
	srv, calls := cursorAPI(t, http.StatusOK, `{}`)
	f, l := cursorFetch(t, srv, time.Hour)
	f.start(l)
	running := f.done
	f.start(l)
	if f.done != running {
		t.Error("a second fetch started while one ran")
	}
	f.finished(<-f.done)
	f.start(l)
	if f.done != nil {
		t.Error("a fetch started within the interval")
	}
	if calls.Load() != 1 {
		t.Errorf("%d calls", calls.Load())
	}

	f.last = f.last.Add(-time.Hour) // the interval past
	f.start(l)
	if f.done == nil {
		t.Fatal("no fetch once the interval passed")
	}
	f.finished(<-f.done)
	if calls.Load() != 2 {
		t.Errorf("%d calls", calls.Load())
	}
}

// TestUsageFetchDeniedOnce: a refused login is remembered, so that it is logged once,
// until a fetch succeeds again.
func TestUsageFetchDeniedOnce(t *testing.T) {
	t.Parallel()
	srv, _ := cursorAPI(t, http.StatusUnauthorized, `{"code": "unauthenticated"}`)
	f, l := cursorFetch(t, srv, time.Nanosecond)
	f.start(l)
	f.finished(<-f.done)
	if !f.denied {
		t.Error("a refused login not remembered")
	}
	f.finished(usageFetchEnded{})
	if f.denied {
		t.Error("still refused after a fetch succeeded")
	}
}

// TestUsageFetchOnlyForAFetcher: an ACA that tells its usage in its own files fetches
// nothing.
func TestUsageFetchOnlyForAFetcher(t *testing.T) {
	t.Parallel()
	reads := NewStatusReads(context.Background(), nil, "", nil, nil, 0, 0)
	f := reads.usageFetch(context.Background(), &acatest.ACA{})
	f.start(aca.Launch{})
	if f.done != nil {
		t.Error("a fetch started for an ACA with nothing to fetch")
	}
}

// TestRecordSizeKeepsTheModel: a model the ACA's files name is recorded and told, but
// not from a file written before the agent's terminal started, which may be the last
// run's, nor a name not of a model's form; none keeps the one recorded.
func TestRecordSizeKeepsTheModel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "")
	a := f.create(t, "helper")
	running := f.waitState(t, a.ID, terminal.StateRunning)
	started := running.Terminal.StartedAt
	var told int
	c := &StatusReads{store: f.agents.Store(), OnChange: func() { told++ }}
	record := func(size aca.ContextSize, want string, wantTold int) {
		t.Helper()
		c.recordSize(ctx, a.ID, started, size, nil)
		got, err := f.agents.Store().Get(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Model != want || told != wantTold {
			t.Errorf("after %+v: model %q, told %d times; want %q, %d", size, got.Model, told, want, wantTold)
		}
	}
	after := started.Add(time.Millisecond)
	record(aca.ContextSize{Tokens: 1, Model: "Opus 5.5", ModelAt: started.Add(-time.Second)}, "", 1)
	record(aca.ContextSize{Tokens: 1, Model: "Opus 5.5", ModelAt: after}, "Opus 5.5", 2)
	record(aca.ContextSize{Tokens: 1, Model: "Opus 5.5", ModelAt: after}, "Opus 5.5", 2)
	record(aca.ContextSize{Tokens: 1}, "Opus 5.5", 2)
	record(aca.ContextSize{Tokens: 1, Model: "opus\nrm -rf", ModelAt: after}, "Opus 5.5", 2)
	record(aca.ContextSize{Tokens: 1, Model: "Fable 5.2", ModelAt: started}, "Fable 5.2", 3)
}
