package cli

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/observe"
	"github.com/srjn45/wdx/internal/runtest"
)

// fakeRuntimeScript answers like Docker or Podman for the commands the
// observe package runs, keeping the container, image and volume as files in
// its state folder, and logs every call. A file named fail there makes every
// call fail with its content; one named crash makes the container exit as it
// starts, printing its content.
const fakeRuntimeScript = `#!/bin/sh
S=%[1]q
echo "$*" >> "$S/calls"
if [ -f "$S/fail" ]; then cat "$S/fail" >&2; exit 1; fi
case "$1" in
ps) [ -f "$S/container" ] && echo %[2]s ;;
inspect)
	if [ "$3" = "{{.State.Status}}" ]; then cat "$S/container"
	else printf '%%s\t%%s\t%%s\n' "$(cat "$S/container")" "$(cat "$S/image")" "$(cat "$S/logs")"; fi ;;
image) [ -f "$S/image" ] || exit 1 ;;
pull) echo "pulling $2"; echo "$2" > "$S/image" ;;
run)
	echo "$*" > "$S/run"
	if [ -f "$S/crash" ]; then echo exited > "$S/container"; else echo running > "$S/container"; fi
	touch "$S/volume"
	for a; do case "$a" in %[3]s=*) echo "${a#%[3]s=}" > "$S/logs" ;; esac; done
	echo 4f1c ;;
logs) if [ -f "$S/crash" ]; then cat "$S/crash"; else echo "The OpenTelemetry collector and the Grafana LGTM stack are up and running."; fi ;;
rm) rm "$S/container" ;;
volume) case "$2" in ls) [ -f "$S/volume" ] && echo %[2]s ;; rm) rm "$S/volume" ;; esac ;;
esac
exit 0
`

// fakeRuntime is a container runtime the observe commands run instead of
// Docker or Podman, on free ports.
type fakeRuntime struct {
	name  string // docker or podman
	state string
	ports observe.Ports
}

func newFakeRuntime(t *testing.T, name string) *fakeRuntime {
	t.Helper()
	f := &fakeRuntime{name: name, state: t.TempDir(), ports: fakePorts}
	script := fmt.Sprintf(fakeRuntimeScript, f.state, observe.Container, identity.Name+".logs")
	if err := runtest.WriteExecutable(filepath.Join(f.state, name), script); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fakeRuntime) env(env *Env) {
	env.ObservePorts = f.ports
	// The fake runtime listens on nothing, so whether these are free says
	// nothing, and another test or test run may be using them.
	env.ObservePortCheck = func(int) error { return nil }
	env.LookPath = func(file string) (string, error) {
		if file == f.name {
			return filepath.Join(f.state, f.name), nil
		}
		return "", exec.ErrNotFound
	}
}

func (f *fakeRuntime) read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.state, name))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

