// Package runtest gives a test binary a scope of its own, so that it affects
// neither the tests beside it nor other test runs on the machine: a directory
// only it uses, an environment that says nothing of the machine, executables
// no fork can hold busy, and a check at its end that it left nothing behind.
package runtest

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/gittest"
)

// base holds the runs' directories. It is not os.TempDir because sockets go
// under it, and a path there can exceed the 104 bytes macOS allows.
const base = "/tmp"

const (
	runPrefix = "wdxrun"
	// newPrefix names a run's directory until it is locked, so that no sweep
	// can take it for one whose run has died.
	newPrefix = "wdxnew"
	lockName  = "lock"
	homeName  = "home"
)

// leftWait is how long the processes a run started get to end after its last
// test. They were all told to end by then, so one still there has leaked.
const leftWait = 30 * time.Second

var (
	root     atomic.Pointer[string]
	original []string
	// lock keeps the run's directory from being swept: the kernel releases
	// it when the process ends, however it ends.
	lock *os.File
)

// Main runs m in a scope of its own and returns its exit code, for TestMain.
func Main(m *testing.M) int {
	done := Start()
	return done(m.Run())
}

// Start sets up the scope, for a TestMain with work to do before its tests,
// and returns what ends it. Call it first, before anything starts a goroutine
// or a process, since it replaces the process's environment.
func Start() (done func(code int) int) {
	sweep(base)
	dir, err := create(base)
	if err != nil {
		panic(fmt.Sprintf("runtest: %v", err))
	}
	root.Store(&dir)
	original = os.Environ()
	if err := setEnviron(dir); err != nil {
		panic(fmt.Sprintf("runtest: %v", err))
	}
	gittest.Isolate()
	if err := checkExecutable(dir); err != nil {
		panic(fmt.Sprintf("runtest: tests run scripts from %s, which cannot run them: %v", dir, err))
	}
	return func(code int) int { return finish(dir, code, leftWait) }
}

// Dir returns a new directory short enough to hold sockets, removed when t
// ends. It holds a test's home.
func Dir(t *testing.T) string {
	t.Helper()
	dir := root.Load()
	if dir == nil {
		t.Fatal("runtest.Dir needs runtest.Main in the package's TestMain")
	}
	d, err := os.MkdirTemp(*dir, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeAll(d) })
	return d
}

// LoginShell writes a shell for a test's terminals and returns its path. A
// real login shell runs the machine's profile, which says nothing of wdx and
// can take any time. This one stays until it is hung up on and, given a
// command as an agent's holder gives it, runs it, skipping the -l and -i.
func LoginShell(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shell")
	if err := WriteExecutable(path, LoginShellScript); err != nil {
		t.Fatal(err)
	}
	return path
}

// LoginShellScript is what LoginShell writes.
const LoginShellScript = "#!/bin/sh\nif [ \"$3\" = -c ]; then exec /bin/sh -c \"$4\"; fi\nexec sleep 60\n"

// ProductionLimits reports whether this run keeps wdx's real limits on how
// long its processes wait for each other, instead of ones a loaded machine
// does not reach. The nightly job runs so once: what fails only then is what a
// user on a loaded machine could hit, which the usual limits would hide.
func ProductionLimits() bool {
	return slices.Contains(original, productionLimitsVar+"=1")
}

const productionLimitsVar = "WDX_TEST_PRODUCTION_LIMITS"

// OriginalEnviron is the environment the test binary started with, for the
// programs that need the machine's: the go command and its caches.
func OriginalEnviron() []string { return slices.Clone(original) }

