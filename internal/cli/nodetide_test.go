package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/config"
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

// TestHostIndicator runs TIDE on the desktop while the laptop opens a
// session into it and closes it: the bar says remote: laptop, in cyan, while
// it is open, and no longer once it closed, which TIDE follows on one
// stream it opens once, asking for the sessions in no other way.
func TestHostIndicator(t *testing.T) {
	t.Parallel()
	p := newHubPair(t)
	p.setUp()
	p.remoteAccess(true)
	newProjectDir(t, p.desktop.home, "app")

	args := []string{"tide"}
	run := startInTerminalWith(t, p.desktop.home, "", 128, 24, args, p.desktop.env(nil))
	run.waitScreen("┃ Switch Project")
	run.input("\r")
	run.waitScreen("\n app · project ")
	run.wantBar(ansi.Green)

	if v, exit := p.laptop.sessionJSON("hub", "session", "open", "--node", "desktop", "--code", p.code()); exit != clikit.ExitOK {
		t.Fatalf("open: %d %+v", exit, v.jsonOutput)
	}
	run.waitScreen("\n remote: laptop · app · project ")
	run.wantBar(ansi.Cyan)

	if v, exit := p.laptop.sessionJSON("hub", "session", "close", "--all"); exit != clikit.ExitOK || len(v.Sessions) != 1 {
		t.Fatalf("close: %d %+v", exit, v.jsonOutput)
	}
	run.waitScreen("\n app · project ")
	run.wantBar(ansi.Green)

	run.input("\x11") // C-q: the menu
	run.input("q")
	if out := run.waitText(); out.Exit != clikit.ExitOK {
		t.Fatalf("got %+v", out)
	}
	streams := 0
	for _, l := range logLines(t, p.desktop.home, runID(t, p.desktop.home, args)) {
		if l["msg"] != "request received" {
			continue
		}
		switch l["path"] {
		case "/v1/hub/sessions/events":
			streams++
		case "/v1/hub/sessions":
			t.Errorf("TIDE asked for the sessions: %v", l)
		}
	}
	if streams != 1 {
		t.Errorf("TIDE opened the sessions' stream %d times, want once", streams)
	}
}

// TestNodeTideNoUserHome: a TIDE on another node shows the host's paths
// whole, never shortened with this machine's home.
func TestNodeTideNoUserHome(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"tide"}, "/home/alice"},
		{[]string{"tide", "--node", "desktop"}, ""},
	} {
		root := newRoot(config.Default())
		cmd, rest, err := root.Find(tc.args)
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.ParseFlags(rest); err != nil {
			t.Fatal(err)
		}
		cmd.SetContext(context.WithValue(t.Context(), envKey{}, &Env{UserHome: "/home/alice"}))
		if got := tideOptions(cmd, nil).UserHome; got != tc.want {
			t.Errorf("%v: UserHome %q, want %q", tc.args, got, tc.want)
		}
	}
}
