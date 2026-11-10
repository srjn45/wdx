package hublink

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/hub/hubtest"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/waittest"
)

// loggedInAs is a machine whose home hubtest logged in as u's node name, its
// link open and connected.
func loggedInAs(t *testing.T, h *hubtest.Hub, u hubtest.User, name string) (*node, hubtest.Node) {
	t.Helper()
	home := t.TempDir()
	hn := h.LogIn(home, u, name)
	n := newNodeAt(t, h, home, nil)
	if s := n.waitConnection(Connected); s.Node.ID != hn.ID || s.User != u.Username {
		t.Fatalf("status %+v, want logged in as %s", s, name)
	}
	return n, hn
}

// accepted waits for the next stream n is asked for.
func (n *node) accepted() Stream {
	n.t.Helper()
	select {
	case s := <-n.link.Streams():
		n.t.Cleanup(func() { _ = s.Close() })
		return s
	case <-waittest.After(n.t):
		n.t.Fatal("no stream came")
		return Stream{}
	}
}

func TestDialAndAnswer(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	laptop, laptopNode := loggedInAs(t, h, h.User, "laptop")
	desktop, desktopNode := loggedInAs(t, h, h.User, "desktop")

	dialled := make(chan net.Conn, 1)
	go func() {
		conn, err := laptop.link.Dial(laptop.ctx, desktopNode.ID)
		if err != nil {
			t.Error(err)
		}
		dialled <- conn
	}()
	in := desktop.accepted()
	if in.Node.ID != laptopNode.ID || in.Node.Name != "laptop" {
		t.Fatalf("the stream is from %+v, want the laptop", in.Node)
	}
	var out net.Conn
	select {
	case out = <-dialled:
	case <-waittest.After(t):
		t.Fatal("the dial did not return")
	}
	if out == nil {
		t.FailNow()
	}
	defer out.Close()

	up, down := []byte("link-bytes-up-41c9d7"), []byte("link-bytes-down-0a8e5b")
	laptop.secret(string(up), string(down))
	desktop.secret(string(up), string(down))
	exchange := func(from, to net.Conn, b []byte) {
		t.Helper()
		if _, err := from.Write(b); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(b))
		if _, err := io.ReadFull(to, got); err != nil || !bytes.Equal(got, b) {
			t.Fatalf("read %q %v, want %q", got, err, b)
		}
	}
	exchange(out, in, up)
	exchange(in, out, down)

	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("the host read %v, want the end of the stream", err)
	}
	if len(laptop.logged("stream joined")) != 1 || len(desktop.logged("stream joined")) != 1 {
		t.Error("want the stream joined logged on both nodes")
	}
}

func TestDialRefused(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	laptop, laptopNode := loggedInAs(t, h, h.User, "laptop")
	tower := h.NewNode(h.AddUser("bob"), "tower")
	tablet := h.NewNode(h.User, "tablet") // never connected
	for _, c := range []struct {
		what, to string
		want     hubapi.ErrorCode
	}{
		{"another user's node", tower.ID, hubapi.ErrorCodeNodeNotFound},
		{"this node", laptopNode.ID, hubapi.ErrorCodeNodeNotFound},
		{"a node offline", tablet.ID, hubapi.ErrorCodeNodeOffline},
	} {
		if _, err := laptop.link.Dial(laptop.ctx, c.to); Code(err) != c.want {
			t.Errorf("%s: %v, want %s", c.what, err, c.want)
		}
	}
	out := newNode(t, h, nil)
	if _, err := out.link.Dial(out.ctx, tablet.ID); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("logged out: %v, want ErrNotLoggedIn", err)
	}
}

func TestDialNotAnswered(t *testing.T) {
	t.Parallel()
	expire := make(chan time.Time)
	h := hubtest.Start(t, hubtest.StreamExpiry(func(time.Duration) <-chan time.Time { return expire }))
	laptop, _ := loggedInAs(t, h, h.User, "laptop")
	desktop := h.NewNode(h.User, "desktop")
	h.Connect(desktop) // online, but never joins
	failed := make(chan error, 1)
	go func() {
		_, err := laptop.link.Dial(laptop.ctx, desktop.ID)
		failed <- err
	}()
	waittest.For(t, "the laptop waiting", func() bool { return len(h.Logged("stream join waiting")) == 1 })
	expire <- time.Now()
	select {
	case err := <-failed:
		if Code(err) != hubapi.ErrorCodeNodeOffline {
			t.Fatalf("%v, want node_offline", err)
		}
	case <-waittest.After(t):
		t.Fatal("the dial did not return")
	}
}

