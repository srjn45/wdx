// Package observe runs Grafana with wdx's logs and the daemon's metrics on
// this machine: the all-in-one grafana/otel-lgtm image in a container, under
// Docker or Podman.
package observe

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

// Image is the stack. The registry is spelled out: Podman may ask which
// one a short name means.
const Image = "docker.io/grafana/otel-lgtm:0.36.0"

var (
	// Container runs the stack; Volume holds its data across starts.
	Container = identity.Name + "-observability"
	Volume    = Container
	// logsLabel names the logs folder a container reads, which status shows.
	logsLabel = identity.Name + ".logs"
)

// readyLine is what the image prints once every service in it answers.
const readyLine = "LGTM stack are up and running"

const (
	readyTimeout  = 3 * time.Minute
	readyInterval = time.Second
)

//go:embed files
var files embed.FS

// Ports are where the stack listens on 127.0.0.1.
type Ports struct {
	Grafana int
	OTLP    int // where the daemon sends its metrics
}

// DefaultPorts are the image's own.
var DefaultPorts = Ports{Grafana: 3000, OTLP: 4318}

// RuntimeMissingError is returned when neither Docker nor Podman is
// installed.
type RuntimeMissingError struct{}

func (e *RuntimeMissingError) Error() string { return "neither docker nor podman is installed" }

// RuntimeError is a container runtime command that failed, with what the
// runtime printed.
type RuntimeError struct {
	Runtime string
	Args    []string
	Output  string
	Err     error
}

func (e *RuntimeError) Error() string {
	msg := fmt.Sprintf("%s %s: %v", e.Runtime, strings.Join(e.Args, " "), e.Err)
	if e.Output != "" {
		msg += ": " + e.Output
	}
	return msg
}

func (e *RuntimeError) Unwrap() error { return e.Err }

// PortInUseError is returned when something else listens on a port the stack
// needs.
type PortInUseError struct {
	Port int
}

func (e *PortInUseError) Error() string {
	return fmt.Sprintf("port %d on 127.0.0.1 is already in use", e.Port)
}

// Stack is the observability stack of a home: the container reads its logs.
type Stack struct {
	Runtime string // docker or podman
	path    string
	home    string
	ports   Ports
}

// Find returns the stack run by Docker, else Podman, as lookPath finds them.
func Find(ctx context.Context, lookPath func(string) (string, error), home string, ports Ports) (*Stack, error) {
	for _, name := range []string{"docker", "podman"} {
		if path, err := lookPath(name); err == nil {
			logging.FromContext(ctx).Info("container runtime found", "runtime", name, "path", path)
			return &Stack{Runtime: name, path: path, home: home, ports: ports}, nil
		}
	}
	return nil, &RuntimeMissingError{}
}

// GrafanaURL is where the stack shows the logs and metrics.
func (s *Stack) GrafanaURL() string { return fmt.Sprintf("http://localhost:%d", s.ports.Grafana) }

// OTLPEndpoint is where the stack takes the daemon's metrics.
func (s *Stack) OTLPEndpoint() string { return fmt.Sprintf("http://localhost:%d", s.ports.OTLP) }

// State is the stack's container as the runtime reports it.
type State struct {
	Status string // running, exited, created, ...
	Image  string
	Logs   string // the logs folder it reads
}

// State returns the container, or nil when there is none.
func (s *Stack) State(ctx context.Context) (*State, error) {
	names, err := s.run(ctx, "ps", "--all", "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	if !containsLine(names, Container) {
		return nil, nil
	}
	out, err := s.run(ctx, "inspect", "--format", `{{.State.Status}}	{{.Config.Image}}	{{index .Config.Labels "`+logsLabel+`"}}`, Container)
	if err != nil {
		return nil, err
	}
	fields := strings.Split(out, "\t")
	if len(fields) != 3 {
		return nil, &RuntimeError{Runtime: s.Runtime, Args: []string{"inspect", Container}, Output: out, Err: errors.New("unexpected output")}
	}
	return &State{Status: fields[0], Image: fields[1], Logs: fields[2]}, nil
}

