package hubcli

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/atomicfile"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/hub/hubdb"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/sqlite"
	"github.com/srjn45/wdx/internal/style"
	"github.com/srjn45/wdx/internal/update"
)

const (
	resultUpToDate   = "up_to_date"
	resultRolledBack = "rolled_back"

	discardChangesQuestion = "discard changes"

	// backupsKept is how many database backups update keeps, one per update:
	// a hub's data is other people's.
	backupsKept = 3
	// defaultStartTimeout is how long the hub started on a release gets to
	// answer on its socket.
	defaultStartTimeout = 30 * time.Second
	// servicePath is the PATH the release runs with: sudo's, whatever the
	// environment of the update.
	servicePath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
)

// UpdateOptions are what tests change of update.
type UpdateOptions struct {
	// Layout is the installed hub's; hubconfig.Installed when nil.
	Layout *hubconfig.Layout
	// BaseURL holds the releases; the update.base_url setting when empty.
	BaseURL string
	// Version is the version of this wdx-hub; identity.Version when empty.
	Version string
	// Systemctl runs systemctl with args; nil runs it.
	Systemctl func(ctx context.Context, args ...string) error
	// Preflight is how long the release gets to run on the settings;
	// update's default when 0.
	Preflight time.Duration
	// StartTimeout is how long the hub started on a release gets to answer;
	// defaultStartTimeout when 0.
	StartTimeout time.Duration
	// LookPath finds a shell, whose completion --apply installs;
	// exec.LookPath when nil.
	LookPath func(file string) (string, error)
}

func (o UpdateOptions) layout() hubconfig.Layout {
	if o.Layout != nil {
		return *o.Layout
	}
	return hubconfig.Installed
}

// rootRequiredError is an update or rollback run by a user other than root.
type rootRequiredError struct{ flag string }

func (e *rootRequiredError) Error() string {
	return "--" + e.flag + " replaces " + name + "'s binary and service, which only root may"
}

// notInstalledError is an update of a hub not installed as install-hub.sh
// installs it.
type notInstalledError struct{ detail string }

func (e *notInstalledError) Error() string { return e.detail }

// untrustedSourceError is an update as root from a release address the
// config file names: the hub's user may have written it there.
type untrustedSourceError struct{ url, from string }

func (e *untrustedSourceError) Error() string {
	return fmt.Sprintf("update.base_url %s is set in %s, which the hub's user can change, and root does not install from it", e.url, e.from)
}

// homeUnsafeError is a hub's home that root may not write in: path is a
// symlink, not a directory, or not the hub's user's.
type homeUnsafeError struct{ path, reason string }

func (e *homeUnsafeError) Error() string {
	return fmt.Sprintf("%s %s, so root writes nothing in the hub's home", e.path, e.reason)
}

// homeSafe checks that home, its logs/ and backups/ are directories of a's,
// not symlinks, before root writes in them; logs/ and backups/ may be
// missing, which root then makes as a's.
func homeSafe(home string, a hubconfig.Account) error {
	for _, path := range []string{home, hubconfig.LogsDir(home), hubconfig.BackupsDir(home)} {
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) && path != home {
			continue
		}
		if err != nil {
			return &homeUnsafeError{path: path, reason: "cannot be read: " + err.Error()}
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return &homeUnsafeError{path: path, reason: "is a symlink"}
		}
		if !info.IsDir() {
			return &homeUnsafeError{path: path, reason: "is not a directory"}
		}
		if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != a.UID {
			return &homeUnsafeError{path: path, reason: "is not owned by " + a.Name}
		}
	}
	return nil
}

// baseURLTrusted fails when update.base_url is set to other than its default
// in the config file, which root takes only from its own environment.
func baseURLTrusted(env Env) error {
	if env.Update.BaseURL != "" || env.Config.UpdateBaseURL == hubconfig.ReleasesURL {
		return nil
	}
	from := env.Config.From[baseURLKey]
	if from == "default" || from == baseURLVar() {
		return nil
	}
	return &untrustedSourceError{url: env.Config.UpdateBaseURL, from: from}
}

const baseURLKey = "update.base_url"

