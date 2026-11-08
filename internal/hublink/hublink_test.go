package hublink

import (
	"bufio"
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/hub/hubtest"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/waittest"
)

func TestMain(m *testing.M) {
	os.Exit(runtest.Main(m))
}

// fakeClock is the hub's clock, whose waits end only when a test moves it
// on: so a test knows when the link polls or expires.
type fakeClock struct {
	hub     *hubtest.Clock
	mu      sync.Mutex
	waiters []*waiter
	changed chan struct{}
}

type waiter struct {
	at time.Time
	d  time.Duration
	ch chan time.Time
}

func newFakeClock(h *hubtest.Hub) *fakeClock {
	return &fakeClock{hub: h.Clock, changed: make(chan struct{})}
}

func (c *fakeClock) Now() time.Time { return c.hub.Now() }

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.Now()
		return ch
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waiters = append(c.waiters, &waiter{at: c.Now().Add(d), d: d, ch: ch})
	close(c.changed)
	c.changed = make(chan struct{})
	return ch
}

// fire ends the waits for which due is true.
func (c *fakeClock) fire(due func(*waiter) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if due(w) {
			w.ch <- c.Now()
		} else {
			kept = append(kept, w)
		}
	}
	c.waiters = kept
}

// Advance moves the hub's time on by d, ending the waits it passes.
func (c *fakeClock) Advance(d time.Duration) {
	c.hub.Advance(d)
	now := c.Now()
	c.fire(func(w *waiter) bool { return !w.at.After(now) })
}

// waiting waits for a wait of d to be pending.
func (c *fakeClock) waiting(t *testing.T, d time.Duration) {
	t.Helper()
	c.waitingFor(t, d.String(), func(w time.Duration) bool { return w == d })
}

// waitingFor waits for a wait that ok accepts to be pending.
func (c *fakeClock) waitingFor(t *testing.T, what string, ok func(time.Duration) bool) {
	t.Helper()
	timeout := waittest.After(t)
	for {
		c.mu.Lock()
		var seen []time.Duration
		for _, w := range c.waiters {
			if ok(w.d) {
				c.mu.Unlock()
				return
			}
			seen = append(seen, w.d)
		}
		changed := c.changed
		c.mu.Unlock()
		select {
		case <-changed:
		case <-timeout:
			t.Fatalf("no wait of %s; waits pending: %v", what, seen)
		}
	}
}

// node is a machine in the test: its home and its link to the hub.
type node struct {
	t     *testing.T
	h     *hubtest.Hub
	home  string
	log   string
	clock Clock
	opts  Options
	link  *Link
	ctx   context.Context // carries the node's logger, as a request's does

	mu      sync.Mutex
	secrets []string
}

// newNode is a machine whose link to h is open, with the backoff short, a
// clock of clock unless nil, and its log in its home.
func newNode(t *testing.T, h *hubtest.Hub, clock Clock) *node {
	t.Helper()
	n := &node{t: t, h: h, home: t.TempDir(), clock: clock}
	n.log = filepath.Join(n.home, "hublink.log")
	n.opts = Options{Clock: clock, MinBackoff: time.Millisecond, MaxBackoff: 10 * time.Millisecond}
	n.open()
	t.Cleanup(n.wantNoSecrets)
	return n
}

// open opens the node's link, closed when the test ends.
func (n *node) open() {
	t := n.t
	t.Helper()
	f, err := os.OpenFile(n.log, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	ctx := logging.NewContext(context.Background(), logging.New(f, slog.LevelDebug))
	n.ctx = ctx
	l, err := Open(ctx, n.home, n.opts)
	if err != nil {
		t.Fatal(err)
	}
	n.link = l
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Error(err)
		}
		f.Close()
	})
}

// secret names values no line of the node's log, nor the hub's, may hold.
func (n *node) secret(values ...string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, v := range values {
		if v != "" {
			n.secrets = append(n.secrets, v, strings.ReplaceAll(v, "-", ""))
		}
	}
	n.h.Secret(values...)
}

// secretsOf names the secrets of g: its key, verifier and codes.
func (n *node) secretsOf(g *Login) {
	key, err := keyPEM(g.key)
	if err != nil {
		n.t.Fatal(err)
	}
	pemLines := strings.Split(strings.TrimSpace(string(key)), "\n")
	n.secret(g.verifier, g.device, g.UserCode, pemLines[1])
}

