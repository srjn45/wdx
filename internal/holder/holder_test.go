package holder

import (
	"context"
	"errors"
	"flag"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logtest"
	"github.com/srjn45/wdx/internal/waittest"
)

// TestMain makes the test binary a holder when run as "<binary> holder",
// as Start runs wdx; the cli's hidden command does the same in wdx.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "holder" {
		os.Exit(runHolder(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func runHolder(args []string) int {
	flags := flag.NewFlagSet("holder", flag.ContinueOnError)
	var o Options
	flags.StringVar(&o.ID, "id", "", "")
	flags.StringVar(&o.Project, "project", "", "")
	flags.StringVar(&o.Dir, "dir", "", "")
	flags.StringVar(&o.Shell, "shell", "", "")
	flags.Func("command", "", func(word string) error {
		o.Command = append(o.Command, word)
		return nil
	})
	flags.Int64Var(&o.HistoryLines, "history-lines", 0, "")
	if flags.Parse(args) != nil {
		return 2
	}
	home := os.Getenv(identity.Env("HOME")) //nolint:forbidigo // the test binary is this holder's main, which reads its home as cmd/wdx does
	o.Home = home
	o.Socket = identity.HolderSocket(home, o.ID)
	o.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, identity.Env("HOME")+"=") })
	o.Ready = os.NewFile(3, "ready")
	if Run(context.Background(), o) != nil {
		return 1
	}
	return 0
}

