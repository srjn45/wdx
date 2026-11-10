// Package hubtest serves a hub in a test's process, for the tests of the hub
// and of wdx's side of it: on 127.0.0.1 on ports the system picks, with a
// home and a certificate authority made in the test, a user made through
// the store, and a browser that logs in and approves or denies a node's
// login as a person would, with a cookie jar and the codes of the user's
// authenticator. When the test ends, every request must have been logged
// from received to returned, and no line of the hub's log may hold a secret
// the test named. It is the one package of the hub wdx's tests may use, and
// only they.
package hubtest

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/srjn45/wdx/internal/hub/account"
	"github.com/srjn45/wdx/internal/hub/audit"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/hub/hubdb"
	"github.com/srjn45/wdx/internal/hub/server"
	"github.com/srjn45/wdx/internal/limitstest"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/logtest"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/sqlite/sqlitetest"
)

// PublicURL is the address the hub says it is at, in the links it hands
// out. The web endpoint is at Hub.URL instead, on a port the system picked:
// the browser here goes there for it.
const PublicURL = "http://127.0.0.1:8080"

// Password is the password of every user a hub here makes.
const Password = "correct horse battery"

// Clock is the hub's time: the real time, moved on by what a test advances
// it, so that the certificates it issues are valid now and the codes of an
// authenticator can be made one 30-second step after another.
type Clock struct {
	mu     sync.Mutex
	offset time.Duration
}

// Now is the hub's time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.offset)
}

// Advance moves the hub's time on by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.offset += d
}

// User is a user of the hub, signed up with an authenticator.
type User struct {
	ID, Username, Password string
	// Key is the authenticator's secret, which Code makes its codes from.
	Key string
}

// Hub is a hub served in the test.
type Hub struct {
	t      *testing.T
	Server *server.Server
	Home   string
	// URL is the web endpoint, http://127.0.0.1:<port>.
	URL string
	// NodesURL is the node endpoint, https://127.0.0.1:<port>, as nodes are
	// told it.
	NodesURL string
	Clock    *Clock
	// User is the user the hub was started with, alice.
	User User

	opts    server.Options
	stop    func()
	mu      sync.Mutex
	secrets []string
}

// Stop stops the hub, as its operator does, before the test ends.
func (h *Hub) Stop() { h.stop() }

// Restart stops the hub and serves it again on the same home and the same
// ports, as its operator does when updating it: nodes reconnect to it at the
// addresses they were given.
func (h *Hub) Restart() {
	h.t.Helper()
	h.stop()
	h.opts.Config.ListenWeb = strings.TrimPrefix(h.URL, "http://")
	h.opts.Config.ListenNodes = h.Server.Addresses().Nodes
	h.serve()
}

// Move restarts the hub with nodes_url set to https://<host> and the port
// the node endpoint listens on, and nodes_url_previous to where nodes were
// told before, as its operator moves it to another name; NodesURL is then
// the new address.
func (h *Hub) Move(host string) {
	h.t.Helper()
	_, port, err := net.SplitHostPort(h.Server.Addresses().Nodes)
	if err != nil {
		h.t.Fatal(err)
	}
	h.opts.Config.NodesURLPrevious = append(h.opts.Config.NodesURLPrevious, h.NodesURL)
	h.opts.Config.NodesURL = "https://" + net.JoinHostPort(host, port)
	h.Restart()
	h.NodesURL = h.Server.NodesURL()
}

// serve listens and serves the hub with its options until stop.
func (h *Hub) serve() {
	t := h.t
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s, err := server.Listen(ctx, h.opts)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx) }()
	h.Server = s
	h.stop = sync.OnceFunc(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("hub: serve: %v", err)
		}
	})
}

// Option changes the hub's options before it starts.
type Option func(*server.Options)

// RenewEvery makes the hub check every d, rather than daily, whether a
// connected node's certificate is due for renewal.
func RenewEvery(d time.Duration) Option {
	return func(o *server.Options) { o.RenewInterval = d }
}

// Start serves a hub until the test ends, with a user, alice. A test binary
// that uses it runs in a scope of its own: runtest.Main in its TestMain.
func Start(t *testing.T, opts ...Option) *Hub {
	t.Helper()
	cfg := hubconfig.Default()
	cfg.Home = runtest.Dir(t) // it holds the socket
	cfg.PublicURL = PublicURL
	cfg.ListenWeb, cfg.ListenNodes = "127.0.0.1:0", "127.0.0.1:0"
	cfg.MasterKey = make([]byte, hubconfig.MasterKeySize)
	if _, err := rand.Read(cfg.MasterKey); err != nil {
		t.Fatal(err)
	}
	clock := &Clock{}
	o := server.Options{Config: cfg, Now: clock.Now, Argon2: account.Argon2{Memory: 8, Time: 1, Threads: 1}}
	// A machine slowed down by other tests slows them rather than fails them.
	limitstest.Lengthen(t, &o.Limits)
	for _, opt := range opts {
		opt(&o)
	}
	home := o.Config.Home
	sqlitetest.Seed(t, hubconfig.DBPath(home), hubdb.Open)
	logtest.OnFailure(t, home) // registered first, so it runs once the hub stopped
	logs := logging.NewDailyFile(hubconfig.LogsDir(home), hubconfig.Name, 7, time.Now)
	o.Log = logging.New(logs, slog.LevelDebug)
	h := &Hub{t: t, Home: home, Clock: clock, opts: o, stop: func() {}}
	t.Cleanup(func() {
		h.stop()
		logs.Close()
		h.wantLogged()
	})
	h.serve()
	// The same on a restart, which listens at them again.
	h.URL, h.NodesURL = "http://"+h.Server.Addresses().Web, h.Server.NodesURL()
	h.User = h.AddUser("alice")
	return h
}