// baseURLVar is the variable update.base_url is taken from in the
// environment.
func baseURLVar() string {
	for _, s := range hubconfig.Settings() {
		if s.Key == baseURLKey {
			return s.EnvVar
		}
	}
	panic("no setting " + baseURLKey)
}

// unitSafe checks the unit text, printed by the release, starts binary as
// user: a release built wrong must not run as root.
func unitSafe(text string, l hubconfig.Layout) error {
	var section string
	var execStarts, users, groups []string
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || section != "[Service]" {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "ExecStart":
			execStarts = append(execStarts, value)
		case "User":
			users = append(users, value)
		case "Group":
			groups = append(groups, value)
		}
	}
	var command string
	if len(execStarts) == 1 {
		command, _, _ = strings.Cut(execStarts[0], " ")
	}
	switch {
	case command != l.Binary:
		return fmt.Errorf("its unit does not start %s, with one ExecStart: %q", l.Binary, execStarts)
	case !slices.Equal(users, []string{l.User}):
		return fmt.Errorf("its unit does not run as the user %s, with one User: %q", l.User, users)
	case len(groups) > 1 || len(groups) == 1 && groups[0] != l.User:
		return fmt.Errorf("its unit does not run in the group %s: %q", l.User, groups)
	}
	return nil
}

