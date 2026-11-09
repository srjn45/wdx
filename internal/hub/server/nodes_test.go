package server_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/srjn45/wdx/internal/hub/ca"
	"github.com/srjn45/wdx/internal/hub/hubtest"
	"github.com/srjn45/wdx/internal/hub/node"
	"github.com/srjn45/wdx/internal/hub/server"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/waittest"
)

// machine is a machine logging in: its key and PKCE verifier.
type machine struct {
	pub      ed25519.PublicKey
	key      ed25519.PrivateKey
	verifier string
}

func newMachine(t *testing.T, h *hubtest.Hub) machine {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m := machine{pub: pub, key: key, verifier: rand.Text() + rand.Text()}
	h.Secret(m.verifier)
	return m
}

func (m machine) challenge() string {
	sum := sha256.Sum256([]byte(m.verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

const callback = "http://127.0.0.1:43210/callback"

func (m machine) browser(name string) hubapi.StartNodeLogin {
	challenge, cb := m.challenge(), callback
	return hubapi.StartNodeLogin{Flow: hubapi.NodeLoginFlowBrowser, Name: name, Os: "linux/amd64", WdxVersion: "0.5.0", PublicKey: m.pub, PkceChallenge: &challenge, Callback: &cb}
}

func (m machine) device(name string) hubapi.StartNodeLogin {
	return hubapi.StartNodeLogin{Flow: hubapi.NodeLoginFlowDevice, Name: name, Os: "linux/amd64", WdxVersion: "0.5.0", PublicKey: m.pub}
}

func code(t *testing.T, b []byte) hubapi.ErrorCode {
	t.Helper()
	var p hubapi.Problem
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("not a problem: %s", b)
	}
	return p.Code
}

func wantProblem(t *testing.T, what string, status int, body []byte, wantStatus int, want hubapi.ErrorCode) {
	t.Helper()
	if status != wantStatus || code(t, body) != want {
		t.Fatalf("%s: got %d %s, want %d %s", what, status, body, wantStatus, want)
	}
}

// startBrowser starts a browser login of m and returns its login_url.
func startBrowser(t *testing.T, h *hubtest.Hub, m machine, name string) string {
	t.Helper()
	r, err := h.WebClient().StartNodeLoginWithResponse(context.Background(), m.browser(name))
	if err != nil || r.JSON201 == nil || r.JSON201.LoginUrl == nil || r.JSON201.DeviceCode != nil {
		t.Fatalf("start: %v %d %s", err, r.StatusCode(), r.Body)
	}
	return *r.JSON201.LoginUrl
}

// startDevice starts a device login of m and returns it.
func startDevice(t *testing.T, h *hubtest.Hub, m machine, name string) hubapi.NodeLoginStarted {
	t.Helper()
	r, err := h.WebClient().StartNodeLoginWithResponse(context.Background(), m.device(name))
	if err != nil || r.JSON201 == nil || r.JSON201.DeviceCode == nil || r.JSON201.LoginUrl != nil {
		t.Fatalf("start: %v %d %s", err, r.StatusCode(), r.Body)
	}
	h.Secret(*r.JSON201.DeviceCode, *r.JSON201.UserCode)
	return *r.JSON201
}

// wantCredentials checks c are a node's named name, with a certificate of
// m's key that the hub's CA issued for it.
func wantCredentials(t *testing.T, h *hubtest.Hub, c *hubapi.NodeCredentials, m machine, name string) tls.Certificate {
	t.Helper()
	if c == nil || c.Node.Name != name || c.User != h.User.Username || c.NodesUrl != h.NodesURL || !strings.HasPrefix(c.Node.Id, "node-") {
		t.Fatalf("credentials %+v", c)
	}
	block, _ := pem.Decode([]byte(c.Certificate))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := ca.NodeID(cert); !ok || id != c.Node.Id || !cert.PublicKey.(ed25519.PublicKey).Equal(m.pub) {
		t.Fatalf("the certificate names %v for %v", cert.URIs, cert.PublicKey)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(c.CaCertificate)) {
		t.Fatal("no CA certificate")
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("not issued by the CA given: %v", err)
	}
	return hubtest.Certificate(t, c.Certificate, m.key)
}

func TestBrowserLogin(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	ctx := context.Background()
	m := newMachine(t, h)
	loginURL := startBrowser(t, h, m, "desktop")
	if !strings.HasPrefix(loginURL, hubtest.PublicURL+"/nodes/approve?login=nl-") {
		t.Fatalf("login_url %s", loginURL)
	}
	back, err := h.Browser(h.User).Approve(loginURL, "")
	if err != nil {
		t.Fatal(err)
	}
	q := back.Query()
	login, _ := url.Parse(loginURL)
	if back.Scheme+"://"+back.Host+back.Path != callback || q.Get("code") == "" || q.Get("state") != login.Query().Get("login") {
		t.Fatalf("sent back to %s", back)
	}
	web := h.WebClient()
	wrong, err := web.ExchangeNodeLoginWithResponse(ctx, hubapi.ExchangeNodeLogin{Code: q.Get("code"), PkceVerifier: newMachine(t, h).verifier})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, "another verifier", wrong.StatusCode(), wrong.Body, http.StatusUnprocessableEntity, hubapi.ErrorCodeInvalidGrant)
	r, err := web.ExchangeNodeLoginWithResponse(ctx, hubapi.ExchangeNodeLogin{Code: q.Get("code"), PkceVerifier: m.verifier})
	if err != nil {
		t.Fatal(err)
	}
	cert := wantCredentials(t, h, r.JSON200, m, "desktop")
	again, err := web.ExchangeNodeLoginWithResponse(ctx, hubapi.ExchangeNodeLogin{Code: q.Get("code"), PkceVerifier: m.verifier})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, "the code again", again.StatusCode(), again.Body, http.StatusNotFound, hubapi.ErrorCodeNotFound)

	n := hubtest.Node{ID: r.JSON200.Node.Id, Name: "desktop", Key: m.key, Certificate: cert}
	list, err := h.NodesClient(n).ListNodesWithResponse(ctx)
	if err != nil || list.JSON200 == nil || len(list.JSON200.Nodes) != 1 || !list.JSON200.Nodes[0].This || list.JSON200.Nodes[0].Version != "0.5.0" {
		t.Fatalf("nodes: %v %s", err, list.Body)
	}
	if len(h.Logged("login approved")) != 1 || len(h.Logged("login finished")) != 1 {
		t.Error("want the approval and the finish logged")
	}
}

