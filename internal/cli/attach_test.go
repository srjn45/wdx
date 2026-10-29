package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"image/color"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/pty"
	"github.com/srjn45/wdx/internal/screen"
)

// interactiveShell is a shell to type into.
const interactiveShell = "#!/bin/sh\nPS1='$ ' exec /bin/sh\n"

// attachRun is wdx terminal attach running in a terminal of its own: a PTY
// whose other side a vt reads, as a terminal app would, answering what the
// view asks it.
type attachRun struct {
	t      *testing.T
	home   string
	args   []string
	master *os.File
	done   chan output

	mu     sync.Mutex
	screen *screen.Screen // the calling terminal
}

// callingText is on the calling terminal's screen before attaching.
const callingText = "calling terminal"

func startAttach(t *testing.T, home, dir string, args ...string) *attachRun {
	t.Helper()
	return startAttachSized(t, home, dir, 80, 24, args...)
}

func startAttachSized(t *testing.T, home, dir string, cols, rows int, args ...string) *attachRun {
	t.Helper()
	master, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { master.Close() })
	if err := pty.Setsize(master, cols, rows); err != nil {
		t.Fatal(err)
	}
	// The terminal's answers go back to the view.
	sc := screen.New(cols, rows, func(p []byte) { _, _ = master.Write(p) })
	sc.Write([]byte(callingText + "\r\n$ "))
	a := &attachRun{t: t, home: home, args: append([]string{"terminal", "attach"}, args...), master: master, screen: sc, done: make(chan output, 1)}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			a.mu.Lock()
			sc.Write(buf[:n])
			a.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		sc.Close()
	})
	go func() {
		var stdout, stderr bytes.Buffer
		env := Env{
			Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Config: testConfig(home), Executable: wdxBinary,
			WorkDir: dir, TTY: func() (*os.File, error) { return tty, nil }, TermEnv: []string{"TERM=xterm-256color"},
		}
		exit := Run(context.Background(), a.args, env)
		a.done <- output{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
	}()
	return a
}

