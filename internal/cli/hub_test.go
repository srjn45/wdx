package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/clikit/clikittest"
	"github.com/srjn45/wdx/internal/daemon"
	"github.com/srjn45/wdx/internal/deps"
	"github.com/srjn45/wdx/internal/hub/hubtest"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/waittest"
)

// hubClock is the time of a daemon's link to the hub: the hub's, whose waits
// end only when a test moves it on, so that a test says when the link polls
// a login, expires it or connects again.
type hubClock struct {
	hub     *hubtest.Clock
	mu      sync.Mutex
	waits   []*hubWait
	changed chan struct{}
}

type hubWait struct {
	at time.Time
	d  time.Duration
	ch chan time.Time
}

func newHubClock(h *hubtest.Hub) *hubClock {
	return &hubClock{hub: h.Clock, changed: make(chan struct{})}
}

func (c *hubClock) Now() time.Time { return c.hub.Now() }

func (c *hubClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.Now()
		return ch
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waits = append(c.waits, &hubWait{at: c.Now().Add(d), d: d, ch: ch})
	close(c.changed)
	c.changed = make(chan struct{})
	return ch
}

// Advance moves the hub's time on by d, ending the waits it passes.
func (c *hubClock) Advance(d time.Duration) {
	c.hub.Advance(d)
	now := c.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	kept := c.waits[:0]
	for _, w := range c.waits {
		if w.at.After(now) {
			kept = append(kept, w)
		} else {
			w.ch <- now
		}
	}
	c.waits = kept
}

