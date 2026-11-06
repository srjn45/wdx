package proc_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/proc"
	"github.com/srjn45/wdx/internal/runtest"
)

func TestMain(m *testing.M) {
	os.Exit(runtest.Main(m))
}

// entry is an environment entry no other process on the machine holds, as other
// runs of these tests go on beside this one.
func entry(t *testing.T) string {
	t.Helper()
	return "WDX_PROC_TEST=" + rand.Text()
}

// started runs script under sh with env added to the test's environment, once it
// has said it is ready: by then sh runs with env, which what it execs keeps.
func started(t *testing.T, script string, env ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", script+"; echo ready; exec sleep 600")
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	if line, err := bufio.NewReader(out).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("not ready: %q, %v", line, err)
	}
	return cmd
}

func pids(ps []proc.Process) []int {
	var pids []int
	for _, p := range ps {
		pids = append(pids, p.PID)
	}
	return pids
}

// TestWithEnvFindsTheEntryExactly: a value that only begins the same, or the same
// value under another name, is another agent's.
func TestWithEnvFindsTheEntryExactly(t *testing.T) {
	t.Parallel()
	e := entry(t)
	want := started(t, "true", e)
	started(t, "true", e+"x")
	started(t, "true", "WDX_OTHER"+e[len("WDX_PROC_TEST"):])
	found, err := proc.WithEnv(e, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := pids(found); !slices.Equal(got, []int{want.Process.Pid}) {
		t.Fatalf("found %v, want [%d]", got, want.Process.Pid)
	}
	if found[0].Name != "sleep" {
		t.Errorf("name %q, want sleep", found[0].Name)
	}
}

func TestWithEnvLeavesOutWhatIsSpared(t *testing.T) {
	t.Parallel()
	e := entry(t)
	spared := started(t, "true", e)
	found, err := proc.WithEnv(e, []int{spared.Process.Pid})
	if err != nil || len(found) != 0 {
		t.Fatalf("found %v, %v; want none", found, err)
	}
}

// endedBy waits for cmd and checks the signal that ended it.
func endedBy(t *testing.T, cmd *exec.Cmd, want syscall.Signal) {
	t.Helper()
	exit, ok := errors.AsType[*exec.ExitError](cmd.Wait())
	if !ok {
		t.Fatalf("pid %d did not end by a signal", cmd.Process.Pid)
	}
	if ws := exit.Sys().(syscall.WaitStatus); !ws.Signaled() || ws.Signal() != want {
		t.Errorf("pid %d ended by %v, want %v", cmd.Process.Pid, ws, want)
	}
}

// stopped runs Stop on what carries e, and checks it signalled n and left none.
func stopped(t *testing.T, e string, grace time.Duration, n int) {
	t.Helper()
	got, err := proc.Stop(context.Background(), e, grace, nil)
	if err != nil || got != n {
		t.Fatalf("Stop = %d, %v; want %d, nil", got, err, n)
	}
	if left, err := proc.WithEnv(e, nil); err != nil || len(left) != 0 {
		t.Fatalf("left %v, %v", left, err)
	}
}

// TestStopTerms: a process that does not ignore SIGTERM ends with it, well within a
// grace it does not wait out.
func TestStopTerms(t *testing.T) {
	t.Parallel()
	e := entry(t)
	cmd := started(t, "true", e)
	stopped(t, e, time.Hour, 1)
	endedBy(t, cmd, syscall.SIGTERM)
}

// TestStopKillsOnceTheGraceIsOver: a process that ignores SIGTERM is killed.
func TestStopKillsOnceTheGraceIsOver(t *testing.T) {
	t.Parallel()
	e := entry(t)
	cmd := started(t, `trap "" TERM`, e)
	stopped(t, e, 100*time.Millisecond, 1)
	endedBy(t, cmd, syscall.SIGKILL)
}

func TestStopFindingNoneSendsNothing(t *testing.T) {
	t.Parallel()
	if n, err := proc.Stop(context.Background(), entry(t), time.Minute, nil); err != nil || n != 0 {
		t.Fatalf("Stop = %d, %v; want 0, nil", n, err)
	}
}