// Start runs the stack and waits until it is ready, pulling the image first
// when the runtime does not have it, with the pull's output going to
// progress. A container left stopped is replaced.
func (s *Stack) Start(ctx context.Context, progress io.Writer) error {
	log := logging.FromContext(ctx)
	if _, err := s.Remove(ctx); err != nil {
		return err
	}
	for _, port := range []int{s.ports.Grafana, s.ports.OTLP} {
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			log.Info("port in use", "port", port, "err", err)
			return &PortInUseError{Port: port}
		}
		l.Close()
	}
	dir, err := s.writeFiles(ctx)
	if err != nil {
		return err
	}
	// The runtime would create a missing folder owned by root, and wdx could
	// no longer write its logs.
	logs := identity.LogsDir(s.home)
	if err := os.MkdirAll(logs, 0o700); err != nil {
		return err
	}
	if err := s.pull(ctx, progress); err != nil {
		return err
	}
	if _, err := s.run(ctx, s.runArgs(dir, logs)...); err != nil {
		return err
	}
	log.Info("stack started", "container", Container, "image", Image)
	return s.waitReady(ctx)
}

// writeFiles writes the files the container mounts into <home>/observe and
// returns the folder.
func (s *Stack) writeFiles(ctx context.Context) (string, error) {
	dir := filepath.Join(s.home, "observe")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	entries, err := files.ReadDir("files")
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		b, err := files.ReadFile("files/" + e.Name())
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o600); err != nil {
			return "", err
		}
	}
	logging.FromContext(ctx).Info("stack files written", "dir", dir)
	return dir, nil
}

func (s *Stack) pull(ctx context.Context, progress io.Writer) error {
	if _, err := s.run(ctx, "image", "inspect", "--format", "{{.Id}}", Image); err == nil {
		return nil
	}
	log := logging.FromContext(ctx)
	log.Info("pulling image", "image", Image)
	start := time.Now()
	cmd := exec.CommandContext(ctx, s.path, "pull", Image)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.MultiWriter(progress, &output), io.MultiWriter(progress, &output)
	if err := cmd.Run(); err != nil {
		e := &RuntimeError{Runtime: s.Runtime, Args: []string{"pull", Image}, Output: lastLines(output.String(), 5), Err: err}
		log.Error("pull failed", "err", err.Error(), "output", e.Output, logging.Duration(start))
		return e
	}
	log.Info("image pulled", "image", Image, logging.Duration(start))
	return nil
}

func (s *Stack) runArgs(dir, logs string) []string {
	// Podman labels mounts for SELinux, as on Fedora; Docker needs no label
	// there unless SELinux is enforced for it.
	ro := "ro"
	if s.Runtime == "podman" {
		ro = "ro,z"
	}
	grafana := "/otel-lgtm/grafana/conf/provisioning/dashboards"
	mount := func(from, to string) string { return from + ":" + to + ":" + ro }
	publish := func(port, inside int) string { return fmt.Sprintf("127.0.0.1:%d:%d", port, inside) }
	return []string{
		"run", "--detach", "--name", Container,
		"--label", logsLabel + "=" + logs,
		// Bound to this machine only: Grafana in the image lets anyone in as
		// admin.
		"--publish", publish(s.ports.Grafana, DefaultPorts.Grafana),
		"--publish", publish(s.ports.OTLP, DefaultPorts.OTLP),
		"--env", "OTELCOL_EXTRA_ARGS=--config=file:/wdx/otelcol-wdx.yaml",
		"--env", "GF_DASHBOARDS_DEFAULT_HOME_DASHBOARD_PATH=" + grafana + "/custom/wdx.json",
		"--volume", mount(filepath.Join(dir, "otelcol-wdx.yaml"), "/wdx/otelcol-wdx.yaml"),
		"--volume", mount(logs, "/wdx/logs"),
		"--volume", mount(filepath.Join(dir, "dashboards.yaml"), grafana+"/custom.yaml"),
		"--volume", mount(filepath.Join(dir, "wdx.json"), grafana+"/custom/wdx.json"),
		"--volume", Volume + ":/data",
		Image,
	}
}

