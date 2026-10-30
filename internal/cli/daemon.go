package cli

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/daemon"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/style"
	"github.com/srjn45/wdx/internal/telemetry"
)

const (
	// startTimeout is how long a started daemon gets to answer.
	startTimeout = 10 * time.Second
	// stopTimeout is how long a daemon gets to stop: longer than it gives
	// the requests in flight (30 s), then a change it cancels (10 s).
	stopTimeout = 50 * time.Second
	// pollInterval is how often starting and stopping check on the daemon.
	pollInterval = 50 * time.Millisecond
)

func newDaemonCmd(home string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Start, stop and inspect the daemon",
		Long: "The daemon serves the project commands on " + identity.SocketPath(home) + ".\n" +
			"It reads the settings when it starts; restart it after changing them or\n" +
			"updating " + identity.Name + ".",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newDaemonStartCmd(), newDaemonStopCmd(), newDaemonRestartCmd(), newDaemonStatusCmd())
	return cmd
}

// daemonResult is what starting or stopping the daemon did.
type daemonResult struct {
	Result string      `json:"result"`
	Daemon *api.Daemon `json:"daemon"` // the daemon started or stopped; null when unknown
}

func (r daemonResult) text(w io.Writer, st style.Style) {
	if r.Daemon == nil {
		fmt.Fprintf(w, "daemon %s\n", st.Warning(strings.ReplaceAll(r.Result, "_", " ")))
		return
	}
	d := r.Daemon
	switch r.Result {
	case resultStopped:
		fmt.Fprintf(w, "daemon %s %s\n", st.Success("stopped"), st.Muted(fmt.Sprintf("(pid %d)", d.Pid)))
	case resultRunning:
		fmt.Fprintf(w, "daemon %s %s on %s\n", st.Warning("already running"), st.Muted(fmt.Sprintf("(pid %d, version %s)", d.Pid, d.Version)), d.Socket)
	default:
		fmt.Fprintf(w, "daemon %s %s on %s\n", st.Success(r.Result), st.Muted(fmt.Sprintf("(pid %d, version %s)", d.Pid, d.Version)), d.Socket)
	}
}

