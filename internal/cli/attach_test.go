package cli

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"image/color"
	"io"
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/sys/unix"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/pty"
	"github.com/srjn45/wdx/internal/screen"
	"github.com/srjn45/wdx/internal/waittest"
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
	tty    *os.File // the side attach reads
	done   chan output

	mu     sync.Mutex
	screen *screen.Screen // the calling terminal
	raw    []byte         // everything written to it
}

// callingText is on the calling terminal's screen before attaching.
const callingText = "calling terminal"

func startAttach(t *testing.T, home, dir string, args ...string) *attachRun {
	t.Helper()
	return startAttachSized(t, home, dir, 80, 24, args...)
}

func startAttachSized(t *testing.T, home, dir string, cols, rows int, args ...string) *attachRun {
	t.Helper()
	return startInTerminal(t, home, dir, cols, rows, append([]string{"terminal", "attach"}, args...))
}

// startInTerminal runs args in a cols × rows terminal of their own.
func startInTerminal(t *testing.T, home, dir string, cols, rows int, args []string) *attachRun {
	t.Helper()
	master, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { master.Close() })
	if err := pty.Setsize(master, cols, rows); err != nil {
		t.Fatal(err)
	}
	a := newAttachRun(t, home, master, tty, cols, rows, args)
	go func() {
		var stdout, stderr bytes.Buffer
		env := Env{
			Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Config: testConfig(home), Executable: wdxBinary,
			WorkDir: dir, TTY: func() (*os.File, error) { return tty, nil }, CallerTerm: []string{"TERM=xterm-256color"},
		}
		exit := Run(context.Background(), a.args, env)
		a.done <- output{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
	}()
	return a
}

// startAttachBinary runs attach in the wdx binary, in a terminal of its
// own as a user does, so that a signal reaches it alone.
func startAttachBinary(t *testing.T, home, dir string, args ...string) (*attachRun, *os.Process) {
	t.Helper()
	master, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		master.Close()
		tty.Close() // the command has its own
	})
	if err := pty.Setsize(master, 80, 24); err != nil {
		t.Fatal(err)
	}
	a := newAttachRun(t, home, master, tty, 80, 24, append([]string{"terminal", "attach"}, args...))
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(wdxBinary, a.args...)
	cmd.Dir = dir
	cmd.Env = []string{"WDX_HOME=" + home, "PATH=/usr/bin:/bin", "TERM=xterm-256color"}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, &stdout, &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	go func() {
		_ = cmd.Wait() // the exit code tells
		a.done <- output{exit: cmd.ProcessState.ExitCode(), stdout: stdout.String(), stderr: stderr.String(), pid: cmd.Process.Pid}
	}()
	return a, cmd.Process
}

// newAttachRun is the calling terminal on master's other side.
func newAttachRun(t *testing.T, home string, master, tty *os.File, cols, rows int, args []string) *attachRun {
	// Its own copy of tty, which attach closes when it ends; Fd would turn
	// attach's blocking.
	raw, err := tty.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var fd int
	if cerr := raw.Control(func(f uintptr) { fd, err = unix.Dup(int(f)) }); cerr != nil || err != nil {
		t.Fatal(cerr, err)
	}
	tty = os.NewFile(uintptr(fd), tty.Name())
	t.Cleanup(func() { tty.Close() })
	// The terminal's answers go back to the view.
	sc := screen.New(cols, rows, func(p []byte) { _, _ = master.Write(p) })
	sc.Write([]byte(callingText + "\r\n$ "))
	a := &attachRun{t: t, home: home, args: args, master: master, tty: tty, screen: sc, done: make(chan output, 1)}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			a.mu.Lock()
			sc.Write(buf[:n])
			a.raw = append(a.raw, buf[:n]...)
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
	return a
}

func (a *attachRun) input(s string) {
	a.t.Helper()
	if _, err := a.master.WriteString(s); err != nil {
		a.t.Fatal(err)
	}
	// Keys are typed one after another, not as one paste: the next waits
	// until attach has read this one.
	var n int
	var err error
	if !eventually(a.t, func() bool {
		n, err = unix.IoctlGetInt(int(a.tty.Fd()), unreadInput)
		return err == nil && n == 0
	}) {
		a.t.Fatalf("attach did not read %q: %d bytes unread, %v", s, n, err)
	}
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
	if !eventually(a.t, func() bool { return ok(a.text()) }) {
		a.t.Fatalf("no %s on the terminal:\n%s", what, a.text())
	}
}