func (n *node) wantNoSecrets() {
	t := n.t
	n.mu.Lock()
	secrets := n.secrets
	n.mu.Unlock()
	b, err := os.ReadFile(n.log)
	if err != nil {
		t.Fatal(err)
	}
	s := bufio.NewScanner(bytes.NewReader(b))
	s.Buffer(nil, 1<<20)
	for s.Scan() {
		for _, secret := range secrets {
			if bytes.Contains(s.Bytes(), []byte(secret)) {
				t.Errorf("node: a secret, %q, logged: %s", secret, s.Bytes())
			}
		}
	}
}

// logged returns the node's log lines whose msg is msg.
func (n *node) logged(msg string) []map[string]any {
	n.t.Helper()
	b, err := os.ReadFile(n.log)
	if err != nil {
		n.t.Fatal(err)
	}
	var lines []map[string]any
	for line := range bytes.Lines(b) {
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			n.t.Fatalf("%s: %v", line, err)
		}
		if m["msg"] == msg {
			lines = append(lines, m)
		}
	}
	return lines
}

// waitStatus waits for the link's status to be one ok accepts.
func (n *node) waitStatus(what string, ok func(Status) bool) Status {
	n.t.Helper()
	timeout := waittest.After(n.t)
	for {
		s, changed := n.link.Watch()
		if ok(s) {
			return s
		}
		select {
		case <-changed:
		case <-timeout:
			n.t.Fatalf("waiting for %s: the status is %+v", what, s)
		}
	}
}

func (n *node) waitConnection(c Connection) Status {
	n.t.Helper()
	return n.waitStatus(string(c), func(s Status) bool { return s.Connection == c })
}

// waitLogin waits for g to end.
func waitLogin(t *testing.T, g *Login) LoginState {
	t.Helper()
	timeout := waittest.After(t)
	for {
		s, changed := g.Watch()
		if s.Phase != LoginPending {
			return s
		}
		select {
		case <-changed:
		case <-timeout:
			t.Fatalf("the login is still pending")
		}
	}
}

func wantProblem(t *testing.T, s LoginState, code ProblemCode) {
	t.Helper()
	if s.Phase != LoginFailed || s.Problem == nil || s.Problem.Code != code {
		t.Fatalf("login %+v, want it failed with %s", s, code)
	}
}

const callback = "http://127.0.0.1:43210/callback"

// startBrowser starts a browser login of n as name.
func (n *node) startBrowser(name string) *Login {
	n.t.Helper()
	g, err := n.link.StartLogin(n.ctx, LoginRequest{URL: n.h.URL, Name: name, Flow: FlowBrowser, Callback: callback})
	if err != nil {
		n.t.Fatal(err)
	}
	n.secretsOf(g)
	return g
}

// startDevice starts a device login of n as name.
func (n *node) startDevice(name string) *Login {
	n.t.Helper()
	g, err := n.link.StartLogin(n.ctx, LoginRequest{URL: n.h.URL, Name: name, Flow: FlowDevice})
	if err != nil {
		n.t.Fatal(err)
	}
	n.secretsOf(g)
	return g
}

// approve approves the browser login g in a browser of the hub's user and
// brings the callback's code to it, as the CLI does.
func (n *node) approve(g *Login, name string) {
	n.t.Helper()
	back, err := n.h.Browser(n.h.User).Approve(g.LoginURL, name)
	if err != nil {
		n.t.Fatal(err)
	}
	q := back.Query()
	n.secret(q.Get("code"))
	if err := g.Callback(n.ctx, Callback{Code: q.Get("code"), State: q.Get("state")}); err != nil {
		n.t.Fatal(err)
	}
}

// loggedIn logs n in as name by the browser flow and waits for it to be
// connected.
func (n *node) loggedIn(name string) Status {
	n.t.Helper()
	g := n.startBrowser(name)
	n.approve(g, "")
	if s := waitLogin(n.t, g); s.Phase != LoginLoggedIn {
		n.t.Fatalf("login %+v", s)
	}
	return n.waitConnection(Connected)
}

