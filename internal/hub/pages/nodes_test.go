package pages_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/hub/audit"
	"github.com/srjn45/wdx/internal/hub/node"
)

// startLogin starts a node login of flow named name, as a machine at
// 192.0.2.7 would.
func (h *hub) startLogin(flow node.Flow, name string) node.Started {
	h.t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		h.t.Fatal(err)
	}
	req := node.StartRequest{Flow: flow, Name: name, OS: "linux/amd64", Version: "0.5.0", PublicKey: pub}
	if flow == node.FlowBrowser {
		sum := sha256.Sum256([]byte(rand.Text()))
		req.PKCEChallenge, req.Callback = base64.RawURLEncoding.EncodeToString(sum[:]), "http://127.0.0.1:43210/callback"
	}
	s, err := h.server.Nodes().StartLogin(h.ctx, node.By{Actor: audit.ActorUser, Address: "192.0.2.7"}, req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.secret(s.DeviceCode, s.UserCode, node.NormalUserCode(s.UserCode))
	return s
}

func TestApprovalPage(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	b := h.browser()
	h.signUp(b, "alice")
	s := h.startLogin(node.FlowBrowser, "desktop")
	r := b.get("/nodes/approve?login=" + s.ID)
	r.want(t, http.StatusOK, "", "wdx on <strong>desktop</strong> wants to join your account", "linux/amd64", "0.5.0", "192.0.2.7", `value="desktop"`)
	// Approving sends the browser to the callback, which the form may.
	if csp := r.header.Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'self' http://127.0.0.1:43210;") {
		t.Errorf("CSP %q does not let the form go on to the callback", csp)
	}
	// No other page lets a form go there.
	if csp := b.get("/security").header.Get("Content-Security-Policy"); strings.Contains(csp, "127.0.0.1") {
		t.Errorf("the Security page's CSP %q", csp)
	}
}

// A device login is approved only by typing its code: a link to its approval
// page, sent to someone else, gets its sender nothing.
func TestDeviceLoginNeedsItsCode(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	b := h.browser()
	h.signUp(b, "alice")
	s := h.startLogin(node.FlowDevice, "laptop")
	b.get("/nodes/approve?login="+s.ID).want(t, http.StatusNotFound, "", "Type the code instead")
	b.post("/nodes/approve", map[string]string{"login": s.ID, "name": "laptop"}).want(t, http.StatusNotFound, "", "No such login")
	b.post("/nodes/deny", map[string]string{"login": s.ID}).want(t, http.StatusNotFound, "", "No such login")
	if l, err := h.server.Nodes().Login(h.ctx, s.ID); err != nil || l.Status != node.StatusPending {
		t.Fatalf("want it pending, got %+v, %v", l, err)
	}

	page := b.submit(b.get("/device"), "/device", map[string]string{"code": strings.ToLower(s.UserCode)})
	page.want(t, http.StatusOK, "/device", "wdx on <strong>laptop</strong> wants to join your account")
	b.submit(page, "/nodes/approve", nil).want(t, http.StatusOK, "/nodes/approve", "is a node of your account now")
	if l, err := h.server.Nodes().Login(h.ctx, s.ID); err != nil || l.Status != node.StatusApproved {
		t.Fatalf("want it approved, got %+v, %v", l, err)
	}
}

func TestWrongUserCodesLimited(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	b := h.browser()
	h.signUp(b, "alice")
	form := b.get("/device")
	// The sign-up's own tries count too.
	for range 21 {
		r := b.submit(form, "/device", map[string]string{"code": "WDX-AAAA-AAAA"})
		if r.status == http.StatusTooManyRequests {
			r.want(t, http.StatusTooManyRequests, "", "Too many tries")
			return
		}
		r.want(t, http.StatusUnprocessableEntity, "", "That code is unknown")
	}
	t.Fatal("21 wrong codes in a minute, none refused")
}

// An unknown login on the approval page counts against the address, as a
// wrong code does, so that ids cannot be tried at will; a known one does not.
func TestUnknownLoginsLimited(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	b := h.browser()
	h.signUp(b, "alice")
	s := h.startLogin(node.FlowBrowser, "desktop")
	for range 25 {
		b.get("/nodes/approve?login="+s.ID).want(t, http.StatusOK, "", "wants to join your account")
	}
	// The sign-up's own tries count too.
	for range 21 {
		r := b.get("/nodes/approve?login=nl-00000000000000000000000000000000")
		if r.status == http.StatusTooManyRequests {
			r.want(t, http.StatusTooManyRequests, "", "Too many tries")
			return
		}
		r.want(t, http.StatusNotFound, "", "No such login")
	}
	t.Fatal("21 unknown logins in a minute, none refused")
}

func TestDevicePageNeedsALogin(t *testing.T) {
	t.Parallel()
	h := startHub(t, nil)
	b := h.browser()
	b.get("/device").want(t, http.StatusOK, "/login?next=%2Fdevice", "Log in")
}
