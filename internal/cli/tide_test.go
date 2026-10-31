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

// TestTideDetachesAttach runs attach in a tide pane, where tide takes attach's
// prefix: C-q \\ d gives it to attach and detaches, tide still running.
func TestTideDetachesAttach(t *testing.T) {
	t.Parallel()
	home := newHomeWithShell(t, interactiveShell)
	dir := newProjectDir(t, home, "app")
	newTerminal(t, home, dir, "first") // the tab selected, where attach runs
	other := newTerminal(t, home, dir, "second")
	run := startInTerminal(t, home, home, 128, 24, []string{"tide"})
	run.waitScreen("┃ Switch Project")
	run.input("\r")
	run.waitScreen("┌ [Project: app]")
	run.input("\x1b[1;3B") // Alt+↓: the terminal pane
	run.waitScreen(" app · terminal · first")
	run.input("\x1d") // C-]: zoomed, room for attach
	run.waitScreen("zoomed")
	run.waitScreen("│$") // attached: until then the pane drops what is typed
	run.input(wdxBinary + " terminal attach --id " + other.Id + "\r")
	run.waitScreen("C-\\ ? for keys")
	run.input("\x11") // C-q: the menu
	run.waitScreen("send C-\\ to the program")
	run.input("\\")
	run.waitScreen("C-\\ …") // attach has its prefix
	run.input("d")
	run.waitScreen("detached from terminal")
	run.input("\x11")
	run.waitScreen("┃ o  open project")
	run.input("q")
	if out := run.waitText(); out.exit != api.ExitOK {
		t.Fatalf("got %+v", out)
	}
}

func TestTide(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	dir := newProjectDir(t, home, "app")
	newTerminal(t, home, dir, "shell")
	if _, exit := wdxJSON(t, home, "project", "close", "--name", "app", "--stop-running"); exit != api.ExitOK {
		t.Fatalf("close: exit %d", exit)
	}
	run := startInTerminal(t, home, home, 128, 24, []string{"tide"}) // wide enough for the project pane's title
	run.waitScreen("┃ Open Project")
	run.input("\x1b[B\x1b[B\x1b[B\r") // ↓ ↓ ↓ enter: reopen app
	run.waitScreen("┌ [Project: app]")
	run.waitScreen(" [● shell] ") // relaunched with the project
	newTerminal(t, home, dir, "build")
	run.waitScreen(" [● build] [● shell] ")
	run.input("\x1b[1;4C") // Alt+Shift+→: the next open project, app again, on the strip
	run.waitScreen("▸ app ◂")
	run.input("\x1b[1;3B") // Alt+↓: the terminal pane
	run.waitScreen(" app · terminal · shell")
	run.input("\x1b\x1b[A") // Alt+↑ as a Meta key sends it: back to the project
	run.input("\x1b[1;3C")  // Alt+→: the agent
	run.waitScreen(" app · agent ")
	run.input("\x1d") // C-]: zoomed
	run.waitScreen("agent · zoomed")
	run.waitScreen("no agents: C-q a creates one")
	run.input("\x11") // C-q: the menu
	run.waitScreen("┃ o  open project")
	run.input("q")
	out := run.waitText()
	if out.exit != api.ExitOK || out.stdout != "tide closed\n" {
		t.Fatalf("got %+v", out)
	}
	run.waitScreen(callingText) // the window given back
}