func TestBrowserLogin(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	n := newNode(t, h, nil)
	g := n.startBrowser("desktop")
	if !strings.HasPrefix(g.LoginURL, hubtest.PublicURL+"/nodes/approve?login=") || g.UserCode != "" {
		t.Fatalf("login %+v", g)
	}
	if n.link.Pending() != g {
		t.Fatal("want the login pending")
	}
	n.approve(g, "")
	s := waitLogin(t, g)
	if s.Phase != LoginLoggedIn || s.Status.User != "alice" || s.Status.Node.Name != "desktop" || !strings.HasPrefix(s.Status.Node.ID, "node-") || s.Status.URL != h.URL {
		t.Fatalf("login %+v", s)
	}
	if n.link.Pending() != nil {
		t.Fatal("want no login pending once it ended")
	}
	st := n.waitConnection(Connected)
	if st.HubVersion != identity.Version || st.InRange == nil || !*st.InRange {
		t.Fatalf("status %+v", st)
	}
	if lines := n.logged("control connected"); len(lines) != 1 {
		t.Fatalf("want control connected logged once, got %v", lines)
	}
	if err := g.Callback(n.ctx, Callback{Code: "again", State: g.id}); !errors.Is(err, ErrLoginEnded) {
		t.Fatalf("a second callback: %v", err)
	}
}

func TestBrowserLoginWrongState(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	n := newNode(t, h, nil)
	g := n.startBrowser("desktop")
	back, err := h.Browser(h.User).Approve(g.LoginURL, "")
	if err != nil {
		t.Fatal(err)
	}
	code := back.Query().Get("code")
	n.secret(code)
	if err := g.Callback(n.ctx, Callback{Code: code, State: "nl-someone-else"}); !errors.Is(err, ErrWrongState) {
		t.Fatalf("want ErrWrongState, got %v", err)
	}
	if s := g.State(); s.Phase != LoginPending {
		t.Fatalf("want it still pending, got %+v", s)
	}
	if err := g.Callback(n.ctx, Callback{Code: code, State: back.Query().Get("state")}); err != nil {
		t.Fatal(err)
	}
	if s := waitLogin(t, g); s.Phase != LoginLoggedIn {
		t.Fatalf("login %+v", s)
	}
}

func TestBrowserLoginDenied(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	n := newNode(t, h, nil)
	g := n.startBrowser("desktop")
	back, err := h.Browser(h.User).Deny(g.LoginURL)
	if err != nil {
		t.Fatal(err)
	}
	q := back.Query()
	if err := g.Callback(n.ctx, Callback{Error: q.Get("error"), State: q.Get("state")}); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, waitLogin(t, g), ProblemDenied)
	if s := n.link.Status(); s.Connection != LoggedOut {
		t.Fatalf("status %+v", s)
	}
}

func TestBrowserLoginExpired(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	clock := newFakeClock(h)
	n := newNode(t, h, clock)
	g := n.startBrowser("desktop")
	clock.waitingFor(t, "the login's life", func(d time.Duration) bool { return d > 9*time.Minute })
	clock.Advance(g.ExpiresAt.Sub(clock.Now()))
	wantProblem(t, waitLogin(t, g), ProblemExpired)
}

func TestDeviceLogin(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	clock := newFakeClock(h)
	n := newNode(t, h, clock)
	g := n.startDevice("desktop")
	if g.LoginURL != "" || !strings.HasPrefix(g.UserCode, "WDX-") || g.VerificationURL != hubtest.PublicURL+"/device" {
		t.Fatalf("login %+v", g)
	}
	// Polled before it is approved: pending.
	clock.waiting(t, 5*time.Second)
	clock.Advance(5 * time.Second)
	clock.waiting(t, 5*time.Second)
	if err := h.Browser(h.User).ApproveCode(g.UserCode, ""); err != nil {
		t.Fatal(err)
	}
	clock.Advance(5 * time.Second)
	s := waitLogin(t, g)
	if s.Phase != LoginLoggedIn || s.Status.Node.Name != "desktop" {
		t.Fatalf("login %+v", s)
	}
	n.waitConnection(Connected)
}

func TestDeviceLoginSlowDown(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	clock := newFakeClock(h)
	n := newNode(t, h, clock)
	g := n.startDevice("desktop")
	clock.waiting(t, 5*time.Second)
	clock.Advance(5 * time.Second)
	clock.waiting(t, 5*time.Second)
	// Polled again before the interval passed, as the hub's clock has it.
	clock.fire(func(*waiter) bool { return true })
	clock.waiting(t, 10*time.Second)
	if lines := n.logged("login polling slowed down"); len(lines) != 1 {
		t.Fatalf("want the slow down logged, got %v", lines)
	}
	g.Cancel()
	wantProblem(t, g.State(), ProblemCancelled)
}

