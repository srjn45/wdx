// Package update replaces the installed wdx with another release and rolls
// it back. Every switch stops the daemon, keeps the binary, a backup of the
// database and the config file it replaces, and starts the daemon on the
// new binary; when a step fails, it puts them back and starts the daemon on
// the old one. An update writes the config file anew with the settings of
// the release, as its settings write does.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/srjn45/wdx/internal/atomicfile"
	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

// preflightTimeout is how long the new binary gets to write the settings
// and to say its version.
const preflightTimeout = 30 * time.Second

// Options are what an update works on.
type Options struct {
	Home    string // the home whose daemon and database are switched
	Binary  string // the installed wdx, replaced in place
	Current string // the version installed
	BaseURL string // where the releases are
	// Environ is the environment, as os.Environ gives it, of the new
	// binary when it is checked.
	Environ []string
	Daemon  Daemon
	// Progress gets a line for each step.
	Progress io.Writer
}

// Daemon stops and starts the daemon of the home.
type Daemon struct {
	Stop func(ctx context.Context) error
	// Start starts the daemon and returns its version once it answers.
	Start func(ctx context.Context) (version string, err error)
}

// Status is the installed version and the newest on the channel.
type Status struct {
	Current   string
	Latest    string
	Channel   string
	Available bool // Latest is newer than Current
}

// Result is what an update or rollback did.
type Result struct {
	From, To             string
	SchemaFrom, SchemaTo int
	UpToDate             bool // nothing was newer, so nothing changed
	DataRestored         bool // the rollback restored the database backup
	// Settings is how the update changed the config file; nil for a
	// rollback, which puts back the file the update replaced.
	Settings *Settings
	// Warnings are what failed once the switch succeeded: the version
	// installed runs, but what they say needs a look.
	Warnings []string
}

// Settings is how the new release wrote the config file anew.
type Settings struct {
	Added   []string // the settings it lists that the file did not
	Dropped []string // the keys no setting of it has, commented at the end
}

// SettingsError is a value the config file sets that the release does not
// accept.
type SettingsError struct {
	Version string
	Detail  string          // the release's own message
	Details json.RawMessage // the release's problems with the settings
}

func (e *SettingsError) Error() string {
	return fmt.Sprintf("%s %s does not accept the settings: %s", identity.Name, e.Version, e.Detail)
}

// OlderError is a release asked for that is older than the installed one.
type OlderError struct {
	Current, Requested string
}

func (e *OlderError) Error() string {
	return fmt.Sprintf("%s is older than the installed %s", e.Requested, e.Current)
}

// RunningError is returned while another update or rollback holds the lock.
type RunningError struct{}

func (e *RunningError) Error() string { return "another update or rollback is running" }

// FailedError is a switch that failed once the daemon was stopped.
type FailedError struct {
	Step string // what failed: stop, back up, install, start
	Err  error
	// UndoErr is why putting back the old binary and database failed; nil
	// when the old version runs again.
	UndoErr error
	Version string // the version running again
}

func (e *FailedError) Error() string {
	if e.UndoErr != nil {
		return fmt.Sprintf("%s failed: %v; undoing it failed too: %v", e.Step, e.Err, e.UndoErr)
	}
	return fmt.Sprintf("%s failed: %v; %s runs again", e.Step, e.Err, e.Version)
}

func (e *FailedError) Unwrap() error { return e.Err }

// NoPreviousError is a rollback with no previous version kept for the
// installed one.
type NoPreviousError struct {
	// Kept is the version kept, when another install has since replaced
	// UpdatedTo, the version the update that kept it installed.
	Kept, UpdatedTo string
}

func (e *NoPreviousError) Error() string {
	if e.Kept != "" {
		return fmt.Sprintf("the version kept, %s, is the one the update to %s replaced, which another install has replaced since", e.Kept, e.UpdatedTo)
	}
	return "no previous version is kept"
}

// RestoreRequiredError is a rollback that can only restore the database
// backup: a migration since the update breaks the previous version.
type RestoreRequiredError struct {
	To     string // the previous version
	Schema int    // the database's schema version
	Oldest int    // the oldest schema version that can use it
	Known  int    // the schema version the previous version knows
}

func (e *RestoreRequiredError) Error() string {
	return fmt.Sprintf("%s knows schema version %d, but the database needs %d: rolling back restores the backup taken at the update, losing every change since", e.To, e.Known, e.Oldest)
}

// previous is what PreviousDir/previous.json says of the version kept there.
type previous struct {
	Version string `json:"version"`
	Schema  int    `json:"schema"` // the schema version it knows
	// UpdatedTo is the version the update installed in its place; "" when
	// kept before it was recorded.
	UpdatedTo string `json:"updated_to,omitempty"`
}

