package remote

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"database/sql"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/cpace"

	"github.com/srjn45/wdx/internal/authcode"
	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/db/dbtest"
	"github.com/srjn45/wdx/internal/hub/hubtest"
	"github.com/srjn45/wdx/internal/hublink"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/nodeaudit"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/waittest"
)

func TestMain(m *testing.M) {
	os.Exit(runtest.Main(m))
}

// clock is the nodes' time, which moves only when a test advances it; its
// waits end then.
type clock struct {
	mu    sync.Mutex
	now   time.Time
	waits []clockWait
	// changed is closed at each wait added.
	changed chan struct{}
}

type clockWait struct {
	at time.Time
	ch chan time.Time
}

// newClock starts a clock 10 seconds into a 30-second step, so that a test
// moving it a few seconds stays in the step.
func newClock() *clock {
	return &clock{now: time.Unix(authcode.Step(time.Now())*30+10, 0), changed: make(chan struct{})}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
		return ch
	}
	c.waits = append(c.waits, clockWait{at: c.now.Add(d), ch: ch})
	close(c.changed)
	c.changed = make(chan struct{})
	return ch
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.waits[:0]
	for _, w := range c.waits {
		if w.at.After(c.now) {
			kept = append(kept, w)
		} else {
			w.ch <- c.now
		}
	}
	c.waits = kept
}

// offset is a clock some steps away from another, as a machine's whose
// clock is wrong.
type offset struct {
	*clock
	by time.Duration
}

func (o offset) Now() time.Time { return o.clock.Now().Add(o.by) }

// world is a hub and the nodes of a test, with one log, which no line may
// hold a secret of.
type world struct {
	t     *testing.T
	hub   *hubtest.Hub
	clock *clock

	mu      sync.Mutex
	log     bytes.Buffer
	secrets []string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, hub: hubtest.Start(t), clock: newClock()}
	t.Cleanup(w.wantNoSecrets)
	return w
}

func (w *world) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.log.Write(b)
}

// secret names values no log line may hold.
func (w *world) secret(values ...string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.secrets = append(w.secrets, values...)
	w.hub.Secret(values...)
}

func (w *world) wantNoSecrets() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for line := range strings.SplitSeq(w.log.String(), "\n") {
		for _, s := range w.secrets {
			if s != "" && strings.Contains(line, s) {
				w.t.Errorf("a secret, %q, logged: %s", s, line)
			}
		}
	}
}

// logged is the lines logged with msg.
func (w *world) logged(msg string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Count(w.log.String(), `"msg":"`+msg+`"`)
}

// node is a machine of the test: its home, database, link and Remote, which
// answers the streams the link is asked for.
type node struct {
	t      *testing.T
	w      *world
	ref    hublink.NodeRef
	hn     hubtest.Node // as the hub knows it
	home   string
	db     *sql.DB
	link   *hublink.Link
	files  *authcode.Files
	opts   Options
	remote *Remote
	ctx    context.Context

	access  atomic.Bool
	timeout atomic.Int64
	// secret is the code's secret, once set up.
	secret string
	serve  sync.WaitGroup
}

