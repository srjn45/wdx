package node

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/hub/audit"
	"github.com/srjn45/wdx/internal/hub/hubdb"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/sqlite/sqlitetest"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type fixture struct {
	*Store
	db    *sql.DB
	clock *clock
	ctx   context.Context
	log   *bytes.Buffer
	// secrets are what no log line may hold.
	secrets []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hub.db")
	sqlitetest.Seed(t, path, hubdb.Open)
	d, err := hubdb.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	c := &clock{t: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)}
	var log bytes.Buffer
	ctx := logging.NewContext(context.Background(), logging.New(&log, slog.LevelDebug))
	f := &fixture{Store: New(d, Options{Now: c.now}), db: d, clock: c, ctx: ctx, log: &log}
	t.Cleanup(func() {
		for _, s := range f.secrets {
			if strings.Contains(log.String(), s) {
				t.Errorf("a secret, %q, logged", s)
			}
		}
	})
	return f
}

func (f *fixture) secret(values ...string) {
	for _, v := range values {
		if v != "" {
			f.secrets = append(f.secrets, v)
		}
	}
}

// user adds a user named username and returns their id.
func (f *fixture) user(t *testing.T, username string) string {
	t.Helper()
	id := "usr-" + username
	f.exec(t, "INSERT INTO users (id, username, password_hash, created_at) VALUES (?, ?, 'x', 0)", id, username)
	return id
}

