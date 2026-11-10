package remote

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/hublink"
	"github.com/srjn45/wdx/internal/waittest"
)

// echoed is a host whose streams echo what they read, and a client with a
// session and a stream open into it; served is closed once the host's end
// of the stream ended.
func echoed(t *testing.T) (w *world, host, client *node, s Session, conn net.Conn, served <-chan struct{}) {
	t.Helper()
	w = newWorld(t)
	ended := make(chan struct{})
	host = w.newNode(w.hub.User, "desktop", func(o *Options) {
		o.ServeStream = func(_ context.Context, c net.Conn, _ Session) {
			defer close(ended)
			_, _ = io.Copy(c, c)
		}
	})
	client = w.newNode(w.hub.User, "laptop")
	host.setUp()
	s = client.mustOpen(host)
	conn, _, err := client.remote.Stream(client.ctx, host.ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetDeadline(waittest.Deadline(t)); err != nil {
		t.Fatal(err)
	}
	const payload = "traffic before the cut"
	w.secret(payload)
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(conn, make([]byte, len(payload))); err != nil {
		t.Fatal(err)
	}
	// From now on only a cut ends a read.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	return w, host, client, s, conn, ended
}

// wantCut checks the client's end of conn and the host's ended.
func wantCut(t *testing.T, conn net.Conn, served <-chan struct{}) {
	t.Helper()
	read := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 1))
		read <- err
	}()
	select {
	case err := <-read:
		if err == nil {
			t.Error("the client's end read a byte, want it closed")
		}
	case <-waittest.After(t):
		t.Error("the client's end of the stream did not end")
	}
	select {
	case <-served:
	case <-waittest.After(t):
		t.Error("the host's end of the stream did not end")
	}
}

// wantClosedFor waits for the node's session id to close and checks why.
func (n *node) wantClosedFor(id string, want Reason) {
	n.t.Helper()
	waittest.For(n.t, n.ref.Name+"'s session closed", func() bool { return n.closedReason(id) != "" })
	if got := n.closedReason(id); got != want {
		n.t.Errorf("%s's session closed %q, want %s", n.ref.Name, got, want)
	}
}

func waitConnection(t *testing.T, n *node, want hublink.Connection) {
	t.Helper()
	waittest.For(t, n.ref.Name+" "+string(want), func() bool { return n.link.Status().Connection == want })
}

// Codex's proof (REVIEW_SEC_CODEX S2): disabling the account cuts the
// stream open and closes the sessions on both nodes.
func TestDisabledCutsOff(t *testing.T) {
	t.Parallel()
	w, host, client, s, conn, served := echoed(t)
	w.hub.DisableUser("alice")
	waitConnection(t, host, hublink.Disabled)
	waitConnection(t, client, hublink.Disabled)
	wantCut(t, conn, served)
	host.wantClosedFor(s.ID, ReasonCutOff)
	client.wantClosedFor(s.ID, ReasonCutOff)
	_, err := host.remote.Use(host.ctx, s.ID, client.ref)
	wantCode(t, err, CodeSessionClosed)
	if e, _ := asError(err); ReasonOf(e) != ReasonCutOff {
		t.Errorf("Use refused %v, want the reason cut_off", e.Details)
	}
}

// Removing the client cuts the stream open, and the client, cut off,
// closes its session. The host closes it when the hub tells it the node was
// removed, which the daemon hands NodeGone.
func TestRemovedCutsOff(t *testing.T) {
	t.Parallel()
	w, host, client, s, conn, served := echoed(t)
	w.hub.RemoveNode(host.hn, "id:"+client.ref.ID)
	waitConnection(t, client, hublink.Revoked)
	wantCut(t, conn, served)
	client.wantClosedFor(s.ID, ReasonCutOff)
}

// Use refuses a session while the link is cut off, before the sessions
// were closed for it.
func TestUseRefusedCutOff(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	// The link's state closes nothing on the host.
	host := w.newNode(w.hub.User, "desktop", func(o *Options) { o.linkCut = func(context.Context) {} })
	client := w.newNode(w.hub.User, "laptop")
	host.setUp()
	s := client.mustOpen(host)
	w.hub.DisableUser("alice")
	waitConnection(t, host, hublink.Disabled)
	_, err := host.remote.Use(host.ctx, s.ID, client.ref)
	wantCode(t, err, CodeSessionClosed)
	if e, _ := asError(err); ReasonOf(e) != ReasonCutOff {
		t.Errorf("Use refused %v, want the reason cut_off", e.Details)
	}
}

// A session closed on the client, which the host has not heard of, cuts
// the client's streams of it.
func TestClosedCutsClientStreams(t *testing.T) {
	t.Parallel()
	_, _, client, s, conn, served := echoed(t)
	if _, err := client.remote.closeWhere(client.ctx, ReasonClosed, "id = ?", s.ID); err != nil {
		t.Fatal(err)
	}
	wantCut(t, conn, served)
}
