package holder

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/waittest"
)

// relayed attaches to the holder of id through a relay for node, as the
// daemon serves a client elsewhere: AttachConn on one end of a pipe, whose
// other end the relay greets and copies to the holder.
func (f fixture) relayed(t *testing.T, id, node string) *Session {
	t.Helper()
	return f.relayedAt(t, id, node, Version)
}

// relayedAt is relayed for a client the relay says speaks version.
func (f fixture) relayedAt(t *testing.T, id, node string, version int) *Session {
	t.Helper()
	r, err := DialRelay(limited(t), identity.HolderSocket(f.home, id), id, node, version)
	if err != nil {
		t.Fatal(err)
	}
	client, daemon := net.Pipe()
	var copying sync.WaitGroup
	t.Cleanup(func() {
		client.Close()
		daemon.Close()
		r.Conn.Close()
		copying.Wait()
	})
	copying.Go(func() {
		if err := r.Greet(limited(t), daemon, daemon); err != nil {
			daemon.Close()
			return
		}
		copying.Go(func() {
			_, _ = io.Copy(r.Conn, daemon)
			r.Conn.Close()
		})
		_, _ = io.Copy(daemon, r.Conn)
		daemon.Close()
	})
	s, err := AttachConn(limited(t), client, id, Size{Cols: 80, Rows: 24}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAttachRelayed(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.startShell(t, "term-00000301", interactiveShell)
	s := f.relayed(t, "term-00000301", "laptop")
	if s.Hello.Version != Version || s.Hello.TerminalID != "term-00000301" {
		t.Fatalf("got hello %+v, want the holder's", s.Hello)
	}
	out := readSession(s)
	if _, err := s.Write([]byte("echo re''layed\n")); err != nil {
		t.Fatal(err)
	}
	out.waitFor(t, "relayed\r\n")

	var attached logLine
	if !waittest.Until(t, func() bool {
		for _, l := range f.logged(t) {
			if l.msg() == "client attached" {
				attached = l
				return true
			}
		}
		return false
	}) {
		t.Fatal("no client attached logged")
	}
	if attached["remote"] != true || attached["from_node"] != "laptop" {
		t.Errorf("logged %v, want the client remote, from laptop", attached)
	}
}

// TestAttachRelayedHere relays a client of this node: the holder logs no
// remote one.
func TestAttachRelayedHere(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.startShell(t, "term-00000302", interactiveShell)
	s := f.relayed(t, "term-00000302", "")
	out := readSession(s)
	if _, err := s.Write([]byte("echo he''re\n")); err != nil {
		t.Fatal(err)
	}
	out.waitFor(t, "here\r\n")
	for _, l := range f.logged(t) {
		if l.msg() == "client attached" && (l["remote"] != nil || l["from_node"] != nil) {
			t.Errorf("logged %v, want no remote client", l)
		}
	}
}

// TestDialRelayVersions dials holders of each version: those before version 5
// know no relayed client and are refused.
func TestDialRelayVersions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		version int
		ok      bool
	}{{1, false}, {2, false}, {4, false}, {5, true}} {
		f := newFixture(t)
		socket := filepath.Join(f.home, "holder.sock")
		l, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { l.Close() })
		said := make(chan Hello, 1)
		go func() {
			c, err := l.Accept()
			if err != nil {
				return
			}
			defer c.Close()
			_, body, err := readFrame(c)
			if err != nil {
				return
			}
			var hello Hello
			_ = json.Unmarshal(body, &hello)
			said <- hello
			_ = writeFrame(c, frameHello, Hello{Version: tc.version, TerminalID: "term-0000030a"})
			_, _ = io.Copy(io.Discard, c) // until the relay closes
		}()
		r, err := DialRelay(limited(t), socket, "term-0000030a", "laptop", Version)
		if tc.ok {
			if err != nil {
				t.Fatalf("version %d: %v", tc.version, err)
			}
			r.Conn.Close()
		} else if !errors.Is(err, ErrHolderVersionUnsupported) {
			t.Fatalf("version %d: got %v, want ErrHolderVersionUnsupported", tc.version, err)
		}
		if hello := <-said; hello.Role != roleClient || !hello.Remote || hello.Node != "laptop" {
			t.Errorf("version %d: the relay said %+v", tc.version, hello)
		}
	}
}

func TestDialRelayOtherTerminal(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.startShell(t, "term-00000303", interactiveShell)
	_, err := DialRelay(limited(t), identity.HolderSocket(f.home, "term-00000303"), "term-00000399", "laptop", Version)
	if err == nil || !strings.Contains(err.Error(), "not term-00000399") {
		t.Fatalf("got %v, want the other terminal refused", err)
	}
}

// TestAttachRelayedNewerClient relays a client newer than the holder, which
// is served: the holder is told the client's version, not the relay's.
func TestAttachRelayedNewerClient(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.startShell(t, "term-00000304", interactiveShell)
	s := f.relayedAt(t, "term-00000304", "laptop", Version+1)
	out := readSession(s)
	if _, err := s.Write([]byte("echo ne''wer\n")); err != nil {
		t.Fatal(err)
	}
	out.waitFor(t, "newer\r\n")
	var attached logLine
	if !waittest.Until(t, func() bool {
		for _, l := range f.logged(t) {
			if l.msg() == "client attached" {
				attached = l
				return true
			}
		}
		return false
	}) {
		t.Fatal("no client attached logged")
	}
	if attached["client_version"] != float64(Version+1) {
		t.Errorf("logged %v, want the client's version %d", attached, Version+1)
	}
}

// TestDialRelayOlderClient relays a client older than the oldest the holder
// serves: the holder refuses it, and the relay says so.
func TestDialRelayOlderClient(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.startShell(t, "term-00000305", interactiveShell)
	_, err := DialRelay(limited(t), identity.HolderSocket(f.home, "term-00000305"), "term-00000305", "laptop", oldestClientVersion-1)
	if !errors.Is(err, ErrClientVersionUnsupported) {
		t.Fatalf("got %v, want ErrClientVersionUnsupported", err)
	}
	if !waittest.Until(t, func() bool {
		for _, l := range f.logged(t) {
			if l.msg() == "client refused" && l["client_version"] == float64(oldestClientVersion-1) {
				return true
			}
		}
		return false
	}) {
		t.Fatal("the holder logged no client refused")
	}
}