func TestDeviceLoginDenied(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	clock := newFakeClock(h)
	n := newNode(t, h, clock)
	g := n.startDevice("desktop")
	if err := h.Browser(h.User).DenyCode(g.UserCode); err != nil {
		t.Fatal(err)
	}
	clock.waiting(t, 5*time.Second)
	clock.Advance(5 * time.Second)
	wantProblem(t, waitLogin(t, g), ProblemDenied)
}

func TestDeviceLoginExpired(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	clock := newFakeClock(h)
	n := newNode(t, h, clock)
	g := n.startDevice("desktop")
	clock.waiting(t, 5*time.Second)
	clock.Advance(g.ExpiresAt.Sub(clock.Now()))
	wantProblem(t, waitLogin(t, g), ProblemExpired)
}

func TestDeviceLoginHubUnreachable(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	clock := newFakeClock(h)
	n := newNode(t, h, clock)
	g := n.startDevice("desktop")
	h.Stop()
	clock.waiting(t, 5*time.Second)
	clock.Advance(5 * time.Second)
	// Polled again while the hub is gone, until the login expires.
	clock.waiting(t, 5*time.Second)
	clock.Advance(g.ExpiresAt.Sub(clock.Now()))
	wantProblem(t, waitLogin(t, g), ProblemUnreachable)
}

func TestStartLoginHubUnreachable(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	n := newNode(t, h, nil)
	h.Stop()
	_, err := n.link.StartLogin(n.ctx, LoginRequest{URL: h.URL, Name: "desktop", Flow: FlowDevice})
	if _, ok := errors.AsType[*UnreachableError](err); !ok {
		t.Fatalf("want an UnreachableError, got %v", err)
	}
}

func TestSecondLoginCancelsFirst(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	n := newNode(t, h, nil)
	first := n.startBrowser("desktop")
	second := n.startBrowser("desktop")
	wantProblem(t, first.State(), ProblemCancelled)
	if n.link.Pending() != second {
		t.Fatal("want the second login pending")
	}
	n.approve(second, "")
	if s := waitLogin(t, second); s.Phase != LoginLoggedIn {
		t.Fatalf("login %+v", s)
	}
	if _, err := n.link.StartLogin(n.ctx, LoginRequest{URL: h.URL, Name: "desktop", Flow: FlowDevice}); !errors.Is(err, ErrAlreadyLoggedIn) {
		t.Fatalf("want ErrAlreadyLoggedIn, got %v", err)
	}
}

func TestCheckURL(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]string{
		"https://hub.example.com":       "https://hub.example.com",
		"https://hub.example.com:8443/": "https://hub.example.com:8443",
		"http://localhost:8080":         "http://localhost:8080",
		"http://127.0.0.1:8080/":        "http://127.0.0.1:8080",
		"http://hub.example.com":        "",
		"http://192.168.1.2:8080":       "",
		"ftp://hub.example.com":         "",
		"https://hub.example.com/x":     "",
		"https://u:p@hub.example.com":   "",
		"hub.example.com":               "",
	} {
		got, err := CheckURL(raw)
		if want == "" {
			if _, ok := errors.AsType[*URLError](err); !ok {
				t.Errorf("%s: want a URLError, got %q %v", raw, got, err)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%s: got %q %v, want %q", raw, got, err, want)
		}
	}
}

func wantMode(t *testing.T, path string, want fs.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != want {
		t.Errorf("%s: mode %v, want %v", path, got, want)
	}
}

