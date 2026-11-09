// Package server serves the hub's API, api/hub.yaml, on its three endpoints:
// web for browsers, nodes for wdx daemons with mutual TLS, and admin on the
// Unix socket for wdx-hub's own commands. Every request is logged from what
// was received to what was returned, measured, and checked against the spec.
package server

import (
	"cmp"
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/srjn45/wdx/internal/hub/account"
	"github.com/srjn45/wdx/internal/hub/audit"
	"github.com/srjn45/wdx/internal/hub/ca"
	"github.com/srjn45/wdx/internal/hub/docsync"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/hub/hubdb"
	"github.com/srjn45/wdx/internal/hub/node"
	"github.com/srjn45/wdx/internal/hub/pages"
	"github.com/srjn45/wdx/internal/hub/relay"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/telemetry"
)

// DefaultShutdownTimeout is how long requests in flight get to finish once
// the hub is asked to stop, unless Options say otherwise.
const DefaultShutdownTimeout = 30 * time.Second

// DefaultHousekeepingInterval is how often the hub deletes what has expired
// and logs the head of its audit chain, besides at startup, unless Options
// say otherwise.
const DefaultHousekeepingInterval = time.Hour

// Options configure a hub.
type Options struct {
	// Config is the settings the hub starts with.
	Config hubconfig.Config
	Log    *slog.Logger // without a debug_id; each request adds its own
	// LogLevel is the level Log logs at, which a reload sets; nil leaves it.
	LogLevel *slog.LevelVar
	// Reload reads the settings again when asked to. Nil leaves the hub as
	// it is.
	Reload func() (hubconfig.Config, error)
	// Settings lists and changes the settings for the API. Nil has none.
	Settings SettingStore
	// ShutdownTimeout is how long requests in flight get once the hub is
	// asked to stop, before the change among them is cancelled;
	// DefaultShutdownTimeout when 0.
	ShutdownTimeout time.Duration
	// Now is the clock of the certificate authority and the accounts;
	// time.Now when nil.
	Now func() time.Time
	// Argon2 hashes the passwords; OWASP's when zero. Tests lower it.
	Argon2 account.Argon2
	// HousekeepingInterval is how often the hub deletes what has expired and
	// logs the head of its audit chain, besides at startup;
	// DefaultHousekeepingInterval when 0.
	HousekeepingInterval time.Duration
	// HelloTimeout, PingInterval and RenewInterval time the control
	// connections; DefaultHelloTimeout, DefaultPingInterval and
	// DefaultRenewInterval when 0.
	HelloTimeout, PingInterval, RenewInterval time.Duration
	// Streams limits the streams between nodes; the relay's defaults when
	// zero.
	Streams relay.Options
	// Version is the release the hub says it runs: identity.Version when
	// empty. wdx-hub update's tests start a hub as the release they install.
	Version string
}

// version is the release the hub says it runs.
func (s *Server) version() string { return cmp.Or(s.opts.Version, identity.Version) }