// hubState is the state the hub shows of node id, as other's link reads it.
func hubState(t *testing.T, other *node, id string) State {
	t.Helper()
	n, err := other.link.Node(context.Background(), "id:"+id)
	if err != nil {
		t.Fatal(err)
	}
	return State{RemoteAccess: n.RemoteAccess, CodeSetup: n.CodeSetup}
}

func TestSendState(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	laptop, _ := loggedInAs(t, h, h.User, "laptop")
	desktop := newNode(t, h, nil)

	// The same state given again changes nothing to send.
	desktop.link.SendState(State{RemoteAccess: true})
	<-desktop.link.stateChanged
	desktop.link.SendState(State{RemoteAccess: true})
	if len(desktop.link.stateChanged) != 0 {
		t.Error("the same state given again is to be sent")
	}

	// Given while logged out: sent at the connect.
	id := desktop.loggedIn("desktop").Node.ID
	saved := func(n int, want State) {
		t.Helper()
		waittest.For(t, fmt.Sprintf("the state saved %d times", n), func() bool { return len(h.Logged("node state saved")) == n })
		if got := hubState(t, laptop, id); got != want {
			t.Fatalf("the hub shows %+v, want %+v", got, want)
		}
	}
	saved(1, State{RemoteAccess: true})
	// Changed: sent at once.
	desktop.link.SendState(State{RemoteAccess: true, CodeSetup: true})
	saved(2, State{RemoteAccess: true, CodeSetup: true})
	// At every connect, the last one given.
	h.Restart()
	saved(3, State{RemoteAccess: true, CodeSetup: true})
}

func TestStateSentOnce(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	desktop := newNode(t, h, nil)

	// Given while logged out, as the daemon does at its start: its notice
	// is still pending at the connect.
	desktop.link.SendState(State{RemoteAccess: true})
	desktop.loggedIn("desktop")
	waittest.For(t, "the first state saved", func() bool { return len(h.Logged("node state saved")) >= 1 })
	// The hub saves the states in the order sent, so that when it has saved
	// the second all sent before it are saved.
	desktop.link.SendState(State{RemoteAccess: true, CodeSetup: true})
	waittest.For(t, "the second state saved", func() bool {
		saved := h.Logged("node state saved")
		return saved[len(saved)-1]["code_setup"] == true
	})
	if saved := h.Logged("node state saved"); len(saved) != 2 {
		t.Errorf("the hub saved %d states, want 2 (each sent once): %v", len(saved), saved)
	}
}

// silentHub stands for a hub that accepts the requests to paths starting
// with prefix, of method, and never answers them: each waits until its
// request is given up, and says so on stalled first.
type silentHub struct {
	next           http.RoundTripper
	method, prefix string
	stalled        chan string
}

func (s *silentHub) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != s.method || !strings.HasPrefix(req.URL.Path, s.prefix) {
		return s.next.RoundTrip(req)
	}
	s.stalled <- req.URL.Path
	<-req.Context().Done()
	return nil, req.Context().Err()
}

// silentNode is a node logged in as name, connected, whose requests the
// silent hub never answers, on clock, with a setup timeout of silentSetup.
func silentNode(t *testing.T, h *hubtest.Hub, name string, clock Clock, silent *silentHub) *node {
	t.Helper()
	home := t.TempDir()
	h.LogIn(home, h.User, name)
	n := &node{t: t, h: h, home: home, clock: clock}
	n.log = filepath.Join(home, "hublink.log")
	n.opts = testOptions(t, clock)
	n.opts.Limits.Setup = silentSetup
	n.opts.WrapNodes = func(rt http.RoundTripper) http.RoundTripper { silent.next = rt; return silent }
	n.open()
	t.Cleanup(n.wantNoSecrets)
	n.waitConnection(Connected)
	return n
}

// silentSetup is a silent node's setup timeout: none of the link's other
// waits on its clock is as long.
const silentSetup = 7 * time.Second

// TestDialHubSilent checks a hub that accepts and never answers, when asked
// for a stream or when joining it, is unreachable once the setup timeout
// passed.
func TestDialHubSilent(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ what, method, prefix string }{
		{"asked for a stream", http.MethodPost, "/v1/streams"},
		{"joining it", http.MethodGet, "/v1/streams/"},
	} {
		t.Run(c.what, func(t *testing.T) {
			t.Parallel()
			h := hubtest.Start(t)
			_, desktop := loggedInAs(t, h, h.User, "desktop")
			clock := newFakeClock(h)
			silent := &silentHub{method: c.method, prefix: c.prefix, stalled: make(chan string, 1)}
			laptop := silentNode(t, h, "laptop", clock, silent)

			failed := make(chan error, 1)
			go func() {
				_, err := laptop.link.Dial(laptop.ctx, desktop.ID)
				failed <- err
			}()
			select {
			case <-silent.stalled:
			case <-waittest.After(t):
				t.Fatal("the dial never reached the hub")
			}
			clock.waiting(t, silentSetup)
			clock.Advance(silentSetup)
			select {
			case err := <-failed:
				if ranOut, ok := errors.AsType[*setupRanOut](err); !isUnreachable(err) || !ok || ranOut.after != silentSetup {
					t.Fatalf("%v, want the hub unreachable after %s", err, silentSetup)
				}
			case <-waittest.After(t):
				t.Fatal("the dial did not return")
			}
		})
	}
}

