// Package daemon serves the API over a Unix socket: one daemon per home holds
// the database and does the work the CLI asks for.
package daemon

import (
	"cmp"
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
	"sync/atomic"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/agent"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/doc"
	"github.com/srjn45/wdx/internal/fswatch"
	"github.com/srjn45/wdx/internal/holder"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/plan"
	"github.com/srjn45/wdx/internal/project"
	"github.com/srjn45/wdx/internal/screen"
	"github.com/srjn45/wdx/internal/telemetry"
	"github.com/srjn45/wdx/internal/terminal"
	"github.com/srjn45/wdx/internal/tidestate"
	"github.com/srjn45/wdx/internal/worktree"
)

// DefaultShutdownTimeout is how long requests in flight get to finish once
// the daemon is asked to stop, unless Options say otherwise; a clone may
// take a while.
const DefaultShutdownTimeout = 30 * time.Second

// DefaultPlanParallel is plan.parallel's default, for a daemon given none.
const DefaultPlanParallel = 3

// Options configure a daemon.
type Options struct {
	Home    string
	Socket  string       // identity.SocketPath(Home) when empty
	Log     *slog.Logger // without a debug_id; each request adds its own
	Metrics telemetry.Options

	// Executable is the wdx binary terminals' holders run; this process's
	// own when empty.
	Executable string
	// ShellEnv holds the variables shells inherit, as KEY=value.
	ShellEnv []string
	// GH is the program that speaks to GitHub; "" for none, as on a machine without
	// it, which keeps a test from reaching the network.
	GH string
	// TerminalShell is the shell terminals run; "" for the login shell.
	TerminalShell string
	// TerminalHistoryLines bounds each terminal's history; 0 keeps none.
	TerminalHistoryLines int64
	// Editor is the editor files are edited with; editor.Default when "".
	Editor string
	// DocsGitPath is the directory of each project's repository its docs are kept
	// written into; "" for none.
	DocsGitPath string
	// PlanParallel is the most tasks each plan runs at once unless its run says;
	// DefaultPlanParallel when 0.
	PlanParallel int
	// EditorCommand is what runs the editor named program on path, in place of
	// its own command, as tests give; nil runs the editor.
	EditorCommand func(program, path string) []string
	// ACAs are the AI coding agents it runs, in the order they are offered;
	// aca.All() when nil.
	ACAs []aca.AiCodingAgent
	// ACAEnabled says by id which ACAs new agents may run; one missing may.
	ACAEnabled map[string]bool
	// LookProgram fails when an ACA's program is not found where agents start;
	// through the shell terminals run when nil, as tests do not have it.
	LookProgram func(ctx context.Context, program string) error
	// ACACheckAge is how long what is found installed is kept for a list;
	// agent.DefaultCheckAge when 0.
	ACACheckAge time.Duration

	// ShutdownTimeout is how long requests in flight get once the daemon
	// is asked to stop, before the changes among them are cancelled;
	// DefaultShutdownTimeout when 0.
	ShutdownTimeout time.Duration
	// StatusInterval is the least time between two reads of what an agent's ACA tells
	// of its status, its context size and its usage; agent.DefaultStatusInterval when 0.
	StatusInterval time.Duration
	// UsageFetchInterval is the least time between two fetches of an agent's account's
	// usage, for an ACA that tells it only through its vendor's API, as Cursor;
	// agent.DefaultUsageFetchInterval when 0.
	UsageFetchInterval time.Duration
	// TrashRetention is how long what is kept of a removed agent stays;
	// agent.DefaultTrashRetention when 0.
	TrashRetention time.Duration
	// StopGrace is how long a shell has to end after it is hung up on;
	// terminal.DefaultStopGrace when 0.
	StopGrace time.Duration
	// StopReport is how long past the grace stopping waits for a holder to
	// report; the terminal package's default when 0.
	StopReport time.Duration
	// HolderLimits are how long holders and the daemon wait for each other;
	// the holder package's defaults when zero.
	HolderLimits holder.Limits

	// Reload reads the settings again when asked to: it applies those of the
	// process, such as the logs', and returns the ones the daemon applies.
	// Nil leaves the daemon as it is.
	Reload func() (Settings, error)
	// SettingStore lists and changes the settings for the API. Nil has none.
	SettingStore SettingStore
}

// SettingStore is the settings as the cli keeps them: the daemon may not read the
// config file itself.
type SettingStore interface {
	// List returns every setting as the config file has it now.
	List() ([]api.Setting, error)
	// Set saves value for the setting key, and reports whether the file changed. It
	// fails with an *UnknownSettingError or an *InvalidSettingError.
	Set(key, value string) (api.Setting, bool, error)
}

// UnknownSettingError is a key no setting has.
type UnknownSettingError struct {
	Key string
}

