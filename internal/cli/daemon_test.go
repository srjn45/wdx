package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/daemon"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/service"
)

// TestDaemonLifecycle starts, restarts and stops a daemon running the wdx
// binary in its own process.
func TestDaemonLifecycle(t *testing.T) {
	t.Parallel()
	home := shortHome(t)
	t.Cleanup(func() {
		if pid, _ := daemon.Running(home); pid != 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})

	v, exit := wdxJSON(t, home, "daemon", "status")
	if exit != api.ExitOK || v.Running == nil || *v.Running || v.Pid != nil || v.Daemon != nil {
		t.Fatalf("want not running, got %+v", v)
	}

	v, exit = wdxJSON(t, home, "daemon", "start")
	if exit != api.ExitOK || v.Result != "started" || v.Daemon == nil || v.Daemon.Pid == os.Getpid() || v.Daemon.Home != home || v.Daemon.Socket != identity.SocketPath(home) {
		t.Fatalf("want started in its own process, got %+v", v)
	}
	first := v.Daemon.Pid
	if v, _ = wdxJSON(t, home, "daemon", "start"); v.Result != "running" || v.Daemon.Pid != first {
		t.Fatalf("want the running daemon %d, got %+v", first, v)
	}
	if v, _ = wdxJSON(t, home, "daemon", "status"); !*v.Running || *v.Pid != first || v.Daemon.Pid != first {
		t.Fatalf("want running as %d, got %+v", first, v)
	}
	newProject(t, home, "app")

	v, exit = wdxJSON(t, home, "daemon", "restart")
	if exit != api.ExitOK || v.Result != "restarted" || v.Daemon.Pid == first {
		t.Fatalf("want restarted in a new process, got %+v", v)
	}
	if v, _ = wdxJSON(t, home, "project", "get", "--name", "app"); v.Project == nil {
		t.Fatalf("want the project served after the restart, got %+v", v)
	}
	second := v.Daemon

	v, exit = wdxJSON(t, home, "daemon", "stop")
	if exit != api.ExitOK || v.Result != "stopped" {
		t.Fatalf("want stopped, got %+v", v)
	}
	if pid, err := daemon.Running(home); err != nil || pid != 0 {
		t.Fatalf("want none running, got %d, %v", pid, err)
	}
	if v, _ = wdxJSON(t, home, "daemon", "stop"); v.Result != "not_running" || v.Daemon != nil {
		t.Fatalf("want not running, got %+v (second daemon %+v)", v, second)
	}

	out := wdx(t, home, "daemon", "start")
	if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "daemon started (pid ") {
		t.Fatalf("unexpected text: %+v", out)
	}
	if out = wdx(t, home, "daemon", "status"); !strings.HasPrefix(out.stdout, "daemon running (pid ") || !strings.Contains(out.stdout, "  socket:  "+identity.SocketPath(home)) {
		t.Fatalf("unexpected text: %q", out.stdout)
	}
	if out = wdx(t, home, "daemon", "stop"); !strings.HasPrefix(out.stdout, "daemon stopped (pid ") {
		t.Fatalf("unexpected text: %q", out.stdout)
	}
	if out = wdx(t, home, "daemon", "status"); out.stdout != "daemon not running\n" {
		t.Fatalf("unexpected text: %q", out.stdout)
	}
}