func newDaemonStartCmd() *cobra.Command {
	var foreground bool
	cmd := &cobra.Command{
		Use:   "start [--foreground]",
		Short: "Start the daemon",
		Long: "Waits until the daemon answers. It starts through systemd or launchd when\n" +
			"the installer set it up as a service, else in the background. With\n" +
			"--foreground it runs in this process until interrupted, as a service\n" +
			"manager runs it.",
		Example: examples(
			"Start the daemon", identity.Name+" daemon start",
			"Run it in this terminal until Ctrl-C", identity.Name+" daemon start --foreground",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not started",
			outputNote: "The daemon, already running or started; with --foreground, the daemon once stopped.\n" +
				`With --json: {"result": ..., "daemon": {...}}`,
			resultList: annotation(resultRunning, resultStarted, resultStopped),
			errorList:  codeList(api.ErrorCodeAlreadyRunning, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if foreground {
				return runForeground(cmd)
			}
			d, started, err := startDaemon(cmd)
			if err != nil {
				return err
			}
			result := resultRunning
			if started {
				result = resultStarted
			}
			return printResult(cmd, daemonResult{Result: result, Daemon: &d})
		},
	}
	cmd.Flags().BoolVar(&foreground, "foreground", false, "run in this process until interrupted")
	return cmd
}

// runForeground runs the daemon in this process until SIGINT or SIGTERM.
func runForeground(cmd *cobra.Command) error {
	cfg := invocationOf(cmd).env.Config
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logs := logging.NewDailyFile(identity.LogsDir(cfg.Home), cfg.LogRetentionDays, time.Now)
	defer logs.Close()
	log := logging.New(logs, cfg.LogLevel).With("pid", os.Getpid(), "process", "daemon")

	d, err := daemon.Listen(ctx, daemon.Options{
		Home:                 cfg.Home,
		Log:                  log,
		Metrics:              telemetry.Options{Endpoint: cfg.OtelEndpoint, Interval: cfg.OtelInterval},
		ShellEnv:             invocationOf(cmd).env.ShellEnv,
		TerminalShell:        cfg.TerminalShell,
		TerminalHistoryLines: cfg.TerminalHistoryLines,
	})
	if err != nil {
		return err
	}
	info := d.Info()
	logging.FromContext(ctx).Info("daemon serving", "socket", info.Socket)
	if !jsonMode(cmd) {
		st := stderrStyle(cmd)
		fmt.Fprintf(cmd.ErrOrStderr(), "daemon %s on %s %s; stop it with Ctrl-C or %s\n",
			st.Success("running"), info.Socket, st.Muted(fmt.Sprintf("(pid %d)", info.Pid)), st.Command(identity.Name+" daemon stop"))
	}
	if err := d.Serve(ctx); err != nil {
		return err
	}
	return printResult(cmd, daemonResult{Result: resultStopped, Daemon: &info})
}

// startDaemon starts the daemon in the background unless one is running,
// and returns it once it answers; started tells whether it was started.
func startDaemon(cmd *cobra.Command) (_ api.Daemon, started bool, _ error) {
	ctx := cmd.Context()
	log := logging.FromContext(ctx)
	inv := invocationOf(cmd)
	home := inv.env.Config.Home
	pid, err := daemon.Running(home)
	if err != nil {
		return api.Daemon{}, false, err
	}
	if pid != 0 {
		log.Info("daemon already running", "daemon_pid", pid)
		d, err := waitAnswering(cmd, nil)
		return d, false, err
	}
	if m := inv.env.Service; m != nil {
		if err := m.Run(ctx, m.Start); err != nil {
			return api.Daemon{}, false, err
		}
		d, err := waitAnswering(cmd, nil)
		if err != nil {
			return api.Daemon{}, false, &hinted{err, "see the log in " + identity.LogsDir(home) + " and " + m.Logs}
		}
		return d, true, nil
	}

	// The daemon's stderr goes to a file read only if it exits at once; a
	// pipe would break once this process exits.
	if err := os.MkdirAll(identity.LogsDir(home), 0o700); err != nil {
		return api.Daemon{}, false, err
	}
	stderr, err := os.CreateTemp(identity.LogsDir(home), "daemon-start-*.log")
	if err != nil {
		return api.Daemon{}, false, err
	}
	defer os.Remove(stderr.Name())
	defer stderr.Close()
	c, err := startRetrying(func() *exec.Cmd {
		c := exec.Command(inv.env.Executable, "daemon", "start", "--foreground")
		c.Env = append(slices.Clip(inv.env.Environ), identity.Env("HOME")+"="+home)
		c.Stderr = stderr
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		return c
	})
	if err != nil {
		return api.Daemon{}, false, err
	}
	log.Info("daemon process started", "daemon_pid", c.Process.Pid, "executable", inv.env.Executable)
	exited := make(chan error, 1)
	go func() { exited <- c.Wait() }()

	d, err := waitAnswering(cmd, exited)
	if err != nil {
		// Given up on, it must not start serving behind the failure.
		if c.Process.Kill() == nil {
			status := <-exited
			log.Error("daemon killed: it did not answer in time", "daemon_pid", c.Process.Pid, "status", fmt.Sprint(status))
		}
		if out, _ := os.ReadFile(stderr.Name()); len(out) > 0 {
			log.Error("daemon failed to start", "stderr", string(out))
			err = fmt.Errorf("%w: %s", err, out)
		}
		return api.Daemon{}, false, &hinted{err, "see the log in " + identity.LogsDir(home)}
	}
	return d, true, nil
}

// startRetrying starts the command newCmd makes, again while the binary is
// "text file busy": wdx update starts the daemon on the binary it has just
// written, which a process forked meanwhile holds open for writing until it
// execs (golang.org/issue/22315).
func startRetrying(newCmd func() *exec.Cmd) (*exec.Cmd, error) {
	for attempt := 1; ; attempt++ {
		c := newCmd() // a Cmd cannot be started twice
		err := c.Start()
		if !errors.Is(err, syscall.ETXTBSY) || attempt == 5 {
			return c, err
		}
		time.Sleep(time.Duration(attempt) * 50 * time.Millisecond)
	}
}

// waitAnswering waits until the daemon answers, giving up when it exits
// (when exited is not nil) or after the start timeout.
func waitAnswering(cmd *cobra.Command, exited <-chan error) (api.Daemon, error) {
	timeout := cmp.Or(invocationOf(cmd).env.Timeouts.Start, startTimeout)
	deadline := time.After(timeout)
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		d, err := clientOf(cmd).Daemon(cmd.Context())
		if !unavailable(err) {
			return d, err
		}
		select {
		case err := <-exited:
			return api.Daemon{}, &client.UnavailableError{Socket: identity.SocketPath(invocationOf(cmd).env.Config.Home), Err: fmt.Errorf("the daemon exited: %w", err)}
		case <-deadline:
			return api.Daemon{}, &client.UnavailableError{Socket: identity.SocketPath(invocationOf(cmd).env.Config.Home), Err: fmt.Errorf("no answer within %s", timeout)}
		case <-tick.C:
		}
	}
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon",
		Long: "The requests in flight finish first. It stops through systemd or launchd\n" +
			"when it runs as a service, else by signalling it.",
		Example: examples(
			"Stop the daemon", identity.Name+" daemon stop",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not stopped",
			outputNote:    "The daemon stopped, or that none was running.\n" + `With --json: {"result": ..., "daemon": {...}}`,
			resultList:    annotation(resultNotRunning, resultStopped),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := stopDaemon(cmd)
			if err != nil {
				return err
			}
			if d == nil {
				return printResult(cmd, daemonResult{Result: resultNotRunning})
			}
			return printResult(cmd, daemonResult{Result: resultStopped, Daemon: d})
		},
	}
}