type fixture struct {
	home, dir string
	exe       string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	// Sockets live under home: a path in t.TempDir can be too long for one.
	home, err := os.MkdirTemp("/tmp", "wdx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	// Runs before the home goes, after the holders in it were stopped.
	logtest.OnFailure(t, home)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return fixture{home: home, dir: t.TempDir(), exe: exe}
}

// script writes a shell script that runs body and returns its path.
func (f fixture) script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(f.home, "shell-"+strings.ReplaceAll(t.Name(), "/", "-"))
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func (f fixture) options(id, shell string) StartOptions {
	return StartOptions{
		Executable: f.exe, Home: f.home, ID: id, Project: "warden", Dir: f.dir, Shell: shell,
		Env: []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"},
	}
}

// start starts a holder running shell; the test kills whatever of it is
// left when it ends.
func (f fixture) start(t *testing.T, id, shell string) Started {
	t.Helper()
	s, err := Start(context.Background(), f.options(id, shell))
	if err != nil {
		t.Fatal(err)
	}
	// Most tests end their shell and holder; this catches the rest.
	t.Cleanup(func() { killHolder(s) })
	return s
}

func killHolder(s Started) {
	_ = syscall.Kill(-s.PID, syscall.SIGKILL)
	_ = syscall.Kill(s.HolderPID, syscall.SIGKILL)
}

func (f fixture) dial(t *testing.T, id string) *Conn {
	t.Helper()
	c, err := Dial(context.Background(), identity.HolderSocket(f.home, id), id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func waitExit(t *testing.T, c *Conn) Exit {
	t.Helper()
	select {
	case e := <-c.Exited():
		return e
	case <-c.Done():
		t.Fatal("the connection ended before the shell exited")
	case <-waittest.After(t):
		t.Fatal("the shell did not exit")
	}
	return Exit{}
}

// waitGone waits until process pid no longer exists.
func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := waittest.Deadline(t)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("process %d is still running", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestShellEnvironment(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	out := filepath.Join(f.home, "env")
	f.start(t, "term-00000001", f.script(t, "pwd > "+out+"; env >> "+out+"; exit 3"))
	c := f.dial(t, "term-00000001")
	if e := waitExit(t, c); e.Code != 3 {
		t.Fatalf("want exit code 3, got %d", e.Code)
	}

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if want, _ := filepath.EvalSymlinks(f.dir); lines[0] != want && lines[0] != f.dir {
		t.Errorf("want the shell in %s, got %s", f.dir, lines[0])
	}
	account, err := LookupAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"HOME": account.Home, "USER": account.Name, "LOGNAME": account.Name, "SHELL": account.LoginShell(),
		"TERM": "xterm-256color", "WDX_TERMINAL_ID": "term-00000001", "WDX_PROJECT": "warden",
		"WDX_HOME": f.home, "PATH": "/usr/bin:/bin", "LANG": "C.UTF-8",
	}
	// What sh sets itself.
	added := []string{"PWD", "OLDPWD", "SHLVL", "_"}
	got := map[string]string{}
	for _, kv := range lines[1:] {
		k, v, _ := strings.Cut(kv, "=")
		if !slices.Contains(added, k) {
			got[k] = v
		}
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: want %q, got %q", k, v, got[k])
		}
		delete(got, k)
	}
	if len(got) > 0 {
		t.Errorf("want nothing else in the environment, got %v", got)
	}
}

func TestStopHangsUp(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	s := f.start(t, "term-00000002", f.script(t, "exec sleep 60"))
	c := f.dial(t, "term-00000002")
	if h := c.Hello(); h.PID != s.PID || h.HolderPID != s.HolderPID || h.Exited != nil || h.Version != Version {
		t.Fatalf("want the running shell %d, got %+v", s.PID, h)
	}
	if err := c.Stop(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if e := waitExit(t, c); e.Code != 128+int(syscall.SIGHUP) {
		t.Fatalf("want the shell hung up, got exit code %d", e.Code)
	}
}

func TestStopKillsAfterGrace(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.start(t, "term-00000003", f.script(t, "trap '' HUP; touch trapped; while :; do sleep 1; done"))
	c := f.dial(t, "term-00000003")
	// A hangup that comes before the trap is set ends the shell.
	deadline := waittest.Deadline(t)
	for _, err := os.Stat(filepath.Join(f.dir, "trapped")); err != nil; _, err = os.Stat(filepath.Join(f.dir, "trapped")) {
		if time.Now().After(deadline) {
			t.Fatal("the shell did not set its trap")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := c.Stop(100 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if e := waitExit(t, c); e.Code != 128+int(syscall.SIGKILL) {
		t.Fatalf("want the shell killed, got exit code %d", e.Code)
	}
}

func TestExitBySignal(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.start(t, "term-00000004", f.script(t, "kill -TERM $$"))
	if e := waitExit(t, f.dial(t, "term-00000004")); e.Code != 128+int(syscall.SIGTERM) {
		t.Fatalf("want 128 + SIGTERM, got %d", e.Code)
	}
}

// TestCommandReplacesTheShell runs a command through the shell: its end is
// the terminal's, so no shell is left to type in.
func TestCommandReplacesTheShell(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	o := f.options("term-00000020", "/bin/sh")
	o.Command = []string{"/bin/sh", "-c", "exit 7"}
	s, err := Start(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killHolder(s) })
	if e := waitExit(t, f.dial(t, "term-00000020")); e.Code != 7 {
		t.Fatalf("want the command's exit code 7, got %d", e.Code)
	}
}

// TestCommandWords passes words the shell must not split or expand.
func TestCommandWords(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	got := filepath.Join(f.dir, "got")
	o := f.options("term-00000021", "/bin/sh")
	o.Command = []string{"/bin/sh", "-c", `printf %s "$1" > "$2"`, "sh", `it's $HOME and two  spaces`, got}
	s, err := Start(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killHolder(s) })
	if e := waitExit(t, f.dial(t, "term-00000021")); e.Code != 0 {
		t.Fatalf("want exit code 0, got %d", e.Code)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if want := `it's $HOME and two  spaces`; string(data) != want {
		t.Fatalf("the command got %q, want %q", data, want)
	}
}

// TestOutputDrained runs a shell that writes more than a PTY buffers: it
// ends only if the holder reads its output.
func TestOutputDrained(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.start(t, "term-00000005", f.script(t, "yes | head -c 4000000; exit 0"))
	if e := waitExit(t, f.dial(t, "term-00000005")); e.Code != 0 {
		t.Fatalf("want exit code 0, got %d", e.Code)
	}
}

// TestReconnectAndRelease connects as a restarted daemon would: the exit is
// kept until the daemon releases the holder, which then quits.
func TestReconnectAndRelease(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	s := f.start(t, "term-00000006", f.script(t, "exec sleep 60"))
	first := f.dial(t, "term-00000006")
	second := f.dial(t, "term-00000006")
	select {
	case <-first.Done():
	case <-waittest.After(t):
		t.Fatal("want the first connection replaced by the second")
	}
	if err := second.Stop(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	exit := waitExit(t, second)
	second.Close()

	third := f.dial(t, "term-00000006")
	if h := third.Hello(); h.Exited == nil || *h.Exited != exit {
		t.Fatalf("want the exit %+v kept, got %+v", exit, h.Exited)
	}
	if e := waitExit(t, third); e != exit {
		t.Fatalf("want Exited to receive %+v, got %+v", exit, e)
	}
	if err := third.Release(); err != nil {
		t.Fatal(err)
	}
	waitGone(t, s.HolderPID)
	if _, err := os.Stat(identity.HolderSocket(f.home, "term-00000006")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want the socket removed, got %v", err)
	}
}

func TestReleaseIgnoredWhileRunning(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	s := f.start(t, "term-00000007", f.script(t, "exec sleep 60"))
	if err := f.dial(t, "term-00000007").Release(); err != nil {
		t.Fatal(err)
	}
	c := f.dial(t, "term-00000007")
	if h := c.Hello(); h.PID != s.PID || h.Exited != nil {
		t.Fatalf("want the shell still running, got %+v", h)
	}
}

func TestHolderGone(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	s := f.start(t, "term-00000008", f.script(t, "exec sleep 60"))
	c := f.dial(t, "term-00000008")
	if err := syscall.Kill(s.HolderPID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-waittest.After(t):
		t.Fatal("want the connection ended with the holder")
	}
	if _, err := Dial(context.Background(), identity.HolderSocket(f.home, "term-00000008"), "term-00000008"); err == nil {
		t.Fatal("want no holder to answer")
	}
}

func TestDialOtherTerminal(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.start(t, "term-00000009", f.script(t, "exec sleep 60"))
	if _, err := Dial(context.Background(), identity.HolderSocket(f.home, "term-00000009"), "term-0000000a"); err == nil || !strings.Contains(err.Error(), "runs terminal term-00000009") {
		t.Fatalf("want the other terminal refused, got %v", err)
	}
}

// TestDialVersions answers the daemon's hello as a holder of each version
// would: the daemon drives versions 1 and 2, so that it keeps the holders
// of the release it was rolled back from.
func TestDialVersions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		version int
		drives  bool
	}{{0, false}, {1, true}, {2, true}, {3, false}} {
		f := newFixture(t)
		socket := filepath.Join(f.home, "holder.sock")
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
			if _, _, err := readFrame(c); err != nil {
				return
			}
			_ = writeFrame(c, frameHello, Hello{Version: tc.version, TerminalID: "term-0000000b"})
			_, _, _ = readFrame(c) // until the daemon closes
		}()
		c, err := Dial(context.Background(), socket, "term-0000000b")
		if tc.drives {
			if err != nil {
				t.Fatalf("version %d: %v", tc.version, err)
			}
			if c.Hello().Version != tc.version {
				t.Fatalf("version %d: hello says %d", tc.version, c.Hello().Version)
			}
			c.Close()
		} else if err == nil || !strings.Contains(err.Error(), "cannot drive") {
			t.Fatalf("version %d: want it refused, got %v", tc.version, err)
		}
	}
}

func TestStartFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, tc := range []struct {
		name  string
		edit  func(*StartOptions)
		error string
	}{
		{"no shell", func(o *StartOptions) { o.Shell = "/nonexistent/sh" }, "/nonexistent/sh"},
		{"no dir", func(o *StartOptions) { o.Dir = "/nonexistent/dir" }, "/nonexistent/dir"},
		{"no holder", func(o *StartOptions) { o.Executable = "/bin/false" }, "exited before starting the shell"},
	} {
		o := f.options("term-0000000b", "/bin/sh")
		tc.edit(&o)
		if _, err := Start(context.Background(), o); err == nil || !strings.Contains(err.Error(), tc.error) {
			t.Errorf("%s: want an error naming %q, got %v", tc.name, tc.error, err)
		}
	}
	if _, err := os.Stat(identity.HolderSocket(f.home, "term-0000000b")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want no socket left, got %v", err)
	}
}

func TestRunDirPrivate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.start(t, "term-0000000c", f.script(t, "exec sleep 60"))
	info, err := os.Stat(filepath.Dir(identity.HolderSocket(f.home, "term-0000000c")))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("want the run dir 0700, got %v, %v", info.Mode(), err)
	}
}

