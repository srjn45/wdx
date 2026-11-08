package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/authcode"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/clikit/clikittest"
	"github.com/srjn45/wdx/internal/hub/hubtest"
	"github.com/srjn45/wdx/internal/identity"
)

// sessionOutput is what the session commands print with --json.
type sessionOutput struct {
	jsonOutput
	Session  *api.HubSession  `json:"session"`
	Sessions []api.HubSession `json:"sessions"`
}

// sessionJSON runs args on hh with --json.
func (hh *hubHome) sessionJSON(args ...string) (sessionOutput, int) {
	hh.t.Helper()
	out := wdxWith(hh.t, hh.home, hh.env(nil), append(args, "--json")...)
	var v sessionOutput
	if err := json.Unmarshal(clikittest.JSON(hh.t, args, out, codes), &v); err != nil {
		hh.t.Fatalf("%v: stdout is not JSON: %v\n%s", args, err, out.Stdout)
	}
	wantDeclared(hh.t, args, v.jsonOutput)
	return v, out.Exit
}

// hubPair is two machines of one account on one hub: a desktop, the host,
// and a laptop, the client, both logged in.
type hubPair struct {
	hub             *hubtest.Hub
	clock           *hubClock
	desktop, laptop *hubHome
	secret          string // the desktop's code's, once set up
}

func newHubPair(t *testing.T) *hubPair {
	t.Helper()
	h := hubtest.Start(t)
	clock := newHubClock(h)
	p := &hubPair{hub: h, clock: clock, desktop: newHubHomeOn(t, h, clock), laptop: newHubHomeOn(t, h, clock)}
	p.desktop.loggedIn("desktop")
	p.laptop.loggedIn("laptop")
	p.desktop.waitConnection(api.HubConnectionConnected)
	p.laptop.waitConnection(api.HubConnectionConnected)
	return p
}

// setUp sets the desktop's code up, as wdx hub code setup there writes it.
func (p *hubPair) setUp() {
	t := p.desktop.t
	t.Helper()
	pending, err := authcode.Generate("wdx", "alice@desktop")
	if err != nil {
		t.Fatal(err)
	}
	p.secret = pending.Secret
	p.desktop.secret(pending.Secret, pending.URL)
	p.laptop.secret(pending.Secret, pending.URL)
	if _, err := authcode.Open(identity.HubDir(p.desktop.home)).Write(context.Background(), pending.Secret, authcode.Step(p.clock.Now())-10); err != nil {
		t.Fatal(err)
	}
}

// remoteAccess sets hub.remote_access on the desktop.
func (p *hubPair) remoteAccess(on bool) {
	t := p.desktop.t
	t.Helper()
	value := "false"
	if on {
		value = "true"
	}
	if _, exit := p.desktop.sessionJSON("setting", "set", "--name", "hub.remote_access", "--value", value); exit != clikit.ExitOK {
		t.Fatalf("remote access %s: exit %d", value, exit)
	}
}

// code is the desktop's code at the start of the next step, which the clock
// moves on to, so that the open it is for arrives in its step wherever in a
// step the real time is; no log line of either home may hold it.
func (p *hubPair) code() string {
	now := p.clock.Now()
	p.clock.Advance(time.Unix((authcode.Step(now)+1)*int64(authcode.Period/time.Second), 0).Sub(now))
	c := p.desktop.code(p.secret, 0)
	p.laptop.mu.Lock()
	p.laptop.codes = append(p.laptop.codes, c)
	p.laptop.mu.Unlock()
	return c
}

