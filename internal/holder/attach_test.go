package holder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/identity"
)

// testClient attaches to a holder as a client does, frame by frame, and
// records what it gets.
type testClient struct {
	t     *testing.T
	conn  net.Conn
	hello Hello
	done  chan struct{} // closed when the holder closes the connection

	mu       sync.Mutex
	history  []HistoryLines
	snapshot *Snapshot
	output   bytes.Buffer // after the snapshot: its paint, then the output
	exit     *Exit
}

func (f fixture) attach(t *testing.T, id string, size Size, history int) *testClient {
	t.Helper()
	conn, err := net.Dial("unix", identity.HolderSocket(f.home, id))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c := &testClient{t: t, conn: conn, done: make(chan struct{})}
	if err := writeFrame(conn, frameHello, Hello{Version: Version, Role: roleClient}); err != nil {
		t.Fatal(err)
	}
	typ, body, err := readFrame(conn)
	if err != nil || typ != frameHello || json.Unmarshal(body, &c.hello) != nil {
		t.Fatalf("no hello: %d %v", typ, err)
	}
	if err := writeFrame(conn, frameAttach, attachRequest{Size: size, History: history}); err != nil {
		t.Fatal(err)
	}
	go c.read()
	return c
}

func (c *testClient) read() {
	defer close(c.done)
	for {
		typ, body, err := readFrame(c.conn)
		if err != nil {
			return
		}
		c.mu.Lock()
		switch typ {
		case frameHistory:
			var h HistoryLines
			if json.Unmarshal(body, &h) == nil {
				c.history = append(c.history, h)
			}
		case frameSnapshot:
			c.snapshot = &Snapshot{}
			_ = json.Unmarshal(body, c.snapshot)
		case frameOutput:
			c.output.Write(body)
		case frameExited:
			c.exit = &Exit{}
			_ = json.Unmarshal(body, c.exit)
		}
		c.mu.Unlock()
	}
}

func (c *testClient) input(s string) {
	c.t.Helper()
	if err := writeRaw(c.conn, frameInput, []byte(s)); err != nil {
		c.t.Fatal(err)
	}
}

func (c *testClient) resize(size Size) {
	c.t.Helper()
	if err := writeFrame(c.conn, frameResize, size); err != nil {
		c.t.Fatal(err)
	}
}