// waitText waits for the command to end and returns its output.
func (a *attachRun) waitText() output {
	a.t.Helper()
	var out output
	select {
	case out = <-a.done:
	case <-waittest.After(a.t):
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
	var cell *uv.Cell
	painted := eventually(a.t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		_, rows := a.screen.Size()
		cell = nil
		if c := a.screen.CellAt(0, rows-1); c != nil {
			copied := *c // the vt changes it
			cell = &copied
		}
		return cell != nil && (bg == nil && cell.Style.Attrs&uv.AttrReverse != 0 || bg != nil && cell.Style.Bg == bg)
	})
	if !painted {
		a.t.Fatalf("want the status line painted in %v, got %+v", bg, cell)
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

// TestTerminalAttachSignal ends attach with a signal: it gives the window
// back and detaches, leaving the terminal running.
func TestTerminalAttachSignal(t *testing.T) {
	t.Parallel()
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			t.Parallel()
			home := newHomeWithShell(t, interactiveShell)
			app := newProjectDir(t, home, "app")
			newTerminal(t, home, app, "api")
			a, p := startAttachBinary(t, home, app, "--name", "api", "--json")
			a.waitScreen("C-\\ ? for keys")
			a.input("echo hel''lo\r")
			a.waitScreen("hello")
			if err := p.Signal(sig); err != nil {
				t.Fatal(err)
			}
			v, exit := a.wait()
			if exit != api.ExitOK || v.Result != "detached" || v.Terminal == nil || v.Terminal.State != api.TerminalStateRunning {
				t.Fatalf("want detached with the terminal running, got exit %d, %+v", exit, v)
			}
			a.wantGivenBack("hello")
		})
	}
}

// TestTerminalAttach attaches, types and detaches: the terminal runs on,
// and the calling terminal is given back.
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
}

// TestTerminalAttachAgain attaches to a terminal attached before: it shows
// the screen left.
func TestTerminalAttachAgain(t *testing.T) {
	t.Parallel()
	home := newHomeWithShell(t, interactiveShell)
	app := newProjectDir(t, home, "app")
	term := newTerminal(t, home, app, "api")
	a := startAttach(t, home, app, "--name", "api", "--json")
	a.waitScreen("for keys") // attached: the view is up
	a.input("echo hel''lo\r")
	a.waitScreen("hello")
	a.input("\x1c")
	a.input("d")
	if _, exit := a.wait(); exit != api.ExitOK {
		t.Fatalf("got exit %d", exit)
	}

	b := startAttach(t, home, app, "--id", term.Id, "--json")
	b.waitScreen("hello")
	b.input("\x1c")
	b.input("d")
	if v, exit := b.wait(); exit != api.ExitOK || v.Result != "detached" {
		t.Fatalf("want detached, got exit %d, %+v", exit, v)
	}
}

// TestTerminalAttachExited exits the shell attached to: attach ends with
// its exit code.
func TestTerminalAttachExited(t *testing.T) {
	t.Parallel()
	home := newHomeWithShell(t, interactiveShell)
	app := newProjectDir(t, home, "app")
	newTerminal(t, home, app, "api")

	a := startAttach(t, home, app, "--name", "api", "--json")
	a.waitScreen("for keys") // attached: the view is up
	a.input("echo hel''lo\r")
	a.waitScreen("hello")
	a.input("exit 3\r")
	v, exit := a.wait()
	if exit != api.ExitOK || v.Result != "exited" || v.Terminal.State != api.TerminalStateExited || v.Terminal.ExitCode == nil || *v.Terminal.ExitCode != 3 {
		t.Fatalf("want exited with code 3, got exit %d, %+v", exit, v)
	}
	a.wantGivenBack("hello")
}

func TestTerminalAttachNotRunning(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newTerminal(t, home, app, "api")
	wdxJSON(t, home, "project", "close", "--name", "app", "--stop-running")

	a := startAttach(t, home, app, "--name", "api", "--json")
	v, exit := a.wait()
	wantError(t, v, exit, api.ErrorCodeTerminalNotRunning, api.ExitRejected)
	wantHint(t, v, "terminal start")
}

// TestTerminalAttachExitedText attaches in text: the shell's exit ends
// attach with its code.
func TestTerminalAttachExitedText(t *testing.T) {
	t.Parallel()
	home := newHomeWithShell(t, interactiveShell)
	app := newProjectDir(t, home, "app")
	newTerminal(t, home, app, "api")

	a := startAttach(t, home, app, "--name", "api")
	a.waitScreen("for keys") // attached: the view is up
	a.input("exit 3\r")
	if out := a.waitText(); out.exit != api.ExitOK || out.stdout != "terminal api in app exited with code 3\n" {
		t.Fatalf("want exited with code 3, got %+v", out)
	}
}