func (e *UnknownSettingError) Error() string { return "no setting is named " + strconv.Quote(e.Key) }

// InvalidSettingError is a value the setting cannot take, or a setting that
// cannot be set.
type InvalidSettingError struct {
	Key string
	Err error
}

func (e *InvalidSettingError) Error() string { return e.Err.Error() }
func (e *InvalidSettingError) Unwrap() error { return e.Err }

// Settings are the settings the daemon can change while it runs.
type Settings struct {
	Metrics              telemetry.Options
	TerminalShell        string
	TerminalHistoryLines int64
	Editor               string
	DocsGitPath          string
	PlanParallel         int
	ACAEnabled           map[string]bool
}

// ReloadError is settings that could not be read again; the daemon keeps the
// ones it has.
type ReloadError struct {
	Err error
}

func (e *ReloadError) Error() string { return e.Err.Error() }
func (e *ReloadError) Unwrap() error { return e.Err }

// Daemon is a daemon listening on its socket.
type Daemon struct {
	opts     Options
	log      *slog.Logger // the daemon's own lines: start and stop
	lock     *os.File
	listener net.Listener
	db       *sql.DB
	projects *project.Store
	agents   *agent.Manager
	backups  *agent.Backups
	docs     *doc.Store
	plans    *plan.Store
	runner   *plan.Runner
	// stopRunner ends the runner, which steps between the requests.
	stopRunner context.CancelFunc
	// planParallel is plan.parallel, which a reload changes.
	planParallel atomic.Int64
	tabs         *tidestate.Store
	gitCopy      *gitCopy
	terminals    *terminal.Supervisor
	tracker      *worktree.Tracker
	// watcher is the one inotify instance everything the daemon watches goes
	// through: the tracker's git files and tabs, the agents' status files (the
	// wdx doc WATCHERS). It opens at the first path watched.
	watcher  *fswatch.Watcher
	marks    *worktree.Marks
	comments *worktree.Comments
	// stopTracker ends what the tracker does, once no stream follows.
	stopTracker context.CancelFunc
	// lander lands worktrees, each in the background.
	lander *worktree.Lander
	// pullRequests keeps what gh said of the worktrees' PRs.
	pullRequests *worktree.PullRequests
	// trackerNotices is what tells it of the changes.
	trackerNotices sync.WaitGroup
	changes        sync.Mutex    // one request changing state at a time
	notices        *notices      // tells the project streams about changes
	stopping       chan struct{} // closed when the daemon starts stopping
	// abort is cancelled when the changes in flight at shutdown have run
	// out of time.
	abort        context.Context
	abortChanges context.CancelFunc
	started      time.Time

	// reloading is one reload at a time, and none while the daemon closes.
	reloading   sync.Mutex
	stopMetrics func(context.Context) error
	// stoppingMetrics counts the exporters replaced that are sending their
	// last metrics.
	stoppingMetrics sync.WaitGroup
	// requestDuration is replaced with the exporter while requests record
	// to it.
	requestDuration atomic.Pointer[metric.Float64Histogram]
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
	opts.ShutdownTimeout = cmp.Or(opts.ShutdownTimeout, DefaultShutdownTimeout)
	d := &Daemon{opts: opts, log: opts.Log.With("debug_id", logging.NewDebugID()), started: time.Now(), notices: newNotices(), stopping: make(chan struct{})}
	d.abort, d.abortChanges = context.WithCancel(context.Background())
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
	if err := d.meter(logging.NewContext(ctx, d.log), opts.Metrics); err != nil {
		return nil, err
	}
	if d.db, err = db.Open(logging.NewContext(ctx, d.log), identity.DBPath(opts.Home)); err != nil {
		return nil, err
	}
	d.projects = project.NewStore(d.db, identity.ProjectsDir(opts.Home))
	d.docs = doc.NewStore(d.db, identity.DocsDir(opts.Home))
	d.plans = plan.NewStore(d.db, identity.PlansDir(opts.Home))
	d.startRunner()
	d.tabs = tidestate.NewStore(d.db)
	d.gitCopy = newGitCopy(d.projects, d.docs, opts.DocsGitPath)
	if opts.Executable == "" {
		if opts.Executable, err = os.Executable(); err != nil {
			return nil, err
		}
	}
	logged := logging.NewContext(ctx, d.log)
	terminals := terminal.NewStore(d.db)
	// A terminal changes state outside requests too, as when its shell
	// exits.
	terminals.OnChange(d.notices.notify)
	if opts.ACAs == nil {
		opts.ACAs = aca.All()
	}
	look := opts.LookProgram
	if look == nil {
		// Called only once requests are served, when the supervisor exists.
		look = func(ctx context.Context, program string) error { return d.terminals.LookProgram(ctx, program) }
	}
	acas := agent.NewACAs(opts.ACAs, look, opts.ACACheckAge)
	acas.SetEnabled(opts.ACAEnabled)
	agents := agent.NewStore(d.db)
	d.backups = agent.NewBackups(logged, agents, opts.Home, acas, opts.TrashRetention)
	d.backups.OnChange = d.notices.notify
	d.watcher = fswatch.New(logged)
	statusReads := agent.NewStatusReads(logged, agents, opts.Home, acas, d.watcher, opts.StatusInterval, opts.UsageFetchInterval)
	statusReads.OnChange = d.notices.notify
	d.backups.OnSessionChange = statusReads.SessionChanged
	d.terminals = terminal.NewSupervisor(logged, terminals, terminal.Options{
		Executable:    opts.Executable,
		Home:          opts.Home,
		Env:           opts.ShellEnv,
		Shell:         opts.TerminalShell,
		HistoryLines:  opts.TerminalHistoryLines,
		Editor:        opts.Editor,
		EditorCommand: opts.EditorCommand,
		StopGrace:     opts.StopGrace,
		StopReport:    opts.StopReport,
		Limits:        opts.HolderLimits,
		AgentLaunch:   agent.Launches(d.backups),
		AgentEnded: func(id string) {
			d.backups.Request(id)
			d.agentEnded(id)
			d.clearPrompt(id)
		},
		AgentFollowed: statusReads.Watch,
		AgentOf: func(ctx context.Context, id string) (string, error) {
			a, err := agents.GetByTerminal(ctx, id)
			return a.ID, err
		},
		AgentSession: func(ctx context.Context, id string) (string, error) {
			a, err := agents.GetByTerminal(ctx, id)
			return a.SessionID, err
		},
		AgentStatus: func(ctx context.Context, id string, status holder.Status) {
			changed, err := agents.SetStatus(ctx, id, status.Activity, status.Draft, status.Queued, status.Model, status.Prompt)
			if err != nil {
				logging.FromContext(ctx).Error("agent status not recorded", "terminal_id", id, "err", err)
			}
			if changed {
				d.notices.notify()
				if status.Activity == string(screen.ActivityIdle) || status.Activity == string(screen.ActivityWaiting) {
					d.backups.Request(id)
				}
			}
		},
	})
	d.agents = agent.NewManager(agents, d.terminals, d.backups)
	d.startTracker()
	if err := d.terminals.Reconcile(logged); err != nil {
		return nil, err
	}
	if err := d.terminals.Sweep(logged); err != nil {
		return nil, err
	}
	// A terminal that ended while no daemon ran kept its agent's prompt.
	if err := agents.ClearEndedPrompts(logged); err != nil {
		return nil, err
	}
	// A copy is only a backup of what the database holds, so one left
	// behind fails nothing.
	if err := d.docs.CatchUpCopies(logged); err != nil {
		d.log.Error("doc copies not caught up", "err", err)
	}
	if err := d.plans.CatchUpCopies(logged); err != nil {
		d.log.Error("plan copies not caught up", "err", err)
	}
	d.gitCopy.syncAll(logged)
	if err := d.backups.Sweep(logged); err != nil {
		d.log.Error("agent backups not swept", "err", err)
	}
	if err := d.backups.RequestAll(logged); err != nil {
		d.log.Error("agent backups not requested", "err", err)
	}
	if err := d.runner.WakeUnfinished(logged); err != nil {
		return nil, err
	}
	d.log.Info("daemon started",
		"home", opts.Home,
		"socket", opts.Socket,
		"version", identity.Version,
		"commit", identity.Commit)
	return d, nil
}

