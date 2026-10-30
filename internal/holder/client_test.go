package holder

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/waittest"
)

// output collects what a session reads, until it ends.
type output struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	err  error
	done chan struct{}
}

func readSession(s *Session) *output {
	o := &output{done: make(chan struct{})}
	go func() {
		defer close(o.done)
		p := make([]byte, 4096)
		for {
			n, err := s.Read(p)
			o.mu.Lock()
			o.buf.Write(p[:n])
			if err != nil {
				o.err = err
			}
			o.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return o
}

func (o *output) waitFor(t *testing.T, s string) {
	t.Helper()
	deadline := waittest.Deadline(t)
	for {
		o.mu.Lock()
		got := o.buf.String()
		o.mu.Unlock()
		if strings.Contains(got, s) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q in %q", s, got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (o *output) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-o.done:
	case <-waittest.After(t):
		t.Fatal("the session did not end")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.err
}

func (f fixture) session(t *testing.T, id string, size Size, history int) *Session {
	t.Helper()
	s, err := Attach(context.Background(), identity.HolderSocket(f.home, id), id, size, history)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.startShell(t, "term-00000201", "seq 1 40; "+interactiveShell)
	s := f.session(t, "term-00000201", Size{Cols: 100, Rows: 30}, 5)
	if s.Snapshot.Size != (Size{Cols: 100, Rows: 30}) || s.Hello.TerminalID != "term-00000201" {
		t.Fatalf("got snapshot %+v, hello %+v", s.Snapshot, s.Hello)
	}
	if s.History.From+int64(len(s.History.Lines)) != s.Snapshot.HistoryLines || len(s.History.Lines) > 5 {
		t.Fatalf("got history %+v for a screen at line %d", s.History, s.Snapshot.HistoryLines)
	}
	out := readSession(s)
	if _, err := s.Write([]byte("stty size\n")); err != nil {
		t.Fatal(err)
	}
	out.waitFor(t, "30 100\r\n")
	if err := s.Resize(Size{Cols: 90, Rows: 25}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write([]byte("stty size; exit 4\n")); err != nil {
		t.Fatal(err)
	}
	out.waitFor(t, "25 90\r\n")
	if err := out.wait(t); !errors.Is(err, io.EOF) {
		t.Fatalf("got %v, want EOF at the exit", err)
	}
	if e := s.Exit(); e == nil || e.Code != 4 {
		t.Fatalf("got exit %+v, want 4", e)
	}
}

// TestSessionOutlivesWriteTimeout types into a session attached for longer
// than the holder's write timeout: its output keeps coming.
func TestSessionOutlivesWriteTimeout(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.startShell(t, "term-00000207", interactiveShell)
	s := f.session(t, "term-00000207", Size{Cols: 80, Rows: 24}, 0)
	out := readSession(s)
	time.Sleep(writeTimeout + time.Second)
	if _, err := s.Write([]byte("echo la''ter\n")); err != nil {
		t.Fatal(err)
	}
	out.waitFor(t, "later\r\n")
}

// TestSessionDetached closes a session: the shell runs on, and another
// attaches to it.
func TestSessionDetached(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.startShell(t, "term-00000202", interactiveShell)
	first := f.session(t, "term-00000202", Size{Cols: 80, Rows: 24}, 0)
	if _, err := first.Write([]byte("echo fir''st\n")); err != nil {
		t.Fatal(err)
	}
	readSession(first).waitFor(t, "first\r\n")
	first.Close()
	second := f.session(t, "term-00000202", Size{Cols: 80, Rows: 24}, 0)
	out := readSession(second)
	out.waitFor(t, "first") // on the screen it paints
	if _, err := second.Write([]byte("echo sec''ond\n")); err != nil {
		t.Fatal(err)
	}
	out.waitFor(t, "second\r\n")
}

func TestSessionConnectionLost(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	o := f.options("term-00000203", f.script(t, "exec sleep 60"))
	started, err := Start(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killHolder(started) })
	s := f.session(t, "term-00000203", Size{Cols: 80, Rows: 24}, 0)
	out := readSession(s)
	if err := syscall.Kill(started.HolderPID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if err := out.wait(t); !errors.Is(err, ErrConnectionLost) || errors.Is(err, io.EOF) {
		t.Fatalf("got %v, want the connection lost, which is no exit", err)
	}
}

func TestSessionRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.startShell(t, "term-00000204", "exec sleep 60")
	socket := identity.HolderSocket(f.home, "term-00000204")
	if _, err := Attach(context.Background(), socket, "term-00000205", Size{Cols: 80, Rows: 24}, 0); err == nil || !strings.Contains(err.Error(), "runs terminal term-00000204") {
		t.Fatalf("got %v, want the other terminal refused", err)
	}

	// A version 1 holder answers any hello as the daemon's.
	old := filepath.Join(f.home, "old.sock")
	l, err := net.Listen("unix", old)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		if _, _, err := readFrame(c); err == nil {
			_ = writeFrame(c, frameHello, Hello{Version: 1, TerminalID: "term-00000206"})
		}
		_, _, _ = readFrame(c)
	}()
	if _, err := Attach(context.Background(), old, "term-00000206", Size{Cols: 80, Rows: 24}, 0); !errors.Is(err, ErrAttachUnsupported) {
		t.Fatalf("got %v, want attach unsupported", err)
	}
}