func newUpdateCmd() *cobra.Command {
	var apply, rollback, discardChanges, cancel bool
	var channel, version string
	cmd := &cobra.Command{
		Use:   "update [--apply | --rollback]",
		Short: "Show whether a newer " + name + " is released, install it or roll it back",
		Long: "Without a flag nothing changes, and any user may run it. --apply installs the\n" +
			"newest release on the channel (the update.channel setting, or --channel):\n" +
			"the database is backed up to <home>/backups/, the last " + strconv.Itoa(backupsKept) + " kept, the service\n" +
			"is stopped, the installed binary, a copy of the database and the config file\n" +
			"are kept, and the service starts on the new binary. If any step fails, they\n" +
			"are put back and the service starts on the old binary.\n\n" +
			"The release writes the config file anew, as its setting write does: the\n" +
			"values set stay, its new settings are listed, and keys it no longer has are\n" +
			"commented out at the end. --apply then writes the completion of every shell\n" +
			"found for every user, from the installed binary, even when up to date.\n\n" +
			"--rollback installs the kept version again, with the config file as it was\n" +
			"before the update. The database stays when that version can use it; when a\n" +
			"migration since the update breaks it, the copy is restored, losing every\n" +
			"change since the update, which --discard-changes or the prompt agrees to,\n" +
			"and --cancel refuses. Only one previous version is kept.\n\n" +
			"--apply and --rollback need root, and a hub installed by install-hub.sh: the\n" +
			"service " + hubconfig.Service + " running " + hubconfig.Installed.Binary + ". They restart it, which\n" +
			"closes the nodes' connections; the nodes connect again on their own.\n\n" +
			"As root, update trusts only what root chose. --apply refuses an\n" +
			"update.base_url the config file sets, which the hub's user can write: give\n" +
			"it as " + hubconfig.EnvPrefix + "UPDATE_BASE_URL in root's environment instead. Both refuse a\n" +
			"home, logs/ or backups/ that is a symlink or not the hub's user's, before\n" +
			"writing anything, and a release whose unit does not start " + hubconfig.Installed.Binary + "\n" +
			"as the hub's user.",
		Example: clikit.Examples(
			"Show whether a newer release is available", name+" update",
			"Install it, as root: with sudo", name+" update --apply",
			"Install the newest dev release this once", name+" update --apply --channel dev",
			"Install one release", name+" update --apply --version 0.5.0",
			"Go back to the version the last update replaced", name+" update --rollback",
			"From a script, restoring the database copy if the previous version needs it", name+" update --rollback --discard-changes --json",
			"From a script, rolling back only if the database can stay", name+" update --rollback --cancel --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote: "Without a flag, the installed version, the newest on the channel and whether it is newer;\n" +
				"with --apply or --rollback, the versions and schema versions before and after.\n" +
				`With --json: {"update": {"current": ..., "latest": ..., "channel": ..., "available": ...}}, or` + "\n" +
				`{"result": ..., "update": {"from": ..., "to": ..., "channel": ..., "schema_from": ..., "schema_to": ..., "data_restored": ..., "settings": {"added": [...], "dropped": [...]}}, "warnings": [...]}`,
			clikit.Results:      clikit.Annotation(resultRolledBack, resultUpToDate, resultUpdated),
			clikit.JSONRequires: "rollback:" + clikit.Annotation("discard-changes", "cancel"),
			clikit.Errors: clikit.CodeList(hubapi.ErrorCodeChecksumMismatch, hubapi.ErrorCodeDownloadFailed, hubapi.ErrorCodeNoPreviousVersion,
				hubapi.ErrorCodeNotInstalled, hubapi.ErrorCodeOlderVersion, hubapi.ErrorCodeRestoreRequired, hubapi.ErrorCodeRootRequired,
				hubapi.ErrorCodeSettingsInvalid, hubapi.ErrorCodeUpdateFailed, hubapi.ErrorCodeUpdateRunning,
				hubapi.ErrorCodeUpdateSourceUntrusted, hubapi.ErrorCodeHomeUnsafe),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			channel = cmp.Or(channel, envOf(cmd).Config.UpdateChannel)
			if !slices.Contains(hubconfig.UpdateChannels, channel) {
				return &clikit.UsageError{Err: fmt.Errorf("--channel %q is not one of %s", channel, strings.Join(hubconfig.UpdateChannels, ", "))}
			}
			if discardChanges && !rollback {
				return &clikit.UsageError{Err: errors.New("--discard-changes goes with --rollback")}
			}
			if cancel && !rollback {
				return &clikit.UsageError{Err: errors.New("--cancel goes with --rollback")}
			}
			switch {
			case apply:
				cmd.Annotations[clikit.FailurePrefix] = "not updated"
				o, err := switchOptions(cmd, "apply")
				if err != nil {
					return err
				}
				r, err := update.Apply(cmd.Context(), o, channel, version)
				if err != nil {
					return err
				}
				// Up to date too: a hub installed before its completions gets them.
				r.Warnings = append(r.Warnings, writeCompletions(cmd, o)...)
				result := resultUpdated
				if r.UpToDate {
					result = resultUpToDate
				}
				return printUpdateResult(cmd, newUpdateResult(result, r, &channel))
			case rollback:
				cmd.Annotations[clikit.FailurePrefix] = "not rolled back"
				o, err := switchOptions(cmd, "rollback")
				if err != nil {
					return err
				}
				r, err := rollBack(cmd, o, discardChanges, cancel)
				if err != nil {
					return err
				}
				return printUpdateResult(cmd, newUpdateResult(resultRolledBack, r, nil))
			}
			s, err := update.Check(cmd.Context(), checkOptions(cmd), channel, version)
			if err != nil {
				return err
			}
			return clikit.PrintResult(cmd, updateCheck{Update: updateStatus{Current: s.Current, Latest: s.Latest, Channel: s.Channel, Available: s.Available}})
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "install the newest release on the channel")
	cmd.Flags().BoolVar(&rollback, "rollback", false, "install the version the last update replaced")
	cmd.Flags().StringVar(&channel, "channel", "", "follow this `channel`: stable or dev; the update.channel setting by default")
	cmd.Flags().StringVar(&version, "version", "", "this release `version` instead of the newest, such as 0.5.0")
	cmd.Flags().BoolVar(&discardChanges, "discard-changes", false, "with --rollback, restore the database copy without asking when the previous version needs it")
	cmd.Flags().BoolVar(&cancel, "cancel", false, "with --rollback, change nothing without asking when the previous version needs the database copy restored")
	cmd.MarkFlagsMutuallyExclusive("discard-changes", "cancel")
	clikit.CompleteFlag(cmd, "channel", cobra.FixedCompletions(hubconfig.UpdateChannels, cobra.ShellCompDirectiveNoFileComp))
	clikit.CompleteFlag(cmd, "version", cobra.NoFileCompletions)
	cmd.MarkFlagsMutuallyExclusive("apply", "rollback")
	cmd.MarkFlagsMutuallyExclusive("rollback", "channel")
	cmd.MarkFlagsMutuallyExclusive("rollback", "version")
	return cmd
}

// checkOptions are the options of the check, which reads only the releases.
func checkOptions(cmd *cobra.Command) update.Options {
	env := envOf(cmd)
	return update.Options{
		Program: update.Program{Name: name, GitHub: update.GitHub{Releases: hubconfig.ReleasesURL, API: hubconfig.ReleasesAPI, DevLatest: hubconfig.DevLatestURL}},
		Home:    env.Config.Home,
		Current: cmp.Or(env.Update.Version, identity.Version),
		BaseURL: cmp.Or(env.Update.BaseURL, env.Config.UpdateBaseURL, hubconfig.ReleasesURL),
	}
}

// switchOptions are the options of an update or rollback, given with flag:
// the installed hub's, switched by root, its home's steps run as its user.
func switchOptions(cmd *cobra.Command, flag string) (update.Options, error) {
	log := logging.FromContext(cmd.Context())
	env := envOf(cmd)
	layout := env.Update.layout()
	if !layout.Present() {
		log.Info("update refused: the hub is not installed", "binary", layout.Binary, "unit", layout.Unit)
		return update.Options{}, &notInstalledError{detail: fmt.Sprintf("%s is not installed as install-hub.sh installs it: no %s and %s", name, layout.Binary, layout.Unit)}
	}
	if !env.Root {
		return update.Options{}, &rootRequiredError{flag: flag}
	}
	if env.Account == nil {
		return update.Options{}, &notInstalledError{detail: fmt.Sprintf("%s is installed, but its user %s is not on this machine", name, layout.User)}
	}
	if err := runOf(cmd).homeErr; err != nil {
		return update.Options{}, err
	}
	if flag == "apply" {
		if err := baseURLTrusted(env); err != nil {
			log.Info("update refused: the release address is from the config file", "url", env.Config.UpdateBaseURL, "from", env.Config.From[baseURLKey])
			return update.Options{}, err
		}
	}
	a := *env.Account
	home := env.Config.Home
	o := checkOptions(cmd)
	o.Program = update.Program{
		Name:     name,
		Binary:   layout.Binary,
		DB:       hubconfig.DBPath(home),
		Config:   hubconfig.ConfigPath(home),
		Previous: layout.Previous(),
		Known:    hubdb.Known(),
		Command:  asAccount(a),
		GitHub:   o.Program.GitHub,
		Owner:    &update.Owner{UID: a.UID, GID: a.GID},
	}
	o.Staging = layout.Lib
	o.Preflight = env.Update.Preflight
	s := hubService{cmd: cmd, layout: layout, program: o.Program}
	o.Daemon = update.Daemon{Stop: s.stop, Start: s.start}
	if flag == "apply" {
		o.Backup = func(ctx context.Context) error { return s.backup(ctx, a, o.Current) }
	}
	if !clikit.JSONMode(cmd) {
		o.Progress = cmd.ErrOrStderr()
	}
	return o, nil
}

// writeCompletions writes the completion of every shell found, as the
// installed binary prints it, where the layout says, and returns what failed
// as warnings: the update is done, and Tab is a convenience.
func writeCompletions(cmd *cobra.Command, o update.Options) []string {
	ctx := cmd.Context()
	log := logging.FromContext(ctx)
	env := envOf(cmd)
	lookPath := env.Update.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	var warnings []string
	for _, c := range env.Update.layout().Completions {
		if _, err := lookPath(c.Shell); err != nil {
			log.Info("no completion written: the shell is not found", "shell", c.Shell)
			continue
		}
		script, err := output(func() *exec.Cmd {
			return o.Program.Command(ctx, o.Program.Binary, env.Config.Home, "completion", c.Shell)
		})
		if err == nil {
			err = os.MkdirAll(filepath.Dir(c.Path), 0o755) //nolint:gosec // the release's directory, which the hub's user reads
		}
		if err == nil {
			err = atomicfile.Write(c.Path, script, 0o644)
		}
		if err != nil {
			log.Error("completion not written", "shell", c.Shell, "path", c.Path, "err", err)
			warnings = append(warnings, fmt.Sprintf("the %s completion is not written to %s: %v", c.Shell, c.Path, err))
			continue
		}
		log.Info("completion written", "shell", c.Shell, "path", c.Path)
	}
	return warnings
}

// asAccount runs a release as the user a, on the home it is given.
func asAccount(a hubconfig.Account) func(ctx context.Context, binary, home string, args ...string) *exec.Cmd {
	return func(ctx context.Context, binary, home string, args ...string) *exec.Cmd {
		c := exec.CommandContext(ctx, binary, args...) //nolint:gosec // the release just installed, as the hub's user
		c.Env = []string{"PATH=" + servicePath, "HOME=" + a.Home, hubconfig.EnvPrefix + "HOME=" + home}
		if a.UID != os.Geteuid() {
			groups := make([]uint32, len(a.Groups))
			for i, g := range a.Groups {
				groups[i] = uint32(g) //nolint:gosec // a group id the system gave fits a uint32
			}
			c.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(a.UID), Gid: uint32(a.GID), Groups: groups}} //nolint:gosec // a user id the system gave fits a uint32
		}
		return c
	}
}

