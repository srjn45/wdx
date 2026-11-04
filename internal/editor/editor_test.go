package editor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/pty"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/screen"
	"github.com/srjn45/wdx/internal/waittest"
)

func TestMain(m *testing.M) { os.Exit(runtest.Main(m)) }

func TestTable(t *testing.T) {
	t.Parallel()
	if !slices.Contains(Names(), Default) {
		t.Fatalf("the default %q is not among %v", Default, Names())
	}
	for _, name := range Names() {
		e, ok := Lookup(name)
		if !ok || e.Name != name || e.Save("a") == "" || e.Discard("a") == "" {
			t.Errorf("%s: %+v, %v", name, e, ok)
		}
		if cmd := e.Command("-x"); cmd[0] != name || cmd[len(cmd)-2] != "--" || cmd[len(cmd)-1] != "-x" {
			t.Errorf("%s runs %q, which does not take -x as the file", name, cmd)
		}
	}
	if _, ok := Lookup("ed"); ok {
		t.Error("ed is not supported")
	}
}

// running is an editor started on a file in a PTY, its output kept on a
// screen that answers its queries, as a holder's does.
type running struct {
	t      *testing.T
	cmd    *exec.Cmd
	pty    *os.File
	mu     sync.Mutex
	screen *screen.Screen
	shown  bytes.Buffer // all it wrote to the terminal
	exited chan struct{}
}

// run starts e on the file name in dir, with no settings of the user's, and
// waits until it shows that it read it.
func run(t *testing.T, e Editor, dir, name, readMessage string) *running {
	t.Helper()
	argv := e.Command(name)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	// No comma in the test's name, which is in its directories: Neovim escapes one
	// in the path of its log, cannot open it and writes .nvimlog beside the file.
	// Neovim keeps its server's socket under TMPDIR. Emacs, with no cache of its
	// compiled packages in an empty HOME, would compile them in processes of its own
	// that outlive it, which the run's end finds left.
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "TMPDIR="+t.TempDir(), "TERM=xterm-256color",
		"EMACS_INHIBIT_AUTOMATIC_NATIVE_COMPILATION=1")
	master, err := pty.Start(cmd, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	r := &running{t: t, cmd: cmd, pty: master, exited: make(chan struct{})}
	r.screen = screen.New(80, 24, func(p []byte) { _, _ = master.Write(p) })
	read := make(chan struct{})
	go func() {
		defer close(read)
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			r.mu.Lock()
			r.shown.Write(buf[:n])
			r.screen.Write(buf[:n])
			r.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer close(r.exited)
		_ = cmd.Wait()
	}()
	t.Cleanup(func() {
		// Neovim's interface runs its editor as a child in a session of its own, which
		// quits soon after the interface: killed too, and waited for.
		children := childrenOf(cmd.Process.Pid)
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		for _, pid := range children {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		<-r.exited
		for _, pid := range children {
			if !waittest.Until(t, func() bool { return syscall.Kill(pid, 0) != nil }) {
				t.Errorf("process %d of the editor's still runs", pid)
			}
		}
		master.Close()
		<-read
		r.screen.Close()
	})
	r.waitShown(readMessage)
	return r
}

// childrenOf returns the processes pid started.
func childrenOf(pid int) []int {
	b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
	var pids []int
	for _, f := range strings.Fields(string(b)) {
		if n, err := strconv.Atoi(f); err == nil {
			pids = append(pids, n)
		}
	}
	return pids
}

func (r *running) write(keys string) {
	r.t.Helper()
	if _, err := r.pty.WriteString(keys); err != nil {
		r.t.Fatal(err)
	}
}

// text is the screen as it is now, then all it was sent, as text: a message may
// be drawn over before it is read.
func (r *running) text() (screen, shown string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.screen.Text(), "\n"), r.shown.String()
}

func (r *running) waitShown(text string) {
	r.t.Helper()
	ok := waittest.Until(r.t, func() bool {
		screen, shown := r.text()
		return strings.Contains(screen, text) || strings.Contains(shown, text)
	})
	if !ok {
		screen, _ := r.text()
		r.t.Fatalf("waited until the deadline for %q on the screen, which shows:\n%s", text, screen)
	}
}

func (r *running) waitExited() {
	r.t.Helper()
	select {
	case <-r.exited:
	case <-waittest.After(r.t):
		screen, _ := r.text()
		r.t.Fatalf("waited until the deadline for the editor to quit, which shows:\n%s", screen)
	}
}