// TestAnswerHubSilent checks a stream waiting that the hub never lets the
// node join is given up once the setup timeout passed, and that a logout
// does not wait for it meanwhile.
func TestAnswerHubSilent(t *testing.T) {
	t.Parallel()
	expire := make(chan time.Time)
	h := hubtest.Start(t, hubtest.StreamExpiry(func(time.Duration) <-chan time.Time { return expire }))
	laptop, _ := loggedInAs(t, h, h.User, "laptop")
	clock := newFakeClock(h)
	silent := &silentHub{method: http.MethodGet, prefix: "/v1/streams/", stalled: make(chan string, 1)}
	desktop := silentNode(t, h, "desktop", clock, silent)
	desktopID := desktop.link.Status().Node.ID

	failed := make(chan error, 1)
	go func() {
		_, err := laptop.link.Dial(laptop.ctx, desktopID)
		failed <- err
	}()
	select {
	case <-silent.stalled:
	case <-waittest.After(t):
		t.Fatal("the desktop never tried to join")
	}
	clock.waiting(t, silentSetup)

	loggedOut := make(chan error, 1)
	go func() {
		_, err := desktop.link.Logout(desktop.ctx)
		loggedOut <- err
	}()
	select {
	case err := <-loggedOut:
		if err != nil {
			t.Fatal(err)
		}
	case <-waittest.After(t):
		t.Fatal("the logout waited for the join")
	}

	clock.Advance(silentSetup)
	waittest.For(t, "the join given up", func() bool { return len(desktop.logged("stream not joined")) == 1 })
	if err := desktop.logged("stream not joined")[0]["err"]; !strings.Contains(fmt.Sprint(err), "did not answer within "+silentSetup.String()) {
		t.Fatalf("logged %v, want the hub's silence", err)
	}
	var err error
	select {
	case err = <-failed:
	case expire <- time.Now():
		err = <-failed
	}
	if err == nil {
		t.Fatal("the laptop joined a stream the desktop never joined")
	}
}

// TestNodeTransportBounded checks connecting to the node endpoint and
// waiting for its headers have limits, which a silent hub runs into without
// any wait of the link's.
func TestNodeTransportBounded(t *testing.T) {
	t.Parallel()
	tr := nodeTransport(&credentials{}, Limits{}.orDefaults())
	if tr.DialContext == nil || tr.ResponseHeaderTimeout != nodeHeaderTimeout || tr.TLSHandshakeTimeout == 0 {
		t.Fatalf("dial %v, headers %s, TLS %s: want each bounded", tr.DialContext != nil, tr.ResponseHeaderTimeout, tr.TLSHandshakeTimeout)
	}
}

// deafHub stands for a network that, once deaf is closed, stops bringing
// the node what the hub sends on its streams: what comes is dropped, while
// what the node sends still goes.
type deafHub struct {
	next http.RoundTripper
	deaf chan struct{}
}

func (d *deafHub) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := d.next.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		return resp, err
	}
	resp.Body = &deafBody{ReadWriteCloser: resp.Body.(io.ReadWriteCloser), deaf: d.deaf}
	return resp, nil
}

type deafBody struct {
	io.ReadWriteCloser
	deaf chan struct{}
}

func (b *deafBody) Read(p []byte) (int, error) {
	for {
		n, err := b.ReadWriteCloser.Read(p)
		select {
		case <-b.deaf:
			if err != nil {
				return 0, err
			}
			// dropped; the read goes on until the stream is closed
		default:
			return n, err
		}
	}
}