func TestBrowserLoginDenied(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	m := newMachine(t, h)
	loginURL := startBrowser(t, h, m, "desktop")
	back, err := h.Browser(h.User).Deny(loginURL)
	if err != nil {
		t.Fatal(err)
	}
	if back.Query().Get("error") != "access_denied" || back.Query().Get("code") != "" || back.Query().Get("state") == "" {
		t.Fatalf("sent back to %s", back)
	}
}

func TestCallbackNotLoopbackRefused(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	for _, cb := range []string{"http://evil.example.com:43210/callback", "http://localhost:43210/callback", "https://127.0.0.1:43210/"} {
		req := newMachine(t, h).browser("desktop")
		req.Callback = &cb
		r, err := h.WebClient().StartNodeLoginWithResponse(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		wantProblem(t, cb, r.StatusCode(), r.Body, http.StatusUnprocessableEntity, hubapi.ErrorCodeInvalidArgument)
	}
}

func TestDeviceLogin(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	ctx := context.Background()
	m := newMachine(t, h)
	s := startDevice(t, h, m, "laptop")
	if *s.VerificationUrl != hubtest.PublicURL+"/device" || *s.Interval != 5 || strings.Contains(*s.VerificationUrl, *s.UserCode) {
		t.Fatalf("started %+v", s)
	}
	web := h.WebClient()
	poll := func() *hubapi.PollNodeLoginResponse {
		r, err := web.PollNodeLoginWithResponse(ctx, hubapi.PollNodeLogin{DeviceCode: *s.DeviceCode})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := poll()
	wantProblem(t, "the first poll", r.StatusCode(), r.Body, http.StatusConflict, hubapi.ErrorCodeAuthorizationPending)
	r = poll()
	wantProblem(t, "a poll at once", r.StatusCode(), r.Body, http.StatusTooManyRequests, hubapi.ErrorCodeSlowDown)
	if err := h.Browser(h.User).ApproveCode(*s.UserCode, ""); err != nil {
		t.Fatal(err)
	}
	h.Clock.Advance(node.PollInterval)
	r = poll()
	wantCredentials(t, h, r.JSON200, m, "laptop")
	h.Clock.Advance(node.PollInterval)
	r = poll()
	wantProblem(t, "polled again", r.StatusCode(), r.Body, http.StatusNotFound, hubapi.ErrorCodeNotFound)
}

func TestDeviceLoginDeniedAndExpired(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	ctx := context.Background()
	web := h.WebClient()
	denied := startDevice(t, h, newMachine(t, h), "laptop")
	if err := h.Browser(h.User).DenyCode(*denied.UserCode); err != nil {
		t.Fatal(err)
	}
	r, err := web.PollNodeLoginWithResponse(ctx, hubapi.PollNodeLogin{DeviceCode: *denied.DeviceCode})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, "denied", r.StatusCode(), r.Body, http.StatusForbidden, hubapi.ErrorCodeAccessDenied)

	late := startDevice(t, h, newMachine(t, h), "tower")
	h.Clock.Advance(node.LoginLife)
	r, err = web.PollNodeLoginWithResponse(ctx, hubapi.PollNodeLogin{DeviceCode: *late.DeviceCode})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, "expired", r.StatusCode(), r.Body, http.StatusGone, hubapi.ErrorCodeExpiredToken)
	var page *hubtest.Page
	if err := h.Browser(h.User).ApproveCode(*late.UserCode, ""); !errors.As(err, &page) || page.Status != http.StatusGone {
		t.Fatalf("approving it: want the expired page, got %v", err)
	}
}