// editCase is a way to leave an editor started on first.txt, which holds
// "first\n".
type editCase struct {
	name     string
	keys     string // typed once the file shows
	discard  bool   // answered with Discard rather than Save
	other    string // written to the file by another program once it shows; "" for none
	readOnly bool
	stays    string // what the editor says when it does not quit; "" when it does
	want     string // the file at the end
}

// drive runs the editor named name through each case, and checks the file at
// the end and that nothing else was written beside it. read is what it shows
// once it has read the file.
func drive(t *testing.T, name, read string, cases []editCase) {
	t.Helper()
	driveLocking(t, name, read, "", cases)
}

// driveLocking is drive for an editor that keeps the lock file named lock beside
// the file while it edits it.
func driveLocking(t *testing.T, name, read, lock string, cases []editCase) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		if name == Default && runtime.GOOS == "linux" {
			t.Fatal("vi is needed to test it: install vim-tiny")
		}
		t.Skip(name + " is not installed")
	}
	e, _ := Lookup(name)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "first.txt")
			// A long name would wrap vi's message and have it wait for Enter.
			mode := os.FileMode(0o644)
			if tc.readOnly {
				mode = 0o444
			}
			if err := os.WriteFile(path, []byte("first\n"), mode); err != nil {
				t.Fatal(err)
			}
			r := run(t, e, dir, "first.txt", read)
			if tc.other != "" {
				if err := os.WriteFile(path, []byte(tc.other), 0o644); err != nil {
					t.Fatal(err)
				}
				// Editors tell a change by the time, which may not move within its granularity.
				later := time.Now().Add(time.Hour)
				if err := os.Chtimes(path, later, later); err != nil {
					t.Fatal(err)
				}
			}
			answer := e.Save("first.txt")
			if tc.discard {
				answer = e.Discard("first.txt")
			}
			r.write(tc.keys + answer)
			if tc.stays == "" {
				r.waitExited()
			} else {
				r.waitShown(tc.stays)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != tc.want {
				t.Errorf("file %q, %v; want %q", got, err, tc.want)
			}
			// Nothing else in the worktree: no swap, backup or lock file, and the second
			// file unwritten.
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() != "first.txt" && (entry.Name() != lock || tc.stays == "") {
					content, _ := os.ReadFile(filepath.Join(dir, entry.Name()))
					t.Errorf("%s left beside the file, holding %q", entry.Name(), content)
				}
			}
		})
	}
}

// vimCases are the cases measured with vi in docs/TIDE_CODE.md, which vim and
// Neovim answer alike but for some messages.
func vimCases(readOnly, secondEdited string) []editCase {
	const second = "A more\x1b:set hidden\r:e second\rAtheirs\x1b"
	return []editCase{
		{name: "save from insert mode", keys: "A more", want: "first more\n"},
		{name: "discard from insert mode", keys: "A more", discard: true, want: "first\n"},
		{name: "save with a command half typed", keys: "A more\x1b:s/first/", want: "first more\n"},
		{name: "discard from visual mode", keys: "A more\x1bv", discard: true, want: "first\n"},
		{name: "an arrow key in insert mode", keys: "A more\x1b[DX", want: "first morXe\n"},
		{name: "written by another program", other: "theirs\n", want: "theirs\n"},
		{name: "written by another program and edited", keys: "A more", other: "theirs\n", stays: "(y/n)", want: "theirs\n"},
		{name: "read only and edited", keys: "A more", readOnly: true, stays: readOnly, want: "first\n"},
		{name: "a second file edited and saved", keys: second, stays: secondEdited, want: "first more\n"},
		{name: "a second file edited and discarded", keys: second, discard: true, stays: secondEdited, want: "first\n"},
	}
}

// Once it has read the file, vi names it in quotes; what follows differs:
// "1 line, 6 bytes" from vim-tiny, "1L, 6B" from vim.
const vimRead = `"first.txt"`

func TestVi(t *testing.T) {
	t.Parallel()
	drive(t, "vi", vimRead, vimCases("E45", "E162"))
}

func TestVim(t *testing.T) {
	t.Parallel()
	drive(t, "vim", vimRead, vimCases("E45", "E162"))
}

func TestNvim(t *testing.T) {
	t.Parallel()
	// Neovim's shortmess leaves out the file's message: its status line names it.
	drive(t, "nvim", "first.txt", vimCases("E45", "E162"))
}