// newNode logs a machine named name in as a node of u, with remote access
// on and an idle timeout of an hour.
func (w *world) newNode(u hubtest.User, name string, adjust ...func(*Options)) *node {
	t := w.t
	t.Helper()
	home := t.TempDir()
	hn := w.hub.LogIn(home, u, name)
	path := filepath.Join(home, "wdx.db")
	dbtest.Seed(t, path)
	ctx, cancel := context.WithCancel(logging.NewContext(context.Background(), logging.New(w, slog.LevelDebug).With("node", name)))
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	link, err := hublink.Open(ctx, home, hublink.Options{MinBackoff: time.Millisecond, MaxBackoff: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	n := &node{t: t, w: w, ref: hublink.NodeRef{ID: hn.ID, Name: name}, hn: hn, home: home, db: d, link: link, files: authcode.Open(identity.HubDir(home)), ctx: ctx}
	n.access.Store(true)
	n.timeout.Store(int64(time.Hour))
	n.opts = Options{DB: d, Link: link, Code: n.files, Clock: w.clock,
		RemoteAccess: n.access.Load, IdleTimeout: func() time.Duration { return time.Duration(n.timeout.Load()) }}
	for _, a := range adjust {
		a(&n.opts)
	}
	n.start()
	t.Cleanup(func() {
		cancel()
		n.remote.Close()
		_ = link.Close()
		n.serve.Wait()
		d.Close()
	})
	n.connected()
	go func() {
		for {
			select {
			case s := <-link.Streams():
				n.serve.Add(1)
				go func() {
					defer n.serve.Done()
					n.remote.Serve(ctx, s)
				}()
			case <-ctx.Done():
				return
			}
		}
	}()
	return n
}

// start makes the node's Remote, as a daemon starting does.
func (n *node) start() {
	n.t.Helper()
	r, err := New(n.ctx, n.opts)
	if err != nil {
		n.t.Fatal(err)
	}
	n.remote = r
}

// restart is the daemon stopping and starting again.
func (n *node) restart() {
	n.t.Helper()
	n.remote.Close()
	n.start()
}

// connected waits for the node's control connection.
func (n *node) connected() {
	n.t.Helper()
	timeout := waittest.After(n.t)
	for {
		s, changed := n.link.Watch()
		if s.Connection == hublink.Connected {
			return
		}
		select {
		case <-changed:
		case <-timeout:
			n.t.Fatalf("%s not connected: %+v", n.ref.Name, s)
		}
	}
}

// setUp sets up a code on the node, its last step accepted well before now.
func (n *node) setUp() {
	n.t.Helper()
	p, err := authcode.Generate("wdx", "alice@"+n.ref.Name)
	if err != nil {
		n.t.Fatal(err)
	}
	n.w.secret(p.Secret, p.URL)
	if _, err := n.files.Write(n.ctx, p.Secret, authcode.Step(n.w.clock.Now())-10); err != nil {
		n.t.Fatal(err)
	}
	n.secret = p.Secret
}

// code is the node's code at the step now is in, and off steps from it.
func (n *node) code(off int64) string {
	n.t.Helper()
	c, err := authcode.CodeAt(n.secret, authcode.Step(n.w.clock.Now())+off)
	if err != nil {
		n.t.Fatal(err)
	}
	n.w.secret(c)
	return c
}

// wrongCode is a code of no step accepted now.
func (n *node) wrongCode() string {
	right := map[string]bool{n.code(-1): true, n.code(0): true, n.code(1): true}
	for _, c := range []string{"000000", "111111", "222222", "333333"} {
		if !right[c] {
			return c
		}
	}
	panic("four codes taken")
}

// open opens a session from n into host with code.
func (n *node) open(host *node, code string) (Session, bool, error) {
	return n.remote.Open(n.ctx, host.ref, code)
}

// mustOpen opens a session into host with its code now.
func (n *node) mustOpen(host *node) Session {
	n.t.Helper()
	s, opened, err := n.open(host, host.code(0))
	if err != nil || !opened {
		n.t.Fatalf("open: %v, opened %v", err, opened)
	}
	return s
}

func wantCode(t *testing.T, err error, want Code) {
	t.Helper()
	e, ok := asError(err)
	if !ok || e.Code != want {
		t.Fatalf("got %v, want %s", err, want)
	}
}

// sessions is the node's open sessions, of direction d.
func (n *node) sessions(d Direction) []Session {
	n.t.Helper()
	list, err := n.remote.Sessions(n.ctx)
	if err != nil {
		n.t.Fatal(err)
	}
	var of []Session
	for _, s := range list {
		if s.Direction == d {
			of = append(of, s)
		}
	}
	return of
}

// closedReason is why the node's session id closed, "" while open.
func (n *node) closedReason(id string) Reason {
	n.t.Helper()
	var reason sql.NullString
	if err := n.db.QueryRowContext(n.ctx, "SELECT closed_reason FROM hub_sessions WHERE id = ?", id).Scan(&reason); err != nil {
		n.t.Fatal(err)
	}
	return Reason(reason.String)
}

// audited is the actions of the node's audit chain, in order, which must
// verify.
func (n *node) audited() []string {
	n.t.Helper()
	v, err := nodeaudit.Verify(n.ctx, n.db)
	if err != nil || v.Broken != nil {
		n.t.Fatalf("audit chain: %v %+v", err, v.Broken)
	}
	rows, err := n.db.QueryContext(n.ctx, "SELECT action FROM hub_audit ORDER BY id")
	if err != nil {
		n.t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			n.t.Fatal(err)
		}
		actions = append(actions, a)
	}
	return actions
}

// stream opens a stream of the node's session into host.
func (n *node) stream(host *node) error {
	conn, _, err := n.remote.Stream(n.ctx, host.ref.ID)
	if err == nil {
		conn.Close()
	}
	return err
}

func TestSessionOpened(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop")
	laptop := w.newNode(w.hub.User, "laptop")
	desktop.setUp()

	s := laptop.mustOpen(desktop)
	if s.Direction != Outgoing || s.Node != desktop.ref || s.IdleExpiresAt != s.OpenedAt.Add(time.Hour) {
		t.Fatalf("session %+v", s)
	}
	in := desktop.sessions(Incoming)
	if len(in) != 1 || in[0].ID != s.ID || in[0].Node != laptop.ref {
		t.Fatalf("the desktop's sessions %+v, want the laptop's", in)
	}
	for _, n := range []struct {
		at    *node
		other *node
	}{{laptop, desktop}, {desktop, laptop}} {
		known, err := n.at.remote.KeyKnown(n.at.ctx, n.other.ref.ID)
		if err != nil || !known {
			t.Errorf("%s: key of %s known %v %v", n.at.ref.Name, n.other.ref.Name, known, err)
		}
	}
	if got := desktop.audited(); len(got) != 1 || got[0] != nodeaudit.ActionSessionOpened {
		t.Errorf("the desktop's audit chain %v, want a session opened", got)
	}

	// One session per host: no code is needed, once a stream checked it.
	again, opened, err := laptop.open(desktop, "")
	if err != nil || opened || again.ID != s.ID {
		t.Fatalf("open again: %+v %v %v, want unchanged", again, opened, err)
	}

	w.clock.Advance(10 * time.Minute)
	if err := laptop.stream(desktop); err != nil {
		t.Fatal(err)
	}
	if in := desktop.sessions(Incoming); !in[0].LastUsedAt.Equal(w.clock.Now()) {
		t.Errorf("last used %v, want the stream's time %v", in[0].LastUsedAt, w.clock.Now())
	}
	incoming, outgoing, err := desktop.remote.Counts(desktop.ctx)
	if err != nil || incoming != 1 || outgoing != 0 {
		t.Errorf("the desktop's counts %d %d %v", incoming, outgoing, err)
	}
	if w.logged("session opened") != 2 || w.logged("node key pinned") != 2 {
		t.Error("want the session opened and both keys pinned logged on both nodes")
	}
}

func TestOpenRefused(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop")
	laptop := w.newNode(w.hub.User, "laptop")

	_, _, err := laptop.open(desktop, "123456")
	wantCode(t, err, CodeCodeNotSetUp)

	desktop.setUp()
	desktop.access.Store(false)
	_, _, err = laptop.open(desktop, desktop.code(0))
	wantCode(t, err, CodeRemoteAccessOff)

	if len(laptop.sessions(Outgoing)) != 0 || len(desktop.sessions(Incoming)) != 0 {
		t.Error("a session opened")
	}
	if known, _ := laptop.remote.KeyKnown(laptop.ctx, desktop.ref.ID); known {
		t.Error("a key pinned without a session")
	}
}

func TestWrongCodesSlowDown(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop")
	laptop := w.newNode(w.hub.User, "laptop")
	phone := w.newNode(w.hub.User, "phone")
	desktop.setUp()

	_, _, err := laptop.open(desktop, desktop.wrongCode())
	wantCode(t, err, CodeCodeWrong)
	// Refused unread: the right code too, from the laptop or anyone.
	_, _, err = laptop.open(desktop, desktop.code(0))
	wantCode(t, err, CodeCodeWrong)
	_, _, err = phone.open(desktop, desktop.code(0))
	wantCode(t, err, CodeCodeWrong)

	w.clock.Advance(time.Second)
	_, _, err = laptop.open(desktop, desktop.wrongCode())
	wantCode(t, err, CodeCodeWrong)
	w.clock.Advance(time.Second) // the second wrong code waits 2 seconds
	_, _, err = laptop.open(desktop, desktop.code(0))
	wantCode(t, err, CodeCodeWrong)
	w.clock.Advance(time.Second)
	laptop.mustOpen(desktop)
	// A right code ends the waits, the host's own with them; the next code
	// is the next step's, as a code opens one session.
	w.clock.Advance(authcode.Period)
	phone.mustOpen(desktop)

	if got := desktop.audited(); strings.Join(got, " ") != "code.wrong code.wrong session.opened session.opened" {
		t.Errorf("the desktop's audit chain %v", got)
	}
	if w.logged("code wrong") != 2 {
		t.Error("want each wrong code logged")
	}
}

// wantTooSoon checks n's next open into host is refused unread, as too soon
// after a wrong code.
func (n *node) wantTooSoon(host *node, why string) {
	n.t.Helper()
	_, _, err := n.open(host, host.code(0))
	wantCode(n.t, err, CodeCodeWrong)
	if !strings.Contains(err.Error(), "too soon") {
		n.t.Fatalf("%s: got %v, want too soon", why, err)
	}
}

func TestWrongCodesFromOneNode(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop")
	laptop := w.newNode(w.hub.User, "laptop")
	phone := w.newNode(w.hub.User, "phone")
	desktop.setUp()

	// The first ten wait 1 s doubling up to 5 minutes, as before.
	wait := time.Second
	for range 10 {
		_, _, err := laptop.open(desktop, desktop.wrongCode())
		wantCode(t, err, CodeCodeWrong)
		w.clock.Advance(wait)
		wait = min(2*wait, 5*time.Minute)
	}
	// The 11th's goes on doubling past them: 10 minutes.
	_, _, err := laptop.open(desktop, desktop.wrongCode())
	wantCode(t, err, CodeCodeWrong)
	w.clock.Advance(5*time.Minute + time.Second)
	laptop.wantTooSoon(desktop, "5 minutes after the 11th wrong code")
	w.clock.Advance(5 * time.Minute)
	_, _, err = laptop.open(desktop, desktop.wrongCode())
	wantCode(t, err, CodeCodeWrong)

	// The host's total stops at 5 minutes: the phone's right code passes
	// then, and leaves the laptop's own wait of 20 minutes.
	w.clock.Advance(5 * time.Minute)
	phone.mustOpen(desktop)
	w.clock.Advance(authcode.Period)
	laptop.wantTooSoon(desktop, "after another node's right code")
	w.clock.Advance(15 * time.Minute)
	laptop.mustOpen(desktop)

	if got := slicesCount(desktop.audited(), nodeaudit.ActionCodeWrong); got != 12 {
		t.Errorf("%d wrong codes in the desktop's audit chain, want 12", got)
	}
}

func TestConfirmStalls(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	var changes atomic.Int64
	desktop := w.newNode(w.hub.User, "desktop", func(o *Options) {
		o.confirmTimeout = 50 * time.Millisecond
		o.Changed = func() { changes.Add(1) }
	})
	laptop := w.newNode(w.hub.User, "laptop")
	desktop.setUp()

	// The laptop opens with the right code, and never confirms.
	conn, me, hostKey, _, err := laptop.remote.dial(laptop.ctx, desktop.ref)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ex, err := exporter(conn.ConnectionState())
	if err != nil {
		t.Fatal(err)
	}
	b := binding{exporter: ex, client: me.key, host: hostKey, step: authcode.Step(w.clock.Now())}
	msgA, _, err := cpace.Start(desktop.code(0), b.context())
	if err != nil {
		t.Fatal(err)
	}
	if err := write(conn, message{Type: msgOpen, Version: protocolVersion, Step: b.step, CPace: msgA}); err != nil {
		t.Fatal(err)
	}
	if _, err := read(conn, msgExchange); err != nil {
		t.Fatal(err)
	}
	_, err = read(conn, msgOpened)
	wantCode(t, err, CodeCodeWrong)

	// A wrong code: the next try waits, and the session streams are told.
	laptop.wantTooSoon(desktop, "after a stalled confirm")
	if changes.Load() != 1 {
		t.Errorf("Changed called %d times, want once, for the wrong code", changes.Load())
	}
	if got := desktop.audited(); len(got) != 1 || got[0] != nodeaudit.ActionCodeWrong {
		t.Errorf("the desktop's audit chain %v, want the wrong code", got)
	}
}

func TestCodeUsedOnce(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop")
	laptop := w.newNode(w.hub.User, "laptop")
	desktop.setUp()

	code := desktop.code(0)
	s, _, err := laptop.open(desktop, code)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := laptop.remote.CloseSession(laptop.ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err = laptop.open(desktop, code)
	wantCode(t, err, CodeCodeWrong)
	if got := desktop.audited(); slicesCount(got, nodeaudit.ActionCodeWrong) != 0 {
		t.Errorf("a step used is refused before CPace, and is no wrong code: %v", got)
	}
}

func slicesCount(list []string, v string) int {
	n := 0
	for _, s := range list {
		if s == v {
			n++
		}
	}
	return n
}

func TestStepOutOfRange(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop")
	// The laptop's clock is two steps ahead: the step it sends is out of
	// range, whatever the code.
	laptop := w.newNode(w.hub.User, "laptop", func(o *Options) { o.Clock = offset{w.clock, 2 * authcode.Period} })
	desktop.setUp()

	_, _, err := laptop.open(desktop, desktop.code(2))
	wantCode(t, err, CodeCodeWrong)
	if len(desktop.sessions(Incoming)) != 0 {
		t.Error("a session opened")
	}
}

func TestOtherChannelFails(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop")
	// The laptop binds CPace to another TLS session's exporter value, as a
	// hub between two TLS sessions of its own would see each side do.
	other := otherExporter(t)
	laptop := w.newNode(w.hub.User, "laptop", func(o *Options) {
		o.Exporter = func(tls.ConnectionState) ([]byte, error) { return other, nil }
	})
	desktop.setUp()

	_, _, err := laptop.open(desktop, desktop.code(0))
	wantCode(t, err, CodeCodeWrong)
	if got := desktop.audited(); len(got) != 1 || got[0] != nodeaudit.ActionCodeWrong {
		t.Errorf("the desktop's audit chain %v, want a wrong code", got)
	}
	if len(desktop.sessions(Incoming)) != 0 || len(laptop.sessions(Outgoing)) != 0 {
		t.Error("a session opened")
	}
}

// otherExporter is the exporter value of a TLS session of its own.
func otherExporter(t *testing.T) []byte {
	t.Helper()
	_, k1, _ := ed25519.GenerateKey(rand.Reader)
	_, k2, _ := ed25519.GenerateKey(rand.Reader)
	c1, err := certificate(k1)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := certificate(k2)
	if err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	client, server := tls.Client(a, config(c1, false)), tls.Server(b, config(c2, true))
	done := make(chan error, 1)
	go func() { done <- server.Handshake() }()
	if err := client.Handshake(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	defer server.Close()
	ex, err := exporter(client.ConnectionState())
	if err != nil {
		t.Fatal(err)
	}
	return ex
}

func TestPinnedKeyChanged(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop")
	laptop := w.newNode(w.hub.User, "laptop")
	desktop.setUp()
	laptop.mustOpen(desktop)

	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := laptop.db.Exec("UPDATE hub_known_nodes SET public_key = ? WHERE node_id = ?", []byte(pub), desktop.ref.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, laptop.stream(desktop), CodeNodeKeyMismatch)
	if got := laptop.audited(); len(got) != 1 || got[0] != nodeaudit.ActionNodeKeyMismatch {
		t.Errorf("the laptop's audit chain %v, want the mismatch", got)
	}

	// The host refuses a client whose key is not the one it pinned, too.
	if _, err := laptop.db.Exec("UPDATE hub_sessions SET closed_at = 1, closed_reason = 'closed'"); err != nil {
		t.Fatal(err)
	}
	if _, err := laptop.db.Exec("DELETE FROM hub_known_nodes"); err != nil {
		t.Fatal(err)
	}
	if _, err := desktop.db.Exec("UPDATE hub_known_nodes SET public_key = ? WHERE node_id = ?", []byte(pub), laptop.ref.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err := laptop.open(desktop, desktop.code(1))
	wantCode(t, err, CodeNodeKeyMismatch)
	if got := desktop.audited(); got[len(got)-1] != nodeaudit.ActionNodeKeyMismatch {
		t.Errorf("the desktop's audit chain %v, want the mismatch last", got)
	}
}

func TestIdleSessionCloses(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop")
	laptop := w.newNode(w.hub.User, "laptop")
	desktop.setUp()
	desktop.timeout.Store(int64(time.Minute))
	s := laptop.mustOpen(desktop)

	w.clock.Advance(30 * time.Second)
	if err := laptop.stream(desktop); err != nil {
		t.Fatal(err)
	}
	// Idle from the stream on: 59 seconds after it, it is open still.
	w.clock.Advance(59 * time.Second)
	if err := laptop.stream(desktop); err != nil {
		t.Fatal(err)
	}
	// At its expiry it is open still: it expires only once that passed.
	w.clock.Advance(time.Minute)
	if err := laptop.stream(desktop); err != nil {
		t.Fatal(err)
	}
	w.clock.Advance(time.Minute + time.Millisecond)
	desktop.wantClosedFor(s.ID, ReasonIdle)
	err := laptop.stream(desktop)
	wantCode(t, err, CodeSessionClosed)
	if got := laptop.closedReason(s.ID); got != ReasonIdle {
		t.Errorf("the laptop's row closed %q, want idle as the host said", got)
	}
	if got := desktop.audited(); got[len(got)-1] != nodeaudit.ActionSessionClosed {
		t.Errorf("the desktop's audit chain %v", got)
	}
}

// Claude's proof (REVIEW_COR_CLAUDE C1): a session the host closed is not
// unchanged on the client, whose row was only its guess; the open goes on
// with the code, which a wrong one fails.
func TestStaleOutgoingOpensWithCode(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop")
	laptop := w.newNode(w.hub.User, "laptop")
	desktop.setUp()
	s := laptop.mustOpen(desktop)
	desktop.restart()

	_, opened, err := laptop.open(desktop, desktop.wrongCode())
	wantCode(t, err, CodeCodeWrong)
	if opened {
		t.Error("opened with a wrong code")
	}
	if got := laptop.closedReason(s.ID); got != ReasonRestart {
		t.Errorf("the laptop's row closed %q, want restart as the host said", got)
	}
	w.clock.Advance(authcode.Period)
	again := laptop.mustOpen(desktop)
	if again.ID == s.ID || len(laptop.sessions(Outgoing)) != 1 || len(desktop.sessions(Incoming)) != 1 {
		t.Errorf("open again: %+v; the laptop has %d, the desktop %d", again, len(laptop.sessions(Outgoing)), len(desktop.sessions(Incoming)))
	}
}

func TestRestartClosesIncoming(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop")
	laptop := w.newNode(w.hub.User, "laptop")
	desktop.setUp()
	s := laptop.mustOpen(desktop)

	desktop.restart()
	if got := desktop.closedReason(s.ID); got != ReasonRestart {
		t.Fatalf("closed %q, want restart", got)
	}
	wantCode(t, laptop.stream(desktop), CodeSessionClosed)
	if got := laptop.closedReason(s.ID); got != ReasonRestart {
		t.Errorf("the laptop's row closed %q, want restart", got)
	}
	// The laptop's own restart keeps its outgoing sessions: only a host
	// closes them.
	w.clock.Advance(authcode.Period)
	s = laptop.mustOpen(desktop)
	laptop.restart()
	if got := laptop.closedReason(s.ID); got != "" {
		t.Errorf("the laptop's outgoing session closed %q at its restart", got)
	}
}

func TestRemoteAccessOffClosesIncoming(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop")
	laptop := w.newNode(w.hub.User, "laptop")
	desktop.setUp()
	s := laptop.mustOpen(desktop)

	desktop.access.Store(false)
	desktop.remote.Apply(desktop.ctx)
	if got := desktop.closedReason(s.ID); got != ReasonRemoteAccessOff {
		t.Fatalf("closed %q, want remote_access_off", got)
	}
	wantCode(t, laptop.stream(desktop), CodeSessionClosed)
	if got := laptop.closedReason(s.ID); got != ReasonRemoteAccessOff {
		t.Errorf("the laptop's row closed %q, want remote_access_off as the host said", got)
	}
	_, _, err := laptop.open(desktop, desktop.code(1))
	wantCode(t, err, CodeRemoteAccessOff)
}

// Cursor's C3: a host that cannot send opened closes the session it just
// recorded, which the client never had.
func TestOpenedNotSentCloses(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop", func(o *Options) {
		o.recording = func(conn net.Conn) { conn.Close() }
	})
	laptop := w.newNode(w.hub.User, "laptop")
	desktop.setUp()

	if _, _, err := laptop.open(desktop, desktop.code(0)); err == nil {
		t.Fatal("opened, though the host sent nothing")
	}
	desktop.serve.Wait() // the client may have given up first
	if in := desktop.sessions(Incoming); len(in) != 0 {
		t.Errorf("the desktop's sessions %+v, want none", in)
	}
	if len(laptop.sessions(Outgoing)) != 0 {
		t.Error("the laptop recorded a session")
	}
}

// A node opening a session closes its older ones on the host: one the
// client closed while the host could not be told.
func TestOpenClosesOlder(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop")
	laptop := w.newNode(w.hub.User, "laptop")
	phone := w.newNode(w.hub.User, "phone")
	desktop.setUp()
	old := laptop.mustOpen(desktop)
	w.clock.Advance(authcode.Period)
	kept := phone.mustOpen(desktop)
	// Closed on the laptop alone, as when the host could not be told.
	if _, err := laptop.remote.closeWhere(laptop.ctx, ReasonClosed, "id = ?", old.ID); err != nil {
		t.Fatal(err)
	}
	w.clock.Advance(authcode.Period)
	s := laptop.mustOpen(desktop)

	if got := desktop.closedReason(old.ID); got != ReasonClosed {
		t.Errorf("the older session closed %q, want closed", got)
	}
	in := desktop.sessions(Incoming)
	if len(in) != 2 || in[0].ID != kept.ID || in[1].ID != s.ID {
		t.Errorf("the desktop's sessions %+v, want the phone's and the laptop's new one", in)
	}
}

// held is a host whose opens wait, once remote access was checked, until
// release is closed; entered is told as each waits.
func held(t *testing.T, w *world) (desktop *node, entered chan struct{}, release chan struct{}) {
	t.Helper()
	entered, release = make(chan struct{}, 1), make(chan struct{})
	desktop = w.newNode(w.hub.User, "desktop", func(o *Options) {
		o.Exporter = func(cs tls.ConnectionState) ([]byte, error) {
			entered <- struct{}{}
			<-release
			return exporter(cs)
		}
	})
	desktop.setUp()
	return desktop, entered, release
}

// incomingRows is how many incoming sessions the node ever recorded.
func (n *node) incomingRows() int {
	n.t.Helper()
	var rows int
	if err := n.db.QueryRow("SELECT COUNT(*) FROM hub_sessions WHERE direction = 'incoming'").Scan(&rows); err != nil {
		n.t.Fatal(err)
	}
	return rows
}

// Codex's proof (REVIEW_COR_CODEX C4): an open crossing remote access
// turned off is refused, and leaves no session.
func TestOpenCrossingAccessOff(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop, entered, release := held(t, w)
	laptop := w.newNode(w.hub.User, "laptop")

	opened := make(chan error, 1)
	go func() {
		_, _, err := laptop.open(desktop, desktop.code(0))
		opened <- err
	}()
	<-entered
	desktop.access.Store(false)
	applied := make(chan struct{})
	go func() {
		defer close(applied)
		desktop.remote.Apply(desktop.ctx)
	}()
	close(release)
	err := <-opened
	<-applied
	desktop.serve.Wait()
	wantCode(t, err, CodeRemoteAccessOff)
	if e, _ := asError(err); ReasonOf(e) != ReasonRemoteAccessOff {
		t.Errorf("refused %v, want the reason remote_access_off", e.Details)
	}
	if rows := desktop.incomingRows(); rows != 0 || len(laptop.sessions(Outgoing)) != 0 {
		t.Errorf("the desktop recorded %d sessions, the laptop %d", rows, len(laptop.sessions(Outgoing)))
	}
}

// Antigravity's C10: an open crossing a cut off is refused, and leaves no
// session. It waits once the code was checked, as the hub cuts the stream.
func TestOpenCrossingCutOff(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	desktop := w.newNode(w.hub.User, "desktop", func(o *Options) {
		o.recording = func(net.Conn) {
			entered <- struct{}{}
			<-release
		}
	})
	desktop.setUp()
	laptop := w.newNode(w.hub.User, "laptop")

	opened := make(chan error, 1)
	go func() {
		_, _, err := laptop.open(desktop, desktop.code(0))
		opened <- err
	}()
	<-entered
	w.hub.DisableUser("alice")
	waitConnection(t, desktop, hublink.Disabled)
	close(release)
	if err := <-opened; err == nil {
		t.Error("opened across the cut off")
	}
	desktop.serve.Wait() // the hub cut the stream: the client gave up first
	if rows := desktop.incomingRows(); rows != 0 {
		t.Errorf("the desktop recorded %d sessions, want none", rows)
	}
}

func TestClose(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop")
	laptop := w.newNode(w.hub.User, "laptop")
	desktop.setUp()

	// Closing an outgoing session tells the host.
	s := laptop.mustOpen(desktop)
	if _, err := laptop.remote.CloseSession(laptop.ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if got := desktop.closedReason(s.ID); got != ReasonClosed {
		t.Fatalf("the host's row closed %q, want closed", got)
	}
	_, err := laptop.remote.CloseSession(laptop.ctx, s.ID)
	wantCode(t, err, CodeSessionNotFound)

	// Closing an incoming one: the client finds out at its next stream.
	w.clock.Advance(authcode.Period)
	s = laptop.mustOpen(desktop)
	closed, err := desktop.remote.CloseAll(desktop.ctx)
	if err != nil || len(closed) != 1 || closed[0].ID != s.ID {
		t.Fatalf("close all: %+v %v", closed, err)
	}
	wantCode(t, laptop.stream(desktop), CodeSessionClosed)
	if got := laptop.closedReason(s.ID); got != ReasonClosed {
		t.Errorf("the laptop's row closed %q, want closed", got)
	}
}

// Closing a session the host closed already returns it: telling the host
// closed the row, for the reason it gave (REVIEW_COR_CLAUDE C1).
func TestCloseClosedAtHost(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop")
	laptop := w.newNode(w.hub.User, "laptop")
	desktop.setUp()
	s := laptop.mustOpen(desktop)
	desktop.restart()

	closed, err := laptop.remote.CloseSession(laptop.ctx, s.ID)
	if err != nil || closed.ID != s.ID {
		t.Fatalf("close: %+v %v, want the session", closed, err)
	}
	if got := laptop.closedReason(s.ID); got != ReasonRestart {
		t.Errorf("the laptop's row closed %q, want restart as the host said", got)
	}
}

func TestLoggedOut(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop")
	laptop := w.newNode(w.hub.User, "laptop")
	desktop.setUp()
	s := laptop.mustOpen(desktop)

	for _, n := range []*node{desktop, laptop} {
		if err := n.remote.LoggedOut(n.ctx); err != nil {
			t.Fatal(err)
		}
		if got := n.closedReason(s.ID); got != ReasonLoggedOut {
			t.Errorf("%s: closed %q, want logged_out", n.ref.Name, got)
		}
		var keys int
		if err := n.db.QueryRow("SELECT COUNT(*) FROM hub_known_nodes").Scan(&keys); err != nil || keys != 0 {
			t.Errorf("%s: %d keys pinned after logout %v", n.ref.Name, keys, err)
		}
	}
}

func TestVersionUnsupported(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop")
	laptop := w.newNode(w.hub.User, "laptop")
	desktop.setUp()

	conn, _, _, _, err := laptop.remote.dial(laptop.ctx, desktop.ref)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := write(conn, message{Type: msgOpen, Version: protocolVersion + 1}); err != nil {
		t.Fatal(err)
	}
	_, err = read(conn, msgExchange)
	wantCode(t, err, CodeNodeVersionUnsupported)
}

func TestAuditChainChanged(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	desktop := w.newNode(w.hub.User, "desktop")
	laptop := w.newNode(w.hub.User, "laptop")
	desktop.setUp()
	s := laptop.mustOpen(desktop)
	if _, err := laptop.remote.CloseSession(laptop.ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if got := desktop.audited(); strings.Join(got, " ") != "session.opened session.closed" {
		t.Fatalf("the desktop's audit chain %v", got)
	}
	if _, err := desktop.db.Exec(`UPDATE hub_audit SET details = '{"reason":"idle"}' WHERE action = 'session.closed'`); err != nil {
		t.Fatal(err)
	}
	v, err := nodeaudit.Verify(desktop.ctx, desktop.db)
	if err != nil || v.Broken == nil || v.Rows != 1 {
		t.Fatalf("verify %+v %v, want the second row broken", v, err)
	}
}

// keyKnown reports whether the node has a key pinned for other.
func (n *node) keyKnown(other *node) bool {
	n.t.Helper()
	known, err := n.remote.KeyKnown(n.ctx, other.ref.ID)
	if err != nil {
		n.t.Fatal(err)
	}
	return known
}

func TestNodeGone(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	var changes atomic.Int64
	desktop := w.newNode(w.hub.User, "desktop")
	laptop := w.newNode(w.hub.User, "laptop", func(o *Options) { o.Changed = func() { changes.Add(1) } })
	phone := w.newNode(w.hub.User, "phone")
	desktop.setUp()
	laptop.setUp()
	out := laptop.mustOpen(desktop)
	w.clock.Advance(authcode.Period)
	in := desktop.mustOpen(laptop)
	w.clock.Advance(authcode.Period)
	kept := phone.mustOpen(laptop)

	before, audited := changes.Load(), len(laptop.audited())
	if err := laptop.remote.NodeGone(laptop.ctx, desktop.ref.ID); err != nil {
		t.Fatal(err)
	}
	for way, s := range map[string]Session{"outgoing": out, "incoming": in} {
		if got := laptop.closedReason(s.ID); got != ReasonNodeRemoved {
			t.Errorf("the %s session closed %q, want node_removed", way, got)
		}
	}
	if got := laptop.closedReason(kept.ID); got != "" {
		t.Errorf("the phone's session closed %q", got)
	}
	if !laptop.keyKnown(desktop) || !laptop.keyKnown(phone) {
		t.Error("want every key kept: pins are forgotten at logout only")
	}
	if got := laptop.audited(); len(got) != audited+1 || got[len(got)-1] != nodeaudit.ActionSessionClosed {
		t.Errorf("audit chain %v, want the incoming session's close after %d", got, audited)
	}
	if changes.Load() == before {
		t.Error("Changed was not called")
	}

	// Again, as a second message or the catch-up would: nothing.
	before, audited = changes.Load(), len(laptop.audited())
	if err := laptop.remote.NodeGone(laptop.ctx, desktop.ref.ID); err != nil {
		t.Fatal(err)
	}
	if err := laptop.remote.AccountNodes(laptop.ctx, []hublink.NodeRef{laptop.ref, phone.ref}); err != nil {
		t.Fatal(err)
	}
	if changes.Load() != before || len(laptop.audited()) != audited || laptop.closedReason(kept.ID) != "" {
		t.Errorf("repeated: %d changes, audit %v", changes.Load()-before, laptop.audited())
	}

	// The catch-up: the phone is not among the account's nodes any more.
	if err := laptop.remote.AccountNodes(laptop.ctx, []hublink.NodeRef{laptop.ref}); err != nil {
		t.Fatal(err)
	}
	if got := laptop.closedReason(kept.ID); got != ReasonNodeRemoved || !laptop.keyKnown(phone) {
		t.Errorf("the phone's session closed %q, key known %v; want node_removed and kept", got, laptop.keyKnown(phone))
	}

	// A hub that says the desktop is gone cannot have its key forgotten:
	// another key under its id is still a mismatch.
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := laptop.db.Exec("UPDATE hub_known_nodes SET public_key = ? WHERE node_id = ?", []byte(pub), desktop.ref.ID); err != nil {
		t.Fatal(err)
	}
	w.clock.Advance(authcode.Period)
	_, _, err := laptop.open(desktop, desktop.code(0))
	wantCode(t, err, CodeNodeKeyMismatch)
}
