package daemon

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/remote"
	"github.com/srjn45/wdx/internal/waittest"
)

// TestRemoteOperationsClassifyEvery checks every operation of the spec is
// served through a session or refused with a reason, and nothing else is
// classified: a new operation must be decided on.
func TestRemoteOperationsClassifyEvery(t *testing.T) {
	t.Parallel()
	spec, err := api.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			id := op.OperationID
			seen[id] = true
			rule, ok := remoteOperations[id]
			switch {
			case !ok:
				t.Errorf("%s (%s %s) is not in remoteOperations: decide whether a session may reach it", id, method, path)
			case !rule.allowed && rule.reason == "":
				t.Errorf("%s is refused with no reason", id)
			case rule.allowed && (strings.HasPrefix(path, hubPath) || strings.HasPrefix(path, settingsPath) || path == "/v1/settings" || path == reloadPath || path == tideViewPath || strings.HasSuffix(path, "/view") || strings.HasSuffix(path, "/tabs")):
				t.Errorf("%s (%s %s) is allowed, which no session may reach", id, method, path)
			}
		}
	}
	for id := range remoteOperations {
		if !seen[id] {
			t.Errorf("remoteOperations has %s, which the spec does not", id)
		}
	}
}

// TestSessionStreamPassesTheLastAnswer checks the last bytes the session
// server writes before it closes a stream reach the client: the stream's
// write of them is held until the stream would be closed under it, or
// until serve has seen the server close its end.
func TestSessionStreamPassesTheLastAnswer(t *testing.T) {
	t.Parallel()
	streams := newSessionStreams()
	defer streams.Close()
	client, host := net.Pipe()
	defer client.Close()
	stream := &heldConn{Conn: host, release: make(chan struct{}), closed: make(chan struct{})}
	ended := &lineWatch{want: "session stream ended", seen: make(chan struct{})}
	ctx := logging.NewContext(context.Background(), logging.New(ended, slog.LevelInfo))
	served := make(chan struct{})
	go func() {
		defer close(served)
		streams.serve(ctx, stream, remote.Session{ID: "sess-test"})
	}()
	go func() {
		select {
		case <-ended.seen:
			close(stream.release)
		case <-stream.closed:
		}
	}()

	conn, err := streams.Accept()
	if err != nil {
		t.Fatal(err)
	}
	answer := []byte("HTTP/1.1 409 Conflict\r\nConnection: close\r\nContent-Length: 0\r\n\r\n")
	if _, err := conn.Write(answer); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	_ = client.SetReadDeadline(waittest.Deadline(t))
	got, err := io.ReadAll(client)
	if err != nil {
		t.Fatalf("reading the stream: %v, after %q", err, got)
	}
	if !bytes.Equal(got, answer) {
		t.Errorf("the client read %q before the stream closed, want %q", got, answer)
	}
	<-served
}

// heldConn holds each write until release closes, or until it is closed,
// when the write goes on to the closed connection.
type heldConn struct {
	net.Conn
	release chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (h *heldConn) Write(p []byte) (int, error) {
	select {
	case <-h.release:
	case <-h.closed:
	}
	return h.Conn.Write(p)
}

func (h *heldConn) Close() error {
	err := h.Conn.Close()
	h.once.Do(func() { close(h.closed) })
	return err
}

// lineWatch closes seen at the first log line that holds want.
type lineWatch struct {
	want string
	seen chan struct{}
	once sync.Once
}

func (l *lineWatch) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(l.want)) {
		l.once.Do(func() { close(l.seen) })
	}
	return len(p), nil
}