func TestFilesWritten(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	n := newNode(t, h, nil)
	st := n.loggedIn("desktop")
	dir := identity.HubDir(n.home)
	wantMode(t, dir, 0o700)
	for _, f := range []string{"login.json", "node.key", "node.crt", "hub-ca.crt"} {
		wantMode(t, filepath.Join(dir, f), 0o600)
	}
	b, err := os.ReadFile(filepath.Join(dir, "login.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lj map[string]any
	if err := json.Unmarshal(b, &lj); err != nil {
		t.Fatal(err)
	}
	if lj["url"] != h.URL || lj["nodes_url"] != h.NodesURL || lj["user"] != "alice" || lj["node"].(map[string]any)["id"] != st.Node.ID {
		t.Fatalf("login.json %s", b)
	}

	// Opened again, as the daemon is at its start: logged in, connected.
	n.link.Close()
	n.open()
	if s := n.link.Status(); s.Node != st.Node || s.User != "alice" || s.Connection == LoggedOut {
		t.Fatalf("reopened: %+v", s)
	}
	n.waitConnection(Connected)
}

func TestNoLoginJSONIsLoggedOut(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	n := newNode(t, h, nil)
	n.loggedIn("desktop")
	n.link.Close()
	// As a crash would leave it, between the files and login.json.
	dir := identity.HubDir(n.home)
	if err := os.Remove(filepath.Join(dir, "login.json")); err != nil {
		t.Fatal(err)
	}
	n.open()
	if s := n.link.Status(); s.Connection != LoggedOut || s.User != "" {
		t.Fatalf("status %+v", s)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("want the leftovers removed: %v", err)
	}
}

func TestReconnectAfterHubRestart(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	n := newNode(t, h, nil)
	n.loggedIn("desktop")
	h.Restart()
	n.waitStatus("offline after the restart", func(s Status) bool { return s.Connection != Connected })
	n.waitConnection(Connected)
	if lines := n.logged("control connected"); len(lines) < 2 {
		t.Fatalf("want connected twice, got %v", lines)
	}
}

// waitStopped waits for the control loop of n to have stopped trying.
func (n *node) waitStopped() {
	n.t.Helper()
	n.link.mu.Lock()
	control := n.link.control
	n.link.mu.Unlock()
	select {
	case <-control.done:
	case <-waittest.After(n.t):
		n.t.Fatalf("the control connection is still tried: %+v", n.link.Status())
	}
}

func TestRevokedStopsTrying(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	n := newNode(t, h, nil)
	st := n.loggedIn("desktop")
	other := h.NewNode(h.User, "laptop")
	if r, err := h.NodesClient(other).RemoveNodeWithResponse(context.Background(), "desktop"); err != nil || r.JSON200 == nil {
		t.Fatalf("remove: %v %s", err, r.Body)
	}
	n.waitConnection(Revoked)
	n.waitStopped()
	if s := n.link.Status(); s.User != "alice" || s.Node != st.Node {
		t.Fatalf("want the login kept until logout: %+v", s)
	}

	// Refused at the start, as the daemon is when it was revoked meanwhile.
	n.link.Close()
	n.open()
	n.waitConnection(Revoked)
	n.waitStopped()
}

func TestDisabledStopsTrying(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	n := newNode(t, h, nil)
	n.loggedIn("desktop")
	if r, err := h.Admin().UpdateUserWithResponse(context.Background(), "alice", hubapi.UpdateUser{Disabled: true}); err != nil || r.StatusCode() != http.StatusOK {
		t.Fatalf("disable: %v %s", err, r.Body)
	}
	n.waitConnection(Disabled)
	n.waitStopped()
}

func TestCAPinned(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	other := hubtest.Start(t)
	n := newNode(t, h, nil)
	n.loggedIn("desktop")
	n.link.Close()
	// The node endpoint of another hub, whose certificate another CA
	// issued.
	path := filepath.Join(identity.HubDir(n.home), "login.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.ReplaceAll(b, []byte(h.NodesURL), []byte(other.NodesURL)), 0o600); err != nil {
		t.Fatal(err)
	}
	before := len(n.logged("control lost"))
	n.open()
	n.waitConnection(Offline)
	lost := n.logged("control lost")
	if len(lost) <= before || !strings.Contains(lost[before]["err"].(string), "certificate signed by unknown authority") {
		t.Fatalf("want the node endpoint refused for its CA, got %v", lost[before:])
	}
	if got := other.Logged("request received"); len(got) != 0 {
		t.Fatalf("want no request reaching the other hub, got %v", got)
	}
}

func TestCertificateRenewed(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t, hubtest.RenewEvery(time.Millisecond))
	n := newNode(t, h, nil)
	n.loggedIn("desktop")
	path := filepath.Join(identity.HubDir(n.home), "node.crt")
	first := readCert(t, path)
	// Past two thirds of its life, as the hub's clock has it: renewed at the
	// hub's next check.
	h.Clock.Advance(first.NotAfter.Sub(first.NotBefore) * 3 / 4)
	n.waitStatus("the certificate renewed", func(Status) bool {
		return n.link.creds.expiresAt().After(first.NotAfter)
	})
	renewed := readCert(t, path)
	if !renewed.NotAfter.After(first.NotAfter) {
		t.Fatalf("node.crt ends %v, not after %v", renewed.NotAfter, first.NotAfter)
	}
	wantMode(t, path, 0o600)
	// Closed by the node, then the hub stopped, which waits for its
	// connections: the certificate_saved sent before the close was handled.
	n.link.Close()
	h.Stop()
	if lines := h.Logged("certificate renewed"); len(lines) == 0 {
		t.Fatalf("want the hub to record the renewal, got %v", lines)
	}
}

func readCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(b)
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLogoutOnline(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	n := newNode(t, h, nil)
	n.loggedIn("desktop")
	told, err := n.link.Logout(n.ctx)
	if err != nil || !told {
		t.Fatalf("logout: %v %v", told, err)
	}
	if s := n.link.Status(); s.Connection != LoggedOut || s.User != "" {
		t.Fatalf("status %+v", s)
	}
	if _, err := os.Stat(identity.HubDir(n.home)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("want the hub's directory deleted: %v", err)
	}
	r, err := h.NodesClient(h.NewNode(h.User, "laptop")).ListNodesWithResponse(context.Background())
	if err != nil || r.JSON200 == nil || len(r.JSON200.Nodes) != 1 {
		t.Fatalf("want the node gone from the hub: %v %s", err, r.Body)
	}
	if _, err := n.link.Logout(n.ctx); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("logout again: %v", err)
	}
}

