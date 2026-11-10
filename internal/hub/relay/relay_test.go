package relay

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/waittest"
)

var nodes = Nodes{UserID: "usr-a", ClientID: "node-laptop", HostID: "node-desktop"}

// said is a logger that hands on the message of each line it is given, so
// that a test knows a join is waiting once it said so.
type said chan string

func (s said) Enabled(context.Context, slog.Level) bool      { return true }
func (s said) Handle(_ context.Context, r slog.Record) error { s <- r.Message; return nil }
func (s said) WithAttrs([]slog.Attr) slog.Handler            { return s }
func (s said) WithGroup(string) slog.Handler                 { return s }

// waitSaid waits until s was given msg, and fails at the deadline.
func waitSaid(t *testing.T, s said, msg string) {
	t.Helper()
	for {
		select {
		case got := <-s:
			if got == msg {
				return
			}
		case <-waittest.After(t):
			t.Fatalf("%q never logged", msg)
		}
	}
}

// newRelay is a relay whose streams expire when the test closes the channel
// returned, and never otherwise.
func newRelay(maxPerUser int) (*Relay, chan time.Time) {
	expire := make(chan time.Time)
	return New(Options{MaxPerUser: maxPerUser, After: func(time.Duration) <-chan time.Time { return expire }}), expire
}

// joined is what a Join returned.
type joined struct {
	j   *Joined
	err error
}

// join joins id as nodeID in the background, saying what it logs to s.
func join(r *Relay, ctx context.Context, s said, id, nodeID string) <-chan joined {
	done := make(chan joined, 1)
	go func() {
		j, err := r.Join(logging.NewContext(ctx, slog.New(s)), id, nodeID)
		done <- joined{j, err}
	}()
	return done
}

func waitJoined(t *testing.T, done <-chan joined) joined {
	t.Helper()
	select {
	case j := <-done:
		return j
	case <-waittest.After(t):
		t.Fatal("the join never returned")
		return joined{}
	}
}

func TestJoinBoth(t *testing.T) {
	t.Parallel()
	r, _ := newRelay(0)
	ctx := context.Background()
	id, err := r.Open(ctx, nodes)
	if err != nil {
		t.Fatal(err)
	}
	s := make(said, 16)
	client := join(r, ctx, s, id, nodes.ClientID)
	waitSaid(t, s, "stream join waiting")
	host, err := r.Join(ctx, id, nodes.HostID)
	if err != nil || host.other() != nodes.ClientID {
		t.Fatalf("the host's join: %+v, %v", host, err)
	}
	if c := waitJoined(t, client); c.err != nil || c.j.other() != nodes.HostID {
		t.Fatalf("the client's join: %+v", c)
	}
	// Joined, it is no longer waiting: neither side joins it again.
	for _, nodeID := range []string{nodes.ClientID, nodes.HostID} {
		if _, err := r.Join(ctx, id, nodeID); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s joining again: %v, want ErrNotFound", nodeID, err)
		}
	}
}

func TestJoinRefused(t *testing.T) {
	t.Parallel()
	r, _ := newRelay(0)
	ctx := context.Background()
	id, err := r.Open(ctx, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Join(ctx, "str-0000000000000000", nodes.ClientID); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown stream: %v, want ErrNotFound", err)
	}
	if _, err := r.Join(ctx, id, "node-other"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a node not of the stream: %v, want ErrNotFound", err)
	}
	s := make(said, 16)
	client := join(r, ctx, s, id, nodes.ClientID)
	waitSaid(t, s, "stream join waiting")
	if _, err := r.Join(ctx, id, nodes.ClientID); !errors.Is(err, ErrNotFound) {
		t.Errorf("the client joining twice: %v, want ErrNotFound", err)
	}
	// The first join is still the one waiting.
	if _, err := r.Join(ctx, id, nodes.HostID); err != nil {
		t.Fatal(err)
	}
	if c := waitJoined(t, client); c.err != nil {
		t.Fatalf("the client's join: %v", c.err)
	}
}

func TestJoinExpired(t *testing.T) {
	t.Parallel()
	r, expire := newRelay(1)
	ctx := context.Background()
	id, err := r.Open(ctx, nodes)
	if err != nil {
		t.Fatal(err)
	}
	s := make(said, 16)
	client := join(r, ctx, s, id, nodes.ClientID)
	waitSaid(t, s, "stream join waiting")
	close(expire)
	if c := waitJoined(t, client); !errors.Is(c.err, ErrPeerAbsent) {
		t.Fatalf("the client's join: %v, want ErrPeerAbsent", c.err)
	}
	if _, err := r.Join(ctx, id, nodes.HostID); !errors.Is(err, ErrNotFound) {
		t.Errorf("the host joining an expired stream: %v, want ErrNotFound", err)
	}
	// The user's stream is counted out: another can be asked for.
	if _, err := r.Open(ctx, nodes); err != nil {
		t.Errorf("a stream after the one expired: %v", err)
	}
}