func (f *fixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) actions(t *testing.T) []string {
	t.Helper()
	rows, err := f.db.Query("SELECT action FROM audit ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var list []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		list = append(list, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return list
}

// machine is what a machine logging in keeps.
type machine struct {
	pub      ed25519.PublicKey
	verifier string
}

func newMachine(t *testing.T) machine {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return machine{pub: pub, verifier: rand.Text() + rand.Text()}
}

func (m machine) challenge() string {
	sum := sha256.Sum256([]byte(m.verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

const callback = "http://127.0.0.1:43210/callback"

func (m machine) browser(name string) StartRequest {
	return StartRequest{Flow: FlowBrowser, Name: name, OS: "linux/amd64", Version: "0.5.0", PublicKey: m.pub, PKCEChallenge: m.challenge(), Callback: callback}
}

func (m machine) device(name string) StartRequest {
	return StartRequest{Flow: FlowDevice, Name: name, OS: "linux/amd64", Version: "0.5.0", PublicKey: m.pub}
}

var from = By{Actor: audit.ActorUser, Address: "192.0.2.1"}

func (f *fixture) start(t *testing.T, req StartRequest) Started {
	t.Helper()
	s, err := f.StartLogin(f.ctx, from, req)
	if err != nil {
		t.Fatal(err)
	}
	f.secret(s.DeviceCode, NormalUserCode(s.UserCode), s.UserCode)
	return s
}

// issued counts the certificates issued.
type issued struct{ n int }

func (i *issued) issue(clk *clock) Issue {
	return func(n Node) ([]byte, time.Time, error) {
		i.n++
		return []byte("PEM of " + n.ID), clk.now().Add(365 * 24 * time.Hour), nil
	}
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: got %v, want %v", what, err, want)
	}
}

func TestBrowserFlow(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	alice := f.user(t, "alice")
	m := newMachine(t)
	f.secret(m.verifier)
	s := f.start(t, m.browser("desktop"))
	if s.DeviceCode != "" || s.UserCode != "" || s.Status != StatusPending || !s.ExpiresAt.Equal(f.clock.now().Add(LoginLife)) {
		t.Fatalf("started %+v", s)
	}
	// The approval link names the login: its id is 128 random bits.
	if id, ok := strings.CutPrefix(s.ID, "nl-"); !ok || len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
		t.Errorf("login id %q, want nl- and 32 hex digits", s.ID)
	}
	a, err := f.Approve(f.ctx, from, alice, s.ID, "desktop")
	if err != nil {
		t.Fatal(err)
	}
	f.secret(a.Code)
	if a.Code == "" || a.Node.Name != "desktop" || !strings.HasPrefix(a.Node.ID, "node-") {
		t.Fatalf("approved %+v", a)
	}
	var i issued
	c, err := f.Exchange(f.ctx, a.Code, m.verifier, i.issue(f.clock))
	if err != nil {
		t.Fatal(err)
	}
	if c.Username != "alice" || c.Node.ID != a.Node.ID || string(c.Certificate) != "PEM of "+a.Node.ID || !c.Node.PublicKey.Equal(m.pub) || c.Node.CertExpiresAt.IsZero() {
		t.Fatalf("credentials %+v", c)
	}
	_, err = f.Exchange(f.ctx, a.Code, m.verifier, i.issue(f.clock))
	wantErr(t, "the code used again", err, ErrLoginNotFound)
	if i.n != 1 {
		t.Errorf("%d certificates issued, want 1", i.n)
	}
	if got := f.actions(t); !slices.Equal(got, []string{ActionLoginStarted, ActionApproved}) {
		t.Errorf("audit %v", got)
	}
}

func TestWrongVerifier(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	alice := f.user(t, "alice")
	m := newMachine(t)
	f.secret(m.verifier)
	a, err := f.Approve(f.ctx, from, alice, f.start(t, m.browser("desktop")).ID, "desktop")
	if err != nil {
		t.Fatal(err)
	}
	f.secret(a.Code)
	var i issued
	_, err = f.Exchange(f.ctx, a.Code, newMachine(t).verifier, i.issue(f.clock))
	wantErr(t, "another verifier", err, ErrWrongVerifier)
	_, err = f.Exchange(f.ctx, a.Code, "", i.issue(f.clock))
	wantErr(t, "no verifier", err, ErrWrongVerifier)
	if _, err := f.Exchange(f.ctx, a.Code, m.verifier, i.issue(f.clock)); err != nil {
		t.Fatalf("the right verifier after: %v", err)
	}
}

func TestCallbackOnLoopbackOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	m := newMachine(t)
	for _, bad := range []string{
		"", "http://localhost:43210/callback", "https://127.0.0.1:43210/callback", "http://127.0.0.2:43210/",
		"http://127.0.0.1/callback", "http://127.0.0.1:0/", "http://127.0.0.1:43210/callback?code=x", "http://evil.example.com:43210/",
		"http://u@127.0.0.1:43210/", "//127.0.0.1:43210/",
	} {
		req := m.browser("desktop")
		req.Callback = bad
		var invalid *InvalidError
		if _, err := f.StartLogin(f.ctx, from, req); !errors.As(err, &invalid) || invalid.Field != "callback" {
			t.Errorf("%q: want the callback refused, got %v", bad, err)
		}
	}
	for _, c := range []struct {
		name string
		edit func(*StartRequest)
	}{
		{"no challenge", func(r *StartRequest) { r.PKCEChallenge = "" }},
		{"a plain challenge", func(r *StartRequest) { r.PKCEChallenge = m.verifier }},
		{"a short key", func(r *StartRequest) { r.PublicKey = r.PublicKey[:16] }},
		{"a name with a colon", func(r *StartRequest) { r.Name = "id:desktop" }},
		{"no name", func(r *StartRequest) { r.Name = "" }},
		{"no os", func(r *StartRequest) { r.OS = " " }},
		{"device with a callback", func(r *StartRequest) { r.Flow = FlowDevice; r.PKCEChallenge = "" }},
		{"another flow", func(r *StartRequest) { r.Flow = "magic" }},
	} {
		req := m.browser("desktop")
		c.edit(&req)
		var invalid *InvalidError
		if _, err := f.StartLogin(f.ctx, from, req); !errors.As(err, &invalid) {
			t.Errorf("%s: want it refused, got %v", c.name, err)
		}
	}
	if n := len(f.actions(t)); n != 0 {
		t.Errorf("%d rows audited for logins refused", n)
	}
}

func TestDeviceFlow(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	alice := f.user(t, "alice")
	m := newMachine(t)
	s := f.start(t, m.device("laptop"))
	if len(s.DeviceCode) < 40 || !strings.HasPrefix(s.UserCode, "WDX-") || len(s.UserCode) != len("WDX-4F7K-9QPA") {
		t.Fatalf("started %+v", s)
	}
	var i issued
	_, err := f.Poll(f.ctx, s.DeviceCode, i.issue(f.clock))
	wantErr(t, "the first poll", err, ErrPending)
	_, err = f.Poll(f.ctx, s.DeviceCode, i.issue(f.clock))
	wantErr(t, "a poll at once", err, ErrSlowDown)
	f.clock.advance(PollInterval)
	_, err = f.Poll(f.ctx, s.DeviceCode, i.issue(f.clock))
	wantErr(t, "a poll an interval later", err, ErrPending)

	// As the person may type it.
	typed := strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(s.UserCode, "WDX-"), "-", " "))
	l, err := f.LoginByUserCode(f.ctx, typed)
	if err != nil || l.ID != s.ID || l.Address != "192.0.2.1" || l.Name != "laptop" {
		t.Fatalf("found by %q: %+v, %v", typed, l, err)
	}
	_, err = f.LoginByUserCode(f.ctx, "WDX-AAAA-AAAA")
	wantErr(t, "another code", err, ErrLoginNotFound)
	_, err = f.LoginByUserCode(f.ctx, "WDX-0000-0000")
	wantErr(t, "not a code", err, ErrLoginNotFound)

	a, err := f.Approve(f.ctx, from, alice, l.ID, "laptop")
	if err != nil || a.Code != "" {
		t.Fatalf("approved %+v, %v", a, err)
	}
	f.clock.advance(PollInterval)
	c, err := f.Poll(f.ctx, s.DeviceCode, i.issue(f.clock))
	if err != nil || c.Node.ID != a.Node.ID || c.Username != "alice" {
		t.Fatalf("credentials %+v, %v", c, err)
	}
	f.clock.advance(PollInterval)
	_, err = f.Poll(f.ctx, s.DeviceCode, i.issue(f.clock))
	wantErr(t, "polled again", err, ErrLoginNotFound)
	_, err = f.LoginByUserCode(f.ctx, s.UserCode)
	wantErr(t, "the user code again", err, ErrLoginNotFound)
}

