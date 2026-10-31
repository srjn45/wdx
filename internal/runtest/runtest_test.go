package runtest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/waittest"
)

// leakMode makes the test binary a run that leaves what its name says behind.
const leakMode = "runtest-leak"

func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == leakMode {
		os.Exit(leakyRun(os.Args[2]))
	}
	os.Exit(Main(m))
}

func leakyRun(what string) int {
	Start()
	dir := *root.Load()
	switch what {
	case "process":
		// It takes the run's TMPDIR with it.
		if err := exec.Command("sleep", "60").Start(); err != nil {
			panic(err)
		}
		// So that it is seen: the check runs at once.
		for len(processesOf(dir)) == 0 {
			time.Sleep(time.Millisecond) //nolint:forbidigo // no test here, so no deadline to wait on
		}
	case "file":
		if err := os.WriteFile(filepath.Join(dir, "forgotten"), nil, 0o600); err != nil {
			panic(err)
		}
	}
	return finish(dir, 0, 0)
}

func TestEnvironSaysNothingOfTheMachine(t *testing.T) {
	t.Parallel()
	dir := *root.Load()
	for key, want := range map[string]string{"TMPDIR": dir, "HOME": filepath.Join(dir, homeName), "LANG": "C.UTF-8"} {
		if !slices.Contains(os.Environ(), key+"="+want) {
			t.Fatalf("%s is not %q", key, want)
		}
	}
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		allowed := []string{"HOME", "TMPDIR", "LANG", "PATH"}
		if !slices.Contains(allowed, key) && !strings.HasPrefix(key, "GO") && !strings.HasPrefix(key, "GIT_") {
			t.Fatalf("%s came through from the machine", key)
		}
	}
	if !strings.HasPrefix(t.TempDir(), dir+"/") {
		t.Fatalf("t.TempDir is %s, not under the run's %s", t.TempDir(), dir)
	}
}

func TestDirIsInTheRun(t *testing.T) {
	t.Parallel()
	a, b := Dir(t), Dir(t)
	if a == b || filepath.Dir(a) != *root.Load() {
		t.Fatalf("got %s and %s in %s", a, b, *root.Load())
	}
}

// TestWriteExecutableNeverBusy runs scripts just written while other
// goroutines fork all the time: without ForkLock about 1 in 10 is busy.
func TestWriteExecutableNeverBusy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stop := make(chan struct{})
	var forks sync.WaitGroup
	for range 2 {
		forks.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					_ = exec.Command("/bin/sh", "-c", ":").Run()
				}
			}
		})
	}
	defer forks.Wait()
	defer close(stop)
	for i := range 200 {
		path := filepath.Join(dir, fmt.Sprintf("script%d", i))
		if err := WriteExecutable(path, "#!/bin/sh\nexit 0\n"); err != nil {
			t.Fatal(err)
		}
		if err := exec.Command(path).Run(); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
}

func TestCopyExecutable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := WriteExecutable(src, "#!/bin/sh\nexit 7\n"); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "dest")
	if err := CopyExecutable(dest, src); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if err := exec.Command(dest).Run(); !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("got %v, want exit 7", err)
	}
}

// fakeRun makes a run's directory under dir, as a run that died left it.
func fakeRun(t *testing.T, dir, name string) string {
	t.Helper()
	run := filepath.Join(dir, runPrefix+name)
	if err := os.Mkdir(run, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run, lockName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestSweepRemovesOnlyDeadRuns(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dead := fakeRun(t, dir, "dead")
	readOnly := filepath.Join(dead, "read-only")
	if err := os.Mkdir(readOnly, 0o500); err != nil {
		t.Fatal(err)
	}
	alive := fakeRun(t, dir, "alive")
	f, err := os.Open(filepath.Join(alive, lockName))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "wdx123")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}

	sweep(dir)

	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Fatalf("the dead run stays: %v", err)
	}
	for _, kept := range []string{alive, other} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("%s was swept: %v", kept, err)
		}
	}
}