// A join whose request ends before the other node came gives the stream up:
// the other node finds it gone, and the user's count is freed.
func TestJoinGivenUp(t *testing.T) {
	t.Parallel()
	r, _ := newRelay(1)
	ctx := context.Background()
	id, err := r.Open(ctx, nodes)
	if err != nil {
		t.Fatal(err)
	}
	ended, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.Join(ended, id, nodes.ClientID); !errors.Is(err, context.Canceled) {
		t.Fatalf("a join whose context ended: %v, want context.Canceled", err)
	}
	if _, err := r.Join(ctx, id, nodes.HostID); !errors.Is(err, ErrNotFound) {
		t.Errorf("the host joining a stream given up: %v, want ErrNotFound", err)
	}
	if _, err := r.Open(ctx, nodes); err != nil {
		t.Errorf("a stream after the one given up: %v", err)
	}
}

// A join waiting when the other node's join gives up fails as the stream
// did.
func TestJoinOtherGivenUp(t *testing.T) {
	t.Parallel()
	r, _ := newRelay(0)
	ctx := context.Background()
	id, err := r.Open(ctx, nodes)
	if err != nil {
		t.Fatal(err)
	}
	waiting, cancel := context.WithCancel(ctx)
	s := make(said, 16)
	client := join(r, waiting, s, id, nodes.ClientID)
	waitSaid(t, s, "stream join waiting")
	cancel()
	if c := waitJoined(t, client); !errors.Is(c.err, context.Canceled) {
		t.Fatalf("the client's join: %v, want context.Canceled", c.err)
	}
	if _, err := r.Join(ctx, id, nodes.HostID); !errors.Is(err, ErrNotFound) {
		t.Errorf("the host joining: %v, want ErrNotFound", err)
	}
}

func TestTooMany(t *testing.T) {
	t.Parallel()
	r, _ := newRelay(2)
	ctx := context.Background()
	for range 2 {
		if _, err := r.Open(ctx, nodes); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Open(ctx, nodes); !errors.Is(err, ErrTooMany) {
		t.Fatalf("a third stream: %v, want ErrTooMany", err)
	}
	other := Nodes{UserID: "usr-b", ClientID: "node-b1", HostID: "node-b2"}
	if _, err := r.Open(ctx, other); err != nil {
		t.Errorf("another user's stream: %v", err)
	}
}

// Cutting a user or a node fails the joins of their streams waiting.
func TestCutWaiting(t *testing.T) {
	t.Parallel()
	for name, cut := range map[string]func(*Relay){
		"user":        func(r *Relay) { r.CutUser(context.Background(), nodes.UserID) },
		"client node": func(r *Relay) { r.CutNode(context.Background(), nodes.ClientID) },
		"host node":   func(r *Relay) { r.CutNode(context.Background(), nodes.HostID) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r, _ := newRelay(1)
			ctx := context.Background()
			id, err := r.Open(ctx, nodes)
			if err != nil {
				t.Fatal(err)
			}
			s := make(said, 16)
			client := join(r, ctx, s, id, nodes.ClientID)
			waitSaid(t, s, "stream join waiting")
			cut(r)
			if c := waitJoined(t, client); !errors.Is(c.err, ErrNotFound) {
				t.Fatalf("the client's join: %v, want ErrNotFound", c.err)
			}
			if _, err := r.Open(ctx, nodes); err != nil {
				t.Errorf("a stream after the one cut: %v", err)
			}
		})
	}
	// Another user's stream is left alone.
	r, _ := newRelay(0)
	id, err := r.Open(context.Background(), nodes)
	if err != nil {
		t.Fatal(err)
	}
	r.CutUser(context.Background(), "usr-b")
	r.CutNode(context.Background(), "node-b1")
	s := make(said, 16)
	client := join(r, context.Background(), s, id, nodes.ClientID)
	if _, err := r.Join(context.Background(), id, nodes.HostID); err != nil {
		t.Fatalf("a stream of another user's, after a cut: %v", err)
	}
	if c := waitJoined(t, client); c.err != nil {
		t.Fatal(c.err)
	}
}