// meter starts sending the metrics to where opts says and creates the
// instruments; an exporter already running ends once it has sent what it has,
// without holding up the request that replaced it.
func (d *Daemon) meter(ctx context.Context, opts telemetry.Options) error {
	provider, stop, err := telemetry.Start(ctx, opts)
	if err != nil {
		return err
	}
	duration, err := provider.Meter("github.com/srjn45/wdx/internal/daemon").Float64Histogram("wdx.request.duration",
		metric.WithDescription("how long requests took, by operation, status and error code"),
		metric.WithUnit("s"),
		// A clone can take minutes.
		metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120))
	if err != nil {
		return errors.Join(err, stop(ctx))
	}
	d.requestDuration.Store(&duration)
	if old := d.stopMetrics; old != nil {
		d.stoppingMetrics.Go(func() {
			// The last send is bounded by the reader's own timeout.
			if err := old(context.Background()); err != nil {
				d.log.Warn("last metrics of the old exporter not sent", "err", err)
			}
		})
	}
	d.stopMetrics = stop
	return nil
}

// reload reads the settings again and applies them. Terminals running keep
// what they started with.
func (d *Daemon) reload(ctx context.Context) error {
	d.reloading.Lock()
	defer d.reloading.Unlock()
	log := logging.FromContext(ctx)
	if d.opts.Reload == nil {
		log.Info("settings not reloaded", "reason", "this daemon reads none")
		return nil
	}
	s, err := d.opts.Reload()
	if err != nil {
		return &ReloadError{err}
	}
	d.terminals.Reconfigure(s.TerminalShell, s.TerminalHistoryLines, s.Editor)
	d.planParallel.Store(int64(cmp.Or(s.PlanParallel, DefaultPlanParallel)))
	d.agents.ACAs().SetEnabled(s.ACAEnabled)
	if d.gitCopy.setPath(s.DocsGitPath) {
		// Under the changes' lock, so that no doc's change syncs at the same time.
		d.changes.Lock()
		d.gitCopy.syncAll(ctx)
		d.changes.Unlock()
	}
	if s.Metrics != d.opts.Metrics {
		// The exporter outlives the request, so it logs as the daemon does.
		if err := d.meter(logging.NewContext(context.WithoutCancel(ctx), d.log), s.Metrics); err != nil {
			return err
		}
		d.opts.Metrics = s.Metrics
		log.Info("metrics exporter replaced", "url", s.Metrics.Endpoint, "interval_ms", s.Metrics.Interval.Milliseconds())
	}
	log.Info("settings reloaded", "terminal_shell", s.TerminalShell, "terminal_history_lines", s.TerminalHistoryLines, "editor", s.Editor, "docs_git_path", s.DocsGitPath, "plan_parallel", s.PlanParallel, "aca_enabled", s.ACAEnabled)
	return nil
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
	// Streams never end by themselves, and Shutdown waits for them.
	srv.RegisterOnShutdown(func() { close(d.stopping) })
	served := make(chan error, 1)
	go func() { served <- srv.Serve(d.listener) }()

	var err error
	select {
	case err = <-served:
		d.log.Error("daemon stopped serving", "err", err)
	case <-ctx.Done():
		d.log.Info("daemon stopping")
		shutdown, cancel := context.WithTimeout(context.Background(), d.opts.ShutdownTimeout)
		defer cancel()
		if err = srv.Shutdown(shutdown); err != nil {
			d.log.Error("requests cut short", "err", err)
			srv.Close()
			d.cancelChanges()
		}
		if err := <-served; !errors.Is(err, http.ErrServerClosed) {
			d.log.Error("daemon stopped serving", "err", err)
		}
	}
	d.close()
	d.log.Info("daemon stopped", "uptime_ms", time.Since(d.started).Milliseconds())
	return err
}

