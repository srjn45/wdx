package server

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/waittest"
)

func TestQueueFull(t *testing.T) {
	t.Parallel()
	out, send := newQueue(2)
	if !send("one") || !send("two") {
		t.Fatal("want the queue to take two")
	}
	if send("three") {
		t.Fatal("want a third refused while two wait")
	}
	if got := <-out; got != "one" {
		t.Fatalf("got %v first, want one", got)
	}
	if !send("three") {
		t.Fatal("want a third taken once one was sent")
	}
	if got, next := <-out, <-out; got != "two" || next != "three" {
		t.Fatalf("got %v and %v, want two and three", got, next)
	}
}

// TestRequestsCutShort checks a request still running at ShutdownTimeout is
// cut short when the hub stops: its connection closed, the change it makes
// cancelled, and the stop an error.
func TestRequestsCutShort(t *testing.T) {
	t.Parallel()
	var entered, release chan struct{}
	th := start(t, func(o *Options) {
		o.Reload, entered, release = heldReload(o.Config)
		// The one limit the test is about, which it waits out.
		o.ShutdownTimeout = 50 * time.Millisecond
	})
	reloaded := th.send(http.MethodPost, "/v1/reload", "", "dbg-0000000000c1")
	waitEntered(t, entered)
	stop := th.stop
	stopped := make(chan error, 1)
	go func() { stopped <- stop() }()
	waittest.For(t, "the requests cut short", func() bool { return logged(t, th.home, "requests cut short", "") })
	// The reload is held until the requests are cut short, then let go so
	// that the hub can end.
	close(release)
	if a := <-reloaded; a.err == nil {
		t.Errorf("want the reload's connection closed, got %d %s", a.status, a.body)
	}
	if err := <-stopped; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the hub stopped with %v, want the deadline exceeded", err)
	}
	if !logged(t, th.home, "change cancelled and ended", "") {
		t.Error("want the change cancelled and ended")
	}
	th.stop = func() error { return nil } // stopped, with the error wanted
}