func TestHubSessionOpen(t *testing.T) {
	t.Parallel()
	p := newHubPair(t)
	p.setUp()
	p.remoteAccess(true)

	v, exit := p.laptop.sessionJSON("hub", "session", "open", "--node", "desktop", "--code", p.code())
	if exit != clikit.ExitOK || v.Result != string(api.ResultOpened) || v.Session.Direction != api.HubSessionDirectionOutgoing || v.Session.Node.Name != "desktop" {
		t.Fatalf("open: %d %+v %+v", exit, v.jsonOutput, v.Session)
	}
	id := v.Session.Id
	if !v.Session.IdleExpiresAt.Equal(v.Session.LastUsedAt.Add(time.Hour)) {
		t.Errorf("idle until %v, want an hour after %v, the host's timeout", v.Session.IdleExpiresAt, v.Session.LastUsedAt)
	}
	v, exit = p.laptop.sessionJSON("hub", "session", "open", "--node", "desktop", "--code", "000000")
	if exit != clikit.ExitOK || v.Result != string(api.ResultUnchanged) || v.Session.Id != id {
		t.Fatalf("open again: %d %+v", exit, v.jsonOutput)
	}

	v, _ = p.desktop.sessionJSON("hub", "session", "list")
	if len(v.Sessions) != 1 || v.Sessions[0].Id != id || v.Sessions[0].Direction != api.HubSessionDirectionIncoming || v.Sessions[0].Node.Name != "laptop" {
		t.Fatalf("the desktop's sessions %+v", v.Sessions)
	}
	if s, _ := p.desktop.hubJSON(nil, "hub", "status"); s.Hub.Sessions.Incoming != 1 || s.Hub.Sessions.Outgoing != 0 {
		t.Errorf("the desktop's status %+v", s.Hub.Sessions)
	}
	out := wdxWith(t, p.laptop.home, p.laptop.env(nil), "hub", "node", "get", "--name", "desktop", "--json")
	var node struct {
		Node api.HubNodeDetail `json:"node"`
	}
	if err := json.Unmarshal([]byte(out.Stdout), &node); err != nil || !node.Node.KeyKnown || len(node.Node.Sessions) != 1 || node.Node.Sessions[0].Id != id {
		t.Errorf("node get: %v %+v", err, node.Node)
	}

	v, exit = p.laptop.sessionJSON("hub", "session", "close", "--id", id)
	if exit != clikit.ExitOK || v.Result != string(api.ResultClosed) || v.Session.Id != id {
		t.Fatalf("close: %d %+v", exit, v.jsonOutput)
	}
	if v, _ = p.desktop.sessionJSON("hub", "session", "list"); len(v.Sessions) != 0 {
		t.Errorf("the host was not told: %+v", v.Sessions)
	}
	v, exit = p.laptop.sessionJSON("hub", "session", "close", "--id", id)
	wantCodeExit(t, v.jsonOutput, exit, api.ErrorCodeSessionNotFound, clikit.ExitRejected)

	if _, exit = p.laptop.sessionJSON("hub", "session", "open", "--node", "desktop", "--code", p.code()); exit != clikit.ExitOK {
		t.Fatalf("open: %d", exit)
	}
	v, exit = p.desktop.sessionJSON("hub", "session", "close", "--all")
	if exit != clikit.ExitOK || v.Result != string(api.ResultClosed) || len(v.Sessions) != 1 || v.Sessions[0].Direction != api.HubSessionDirectionIncoming {
		t.Fatalf("close --all: %d %+v %+v", exit, v.jsonOutput, v.Sessions)
	}
}

// wantCodeExit checks a command failed with code and exit.
func wantCodeExit(t *testing.T, v jsonOutput, exit int, code api.ErrorCode, wantExit int) {
	t.Helper()
	if v.Code != string(code) || exit != wantExit {
		t.Fatalf("got %q exit %d, want %s exit %d", v.Code, exit, code, wantExit)
	}
}

