package cli

import (
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

func TestTideRefused(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	// No terminal to run in: TTY is nil.
	v, exit := wdxJSONWith(t, home, func(*Env) {}, "tide", "--json")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	out := wdx(t, home, "tide")
	if out.exit != api.ExitUsage || !strings.Contains(out.stderr, "no terminal to run tide in") {
		t.Fatalf("without a terminal: %+v", out)
	}
}

func TestTide(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	for _, args := range [][]string{{"project", "create", "--name", "app", "--cancel"}, {"project", "close", "--name", "app", "--cancel"}} {
		if _, exit := wdxJSON(t, home, args...); exit != api.ExitOK {
			t.Fatalf("%v: exit %d", args, exit)
		}
	}
	run := startInTerminal(t, home, home, 80, 24, []string{"tide"})
	run.waitScreen("┌ [Open Project]")
	run.waitScreen("no project open")
	run.input("\x1b[B\x1b[B\x1b[B\r") // ↓ ↓ ↓ enter: reopen app
	run.waitScreen("┌ [Project: app]")
	run.waitScreen(" [shell] [tests] [server] ")
	run.input("\x1b[1;4C") // Alt+Shift+→: the next open project, app again, on the strip
	run.waitScreen("▸ app ◂")
	run.input("\x1b[1;3B") // Alt+↓: the terminal pane
	run.waitScreen(" app · terminal · shell")
	run.input("\x1b\x1b[A") // Alt+↑ as a Meta key sends it: back to the project
	run.input("\x1b[1;3C")  // Alt+→: the agent
	run.waitScreen(" app · agent ")
	run.input("x\x1d") // a key for the agent, then C-]: zoomed
	run.waitScreen("agent · zoomed")
	run.waitScreen("got: x")
	run.input("\x11") // C-q: the menu
	run.waitScreen("o    open project")
	run.input("q")
	out := run.waitText()
	if out.exit != api.ExitOK || out.stdout != "tide closed\n" {
		t.Fatalf("got %+v", out)
	}
	run.waitScreen(callingText) // the window given back
}