func (f *fakeRuntime) write(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.state, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fakePorts are the ports the fake runtime is told to publish: nothing ever
// listens on them.
var fakePorts = observe.Ports{Grafana: 33000, OTLP: 34318}

func TestObserveStartAndStop(t *testing.T) {
	t.Parallel()
	home := shortHome(t)
	f := newFakeRuntime(t, "docker")

	v, exit := wdxJSONWith(t, home, f.env, "observe", "status")
	if exit != api.ExitOK || *v.Running || *v.Runtime != "docker" || v.Stack != nil {
		t.Fatalf("want not running under docker, got %+v", v)
	}

	v, exit = wdxJSONWith(t, home, f.env, "observe", "start")
	want := jsonStack{
		Runtime:      "docker",
		Container:    observe.Container,
		Image:        observe.Image,
		Status:       "running",
		GrafanaURL:   fmt.Sprintf("http://localhost:%d", f.ports.Grafana),
		OTLPEndpoint: fmt.Sprintf("http://localhost:%d", f.ports.OTLP),
		Logs:         identity.LogsDir(home),
	}
	if exit != api.ExitOK || v.Result != "started" || *v.Stack != want || v.DataRemoved != nil {
		t.Fatalf("want started, got %+v", v)
	}
	run := f.read(t, "run")
	for _, arg := range []string{
		fmt.Sprintf("--publish 127.0.0.1:%d:3000", f.ports.Grafana),
		fmt.Sprintf("--publish 127.0.0.1:%d:4318", f.ports.OTLP),
		"--volume " + identity.LogsDir(home) + ":/wdx/logs:ro ",
		"--volume " + observe.Volume + ":/data",
		filepath.Join(home, "observe", "otelcol-wdx.yaml") + ":/wdx/otelcol-wdx.yaml:ro",
	} {
		if !strings.Contains(run, arg) {
			t.Errorf("want %q in the run command:\n%s", arg, run)
		}
	}
	for _, name := range []string{"otelcol-wdx.yaml", "dashboards.yaml", "wdx.json"} {
		if _, err := os.Stat(filepath.Join(home, "observe", name)); err != nil {
			t.Errorf("want %s written: %v", name, err)
		}
	}

	if v, _ = wdxJSONWith(t, home, f.env, "observe", "start"); v.Result != "running" || *v.Stack != want {
		t.Fatalf("want already running, got %+v", v)
	}
	if v, _ = wdxJSONWith(t, home, f.env, "observe", "status"); !*v.Running || *v.Stack != want {
		t.Fatalf("want running, got %+v", v)
	}
	if n := strings.Count(f.read(t, "calls"), "\npull "); n != 1 {
		t.Fatalf("want the image pulled once, got %d times", n)
	}

	if v, exit = wdxJSONWith(t, home, f.env, "observe", "stop"); exit != api.ExitOK || v.Result != "stopped" || v.Stack != nil || v.DataRemoved != nil {
		t.Fatalf("want stopped, got %+v", v)
	}
	if v, _ = wdxJSONWith(t, home, f.env, "observe", "stop"); v.Result != "not_running" {
		t.Fatalf("want not running, got %+v", v)
	}
	if v, _ = wdxJSONWith(t, home, f.env, "observe", "stop", "--purge"); v.Result != "not_running" || !*v.DataRemoved {
		t.Fatalf("want the data removed, got %+v", v)
	}
	if v, _ = wdxJSONWith(t, home, f.env, "observe", "stop", "--purge"); *v.DataRemoved {
		t.Fatalf("want no data left to remove, got %+v", v)
	}
}

// TestObservePodman starts the stack under Podman, which labels the mounts
// for SELinux, replacing a container left stopped.
func TestObservePodman(t *testing.T) {
	t.Parallel()
	home := shortHome(t)
	f := newFakeRuntime(t, "podman")
	f.write(t, "container", "exited")
	f.write(t, "image", observe.Image)
	f.write(t, "logs", identity.LogsDir(home))

	v, exit := wdxJSONWith(t, home, f.env, "observe", "status")
	if exit != api.ExitOK || *v.Running || v.Stack.Status != "exited" {
		t.Fatalf("want a stopped container, got %+v", v)
	}
	v, exit = wdxJSONWith(t, home, f.env, "observe", "start")
	if exit != api.ExitOK || v.Result != "started" || v.Stack.Runtime != "podman" || v.Stack.Status != "running" {
		t.Fatalf("want started under podman, got %+v", v)
	}
	calls := f.read(t, "calls")
	if !strings.Contains(calls, "\nrm --force "+observe.Container+"\n") || strings.Contains(calls, "\npull ") {
		t.Fatalf("want the stopped container replaced and the image kept, got:\n%s", calls)
	}
	if run := f.read(t, "run"); !strings.Contains(run, identity.LogsDir(home)+":/wdx/logs:ro,z ") {
		t.Fatalf("want the mounts labelled, got:\n%s", run)
	}
}

func TestObserveRuntimeMissing(t *testing.T) {
	t.Parallel()
	home := shortHome(t)
	none := func(env *Env) {
		env.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	}
	for _, args := range [][]string{{"observe", "start"}, {"observe", "stop"}} {
		v, exit := wdxJSONWith(t, home, none, args...)
		wantError(t, v, exit, api.ErrorCodeRuntimeMissing, api.ExitSystem)
		wantHint(t, v, "wdx deps install --name docker, or --name podman")
	}
	v, exit := wdxJSONWith(t, home, none, "observe", "status")
	if exit != api.ExitOK || *v.Running || v.Runtime != nil || v.Stack != nil {
		t.Fatalf("want not running without a runtime, got %+v", v)
	}
}

func TestObservePortInUse(t *testing.T) {
	t.Parallel()
	home := shortHome(t)
	f := newFakeRuntime(t, "docker")
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	taken := l.Addr().(*net.TCPAddr).Port
	f.ports = observe.Ports{Grafana: taken, OTLP: taken}

	v, exit := wdxJSONWith(t, home, func(env *Env) {
		f.env(env)
		env.ObservePortCheck = nil // this is what is tested: the real one
	}, "observe", "start")
	wantError(t, v, exit, api.ErrorCodePortInUse, api.ExitRejected)
	if port, _ := v.Details["port"].(float64); int(port) != taken {
		t.Fatalf("want the port in details, got %+v", v.Details)
	}
	if f.read(t, "run") != "" {
		t.Fatal("want nothing run")
	}
}

func TestObserveRuntimeFailed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		runtime, output, hint string
	}{
		{"docker", "permission denied while trying to connect to the docker API", "add your user to the docker group"},
		{"podman", "cannot connect to Podman", "check that podman ps works for your user"},
	} {
		home := shortHome(t)
		f := newFakeRuntime(t, tc.runtime)
		f.write(t, "fail", tc.output+"\n")
		for _, args := range [][]string{{"observe", "start"}, {"observe", "stop"}, {"observe", "status"}} {
			v, exit := wdxJSONWith(t, home, f.env, args...)
			wantError(t, v, exit, api.ErrorCodeRuntimeFailed, api.ExitSystem)
			if !strings.Contains(v.Detail, tc.output) || v.Details["output"] != tc.output {
				t.Fatalf("%s %v: want the runtime's output, got %+v", tc.runtime, args, v)
			}
			wantHint(t, v, tc.hint)
		}
	}
}