func TestDaemonStartFails(t *testing.T) {
	t.Parallel()
	home := shortHome(t)
	// A config file the daemon refuses makes it exit at once.
	if err := os.WriteFile(identity.ConfigPath(home), []byte("colour: red\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, exit := wdxJSON(t, home, "daemon", "start")
	wantError(t, v, exit, api.ErrorCodeDaemonUnavailable, api.ExitSystem)
	if !strings.Contains(v.Detail, "colour: unknown setting") {
		t.Fatalf("want the daemon's own error in the detail, got %q", v.Detail)
	}
	wantHint(t, v, "see the log in "+identity.LogsDir(home))
}

// TestDaemonForeground runs the daemon in a command until its context ends,
// as a signal would end it.
func TestDaemonForeground(t *testing.T) {
	t.Parallel()
	home := shortHome(t)
	cfg := testConfig(home)
	ctx, stop := context.WithCancel(context.Background())
	var stdout bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Run(ctx, []string{"daemon", "start", "--foreground", "--json"}, Env{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &bytes.Buffer{}, Config: cfg})
	}()

	if !eventually(func() bool {
		v, _ := wdxJSON(t, home, "daemon", "status")
		return v.Daemon != nil
	}) {
		t.Fatal("the daemon did not answer")
	}
	newProject(t, home, "app")

	v, exit := wdxJSON(t, home, "daemon", "start", "--foreground")
	wantError(t, v, exit, api.ErrorCodeAlreadyRunning, api.ExitRejected)
	if pid, _ := v.Details["pid"].(float64); int(pid) != os.Getpid() {
		t.Fatalf("want the running daemon's pid in details, got %+v", v.Details)
	}

	stop()
	if exit := <-done; exit != api.ExitOK {
		t.Fatalf("want exit 0, got %d: %s", exit, stdout.String())
	}
	var r struct {
		Result string     `json:"result"`
		Daemon api.Daemon `json:"daemon"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil || r.Result != "stopped" || r.Daemon.Pid != os.Getpid() || r.Daemon.Home != home {
		t.Fatalf("want stopped, got %s, %v", stdout.String(), err)
	}
}

// TestDaemonAsService starts and stops the daemon through a service manager,
// one that runs wdx itself.
func TestDaemonAsService(t *testing.T) {
	t.Parallel()
	home := shortHome(t)
	t.Cleanup(func() {
		if pid, _ := daemon.Running(home); pid != 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	ran := filepath.Join(t.TempDir(), "ran")
	manage := func(verb string) []string {
		return []string{"sh", "-c", fmt.Sprintf("echo %s >> %s && WDX_HOME=%s %s daemon %s", verb, ran, home, wdxBinary, verb)}
	}
	svc := &service.Manager{Name: "fake", Start: manage("start"), Stop: manage("stop"), Logs: "the fake's log"}
	status := func() map[string]any {
		t.Helper()
		var v map[string]any
		if err := json.Unmarshal([]byte(wdxService(t, home, svc, "daemon", "status", "--json").stdout), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	if v := status(); v["running"] != false || v["manager"] != "fake" {
		t.Fatalf("want stopped under fake, got %v", v)
	}
	if out := wdxService(t, home, svc, "daemon", "start"); out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "daemon started (pid ") {
		t.Fatalf("want started, got %+v", out)
	}
	if v := status(); v["running"] != true || v["daemon"] == nil {
		t.Fatalf("want running, got %v", v)
	}
	if out := wdxService(t, home, svc, "daemon", "restart"); out.exit != api.ExitOK {
		t.Fatalf("want restarted, got %+v", out)
	}
	if out := wdxService(t, home, svc, "daemon", "stop"); out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "daemon stopped (pid ") {
		t.Fatalf("want stopped, got %+v", out)
	}
	if b, err := os.ReadFile(ran); err != nil || string(b) != "start\nstop\nstart\nstop\n" {
		t.Fatalf("want the service manager to do the starting and stopping, got %q, %v", b, err)
	}
}

func TestDaemonServiceFails(t *testing.T) {
	t.Parallel()
	home := shortHome(t)
	svc := &service.Manager{Name: "fake", Start: []string{"sh", "-c", "echo unit wdx.service not found >&2; exit 5"}}
	out := wdxService(t, home, svc, "daemon", "start", "--json")
	var v map[string]any
	if err := json.Unmarshal([]byte(out.stdout), &v); err != nil || out.exit != api.ExitSystem || v["code"] != "internal" || !strings.Contains(v["detail"].(string), "unit wdx.service not found") {
		t.Fatalf("want the service manager's failure, got %+v", out)
	}
}
