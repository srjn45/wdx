package cli

import (
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/clikit"
)

// TestNodeTide runs TIDE on the laptop showing the desktop: the code is
// asked before the window, the desktop's project and terminal show under the
// node's bar, typing reaches the desktop's shell, and when the desktop closes
// the session the message asks for a code, which goes on as TIDE was. The
// laptop's tide lock is taken, and the desktop's log shows no request to its
// TIDE's view or tabs.
func TestNodeTide(t *testing.T) {
	t.Parallel()
	p := newHubPairShell(t, interactiveShell)
	p.setUp()
	p.remoteAccess(true)
	app := newProjectDir(t, p.desktop.home, "app")
	newTerminal(t, p.desktop.home, app, "shell")
	for _, hh := range []*hubHome{p.desktop, p.laptop} {
		hh.secret("qu''ixote", "quixote")
	}

	args := []string{"tide", "--node", "desktop"}
	run := startInTerminalWith(t, p.laptop.home, "", 128, 24, args, p.laptop.env(func(env *Env) {
		env.Stdin, _ = env.TTY() // the code is asked on the terminal TIDE runs in
	}))
	run.input(p.code() + "\r")
	run.waitScreen("┃ Switch Project")
	run.input("\r")
	run.waitScreen("┏ [Project: app]")
	run.waitScreen(" desktop › app · project")
	run.waitScreen(" [● shell] ")

	// One TIDE per machine, whichever node it shows.
	if out := wdx(t, p.laptop.home, "tide", "--color", "never"); out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "[tide_running]") {
		t.Fatalf("a second tide on the laptop: %+v", out)
	}

	run.input("\x1b[1;3B") // Alt+↓: the terminal pane
	run.waitScreen(" desktop › app · terminal · shell")
	run.waitScreen("┃$")
	run.input("echo qu''ixote\r")
	run.waitScreen("quixote")

	if _, exit := p.desktop.sessionJSON("hub", "session", "close", "--all"); exit != clikit.ExitOK {
		t.Fatalf("closing the desktop's sessions: exit %d", exit)
	}
	run.waitScreen("session to desktop closed")
	run.waitScreen("it was closed, on desktop or here")
	run.input(p.code() + "\r")
	run.waitScreen(" desktop › app · terminal · shell")
	run.waitScreen("┃$")

	run.input("\x11") // C-q: the menu
	run.waitScreen("┃ s  settings are changed on desktop itself")
	run.input("q")
	out := run.waitText()
	if out.Exit != clikit.ExitOK || out.Stdout != "TIDE closed\n" {
		t.Fatalf("got %+v", out)
	}
	if !strings.HasPrefix(out.Stderr, "on node desktop\ncode for desktop: ") {
		t.Errorf("stderr %q, want the code asked before the window", out.Stderr)
	}

	id := runID(t, p.laptop.home, args)
	if served(t, p.desktop.home, id) == 0 {
		t.Fatal("the desktop served nothing of the run")
	}
	for _, l := range logLines(t, p.desktop.home, id) {
		path, _ := l["path"].(string)
		if l["msg"] == "request received" && (path == "/v1/tide/view" || strings.HasSuffix(path, "/tabs") || strings.HasSuffix(path, "/view")) {
			t.Errorf("the desktop received a request for its TIDE's state: %v", l)
		}
	}
}

// TestNodeTideForAgents: an agent cannot run TIDE on another node, which is
// refused before anything is asked or locked.
func TestNodeTideForAgents(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	args := []string{"tide", "--node", "desktop"}
	run := startInTerminalWith(t, home, "", 80, 24, args, func(env *Env) { env.AgentID = "agent-1a2b3c4d" })
	out := run.waitText()
	if out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "["+string(api.ErrorCodeRemoteNotForAgents)+"]") || strings.Contains(out.Stderr, "code for") {
		t.Fatalf("got %+v", out)
	}
	if run.altScreen() {
		t.Error("the window opened")
	}
}