// TestStreamPinged checks a joined stream that hears nothing from the hub
// is pinged once quiet for StreamQuiet, not before, and closed when no pong
// comes within StreamPongWait, on the link's clock; the hub then closes the
// other node's side.
func TestStreamPinged(t *testing.T) {
	t.Parallel()
	const quiet, pongWait = 3 * time.Second, 5 * time.Second
	h := hubtest.Start(t)
	desktop, desktopNode := loggedInAs(t, h, h.User, "desktop")
	clock := newFakeClock(h)
	deaf := &deafHub{deaf: make(chan struct{})}
	home := t.TempDir()
	h.LogIn(home, h.User, "laptop")
	laptop := &node{t: t, h: h, home: home, clock: clock, log: filepath.Join(home, "hublink.log")}
	laptop.opts = testOptions(t, clock)
	laptop.opts.StreamQuiet, laptop.opts.StreamPongWait = quiet, pongWait
	laptop.opts.WrapNodes = func(rt http.RoundTripper) http.RoundTripper { deaf.next = rt; return deaf }
	laptop.open()
	t.Cleanup(laptop.wantNoSecrets)
	laptop.waitConnection(Connected)

	dialled := make(chan net.Conn, 1)
	go func() {
		conn, err := laptop.link.Dial(laptop.ctx, desktopNode.ID)
		if err != nil {
			t.Error(err)
		}
		dialled <- conn
	}()
	in := desktop.accepted()
	var out net.Conn
	select {
	case out = <-dialled:
	case <-waittest.After(t):
		t.Fatal("the dial did not return")
	}
	if out == nil {
		t.FailNow()
	}
	defer out.Close()
	// The clock goes on as the hub's does, so a wait the link works out
	// from it is a little shorter than the time it waits for.
	waitingAtMost := func(d time.Duration) {
		t.Helper()
		clock.waitingFor(t, "at most "+d.String(), func(w time.Duration) bool { return w > 0 && w <= d })
	}
	waitingAtMost(quiet)

	// Bytes received put the ping off.
	clock.Advance(2 * time.Second)
	if _, err := in.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(out, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	close(deaf.deaf)
	clock.Advance(time.Second)
	waitingAtMost(2 * time.Second)
	if got := laptop.logged("stream closed"); len(got) != 0 {
		t.Fatalf("closed before it was quiet for %s: %v", quiet, got)
	}

	clock.Advance(2 * time.Second)
	clock.waiting(t, pongWait)
	clock.Advance(pongWait)
	if _, err := out.Read(make([]byte, 1)); err == nil {
		t.Fatal("the laptop read from a stream it heard no pong on")
	}
	// The ping's context ended closes the stream, before the link logs why.
	var got []map[string]any
	if !waittest.Until(t, func() bool {
		got = laptop.logged("stream closed")
		return len(got) == 1 && got[0]["reason"] == "no pong"
	}) {
		t.Fatalf("logged %v, want the stream closed for no pong", got)
	}
	read := make(chan error, 1)
	go func() {
		_, err := in.Read(make([]byte, 1))
		read <- err
	}()
	select {
	case err := <-read:
		if err == nil {
			t.Fatal("the desktop read from a stream the laptop closed")
		}
	case <-waittest.After(t):
		t.Fatal("the desktop's side stayed open")
	}
}

// TestStreamNobodyTook checks a stream the node joined for another, which
// nothing takes from Streams within Limits.Handoff, is closed, on the link's
// clock: the other node's side sees it end.
func TestStreamNobodyTook(t *testing.T) {
	t.Parallel()
	const handoff = 7 * time.Second
	h := hubtest.Start(t)
	laptop, _ := loggedInAs(t, h, h.User, "laptop")
	clock := newFakeClock(h)
	home := t.TempDir()
	desktopNode := h.LogIn(home, h.User, "desktop")
	desktop := &node{t: t, h: h, home: home, clock: clock, log: filepath.Join(home, "hublink.log")}
	desktop.opts = testOptions(t, clock)
	desktop.opts.Limits.Handoff = handoff
	desktop.open()
	t.Cleanup(desktop.wantNoSecrets)
	desktop.waitConnection(Connected)

	dialled := make(chan net.Conn, 1)
	go func() {
		conn, err := laptop.link.Dial(laptop.ctx, desktopNode.ID)
		if err != nil {
			t.Error(err)
		}
		dialled <- conn
	}()
	var out net.Conn
	select {
	case out = <-dialled:
	case <-waittest.After(t):
		t.Fatal("the dial did not return")
	}
	if out == nil {
		t.FailNow()
	}
	defer out.Close()
	clock.waiting(t, handoff)
	if lines := desktop.logged("stream closed"); len(lines) != 0 {
		t.Fatalf("closed before the handoff ran out: %v", lines)
	}
	clock.Advance(handoff)
	if err := out.SetReadDeadline(waittest.Deadline(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := out.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("the laptop read %v, want the end of the stream", err)
	}
	lines := desktop.logged("stream closed")
	if len(lines) != 1 || lines[0]["reason"] != "nobody took it" {
		t.Fatalf("want the stream closed as nobody took it, got %v", lines)
	}
}