// waiting waits for a wait that ok accepts to be pending.
func (c *hubClock) waiting(t *testing.T, what string, ok func(time.Duration) bool) {
	t.Helper()
	timeout := waittest.After(t)
	for {
		c.mu.Lock()
		var seen []time.Duration
		for _, w := range c.waits {
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

// devicePollInterval is how often the hub has a device login polled.
const devicePollInterval = 5 * time.Second

// waitPoll moves the clock on to the device login's next poll.
func (c *hubClock) waitPoll(t *testing.T) {
	t.Helper()
	c.waiting(t, "the next poll", func(d time.Duration) bool { return d == devicePollInterval })
	c.Advance(devicePollInterval)
}

// hubHome is a home whose daemon's link to a hub in the test goes by clock.
type hubHome struct {
	t     *testing.T
	home  string
	hub   *hubtest.Hub
	clock *hubClock
	mu    sync.Mutex
	// secrets are what no line of the home's logs may hold.
	secrets []string
}

// newHubHome serves a hub and a home whose daemon links to it. The hub is
// served first, so that it stops after the daemon.
func newHubHome(t *testing.T) *hubHome {
	t.Helper()
	h := hubtest.Start(t)
	clock := newHubClock(h)
	hh := &hubHome{t: t, hub: h, clock: clock}
	t.Cleanup(hh.wantNoSecrets) // once the daemon stopped, as it is served after
	hh.home = newHomeOpts(t, testShell, nil, func(o *daemon.Options) {
		o.Hub = daemon.HubOptions{Clock: clock}
	})
	return hh
}

// secret names values no line of the home's logs, nor of the hub's, may hold.
func (hh *hubHome) secret(values ...string) {
	hh.hub.Secret(values...)
	hh.mu.Lock()
	defer hh.mu.Unlock()
	for _, v := range values {
		if v != "" {
			hh.secrets = append(hh.secrets, v)
		}
	}
}

// keySecret names the node's key, as written, a secret.
func (hh *hubHome) keySecret() {
	b, err := os.ReadFile(filepath.Join(identity.HubDir(hh.home), "node.key"))
	if err != nil {
		hh.t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if len(line) > 16 && !strings.HasPrefix(line, "-----") {
			hh.secret(line)
		}
	}
}

// wantNoSecrets checks no line the daemon, the CLI or a holder logged holds
// a secret.
func (hh *hubHome) wantNoSecrets() {
	t := hh.t
	hh.mu.Lock()
	secrets := hh.secrets
	hh.mu.Unlock()
	files, err := filepath.Glob(filepath.Join(identity.LogsDir(hh.home), "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		file, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		s := bufio.NewScanner(file)
		s.Buffer(make([]byte, 0, 64<<10), 4<<20)
		for s.Scan() {
			for _, secret := range secrets {
				if bytes.Contains(s.Bytes(), []byte(secret)) {
					t.Errorf("a secret, %q, logged: %s", secret, s.Bytes())
				}
			}
		}
		file.Close()
	}
}

// hubOutput is what the hub's commands print with --json.
type hubOutput struct {
	jsonOutput
	Hub     *api.HubStatus `json:"hub"`
	HubTold *bool          `json:"hub_told"`
	Nodes   []api.HubNode  `json:"nodes"`
	Node    *struct {
		api.HubNode
		Fingerprint string `json:"fingerprint"`
	} `json:"node"`
}

// hubJSON runs args with --json, as wdxJSONWith does, in an environment that
// opens no browser unless adjust says how.
func (hh *hubHome) hubJSON(adjust func(*Env), args ...string) (hubOutput, int) {
	hh.t.Helper()
	return hubJSONOf(hh.t, wdxWith(hh.t, hh.home, hh.env(adjust), append(args, "--json")...), args)
}

func hubJSONOf(t *testing.T, out clikittest.Output, args []string) (hubOutput, int) {
	t.Helper()
	var v hubOutput
	// What to open, and the code, go to stderr: stdout is the one object.
	out.Stderr = ""
	if err := json.Unmarshal(clikittest.JSON(t, args, out, codes), &v); err != nil {
		t.Fatalf("%v: stdout is not JSON: %v\n%s", args, err, out.Stdout)
	}
	wantDeclared(t, args, v.jsonOutput)
	return v, out.Exit
}

// env is adjust after what every run against the hub has: no browser to
// open, and none opened.
func (hh *hubHome) env(adjust func(*Env)) func(*Env) {
	return func(env *Env) {
		env.Deps = noOpener()
		env.OpenBrowser = func(_ context.Context, _, link string) error {
			hh.t.Errorf("a browser opened %s", link)
			return errors.New("no browser in a test")
		}
		if adjust != nil {
			adjust(env)
		}
	}
}

// noOpener is a machine with no browser opener.
func noOpener() deps.Probe {
	return deps.Probe{GOOS: "linux", Session: "x11", LookPath: func(string) (string, error) { return "", exec.ErrNotFound }}
}

// withBrowser is a machine with a graphical session and xdg-open, whose
// browser does what open does with the link it is sent to.
func withBrowser(open func(link string) error) func(*Env) {
	return func(env *Env) {
		env.Deps = deps.Probe{GOOS: "linux", Session: "x11", LookPath: func(file string) (string, error) {
			if file == "xdg-open" {
				return "/usr/bin/xdg-open", nil
			}
			return "", exec.ErrNotFound
		}}
		env.OpenBrowser = func(_ context.Context, program, link string) error {
			if program != "/usr/bin/xdg-open" {
				return errors.New("opened with " + program)
			}
			return open(link)
		}
	}
}

// approving is a browser of the hub's user that approves the login it is
// sent to, naming the node name unless empty, and follows the callback, as
// a person would; page gets what the CLI's loopback showed. A failure ends
// the run, as Ctrl-C would, through cancel.
func (hh *hubHome) approving(cancel context.CancelFunc, deny bool, page *string) func(link string) error {
	return func(link string) error {
		t := hh.t
		b := hh.hub.Browser(hh.hub.User)
		decide := b.Approve
		if deny {
			decide = func(link, _ string) (*url.URL, error) { return b.Deny(link) }
		}
		callback, err := decide(link, "")
		if err != nil {
			t.Errorf("approve %s: %v", link, err)
			cancel()
			return err
		}
		hh.secret(callback.Query().Get("code"))
		body, err := b.FollowCallback(callback)
		if err != nil {
			t.Errorf("callback: %v", err)
			cancel()
			return err
		}
		*page = body
		return nil
	}
}

// browserLogin logs in by the browser flow, the browser approving, or
// denying, the login it is sent to, and returns what login printed and the
// page the CLI's loopback showed.
func (hh *hubHome) browserLogin(deny bool, args ...string) (clikittest.Output, string) {
	hh.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var page string
	args = append([]string{"hub", "login", "--url", hh.hub.URL}, args...)
	out := runWdx(ctx, hh.t, hh.home, hh.env(withBrowser(hh.approving(cancel, deny, &page))), args...)
	wantLogged(hh.t, hh.home, args, out)
	return out, page
}

// loggedIn makes the home a node named name, by the browser flow, and waits
// for its control connection.
func (hh *hubHome) loggedIn(name string) {
	hh.t.Helper()
	if out, _ := hh.browserLogin(false, "--name", name); out.Exit != clikit.ExitOK {
		hh.t.Fatalf("login: %+v", out)
	}
	hh.keySecret()
	hh.waitConnection(api.HubConnectionConnected)
}

// waitConnection waits for the daemon's connection to the hub to be want.
func (hh *hubHome) waitConnection(want api.HubConnection) api.HubStatus {
	hh.t.Helper()
	c := client.New(client.Options{Socket: identity.SocketPath(hh.home), DebugID: logging.NewDebugID()})
	var last api.HubStatus
	if !eventually(hh.t, func() bool {
		r, err := c.Hub(context.Background())
		last = r.Hub
		return err == nil && r.Hub.Connection == want
	}) {
		hh.t.Fatalf("want the connection %s, last %+v", want, last)
	}
	return last
}

// stderr is a command's stderr, which a test reads while it runs.
type stderr struct {
	mu      sync.Mutex
	b       bytes.Buffer
	changed chan struct{}
}

func newStderr() *stderr { return &stderr{changed: make(chan struct{})} }

func (s *stderr) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	close(s.changed)
	s.changed = make(chan struct{})
	return s.b.Write(p)
}

func (s *stderr) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// wait waits for re to match what was written, returning its submatches.
func (s *stderr) wait(t *testing.T, re *regexp.Regexp) []string {
	t.Helper()
	timeout := waittest.After(t)
	for {
		s.mu.Lock()
		m := re.FindStringSubmatch(s.b.String())
		got, changed := s.b.String(), s.changed
		s.mu.Unlock()
		if m != nil {
			return m
		}
		select {
		case <-changed:
		case <-timeout:
			t.Fatalf("no %s in stderr:\n%s", re, got)
		}
	}
}

// running is a command running beside the test.
type running struct {
	args   []string
	stderr *stderr
	cancel context.CancelFunc
	done   chan clikittest.Output
}

// start runs args beside the test, until it ends or cancel, which is Ctrl-C.
func (hh *hubHome) start(args ...string) *running {
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{args: args, stderr: newStderr(), cancel: cancel, done: make(chan clikittest.Output, 1)}
	adjust := hh.env(func(env *Env) { env.Stderr = r.stderr })
	go func() { r.done <- runWdx(ctx, hh.t, hh.home, adjust, args...) }()
	hh.t.Cleanup(cancel)
	return r
}

// wait waits for the command to end, and checks it was logged.
func (hh *hubHome) wait(r *running) clikittest.Output {
	hh.t.Helper()
	var out clikittest.Output
	select {
	case out = <-r.done:
	case <-waittest.After(hh.t):
		hh.t.Fatalf("%v still running; stderr:\n%s", r.args, r.stderr.String())
	}
	r.cancel()
	wantLogged(hh.t, hh.home, r.args, out)
	return out
}

var userCode = regexp.MustCompile(`open (\S+) and type the code (\S+)`)

// deviceLogin starts a login by the device flow with --json, and returns it
// and the code it printed.
func (hh *hubHome) deviceLogin(args ...string) (*running, string) {
	hh.t.Helper()
	r := hh.start(append([]string{"hub", "login", "--url", hh.hub.URL, "--no-browser", "--json"}, args...)...)
	code := r.stderr.wait(hh.t, userCode)[2]
	hh.secret(code)
	return r, code
}

func TestHubLoginBrowser(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	out, page := hh.browserLogin(false, "--name", "desk", "--json")
	v, exit := hubJSONOf(t, out, []string{"hub", "login"})
	if exit != clikit.ExitOK || v.Result != "logged_in" || v.Hub == nil || deref(v.Hub.User) != "alice" || v.Hub.Node.Name != "desk" || deref(v.Hub.Url) != hh.hub.URL {
		t.Fatalf("exit %d, %+v", exit, v)
	}
	if !strings.Contains(out.Stderr, "opening "+hubtest.PublicURL+"/nodes/approve?login=") || !strings.Contains(page, "Approved: back to your terminal.") {
		t.Fatalf("stderr %q, page %q", out.Stderr, page)
	}
	hh.keySecret()
	hh.waitConnection(api.HubConnectionConnected)
}

func TestHubLoginDevice(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	r, code := hh.deviceLogin("--name", "desk")
	if !strings.Contains(r.stderr.String(), "open "+hubtest.PublicURL+"/device and type the code ") {
		t.Fatalf("stderr %q", r.stderr.String())
	}
	if err := hh.hub.Browser(hh.hub.User).ApproveCode(code, ""); err != nil {
		t.Fatal(err)
	}
	hh.clock.waitPoll(t)
	out := hh.wait(r)
	v, exit := hubJSONOf(t, out, r.args)
	if exit != clikit.ExitOK || v.Result != "logged_in" || v.Hub.Node.Name != "desk" || strings.Contains(out.Stdout, code) {
		t.Fatalf("exit %d, %+v", exit, out)
	}
	hh.keySecret()
}

func TestHubLoginNoOpener(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	r := hh.start("hub", "login", "--url", hh.hub.URL, "--name", "desk")
	code := r.stderr.wait(t, userCode)[2]
	hh.secret(code)
	if !strings.Contains(r.stderr.String(), "no browser to open: xdg-open is not installed") {
		t.Fatalf("stderr %q", r.stderr.String())
	}
	if err := hh.hub.Browser(hh.hub.User).ApproveCode(code, ""); err != nil {
		t.Fatal(err)
	}
	hh.clock.waitPoll(t)
	out := hh.wait(r)
	if out.Exit != clikit.ExitOK || out.Stdout != "logged in to "+hh.hub.URL+" as alice; this machine is node desk\n" {
		t.Fatalf("%+v", out)
	}
}

func TestHubLoginAlreadyLoggedIn(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	hh.loggedIn("desk")
	v, exit := hh.hubJSON(nil, "hub", "login", "--url", hh.hub.URL, "--no-browser")
	wantError(t, v.jsonOutput, exit, api.ErrorCodeAlreadyLoggedIn, clikit.ExitRejected)
	if v.Hint == nil || !strings.Contains(*v.Hint, "wdx hub logout") {
		t.Fatalf("hint %v", v.Hint)
	}
}

func TestHubLoginDenied(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	out, page := hh.browserLogin(true, "--json")
	v, exit := hubJSONOf(t, out, []string{"hub", "login"})
	wantError(t, v.jsonOutput, exit, api.ErrorCodeLoginDenied, clikit.ExitRejected)
	if !strings.Contains(page, "Denied") {
		t.Fatalf("page %q", page)
	}
}

func TestHubLoginExpired(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	r, _ := hh.deviceLogin()
	hh.clock.waiting(t, "the first poll", func(d time.Duration) bool { return d == devicePollInterval })
	hh.clock.Advance(11 * time.Minute)
	v, exit := hubJSONOf(t, hh.wait(r), r.args)
	wantError(t, v.jsonOutput, exit, api.ErrorCodeLoginExpired, clikit.ExitRejected)
}

func TestHubLoginCancelled(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	first, _ := hh.deviceLogin("--name", "desk")
	second, _ := hh.deviceLogin("--name", "desk-2")

	// The second replaced the first.
	v, exit := hubJSONOf(t, hh.wait(first), first.args)
	wantError(t, v.jsonOutput, exit, api.ErrorCodeLoginCancelled, clikit.ExitRejected)

	// Ctrl-C cancels the second, which the hub cannot finish then.
	second.cancel()
	v, exit = hubJSONOf(t, hh.wait(second), second.args)
	wantError(t, v.jsonOutput, exit, api.ErrorCodeLoginCancelled, clikit.ExitRejected)
	if v.Detail != "you cancelled the login" {
		t.Fatalf("detail %q", v.Detail)
	}
	if _, err := os.Stat(identity.HubDir(hh.home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want no login written: %v", err)
	}
}

func TestHubLoginNameTaken(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	hh.hub.NewNode(hh.hub.User, "desk")
	r, code := hh.deviceLogin("--name", "desk")
	b := hh.hub.Browser(hh.hub.User)
	if err := b.ApproveCode(code, ""); !errors.Is(err, hubtest.ErrNameTaken) {
		t.Fatalf("want the name asked again, got %v", err)
	}
	// The page asks again, and the person names it.
	if err := b.ApproveCode(code, "desk-2"); err != nil {
		t.Fatal(err)
	}
	hh.clock.waitPoll(t)
	v, exit := hubJSONOf(t, hh.wait(r), r.args)
	if exit != clikit.ExitOK || v.Hub.Node.Name != "desk-2" {
		t.Fatalf("exit %d, %+v", exit, v)
	}
}

func TestHubLoginUnreachable(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + ln.Addr().String()
	ln.Close()
	v, exit := hh.hubJSON(nil, "hub", "login", "--url", closed, "--no-browser")
	wantError(t, v.jsonOutput, exit, api.ErrorCodeHubUnreachable, clikit.ExitSystem)
}

func TestHubLoginInvalidURL(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	v, exit := hh.hubJSON(nil, "hub", "login", "--url", "http://hub.example.com", "--no-browser")
	wantError(t, v.jsonOutput, exit, api.ErrorCodeInvalidArgument, clikit.ExitRejected)
}

func TestHubLogoutOnline(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	hh.loggedIn("desk")
	v, exit := hh.hubJSON(nil, "hub", "logout")
	if exit != clikit.ExitOK || v.Result != "logged_out" || v.HubTold == nil || !*v.HubTold || v.Hub.Node.Name != "desk" {
		t.Fatalf("exit %d, %+v", exit, v)
	}
	if _, err := os.Stat(identity.HubDir(hh.home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want %s gone: %v", identity.HubDir(hh.home), err)
	}
	if v, _ := hh.hubJSON(nil, "hub", "status"); v.Hub.Connection != api.HubConnectionLoggedOut {
		t.Fatalf("status %+v", v.Hub)
	}
}

func TestHubLogoutOffline(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	hh.loggedIn("desk")
	hh.hub.Stop()
	hh.waitConnection(api.HubConnectionOffline)
	v, exit := hh.hubJSON(nil, "hub", "logout")
	if exit != clikit.ExitOK || v.Result != "logged_out" || v.HubTold == nil || *v.HubTold {
		t.Fatalf("exit %d, %+v", exit, v)
	}
	if _, err := os.Stat(identity.HubDir(hh.home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want %s gone: %v", identity.HubDir(hh.home), err)
	}
}

func TestHubLogoutNotLoggedIn(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	v, exit := hh.hubJSON(nil, "hub", "logout")
	wantError(t, v.jsonOutput, exit, api.ErrorCodeNotLoggedIn, clikit.ExitRejected)
}

func TestHubStatusLoggedOut(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	v, exit := hh.hubJSON(nil, "hub", "status")
	if exit != clikit.ExitOK || v.Hub == nil || v.Hub.Connection != api.HubConnectionLoggedOut || v.Hub.Url != nil || v.Hub.User != nil || v.Hub.Node != nil {
		t.Fatalf("exit %d, %+v", exit, v.Hub)
	}
}

func TestHubStatusConnected(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	hh.loggedIn("desk")
	v, exit := hh.hubJSON(nil, "hub", "status")
	h := v.Hub
	if exit != clikit.ExitOK || h.Connection != api.HubConnectionConnected || deref(h.User) != "alice" || h.Node.Name != "desk" || h.HubVersion == nil || h.InRange == nil || !*h.InRange {
		t.Fatalf("exit %d, %+v", exit, h)
	}
}

func TestHubStatusOffline(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	hh.loggedIn("desk")
	hh.hub.Stop()
	hh.waitConnection(api.HubConnectionOffline)
	if v, _ := hh.hubJSON(nil, "hub", "status"); v.Hub.Connection != api.HubConnectionOffline || v.Hub.Node.Name != "desk" {
		t.Fatalf("%+v", v.Hub)
	}
}

func TestHubStatusConnecting(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	hh.loggedIn("desk")
	nodes := strings.TrimPrefix(hh.hub.NodesURL, "https://")
	hh.hub.Stop()
	hh.waitConnection(api.HubConnectionOffline)
	// Where the node endpoint was, something that never answers.
	ln, err := net.Listen("tcp", nodes)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn) // held open, unanswered
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		<-accepted
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	// The backoff's, not a login's ten minutes.
	hh.clock.waiting(t, "the next try", func(d time.Duration) bool { return d <= time.Minute })
	hh.clock.Advance(time.Hour)
	hh.waitConnection(api.HubConnectionConnecting)
	if v, _ := hh.hubJSON(nil, "hub", "status"); v.Hub.Connection != api.HubConnectionConnecting {
		t.Fatalf("%+v", v.Hub)
	}
}

func TestHubStatusRevoked(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	hh.loggedIn("desk")
	hh.hub.RemoveNode(hh.hub.NewNode(hh.hub.User, "laptop"), "desk")
	hh.waitConnection(api.HubConnectionRevoked)
	if v, _ := hh.hubJSON(nil, "hub", "status"); v.Hub.Connection != api.HubConnectionRevoked || v.Hub.Node.Name != "desk" {
		t.Fatalf("%+v", v.Hub)
	}
	out := wdx(t, hh.home, "hub", "status")
	if !strings.HasPrefix(out.Stdout, "revoked: another node removed node desk") || !strings.Contains(out.Stdout, "hint: run wdx hub logout") {
		t.Fatalf("%+v", out)
	}
}

func TestHubStatusDisabled(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	hh.loggedIn("desk")
	hh.hub.DisableUser("alice")
	hh.waitConnection(api.HubConnectionDisabled)
	if v, _ := hh.hubJSON(nil, "hub", "status"); v.Hub.Connection != api.HubConnectionDisabled {
		t.Fatalf("%+v", v.Hub)
	}
}

func TestHubNodes(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	hh.loggedIn("desk")
	laptop := hh.hub.NewNode(hh.hub.User, "laptop")

	v, exit := hh.hubJSON(nil, "hub", "node", "list")
	if exit != clikit.ExitOK || len(v.Nodes) != 2 {
		t.Fatalf("exit %d, %+v", exit, v.Nodes)
	}
	desk, other := v.Nodes[0], v.Nodes[1]
	if desk.Name != "desk" || !desk.This || !desk.Online || other.Name != "laptop" || other.This || other.Online || other.LastSeenAt != nil {
		t.Fatalf("nodes %+v", v.Nodes)
	}

	v, exit = hh.hubJSON(nil, "hub", "node", "get", "--name", "LAPTOP")
	if exit != clikit.ExitOK || v.Node == nil || v.Node.Id != laptop.ID || !strings.HasPrefix(v.Node.Fingerprint, "SHA256:") {
		t.Fatalf("exit %d, %+v", exit, v.Node)
	}
	v, exit = hh.hubJSON(nil, "hub", "node", "get", "--name", "nope")
	wantError(t, v.jsonOutput, exit, api.ErrorCodeNodeNotFound, clikit.ExitRejected)

	v, exit = hh.hubJSON(nil, "hub", "node", "remove", "--name", "desk", "--yes")
	wantError(t, v.jsonOutput, exit, api.ErrorCodeNodeIsThis, clikit.ExitRejected)
	if v.Hint == nil || !strings.Contains(*v.Hint, "wdx hub logout") {
		t.Fatalf("hint %v", v.Hint)
	}
	v, exit = hh.hubJSON(nil, "hub", "node", "remove", "--id", laptop.ID, "--yes")
	if exit != clikit.ExitOK || v.Result != "removed" || v.Node.Name != "laptop" {
		t.Fatalf("exit %d, %+v", exit, v)
	}
	v, exit = hh.hubJSON(nil, "hub", "node", "remove", "--name", "laptop", "--yes")
	wantError(t, v.jsonOutput, exit, api.ErrorCodeNodeNotFound, clikit.ExitRejected)
}

func TestHubNodesNotLoggedIn(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	for _, args := range [][]string{{"hub", "node", "list"}, {"hub", "node", "get", "--name", "desk"}, {"hub", "node", "remove", "--name", "desk", "--yes"}} {
		v, exit := hh.hubJSON(nil, args...)
		wantError(t, v.jsonOutput, exit, api.ErrorCodeNotLoggedIn, clikit.ExitRejected)
	}
}

func TestHubNodesUnreachable(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	hh.loggedIn("desk")
	hh.hub.Stop()
	v, exit := hh.hubJSON(nil, "hub", "node", "list")
	wantError(t, v.jsonOutput, exit, api.ErrorCodeHubUnreachable, clikit.ExitSystem)
}

func TestHubNodeRemovePrompt(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	hh.loggedIn("desk")
	hh.hub.NewNode(hh.hub.User, "laptop")
	out := wdxWith(t, hh.home, hh.env(answering(t, "n")), "hub", "node", "remove", "--name", "laptop")
	if out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "remove node laptop from your account, cutting it off at once? [y/N]: ") || !strings.Contains(out.Stderr, "[cancelled]") {
		t.Fatalf("want it kept, got %+v", out)
	}
	out = wdxWith(t, hh.home, hh.env(answering(t, "y")), "hub", "node", "remove", "--name", "laptop")
	if out.Exit != clikit.ExitOK || out.Stdout != "removed node laptop from the account; the hub cut it off\n" {
		t.Fatalf("want it removed, got %+v", out)
	}
}

func TestCompleteHubNodes(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	if got := complete(t, hh.home, "hub", "node", "get", "--name", ""); len(got) != 0 {
		t.Fatalf("logged out: want nothing offered, got %q", got)
	}
	hh.loggedIn("desk")
	laptop := hh.hub.NewNode(hh.hub.User, "laptop")
	got := complete(t, hh.home, "hub", "node", "remove", "--name", "")
	if len(got) != 2 || !strings.HasPrefix(got[0], "desk\t") || got[1] != "laptop\t"+laptop.ID {
		t.Fatalf("names %q", got)
	}
	got = complete(t, hh.home, "hub", "node", "get", "--id", "")
	if len(got) != 2 || got[1] != laptop.ID+"\tlaptop" {
		t.Fatalf("ids %q", got)
	}
}

func TestHubText(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	out := wdx(t, hh.home, "hub", "status")
	if out.Stdout != "not logged in to a hub\n  hint: run wdx hub login --url <url>\n" {
		t.Fatalf("status %q", out.Stdout)
	}
	out, _ = hh.browserLogin(false, "--name", "desk")
	if out.Exit != clikit.ExitOK || out.Stdout != "logged in to "+hh.hub.URL+" as alice; this machine is node desk\n" {
		t.Fatalf("login %+v", out)
	}
	hh.keySecret()
	hh.waitConnection(api.HubConnectionConnected)
	out = wdx(t, hh.home, "hub", "status")
	if !strings.HasPrefix(out.Stdout, "connected to "+hh.hub.URL+" as alice, node desk\nhub version: ") {
		t.Fatalf("status %q", out.Stdout)
	}
	laptop := hh.hub.NewNode(hh.hub.User, "laptop")
	out = wdx(t, hh.home, "hub", "node", "list")
	if !strings.Contains(out.Stdout, "desk (this)") || !strings.Contains(out.Stdout, "laptop") || !strings.Contains(out.Stdout, "never") {
		t.Fatalf("list %q", out.Stdout)
	}
	out = wdx(t, hh.home, "hub", "node", "get", "--name", "laptop")
	if !strings.HasPrefix(out.Stdout, "node laptop, offline\n") || !strings.Contains(out.Stdout, laptop.ID) || !strings.Contains(out.Stdout, "SHA256:") {
		t.Fatalf("get %q", out.Stdout)
	}
	out = wdx(t, hh.home, "hub", "node", "remove", "--name", "laptop", "--yes")
	if out.Stdout != "removed node laptop from the account; the hub cut it off\n" {
		t.Fatalf("remove %q", out.Stdout)
	}
	hh.hub.Stop()
	hh.waitConnection(api.HubConnectionOffline)
	out = wdx(t, hh.home, "hub", "logout")
	want := "logged out of " + hh.hub.URL + "; this machine is no longer node desk\n" +
		"warning: the hub could not be told; the node stays in the account until it is removed\n" +
		"  hint: remove it from another node: wdx hub node remove --name desk\n"
	if out.Exit != clikit.ExitOK || out.Stdout != want {
		t.Fatalf("logout %q", out.Stdout)
	}
}
