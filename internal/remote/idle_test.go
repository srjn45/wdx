package remote

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/waittest"
)

// suspend is the machine sleeping for d: the wall clock moves on, and the
// waits under way, which Go's timers do not count it in, end d later.
func (c *clock) suspend(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for i := range c.waits {
		c.waits[i].at = c.waits[i].at.Add(d)
	}
}

// waitingUntil waits for a wait that ends at at.
func (c *clock) waitingUntil(t *testing.T, at time.Time) {
	t.Helper()
	timeout := waittest.After(t)
	for {
		c.mu.Lock()
		changed := c.changed
		for _, w := range c.waits {
			if w.at.Equal(at) {
				c.mu.Unlock()
				return
			}
		}
		var waits []time.Time
		for _, w := range c.waits {
			waits = append(waits, w.at)
		}
		c.mu.Unlock()
		select {
		case <-changed:
		case <-timeout:
			t.Fatalf("no wait until %v; waits until %v", at, waits)
		}
	}
}

// lastUsed is when the node's session id was last used, as its row says.
func (n *node) lastUsed(id string) time.Time {
	n.t.Helper()
	var at int64
	if err := n.db.QueryRowContext(n.ctx, "SELECT last_used_at FROM hub_sessions WHERE id = ?", id).Scan(&at); err != nil {
		n.t.Fatal(err)
	}
	return time.UnixMilli(at)
}

// idleIn is a host with the shortest idle timeout, a minute, whose streams
// serve serves, and a client with a session and a stream open into it.
func idleIn(t *testing.T, serve func(net.Conn)) (w *world, host, client *node, s Session, conn net.Conn) {
	t.Helper()
	w = newWorld(t)
	host = w.newNode(w.hub.User, "desktop", func(o *Options) {
		o.ServeStream = func(_ context.Context, c net.Conn, _ Session) { serve(c) }
	})
	host.timeout.Store(int64(time.Minute))
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
	return w, host, client, s, conn
}

// wantUsedNow checks both rows of s say it was used now, and are open.
func wantUsedNow(t *testing.T, w *world, host, client *node, s Session) {
	t.Helper()
	now := w.clock.Now()
	for _, n := range []*node{host, client} {
		if got := n.closedReason(s.ID); got != "" {
			t.Fatalf("%s's row closed %q, want it open", n.ref.Name, got)
		}
		if got := n.lastUsed(s.ID); !got.Equal(now) {
			t.Errorf("%s's row last used %v, want %v", n.ref.Name, got, now)
		}
	}
}

// An attach typed into every 30 seconds, at the shortest idle timeout,
// stays open, and the client's row moves with the host's. The first bytes
// count, however soon after the stream opened.
func TestAttachInUseStaysOpen(t *testing.T) {
	t.Parallel()
	w, host, client, s, conn := idleIn(t, func(c net.Conn) { _, _ = io.Copy(c, c) })
	for _, d := range []time.Duration{10 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second} {
		w.clock.Advance(d)
		if _, err := conn.Write([]byte("k")); err != nil {
			t.Fatal(err)
		}
		// Echoed, so the host has read it.
		if _, err := io.ReadFull(conn, make([]byte, 1)); err != nil {
			t.Fatal(err)
		}
		wantUsedNow(t, w, host, client, s)
	}
}

// A stream that only receives, as a TIDE following its project's events,
// keeps the session open past the idle timeout.
func TestReceivingStreamStaysOpen(t *testing.T) {
	t.Parallel()
	send := make(chan struct{})
	w, host, client, s, conn := idleIn(t, func(c net.Conn) {
		for range send {
			if _, err := c.Write([]byte("e")); err != nil {
				return
			}
		}
	})
	defer close(send)
	for range 5 {
		w.clock.Advance(30 * time.Second)
		send <- struct{}{}
		// Read, so the host has moved it on before it wrote.
		if _, err := io.ReadFull(conn, make([]byte, 1)); err != nil {
			t.Fatal(err)
		}
		wantUsedNow(t, w, host, client, s)
	}
}

// A session that expired while the host was suspended is closed within a
// minute of it waking, though the timer did not count the sleep.
func TestIdleAfterSuspend(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop")
	laptop := w.newNode(w.hub.User, "laptop")
	desktop.setUp()
	s := laptop.mustOpen(desktop)
	w.clock.waitingUntil(t, w.clock.Now().Add(time.Minute))

	w.clock.suspend(2 * time.Hour)
	w.clock.Advance(time.Minute)
	desktop.wantClosedFor(s.ID, ReasonIdle)
}

// The idle timer tries again after an error, rather than stopping.
func TestIdleTimerRetries(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop")
	laptop := w.newNode(w.hub.User, "laptop")
	desktop.setUp()
	desktop.timeout.Store(int64(time.Minute))
	s := laptop.mustOpen(desktop)
	w.clock.waitingUntil(t, s.OpenedAt.Add(time.Minute))

	// The timer looks with the sessions' table away, and fails.
	if _, err := desktop.db.ExecContext(desktop.ctx, "ALTER TABLE hub_sessions RENAME TO hub_sessions_away"); err != nil {
		t.Fatal(err)
	}
	w.clock.Advance(time.Minute)
	w.clock.waitingUntil(t, s.OpenedAt.Add(time.Minute+time.Second))
	if _, err := desktop.db.ExecContext(desktop.ctx, "ALTER TABLE hub_sessions_away RENAME TO hub_sessions"); err != nil {
		t.Fatal(err)
	}
	w.clock.Advance(time.Second)
	desktop.wantClosedFor(s.ID, ReasonIdle)
}
