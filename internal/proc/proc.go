// Package proc finds the user's processes by an entry of their environment, and
// stops them: an agent's leftovers carry its WDX_AGENT_ID (the wdx doc
// LEFTOVER_PROCESSES).
package proc

import (
	"bytes"
	"context"
	"slices"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/srjn45/wdx/internal/logging"
)

// Process is a process found, with the base name of its executable, for the log.
type Process struct {
	PID  int
	Name string
}

// pollEvery is how often Stop looks whether what it signalled has ended.
const pollEvery = 50 * time.Millisecond

// WithEnv returns the user's processes whose environment holds entry, KEY=value,
// exactly, but for those in spare. One that cannot be read, as one of another user
// or one that ended meanwhile, is left out, and so is a zombie, whose environment
// is gone.
func WithEnv(entry string, spare []int) ([]Process, error) {
	pids, err := userPIDs()
	if err != nil {
		return nil, err
	}
	var found []Process
	for _, pid := range pids {
		if slices.Contains(spare, pid) {
			continue
		}
		env, err := environ(pid)
		if err != nil || !hasEntry(env, entry) {
			continue
		}
		found = append(found, Process{PID: pid, Name: name(pid)})
	}
	return found, nil
}

// hasEntry is true when env, entries each ended by a NUL, holds entry.
func hasEntry(env []byte, entry string) bool {
	for e := range bytes.SplitSeq(env, []byte{0}) {
		if string(e) == entry {
			return true
		}
	}
	return false
}

// Stop sends SIGTERM to the processes WithEnv finds, waits up to grace for them to
// end, then sends SIGKILL to those it finds then, what forked meanwhile included,
// and waits up to grace again for those to end. Each signal goes to a process, never
// its group, which may hold others. It returns how many processes it signalled.
func Stop(ctx context.Context, entry string, grace time.Duration, spare []int) (int, error) {
	found, err := WithEnv(entry, spare)
	if err != nil || len(found) == 0 {
		return 0, err
	}
	log := logging.FromContext(ctx)
	log.Info("leftover processes found", "count", len(found))
	signalled := map[int]bool{}
	send(ctx, found, syscall.SIGTERM, signalled)
	left, err := waitGone(ctx, entry, grace, spare)
	if err != nil || len(left) == 0 {
		return len(signalled), err
	}
	send(ctx, left, syscall.SIGKILL, signalled)
	left, err = waitGone(ctx, entry, grace, spare)
	if err == nil && len(left) > 0 {
		log.Error("leftover processes still run after SIGKILL", "count", len(left))
	}
	return len(signalled), err
}

// waitGone waits up to grace for no process to carry entry, and returns those that
// still do.
func waitGone(ctx context.Context, entry string, grace time.Duration, spare []int) ([]Process, error) {
	deadline := time.Now().Add(grace)
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		}
		left, err := WithEnv(entry, spare)
		if err != nil || len(left) == 0 || time.Now().After(deadline) {
			return left, err
		}
	}
}

func send(ctx context.Context, ps []Process, sig syscall.Signal, signalled map[int]bool) {
	log := logging.FromContext(ctx)
	for _, p := range ps {
		if err := syscall.Kill(p.PID, sig); err != nil {
			log.Info("leftover process not signalled", "leftover_pid", p.PID, "name", p.Name, "signal", unix.SignalName(sig), "err", err)
			continue
		}
		signalled[p.PID] = true
		log.Info("leftover process stopped", "leftover_pid", p.PID, "name", p.Name, "signal", unix.SignalName(sig))
	}
}
