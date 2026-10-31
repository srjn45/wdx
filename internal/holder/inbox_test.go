package holder

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/waittest"
)

func TestSendTypesInOrder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	got := filepath.Join(f.dir, "got")
	f.start(t, "term-00000301", f.script(t, `read first; read second; echo "$first, $second" > `+got))
	c := f.dial(t, "term-00000301")
	for _, message := range []string{"one two", "three"} {
		if err := c.Send(message); err != nil {
			t.Fatal(err)
		}
	}
	if e := waitExit(t, c); e.Code != 0 {
		t.Fatalf("exit code %d", e.Code)
	}
	if b, err := os.ReadFile(got); err != nil || string(b) != "one two, three\n" {
		t.Fatalf("the shell read %q, %v", b, err)
	}
}

// TestSendDoesNotHoldUpStop fills the input of a program that does not read
// it: the message cannot be written, and the holder must still take the next
// message and the stop.
func TestSendDoesNotHoldUpStop(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ready := filepath.Join(f.dir, "ready")
	// Raw, or the terminal would throw away what does not fit in a line
	// rather than make the writer wait.
	f.start(t, "term-00000302", f.script(t, `stty raw -echo; : > `+ready+`; exec sleep 60`))
	c := f.dial(t, "term-00000302")
	if !waittest.Until(t, func() bool { _, err := os.Stat(ready); return err == nil }) {
		t.Fatal("the shell did not get ready")
	}
	if err := c.Send(strings.Repeat("x", 512<<10)); err != nil {
		t.Fatal(err)
	}
	if err := c.Send("next"); err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(time.Second); err != nil {
		t.Fatal(err)
	}
	waitExit(t, c)
}

func TestSendTooLarge(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	got := filepath.Join(f.dir, "got")
	f.start(t, "term-00000303", f.script(t, `read line; echo "$line" > `+got))
	c := f.dial(t, "term-00000303")
	if err := c.Send(strings.Repeat("x", maxPayload)); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("want it refused as too large, got %v", err)
	}
	// Refused before anything was written: the connection still works.
	if err := c.Send("fits"); err != nil {
		t.Fatal(err)
	}
	waitExit(t, c)
	if b, err := os.ReadFile(got); err != nil || string(b) != "fits\n" {
		t.Fatalf("the shell read %q, %v", b, err)
	}
}

// TestSendToOlderHolder answers as a holder before the inbox would, which
// would ignore the frame: the message must be refused, not lost.
func TestSendToOlderHolder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	socket := filepath.Join(f.home, "holder.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := readFrame(conn); err != nil {
			return
		}
		_ = writeFrame(conn, frameHello, Hello{Version: inboxVersion - 1, TerminalID: "term-00000304"})
		_, _, _ = readFrame(conn) // until the daemon closes
	}()
	c, err := Dial(limited(t), socket, "term-00000304")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Send("hello"); !errors.Is(err, ErrSendUnsupported) {
		t.Fatalf("want it refused as unsupported, got %v", err)
	}
}