// waitReady waits until the image says every service answers, failing when
// the container stops or readyTimeout passes.
func (s *Stack) waitReady(ctx context.Context) error {
	log := logging.FromContext(ctx)
	log.Info("waiting for the stack", "timeout_ms", readyTimeout.Milliseconds())
	start := time.Now()
	for {
		// Polls log at debug: a first start can take minutes.
		out, err := s.runAt(ctx, slog.LevelDebug, "logs", Container)
		if err != nil {
			return err
		}
		if strings.Contains(out, readyLine) {
			log.Info("stack ready", logging.Duration(start))
			return nil
		}
		if status, err := s.runAt(ctx, slog.LevelDebug, "inspect", "--format", "{{.State.Status}}", Container); err != nil || status != "running" {
			log.Error("stack stopped while starting", "output", lastLines(out, 20))
			return &RuntimeError{Runtime: s.Runtime, Args: []string{"run", Image}, Output: lastLines(out, 5), Err: errors.New("the container stopped while starting")}
		}
		if time.Since(start) > readyTimeout {
			log.Error("stack not ready", "output", lastLines(out, 20), logging.Duration(start))
			return &RuntimeError{Runtime: s.Runtime, Args: []string{"run", Image}, Output: lastLines(out, 5), Err: fmt.Errorf("not ready within %s", readyTimeout)}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(readyInterval):
		}
	}
}

// Remove removes the container and reports whether there was one; the data
// stays in the volume.
func (s *Stack) Remove(ctx context.Context) (bool, error) {
	state, err := s.State(ctx)
	if err != nil || state == nil {
		return false, err
	}
	if _, err := s.run(ctx, "rm", "--force", Container); err != nil {
		return false, err
	}
	logging.FromContext(ctx).Info("stack removed", "container", Container, "status", state.Status)
	return true, nil
}

// RemoveData removes the volume holding the stack's data and reports whether
// there was one.
func (s *Stack) RemoveData(ctx context.Context) (bool, error) {
	names, err := s.run(ctx, "volume", "ls", "--format", "{{.Name}}")
	if err != nil || !containsLine(names, Volume) {
		return false, err
	}
	if _, err := s.run(ctx, "volume", "rm", Volume); err != nil {
		return false, err
	}
	logging.FromContext(ctx).Info("stack data removed", "volume", Volume)
	return true, nil
}

// run runs the runtime and returns its trimmed output.
func (s *Stack) run(ctx context.Context, args ...string) (string, error) {
	return s.runAt(ctx, slog.LevelInfo, args...)
}

// runAt is run, logging that the runtime ran at level.
func (s *Stack) runAt(ctx context.Context, level slog.Level, args ...string) (string, error) {
	log := logging.FromContext(ctx).With("runtime", s.Runtime, "args", args)
	start := time.Now()
	out, err := exec.CommandContext(ctx, s.path, args...).CombinedOutput()
	output := strings.TrimSpace(string(out))
	if err != nil {
		log.Log(ctx, level, "container runtime ran", "ok", false, "output", lastLines(output, 20), logging.Duration(start))
		return "", &RuntimeError{Runtime: s.Runtime, Args: args, Output: lastLines(output, 5), Err: err}
	}
	log.Log(ctx, level, "container runtime ran", "ok", true, logging.Duration(start))
	log.Debug("container runtime output", "output", lastLines(output, 20))
	return output, nil
}

func containsLine(out, line string) bool {
	for l := range strings.Lines(out) {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}