// hubService is the installed hub's systemd service, as an update stops and
// starts it.
type hubService struct {
	cmd     *cobra.Command
	layout  hubconfig.Layout
	program update.Program
}

func (s hubService) systemctl(ctx context.Context, args ...string) error {
	log := logging.FromContext(ctx)
	if run := envOf(s.cmd).Update.Systemctl; run != nil {
		err := run(ctx, args...)
		log.Info("systemctl ran", "args", args, "ok", err == nil)
		return err
	}
	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput() //nolint:gosec // systemctl, with the hub's own arguments
	log.Info("systemctl ran", "args", args, "ok", err == nil)
	if err != nil {
		log.Error("systemctl failed", "args", args, "output", string(out))
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s hubService) stop(ctx context.Context) error {
	return s.systemctl(ctx, "stop", hubconfig.Service)
}

// start writes the unit the installed binary prints, has systemd read it,
// starts the service and returns the version the hub answers on its socket
// with.
func (s hubService) start(ctx context.Context) (string, error) {
	log := logging.FromContext(ctx)
	env := envOf(s.cmd)
	args := []string{"service", "unit", "--json"}
	if privilegedPort(env.Config) {
		args = append(args, "--"+privilegedPortFlag)
	}
	out, err := output(func() *exec.Cmd { return s.program.Command(ctx, s.layout.Binary, env.Config.Home, args...) })
	var u serviceUnit
	if err == nil {
		err = json.Unmarshal(out, &u)
	}
	if err != nil {
		return "", fmt.Errorf("%s does not print its unit: %w", s.layout.Binary, err)
	}
	if err := unitSafe(u.Unit, s.layout); err != nil {
		log.Error("unit refused", "err", err)
		return "", fmt.Errorf("%s: %w", s.layout.Binary, err)
	}
	if err := atomicfile.Write(s.layout.Unit, []byte(u.Unit), 0o644); err != nil {
		return "", err
	}
	log.Info("unit written", "path", s.layout.Unit, "privileged_port", len(args) > 3)
	if err := s.systemctl(ctx, "daemon-reload"); err != nil {
		return "", err
	}
	// A release that failed to start makes systemd refuse to start the
	// service again for a while, the one put back with it.
	if err := s.systemctl(ctx, "reset-failed", hubconfig.Service); err != nil {
		return "", err
	}
	if err := s.systemctl(ctx, "start", hubconfig.Service); err != nil {
		return "", fmt.Errorf("%w; journalctl -u %s says why", err, name)
	}
	inv := clikit.InvocationOf(s.cmd)
	c := newClient(hubconfig.SocketPath(env.Config.Home), inv.DebugID, inv.Quiet)
	defer c.close()
	timeout := cmp.Or(env.Update.StartTimeout, defaultStartTimeout)
	wait, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		v, err := c.version(wait)
		if err == nil {
			log.Info("hub answers", "version", v.Version)
			return v.Version, nil
		}
		if !unavailable(err) {
			return "", err
		}
		select {
		case <-wait.Done():
			return "", fmt.Errorf("the hub did not answer within %s: %w; journalctl -u %s says why", timeout, err, name)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// output runs the command build makes and returns its stdout, again while a
// process forked as its binary was written holds it open: golang.org/issue/22315.
func output(build func() *exec.Cmd) ([]byte, error) {
	for attempt := 1; ; attempt++ {
		out, err := build().Output()
		if !errors.Is(err, syscall.ETXTBSY) || attempt == 5 {
			return out, err
		}
		time.Sleep(time.Duration(attempt) * 50 * time.Millisecond)
	}
}

// privilegedPort is true when the hub listens on a port below 1024, which
// its service needs a capability for.
func privilegedPort(cfg hubconfig.Config) bool {
	for _, addr := range []string{cfg.ListenWeb, cfg.ListenNodes} {
		if _, port, err := net.SplitHostPort(addr); err == nil {
			if n, err := strconv.Atoi(port); err == nil && n > 0 && n < 1024 {
				return true
			}
		}
	}
	return false
}

// backup backs up the database of the hub, version current, to
// <home>/backups/hub-<current>.db, through the running hub, or itself when
// none runs, and keeps the newest backupsKept. What it makes is a's.
func (s hubService) backup(ctx context.Context, a hubconfig.Account, current string) error {
	log := logging.FromContext(ctx)
	env := envOf(s.cmd)
	home := env.Config.Home
	dir := hubconfig.BackupsDir(home)
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	if err := os.Lchown(dir, a.UID, a.GID); err != nil {
		return err
	}
	path := filepath.Join(dir, "hub-"+current+".db")
	// An update from this version again, after a rollback, backs it up anew.
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if !clikit.JSONMode(s.cmd) {
		fmt.Fprintf(s.cmd.ErrOrStderr(), "==> backing up the database to %s\n", path)
	}
	_, err := clientOf(s.cmd).backup(ctx, path)
	if unavailable(err) {
		db := hubconfig.DBPath(home)
		if _, statErr := os.Stat(db); errors.Is(statErr, fs.ErrNotExist) {
			log.Info("no database to back up", "path", db)
			return nil
		}
		log.Info("hub not running: backing up the database itself", "path", db)
		if err = sqlite.Backup(ctx, db, path); err == nil {
			err = os.Lchown(path, a.UID, a.GID)
		}
	}
	if err != nil {
		return err
	}
	log.Info("database backed up", "path", path)
	return pruneBackups(ctx, dir)
}

// pruneBackups deletes all but the newest backupsKept backups in dir.
func pruneBackups(ctx context.Context, dir string) error {
	log := logging.FromContext(ctx)
	paths, err := filepath.Glob(filepath.Join(dir, "hub-*.db"))
	if err != nil {
		return err
	}
	type backup struct {
		path    string
		modTime time.Time
	}
	var backups []backup
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		backups = append(backups, backup{p, info.ModTime()})
	}
	slices.SortFunc(backups, func(a, b backup) int { return b.modTime.Compare(a.modTime) })
	for _, b := range backups[min(len(backups), backupsKept):] {
		if err := os.Remove(b.path); err != nil {
			return err
		}
		log.Info("old backup deleted", "path", b.path)
	}
	return nil
}

// rollBack rolls back, asking before restoring the database copy unless
// discardChanges agreed to it or cancel refused it.
func rollBack(cmd *cobra.Command, o update.Options, discardChanges, cancel bool) (update.Result, error) {
	r, err := update.Rollback(cmd.Context(), o, discardChanges)
	if discardChanges && r.DataRestored {
		clikit.LogAnswer(cmd, discardChangesQuestion, "yes", "--discard-changes")
	}
	if _, needed := errors.AsType[*update.RestoreRequiredError](err); !needed {
		return r, err
	}
	if cancel {
		clikit.LogAnswer(cmd, discardChangesQuestion, "no", "--cancel")
		return r, err
	}
	if !clikit.CanPrompt(cmd) {
		clikit.LogUnanswered(cmd, discardChangesQuestion)
		return r, err
	}
	prompt, st := cmd.ErrOrStderr(), clikit.StderrStyle(cmd)
	fmt.Fprintln(prompt, err)
	answer, askErr := clikit.Ask(prompt, bufio.NewScanner(cmd.InOrStdin()), "restore the copy? "+st.Command("[y/N]")+": ", "y", "n", "")
	if askErr != nil {
		return r, askErr
	}
	if answer != "y" {
		clikit.LogAnswer(cmd, discardChangesQuestion, "no", "prompt")
		return r, err
	}
	clikit.LogAnswer(cmd, discardChangesQuestion, "yes", "prompt")
	return update.Rollback(cmd.Context(), o, true)
}

// updateCheck is the installed version and the newest on the channel.
type updateCheck struct {
	Update updateStatus `json:"update"`
}

type updateStatus struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Channel   string `json:"channel"`
	Available bool   `json:"available"`
}

func (c updateCheck) Text(w io.Writer, st style.Style) {
	u := c.Update
	if !u.Available {
		fmt.Fprintf(w, "%s: %s %s %s\n", st.Success("up to date"), name, st.Name(u.Current), st.Muted(fmt.Sprintf("(newest on the %s channel: %s)", u.Channel, u.Latest)))
		return
	}
	fmt.Fprintf(w, "%s: %s %s -> %s %s\n", st.Warning("update available"), name, u.Current, st.Name(u.Latest), st.Muted("("+u.Channel+" channel)"))
	fmt.Fprintf(w, "  run %s\n", st.Command("sudo "+name+" update --apply"))
}

// updateResult is what an update or rollback did.
type updateResult struct {
	Result string       `json:"result"`
	Update updateChange `json:"update"`
	// Warnings are what failed once the version switched to ran.
	Warnings []string `json:"warnings"`
}

type updateChange struct {
	From         string  `json:"from"`
	To           string  `json:"to"`
	Channel      *string `json:"channel"` // null for a rollback
	SchemaFrom   int     `json:"schema_from"`
	SchemaTo     int     `json:"schema_to"`
	DataRestored *bool   `json:"data_restored"` // null for an update
	// Settings is how an update wrote the config file anew; null for a
	// rollback or when nothing was newer.
	Settings *updateSettings `json:"settings"`
}

type updateSettings struct {
	Added   []string `json:"added"`
	Dropped []string `json:"dropped"`
}

func newUpdateResult(result string, r update.Result, channel *string) updateResult {
	u := updateChange{From: r.From, To: r.To, Channel: channel, SchemaFrom: r.SchemaFrom, SchemaTo: r.SchemaTo}
	if result == resultRolledBack {
		u.DataRestored = &r.DataRestored
	}
	if r.Settings != nil {
		u.Settings = &updateSettings{Added: r.Settings.Added, Dropped: r.Settings.Dropped}
	}
	return updateResult{Result: result, Update: u, Warnings: r.Warnings}
}

// printUpdateResult prints r, and in text its warnings on stderr.
func printUpdateResult(cmd *cobra.Command, r updateResult) error {
	if err := clikit.PrintResult(cmd, r); err != nil || clikit.JSONMode(cmd) {
		return err
	}
	st := clikit.StderrStyle(cmd)
	for _, w := range r.Warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s %s\n", name, st.Warning("warning:"), w)
	}
	return nil
}