// Check returns the installed version and the newest on channel, or
// version when it is not empty.
func Check(ctx context.Context, o Options, channel, version string) (Status, error) {
	target, err := resolve(ctx, o, channel, version)
	if err != nil {
		return Status{}, err
	}
	return Status{Current: o.Current, Latest: target, Channel: channel, Available: newer(target, o.Current)}, nil
}

func resolve(ctx context.Context, o Options, channel, version string) (string, error) {
	r := newReleases(o.BaseURL)
	if version == "" {
		return r.latest(ctx, channel)
	}
	version = strings.TrimPrefix(version, "v")
	return version, r.exists(ctx, version)
}

// Apply installs the newest release on channel, or version when it is not
// empty, unless it is not newer than the installed one. The installed
// binary and database are kept in PreviousDir for a rollback.
func Apply(ctx context.Context, o Options, channel, version string) (Result, error) {
	log := logging.FromContext(ctx)
	unlock, err := lock(o.Home)
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	warnings := sweep(ctx, o)

	target, err := resolve(ctx, o, channel, version)
	if err != nil {
		return Result{}, err
	}
	dbPath := identity.DBPath(o.Home)
	from, err := db.ReadSchema(ctx, dbPath)
	if err != nil {
		return Result{}, err
	}
	if !newer(target, o.Current) {
		if version != "" && target != o.Current {
			return Result{}, &OlderError{Current: o.Current, Requested: target}
		}
		log.Info("already up to date", "version", o.Current, "latest", target)
		return Result{From: o.Current, To: o.Current, SchemaFrom: from.Version, SchemaTo: from.Version, UpToDate: true, Warnings: warnings}, nil
	}

	dir, err := os.MkdirTemp(o.Home, "update-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)
	o.progress("downloading %s %s", identity.Name, target)
	binary, err := newReleases(o.BaseURL).download(ctx, target, dir)
	if err != nil {
		return Result{}, err
	}
	o.progress("checking that it runs on the settings")
	staged, settings, err := preflight(ctx, o, binary, target, dir)
	if err != nil {
		return Result{}, err
	}

	replaced, err := o.switchTo(ctx, binary, "", staged, target)
	if err != nil {
		return Result{}, err
	}
	// The new version runs: what fails from here on is a warning.
	r := Result{From: o.Current, To: target, SchemaFrom: from.Version, Settings: &settings, Warnings: warnings}
	if warning, err := keep(ctx, o, replaced, target); err != nil {
		log.Error("replaced version not kept", "dir", replaced, "err", err)
		r.Warnings = append(r.Warnings, fmt.Sprintf("%s %s is not kept for a rollback: %v; its binary, database and config file are in %s", identity.Name, o.Current, err, replaced))
	} else if warning != "" {
		r.Warnings = append(r.Warnings, warning)
	}
	r.SchemaTo = r.schemaTo(ctx, dbPath)
	log.Info("updated", "from", o.Current, "to", target, "schema_from", from.Version, "schema_to", r.SchemaTo, "settings_added", settings.Added, "settings_dropped", settings.Dropped, "warnings", r.Warnings)
	return r, nil
}

// schemaTo reads the schema version the switch left, or warns that it
// could not.
func (r *Result) schemaTo(ctx context.Context, dbPath string) int {
	to, err := db.ReadSchema(ctx, dbPath)
	if err != nil {
		logging.FromContext(ctx).Error("schema version not read", "err", err)
		r.Warnings = append(r.Warnings, "the database's schema version could not be read: "+err.Error())
		return 0
	}
	return to.Version
}

// keep makes the replaced binary and database the previous version, in
// place of the one kept before, which is set aside until the new one is in
// place: a failure leaves it kept. warning is a cleanup that failed after.
func keep(ctx context.Context, o Options, replaced, updatedTo string) (warning string, err error) {
	log := logging.FromContext(ctx)
	b, err := json.Marshal(previous{Version: o.Current, Schema: db.Known(), UpdatedTo: updatedTo})
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(replaced, "previous.json"), b, 0o600); err != nil {
		return "", err
	}
	dir := identity.PreviousDir(o.Home)
	old := dir + ".old"
	if exists(dir) {
		// An old one beside it was left by a keep cut short: dir is newer.
		if err := os.RemoveAll(old); err != nil {
			return "", err
		}
		if err := os.Rename(dir, old); err != nil {
			return "", err
		}
	}
	if err := os.Rename(replaced, dir); err != nil {
		if exists(old) {
			if err := os.Rename(old, dir); err != nil {
				log.Error("previous version not put back", "dir", old, "err", err)
			}
		}
		return "", err
	}
	log.Info("previous version kept", "version", o.Current, "schema", db.Known(), "dir", dir)
	if err := os.RemoveAll(old); err != nil {
		log.Error("version kept before not deleted", "dir", old, "err", err)
		return fmt.Sprintf("the version kept before is left in %s: %v", old, err), nil
	}
	return "", nil
}