// TestTerminalAttachTitle lets the shell set the window title: the calling
// terminal's is saved before and restored after.
func TestTerminalAttachTitle(t *testing.T) {
	t.Parallel()
	home := newHomeWithShell(t, interactiveShell)
	app := newProjectDir(t, home, "app")
	newTerminal(t, home, app, "api")

	a := startAttach(t, home, app, "--name", "api", "--json")
	a.waitScreen("for keys") // attached: the view is up
	a.input("printf '\\033]2;%s\\007' shell-title\r")
	a.waitFor("the shell's title", func(string) bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.screen.Title() == "shell-title"
	})
	a.input("\x1c")
	a.input("d")
	if _, exit := a.wait(); exit != api.ExitOK {
		t.Fatalf("got exit %d", exit)
	}
	// Attach has written all it writes, which this test may not have read yet.
	var raw string
	eventually(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		raw = string(a.raw)
		return strings.Contains(raw, titlePop)
	})
	push, pop := strings.Index(raw, titlePush), strings.LastIndex(raw, titlePop)
	set := strings.Index(raw, "\x1b]2;")
	if push < 0 || set < push || pop < strings.LastIndex(raw, "\x1b]2;") {
		t.Fatalf("want the title saved first and restored last, got push at %d, first set at %d, pop at %d", push, set, pop)
	}
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
	var v jsonOutput
	if !eventually(t, func() bool {
		var exit int
		v, exit = wdxJSONWith(t, home, in(dir), "terminal", "history", "--name", name, "--tail", "1")
		return exit == api.ExitOK && v.History.Next >= lines
	}) {
		t.Fatalf("history of %s stays at %+v", name, v.History)
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
	// Nothing is left to stop the shell, which the holder took with it.
	t.Cleanup(func() { _ = syscall.Kill(*term.Pid, syscall.SIGKILL) })
	if err := syscall.Kill(holderPID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	v, exit := a.wait()
	wantError(t, v, exit, api.ErrorCodeConnectionLost, api.ExitSystem)
	wantHint(t, v, "attach again")
	a.wantGivenBack("$ $")
}

// TestTerminalAttachNoTerminal attaches with no terminal to show it in.
func TestTerminalAttachNoTerminal(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newTerminal(t, home, app, "api")
	v, exit := wdxJSONWith(t, home, in(app), "terminal", "attach", "--name", "api") // TTY is nil
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
}

func TestTerminalAttachNotFound(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	v, exit := wdxJSONWith(t, home, in(app), "terminal", "attach", "--name", "web")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
}

// TestTerminalAttachPrefixKeyInvalid refuses a prefix key that is no key,
// and those that are Tab and Enter.
func TestTerminalAttachPrefixKeyInvalid(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	for _, key := range []string{"ctrl-1", "ctrl-i", "ctrl-m"} {
		v, exit := wdxJSONWith(t, home, in(app), "terminal", "attach", "--name", "api", "--prefix-key", key)
		wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	}
}

func TestTerminalAttachTooSmall(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newTerminal(t, home, app, "api")
	small := startAttachSized(t, home, app, 8, 2, "--name", "api", "--json")
	v, exit := small.wait()
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
}

// TestTerminalAttachUnsupported attaches to a terminal whose holder speaks
// the first protocol, which only the daemon may use.
func TestTerminalAttachUnsupported(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	term := newTerminal(t, home, app, "api")
	// An old holder in place of the real one, which serves the daemon on;
	// nothing can reach it to stop it but its shell's end.
	t.Cleanup(func() { _ = syscall.Kill(-*term.Pid, syscall.SIGKILL) })
	socket := identity.HolderSocket(home, term.Id)
	if err := os.Remove(socket); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		hello := []byte(`{"version":1,"terminal_id":"` + term.Id + `"}`)
		frame := append([]byte{1, 0, 0, 0, byte(len(hello))}, hello...) // a hello frame
		var head [5]byte                                                // the client's hello: type, then the body's length
		if _, err := io.ReadFull(c, head[:]); err != nil {
			return
		}
		if _, err := io.CopyN(io.Discard, c, int64(binary.BigEndian.Uint32(head[1:]))); err != nil {
			return
		}
		_, _ = c.Write(frame)
		_, _ = io.ReadAll(c)
	}()

	a := startAttach(t, home, app, "--name", "api", "--json")
	v, exit := a.wait()
	wantError(t, v, exit, api.ErrorCodeAttachUnsupported, api.ExitRejected)
	wantHint(t, v, "to restart its shell")
}

func TestParsePrefixKey(t *testing.T) {
	t.Parallel()
	for key, want := range map[string]string{
		`ctrl-\`: `C-\`, "ctrl-]": "C-]", "ctrl-a": "C-a", "ctrl-b": "C-b", "ctrl-h": "C-h", "ctrl-z": "C-z",
		"ctrl-i": "", "ctrl-m": "", "ctrl-1": "", "ctrl-B": "", "ctrl-[": "", "ctrl-": "", "b": "", "": "",
	} {
		got, err := parsePrefixKey(key)
		if got.label != want || (err != nil) != (want == "") {
			t.Errorf("%q: got %+v, %v; want %q", key, got, err, want)
		}
	}
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
	v, exit = wdxJSONWith(t, home, in(app), "terminal", "history", "--name", "api", "--count", "2")
	if exit != api.ExitOK || v.History.From != 0 || len(v.History.Lines) != 2 {
		t.Fatalf("want the 2 oldest lines, got exit %d, %+v", exit, v.History)
	}
	for _, flags := range [][]string{{"--tail", "2", "--from", "3"}, {"--tail", "2", "--count", "3"}} {
		v, exit = wdxJSONWith(t, home, in(app), append([]string{"terminal", "history", "--name", "api"}, flags...)...)
		wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	}
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