func TestNameAskedAgainWhenTaken(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	h.NewNode(h.User, "desktop")
	m := newMachine(t, h)
	loginURL := startBrowser(t, h, m, "Desktop")
	b := h.Browser(h.User)
	if _, err := b.Approve(loginURL, ""); !errors.Is(err, hubtest.ErrNameTaken) {
		t.Fatalf("want the name asked again, got %v", err)
	}
	back, err := b.Approve(loginURL, "desktop-2")
	if err != nil || back.Query().Get("code") == "" {
		t.Fatalf("with another name: %v %v", back, err)
	}
	r, err := h.WebClient().ExchangeNodeLoginWithResponse(context.Background(), hubapi.ExchangeNodeLogin{Code: back.Query().Get("code"), PkceVerifier: m.verifier})
	if err != nil {
		t.Fatal(err)
	}
	wantCredentials(t, h, r.JSON200, m, "desktop-2")
}

func TestApprovalNeedsARecentSecondFactor(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	b := h.Browser(h.User)
	first := startDevice(t, h, newMachine(t, h), "laptop")
	if err := b.ApproveCode(*first.UserCode, ""); err != nil {
		t.Fatal(err)
	}
	logins := len(h.Logged("second factor passed"))
	second := startDevice(t, h, newMachine(t, h), "tower")
	if err := b.ApproveCode(*second.UserCode, ""); err != nil {
		t.Fatal(err)
	}
	if n := len(h.Logged("second factor passed")); n != logins {
		t.Fatalf("logged in again within 10 minutes: %d logins, %d before", n, logins)
	}
	h.Clock.Advance(11 * time.Minute)
	third := startDevice(t, h, newMachine(t, h), "box")
	if err := b.ApproveCode(*third.UserCode, ""); err != nil {
		t.Fatal(err)
	}
	if n := len(h.Logged("second factor passed")); n != logins+1 || len(h.Logged("page needs a fresh login")) != 1 {
		t.Fatalf("want a login again after 10 minutes: %d logins, %d before", n, logins)
	}
}

