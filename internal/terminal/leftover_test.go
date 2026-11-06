package terminal

import (
	"context"
	"crypto/rand"
	"os"
	"os/exec"
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/proc"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/waittest"
)

// leftoverScript starts what outlives the agent's terminal, a sleep in a session
// of its own, and goes on once it runs.
const leftoverScript = `setsid sh -c 'echo $$ > leftover; exec sleep 600' &
while [ ! -s leftover ]; do sleep 0.05; done
`

// leftoverFixture is a fixture whose agent rows run script under an agent id of
// their own, which no other run of the tests on the machine has.
func leftoverFixture(t *testing.T, script string) (fixture, string) {
	t.Helper()
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("no setsid to start a leftover with")
	}
	f := newFixture(t)
	f.opts.Shell = runtest.LoginShell(t)
	agentID := "agent-" + rand.Text()
	f.opts.AgentLaunch = func(context.Context, Terminal) (AgentLaunch, error) {
		return AgentLaunch{AgentID: agentID, Command: []string{"/bin/sh", "-c", script}}, nil
	}
	f.opts.AgentOf = func(context.Context, string) (string, error) { return agentID, nil }
	return f, agentID
}

// leftoverSupervisor is leftoverFixture's Supervisor and project.
func leftoverSupervisor(t *testing.T, script string) (*Supervisor, string, string) {
	t.Helper()
	f, agentID := leftoverFixture(t, script)
	return f.supervisor(t), f.project(t, "app"), agentID
}

func carrying(t *testing.T, agentID string) []int {
	t.Helper()
	found, err := proc.WithEnv(identity.Env("AGENT_ID")+"="+agentID, nil)
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, p := range found {
		pids = append(pids, p.PID)
	}
	return pids
}

// waitCarrying waits until n processes carry the agent's id.
func waitCarrying(t *testing.T, agentID string, n int) []int {
	t.Helper()
	var pids []int
	if !waittest.Until(t, func() bool { pids = carrying(t, agentID); return len(pids) == n }) {
		t.Fatalf("%d processes carry %s, want %d: %v", len(pids), agentID, n, pids)
	}
	return pids
}

// another starts a process carrying another agent's id, which stopping this one's
// must leave.
func another(t *testing.T) string {
	t.Helper()
	agentID := "agent-" + rand.Text()
	cmd := exec.Command("sleep", "600")
	cmd.Env = append(os.Environ(), identity.Env("AGENT_ID")+"="+agentID)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	waitCarrying(t, agentID, 1)
	return agentID
}

// TestStoppingAnAgentStopsItsLeftovers: once StopAgent returns, nothing carries the
// agent's id, a process that left the terminal's groups included, and another
// agent's process runs on.
func TestStoppingAnAgentStopsItsLeftovers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, p, agentID := leftoverSupervisor(t, leftoverScript+"exec sleep 600")
	other := another(t)
	row, err := s.CreateAgent(ctx, p, "helper", t.TempDir(), noLink)
	if err != nil {
		t.Fatal(err)
	}
	waitCarrying(t, agentID, 2) // its shell's sleep and the leftover
	if _, err := s.StopAgent(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	if left := carrying(t, agentID); len(left) != 0 {
		t.Fatalf("left running after the stop: %v", left)
	}
	if got := carrying(t, other); len(got) != 1 {
		t.Fatalf("another agent's process: %v, want it running", got)
	}
}

// TestAnAgentThatEndsStopsItsLeftovers: an ACA that quits by itself leaves its
// leftovers too, stopped before its watch is done.
func TestAnAgentThatEndsStopsItsLeftovers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, p, agentID := leftoverSupervisor(t, leftoverScript+"exit 0")
	row, err := s.CreateAgent(ctx, p, "helper", t.TempDir(), noLink)
	if err != nil {
		t.Fatal(err)
	}
	waitRowState(t, s, row.ID, StateExited)
	if !waittest.Until(t, func() bool { return !s.watching(row.ID) }) {
		t.Fatal("the agent's watch is not done")
	}
	if left := carrying(t, agentID); len(left) != 0 {
		t.Fatalf("left running after the agent ended: %v", left)
	}
}

// TestRestartStopsTheOldLeftoversOnly: the new session carries the same id, and
// starts only once the old one's leftovers are stopped.
func TestRestartStopsTheOldLeftoversOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, p, agentID := leftoverSupervisor(t, "rm -f leftover\n"+leftoverScript+"exec sleep 600")
	row, err := s.CreateAgent(ctx, p, "helper", t.TempDir(), noLink)
	if err != nil {
		t.Fatal(err)
	}
	old := waitCarrying(t, agentID, 2)
	if _, err := s.RestartAgent(ctx, row.ID, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	now := waitCarrying(t, agentID, 2)
	for _, pid := range now {
		if slices.Contains(old, pid) {
			t.Fatalf("process %d of the old session runs on: old %v, now %v", pid, old, now)
		}
	}
}

// TestReconcileStopsTheLeftoversOfALostAgent: an agent whose holder died while no
// daemon ran is lost, and its leftovers are stopped then.
func TestReconcileStopsTheLeftoversOfALostAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, agentID := leftoverFixture(t, leftoverScript+"exec sleep 600")
	first := NewSupervisor(ctx, NewStore(f.db), f.opts)
	row, err := first.CreateAgent(ctx, f.project(t, "app"), "helper", t.TempDir(), noLink)
	if err != nil {
		t.Fatal(err)
	}
	waitCarrying(t, agentID, 2)
	first.Close()
	kill(row.HolderPID, 0)
	waitGone(t, row.HolderPID)
	kill(0, row.PID)
	waitGone(t, row.PID)
	if left := carrying(t, agentID); len(left) != 1 {
		t.Fatalf("want the leftover alone running, got %v", left)
	}
	second := f.supervisor(t)
	if err := second.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	waitRowState(t, second, row.ID, StateLost)
	if left := carrying(t, agentID); len(left) != 0 {
		t.Fatalf("left running after the reconcile: %v", left)
	}
}