func TestDeny(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	alice := f.user(t, "alice")
	s := f.start(t, newMachine(t).device("laptop"))
	if err := f.Deny(f.ctx, from, alice, s.ID); err != nil {
		t.Fatal(err)
	}
	var i issued
	_, err := f.Poll(f.ctx, s.DeviceCode, i.issue(f.clock))
	wantErr(t, "polled", err, ErrDenied)
	_, err = f.Approve(f.ctx, from, alice, s.ID, "laptop")
	wantErr(t, "approved after", err, ErrLoginDone)
	wantErr(t, "denied again", f.Deny(f.ctx, from, alice, s.ID), ErrLoginDone)
	if got := f.actions(t); !slices.Equal(got, []string{ActionLoginStarted, ActionDenied}) {
		t.Errorf("audit %v", got)
	}
}

// A login's start is audited only once a user answers it: anyone may start
// one, and a login no one answers would grow the chain with no account.
func TestLoginStartedAudited(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	alice := f.user(t, "alice")
	unanswered := f.start(t, newMachine(t).device("laptop"))
	f.start(t, newMachine(t).browser("desktop"))
	if got := f.actions(t); len(got) != 0 {
		t.Fatalf("audit %v after two starts, want none", got)
	}
	started := f.clock.now()
	f.clock.advance(time.Minute)
	if err := f.Deny(f.ctx, By{Actor: audit.ActorUser, Address: "198.51.100.7"}, alice, unanswered.ID); err != nil {
		t.Fatal(err)
	}
	var userID, address, details string
	if err := f.db.QueryRow("SELECT user_id, address, details FROM audit WHERE action = ?", ActionLoginStarted).Scan(&userID, &address, &details); err != nil {
		t.Fatal(err)
	}
	if userID != alice || address != from.Address || !strings.Contains(details, `"started_at":"`+started.Format(time.RFC3339Nano)+`"`) {
		t.Errorf("login started row: user %q, address %q, details %s; want %q from %q, started at %v", userID, address, details, alice, from.Address, started)
	}
	if got := f.actions(t); !slices.Equal(got, []string{ActionLoginStarted, ActionDenied}) {
		t.Errorf("audit %v", got)
	}
}

