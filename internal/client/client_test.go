package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/runtest"
)

func TestMain(m *testing.M) { os.Exit(runtest.Main(m)) }

// serve answers on a new socket with handler until the test ends, and
// returns the socket.
func serve(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	dir := runtest.Dir(t)
	socket := filepath.Join(dir, "wdx.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { srv.Close() })
	return socket
}

func TestSendsDebugIDAndWarnsOnceOfAnotherVersion(t *testing.T) {
	t.Parallel()
	var got http.Header
	socket := serve(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set(api.VersionHeader, "9.9.9")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(api.Version{Name: identity.Name, Version: "9.9.9"})
	})
	var warned []string
	c := New(Options{Socket: socket, DebugID: "dbg-0123456789ab", Quiet: true, VersionMismatch: func(v string) { warned = append(warned, v) }})
	for range 2 {
		v, err := c.Version(context.Background())
		if err != nil || v.Version != "9.9.9" {
			t.Fatalf("unexpected version %+v, %v", v, err)
		}
	}
	if got.Get(api.DebugIDHeader) != "dbg-0123456789ab" || got.Get(api.VersionHeader) != identity.Version || got.Get(api.LogLevelHeader) != "debug" {
		t.Fatalf("want the debug id, version and log level sent, got %v", got)
	}
	if len(warned) != 1 || warned[0] != "9.9.9" {
		t.Fatalf("want one warning of 9.9.9, got %v", warned)
	}
}

func TestProblem(t *testing.T) {
	t.Parallel()
	socket := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(api.VersionHeader, identity.Version)
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(api.NewProblem(api.ErrorCodeNotFound, `project not found: "app"`))
	})
	_, err := New(Options{Socket: socket}).GetProject(context.Background(), "app")
	p, ok := errors.AsType[*ProblemError](err)
	if !ok || p.Problem.Code != api.ErrorCodeNotFound || err.Error() != `project not found: "app"` {
		t.Fatalf("want the not_found problem, got %v", err)
	}
}

func TestUnexpectedResponse(t *testing.T) {
	t.Parallel()
	socket := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(api.VersionHeader, identity.Version)
		w.WriteHeader(http.StatusTeapot)
	})
	_, err := New(Options{Socket: socket}).Version(context.Background())
	if err == nil || errors.As(err, new(*ProblemError)) {
		t.Fatalf("want an unexpected response error, got %v", err)
	}
}

func TestUnavailable(t *testing.T) {
	t.Parallel()
	socket := filepath.Join(t.TempDir(), "wdx.sock")
	_, err := New(Options{Socket: socket}).Version(context.Background())
	u, ok := errors.AsType[*UnavailableError](err)
	if !ok || u.Socket != socket || !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("want unavailable for a missing socket, got %v", err)
	}
	if want := "the daemon is not answering on " + socket + ": connect: no such file or directory"; err.Error() != want {
		t.Fatalf("got %q, want %q", err, want)
	}
}

// lockedBuffer is a log written by requests' goroutines and read by the test.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestWaitForRequestsInFlight: a request in flight when its context is cancelled
// has logged that it gave up by the time Wait returns.
func TestWaitForRequestsInFlight(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	socket := serve(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	})
	var log lockedBuffer
	ctx, cancel := context.WithCancel(logging.NewContext(context.Background(), logging.New(&log, slog.LevelInfo)))
	c := New(Options{Socket: socket, DebugID: "dbg-0123456789ab"})
	go func() { _, _ = c.Version(ctx) }()
	<-entered
	cancel()
	c.Wait()
	if !strings.Contains(log.String(), `"msg":"daemon unreachable"`) {
		t.Fatalf("the request given up on was not logged by the time Wait returned: %s", log.String())
	}
}
