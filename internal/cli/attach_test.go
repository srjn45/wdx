package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/pty"
)

// interactiveShell is a shell to type into.
const interactiveShell = "#!/bin/sh\nPS1='$ ' exec /bin/sh\n"

// attachRun is wdx terminal attach running in a terminal of its own, a
// PTY the test reads and types into.
type attachRun struct {
	t      *testing.T
	home   string
	args   []string
	master *os.File
	done   chan output

	mu     sync.Mutex
	screen bytes.Buffer // everything written to the terminal
}

func startAttach(t *testing.T, home, dir string, args ...string) *attachRun {
	t.Helper()
	master, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { master.Close() })
	if err := pty.Setsize(master, 80, 24); err != nil {
		t.Fatal(err)
	}
	a := &attachRun{t: t, home: home, args: append([]string{"terminal", "attach"}, args...), master: master, done: make(chan output, 1)}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			a.mu.Lock()
			a.screen.Write(buf[:n])
			a.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	go func() {
		var stdout, stderr bytes.Buffer
		env := Env{
			Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Config: testConfig(home), Executable: wdxBinary,
			WorkDir: dir, TTY: func() (*os.File, error) { return tty, nil },
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
}

func (a *attachRun) waitScreen(s string) {
	a.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		a.mu.Lock()
		got := a.screen.String()
		a.mu.Unlock()
		if strings.Contains(got, s) {
			return
		}
		if time.Now().After(deadline) {
			a.t.Fatalf("no %q on the terminal: %q", s, got)
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
		a.t.Fatalf("%v did not end", a.args)
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

func TestTerminalAttach(t *testing.T) {
	t.Parallel()
	home := newHomeWithShell(t, interactiveShell)
	app := newProjectDir(t, home, "app")
	term := newTerminal(t, home, app, "api")

	a := startAttach(t, home, app, "--name", "api", "--json")
	a.input("echo hel''lo\n")
	a.waitScreen("hello\r\n")
	a.input("\x1c")
	v, exit := a.wait()
	if exit != api.ExitOK || v.Result != "detached" || v.Terminal == nil || v.Terminal.Id != term.Id || v.Terminal.State != api.TerminalStateRunning {
		t.Fatalf("want detached with the terminal running, got exit %d, %+v", exit, v)
	}
	a.waitScreen("\x1b[?1049l\x1b[?25h") // the terminal is given back as it was

	// Attaching again shows the screen it left.
	b := startAttach(t, home, app, "--id", term.Id, "--json")
	b.waitScreen("hello")
	b.input("exit 3\n")
	v, exit = b.wait()
	if exit != api.ExitOK || v.Result != "exited" || v.Terminal.State != api.TerminalStateExited || v.Terminal.ExitCode == nil || *v.Terminal.ExitCode != 3 {
		t.Fatalf("want exited with code 3, got exit %d, %+v", exit, v)
	}

	c := startAttach(t, home, app, "--name", "api", "--json")
	v, exit = c.wait()
	wantError(t, v, exit, api.ErrorCodeTerminalNotRunning, api.ExitRejected)
	wantHint(t, v, "terminal start")
}

// TestTerminalAttachDetachKey sends the detach key to the shell by pressing
// it twice, then detaches with another key.
func TestTerminalAttachDetachKey(t *testing.T) {
	t.Parallel()
	home := newHomeWithShell(t, interactiveShell)
	app := newProjectDir(t, home, "app")
	newTerminal(t, home, app, "api")

	a := startAttach(t, home, app, "--name", "api", "--json")
	a.input("stty raw -echo; echo rea''dy; dd bs=1 count=2 2>/dev/null | od -An -c; stty sane\n")
	a.waitScreen("ready")
	a.input("\x1c\x1cx")
	a.waitScreen("034   x")
	a.input("\x1c")
	if v, exit := a.wait(); exit != api.ExitOK || v.Result != "detached" {
		t.Fatalf("want detached, got exit %d, %+v", exit, v)
	}

	b := startAttach(t, home, app, "--name", "api", "--detach-key", "ctrl-]")
	b.waitScreen("$")
	b.input("\x1d")
	if out := b.waitText(); out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "detached from terminal api in app\n") {
		t.Fatalf("want detached by ctrl-], got exit %d, %q", out.exit, out.stdout)
	}
}

// TestTerminalAttachHistory attaches with history: the lines before the
// screen are written first, for the terminal to scroll back to.
func TestTerminalAttachHistory(t *testing.T) {
	t.Parallel()
	home := newHomeWithShell(t, "#!/bin/sh\nseq 1 100\nPS1='$ ' exec /bin/sh\n")
	app := newProjectDir(t, home, "app")
	newTerminal(t, home, app, "api")
	waitHistory(t, home, app, "api", 77)

	a := startAttach(t, home, app, "--name", "api", "--history", "3", "--json")
	a.waitScreen("$")
	a.input("\x1c")
	if _, exit := a.wait(); exit != api.ExitOK {
		t.Fatalf("got exit %d", exit)
	}
	a.mu.Lock()
	screen := a.screen.String()
	a.mu.Unlock()
	m := regexp.MustCompile("\x1b\\[22;0t(\\d+)\r\n(\\d+)\r\n(\\d+)\r\n").FindStringSubmatch(screen)
	if m == nil || m[1] != "75" || m[2] != "76" || m[3] != "77" {
		t.Fatalf("want lines 75 to 77 first, got %q", screen[:min(len(screen), 200)])
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
	a.waitScreen("$")
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
	v, exit = wdxJSONWith(t, home, in(app), "terminal", "attach", "--name", "api", "--detach-key", "ctrl-1")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	v, exit = wdxJSONWith(t, home, in(app), "terminal", "attach", "--name", "api", "--history", "some")
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