func TestLoginStartsRateLimited(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	web := h.WebClient()
	m := newMachine(t, h)
	for i := range 11 {
		r, err := web.StartNodeLoginWithResponse(context.Background(), m.device("laptop"))
		if err != nil {
			t.Fatal(err)
		}
		if i < 10 {
			if r.JSON201 == nil {
				t.Fatalf("start %d: %d %s", i, r.StatusCode(), r.Body)
			}
			h.Secret(*r.JSON201.DeviceCode, *r.JSON201.UserCode)
			continue
		}
		wantProblem(t, "the 11th in a minute", r.StatusCode(), r.Body, http.StatusTooManyRequests, hubapi.ErrorCodeRateLimited)
	}
}

func TestNodesScopedToTheAccount(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	ctx := context.Background()
	desktop := h.NewNode(h.User, "desktop")
	laptop := h.NewNode(h.User, "laptop")
	bob := h.AddUser("bob")
	tower := h.NewNode(bob, "tower")

	list, err := h.NodesClient(tower).ListNodesWithResponse(ctx)
	if err != nil || list.JSON200 == nil || len(list.JSON200.Nodes) != 1 || list.JSON200.Nodes[0].Id != tower.ID {
		t.Fatalf("bob's nodes: %v %s", err, list.Body)
	}
	for _, ref := range []string{"desktop", "id:" + desktop.ID} {
		got, err := h.NodesClient(tower).GetNodeWithResponse(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		wantProblem(t, "alice's node as bob, by "+ref, got.StatusCode(), got.Body, http.StatusNotFound, hubapi.ErrorCodeNotFound)
		removed, err := h.NodesClient(tower).RemoveNodeWithResponse(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		wantProblem(t, "alice's node removed by bob, by "+ref, removed.StatusCode(), removed.Body, http.StatusNotFound, hubapi.ErrorCodeNotFound)
	}
	got, err := h.NodesClient(laptop).GetNodeWithResponse(ctx, "DESKTOP")
	if err != nil || got.JSON200 == nil || got.JSON200.Node.Id != desktop.ID || got.JSON200.Node.This || got.JSON200.Node.Fingerprint != node.Fingerprint(desktop.Key.Public().(ed25519.PublicKey)) {
		t.Fatalf("alice's desktop from her laptop: %v %s", err, got.Body)
	}
	list, err = h.NodesClient(laptop).ListNodesWithResponse(ctx)
	if err != nil || list.JSON200 == nil || len(list.JSON200.Nodes) != 2 || list.JSON200.Nodes[0].Name != "desktop" || !list.JSON200.Nodes[1].This {
		t.Fatalf("alice's nodes: %v %s", err, list.Body)
	}
}

// control is a node's control connection, as wdx keeps it.
type control struct {
	t    *testing.T
	conn *websocket.Conn
	ctx  context.Context
}

// connect opens n's control connection and says hello with apiVersion.
func connect(t *testing.T, h *hubtest.Hub, n hubtest.Node, apiVersion string) control {
	t.Helper()
	ctx, cancel := context.WithDeadline(context.Background(), waittest.Deadline(t))
	t.Cleanup(cancel)
	conn, resp, err := websocket.Dial(ctx, h.NodesURL+"/v1/control", &websocket.DialOptions{HTTPClient: h.NodeClient(n.Certificate)})
	if err != nil {
		t.Fatalf("dial: %v (%v)", err, resp)
	}
	t.Cleanup(func() { _ = conn.CloseNow() }) // closed by the test, or by the hub
	c := control{t: t, conn: conn, ctx: ctx}
	if err := wsjson.Write(ctx, conn, hubapi.ControlHello{Type: hubapi.ControlHelloTypeHello, ApiVersion: apiVersion, WdxVersion: "0.6.0", Os: "darwin/arm64"}); err != nil {
		t.Fatal(err)
	}
	return c
}

// read returns the next message, its type and the whole of it.
func (c control) read() (string, map[string]any) {
	c.t.Helper()
	var m map[string]any
	if err := wsjson.Read(c.ctx, c.conn, &m); err != nil {
		c.t.Fatalf("read: %v", err)
	}
	typ, _ := m["type"].(string)
	return typ, m
}

func (c control) want(typ string) map[string]any {
	c.t.Helper()
	got, m := c.read()
	if got != typ {
		c.t.Fatalf("got %s %v, want %s", got, m, typ)
	}
	return m
}

// wantBye checks the next message is a bye with reason, and the close after.
func (c control) wantBye(reason string) {
	c.t.Helper()
	if m := c.want("bye"); m["reason"] != reason {
		c.t.Fatalf("bye %v, want %s", m, reason)
	}
	if _, _, err := c.conn.Read(c.ctx); websocket.CloseStatus(err) == -1 {
		c.t.Fatalf("want the connection closed after the bye, got %v", err)
	}
}

func TestControlWelcome(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	n := h.NewNode(h.User, "desktop")
	c := connect(t, h, n, "1")
	welcome := c.want("welcome")
	if node, _ := welcome["node"].(map[string]any); node["id"] != n.ID || node["name"] != "desktop" || welcome["api_version"] != "1" {
		t.Fatalf("welcome %v", welcome)
	}
	list, err := h.NodesClient(n).ListNodesWithResponse(context.Background())
	if err != nil || list.JSON200 == nil || !list.JSON200.Nodes[0].Online || list.JSON200.Nodes[0].Version != "0.6.0" || list.JSON200.Nodes[0].Os != "darwin/arm64" || list.JSON200.Nodes[0].LastSeenAt == nil {
		t.Fatalf("want it online with the hello's version: %v %s", err, list.Body)
	}
}

func TestControlVersionUnsupported(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	n := h.NewNode(h.User, "desktop")
	for version, behind := range map[string]string{"0": hubapi.BehindNode, "2": hubapi.BehindHub, "one": hubapi.BehindNode} {
		c := connect(t, h, n, version)
		_, _, err := c.conn.Read(c.ctx)
		var closed websocket.CloseError
		if !errors.As(err, &closed) || closed.Code != hubapi.CloseVersionUnsupported || closed.Reason != behind {
			t.Errorf("api_version %s: want a close %d with %s, got %v", version, hubapi.CloseVersionUnsupported, behind, err)
		}
	}
}

func TestRevokedNodeCutOffAndRefused(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	ctx := context.Background()
	desktop := h.NewNode(h.User, "desktop")
	laptop := h.NewNode(h.User, "laptop")
	c := connect(t, h, desktop, "1")
	c.want("welcome")
	r, err := h.NodesClient(laptop).RemoveNodeWithResponse(ctx, "desktop")
	if err != nil || r.JSON200 == nil || r.JSON200.Result != hubapi.ResultRemoved || r.JSON200.Node.Id != desktop.ID {
		t.Fatalf("remove: %v %s", err, r.Body)
	}
	c.wantBye("revoked")
	again, err := h.NodesClient(desktop).ListNodesWithResponse(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, "the revoked node's request", again.StatusCode(), again.Body, http.StatusForbidden, hubapi.ErrorCodeNodeRevoked)
	_, resp, err := websocket.Dial(ctx, h.NodesURL+"/v1/control", &websocket.DialOptions{HTTPClient: h.NodeClient(desktop.Certificate)})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want its control refused, got %v %v", err, resp)
	}

	// Removing itself is logging out.
	if r, err := h.NodesClient(laptop).RemoveNodeWithResponse(ctx, "id:"+laptop.ID); err != nil || r.JSON200 == nil {
		t.Fatalf("remove itself: %v %s", err, r.Body)
	}
}

// A node removed, by another or by logging out, is told to the user's other
// nodes connected: not to itself, which is cut off, nor to another user's.
func TestNodeRemovedToldToTheOthers(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	desktop, laptop, phone := h.NewNode(h.User, "desktop"), h.NewNode(h.User, "laptop"), h.NewNode(h.User, "phone")
	bob := h.AddUser("bob")
	bobsDesktop, bobsLaptop := h.NewNode(bob, "desktop"), h.NewNode(bob, "laptop")
	controls := map[string]control{}
	for _, n := range []hubtest.Node{desktop, laptop, phone, bobsDesktop} {
		controls[n.ID] = connect(t, h, n, "1")
		controls[n.ID].want("welcome")
	}
	wantRemoved := func(c control, n hubtest.Node) {
		t.Helper()
		m := c.want("node_removed")
		if node, _ := m["node"].(map[string]any); node["id"] != n.ID || node["name"] != n.Name {
			t.Fatalf("node_removed %v, want %s", m, n.Name)
		}
	}

	h.RemoveNode(desktop, "laptop")
	wantRemoved(controls[desktop.ID], laptop)
	wantRemoved(controls[phone.ID], laptop)
	for {
		typ, m := controls[laptop.ID].read()
		if typ == "node_removed" {
			t.Fatalf("the removed node was told of itself: %v", m)
		}
		if typ == "bye" {
			break
		}
	}

	h.RemoveNode(phone, "id:"+phone.ID) // a logout
	wantRemoved(controls[desktop.ID], phone)

	// Bob's desktop was told nothing: the first it hears is a stream asked.
	streamOpened(t, h, bobsLaptop, bobsDesktop)
	controls[bobsDesktop.ID].want("stream_waiting")
}

// admin calls the hub's admin socket.
func admin(t *testing.T, h *hubtest.Hub) *hubapi.ClientWithResponses {
	t.Helper()
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", h.Server.Addresses().Admin)
	}}}
	c, err := hubapi.NewClientWithResponses("http://wdx-hub", hubapi.WithHTTPClient(hc))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDisabledUsersNodesCutOff(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	ctx := context.Background()
	desktop := h.NewNode(h.User, "desktop")
	bob := h.AddUser("bob")
	tower := h.NewNode(bob, "tower")
	c, ct := connect(t, h, desktop, "1"), connect(t, h, tower, "1")
	c.want("welcome")
	ct.want("welcome")
	got, err := admin(t, h).GetUserWithResponse(ctx, "alice")
	if err != nil || got.JSON200 == nil || got.JSON200.User.Nodes != 1 || len(got.JSON200.User.NodeList) != 1 || !got.JSON200.User.NodeList[0].Online {
		t.Fatalf("alice: %v %s", err, got.Body)
	}
	r, err := admin(t, h).UpdateUserWithResponse(ctx, "alice", hubapi.UpdateUser{Disabled: true})
	if err != nil || r.JSON200 == nil {
		t.Fatalf("disable: %v %s", err, r.Body)
	}
	c.wantBye("disabled")
	again, err := h.NodesClient(desktop).ListNodesWithResponse(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, "a disabled user's node", again.StatusCode(), again.Body, http.StatusForbidden, hubapi.ErrorCodeAccountDisabled)

	if r, err := admin(t, h).RemoveUserWithResponse(ctx, "bob"); err != nil || r.JSON200 == nil || r.JSON200.User.Nodes != 1 {
		t.Fatalf("remove bob: %v %s", err, r.Body)
	}
	ct.wantBye("revoked")
	gone, err := h.NodesClient(tower).ListNodesWithResponse(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, "a removed user's node", gone.StatusCode(), gone.Body, http.StatusForbidden, hubapi.ErrorCodeNodeRevoked)
}