func TestExpiry(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	alice := f.user(t, "alice")
	m := newMachine(t)
	f.secret(m.verifier)
	device := f.start(t, m.device("laptop"))
	browser := f.start(t, m.browser("desktop"))
	unfinished := f.start(t, m.browser("tower"))
	a, err := f.Approve(f.ctx, from, alice, unfinished.ID, "tower")
	if err != nil {
		t.Fatal(err)
	}
	f.secret(a.Code)
	f.clock.advance(LoginLife - time.Millisecond)
	if _, err := f.Login(f.ctx, browser.ID); err != nil {
		t.Fatalf("just before the end: %v", err)
	}
	f.clock.advance(time.Millisecond)
	_, err = f.Login(f.ctx, browser.ID)
	wantErr(t, "at the end", err, ErrLoginExpired)
	_, err = f.Approve(f.ctx, from, alice, browser.ID, "desktop")
	wantErr(t, "approved", err, ErrLoginExpired)
	wantErr(t, "denied", f.Deny(f.ctx, from, alice, browser.ID), ErrLoginExpired)
	var i issued
	_, err = f.Poll(f.ctx, device.DeviceCode, i.issue(f.clock))
	wantErr(t, "polled", err, ErrLoginExpired)
	_, err = f.Exchange(f.ctx, a.Code, m.verifier, i.issue(f.clock))
	wantErr(t, "exchanged", err, ErrLoginExpired)

	e, err := f.DeleteExpired(f.ctx)
	if err != nil || e.Logins != 3 || e.Nodes != 1 {
		t.Fatalf("deleted %+v, %v; want 3 logins and the node never finished", e, err)
	}
	if list, err := f.Nodes(f.ctx, alice); err != nil || len(list) != 0 {
		t.Errorf("nodes %+v, %v", list, err)
	}
	_, err = f.Login(f.ctx, browser.ID)
	wantErr(t, "once deleted", err, ErrLoginNotFound)
}

// approve makes a node of userID named name, its login finished.
func (f *fixture) approve(t *testing.T, userID, name string) (Node, machine) {
	t.Helper()
	m := newMachine(t)
	f.secret(m.verifier)
	a, err := f.Approve(f.ctx, from, userID, f.start(t, m.browser(name)).ID, name)
	if err != nil {
		t.Fatal(err)
	}
	f.secret(a.Code)
	var i issued
	c, err := f.Exchange(f.ctx, a.Code, m.verifier, i.issue(f.clock))
	if err != nil {
		t.Fatal(err)
	}
	return c.Node, m
}

func TestNameTaken(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	alice, bob := f.user(t, "alice"), f.user(t, "bob")
	desktop, _ := f.approve(t, alice, "desktop")
	s := f.start(t, newMachine(t).device("desktop"))
	_, err := f.Approve(f.ctx, from, alice, s.ID, "Desktop")
	wantErr(t, "a name the account has, in another case", err, ErrNameTaken)
	if taken, err := f.NameTaken(f.ctx, alice, "DESKTOP"); err != nil || !taken {
		t.Errorf("NameTaken = %v, %v", taken, err)
	}
	if _, err := f.Approve(f.ctx, from, alice, s.ID, "desktop-2"); err != nil {
		t.Fatalf("asked again with another name: %v", err)
	}
	f.approve(t, bob, "desktop") // another account's
	if _, err := f.Remove(f.ctx, From(desktop), alice, "desktop"); err != nil {
		t.Fatal(err)
	}
	f.approve(t, alice, "desktop") // free once removed
}