func (r updateResult) Text(w io.Writer, st style.Style) {
	u := r.Update
	field := clikit.FieldPrinter(w, st, 9)
	schema := fmt.Sprint(u.SchemaTo)
	if u.SchemaFrom != u.SchemaTo {
		schema = fmt.Sprintf("%d -> %d", u.SchemaFrom, u.SchemaTo)
	}
	switch r.Result {
	case resultUpToDate:
		fmt.Fprintf(w, "%s: %s %s %s\n", st.Success("up to date"), name, st.Name(u.To), st.Muted("(newest on the "+*u.Channel+" channel)"))
	case resultUpdated:
		fmt.Fprintf(w, "%s %s %s -> %s\n", st.Success("updated"), name, u.From, st.Name(u.To))
		field("schema:", schema)
		if s := u.Settings; s != nil && len(s.Added) > 0 {
			field("settings:", "added "+strings.Join(s.Added, ", "))
		}
		if s := u.Settings; s != nil && len(s.Dropped) > 0 {
			field("settings:", "dropped "+strings.Join(s.Dropped, ", ")+" "+st.Muted("(commented at the end of the config file)"))
		}
		field("rollback:", st.Command("sudo "+name+" update --rollback")+" goes back to "+u.From)
	default:
		fmt.Fprintf(w, "%s %s %s -> %s\n", st.Success("rolled back"), name, u.From, st.Name(u.To))
		field("schema:", schema)
		if *u.DataRestored {
			field("database:", st.Warning("restored from the copy taken at the update"))
		} else {
			field("database:", "kept")
		}
	}
}