// waitFor waits until the output so far satisfies ok.
func (c *testClient) waitFor(what string, ok func(out string) bool) {
	c.t.Helper()
	deadline := time.Now().Add(waitLimit)
	for {
		c.mu.Lock()
		out := c.output.String()
		c.mu.Unlock()
		if ok(out) {
			return
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("no %s in the output: %q", what, out)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (c *testClient) waitOutput(s string) {
	c.t.Helper()
	c.waitFor(fmt.Sprintf("%q", s), func(out string) bool { return strings.Contains(out, s) })
}

func (c *testClient) waitDone() {
	c.t.Helper()
	select {
	case <-c.done:
	case <-time.After(waitLimit):
		c.t.Fatal("the holder did not close the connection")
	}
}

// startShell starts a holder whose shell runs body in sh, history on.
func (f fixture) startShell(t *testing.T, id, body string) {
	t.Helper()
	o := f.options(id, f.script(t, body))
	o.HistoryLines = 100_000
	s, err := Start(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killHolder(s) })
}

const interactiveShell = "PS1='$ ' exec /bin/sh"

func TestAttachInputOutput(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.startShell(t, "term-00000101", interactiveShell)
	c := f.attach(t, "term-00000101", Size{Cols: 80, Rows: 24}, 0)
	if c.hello.Version != Version || c.hello.TerminalID != "term-00000101" {
		t.Fatalf("got hello %+v", c.hello)
	}
	c.input("echo at''tached\n")
	c.waitOutput("attached\r\n")
	c.mu.Lock()
	snap := c.snapshot
	c.mu.Unlock()
	if snap == nil || snap.Size != (Size{Cols: 80, Rows: 24}) {
		t.Fatalf("got snapshot %+v", snap)
	}
}

// TestAttachConsistent attaches while the shell prints: the history, the
// screen and the output after it hold every line once, in order.
func TestAttachConsistent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	const n = 10_000
	f.startShell(t, "term-00000102", fmt.Sprintf(`i=0; while [ $i -lt %d ]; do echo "line $i"; i=$((i+1)); done; echo done; exec sleep 60`, n))
	time.Sleep(20 * time.Millisecond) // into the output
	c := f.attach(t, "term-00000102", Size{Cols: 80, Rows: 24}, -1)
	replayed := newScreen(80, 24, nil)
	defer replayed.close()
	var scrolled []string
	fed := 0
	c.waitFor("done", func(out string) bool {
		lines, _ := replayed.write([]byte(out[fed:]))
		scrolled, fed = append(scrolled, lines...), len(out)
		return slices.Contains(screenLines(replayed), "done")
	})

	var got []string
	c.mu.Lock()
	next := int64(0)
	for _, h := range c.history {
		if h.From != next {
			t.Fatalf("history frame from %d, want %d", h.From, next)
		}
		got = append(got, h.Lines...)
		next += int64(len(h.Lines))
	}
	t.Logf("attached after %d lines of history, at output position %d", next, c.snapshot.Pos)
	if c.snapshot.HistoryLines != next {
		t.Fatalf("snapshot continues history at %d, got lines to %d", c.snapshot.HistoryLines, next)
	}
	c.mu.Unlock()
	got = append(got, scrolled...)
	got = append(got, screenLines(replayed)...)
	got = slices.DeleteFunc(got, func(l string) bool { return l == "" || l == "done" })
	for i := range n {
		if i >= len(got) || ansi.Strip(got[i]) != fmt.Sprintf("line %d", i) {
			t.Fatalf("line %d: got %q", i, got[min(i, len(got)-1)])
		}
	}
	if len(got) != n {
		t.Fatalf("got %d lines, want %d: %q", len(got), n, got[n:])
	}
}

// screenLines is the text of the screen's lines, without trailing spaces.
func screenLines(s *screen) []string {
	var lines []string
	for y := range s.vt.Height() {
		var b strings.Builder
		for x := range s.vt.Width() {
			if c := s.vt.CellAt(x, y); c != nil {
				b.WriteString(c.Content)
			}
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	return lines
}

func TestAttachLatestSizeWins(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.startShell(t, "term-00000103", interactiveShell)
	a := f.attach(t, "term-00000103", Size{Cols: 100, Rows: 30}, 0)
	a.input("stty size\n")
	a.waitOutput("30 100\r\n")
	b := f.attach(t, "term-00000103", Size{Cols: 90, Rows: 20}, 0)
	b.input("stty size\n")
	b.waitOutput("20 90\r\n")
	a.waitOutput("20 90\r\n") // every client gets the output
	a.resize(Size{Cols: 120, Rows: 40})
	a.input("stty size\n")
	b.waitOutput("40 120\r\n")
}

func TestAttachExited(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.startShell(t, "term-00000104", "echo ready; read x; echo bye; exit 3")
	c := f.attach(t, "term-00000104", Size{Cols: 80, Rows: 24}, 0)
	c.waitOutput("ready")
	c.input("\n")
	c.waitDone()
	c.mu.Lock()
	exit, out := c.exit, c.output.String()
	c.mu.Unlock()
	if exit == nil || exit.Code != 3 || !strings.Contains(out, "bye") {
		t.Fatalf("got exit %+v after %q, want 3 after the last output", exit, out)
	}
	// The holder waits for the daemon to release it: a client attaching
	// meanwhile gets the screen, then the exit.
	late := f.attach(t, "term-00000104", Size{Cols: 80, Rows: 24}, 10)
	late.waitDone()
	late.mu.Lock()
	defer late.mu.Unlock()
	if late.exit == nil || late.exit.Code != 3 || late.snapshot == nil {
		t.Fatalf("got exit %+v, snapshot %+v", late.exit, late.snapshot)
	}
}

func TestAttachSlowClientDropped(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	finished := filepath.Join(f.home, "finished")
	f.startShell(t, "term-00000105", fmt.Sprintf("read x; head -c 30000000 /dev/zero | tr '\\0' x; touch %s; exec sleep 60", finished))
	conn, err := net.Dial("unix", identity.HolderSocket(f.home, "term-00000105"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := writeFrame(conn, frameHello, Hello{Version: Version, Role: roleClient}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readFrame(conn); err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(conn, frameAttach, attachRequest{Size: Size{Cols: 80, Rows: 24}}); err != nil {
		t.Fatal(err)
	}
	if err := writeRaw(conn, frameInput, []byte("\n")); err != nil {
		t.Fatal(err)
	}
	// Reads nothing until the shell is done: the holder drops it rather
	// than hold the shell back.
	deadline := time.Now().Add(waitLimit)
	for {
		if _, err := os.Stat(finished); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the shell was held back by a client that does not read")
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = conn.SetReadDeadline(time.Now().Add(waitLimit))
	n, err := io.Copy(io.Discard, conn)
	if err != nil || n >= 30_000_000 {
		t.Fatalf("read %d bytes, %v; want the connection closed early", n, err)
	}
}

func TestAttachAnswersWithoutClients(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	answer := filepath.Join(f.home, "answer")
	f.startShell(t, "term-00000106", fmt.Sprintf(`stty raw -echo; printf '\033[5;7H\033[6n'; dd bs=1 count=6 2>/dev/null > %s; exec sleep 60`, answer))
	deadline := time.Now().Add(waitLimit)
	for {
		got, _ := os.ReadFile(answer)
		if string(got) == "\x1b[5;7R" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %q, want the screen to answer the cursor position", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestDaemonVersion1Hello connects as a version 1 daemon, whose hello has
// no role: it is the daemon.
func TestDaemonVersion1Hello(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.startShell(t, "term-00000107", "exec sleep 60")
	conn, err := net.Dial("unix", identity.HolderSocket(f.home, "term-00000107"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := writeFrame(conn, frameHello, map[string]int{"version": 1}); err != nil {
		t.Fatal(err)
	}
	if typ, _, err := readFrame(conn); err != nil || typ != frameHello {
		t.Fatalf("got %d, %v", typ, err)
	}
	if err := writeFrame(conn, frameStop, stopRequest{GraceMS: 1000}); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(waitLimit))
	typ, _, err := readFrame(conn)
	if err != nil && !errors.Is(err, io.EOF) || typ != frameExited {
		t.Fatalf("got %d, %v, want exited", typ, err)
	}
}

func TestHistoryOff(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.start(t, "term-00000108", f.script(t, "seq 1 100; exec sleep 60")) // history_lines 0
	c := f.attach(t, "term-00000108", Size{Cols: 80, Rows: 24}, -1)
	c.waitOutput("100")
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hello.HistoryLines != 0 || len(c.history) != 0 || c.snapshot.HistoryLines != 0 {
		t.Fatalf("got hello %+v, history %v, snapshot %+v", c.hello, c.history, c.snapshot)
	}
	if _, err := os.Stat(filepath.Join(identity.TerminalDir(f.home, "term-00000108"), "history")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want no history kept, got %v", err)
	}
}

// TestHistoryRelaunched starts a terminal's shell twice: the history goes
// on, after a marker.
func TestHistoryRelaunched(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.startShell(t, "term-00000109", "seq 1 30; exit 0")
	c := f.dial(t, "term-00000109")
	waitExit(t, c)
	dir := filepath.Join(identity.TerminalDir(f.home, "term-00000109"), "history")
	deadline := time.Now().Add(waitLimit)
	for { // the vt writes history at its own pace
		if h, _ := TailHistory(dir, 1); h.Next >= 6 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no history written")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := c.Release(); err != nil {
		t.Fatal(err)
	}
	waitGone(t, c.Hello().HolderPID)

	f.startShell(t, "term-00000109", "echo again; exec sleep 60")
	got, err := ReadHistory(dir, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Fields("1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30")
	if len(got.Lines) != 31 || !slices.Equal(got.Lines[:30], want) || !strings.HasPrefix(got.Lines[30], "── shell restarted ") {
		t.Fatalf("got %q, want the first run's lines, its last screen included, then the marker", got.Lines)
	}
}