func TestPasswdShell(t *testing.T) {
	t.Parallel()
	line := "me:x:1000:1000:Me,,,:/home/me:/usr/bin/zsh"
	if got := passwdShell(line, "1000"); got != "/usr/bin/zsh" {
		t.Fatalf("got %q", got)
	}
	if got := passwdShell(line, "1001"); got != "" {
		t.Fatalf("want another user's line ignored, got %q", got)
	}
	if got := passwdShell("broken", "1000"); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestLookupAccount(t *testing.T) {
	t.Parallel()
	a, err := LookupAccount(context.Background())
	if err != nil || a.Name == "" || !filepath.IsAbs(a.Home) || !filepath.IsAbs(a.LoginShell()) {
		t.Fatalf("got %+v, %v", a, err)
	}
	if got := (Account{}).LoginShell(); got != "/bin/sh" {
		t.Fatalf("want /bin/sh without a shell in the database, got %q", got)
	}
}

// TestScreenKeptOnExit: what the shell left on the main screen is in
// history by the time the exit is heard, so a client reading history then
// shows it; the alternate screen never goes there.
func TestScreenKeptOnExit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, id, body string
		want           []string
	}{
		{"main screen", "term-00000010", `printf 'one\ntwo\n\n\n'; exit 3`, []string{"one", "two"}},
		{"alternate screen", "term-00000011", `printf 'main\n\033[?1049halt'; exit 0`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			id := tc.id
			o := f.options(id, f.script(t, tc.body))
			o.HistoryLines = 1000
			s, err := Start(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { killHolder(s) })
			c := f.dial(t, id)
			waitExit(t, c)
			got, err := TailHistory(HistoryDir(f.home, id), 10)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got.Lines, tc.want) {
				t.Fatalf("history when the exit is heard: %q, want %q", got.Lines, tc.want)
			}
			if err := c.Release(); err != nil {
				t.Fatal(err)
			}
			waitGone(t, s.HolderPID)
			if again, err := TailHistory(HistoryDir(f.home, id), 10); err != nil || !slices.Equal(again.Lines, tc.want) {
				t.Fatalf("history after the holder quit: %q, %v, want it unchanged", again.Lines, err)
			}
		})
	}
}
