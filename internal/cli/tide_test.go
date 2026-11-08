package cli

import (
	"path/filepath"
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
	if out.exit != api.ExitUsage || !strings.Contains(out.stderr, "no terminal to run TIDE in") {
		t.Fatalf("without a terminal: %+v", out)
	}
}

// TestTideRunning: a second tide for a home is refused while the first runs, and
// starts once it has quit.
func TestTideRunning(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	run := startInTerminal(t, home, home, 128, 24, []string{"tide"})
	run.waitScreen("┃ Open Project")
	// Another command line than the first's: the log checks tell runs apart by it.
	second := []string{"tide", "--color", "never"}
	out := wdx(t, home, second...)
	if out.exit != api.ExitRejected || !strings.Contains(out.stderr, "a TIDE is already running for this home, as pid") ||
		!strings.Contains(out.stderr, "[tide_running]") || !strings.Contains(out.stderr, "hint: go to the window it runs in") {
		t.Fatalf("a second tide: %+v", out)
	}
	run.input("\x1b") // Esc: the popup closed
	// Until it has, a C-q would come right after the Esc and read as Alt+C-q.
	run.waitFor("the popup closed", func(text string) bool { return !strings.Contains(text, "┃ Open Project") })
	run.input("\x11") // C-q: the menu
	run.waitScreen("┃ q  quit")
	run.input("q")
	if out := run.waitText(); out.exit != api.ExitOK {
		t.Fatalf("quit: %+v", out)
	}
	// Once it quit the lock is free again: no terminal to run in is what stops this one.
	if out := wdx(t, home, second...); out.exit != api.ExitUsage || !strings.Contains(out.stderr, "no terminal to run TIDE in") {
		t.Fatalf("after quitting: %+v", out)
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
	run.waitScreen("┏ [Project: app]")
	run.input("\x1b[1;3B") // Alt+↓: the terminal pane
	run.waitScreen(" app · terminal · first")
	run.input("\x1d") // C-]: zoomed, room for attach
	run.waitScreen("zoomed")
	run.waitScreen("┃$") // attached: until then the pane drops what is typed
	// A pane takes keys one at a time: a binary's whole path takes seconds to type.
	writeFile(t, filepath.Join(dir, "a"), "#!/bin/sh\nexec "+wdxBinary+" terminal attach --id "+other.Id+"\n")
	run.input("./a\r")
	run.waitScreen("C-\\ ? for keys")
	run.input("\x11") // C-q: the menu
	run.waitScreen("send C-\\ to the program")
	run.input("\\")
	run.waitScreen("C-\\ …") // attach has its prefix
	run.input("d")
	run.waitScreen("detached from terminal")
	run.input("\x11")
	run.waitScreen("┃ q  quit")
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
	run.waitScreen("┏ [Project: app]")
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
	run.waitScreen("┃ q  quit")
	run.input("q")
	out := run.waitText()
	if out.exit != api.ExitOK || out.stdout != "TIDE closed\n" {
		t.Fatalf("got %+v", out)
	}
	run.waitScreen(callingText) // the window given back
}

// TestTideResume starts tide as an older release's C-q r does, through --resume:
// it shows what the state says, with no popup to choose a project from.
func TestTideResume(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	dir := newProjectDir(t, home, "app")
	newTerminal(t, home, dir, "shell")
	state := `{"project":"app","focus":"terminal","zoomed":"terminal","last_top":"project","terminal_open":true}`
	run := startInTerminal(t, home, home, 128, 24, []string{"tide", "--resume", state})
	run.waitScreen("terminal · shell · zoomed")
	run.input("\x11") // C-q: the menu
	run.waitScreen("┃ q  quit")
	run.input("q")
	if out := run.waitText(); out.exit != api.ExitOK || out.stdout != "TIDE closed\n" {
		t.Fatalf("got %+v", out)
	}
}