// From is a node acting for its user.
func From(n Node) By { return By{Actor: audit.ActorUser, Address: "192.0.2.9", NodeID: n.ID} }

func TestScopedToTheUser(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	alice, bob := f.user(t, "alice"), f.user(t, "bob")
	desktop, _ := f.approve(t, alice, "desktop")
	laptop, _ := f.approve(t, bob, "laptop")
	if list, err := f.Nodes(f.ctx, bob); err != nil || len(list) != 1 || list[0].ID != laptop.ID {
		t.Fatalf("bob's nodes %+v, %v", list, err)
	}
	for _, ref := range []string{"desktop", "id:" + desktop.ID} {
		_, err := f.Node(f.ctx, bob, ref)
		wantErr(t, "alice's node as bob, by "+ref, err, ErrNotFound)
		_, err = f.Remove(f.ctx, From(laptop), bob, ref)
		wantErr(t, "alice's node removed by bob, by "+ref, err, ErrNotFound)
	}
	if n, err := f.Node(f.ctx, alice, "DESKTOP"); err != nil || n.ID != desktop.ID {
		t.Fatalf("alice's own: %+v, %v", n, err)
	}
	if n, err := f.Node(f.ctx, alice, "id:"+desktop.ID); err != nil || n.ID != desktop.ID {
		t.Fatalf("alice's own by id: %+v, %v", n, err)
	}
}

func TestAuthenticate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	alice, bob, carol := f.user(t, "alice"), f.user(t, "bob"), f.user(t, "carol")
	desktop, m := f.approve(t, alice, "desktop")
	if n, err := f.Authenticate(f.ctx, desktop.ID, m.pub); err != nil || n.UserID != alice {
		t.Fatalf("authenticated %+v, %v", n, err)
	}
	_, err := f.Authenticate(f.ctx, desktop.ID, newMachine(t).pub)
	wantErr(t, "another key", err, ErrRevoked)
	_, err = f.Authenticate(f.ctx, "node-00000000", m.pub)
	wantErr(t, "no such node", err, ErrRevoked)

	laptop, lm := f.approve(t, bob, "laptop")
	f.exec(t, "UPDATE users SET disabled_at = 1 WHERE id = ?", bob)
	_, err = f.Authenticate(f.ctx, laptop.ID, lm.pub)
	wantErr(t, "a disabled user's", err, ErrDisabled)
	tower, tm := f.approve(t, carol, "tower")
	f.exec(t, "UPDATE users SET removed_at = 1 WHERE id = ?", carol)
	_, err = f.Authenticate(f.ctx, tower.ID, tm.pub)
	wantErr(t, "a removed user's", err, ErrRevoked)

	if _, err := f.Remove(f.ctx, Admin, alice, "desktop"); err != nil {
		t.Fatal(err)
	}
	_, err = f.Authenticate(f.ctx, desktop.ID, m.pub)
	wantErr(t, "a node removed", err, ErrRevoked)
}

// conn is a connection registered as the server registers one, which notes
// the reason it was closed with.
type conn struct {
	*Conn
	closed chan Reason
}

func (f *fixture) connect(n Node) conn {
	closed := make(chan Reason, 1)
	return conn{Conn: f.Registry().Add(n.ID, n.UserID, func(r Reason) { closed <- r }, nil), closed: closed}
}

// reason is the reason c was closed with, or empty when it was not.
func (c conn) reason() Reason {
	select {
	case r := <-c.closed:
		return r
	default:
		return ""
	}
}