// stopDaemon stops the daemon serving the home and returns it, or nil when
// none was running.
func stopDaemon(cmd *cobra.Command) (*api.Daemon, error) {
	log := logging.FromContext(cmd.Context())
	home := invocationOf(cmd).env.Config.Home
	pid, err := daemon.Running(home)
	if err != nil || pid == 0 {
		return nil, err
	}
	d := api.Daemon{Pid: pid}
	if answered, err := clientOf(cmd).Daemon(cmd.Context()); err == nil {
		d = answered
	}
	if m := invocationOf(cmd).env.Service; m != nil {
		if err := m.Run(cmd.Context(), m.Stop); err != nil {
			return nil, err
		}
	}
	// A daemon started by hand is still running when a service manager runs
	// none.
	if running, err := daemon.Running(home); err != nil || running == 0 {
		log.Info("daemon gone", "daemon_pid", pid)
		return &d, err
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return nil, fmt.Errorf("signal the daemon (pid %d): %w", pid, err)
	}
	log.Info("daemon signalled", "daemon_pid", pid)
	deadline := time.Now().Add(stopTimeout)
	for {
		running, err := daemon.Running(home)
		if err != nil {
			return nil, err
		}
		if running == 0 {
			log.Info("daemon gone", "daemon_pid", pid)
			return &d, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the daemon (pid %d) did not stop within %s", pid, stopTimeout)
		}
		time.Sleep(pollInterval)
	}
}

func newDaemonRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon, or start it",
		Long:  "Stop the daemon if it is running, then start it, as after updating " + identity.Name + " or its settings.",
		Example: examples(
			"Restart the daemon after changing the settings", identity.Name+" daemon restart",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not restarted",
			outputNote:    "The daemon started.\n" + `With --json: {"result": "restarted", "daemon": {...}}`,
			resultList:    resultRestarted,
			errorList:     codeList(api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := stopDaemon(cmd); err != nil {
				return err
			}
			d, _, err := startDaemon(cmd)
			if err != nil {
				return err
			}
			return printResult(cmd, daemonResult{Result: resultRestarted, Daemon: &d})
		},
	}
}

// daemonStatus is whether the daemon is running and, when it answers, what
// it says about itself.
type daemonStatus struct {
	Running bool        `json:"running"`
	Pid     *int        `json:"pid"`
	Daemon  *api.Daemon `json:"daemon"`  // null when it does not answer
	Manager *string     `json:"manager"` // the service manager that runs it, null when none is installed
	socket  string
}

func (s daemonStatus) text(w io.Writer, st style.Style) {
	field := fieldPrinter(w, st, 8)
	switch {
	case !s.Running:
		fmt.Fprintf(w, "daemon %s\n", st.Warning("not running"))
	case s.Daemon == nil:
		fmt.Fprintf(w, "daemon running %s %s on %s\n", st.Muted(fmt.Sprintf("(pid %d)", *s.Pid)), st.Warning("but not answering"), s.socket)
	default:
		d := s.Daemon
		fmt.Fprintf(w, "daemon %s %s\n", st.Success("running"), st.Muted(fmt.Sprintf("(pid %d)", d.Pid)))
		field("version:", d.Version+" "+st.Muted("("+d.Commit+")"))
		field("started:", d.StartedAt.Local().Format(time.DateTime)+" "+st.Muted(fmt.Sprintf("(up %s)", time.Since(d.StartedAt).Round(time.Second))))
		field("home:", d.Home)
		field("socket:", d.Socket)
	}
	if s.Manager != nil {
		field("service:", *s.Manager)
	}
}

func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the daemon is running",
		Example: examples(
			"Show whether the daemon is running", identity.Name+" daemon status",
			"As JSON", identity.Name+" daemon status --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "Whether the daemon is running, its pid, what it says about itself when it answers, and the\n" +
				"service manager that runs it, if it is installed as a service.\n" +
				`With --json: {"running": ..., "pid": ..., "daemon": {...}, "manager": ...}`,
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			home := invocationOf(cmd).env.Config.Home
			pid, err := daemon.Running(home)
			if err != nil {
				return err
			}
			s := daemonStatus{Running: pid != 0, socket: identity.SocketPath(home)}
			if m := invocationOf(cmd).env.Service; m != nil {
				s.Manager = &m.Name
			}
			if pid != 0 {
				s.Pid = &pid
				d, err := clientOf(cmd).Daemon(cmd.Context())
				if err != nil && !unavailable(err) {
					return err
				}
				if err == nil {
					s.Daemon = &d
				}
			}
			return printResult(cmd, s)
		},
	}
}

// daemonProblem returns the problem of a daemon error, if err is one.
func daemonProblem(err error) (api.Problem, bool) {
	e, ok := errors.AsType[*daemon.AlreadyRunningError](err)
	if !ok {
		return api.Problem{}, false
	}
	p := api.NewProblem(api.ErrorCodeAlreadyRunning, err.Error())
	if e.PID != 0 {
		p.Details = &map[string]any{"pid": e.PID}
	}
	return p, true
}