// SettingStore is the settings as the commands keep them: the server does not
// read the config file itself.
type SettingStore interface {
	// List returns every setting as the config file has it now.
	List() ([]hubapi.Setting, error)
	// Set saves value for the setting key, and reports whether the file
	// changed. It fails with an *UnknownSettingError or an
	// *InvalidSettingError.
	Set(key, value string) (hubapi.Setting, bool, error)
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

// ReloadError is settings that could not be read again; the hub keeps the
// ones it has.
type ReloadError struct {
	Err error
}

func (e *ReloadError) Error() string { return e.Err.Error() }
func (e *ReloadError) Unwrap() error { return e.Err }

// AlreadyRunningError is returned when another hub holds the lock of the
// home.
type AlreadyRunningError struct {
	PID int // 0 when unknown
}

func (e *AlreadyRunningError) Error() string {
	if e.PID == 0 {
		return "the hub is already running"
	}
	return fmt.Sprintf("the hub is already running (pid %d)", e.PID)
}

// atStart are the settings the hub applies only when it starts; a reload
// applies the others at once.
var atStart = []string{"public_url", "listen.web", "listen.nodes", "nodes_url", "web.tls", "web.tls_cert", "web.tls_key", "master_key", "trusted_proxies"}

// Addresses are where the endpoints listen.
type Addresses struct {
	Web, Nodes string // host:port
	Admin      string // the socket's path
}

// Server is a hub listening on its three endpoints.
type Server struct {
	opts     Options
	log      *slog.Logger // the hub's own lines, under a debug id of their own
	lock     *os.File
	db       *sql.DB
	ca       *ca.CA
	accounts *account.Store
	nodes    *node.Store
	docs     *docsync.Store
	relay    *relay.Relay // the streams between nodes
	pages    *pages.Pages // the sign-in pages, on the web endpoint
	// nodesURL is where nodes reach the node endpoint, as they are told.
	nodesURL string
	// apiVersion is the spec's info.version.
	apiVersion string
	// registration is the setting as the last reload read it, which the
	// pages ask at each request.
	registration atomic.Value
	listeners    map[endpoint]net.Listener
	started      time.Time

	// stopping ends when the hub stops, and with it every control
	// connection, which controls counts; controlsMu orders counting one in
	// with stopping.
	stopping   context.Context
	stopNow    context.CancelFunc
	controlsMu sync.Mutex
	controls   sync.WaitGroup
	// requests counts the requests of the API until their request returned
	// is logged: Shutdown waits for no connection it handed over, whose
	// handler logs its end after controls counted it out.
	requests sync.WaitGroup

	changes sync.Mutex // one admin request changing anything at a time
	// abort is cancelled when the change in flight at shutdown has run out
	// of time.
	abort        context.Context
	abortChanges context.CancelFunc

	// reloading is one reload at a time, and none while the hub closes.
	reloading   sync.Mutex
	metrics     telemetry.Options
	stopMetrics func(context.Context) error
	// stoppingMetrics counts the exporters replaced that are sending their
	// last metrics.
	stoppingMetrics sync.WaitGroup
	// requestDuration is replaced with the exporter while requests record
	// to it.
	requestDuration atomic.Pointer[metric.Float64Histogram]
}

// Listen takes the lock of the home, so that no other hub serves it, opens
// the database, loads the certificate authority, making them at the first
// start, and listens on the three endpoints.
func Listen(ctx context.Context, opts Options) (_ *Server, err error) {
	opts.ShutdownTimeout = cmp.Or(opts.ShutdownTimeout, DefaultShutdownTimeout)
	opts.HousekeepingInterval = cmp.Or(opts.HousekeepingInterval, DefaultHousekeepingInterval)
	opts.HelloTimeout = cmp.Or(opts.HelloTimeout, DefaultHelloTimeout)
	opts.PingInterval = cmp.Or(opts.PingInterval, DefaultPingInterval)
	opts.RenewInterval = cmp.Or(opts.RenewInterval, DefaultRenewInterval)
	if opts.Now == nil {
		opts.Now = time.Now
	}
	cfg := opts.Config
	s := &Server{opts: opts, log: opts.Log.With("debug_id", logging.NewDebugID()), started: time.Now(), listeners: map[endpoint]net.Listener{}, apiVersion: embedded().spec.Info.Version}
	s.abort, s.abortChanges = context.WithCancel(context.Background())
	s.stopping, s.stopNow = context.WithCancel(context.Background())
	defer func() {
		if err != nil {
			s.close()
			s.log.Error("hub could not start", "err", err)
		}
	}()
	ctx = logging.NewContext(ctx, s.log)
	if err := hubconfig.MakeHome(cfg.Home); err != nil {
		return nil, err
	}
	if s.lock, err = lock(hubconfig.LockPath(cfg.Home)); err != nil {
		return nil, err
	}
	if s.db, err = hubdb.Open(ctx, hubconfig.DBPath(cfg.Home)); err != nil {
		return nil, err
	}
	if s.accounts, err = account.New(s.db, account.Options{MasterKey: cfg.MasterKey, Now: opts.Now, Argon2: opts.Argon2}); err != nil {
		return nil, err
	}
	s.nodes = node.New(s.db, node.Options{Now: opts.Now})
	s.relay = relay.New(opts.Streams)
	if s.docs, err = docsync.New(s.db, docsync.Options{MasterKey: cfg.MasterKey, Now: opts.Now}); err != nil {
		return nil, err
	}
	s.registration.Store(cfg.Registration)
	if s.pages, err = pages.New(pages.Options{
		Accounts:       s.accounts,
		Nodes:          s.nodes,
		PublicURL:      cfg.PublicURL,
		TrustedProxies: cfg.TrustedProxies,
		Registration:   func() string { return s.registration.Load().(string) },
		Now:            opts.Now,
	}); err != nil {
		return nil, err
	}
	if s.ca, err = ca.Load(ctx, hubconfig.CADir(cfg.Home), ca.WithClock(opts.Now)); err != nil {
		return nil, err
	}
	if err := s.meter(ctx, metricsOf(cfg)); err != nil {
		return nil, err
	}

	web, err := net.Listen("tcp", cfg.ListenWeb)
	if err != nil {
		return nil, err
	}
	s.listeners[endpointWeb] = web
	if cfg.WebTLS == hubconfig.TLSFiles {
		cert, err := tls.LoadX509KeyPair(cfg.WebTLSCert, cfg.WebTLSKey)
		if err != nil {
			return nil, fmt.Errorf("load web.tls_cert and web.tls_key: %w", err)
		}
		s.listeners[endpointWeb] = tls.NewListener(web, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}})
	}
	nodes, err := net.Listen("tcp", cfg.ListenNodes)
	if err != nil {
		return nil, err
	}
	s.listeners[endpointNodes] = nodes
	// The address nodes are told, and the endpoint's certificate names, is
	// known once it listens, on the port the system chose for port 0.
	s.nodesURL = cfg.NodesEndpoint(nodes.Addr().(*net.TCPAddr).Port)
	nodesHost := hostOf(s.nodesURL)
	nodesCert, err := s.ca.NodeEndpoint(ctx, nodesHost)
	if err != nil {
		return nil, err
	}
	s.listeners[endpointNodes] = tls.NewListener(nodes, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{nodesCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    s.ca.Pool(),
	})
	admin, err := listenSocket(s.log, hubconfig.SocketPath(cfg.Home))
	if err != nil {
		return nil, err
	}
	s.listeners[endpointAdmin] = admin
	a := s.Addresses()
	s.log.Info("hub listening",
		"web", a.Web, "web_tls", cfg.WebTLS, "nodes", a.Nodes, "admin", a.Admin,
		"public_url", cfg.PublicURL, "nodes_url", s.nodesURL, "home", cfg.Home)
	return s, nil
}