func (a *attachRun) input(s string) {
	a.t.Helper()
	if _, err := a.master.WriteString(s); err != nil {
		a.t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // keys typed one after another, not as one paste
}

// text is what the calling terminal shows, without trailing spaces.
func (a *attachRun) text() string {
	a.mu.Lock()
	lines := strings.Split(ansi.Strip(a.screen.Render()), "\n")
	a.mu.Unlock()
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

func (a *attachRun) waitScreen(s string) {
	a.t.Helper()
	a.waitFor(strconv.Quote(s), func(text string) bool { return strings.Contains(text, s) })
}

func (a *attachRun) waitFor(what string, ok func(text string) bool) {
	a.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok(a.text()) {
		if time.Now().After(deadline) {
			a.t.Fatalf("no %s on the terminal:\n%s", what, a.text())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitText waits for the command to end and returns its output.
func (a *attachRun) waitText() output {
	a.t.Helper()
	var out output
	select {
	case out = <-a.done:
	case <-time.After(10 * time.Second):
		a.t.Fatalf("%v did not end:\n%s", a.args, a.text())
	}
	wantLogged(a.t, a.home, a.args, out)
	return out
}

// wait waits for the command to end and returns its --json output.
func (a *attachRun) wait() (jsonOutput, int) {
	a.t.Helper()
	out := a.waitText()
	var v jsonOutput
	if err := json.Unmarshal([]byte(out.stdout), &v); err != nil {
		a.t.Fatalf("%v: stdout is not JSON: %v\n%s", a.args, err, out.stdout)
	}
	return v, out.exit
}

// wantBar waits for the status line, on the bottom row, to be painted in
// bg, or in reverse video when bg is nil.
func (a *attachRun) wantBar(bg color.Color) {
	a.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		a.mu.Lock()
		_, rows := a.screen.Size()
		var cell *uv.Cell
		if c := a.screen.CellAt(0, rows-1); c != nil {
			copied := *c // the vt changes it
			cell = &copied
		}
		a.mu.Unlock()
		if cell != nil && (bg == nil && cell.Style.Attrs&uv.AttrReverse != 0 || bg != nil && cell.Style.Bg == bg) {
			return
		}
		if time.Now().After(deadline) {
			a.t.Fatalf("want the status line painted in %v, got %+v", bg, cell)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (a *attachRun) altScreen() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.screen.AltScreen()
}

// wantGivenBack checks the calling terminal shows what it did before
// attaching, and nothing of the session.
func (a *attachRun) wantGivenBack(session string) {
	a.t.Helper()
	a.waitFor("the calling terminal back", func(string) bool { return !a.altScreen() })
	if text := a.text(); !strings.Contains(text, callingText) || strings.Contains(text, session) {
		a.t.Fatalf("want the calling terminal as it was, got:\n%s", text)
	}
}

func TestTerminalAttach(t *testing.T) {
	t.Parallel()
	home := newHomeWithShell(t, interactiveShell)
	app := newProjectDir(t, home, "app")
	term := newTerminal(t, home, app, "api")

	a := startAttach(t, home, app, "--name", "api", "--json")
	a.waitScreen("C-\\ ? for keys")
	a.wantBar(ansi.Green)
	if !a.altScreen() || !strings.Contains(a.text(), "api · app") {
		t.Fatalf("want the view full-window with its status line, got:\n%s", a.text())
	}
	a.input("echo hel''lo\r")
	a.waitScreen("hello")
	a.input("\x1c")
	a.waitScreen("d detach")
	a.wantBar(ansi.Yellow)
	a.input("d")
	v, exit := a.wait()
	if exit != api.ExitOK || v.Result != "detached" || v.Terminal == nil || v.Terminal.Id != term.Id || v.Terminal.State != api.TerminalStateRunning {
		t.Fatalf("want detached with the terminal running, got exit %d, %+v", exit, v)
	}
	a.wantGivenBack("hello")

	// Attaching again shows the screen it left.
	b := startAttach(t, home, app, "--id", term.Id, "--json")
	b.waitScreen("hello")
	b.input("exit 3\r")
	v, exit = b.wait()
	if exit != api.ExitOK || v.Result != "exited" || v.Terminal.State != api.TerminalStateExited || v.Terminal.ExitCode == nil || *v.Terminal.ExitCode != 3 {
		t.Fatalf("want exited with code 3, got exit %d, %+v", exit, v)
	}
	b.wantGivenBack("hello")

	c := startAttach(t, home, app, "--name", "api", "--json")
	v, exit = c.wait()
	wantError(t, v, exit, api.ErrorCodeTerminalNotRunning, api.ExitRejected)
	wantHint(t, v, "terminal start")
}

// TestTerminalAttachKeys: the prefix twice sends it once, ? lists the
// keys, s hides the status line, and --prefix-key changes the prefix;
// without color the status line is in reverse video.
func TestTerminalAttachKeys(t *testing.T) {
	t.Parallel()
	home := newHomeWithShell(t, interactiveShell)
	app := newProjectDir(t, home, "app")
	newTerminal(t, home, app, "api")

	a := startAttach(t, home, app, "--name", "api", "--json")
	a.waitScreen("for keys") // attached: the view is up
	a.input("stty raw -echo; echo rea''dy; dd bs=1 count=2 2>/dev/null | od -An -c; stty sane\r")
	a.waitScreen("ready")
	a.input("\x1c")
	a.input("\x1c")
	a.input("x")
	a.waitScreen("034   x")
	a.input("\x1c")
	a.input("?")
	a.waitScreen("detach, leaving the terminal running")
	a.input("q") // closes the list, and goes nowhere else
	a.waitFor("the list closed", func(text string) bool { return !strings.Contains(text, "leaving the terminal running") })
	a.input("\x1c")
	a.input("s")
	a.waitFor("the status line hidden", func(text string) bool { return !strings.Contains(text, "for keys") })
	a.input("\x1c")
	a.input("d")
	if v, exit := a.wait(); exit != api.ExitOK || v.Result != "detached" {
		t.Fatalf("want detached, got exit %d, %+v", exit, v)
	}

	b := startAttach(t, home, app, "--name", "api", "--prefix-key", "ctrl-]", "--color", "never")
	b.waitScreen("C-] ? for keys")
	b.wantBar(nil)
	b.input("\x1c") // no longer the prefix: the shell gets it
	b.input("\x1d")
	b.input("d")
	if out := b.waitText(); out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "detached from terminal api in app\n") {
		t.Fatalf("want detached by ctrl-] d, got exit %d, %q", out.exit, out.stdout)
	}
}

// TestTerminalAttachScroll scrolls back through history from before
// attaching, with PageUp, the prefix and the wheel; a key returns.
func TestTerminalAttachScroll(t *testing.T) {
	t.Parallel()
	home := newHomeWithShell(t, "#!/bin/sh\nseq 1 100\nPS1='$ ' exec /bin/sh\n")
	app := newProjectDir(t, home, "app")
	newTerminal(t, home, app, "api")
	waitHistory(t, home, app, "api", 77) // 100 lines and the prompt, on 24 rows

	a := startAttach(t, home, app, "--name", "api", "--json")
	a.waitScreen("for keys") // attached: the view is up
	a.waitScreen("100")
	a.input("\x1b[5~") // PageUp
	a.waitScreen("history ")
	a.wantBar(ansi.Cyan)
	a.input("\x1c")
	a.input("[")
	a.input("\x1b[<64;5;5M") // the wheel, up
	for range 3 {
		a.input("\x1b[5~")
	}
	a.waitFor("line 1 at the top", func(text string) bool { return strings.HasPrefix(text, "1\n") })
	a.input("\x1b[6~") // PageDown
	a.input("e")       // back to the live screen, and to the shell
	a.waitFor("the live screen", func(text string) bool { return strings.Contains(text, "C-\\ ? for keys") })
	a.input("cho li''ve\r")
	a.waitScreen("live")
	a.input("\x1c")
	a.input("d")
	if _, exit := a.wait(); exit != api.ExitOK {
		t.Fatalf("got exit %d", exit)
	}
}

// waitHistory waits until the terminal's history holds lines lines.
func waitHistory(t *testing.T, home, dir, name string, lines int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		v, exit := wdxJSONWith(t, home, in(dir), "terminal", "history", "--name", name, "--tail", "1")
		if exit == api.ExitOK && v.History.Next >= lines {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("history of %s stays at %+v", name, v.History)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestTerminalAttachConnectionLost(t *testing.T) {
	t.Parallel()
	home := newHomeWithShell(t, interactiveShell)
	app := newProjectDir(t, home, "app")
	term := newTerminal(t, home, app, "api")

	a := startAttach(t, home, app, "--name", "api", "--json")
	a.waitScreen("for keys") // attached: the view is up
	ppid, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(*term.Pid)).Output()
	if err != nil {
		t.Fatal(err)
	}
	holderPID, err := strconv.Atoi(strings.TrimSpace(string(ppid)))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(holderPID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	v, exit := a.wait()
	wantError(t, v, exit, api.ErrorCodeConnectionLost, api.ExitSystem)
	wantHint(t, v, "attach again")
	a.wantGivenBack("$ $")
	_ = syscall.Kill(*term.Pid, syscall.SIGKILL)
}

func TestTerminalAttachRefused(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newTerminal(t, home, app, "api")

	// No terminal to attach in: TTY is nil.
	v, exit := wdxJSONWith(t, home, in(app), "terminal", "attach", "--name", "api")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	v, exit = wdxJSONWith(t, home, in(app), "terminal", "attach", "--name", "web")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "terminal", "attach", "--name", "api", "--prefix-key", "ctrl-1")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)

	small := startAttachSized(t, home, app, 8, 2, "--name", "api", "--json")
	v, exit = small.wait()
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
}

func TestTerminalHistory(t *testing.T) {
	t.Parallel()
	home := newHomeWithShell(t, "#!/bin/sh\nprintf '\\033[31mred\\033[0m\\n'\nseq 1 99\nexec sleep 60\n")
	app := newProjectDir(t, home, "app")
	newTerminal(t, home, app, "api")
	waitHistory(t, home, app, "api", 77)

	v, exit := wdxJSONWith(t, home, in(app), "terminal", "history", "--name", "api")
	if exit != api.ExitOK || v.Result != "" || v.History.From != 0 || v.History.First != 0 || int64(len(v.History.Lines)) != v.History.Next {
		t.Fatalf("want every line, got exit %d, %+v", exit, v.History)
	}
	v, exit = wdxJSONWith(t, home, in(app), "terminal", "history", "--name", "api", "--from", "1", "--count", "2")
	if exit != api.ExitOK || v.History.From != 1 || !slices.Equal(v.History.Lines, []string{"1", "2"}) {
		t.Fatalf("want lines 1 and 2, got exit %d, %+v", exit, v.History)
	}
	v, exit = wdxJSONWith(t, home, in(app), "terminal", "history", "--name", "api", "--tail", "2", "--from", "3")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	v, exit = wdxJSONWith(t, home, in(app), "terminal", "history", "--name", "web")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)

	// Text keeps the lines' colors only when the output gets color.
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "red\n1\n"},
		{[]string{"--color", "always"}, "\x1b[31mred\x1b[m\n1\n"},
		{[]string{"--color", "always", "--plain"}, "red\n1\n"},
	} {
		out := wdxWith(t, home, in(app), append([]string{"terminal", "history", "--name", "api", "--from", "0", "--count", "2"}, tc.args...)...)
		if out.exit != api.ExitOK || out.stdout != tc.want {
			t.Fatalf("%v: got exit %d, %q, want %q", tc.args, out.exit, out.stdout, tc.want)
		}
	}
}