func TestLogoutOffline(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	n := newNode(t, h, nil)
	n.loggedIn("desktop")
	h.Stop()
	told, err := n.link.Logout(n.ctx)
	if err != nil || told {
		t.Fatalf("logout: %v %v", told, err)
	}
	if _, err := os.Stat(identity.HubDir(n.home)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("want the hub's directory deleted: %v", err)
	}
	if s := n.link.Status(); s.Connection != LoggedOut {
		t.Fatalf("status %+v", s)
	}
}

func TestNodes(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	ctx := context.Background()
	n := newNode(t, h, nil)
	if _, err := n.link.Nodes(ctx); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("logged out: %v", err)
	}
	st := n.loggedIn("desktop")
	h.NewNode(h.User, "laptop")
	list, err := n.link.Nodes(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("nodes: %v %+v", err, list)
	}
	for _, nd := range list {
		if nd.This != (nd.Id == st.Node.ID) {
			t.Errorf("node %+v: this is wrong", nd)
		}
	}
	got, err := n.link.Node(ctx, "LAPTOP")
	if err != nil || got.Name != "laptop" || !strings.HasPrefix(got.Fingerprint, "SHA256:") {
		t.Fatalf("node: %v %+v", err, got)
	}
	if _, err := n.link.Node(ctx, "nowhere"); Code(err) != hubapi.ErrorCodeNotFound {
		t.Fatalf("want not_found, got %v", err)
	}
	for _, ref := range []string{"Desktop", "id:" + st.Node.ID} {
		if _, err := n.link.RemoveNode(ctx, ref); !errors.Is(err, ErrNodeIsThis) {
			t.Fatalf("remove %s: want ErrNodeIsThis, got %v", ref, err)
		}
	}
	removed, err := n.link.RemoveNode(ctx, "laptop")
	if err != nil || removed.Name != "laptop" {
		t.Fatalf("remove: %v %+v", err, removed)
	}
	if list, err := n.link.Nodes(ctx); err != nil || len(list) != 1 {
		t.Fatalf("nodes after: %v %+v", err, list)
	}
	h.Stop()
	if _, err := n.link.Nodes(ctx); !isUnreachable(err) {
		t.Fatalf("hub stopped: want an UnreachableError, got %v", err)
	}
}

func isUnreachable(err error) bool {
	_, ok := errors.AsType[*UnreachableError](err)
	return ok
}

// TestOpenWithHubDown checks a daemon starts as fast with the hub down:
// Open returns at once, and the link goes on trying.
func TestOpenWithHubDown(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	n := newNode(t, h, nil)
	n.loggedIn("desktop")
	n.link.Close()
	h.Stop()
	n.open()
	if s := n.link.Status(); s.Connection != Connecting && s.Connection != Offline {
		t.Fatalf("status %+v", s)
	}
	n.waitConnection(Offline)
}