// hostOf is the host of an address the settings checked.
func hostOf(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// now is the hub's clock.
func (s *Server) now() time.Time { return s.opts.Now() }

// listenSocket listens on the Unix socket at path, which only the user may
// use: reaching it is the authority to run the admin commands.
func listenSocket(log *slog.Logger, path string) (net.Listener, error) {
	// Only a hub that crashed leaves the socket behind, and the lock says
	// none is running.
	if err := os.Remove(path); err == nil {
		log.Info("stale socket removed", "socket", path)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	// The home is the user's alone, so nobody reaches the socket before
	// this.
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

// Addresses says where the endpoints listen: the port the system chose when
// a setting asked for port 0.
func (s *Server) Addresses() Addresses {
	return Addresses{
		Web:   s.listeners[endpointWeb].Addr().String(),
		Nodes: s.listeners[endpointNodes].Addr().String(),
		Admin: hubconfig.SocketPath(s.opts.Config.Home),
	}
}

// CA is the hub's certificate authority.
func (s *Server) CA() *ca.CA { return s.ca }

// Accounts is the hub's store of accounts.
func (s *Server) Accounts() *account.Store { return s.accounts }

// Nodes is the hub's store of nodes.
func (s *Server) Nodes() *node.Store { return s.nodes }

// Docs is the hub's store of the docs nodes push.
func (s *Server) Docs() *docsync.Store { return s.docs }

// NodesURL is where nodes reach the node endpoint, as they are told at
// login.
func (s *Server) NodesURL() string { return s.nodesURL }

func metricsOf(cfg hubconfig.Config) telemetry.Options {
	return telemetry.Options{Endpoint: cfg.OtelEndpoint, Interval: cfg.OtelInterval, Service: hubconfig.Name}
}

// meter starts sending the metrics to where opts says and creates the
// instrument; an exporter already running ends once it has sent what it has,
// without holding up the request that replaced it.
func (s *Server) meter(ctx context.Context, opts telemetry.Options) error {
	provider, stop, err := telemetry.Start(ctx, opts)
	if err != nil {
		return err
	}
	duration, err := provider.Meter("github.com/srjn45/wdx/internal/hub/server").Float64Histogram("wdx.hub.request.duration",
		metric.WithDescription("how long requests took, by endpoint, operation, status and error code"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60))
	if err != nil {
		return errors.Join(err, stop(ctx))
	}
	s.requestDuration.Store(&duration)
	if old := s.stopMetrics; old != nil {
		s.stoppingMetrics.Go(func() {
			// The last send is bounded by the reader's own timeout.
			if err := old(context.Background()); err != nil {
				s.log.Warn("last metrics of the old exporter not sent", "err", err)
			}
		})
	}
	s.stopMetrics, s.metrics = stop, opts
	return nil
}

// reload reads the settings again and applies log.level and the otel
// settings; it returns the keys of those that changed but wait for the next
// start.
func (s *Server) reload(ctx context.Context) ([]string, error) {
	s.reloading.Lock()
	defer s.reloading.Unlock()
	log := logging.FromContext(ctx)
	if s.opts.Reload == nil {
		log.Info("settings not reloaded", "reason", "this hub reads none")
		return []string{}, nil
	}
	cfg, err := s.opts.Reload()
	if err != nil {
		return nil, &ReloadError{err}
	}
	if s.opts.LogLevel != nil {
		s.opts.LogLevel.Set(cfg.LogLevel)
	}
	s.registration.Store(cfg.Registration)
	if metrics := metricsOf(cfg); metrics != s.metrics {
		// The exporter outlives the request, so it logs as the hub does.
		if err := s.meter(logging.NewContext(context.WithoutCancel(ctx), s.log), metrics); err != nil {
			return nil, err
		}
		log.Info("metrics exporter replaced", "url", metrics.Endpoint, "interval_ms", metrics.Interval.Milliseconds())
	}
	notApplied := []string{}
	for _, key := range atStart {
		if cfg.Value(key) != s.opts.Config.Value(key) {
			notApplied = append(notApplied, key)
		}
	}
	log.Info("settings reloaded", "log_level", cfg.Value("log.level"), "registration", cfg.Registration, "not_applied", notApplied)
	return notApplied, nil
}

// Serve serves requests until ctx is done, then lets the requests in flight
// finish, closes the database and gives up the socket and the lock.
func (s *Server) Serve(ctx context.Context) error {
	servers := map[endpoint]*http.Server{}
	served := make(chan error, len(s.listeners))
	for e, ln := range s.listeners {
		srv := &http.Server{
			Handler:           s.handler(e),
			ReadHeaderTimeout: 10 * time.Second,
			// A refused handshake, as a node without its certificate, is
			// told here; the logger is the hub's, never stderr.
			ErrorLog: slog.NewLogLogger(connectionErrors{s.log.With("endpoint", string(e)).Handler()}, slog.LevelInfo),
		}
		servers[e] = srv
		go func() { served <- srv.Serve(ln) }()
	}
	housekeeping, stopHousekeeping := context.WithCancel(logging.NewContext(context.Background(), s.log))
	housekept := make(chan struct{})
	go func() {
		defer close(housekept)
		s.housekeep(housekeeping)
	}()

	var err error
	select {
	case err = <-served:
		s.log.Error("hub stopped serving", "err", err)
		s.stopControls()
		for _, srv := range servers {
			srv.Close()
		}
		for range len(servers) - 1 {
			if err := <-served; !errors.Is(err, http.ErrServerClosed) {
				s.log.Error("hub stopped serving", "err", err)
			}
		}
	case <-ctx.Done():
		s.log.Info("hub stopping")
		// Each control connection says goodbye: Shutdown waits for none,
		// having handed them over.
		s.stopControls()
		err = s.shutdown(servers)
		for range servers {
			if err := <-served; !errors.Is(err, http.ErrServerClosed) {
				s.log.Error("hub stopped serving", "err", err)
			}
		}
	}
	s.requests.Wait()
	stopHousekeeping()
	<-housekept
	s.close()
	s.log.Info("hub stopped", "uptime_ms", time.Since(s.started).Milliseconds())
	return err
}

// housekeep deletes what has expired and logs the head of the audit chain,
// at once and then every HousekeepingInterval, until ctx is done. The head
// logged lets a chain rewritten whole be caught against a copy of the log the
// hub does not hold.
func (s *Server) housekeep(ctx context.Context) {
	tick := time.NewTicker(s.opts.HousekeepingInterval)
	defer tick.Stop()
	for {
		if _, err := s.accounts.DeleteExpired(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("expired not deleted", "err", err)
		}
		if _, err := s.nodes.DeleteExpired(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("expired logins not deleted", "err", err)
		}
		if head, err := audit.Head(ctx, s.db); err != nil {
			if ctx.Err() == nil {
				s.log.Error("audit chain head not read", "err", err)
			}
		} else {
			s.log.Info("audit chain head", "rows", head.Rows, "head", head.Head)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// shutdown stops the servers, letting the requests in flight finish until
// ShutdownTimeout, and cancels the change still running after it.
func (s *Server) shutdown(servers map[endpoint]*http.Server) error {
	shutdown, cancel := context.WithTimeout(context.Background(), s.opts.ShutdownTimeout)
	defer cancel()
	var wg sync.WaitGroup
	errs := make([]error, 0, len(servers))
	var mu sync.Mutex
	for _, srv := range servers {
		wg.Go(func() {
			if err := srv.Shutdown(shutdown); err != nil {
				srv.Close()
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	err := errors.Join(errs...)
	if err != nil {
		s.log.Error("requests cut short", "err", err)
		s.abortChanges()
		s.changes.Lock() // held for good: the hub takes no more changes
		s.log.Info("change cancelled and ended")
	}
	return err
}

// close releases what Listen took; the listener removes the socket.
func (s *Server) close() {
	s.stopNow()
	for _, ln := range s.listeners {
		ln.Close()
	}
	if s.db != nil {
		s.db.Close()
	}
	s.reloading.Lock()
	defer s.reloading.Unlock()
	if s.stopMetrics != nil {
		// The last send is bounded by the reader's own timeout.
		if err := s.stopMetrics(context.Background()); err != nil {
			s.log.Warn("last metrics not sent", "err", err)
		}
	}
	s.stoppingMetrics.Wait()
	if s.lock != nil {
		// Unlock before closing: a child process forked meanwhile shares the
		// file until it execs, and closing alone would leave the lock held.
		_ = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
		s.lock.Close()
	}
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
