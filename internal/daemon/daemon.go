// Package daemon serves the API over a Unix socket: one daemon per home holds
// the database and does the work the CLI asks for.
package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/project"
	"github.com/srjn45/wdx/internal/telemetry"
	"github.com/srjn45/wdx/internal/terminal"
)

// shutdownTimeout is how long requests in flight get to finish once the
// daemon is asked to stop; a clone may take a while.
const shutdownTimeout = 30 * time.Second

// Options configure a daemon.
type Options struct {
	Home    string
	Socket  string       // identity.SocketPath(Home) when empty
	Log     *slog.Logger // without a debug_id; each request adds its own
	Metrics telemetry.Options

	// Executable is the wdx binary terminals' holders run; this process's
	// own when empty.
	Executable string
	// TerminalEnv holds the variables shells inherit, as KEY=value.
	TerminalEnv []string
	// TerminalShell is the shell terminals run; "" for the login shell.
	TerminalShell string
	// TerminalHistoryLines bounds each terminal's history; 0 keeps none.
	TerminalHistoryLines int64
}

// Daemon is a daemon listening on its socket.
type Daemon struct {
	opts      Options
	log       *slog.Logger // the daemon's own lines: start and stop
	lock      *os.File
	listener  net.Listener
	db        *sql.DB
	store     *project.Store
	terminals *terminal.Supervisor
	changes   sync.Mutex // one request changing state at a time
	started   time.Time

	stopMetrics     func(context.Context) error
	requestDuration metric.Float64Histogram
}

// AlreadyRunningError is returned when another daemon holds the lock of the
// home.
type AlreadyRunningError struct {
	PID int // 0 when unknown
}

func (e *AlreadyRunningError) Error() string {
	if e.PID == 0 {
		return "the daemon is already running"
	}
	return fmt.Sprintf("the daemon is already running (pid %d)", e.PID)
}

// Listen takes the lock of the home, so no other daemon serves it, opens the
// database and listens on the socket.
func Listen(ctx context.Context, opts Options) (_ *Daemon, err error) {
	if opts.Socket == "" {
		opts.Socket = identity.SocketPath(opts.Home)
	}
	d := &Daemon{opts: opts, log: opts.Log.With("debug_id", logging.NewDebugID()), started: time.Now()}
	defer func() {
		if err != nil {
			d.close()
			d.log.Error("daemon could not start", "err", err)
		}
	}()
	if err := os.MkdirAll(opts.Home, 0o700); err != nil {
		return nil, err
	}
	if d.lock, err = lock(identity.LockPath(opts.Home)); err != nil {
		return nil, err
	}
	// Only a daemon that crashed leaves the socket behind, and the lock says
	// none is running.
	if err := os.Remove(opts.Socket); err == nil {
		d.log.Info("stale socket removed", "socket", opts.Socket)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if d.listener, err = net.Listen("unix", opts.Socket); err != nil {
		return nil, err
	}
	if err := os.Chmod(opts.Socket, 0o600); err != nil {
		return nil, err
	}
	if err := d.meter(logging.NewContext(ctx, d.log)); err != nil {
		return nil, err
	}
	if d.db, err = db.Open(logging.NewContext(ctx, d.log), identity.DBPath(opts.Home)); err != nil {
		return nil, err
	}
	d.store = project.NewStore(d.db, identity.ProjectsDir(opts.Home))
	if opts.Executable == "" {
		if opts.Executable, err = os.Executable(); err != nil {
			return nil, err
		}
	}
	logged := logging.NewContext(ctx, d.log)
	d.terminals = terminal.NewSupervisor(logged, terminal.NewStore(d.db), terminal.Options{
		Executable:   opts.Executable,
		Home:         opts.Home,
		Env:          opts.TerminalEnv,
		Shell:        opts.TerminalShell,
		HistoryLines: opts.TerminalHistoryLines,
	})
	if err := d.terminals.Reconcile(logged); err != nil {
		return nil, err
	}
	d.log.Info("daemon started",
		"home", opts.Home,
		"socket", opts.Socket,
		"version", identity.Version,
		"commit", identity.Commit)
	return d, nil
}

// meter starts sending the metrics and creates the instruments.
func (d *Daemon) meter(ctx context.Context) error {
	provider, stop, err := telemetry.Start(ctx, d.opts.Metrics)
	if err != nil {
		return err
	}
	d.stopMetrics = stop
	d.requestDuration, err = provider.Meter("github.com/srjn45/wdx/internal/daemon").Float64Histogram("wdx.request.duration",
		metric.WithDescription("how long requests took, by operation, status and error code"),
		metric.WithUnit("s"),
		// A clone can take minutes.
		metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120))
	return err
}

// Info describes the daemon.
func (d *Daemon) Info() api.Daemon {
	return api.Daemon{
		Name:      identity.Name,
		Version:   identity.Version,
		Commit:    identity.Commit,
		Pid:       os.Getpid(),
		Home:      d.opts.Home,
		Socket:    d.opts.Socket,
		StartedAt: d.started.UTC(),
	}
}

// Running returns the pid of the daemon serving home, or 0 when none is.
func Running(home string) (int, error) {
	f, err := os.Open(identity.LockPath(home))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if err == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return 0, nil
	}
	if !errors.Is(err, syscall.EWOULDBLOCK) {
		return 0, err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("daemon lock %s holds no pid: %q", f.Name(), b)
	}
	return pid, nil
}

// lock locks the file at path, failing with an *AlreadyRunningError when
// another process holds it, and writes this process's pid into it.
func lock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		b, _ := os.ReadFile(path)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, &AlreadyRunningError{PID: pid}
		}
		return nil, err
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// Serve serves requests until ctx is done, then lets the requests in flight
// finish, closes the database and gives up the socket and the lock.
func (d *Daemon) Serve(ctx context.Context) error {
	srv := &http.Server{Handler: d.handler(), ReadHeaderTimeout: 10 * time.Second}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(d.listener) }()

	var err error
	select {
	case err = <-served:
		d.log.Error("daemon stopped serving", "err", err)
	case <-ctx.Done():
		d.log.Info("daemon stopping")
		shutdown, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err = srv.Shutdown(shutdown); err != nil {
			d.log.Error("requests cut short", "err", err)
			srv.Close()
		}
		if err := <-served; !errors.Is(err, http.ErrServerClosed) {
			d.log.Error("daemon stopped serving", "err", err)
		}
	}
	d.close()
	d.log.Info("daemon stopped", "uptime_ms", time.Since(d.started).Milliseconds())
	return err
}

// close releases what Listen took; the listener removes the socket.
func (d *Daemon) close() {
	if d.listener != nil {
		d.listener.Close()
	}
	if d.terminals != nil {
		// The holders and their shells run on; the next daemon adopts them.
		d.terminals.Close()
	}
	if d.db != nil {
		d.db.Close()
	}
	if d.stopMetrics != nil {
		// The last send is bounded by the reader's own timeout.
		if err := d.stopMetrics(context.Background()); err != nil {
			d.log.Warn("last metrics not sent", "err", err)
		}
	}
	if d.lock != nil {
		// Unlock before closing: a child process forked meanwhile shares the
		// file until it execs, and closing alone would leave the lock held.
		_ = syscall.Flock(int(d.lock.Fd()), syscall.LOCK_UN)
		d.lock.Close()
	}
}
