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
	home := t.TempDir()
	run := startInTerminal(t, home, home, 80, 24, []string{"tide"})
	run.waitScreen("┌ project")
	run.waitScreen("Alt+arrows move")
	run.input("\x1b[1;3B") // Alt+↓: the terminal pane
	run.waitScreen(" terminal · shell")
	run.input("\x1b\x1b[A") // Alt+↑ as a Meta key sends it: back to the agent
	run.waitScreen(" agent ")
	run.input("x\x1d") // a key for the agent, then C-]: zoomed
	run.waitScreen("agent · zoomed")
	run.waitScreen("got: x")
	run.input("\x11") // C-q: the menu
	run.waitScreen("q    quit")
	run.input("q")
	out := run.waitText()
	if out.exit != api.ExitOK || out.stdout != "tide closed\n" {
		t.Fatalf("got %+v", out)
	}
	run.waitScreen(callingText) // the window given back
}
