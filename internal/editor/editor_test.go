package editor

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/pty"
	"github.com/srjn45/wdx/internal/runtest"
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
		if !ok || e.Name != name || e.Save == "" || e.Discard == "" {
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

// running is an editor started on a file in a PTY.
type running struct {
	t      *testing.T
	cmd    *exec.Cmd
	pty    *os.File
	mu     sync.Mutex
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
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "TERM=xterm")
	master, err := pty.Start(cmd, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	r := &running{t: t, cmd: cmd, pty: master, exited: make(chan struct{})}
	read := make(chan struct{})
	go func() {
		defer close(read)
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			r.mu.Lock()
			r.shown.Write(buf[:n])
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
		_ = cmd.Process.Kill()
		<-r.exited
		master.Close()
		<-read
	})
	r.waitShown(readMessage)
	return r
}

func (r *running) write(keys string) {
	r.t.Helper()
	if _, err := r.pty.WriteString(keys); err != nil {
		r.t.Fatal(err)
	}
}

func (r *running) waitShown(text string) {
	r.t.Helper()
	ok := waittest.Until(r.t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return bytes.Contains(r.shown.Bytes(), []byte(text))
	})
	if !ok {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.t.Fatalf("waited until the deadline for %q on the screen, which got %q", text, r.shown.String())
	}
}

func (r *running) waitExited() {
	r.t.Helper()
	select {
	case <-r.exited:
	case <-waittest.After(r.t):
		r.mu.Lock()
		defer r.mu.Unlock()
		r.t.Fatalf("waited until the deadline for the editor to quit, which showed %q", r.shown.String())
	}
}

// TestVi drives the real vi through each case measured in docs/TIDE_CODE.md.
func TestVi(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("vi"); err != nil {
		if runtime.GOOS == "linux" {
			t.Fatal("vi is needed to test it: install vim-tiny")
		}
		t.Skip("vi is not installed")
	}
	vi, _ := Lookup("vi")
	save := func(e Editor) string { return e.Save }
	discard := func(e Editor) string { return e.Discard }
	for _, tc := range []struct {
		name     string
		keys     string // typed once the file shows
		answer   func(Editor) string
		other    string // written to the file by another program once it shows; "" for none
		readOnly bool
		stays    string // what vi says when it does not quit; "" when it does
		want     string // the file at the end
	}{
		{name: "save from insert mode", keys: "A more", answer: save, want: "first more\n"},
		{name: "discard from insert mode", keys: "A more", answer: discard, want: "first\n"},
		{name: "save with a command half typed", keys: "A more\x1b:s/first/", answer: save, want: "first more\n"},
		{name: "discard from visual mode", keys: "A more\x1bv", answer: discard, want: "first\n"},
		{name: "an arrow key in insert mode", keys: "A more\x1b[DX", answer: save, want: "first morXe\n"},
		{name: "another program wrote it, not edited", answer: save, other: "theirs\n", want: "theirs\n"},
		{name: "another program wrote it, edited", keys: "A more", answer: save, other: "theirs\n", stays: "(y/n)", want: "theirs\n"},
		{name: "read only, edited", keys: "A more", answer: save, readOnly: true, stays: "E45", want: "first\n"},
		{name: "a second file edited, saved", keys: "A more\x1b:set hidden\r:e second\rAtheirs\x1b", answer: save, stays: "E162", want: "first more\n"},
		{name: "a second file edited, discarded", keys: "A more\x1b:set hidden\r:e second\rAtheirs\x1b", answer: discard, stays: "E162", want: "first\n"},
	} {
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
			r := run(t, vi, dir, "first.txt", "1 line")
			if tc.other != "" {
				if err := os.WriteFile(path, []byte(tc.other), 0o644); err != nil {
					t.Fatal(err)
				}
				// vi tells a change by the time, which may not move within its granularity.
				later := time.Now().Add(time.Hour)
				if err := os.Chtimes(path, later, later); err != nil {
					t.Fatal(err)
				}
			}
			r.write(tc.keys + tc.answer(vi))
			if tc.stays == "" {
				r.waitExited()
			} else {
				r.waitShown(tc.stays)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != tc.want {
				t.Errorf("file %q, %v; want %q", got, err, tc.want)
			}
			if _, err := os.Stat(filepath.Join(dir, "second")); !os.IsNotExist(err) {
				t.Errorf("the second file was written: %v", err)
			}
		})
	}
}