// cancelChanges cancels the change still running when the daemon stops,
// and waits for it to end before the database and the terminals go:
// stopping terminals, which waits on timers, bounds the wait.
func (d *Daemon) cancelChanges() {
	d.abortChanges()
	ended := make(chan struct{})
	go func() {
		d.changes.Lock() // held for good: the daemon takes no more changes
		close(ended)
	}()
	select {
	case <-ended:
		d.log.Info("change cancelled and ended")
	case <-time.After(d.terminals.StopWait()):
		d.log.Error("change still running after it was cancelled")
	}
}

// close releases what Listen took; the listener removes the socket.
func (d *Daemon) close() {
	if d.listener != nil {
		d.listener.Close()
	}
	if d.runner != nil {
		d.stopRunner()
		d.runner.Wait() // it uses the agents, the tracker and the database
	}
	if d.lander != nil {
		d.lander.Close() // it uses the terminals, the tracker and the pull requests
	}
	if d.pullRequests != nil {
		d.pullRequests.Close()
	}
	if d.tracker != nil {
		d.stopTracker()
		d.trackerNotices.Wait() // both read the database, which goes below
		d.tracker.Wait()
	}
	if d.watcher != nil {
		d.watcher.Close() // once nothing watches through it
	}
	if d.terminals != nil {
		// The holders and their shells run on; the next daemon adopts them.
		d.terminals.Close()
	}
	if d.backups != nil {
		d.backups.Close()
	}
	if d.db != nil {
		d.db.Close()
	}
	d.reloading.Lock()
	defer d.reloading.Unlock()
	if d.stopMetrics != nil {
		// The last send is bounded by the reader's own timeout.
		if err := d.stopMetrics(context.Background()); err != nil {
			d.log.Warn("last metrics not sent", "err", err)
		}
	}
	d.stoppingMetrics.Wait()
	if d.lock != nil {
		// Unlock before closing: a child process forked meanwhile shares the
		// file until it execs, and closing alone would leave the lock held.
		_ = syscall.Flock(int(d.lock.Fd()), syscall.LOCK_UN)
		d.lock.Close()
	}
}