func TestSweepKillsWhatADeadRunLeft(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat("/proc/self/cmdline"); err != nil {
		t.Skip("no /proc to find the processes in")
	}
	dir := t.TempDir()
	run := fakeRun(t, dir, "dead")
	c := exec.Command("sleep", "60")
	c.Dir = run
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() {
		_ = c.Wait()
		close(ended)
	}()
	t.Cleanup(func() { _ = c.Process.Kill() })

	waittest.For(t, "the process to be seen", func() bool {
		return slices.ContainsFunc(processesOf(run), func(p process) bool { return p.pid == c.Process.Pid })
	})
	sweep(dir)

	select {
	case <-ended:
	case <-waittest.After(t):
		t.Fatal("the process the dead run left still runs")
	}
}

func TestProcessesOfFindsOnlyItsOwn(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat("/proc/self/cmdline"); err != nil {
		t.Skip("no /proc to find the processes in")
	}
	dir := t.TempDir()
	mine, sibling := filepath.Join(dir, "run1"), filepath.Join(dir, "run12")
	for _, d := range []string{mine, sibling} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	start := func(setup func(*exec.Cmd)) int {
		c := exec.Command("sleep", "60")
		setup(c)
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = c.Process.Kill()
			_ = c.Wait()
		})
		return c.Process.Pid
	}
	byCwd := start(func(c *exec.Cmd) { c.Dir = mine })
	byEnv := start(func(c *exec.Cmd) { c.Env = []string{"TMPDIR=" + mine} })
	notMine := start(func(c *exec.Cmd) { c.Dir, c.Env = sibling, []string{"TMPDIR=" + sibling} })

	has := func(pid int) bool {
		return slices.ContainsFunc(processesOf(mine), func(p process) bool { return p.pid == pid })
	}
	waittest.For(t, "both processes", func() bool { return has(byCwd) && has(byEnv) })
	if has(notMine) {
		t.Fatalf("process %d of another run was found as this one's", notMine)
	}
}

func TestAliveNotForAZombie(t *testing.T) {
	t.Parallel()
	c := exec.Command("sh", "-c", "exit 0")
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Wait() })
	// Nothing reaps it until the cleanup, so it stays a zombie once ended.
	waittest.For(t, "the process to end", func() bool { return !Alive(c.Process.Pid) })
	if err := syscall.Kill(c.Process.Pid, 0); err != nil {
		t.Skipf("no zombie to ask about, as without /proc: %v", err)
	}
	if !Alive(os.Getpid()) || Alive(0) || Alive(-1) {
		t.Fatal("Alive is wrong about this process or about no process")
	}
}

func TestLoginShellRunsItsCommand(t *testing.T) {
	t.Parallel()
	out, err := exec.Command(LoginShell(t), "-l", "-i", "-c", "echo ran").Output()
	if err != nil || string(out) != "ran\n" {
		t.Fatalf("got %q, %v", out, err)
	}
}

// TestRunFailsWhenItLeaves runs a run that passed but left something behind:
// it fails, says what it left, and leaves nothing for the next run.
func TestRunFailsWhenItLeaves(t *testing.T) {
	t.Parallel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for what, want := range map[string]string{"process": "process", "file": "forgotten"} {
		if what == "process" {
			if _, err := os.Stat("/proc/self/cmdline"); err != nil {
				continue // no /proc to find the process in
			}
		}
		out, err := exec.Command(exe, leakMode, what).CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(out), want) {
			t.Fatalf("a run that left a %s: %v\n%s", what, err, out)
		}
	}
	out, err := exec.Command(exe, leakMode, "nothing").CombinedOutput()
	if err != nil {
		t.Fatalf("a run that left nothing: %v\n%s", err, out)
	}
}

// TestExecutablesAreWrittenSafely finds executables written any other way than
// WriteExecutable, in every test of the repo: running one could fail as busy.
func TestExecutablesAreWrittenSafely(t *testing.T) {
	t.Parallel()
	raw := regexp.MustCompile(`os\.WriteFile\(.*, 0o7[0-9]{2}\)`)
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if m := raw.Find(data); m != nil {
			t.Errorf("%s: use runtest.WriteExecutable: %s", path, m)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
