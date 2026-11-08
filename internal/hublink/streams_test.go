package hublink

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
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