func TestHubSessionOpenRefused(t *testing.T) {
	t.Parallel()
	p := newHubPair(t)
	open := func(node, code string) (sessionOutput, int) {
		t.Helper()
		return p.laptop.sessionJSON("hub", "session", "open", "--node", node, "--code", code)
	}

	v, exit := open("desktop", "123456")
	wantCodeExit(t, v.jsonOutput, exit, api.ErrorCodeRemoteAccessOff, clikit.ExitRejected)
	wantHint(t, v.jsonOutput, "run wdx setting set --name hub.remote_access --value true on desktop")

	p.remoteAccess(true)
	v, exit = open("desktop", "123456")
	wantCodeExit(t, v.jsonOutput, exit, api.ErrorCodeCodeNotSetUp, clikit.ExitRejected)
	wantHint(t, v.jsonOutput, "run wdx hub code setup on desktop")

	p.setUp()
	wrong := p.desktop.wrongCode(p.secret)
	v, exit = open("desktop", wrong)
	wantCodeExit(t, v.jsonOutput, exit, api.ErrorCodeCodeWrong, clikit.ExitRejected)
	if got := p.desktop.auditActions(); got[len(got)-1] != "code.wrong" {
		t.Errorf("the desktop's audit chain %v", got)
	}

	v, exit = open("nosuch", "123456")
	wantCodeExit(t, v.jsonOutput, exit, api.ErrorCodeNodeNotFound, clikit.ExitRejected)

	// A node of the account that is not connected.
	p.hub.NewNode(p.hub.User, "phone")
	v, exit = open("phone", "123456")
	wantCodeExit(t, v.jsonOutput, exit, api.ErrorCodeNodeOffline, clikit.ExitRejected)

	// --json needs the code up front.
	v, exit = p.laptop.sessionJSON("hub", "session", "open", "--node", "desktop")
	wantCodeExit(t, v.jsonOutput, exit, api.ErrorCodeUsage, clikit.ExitUsage)
}

func TestHubSessionNotLoggedIn(t *testing.T) {
	t.Parallel()
	hh := newHubHome(t)
	v, exit := hh.sessionJSON("hub", "session", "open", "--node", "desktop", "--code", "123456")
	wantCodeExit(t, v.jsonOutput, exit, api.ErrorCodeNotLoggedIn, clikit.ExitRejected)
	if v, exit = hh.sessionJSON("hub", "session", "list"); exit != clikit.ExitOK || v.Sessions == nil || len(v.Sessions) != 0 {
		t.Fatalf("list: %d %+v", exit, v.Sessions)
	}
}

func TestHubSessionOpenAsks(t *testing.T) {
	t.Parallel()
	p := newHubPair(t)
	p.setUp()
	p.remoteAccess(true)

	r := p.laptop.startOnTerminal("hub", "session", "open", "--node", "desktop")
	r.asked(t, "code for desktop: ", 1)
	r.typeLine(t, p.code())
	out := p.laptop.wait(r.running)
	if out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, "opened a session into node desktop, sess-") || !strings.HasSuffix(out.Stdout, "; it closes after 1h0m0s unused\n") {
		t.Fatalf("%+v", out)
	}
	// With one open, nothing is asked.
	out = wdxWith(t, p.laptop.home, p.laptop.env(nil), "hub", "session", "open", "--node", "desktop")
	if out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, "unchanged: a session into node desktop is open already") {
		t.Fatalf("%+v", out)
	}
}

func TestHubSessionText(t *testing.T) {
	t.Parallel()
	p := newHubPair(t)
	p.setUp()
	p.remoteAccess(true)
	run := func(hh *hubHome, args ...string) clikittest.Output {
		t.Helper()
		out := wdxWith(t, hh.home, hh.env(nil), args...)
		if out.Exit != clikit.ExitOK {
			t.Fatalf("%v: %+v", args, out)
		}
		return out
	}

	if out := run(p.laptop, "hub", "session", "list"); out.Stdout != "no sessions open\n" {
		t.Fatalf("list %q", out.Stdout)
	}
	run(p.laptop, "hub", "session", "open", "--node", "desktop", "--code", p.code())
	out := run(p.desktop, "hub", "session", "list")
	if !strings.HasPrefix(out.Stdout, "ID") || !strings.Contains(out.Stdout, "incoming") || !strings.Contains(out.Stdout, "laptop") {
		t.Fatalf("list %q", out.Stdout)
	}
	if out := run(p.desktop, "hub", "status"); !strings.Contains(out.Stdout, "sessions: 1 incoming, 0 outgoing\n") {
		t.Fatalf("status %q", out.Stdout)
	}
	if out := run(p.laptop, "hub", "node", "get", "--name", "desktop"); !strings.Contains(out.Stdout, "yes, pinned in an earlier session") || !strings.Contains(out.Stdout, "outgoing sess-") {
		t.Fatalf("node get %q", out.Stdout)
	}
	if out := run(p.laptop, "hub", "session", "close", "--all"); !strings.HasPrefix(out.Stdout, "closed 1 sessions\n  outgoing with node desktop, sess-") {
		t.Fatalf("close %q", out.Stdout)
	}
}