func TestObserveStackStopsWhileStarting(t *testing.T) {
	t.Parallel()
	home := shortHome(t)
	f := newFakeRuntime(t, "docker")
	f.write(t, "crash", "otelcol: invalid configuration\n")
	v, exit := wdxJSONWith(t, home, f.env, "observe", "start")
	wantError(t, v, exit, api.ErrorCodeRuntimeFailed, api.ExitSystem)
	if !strings.Contains(v.Detail, "the container stopped while starting") || v.Details["output"] != "otelcol: invalid configuration" {
		t.Fatalf("want the container's last output, got %+v", v)
	}
}

func TestObserveText(t *testing.T) {
	t.Parallel()
	home := shortHome(t)
	f := newFakeRuntime(t, "docker")
	out := wdxWith(t, home, f.env, "observe", "start")
	if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, fmt.Sprintf("observability stack started: open http://localhost:%d\n", f.ports.Grafana)) || !strings.Contains(out.stderr, "pulling "+observe.Image) {
		t.Fatalf("unexpected start: %+v", out)
	}
	if out = wdxWith(t, home, f.env, "observe", "status"); !strings.HasPrefix(out.stdout, "observability stack running") || !strings.Contains(out.stdout, "  logs:    "+identity.LogsDir(home)) {
		t.Fatalf("unexpected status: %q", out.stdout)
	}
	if out = wdxWith(t, home, f.env, "observe", "stop"); !strings.HasPrefix(out.stdout, "observability stack stopped\n") {
		t.Fatalf("unexpected stop: %q", out.stdout)
	}
}