func TestNano(t *testing.T) {
	t.Parallel()
	// Ubuntu's nanorc sets locking, which no flag unsets but ignoring the user's
	// settings. nano names the file in its title bar.
	driveLocking(t, "nano", "first.txt", ".first.txt.swp", []editCase{
		{name: "save", keys: "\x05 more", want: "first more\n"},
		{name: "discard", keys: "\x05 more", discard: true, want: "first\n"},
		{name: "saved not edited", want: "first\n"},
		{name: "discarded not edited", discard: true, want: "first\n"},
		{name: "save at a prompt", keys: "\x05 more\x17fir", want: "first more\n"},
		{name: "an arrow key", keys: "\x05 more\x1b[DX", want: "first morXe\n"},
		{name: "written by another program", other: "theirs\n", want: "theirs\n"},
		{name: "written by another program and edited", keys: "\x05 more", other: "theirs\n", stays: "modified", want: "theirs\n"},
		{name: "read only and edited", keys: "\x05 more", readOnly: true, stays: "ermission", want: "first\n"},
	})
}

func TestHelix(t *testing.T) {
	t.Parallel()
	const second = "A more" + helixEscape + ":open second\rAtheirs" + helixEscape
	drive(t, "hx", "first.txt", []editCase{
		{name: "save from insert mode", keys: "A more", want: "first more\n"},
		{name: "discard from insert mode", keys: "A more", discard: true, want: "first\n"},
		{name: "save with a command half typed", keys: "A more" + helixEscape + ":open fir", want: "first more\n"},
		{name: "discard from select mode", keys: "A more" + helixEscape + "v", discard: true, want: "first\n"},
		{name: "an arrow key in insert mode", keys: "A more\x1b[DX", want: "first morXe\n"},
		{name: "written by another program", other: "theirs\n", want: "theirs\n"},
		{name: "written by another program and edited", keys: "A more", other: "theirs\n", stays: "modified", want: "theirs\n"},
		{name: "read only and edited", keys: "A more", readOnly: true, stays: "read only", want: "first\n"},
		{name: "a second file edited and saved", keys: second, stays: "unsaved buffer", want: "first more\n"},
		{name: "a second file edited and discarded", keys: second, discard: true, stays: "unsaved buffer", want: "first\n"},
	})
}

func TestEmacs(t *testing.T) {
	t.Parallel()
	const second = "\x05 more\x18\x06second\rtheirs"
	drive(t, "emacs", "first.txt", []editCase{
		{name: "save", keys: "\x05 more", want: "first more\n"},
		{name: "discard", keys: "\x05 more", discard: true, want: "first\n"},
		{name: "save at a prompt", keys: "\x05 more\x18\x06sec", want: "first more\n"},
		{name: "an arrow key", keys: "\x05 more\x1b[DX", want: "first morXe\n"},
		{name: "written by another program", other: "theirs\n", want: "theirs\n"},
		{name: "written by another program and edited", keys: "\x05 more", other: "theirs\n", stays: "changed on disk", want: "theirs\n"},
		// emacs edits a read-only file only once told to, with C-x C-q.
		{name: "read only and edited", keys: "\x18\x11\x05 more", readOnly: true, stays: "write-protected", want: "first\n"},
		{name: "a second file edited and saved", keys: second, stays: "Save file", want: "first more\n"},
		{name: "a second file edited and discarded", keys: second, discard: true, stays: "Save file", want: "first\n"},
	})
}

// TestOddNames: hx and emacs, which are given the file's name in what they are
// typed or started with, save a file whose name holds a space, a % or quotes.
func TestOddNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"hx", "emacs"} {
		for _, file := range []string{"a b%c.txt", `it's "%x".txt`} {
			t.Run(name+" "+file, func(t *testing.T) {
				t.Parallel()
				if _, err := exec.LookPath(name); err != nil {
					t.Skip(name + " is not installed")
				}
				e, _ := Lookup(name)
				dir := t.TempDir()
				path := filepath.Join(dir, file)
				if err := os.WriteFile(path, []byte("first\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				r := run(t, e, dir, file, "first")
				edit := map[string]string{"hx": "A more", "emacs": "\x05 more"}[name]
				r.write(edit + e.Save(file))
				r.waitExited()
				if got, err := os.ReadFile(path); err != nil || string(got) != "first more\n" {
					t.Errorf("file %q, %v", got, err)
				}
			})
		}
	}
}

// TestQuote: a path reaches hx and emacs whole, whatever it holds.
func TestQuote(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]string{
		"a b/%c.go": `'a b/%c.go'`,
		`it's\"%`:   `"it's\\\"%%"`,
	} {
		if got := helixQuote(path); got != want {
			t.Errorf("helixQuote(%q) = %s, want %s", path, got, want)
		}
	}
	if got := elispQuote(`a"b\c`); got != `"a\"b\\c"` {
		t.Errorf("elispQuote = %s", got)
	}
}