// create makes a run's directory under dir and holds its lock.
func create(dir string) (string, error) {
	tmp, err := os.MkdirTemp(dir, newPrefix)
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(filepath.Join(tmp, lockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return "", err
	}
	lock = f
	run := filepath.Join(dir, runPrefix+strings.TrimPrefix(filepath.Base(tmp), newPrefix))
	if err := os.Rename(tmp, run); err != nil {
		return "", err
	}
	return run, nil
}

// setEnviron replaces the process's environment with one that says nothing
// of the machine or of who runs the tests. PATH finds the programs and GO*
// keeps what go test passes on (coverage, the race detector); the rest is
// fixed. Temporary files go under dir, and home is empty.
func setEnviron(dir string) error {
	home := filepath.Join(dir, homeName)
	if err := os.Mkdir(home, 0o700); err != nil {
		return err
	}
	env := []string{"HOME=" + home, "TMPDIR=" + dir, "LANG=C.UTF-8"}
	for _, kv := range original {
		if key, _, _ := strings.Cut(kv, "="); key == "PATH" || strings.HasPrefix(key, "GO") {
			env = append(env, kv)
		}
	}
	os.Clearenv()
	for _, kv := range env {
		key, value, _ := strings.Cut(kv, "=")
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return nil
}

// checkExecutable fails when scripts cannot run from dir, as under a /tmp
// mounted noexec, rather than have every test that runs one fail its own way.
func checkExecutable(dir string) error {
	path := filepath.Join(dir, "check-executable")
	if err := WriteExecutable(path, "#!/bin/sh\nexit 0\n"); err != nil {
		return err
	}
	defer os.Remove(path)
	return exec.Command(path).Run()
}

// finish checks that the run left nothing behind and removes what it did,
// returning code, or a failing one when it had passed.
func finish(dir string, code int, wait time.Duration) int {
	var problems []string
	deadline := time.Now().Add(wait)
	left := processesOf(dir)
	for len(left) > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		left = processesOf(dir)
	}
	for _, p := range left {
		problems = append(problems, fmt.Sprintf("process %d still runs: %s", p.pid, p.cmdline))
		_ = syscall.Kill(p.pid, syscall.SIGKILL)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		problems = append(problems, err.Error())
	}
	for _, e := range entries {
		if e.Name() != lockName && e.Name() != homeName {
			problems = append(problems, "left in the run's directory: "+e.Name())
		}
	}
	removeAll(dir)
	lock.Close()
	if len(problems) == 0 {
		return code
	}
	fmt.Fprintf(os.Stderr, "runtest: the tests left things behind:\n  %s\n", strings.Join(problems, "\n  "))
	if code == 0 {
		return 1
	}
	return code
}

// sweep removes the directories of runs that died under dir, with the
// processes they left: a directory whose lock can be taken has no run. A run
// that is alive holds its lock, however long it has run.
func sweep(dir string) {
	runs, _ := filepath.Glob(filepath.Join(dir, runPrefix+"*"))
	for _, run := range runs {
		f, err := os.Open(filepath.Join(run, lockName))
		if err != nil {
			continue // another sweep is removing it
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			f.Close()
			continue
		}
		for _, p := range processesOf(run) {
			_ = syscall.Kill(p.pid, syscall.SIGKILL)
		}
		removeAll(run)
		f.Close()
	}
}

// removeAll removes dir, making writable the directories a test made
// read-only.
func removeAll(dir string) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(path, 0o700)
		}
		return nil
	})
	_ = os.RemoveAll(dir)
}

type process struct {
	pid     int
	cmdline string
}

// processesOf returns the running processes that belong to the run in dir:
// those working in it, started with a path in it or with it as their TMPDIR.
// Without /proc, as on macOS, it finds none.
func processesOf(dir string) []process {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var found []process
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() || !Alive(pid) {
			continue
		}
		cmdline, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		cwd, _ := os.Readlink(filepath.Join("/proc", e.Name(), "cwd"))
		environ, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
		if cwd == dir || strings.HasPrefix(cwd, dir+"/") ||
			bytes.Contains(cmdline, []byte(dir+"/")) ||
			slices.Contains(strings.Split(string(environ), "\x00"), "TMPDIR="+dir) {
			found = append(found, process{pid, string(bytes.ReplaceAll(bytes.TrimRight(cmdline, "\x00"), []byte{0}, []byte{' '}))})
		}
	}
	return found
}

// Alive reports whether process pid is running. A zombie is not: it has
// ended and only waits to be reaped, which where nothing reaps orphans, as in
// a container without an init, may never come.
func Alive(pid int) bool {
	if pid <= 0 || syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return true // no /proc, as on macOS: kill is all there is to ask
	}
	// The state comes after the command, which is in parentheses and may
	// hold anything.
	_, state, ok := strings.Cut(string(stat[bytes.LastIndexByte(stat, ')')+1:]), " ")
	return !ok || !strings.HasPrefix(state, "Z")
}

// WriteExecutable writes a program at path. Parallel tests share a process,
// and a child forked while the file is open for writing holds it open until
// it execs, so that running the file fails with "text file busy"
// (golang.org/issue/22315). No fork can happen while ForkLock is held for
// reading, and on Linux a fork holds it until its child has exec'd.
func WriteExecutable(path, content string) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	return os.WriteFile(path, []byte(content), 0o755)
}

// CopyExecutable copies the program at src to path, a block at a time, as
// WriteExecutable writes one.
func CopyExecutable(path, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
