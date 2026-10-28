// Package update replaces the installed wdx with another release and rolls
// it back. Every switch stops the daemon, keeps the binary and a backup of
// the database it replaces, and starts the daemon on the new binary; when a
// step fails, it puts both back and starts the daemon on the old one.
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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/srjn45/warden/internal/db"
	"github.com/srjn45/warden/internal/identity"
	"github.com/srjn45/warden/internal/logging"
)

// preflightTimeout is how long the new binary gets to say its version.
const preflightTimeout = 30 * time.Second

// Options are what an update works on.
type Options struct {
	Home    string // the home whose daemon and database are switched
	Binary  string // the installed wdx, replaced in place
	Current string // the version installed
	BaseURL string // where the releases are
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

// NoPreviousError is a rollback with no previous version kept.
type NoPreviousError struct{}

func (e *NoPreviousError) Error() string { return "no previous version is kept" }

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
		return Result{From: o.Current, To: o.Current, SchemaFrom: from.Version, SchemaTo: from.Version, UpToDate: true}, nil
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
	o.progress("checking that it runs")
	if err := preflight(ctx, o.Home, binary, target); err != nil {
		return Result{}, err
	}

	replaced, err := o.switchTo(ctx, binary, "", target)
	if err != nil {
		return Result{}, err
	}
	if err := keep(ctx, o, replaced); err != nil {
		os.RemoveAll(replaced)
		return Result{}, err
	}
	to, err := db.ReadSchema(ctx, dbPath)
	if err != nil {
		return Result{}, err
	}
	log.Info("updated", "from", o.Current, "to", target, "schema_from", from.Version, "schema_to", to.Version)
	return Result{From: o.Current, To: target, SchemaFrom: from.Version, SchemaTo: to.Version}, nil
}

// keep makes the replaced binary and database the previous version, in
// place of the one kept before.
func keep(ctx context.Context, o Options, replaced string) error {
	b, err := json.Marshal(previous{Version: o.Current, Schema: db.Known()})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(replaced, "previous.json"), b, 0o600); err != nil {
		return err
	}
	dir := identity.PreviousDir(o.Home)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.Rename(replaced, dir); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("previous version kept", "version", o.Current, "schema", db.Known(), "dir", dir)
	return nil
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

	replaced, err := o.switchTo(ctx, filepath.Join(dir, identity.Name), backup, p.Version)
	if err != nil {
		return Result{}, err
	}
	os.RemoveAll(replaced)
	if err := os.RemoveAll(dir); err != nil {
		return Result{}, err
	}
	to, err := db.ReadSchema(ctx, dbPath)
	if err != nil {
		return Result{}, err
	}
	log.Info("rolled back", "from", o.Current, "to", p.Version, "data_restored", restore)
	return Result{From: o.Current, To: p.Version, SchemaFrom: from.Version, SchemaTo: to.Version, DataRestored: restore}, nil
}

// switchTo stops the daemon, installs binary in place of the installed one
// and the database backup when it is not empty, and starts the daemon on
// them, which must answer as want. It returns a directory under the home
// holding the binary and database it replaced. When a step fails it puts
// them back and starts the daemon on them.
func (o Options) switchTo(ctx context.Context, binary, backup, want string) (string, error) {
	log := logging.FromContext(ctx)
	dbPath := identity.DBPath(o.Home)
	o.progress("stopping the daemon")
	if err := o.Daemon.Stop(ctx); err != nil {
		return "", &FailedError{Step: "stop", Err: err, Version: o.Current}
	}

	replaced, err := os.MkdirTemp(o.Home, "replaced-")
	if err != nil {
		return "", o.undo(ctx, "back up", err, "", false)
	}
	kept := filepath.Join(replaced, identity.Name+".db")
	_, err = os.Stat(dbPath)
	hadDB := err == nil
	o.progress("backing up %s %s and the database", identity.Name, o.Current)
	if err := copyBinary(filepath.Join(replaced, identity.Name), o.Binary); err != nil {
		return "", o.undo(ctx, "back up", err, replaced, false)
	}
	if hadDB {
		if err := db.Backup(ctx, dbPath, kept); err != nil {
			return "", o.undo(ctx, "back up", err, replaced, false)
		}
	}

	o.progress("installing %s %s", identity.Name, want)
	if err := install(binary, o.Binary); err != nil {
		return "", o.undo(ctx, "install", err, replaced, hadDB)
	}
	log.Info("binary installed", "path", o.Binary, "version", want)
	if backup != "" {
		o.progress("restoring the database backup")
		if err := db.Restore(ctx, backup, dbPath); err != nil {
			return "", o.undo(ctx, "install", err, replaced, hadDB)
		}
	}

	o.progress("starting the daemon")
	got, err := o.Daemon.Start(ctx)
	if err == nil && got != want {
		err = fmt.Errorf("the daemon answers as version %s, not %s", got, want)
	}
	if err != nil {
		return "", o.undo(ctx, "start", err, replaced, hadDB)
	}
	return replaced, nil
}

// undo puts back what switchTo replaced, kept in the directory replaced,
// and starts the daemon on it, after step failed with err.
func (o Options) undo(ctx context.Context, step string, err error, replaced string, hadDB bool) error {
	log := logging.FromContext(ctx)
	log.Error("switch failed, undoing it", "step", step, "err", err.Error())
	o.progress("%s failed; putting %s %s back", step, identity.Name, o.Current)
	failed := &FailedError{Step: step, Err: err, Version: o.Current}
	undo := func() error {
		if replaced == "" {
			return nil
		}
		defer os.RemoveAll(replaced)
		if step != "back up" {
			if err := install(filepath.Join(replaced, identity.Name), o.Binary); err != nil {
				return err
			}
			dbPath := identity.DBPath(o.Home)
			if hadDB {
				return db.Restore(ctx, filepath.Join(replaced, identity.Name+".db"), dbPath)
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
		if _, err := o.Daemon.Start(ctx); err != nil {
			failed.UndoErr = fmt.Errorf("start the daemon again: %w", err)
		}
	}
	if failed.UndoErr != nil {
		log.Error("undo failed", "err", failed.UndoErr.Error())
	} else {
		log.Info("switch undone", "version", o.Current)
	}
	return failed
}

func (o Options) progress(format string, args ...any) {
	if o.Progress != nil {
		fmt.Fprintf(o.Progress, "==> "+format+"\n", args...)
	}
}

// preflight runs binary's version command, which must say it is version.
func preflight(ctx context.Context, home, binary, version string) error {
	ctx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, binary, "version", "--json")
	c.Env = append(os.Environ(), identity.Env("HOME")+"="+home)
	out, err := c.Output()
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
	logging.FromContext(ctx).Info("new binary checked", "path", binary, "output", string(out), "ok", err == nil)
	if err != nil {
		return fmt.Errorf("the downloaded %s %s does not run: %w", identity.Name, version, err)
	}
	return nil
}

// install puts a copy of binary at path, renamed into place so a running
// wdx never sees a half-written file.
func install(binary, path string) error {
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := copyBinary(tmp, binary); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func copyBinary(dest, src string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dest, b, 0o755)
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