func TestRemoveCutsOff(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	alice := f.user(t, "alice")
	desktop, _ := f.approve(t, alice, "desktop")
	laptop, _ := f.approve(t, alice, "laptop")
	cd, cl := f.connect(desktop), f.connect(laptop)
	if !f.Registry().Online(desktop.ID) {
		t.Fatal("want the desktop online")
	}
	if _, err := f.Remove(f.ctx, From(laptop), alice, "desktop"); err != nil {
		t.Fatal(err)
	}
	if r := cd.reason(); r != ReasonRevoked {
		t.Errorf("the desktop closed with %q, want revoked", r)
	}
	if r := cl.reason(); r != "" {
		t.Errorf("the laptop closed with %q", r)
	}
	f.Registry().Remove(cd.Conn)
	if f.Registry().Online(desktop.ID) || !f.Registry().Online(laptop.ID) {
		t.Error("want the desktop offline, the laptop online")
	}
	if _, err := f.Remove(f.ctx, From(laptop), alice, "laptop"); err != nil {
		t.Fatal(err)
	}
	if r := cl.reason(); r != ReasonRevoked {
		t.Errorf("the laptop closed with %q, want revoked", r)
	}
	want := []string{ActionLoginStarted, ActionApproved, ActionLoginStarted, ActionApproved, ActionRemoved, ActionCutOff, ActionLoggedOut, ActionCutOff}
	if got := f.actions(t); !slices.Equal(got, want) {
		t.Errorf("audit %v, want %v", got, want)
	}
}

func TestCutOffUser(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	alice, bob := f.user(t, "alice"), f.user(t, "bob")
	desktop, _ := f.approve(t, alice, "desktop")
	laptop, _ := f.approve(t, alice, "laptop")
	tower, _ := f.approve(t, bob, "tower")
	cd, cl, ct := f.connect(desktop), f.connect(laptop), f.connect(tower)
	f.CutOffUser(f.ctx, alice, ReasonDisabled)
	if cd.reason() != ReasonDisabled || cl.reason() != ReasonDisabled {
		t.Error("want both of alice's nodes closed with disabled")
	}
	if r := ct.reason(); r != "" {
		t.Errorf("bob's node closed with %q", r)
	}
	f.Registry().CloseAll(ReasonShuttingDown)
	if r := ct.reason(); r != ReasonShuttingDown {
		t.Errorf("bob's node closed with %q, want shutting_down", r)
	}
}

func TestRenewedAndSeen(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	alice := f.user(t, "alice")
	desktop, m := f.approve(t, alice, "desktop")
	if err := f.Connected(f.ctx, desktop.ID, "0.6.0", "darwin/arm64"); err != nil {
		t.Fatal(err)
	}
	expires := f.clock.now().Add(400 * 24 * time.Hour)
	if err := f.CertificateRenewed(f.ctx, desktop, expires); err != nil {
		t.Fatal(err)
	}
	n, err := f.Authenticate(f.ctx, desktop.ID, m.pub)
	if err != nil || n.Version != "0.6.0" || n.OS != "darwin/arm64" || !n.LastSeenAt.Equal(f.clock.now()) || !n.CertExpiresAt.Equal(expires) {
		t.Fatalf("node %+v, %v", n, err)
	}
	f.clock.advance(time.Minute)
	if err := f.Seen(f.ctx, desktop.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := f.Node(f.ctx, alice, "desktop"); !n.LastSeenAt.Equal(f.clock.now()) {
		t.Errorf("last seen %v, want %v", n.LastSeenAt, f.clock.now())
	}
	if got := f.actions(t); got[len(got)-1] != ActionCertificateRenewed {
		t.Errorf("audit %v", got)
	}
}

func TestSendUser(t *testing.T) {
	t.Parallel()
	r := newRegistry()
	var got []string
	add := func(nodeID, userID string) {
		r.Add(nodeID, userID, func(Reason) {}, func(v any) bool { got = append(got, nodeID+":"+v.(string)); return true })
	}
	add("desktop", "alice")
	add("desktop", "alice") // the newest of two takes it, once
	add("laptop", "alice")
	add("phone", "alice")
	add("tower", "bob")
	sent := r.SendUser("alice", "laptop", "m")
	slices.Sort(sent)
	slices.Sort(got)
	if want := []string{"desktop", "phone"}; !slices.Equal(sent, want) {
		t.Errorf("sent to %v, want %v", sent, want)
	}
	if want := []string{"desktop:m", "phone:m"}; !slices.Equal(got, want) {
		t.Errorf("handed %v, want %v", got, want)
	}
}