// Rollback installs the previous version again. The database stays when
// the previous version can use it; when a migration since the update breaks
// it, the backup taken at the update is restored if discardChanges, else
// it fails with a *RestoreRequiredError.
func Rollback(ctx context.Context, o Options, discardChanges bool) (Result, error) {
	log := logging.FromContext(ctx)
	unlock, err := lock(o.Home)
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	warnings := sweep(ctx, o)

	dir := identity.PreviousDir(o.Home)
	b, err := os.ReadFile(filepath.Join(dir, "previous.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return Result{}, &NoPreviousError{}
	}
	if err != nil {
		return Result{}, err
	}
	var p previous
	if err := json.Unmarshal(b, &p); err != nil {
		return Result{}, fmt.Errorf("read %s: %w", filepath.Join(dir, "previous.json"), err)
	}
	if p.UpdatedTo != "" && p.UpdatedTo != o.Current {
		log.Info("rollback refused: another install replaced the update", "kept", p.Version, "updated_to", p.UpdatedTo, "installed", o.Current)
		return Result{}, &NoPreviousError{Kept: p.Version, UpdatedTo: p.UpdatedTo}
	}
	dbPath := identity.DBPath(o.Home)
	from, err := db.ReadSchema(ctx, dbPath)
	if err != nil {
		return Result{}, err
	}
	restore := from.Version > p.Schema && from.Oldest > p.Schema
	log.Info("rollback planned", "to", p.Version, "known", p.Schema, "schema", from.Version, "oldest", from.Oldest, "restore", restore)
	if restore && !discardChanges {
		return Result{}, &RestoreRequiredError{To: p.Version, Schema: from.Version, Oldest: from.Oldest, Known: p.Schema}
	}
	backup := ""
	if restore {
		backup = filepath.Join(dir, identity.Name+".db")
	}
	settings := filepath.Join(dir, "config.yaml")
	if _, err := os.Stat(settings); err != nil {
		settings = "" // kept by an update before config files were
	}

	replaced, err := o.switchTo(ctx, filepath.Join(dir, identity.Name), backup, settings, p.Version)
	if err != nil {
		return Result{}, err
	}
	// The previous version runs: what fails from here on is a warning.
	r := Result{From: o.Current, To: p.Version, SchemaFrom: from.Version, DataRestored: restore, Warnings: warnings}
	for _, left := range []string{replaced, dir} {
		if err := os.RemoveAll(left); err != nil {
			log.Error("not deleted after the rollback", "dir", left, "err", err)
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s is left behind: %v", left, err))
		}
	}
	r.SchemaTo = r.schemaTo(ctx, dbPath)
	log.Info("rolled back", "from", o.Current, "to", p.Version, "data_restored", restore, "warnings", r.Warnings)
	return r, nil
}

// switchTo stops the daemon, installs binary in place of the installed one,
// the database backup and the config file settings when they are not
// empty, and starts the daemon on them, which must answer as want. It
// returns a directory under the home holding the binary, database and
// config file it replaced. When a step fails it puts them back and starts
// the daemon on them.
func (o Options) switchTo(ctx context.Context, binary, backup, settings, want string) (string, error) {
	log := logging.FromContext(ctx)
	defer o.holdSignals()()
	dbPath, configPath := identity.DBPath(o.Home), identity.ConfigPath(o.Home)
	o.progress("stopping the daemon")
	if err := o.Daemon.Stop(ctx); err != nil {
		return "", &FailedError{Step: "stop", Err: err, Version: o.Current}
	}

	dir, err := os.MkdirTemp(o.Home, "replaced-")
	if err != nil {
		return "", o.undo(ctx, "back up", err, replaced{})
	}
	r := replaced{dir: dir, db: exists(dbPath), config: exists(configPath), settings: settings != ""}
	o.progress("backing up %s %s, the database and the settings", identity.Name, o.Current)
	if err := atomicfile.Copy(filepath.Join(dir, identity.Name), o.Binary, 0o755); err != nil {
		return "", o.undo(ctx, "back up", err, r)
	}
	if r.db {
		if err := db.Backup(ctx, dbPath, filepath.Join(dir, identity.Name+".db")); err != nil {
			return "", o.undo(ctx, "back up", err, r)
		}
	}
	if r.config {
		if err := atomicfile.Copy(filepath.Join(dir, "config.yaml"), configPath, 0o600); err != nil {
			return "", o.undo(ctx, "back up", err, r)
		}
	}

	o.progress("installing %s %s", identity.Name, want)
	if err := atomicfile.Copy(o.Binary, binary, 0o755); err != nil {
		return "", o.undo(ctx, "install", err, r)
	}
	log.Info("binary installed", "path", o.Binary, "version", want)
	if backup != "" {
		o.progress("restoring the database backup")
		if err := db.Restore(ctx, backup, dbPath); err != nil {
			return "", o.undo(ctx, "install", err, r)
		}
	}
	if settings != "" {
		if err := atomicfile.Copy(configPath, settings, 0o600); err != nil {
			return "", o.undo(ctx, "install", err, r)
		}
		log.Info("settings installed", "path", configPath, "from", settings)
	}

	o.progress("starting the daemon")
	got, err := o.Daemon.Start(ctx)
	if err == nil && got != want {
		err = fmt.Errorf("the daemon answers as version %s, not %s", got, want)
	}
	if err != nil {
		return "", o.undo(ctx, "start", err, r)
	}
	return dir, nil
}

// replaced is the directory a switch keeps what it replaces in, whether
// there was a database and a config file to keep, and whether it installs
// a config file.
type replaced struct {
	dir                  string // "" before it is made
	db, config, settings bool
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// undo puts back what switchTo replaced, kept in r, and starts the daemon
// on it, after step failed with err. A daemon the failed start left
// running is stopped first, and the daemon must then answer as the version
// put back.
func (o Options) undo(ctx context.Context, step string, err error, r replaced) error {
	ctx = context.WithoutCancel(ctx)
	log := logging.FromContext(ctx)
	log.Error("switch failed, undoing it", "step", step, "err", err)
	o.progress("%s failed; putting %s %s back", step, identity.Name, o.Current)
	failed := &FailedError{Step: step, Err: err, Version: o.Current}
	undo := func() error {
		if step == "start" {
			// It may run late, as another version, or restarted by systemd.
			if err := o.Daemon.Stop(ctx); err != nil {
				return fmt.Errorf("stop the daemon the update started: %w", err)
			}
		}
		if r.dir == "" {
			return nil
		}
		defer os.RemoveAll(r.dir)
		if step != "back up" {
			if err := atomicfile.Copy(o.Binary, filepath.Join(r.dir, identity.Name), 0o755); err != nil {
				return err
			}
			if r.config {
				if err := atomicfile.Copy(identity.ConfigPath(o.Home), filepath.Join(r.dir, "config.yaml"), 0o600); err != nil {
					return err
				}
			} else if r.settings {
				if err := os.Remove(identity.ConfigPath(o.Home)); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
			}
			dbPath := identity.DBPath(o.Home)
			if r.db {
				return db.Restore(ctx, filepath.Join(r.dir, identity.Name+".db"), dbPath)
			}
			// The new binary created the database; the old one creates its own.
			for _, suffix := range []string{"", "-wal", "-shm"} {
				if err := os.Remove(dbPath + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
			}
		}
		return nil
	}
	if failed.UndoErr = undo(); failed.UndoErr == nil {
		got, err := o.Daemon.Start(ctx)
		if err == nil && got != o.Current {
			err = fmt.Errorf("the daemon answers as version %s, not %s", got, o.Current)
		}
		if err != nil {
			failed.UndoErr = fmt.Errorf("start the daemon again: %w", err)
		}
	}
	if failed.UndoErr != nil {
		log.Error("undo failed", "err", failed.UndoErr)
	} else {
		log.Info("switch undone", "version", o.Current)
	}
	return failed
}

// holdSignals holds off the signals that end wdx until the function it
// returns is called: a switch cut short would leave the daemon stopped or
// the files half put back. The command ends once the switch has.
func (o Options) holdSignals() (release func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	done, said := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(said)
		var sig os.Signal
		select {
		case sig = <-signals:
		case <-done:
			// One held as the switch ended is said all the same.
			select {
			case sig = <-signals:
			default:
			}
		}
		if sig != nil {
			o.progress("%s: the switch finishes first", sig)
		}
	}()
	return func() {
		signal.Stop(signals)
		close(done)
		<-said
	}
}

// sweep deletes the downloads of updates cut short, and warns of the files
// a switch cut short replaced: they may be the only copy of the version it
// replaced, so they stay. The update lock is held: none is in use.
func sweep(ctx context.Context, o Options) []string {
	log := logging.FromContext(ctx)
	warnings := []string{}
	downloads, _ := filepath.Glob(filepath.Join(o.Home, "update-*"))
	for _, dir := range downloads {
		if err := os.RemoveAll(dir); err != nil {
			log.Error("stale download not deleted", "dir", dir, "err", err)
			continue
		}
		log.Info("stale download deleted", "dir", dir)
	}
	replaced, _ := filepath.Glob(filepath.Join(o.Home, "replaced-*"))
	for _, dir := range replaced {
		log.Warn("files a switch cut short replaced are left", "dir", dir)
		w := fmt.Sprintf("%s holds the binary, database and config file a switch cut short replaced; delete it once you no longer need them", dir)
		warnings = append(warnings, w)
	}
	return warnings
}

func (o Options) progress(format string, args ...any) {
	if o.Progress != nil {
		fmt.Fprintf(o.Progress, "==> "+format+"\n", args...)
	}
}

// preflight checks that binary, release version, runs on the settings of
// home: its settings write writes the config file anew into dir, then its
// version command, run on that file, must say it is version. staged is the
// file written, "" when the home has none or it is already as written.
func preflight(ctx context.Context, o Options, binary, version, dir string) (staged string, s Settings, err error) {
	ctx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()
	log := logging.FromContext(ctx)
	fail := func(err error) (string, Settings, error) {
		return "", Settings{}, fmt.Errorf("the downloaded %s %s does not run: %w", identity.Name, version, err)
	}
	s = Settings{Added: []string{}, Dropped: []string{}}
	checkHome := filepath.Join(dir, "home")
	if err := os.MkdirAll(checkHome, 0o700); err != nil {
		return "", Settings{}, err
	}
	current := identity.ConfigPath(o.Home)
	if exists(current) {
		staged = identity.ConfigPath(checkHome)
		out, err := run(ctx, o.Environ, binary, o.Home, "settings", "write", "--json", "--to", staged)
		log.Info("new binary wrote the settings", "path", staged, "output", string(out), "ok", err == nil)
		if err != nil {
			var p struct {
				Code    string `json:"code"`
				Detail  string `json:"detail"`
				Details struct {
					Settings json.RawMessage `json:"settings"`
				} `json:"details"`
			}
			if json.Unmarshal(out, &p) == nil && p.Code == "usage" && p.Details.Settings != nil {
				return "", Settings{}, &SettingsError{Version: version, Detail: p.Detail, Details: p.Details.Settings}
			}
			return fail(err)
		}
		var w struct {
			Settings Settings `json:"settings"`
		}
		if err := json.Unmarshal(out, &w); err != nil {
			return fail(err)
		}
		s.Added, s.Dropped = append(s.Added, w.Settings.Added...), append(s.Dropped, w.Settings.Dropped...)
		if same, err := sameContent(staged, current); err != nil {
			return fail(err)
		} else if same {
			staged = ""
		}
	}

	out, err := run(ctx, o.Environ, binary, checkHome, "version", "--json")
	var v struct {
		CLI struct {
			Version string `json:"version"`
		} `json:"cli"`
	}
	if err == nil {
		err = json.Unmarshal(out, &v)
	}
	if err == nil && v.CLI.Version != version {
		err = fmt.Errorf("it says it is version %q", v.CLI.Version)
	}
	log.Info("new binary checked", "path", binary, "output", string(out), "ok", err == nil)
	if err != nil {
		return fail(err)
	}
	return staged, s, nil
}

// run runs binary on home, in environ, and returns its stdout.
func run(ctx context.Context, environ []string, binary, home string, args ...string) ([]byte, error) {
	for attempt := 1; ; attempt++ {
		c := exec.CommandContext(ctx, binary, args...)
		c.Env = append(slices.Clip(environ), identity.Env("HOME")+"="+home)
		out, err := c.Output()
		// A process forked while binary was being written holds it open for
		// writing until it execs: golang.org/issue/22315.
		if !errors.Is(err, syscall.ETXTBSY) || attempt == 5 {
			return out, err
		}
		time.Sleep(time.Duration(attempt) * 50 * time.Millisecond)
	}
}

func sameContent(a, b string) (bool, error) {
	x, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	y, err := os.ReadFile(b)
	if err != nil {
		return false, err
	}
	return string(x) == string(y), nil
}

// lock takes the update lock of home, failing with a *RunningError when
// another process holds it.
func lock(home string) (unlock func(), err error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(identity.UpdateLockPath(home), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, &RunningError{}
		}
		return nil, err
	}
	return func() { f.Close() }, nil
}
