package cli

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/identity"
)

// attachOnLaptop runs args on the laptop in a terminal of their own.
func (p *hubPair) attachOnLaptop(args ...string) *attachRun {
	p.laptop.t.Helper()
	return startInTerminalWith(p.laptop.t, p.laptop.home, "", 80, 24, args, p.laptop.env(nil))
}

// lastUsed is when the desktop last saw the one session into it used, as it
// says itself, which moves nothing on.
func (p *hubPair) lastUsed() time.Time {
	t := p.desktop.t
	t.Helper()
	v, exit := p.desktop.sessionJSON("hub", "session", "list")
	if exit != clikit.ExitOK || len(v.Sessions) != 1 {
		t.Fatalf("the desktop's sessions: %d %+v", exit, v.Sessions)
	}
	return v.Sessions[0].LastUsedAt
}

// TestNodeTerminalAttach attaches from the laptop to a terminal of the
// desktop: what is typed reaches the desktop's shell and its output comes
// back, the holder logs a remote client, the stream logs bytes counted and no
// byte, and the bytes keep the session from going idle, once a minute at most.
func TestNodeTerminalAttach(t *testing.T) {
	t.Parallel()
	p := openPairShell(t, interactiveShell)
	app := newProjectDir(t, p.desktop.home, "app")
	term := newTerminal(t, p.desktop.home, app, "api")
	// What is typed, and what it prints, are in no log line of either node.
	for _, hh := range []*hubHome{p.desktop, p.laptop} {
		hh.secret("zq''xv", "zqxv", "mo''vedon", "movedon", "st''ill", "still")
	}

	args := []string{"terminal", "attach", "--node", "desktop", "--project-name", "app", "--name", "api", "--json"}
	a := p.attachOnLaptop(args...)
	a.waitScreen("C-\\ ? for keys")
	a.input("echo zq''xv\r")
	a.waitScreen("zqxv")

	before := p.lastUsed()
	p.clock.Advance(idleMove + time.Second)
	a.input("echo mo''vedon\r")
	a.waitScreen("movedon")
	moved := p.lastUsed()
	if moved.Before(before.Add(idleMove)) {
		t.Errorf("last used %v after typing a minute on, want it moved on from %v", moved, before)
	}
	p.clock.Advance(10 * time.Second)
	a.input("echo st''ill\r")
	a.waitScreen("still")
	if again := p.lastUsed(); !again.Equal(moved) {
		t.Errorf("last used %v after typing 10 seconds on, want it left at %v", again, moved)
	}

	a.input("\x1c")
	a.waitScreen("d detach")
	a.input("d")
	v, exit := a.wait()
	if exit != clikit.ExitOK || v.Result != "detached" || v.Terminal == nil || v.Terminal.Id != term.Id || v.Terminal.State != api.TerminalStateRunning {
		t.Fatalf("want detached with the desktop's terminal running, got exit %d, %+v", exit, v)
	}
	a.wantGivenBack("zqxv")

	if lineOf(logLines(t, p.desktop.home, ""), "client attached", map[string]any{"remote": true, "from_node": "laptop"}) == nil {
		t.Error("the desktop's holder logged no remote client from the laptop")
	}
	id := runID(t, p.laptop.home, args)
	if lineOf(logLines(t, p.desktop.home, id), "request returned", map[string]any{"path": "/v1/terminals/id:" + term.Id + "/attach", "status": 101, "response": "byte stream"}) == nil {
		t.Error("the desktop did not switch the attach under the run's debug id")
	}
	var closed map[string]any
	if !eventually(t, func() bool {
		closed = lineOf(logLines(t, p.desktop.home, id), "stream closed", nil)
		return closed != nil
	}) {
		t.Fatal("the desktop logged no stream closed")
	}
	if sent, _ := closed["bytes_sent"].(float64); sent == 0 {
		t.Errorf("stream closed %v, want the bytes the laptop sent counted", closed)
	}
}

// idleMove is how often bytes through an attach move a session's idle time.
const idleMove = time.Minute

func TestNodeAgentAttach(t *testing.T) {
	t.Parallel()
	p := openPair(t)
	app := newProjectDir(t, p.desktop.home, "app")
	agent := typableAgent(t, p.desktop.home, app, "helper")

	a := p.attachOnLaptop("agent", "attach", "--node", "desktop", "--project-name", "app", "--name", "helper", "--json")
	a.waitScreen("C-\\ ? for keys")
	if !strings.Contains(a.text(), "helper · app") {
		t.Fatalf("want the agent's status line, got:\n%s", a.text())
	}
	a.input("echo re''mote\r")
	a.waitScreen("remote")
	a.input("\x1c")
	a.waitScreen("d detach")
	a.input("d")
	v, exit := a.wait()
	if exit != clikit.ExitOK || v.Result != "detached" || v.Agent == nil || v.Agent.Id != agent.Id {
		t.Fatalf("want detached from the desktop's agent, got exit %d, %+v", exit, v)
	}
}

// TestNodeAttachOldHolder attaches through a holder of the protocol before
// relayed clients: the desktop answers holder_version_unsupported.
func TestNodeAttachOldHolder(t *testing.T) {
	t.Parallel()
	p := openPair(t)
	app := newProjectDir(t, p.desktop.home, "app")
	term := newTerminal(t, p.desktop.home, app, "api")

	// The holder's socket gives way to one of version 4; the daemon keeps
	// the connection it has to the real one, which gets its socket back.
	socket := identity.HolderSocket(p.desktop.home, term.Id)
	if err := os.Rename(socket, socket+".real"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Rename(socket+".real", socket); err != nil {
			t.Error(err)
		}
	})
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				var head [5]byte
				if _, err := c.Read(head[:]); err != nil {
					return
				}
				hello, _ := json.Marshal(map[string]any{"version": 4, "terminal_id": term.Id})
				frame := append([]byte{1, 0, 0, 0, byte(len(hello))}, hello...)
				_, _ = c.Write(frame)
				_, _ = io.Copy(io.Discard, c) // until the relay closes
			}()
		}
	}()

	a := p.attachOnLaptop("terminal", "attach", "--node", "desktop", "--project-name", "app", "--name", "api", "--json")
	v, exit := a.wait()
	wantError(t, v, exit, api.ErrorCodeHolderVersionUnsupported, clikit.ExitRejected)
	wantHint(t, v, "restart its shell on desktop")
}