// Secret names values no line of the hub's log may hold.
func (h *Hub) Secret(values ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, v := range values {
		if v != "" {
			h.secrets = append(h.secrets, v)
		}
	}
}

// AddUser signs up username through the store, with an authenticator
// confirmed, as a person would on the pages.
func (h *Hub) AddUser(username string) User {
	h.t.Helper()
	t := h.t
	ctx := context.Background()
	accounts := h.Server.Accounts()
	inv, err := accounts.CreateInvite(ctx, account.Admin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	by := account.By{Actor: audit.ActorUser, Address: "127.0.0.1"}
	u, started, err := accounts.SignUp(ctx, by, account.SignUp{Invite: inv.Token, Username: username, Password: Password, UserAgent: "hubtest"})
	if err != nil {
		t.Fatal(err)
	}
	key, err := accounts.EnrolTOTP(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	user := User{ID: u.ID, Username: username, Password: Password, Key: key.Secret()}
	confirmed, err := accounts.ConfirmTOTP(ctx, by, u.ID, started.ID, h.Code(user))
	if err != nil {
		t.Fatal(err)
	}
	h.Secret(inv.Token, started.Token, confirmed.Token, Password, user.Key)
	h.Secret(confirmed.RecoveryCodes...)
	return user
}

// Code is a code of u's authenticator once the hub's time moved on to the
// next 30-second step, so that each is of a step not used yet: each step is
// accepted once.
// The time moves first, so that the hub checks the code at the step it was
// made at or the next, which a step crossed meanwhile in real time makes.
func (h *Hub) Code(u User) string {
	h.t.Helper()
	h.Clock.Advance(30 * time.Second)
	c, err := totp.GenerateCodeCustom(u.Key, h.Clock.Now(), totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		h.t.Fatal(err)
	}
	h.Secret(c)
	return c
}

// CA is the pool of the hub's CA alone, which nodes trust the node endpoint
// by.
func (h *Hub) CA() *x509.CertPool { return h.Server.CA().Pool() }

// NodeClient is an HTTP client of the node endpoint showing cert.
func (h *Hub) NodeClient(cert tls.Certificate) *http.Client {
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: h.CA(), Certificates: []tls.Certificate{cert},
	}}}
}

// Logged returns the lines of the hub's log whose msg is msg.
func (h *Hub) Logged(msg string) []map[string]any {
	h.t.Helper()
	var lines []map[string]any
	h.eachLine(func(line map[string]any, _ []byte) {
		if line["msg"] == msg {
			lines = append(lines, line)
		}
	})
	return lines
}

func (h *Hub) eachLine(fn func(line map[string]any, raw []byte)) {
	t := h.t
	files, err := filepath.Glob(filepath.Join(hubconfig.LogsDir(h.Home), "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		logtest.Scan(t, f, 4<<20, func(raw []byte) {
			var line map[string]any
			if err := json.Unmarshal(raw, &line); err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			fn(line, raw)
		})
	}
}

var digits = regexp.MustCompile(`^[0-9]+$`)

// wantLogged checks every request was logged as received and returned under
// one debug id, and that no line holds a secret.
func (h *Hub) wantLogged() {
	t := h.t
	t.Helper()
	h.mu.Lock()
	secrets := h.secrets
	h.mu.Unlock()
	received := map[string]int{}
	h.eachLine(func(line map[string]any, _ []byte) {
		delete(line, "time") // its digits are no code's
		b, _ := json.Marshal(line)
		for _, secret := range secrets {
			if leaked(b, secret) {
				t.Errorf("hub: a secret, %q, logged: %s", secret, b)
			}
		}
		id, _ := line["debug_id"].(string)
		switch line["msg"] {
		case "request received":
			received[id]++
		case "request returned":
			received[id]--
		}
	})
	for id, n := range received {
		if n != 0 {
			t.Errorf("hub: %s: received and returned do not pair up", id)
		}
	}
}

// leaked reports whether the line b holds secret; a code of digits must
// stand alone, so that it is not found inside a longer number.
// Leaked says whether line, a JSON log line, holds secret, its time aside,
// whose digits are no code's: a code of digits only as a word of its own.
// A line that is not JSON is matched whole.
func Leaked(line []byte, secret string) bool {
	var v map[string]any
	if json.Unmarshal(line, &v) == nil {
		delete(v, "time")
		line, _ = json.Marshal(v)
	}
	return leaked(line, secret)
}

func leaked(b []byte, secret string) bool {
	if digits.MatchString(secret) {
		return regexp.MustCompile(`(^|[^0-9A-Za-z])` + secret + `([^0-9A-Za-z]|$)`).Match(b)
	}
	return bytes.Contains(b, []byte(secret)) || strings.Contains(string(b), strings.ReplaceAll(secret, "-", ""))
}

// StreamExpiry makes the join timeout of each stream between nodes end when
// after says, rather than once the hub's Limits.StreamJoin passed.
func StreamExpiry(after func(time.Duration) <-chan time.Time) Option {
	return func(o *server.Options) { o.Streams.After = after }
}

// StreamsPerUser lets a user have n streams open or waiting, rather than 32.
func StreamsPerUser(n int) Option {
	return func(o *server.Options) { o.Streams.MaxPerUser = n }
}