func TestRenewalAtTwoThirds(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	n := h.NewNode(h.User, "desktop")
	h.Clock.Advance(ca.NodeValidity*2/3 - time.Hour)
	c := connect(t, h, n, "1")
	c.want("welcome")
	// Nothing comes between the welcome and the bye: no certificate before
	// two thirds of its life.
	h.Server.Nodes().CutOffUser(context.Background(), h.User.ID, node.ReasonShuttingDown)
	c.wantBye("shutting_down")

	h.Clock.Advance(time.Hour)
	c = connect(t, h, n, "1")
	c.want("welcome")
	cert := c.want("certificate")
	pemText, _ := cert["certificate"].(string)
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		t.Fatalf("certificate %v", cert)
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !parsed.PublicKey.(ed25519.PublicKey).Equal(n.Key.Public().(ed25519.PublicKey)) {
		t.Fatalf("not a certificate of the node's key: %v", err)
	}
	expires, _ := time.Parse(time.RFC3339, cert["expires_at"].(string))
	if err := wsjson.Write(c.ctx, c.conn, hubapi.ControlCertificateSaved{Type: hubapi.ControlCertificateSavedTypeCertificateSaved, ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
	waittest.For(t, "the renewal recorded", func() bool { return len(h.Logged("certificate renewed")) == 1 })
	// Renewed, it is not due again at its next welcome.
	h.Server.Nodes().CutOffUser(context.Background(), h.User.ID, node.ReasonShuttingDown)
	c.wantBye("shutting_down")
	c = connect(t, h, n, "1")
	c.want("welcome")
	h.Server.Nodes().CutOffUser(context.Background(), h.User.ID, node.ReasonShuttingDown)
	c.wantBye("shutting_down")
}

func TestNodeGoneWithoutPongs(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t, func(o *server.Options) { o.PingInterval = 20 * time.Millisecond })
	n := h.NewNode(h.User, "desktop")
	c := connect(t, h, n, "1")
	c.want("welcome")
	// It reads nothing more, so it answers no ping.
	waittest.For(t, "the hub to close the connection", func() bool { return !h.Server.Nodes().Registry().Online(n.ID) })
	if len(h.Logged("control closed")) != 1 {
		t.Error("want the close logged")
	}
}

func TestHubStoppingSaysBye(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t)
	n := h.NewNode(h.User, "desktop")
	c := connect(t, h, n, "1")
	c.want("welcome")
	// The hub waits for its goodbye to be answered: read it meanwhile.
	type message struct {
		m   map[string]any
		err error
	}
	read := make(chan message, 1)
	go func() {
		var m map[string]any
		err := wsjson.Read(c.ctx, c.conn, &m)
		read <- message{m, err}
		_, _, _ = c.conn.Read(c.ctx) // the close, answered
	}()
	h.Stop()
	if got := <-read; got.err != nil || got.m["type"] != "bye" || got.m["reason"] != "shutting_down" {
		t.Fatalf("got %v, %v; want a bye shutting_down", got.m, got.err)
	}
}

// With nodes_url set, nodes are told it, and the node endpoint's certificate
// names its host.
func TestNodesURLSetting(t *testing.T) {
	t.Parallel()
	h := hubtest.Start(t, func(o *server.Options) { o.Config.NodesURL = "https://nodes.example.com:9443" })
	if h.NodesURL != "https://nodes.example.com:9443" {
		t.Fatalf("nodes told %s", h.NodesURL)
	}
	n := h.NewNode(h.User, "desktop")
	if n.Credentials.NodesUrl != h.NodesURL {
		t.Errorf("credentials name %s", n.Credentials.NodesUrl)
	}
	conn, err := tls.Dial("tcp", h.Server.Addresses().Nodes, &tls.Config{
		MinVersion: tls.VersionTLS13, ServerName: "nodes.example.com", RootCAs: h.CA(), Certificates: []tls.Certificate{n.Certificate},
	})
	if err != nil {
		t.Fatalf("the endpoint's certificate is not for nodes.example.com: %v", err)
	}
	conn.Close()
}