// updateProblem returns the problem of an update that failed, ok false when
// err is no update error.
func updateProblem(err error) (p hubapi.Problem, ok bool) {
	hint := func(h string) { p.Hint = &h }
	if e, found := errors.AsType[*rootRequiredError](err); found {
		p = hubapi.NewProblem(hubapi.ErrorCodeRootRequired, err.Error())
		hint("run sudo " + name + " update --" + e.flag)
	} else if e, found := errors.AsType[*untrustedSourceError](err); found {
		p = hubapi.NewProblem(hubapi.ErrorCodeUpdateSourceUntrusted, err.Error())
		p.Details = &map[string]any{"url": e.url, "from": e.from}
		hint("to update from it, set it in root's environment for this run: sudo " + baseURLVar() + "=" + e.url + " " + name + " update --apply")
	} else if e, found := errors.AsType[*homeUnsafeError](err); found {
		p = hubapi.NewProblem(hubapi.ErrorCodeHomeUnsafe, err.Error())
		p.Details = &map[string]any{"path": e.path}
		hint("make " + e.path + " a directory of " + hubconfig.Installed.User + "'s again, then run the update again")
	} else if _, found := errors.AsType[*notInstalledError](err); found {
		p = hubapi.NewProblem(hubapi.ErrorCodeNotInstalled, err.Error())
		hint("update it the way it was installed: a container is built again, a binary copied by hand copied again")
	} else if e, found := errors.AsType[*update.DownloadError](err); found {
		p = hubapi.NewProblem(hubapi.ErrorCodeDownloadFailed, err.Error())
		p.Details = &map[string]any{"url": e.URL}
		hint("check the version and the network")
	} else if e, found := errors.AsType[*update.ChecksumError](err); found {
		p = hubapi.NewProblem(hubapi.ErrorCodeChecksumMismatch, err.Error())
		p.Details = &map[string]any{"archive": e.Archive}
		hint("try again; nothing was installed")
	} else if _, found := errors.AsType[*update.OlderError](err); found {
		p = hubapi.NewProblem(hubapi.ErrorCodeOlderVersion, err.Error())
		hint("sudo " + name + " update --rollback goes back to the version the last update replaced")
	} else if _, found := errors.AsType[*update.RunningError](err); found {
		p = hubapi.NewProblem(hubapi.ErrorCodeUpdateRunning, err.Error())
		hint("wait until it ends")
	} else if e, found := errors.AsType[*update.FailedError](err); found {
		p = hubapi.NewProblem(hubapi.ErrorCodeUpdateFailed, err.Error())
		p.Details = &map[string]any{"step": e.Step, "undone": e.UndoErr == nil, "version": e.Version}
		hint(e.Version + " is installed and running again; journalctl -u " + name + " and the debug_id in the logs say what failed")
		if e.UndoErr != nil {
			hint("install it again with install-hub.sh; journalctl -u " + name + " and the debug_id in the logs say what failed")
		}
	} else if e, found := errors.AsType[*update.SettingsError](err); found {
		p = hubapi.NewProblem(hubapi.ErrorCodeSettingsInvalid, err.Error())
		p.Details = &map[string]any{"version": e.Version, "settings": e.Details}
		hint("change them in the config file, then update again; nothing was installed")
	} else if e, found := errors.AsType[*update.NoPreviousError](err); found {
		p = hubapi.NewProblem(hubapi.ErrorCodeNoPreviousVersion, err.Error())
		if e.Kept != "" {
			hint("another install replaced " + e.UpdatedTo + "; installing " + e.UpdatedTo + " again makes the rollback to " + e.Kept + " possible")
		} else {
			hint("only the version " + name + " update --apply replaced is kept, until a rollback")
		}
	} else if e, found := errors.AsType[*update.RestoreRequiredError](err); found {
		p = hubapi.NewProblem(hubapi.ErrorCodeRestoreRequired, err.Error())
		p.Details = &map[string]any{"to": e.To, "schema": e.Schema, "oldest": e.Oldest, "known": e.Known}
		hint("pass --discard-changes to restore the copy")
	} else {
		return hubapi.Problem{}, false
	}
	return p, true
}
