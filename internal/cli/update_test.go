package cli

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/daemon"
	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/identity"
)

// brokenRelease says it is version 0.3.0 and writes the settings, adding a
// line, but cannot run the daemon.
const brokenRelease = `#!/bin/sh
case "$1" in
version) echo '{"cli": {"name": "wdx", "version": "0.3.0", "commit": "none"}, "daemon": null}' ;;
settings) { cat "$WDX_HOME/config.yaml"; echo "# written by 0.3.0"; } >"$5"
	echo '{"result": "written", "settings": {"path": "'"$5"'", "added": ["new.setting"], "dropped": []}}' ;;
*) echo "broken release" >&2; exit 1 ;;
esac
`

// pickyRelease does not accept the settings.
const pickyRelease = `#!/bin/sh
case "$1" in
settings) echo '{"code": "usage", "detail": "invalid settings: color: not here", "details": {"settings": [{"key": "color", "from": "config.yaml:1", "value": "auto", "reason": "not here"}]}}'
	exit 2 ;;
*) echo "picky release" >&2; exit 1 ;;
esac
`

// publish lays out binaries, by version, as releases the way
// scripts/dist.sh does, with latest as the newest stable release and
// latestDev as the newest of either kind, and returns their file:// URL.
func publish(t *testing.T, latest, latestDev string, binaries map[string]string) (base, dir string) {
	t.Helper()
	dir = t.TempDir()
	for version, binary := range binaries {
		name := fmt.Sprintf("%s_%s_%s_%s.tar.gz", identity.Name, version, runtime.GOOS, runtime.GOARCH)
		sum := writeArchive(t, filepath.Join(dir, "v"+version, name), binary)
		writeFile(t, filepath.Join(dir, "v"+version, "checksums.txt"), sum+"  "+name+"\n")
	}
	writeFile(t, filepath.Join(dir, "LATEST"), "v"+latest+"\n")
	writeFile(t, filepath.Join(dir, "LATEST-dev"), "v"+latestDev+"\n")
	return "file://" + dir, dir
}

// writeArchive writes a release archive at path holding binary, streamed
// file to file, and returns its sha256.
func writeArchive(t *testing.T, path, binary string) string {
	t.Helper()
	in, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	hash := sha256.New()
	// Compressing the binary under the race detector takes seconds.
	gz, _ := gzip.NewWriterLevel(io.MultiWriter(out, hash), gzip.NoCompression)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: identity.Name, Mode: 0o755, Size: info.Size()}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(tw, in); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// copyBinary copies the binary src to dest, file to file.
func copyBinary(t *testing.T, dest, src string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
}

// digest is the sha256 of the file at path, read a block at a time.
func digest(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

// installation is wdx installed at binary, serving home, updated from base.
type installation struct {
	home, binary, base string
}

// install installs this tree's wdx for a home whose releases are at base.
// Its daemon, if started, is killed when the test ends.
func install(t *testing.T, base string) installation {
	t.Helper()
	home := shortHome(t)
	t.Cleanup(func() {
		if pid, _ := daemon.Running(home); pid != 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	// The installed binaries read it when run on their own.
	writeFile(t, identity.ConfigPath(home), "otel:\n  endpoint: \"\"\nupdate:\n  base_url: "+base+"\n")
	binary := filepath.Join(t.TempDir(), identity.Name)
	copyBinary(t, binary, wdxBinary)
	return installation{home: home, binary: binary, base: base}
}

func (in installation) env(env *Env) {
	env.Executable = in.binary
	env.Config.UpdateBaseURL = in.base
}

func (in installation) json(t *testing.T, args ...string) (jsonOutput, int) {
	t.Helper()
	return wdxJSONWith(t, in.home, in.env, args...)
}

// run runs the installed binary on its own with --json.
func (in installation) run(t *testing.T, args ...string) (jsonOutput, int) {
	t.Helper()
	c := exec.Command(in.binary, append(args, "--json")...)
	c.Env = append(os.Environ(), identity.Env("HOME")+"="+in.home)
	out, _ := c.Output()
	var v jsonOutput
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("%v: stdout is not JSON: %v\n%s", args, err, out)
	}
	return v, c.ProcessState.ExitCode()
}

// runText runs the installed binary on its own, in text.
func (in installation) runText(t *testing.T, args ...string) output {
	t.Helper()
	return in.runTyped(t, nil, args...)
}

// runTyped runs the installed binary on its own, in text, reading stdin.
func (in installation) runTyped(t *testing.T, stdin io.Reader, args ...string) output {
	t.Helper()
	c := exec.Command(in.binary, args...)
	c.Env = append(os.Environ(), identity.Env("HOME")+"="+in.home)
	c.Stdin = stdin
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	_ = c.Run() // the exit code tells
	return output{exit: c.ProcessState.ExitCode(), stdout: stdout.String(), stderr: stderr.String()}
}

// wantInstalled checks that the installed binary is binary.
func (in installation) wantInstalled(t *testing.T, binary string) {
	t.Helper()
	if digest(t, in.binary) != digest(t, binary) {
		t.Fatalf("want %s installed, got another binary", binary)
	}
}

// The checks run the installed binary: this one, of another version than an
// update installs, would warn about the daemon's.

// wantDaemon checks that the daemon runs version.
func (in installation) wantDaemon(t *testing.T, version string) {
	t.Helper()
	if v, _ := in.run(t, "daemon", "status"); v.Daemon == nil || v.Daemon.Version != version {
		t.Fatalf("want the daemon running %s, got %+v", version, v.Daemon)
	}
}

func (in installation) wantProjects(t *testing.T, names ...string) {
	t.Helper()
	v, _ := in.run(t, "project", "list")
	var got []string
	for _, p := range v.Projects {
		got = append(got, p.Name)
	}
	if strings.Join(got, ",") != strings.Join(names, ",") {
		t.Fatalf("want projects %v, got %v", names, got)
	}
}

func (in installation) newProject(t *testing.T, name string) {
	t.Helper()
	if v, exit := in.run(t, "project", "create", "--name", name, "--cancel"); exit != api.ExitOK {
		t.Fatalf("create %s: %+v", name, v)
	}
}

func TestUpdateCheck(t *testing.T) {
	t.Parallel()
	base, _ := publish(t, releaseVersion, "0.3.0-dev.1", map[string]string{releaseVersion: releaseBinary})
	in := install(t, base)

	v, exit := in.json(t, "update")
	if exit != api.ExitOK || v.Result != "" || v.Update == nil || v.Update.Current != identity.Version || v.Update.Latest != releaseVersion || *v.Update.Channel != "stable" || !v.Update.Available {
		t.Fatalf("want %s available, got %+v", releaseVersion, v.Update)
	}
	if v, _ = in.json(t, "update", "--channel", "dev"); v.Update.Latest != "0.3.0-dev.1" || *v.Update.Channel != "dev" {
		t.Fatalf("want the newest dev release, got %+v", v.Update)
	}
	if v, _ = in.json(t, "update", "--version", "v"+releaseVersion); v.Update.Latest != releaseVersion || !v.Update.Available {
		t.Fatalf("want the release asked for, got %+v", v.Update)
	}
	v, exit = in.json(t, "update", "--version", "9.9.9")
	wantError(t, v, exit, api.ErrorCodeDownloadFailed, api.ExitSystem)
	wantHint(t, v, "update.base_url")

	out := wdxWith(t, in.home, in.env, "update")
	if out.exit != api.ExitOK || out.stdout != "update available: wdx dev -> 0.2.0 (stable channel)\n  run wdx update --apply\n" {
		t.Fatalf("unexpected text: %+v", out)
	}
}

func TestUpdateUsage(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"update", "--channel", "beta"},
		{"update", "--discard-changes"},
		{"update", "--apply", "--rollback"},
		{"update", "--rollback", "--version", "0.2.0"},
		{"update", "--cancel"},
		{"update", "--rollback"},
		{"update", "--rollback", "--discard-changes", "--cancel"},
	} {
		v, exit := wdxJSON(t, t.TempDir(), args...)
		wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	}
}

// TestUpdateApplyAndRollback updates to a release and rolls back to the
// version it replaced, the database staying as it is.
func TestUpdateApplyAndRollback(t *testing.T) {
	t.Parallel()
	base, _ := publish(t, releaseVersion, releaseVersion, map[string]string{releaseVersion: releaseBinary, "0.1.0": wdxBinary})
	in := install(t, base)
	if _, exit := in.json(t, "daemon", "start"); exit != api.ExitOK {
		t.Fatal("the daemon did not start")
	}
	newProject(t, in.home, "before")
	settings := readFile(t, identity.ConfigPath(in.home))

	v, exit := in.json(t, "update", "--apply")
	if exit != api.ExitOK || v.Result != "updated" || v.Update.From != identity.Version || v.Update.To != releaseVersion ||
		*v.Update.Channel != "stable" || v.Update.SchemaFrom != db.Known() || v.Update.SchemaTo != db.Known() || v.Update.DataRestored != nil {
		t.Fatalf("want updated to %s, got exit %d, %+v", releaseVersion, exit, v.Update)
	}
	if v.Warnings == nil || len(v.Warnings) != 0 {
		t.Fatalf("want no warnings, got %#v", v.Warnings)
	}
	if s := v.Update.Settings; s == nil || !slices.Contains(s.Added, "update.channel") || slices.Contains(s.Added, "update.base_url") || len(s.Dropped) != 0 {
		t.Fatalf("want the settings not set added, got %+v", v.Update.Settings)
	}
	if got := readFile(t, identity.ConfigPath(in.home)); !strings.Contains(got, "  base_url: "+in.base+"\n") || !strings.Contains(got, "  # channel: stable\n") {
		t.Fatalf("want the settings written anew, keeping base_url, got\n%s", got)
	}
	in.wantInstalled(t, releaseBinary)
	in.wantDaemon(t, releaseVersion)
	in.wantProjects(t, "before")
	if _, err := os.Stat(filepath.Join(identity.PreviousDir(in.home), identity.Name+".db")); err != nil {
		t.Fatalf("want the database backed up, got %v", err)
	}

	if v, exit := in.run(t, "update", "--apply"); exit != api.ExitOK || v.Result != "up_to_date" || v.Update.To != releaseVersion {
		t.Fatalf("want up to date, got exit %d, %+v", exit, v)
	}
	if v, exit := in.run(t, "update", "--apply", "--version", "0.1.0"); exit != api.ExitRejected || v.Code != string(api.ErrorCodeOlderVersion) {
		t.Fatalf("want an older version refused, got exit %d, %+v", exit, v)
	}
	in.newProject(t, "after")

	v, exit = in.run(t, "update", "--rollback", "--cancel")
	if exit != api.ExitOK || v.Result != "rolled_back" || v.Update.To != identity.Version || v.Update.Channel != nil || *v.Update.DataRestored {
		t.Fatalf("want rolled back keeping the database, got exit %d, %+v", exit, v.Update)
	}
	if v.Warnings == nil || len(v.Warnings) != 0 {
		t.Fatalf("want no warnings, got %#v", v.Warnings)
	}
	in.wantInstalled(t, wdxBinary)
	in.wantDaemon(t, identity.Version)
	in.wantProjects(t, "after", "before")
	if got := readFile(t, identity.ConfigPath(in.home)); got != settings {
		t.Fatalf("want the settings from before the update, got\n%s", got)
	}
	if _, err := os.Stat(identity.PreviousDir(in.home)); !os.IsNotExist(err) {
		t.Fatalf("want nothing kept after the rollback, got %v", err)
	}

	v, exit = in.run(t, "update", "--rollback", "--cancel")
	wantError(t, v, exit, api.ErrorCodeNoPreviousVersion, api.ExitRejected)
}

// TestUpdateRollbackRestores rolls back across a migration that breaks the
// previous version, restoring the backup only when told to.
func TestUpdateRollbackRestores(t *testing.T) {
	t.Parallel()
	in, breaking := updatedAcrossBreaking(t)

	v, exit := in.run(t, "update", "--rollback", "--cancel")
	wantError(t, v, exit, api.ErrorCodeRestoreRequired, api.ExitRejected)
	wantHint(t, v, "--discard-changes")
	if known, _ := v.Details["known"].(float64); int(known) != db.Known() {
		t.Fatalf("want the schema the previous version knows in details, got %+v", v.Details)
	}
	in.wantInstalled(t, releaseBinary)

	out := in.runText(t, "update", "--rollback")
	if out.exit != api.ExitRejected || !strings.Contains(out.stderr, "hint: pass --discard-changes") {
		t.Fatalf("want the restore refused without a terminal, got %+v", out)
	}

	v, exit = in.run(t, "update", "--rollback", "--discard-changes")
	if exit != api.ExitOK || v.Result != "rolled_back" || !*v.Update.DataRestored || v.Update.SchemaFrom != breaking || v.Update.SchemaTo != db.Known() {
		t.Fatalf("want rolled back restoring the database, got exit %d, %+v", exit, v.Update)
	}
	in.wantInstalled(t, wdxBinary)
	in.wantDaemon(t, identity.Version)
	in.wantProjects(t, "before")
}

// TestUpdateRollbackPromptYes restores the backup when asked.
func TestUpdateRollbackPromptYes(t *testing.T) {
	t.Parallel()
	in, _ := updatedAcrossBreaking(t)

	out := in.runTyped(t, typed(t, "y"), "update", "--rollback")
	if out.exit != api.ExitOK || !strings.Contains(out.stderr, "restore the backup? [y/N]: ") {
		t.Fatalf("want rolled back, got %+v", out)
	}
	in.wantInstalled(t, wdxBinary)
	in.wantProjects(t, "before")
}

// TestUpdateRollbackPromptNo answers with Enter, which is no.
func TestUpdateRollbackPromptNo(t *testing.T) {
	t.Parallel()
	in, _ := updatedAcrossBreaking(t)

	out := in.runTyped(t, typed(t, ""), "update", "--rollback")
	if out.exit != api.ExitRejected || !strings.Contains(out.stderr, "[restore_required]") {
		t.Fatalf("want the rollback refused, got %+v", out)
	}
	in.wantInstalled(t, releaseBinary)
}

// updatedAcrossBreaking updates an installation to a release whose
// migration breaks the previous version, which only the database backup
// can then serve, and returns it with that migration's schema.
func updatedAcrossBreaking(t *testing.T) (installation, int) {
	t.Helper()
	base, _ := publish(t, releaseVersion, releaseVersion, map[string]string{releaseVersion: releaseBinary})
	in := install(t, base)
	if _, exit := in.json(t, "daemon", "start"); exit != api.ExitOK {
		t.Fatal("the daemon did not start")
	}
	newProject(t, in.home, "before")
	if _, exit := in.json(t, "update", "--apply"); exit != api.ExitOK {
		t.Fatal("the update failed")
	}
	in.newProject(t, "after")
	// As a migration of the new version that breaks older ones would.
	conn, err := sql.Open("sqlite", identity.DBPath(in.home))
	if err != nil {
		t.Fatal(err)
	}
	breaking := db.Known() + 1
	if _, err := conn.Exec(fmt.Sprintf("PRAGMA user_version = %d; UPDATE schema_readers SET oldest = %d", breaking, breaking)); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	return in, breaking
}

// TestUpdateUndone updates to a release whose daemon does not start: the
// old binary and database are put back and serve again.
func TestUpdateUndone(t *testing.T) {
	t.Parallel()
	broken := filepath.Join(t.TempDir(), identity.Name)
	writeFile(t, broken, brokenRelease)
	base, _ := publish(t, "0.3.0", "0.3.0", map[string]string{"0.3.0": broken})
	in := install(t, base)
	in.wantUndone(t, "start")
}

// TestUpdateUndoneBackUp updates where the installed binary cannot be read
// to back it up: the daemon starts again on what is there.
func TestUpdateUndoneBackUp(t *testing.T) {
	t.Parallel()
	base, _ := publish(t, releaseVersion, releaseVersion, map[string]string{releaseVersion: releaseBinary})
	in := install(t, base)
	// Run, never read: as an executable only.
	if err := os.Chmod(in.binary, 0o111); err != nil {
		t.Fatal(err)
	}
	in.wantUndone(t, "back up", func() {
		if err := os.Chmod(in.binary, 0o755); err != nil {
			t.Fatal(err)
		}
	})
}

// TestUpdateUndoneInstall updates to a release that cannot be read once
// checked: what was backed up is put back.
func TestUpdateUndoneInstall(t *testing.T) {
	t.Parallel()
	unreadable := filepath.Join(t.TempDir(), identity.Name)
	writeFile(t, unreadable, strings.Replace(brokenRelease, "version) ", `version) chmod 111 "$0"; `, 1))
	base, _ := publish(t, "0.3.0", "0.3.0", map[string]string{"0.3.0": unreadable})
	in := install(t, base)
	in.wantUndone(t, "install")
}

// wantUndone starts the daemon, updates, and checks the update failed at
// step and was undone: the binary, database and settings it found are back
// and serve. readable runs once the update has, before the checks.
func (in installation) wantUndone(t *testing.T, step string, readable ...func()) {
	t.Helper()
	if _, exit := in.json(t, "daemon", "start"); exit != api.ExitOK {
		t.Fatal("the daemon did not start")
	}
	newProject(t, in.home, "before")
	settings := readFile(t, identity.ConfigPath(in.home))

	v, exit := in.json(t, "update", "--apply")
	for _, f := range readable {
		f()
	}
	wantError(t, v, exit, api.ErrorCodeUpdateFailed, api.ExitSystem)
	if v.Details["step"] != step || v.Details["undone"] != true || v.Details["version"] != identity.Version {
		t.Fatalf("want the %s undone, got %+v", step, v.Details)
	}
	in.wantInstalled(t, wdxBinary)
	in.wantDaemon(t, identity.Version)
	in.wantProjects(t, "before")
	if got := readFile(t, identity.ConfigPath(in.home)); got != settings {
		t.Fatalf("want the settings put back, got\n%s", got)
	}
	if _, err := os.Stat(identity.PreviousDir(in.home)); !os.IsNotExist(err) {
		t.Fatalf("want nothing kept after a failed update, got %v", err)
	}
}

// TestUpdateKeepsPrevious updates where the version kept before cannot be
// deleted: the update succeeds with a warning, and the version it replaced
// is kept for a rollback all the same.
func TestUpdateKeepsPrevious(t *testing.T) {
	t.Parallel()
	base, _ := publish(t, releaseVersion, releaseVersion, map[string]string{releaseVersion: releaseBinary})
	in := install(t, base)
	if _, exit := in.json(t, "daemon", "start"); exit != api.ExitOK {
		t.Fatal("the daemon did not start")
	}
	prev := identity.PreviousDir(in.home)
	writeFile(t, filepath.Join(prev, "previous.json"), `{"version": "0.0.1", "schema": 1}`)
	writeFile(t, filepath.Join(prev, "locked", "file"), "")
	if err := os.Chmod(filepath.Join(prev, "locked"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, dir := range []string{prev, prev + ".old"} {
			_ = os.Chmod(filepath.Join(dir, "locked"), 0o700)
		}
	})

	v, exit := in.json(t, "update", "--apply")
	if exit != api.ExitOK || v.Result != "updated" || len(v.Warnings) != 1 || !strings.Contains(v.Warnings[0], prev+".old") {
		t.Fatalf("want updated with a warning, got exit %d, %+v, %q", exit, v.Update, v.Warnings)
	}
	if got := readFile(t, filepath.Join(prev, "previous.json")); !strings.Contains(got, `"version":"`+identity.Version+`"`) {
		t.Fatalf("want the replaced version kept, got %s", got)
	}
	if v, exit := in.run(t, "update", "--rollback", "--cancel"); exit != api.ExitOK || v.Result != "rolled_back" {
		t.Fatalf("want a rollback, got exit %d, %+v", exit, v)
	}
	in.wantDaemon(t, identity.Version)
}

// TestUpdateInterrupted sends SIGTERM to wdx update as it stops the daemon:
// the switch finishes and the new version runs. Its leftovers from earlier
// runs cut short are swept, or warned about when they may be a backup.
func TestUpdateInterrupted(t *testing.T) {
	t.Parallel()
	base, _ := publish(t, releaseVersion, releaseVersion, map[string]string{releaseVersion: releaseBinary})
	in := install(t, base)
	if _, exit := in.json(t, "daemon", "start"); exit != api.ExitOK {
		t.Fatal("the daemon did not start")
	}
	download := filepath.Join(in.home, "update-123")
	replaced := filepath.Join(in.home, "replaced-456")
	writeFile(t, filepath.Join(download, "wdx"), "")
	writeFile(t, filepath.Join(replaced, "wdx"), "")

	c := exec.Command(in.binary, "update", "--apply")
	c.Env = append(os.Environ(), identity.Env("HOME")+"="+in.home)
	var stdout bytes.Buffer
	c.Stdout = &stdout
	stderr, err := c.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	var said strings.Builder
	lines := bufio.NewScanner(stderr)
	for lines.Scan() {
		said.WriteString(lines.Text() + "\n")
		if strings.Contains(lines.Text(), "stopping the daemon") {
			if err := c.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := c.Wait(); err != nil || !strings.Contains(stdout.String(), "updated") {
		t.Fatalf("want the update finished, got %v:\n%s%s", err, stdout.String(), said.String())
	}
	if !strings.Contains(said.String(), "the switch finishes first") {
		t.Fatalf("want the signal said to be held, got\n%s", said.String())
	}
	if !strings.Contains(said.String(), replaced) {
		t.Fatalf("want the leftover %s named, got\n%s", replaced, said.String())
	}
	in.wantDaemon(t, releaseVersion)
	if _, err := os.Stat(download); !os.IsNotExist(err) {
		t.Fatalf("want the stale download removed, got %v", err)
	}
	if _, err := os.Stat(replaced); err != nil {
		t.Fatalf("want the stale backup kept, got %v", err)
	}
}

// TestUpdateRollbackAfterReinstall rolls back once another install
// replaced what the update installed: refused, as the version kept is the
// one that update replaced, not the installed one.
func TestUpdateRollbackAfterReinstall(t *testing.T) {
	t.Parallel()
	base, _ := publish(t, releaseVersion, releaseVersion, map[string]string{releaseVersion: releaseBinary})
	in := install(t, base)
	if v, exit := in.json(t, "update", "--apply"); exit != api.ExitOK || v.Result != "updated" {
		t.Fatalf("want updated, got exit %d, %+v", exit, v)
	}
	// Renamed into place, as install.sh does: the daemon runs the other.
	copyBinary(t, in.binary+".new", wdxBinary)
	if err := os.Rename(in.binary+".new", in.binary); err != nil {
		t.Fatal(err)
	}

	v, exit := in.json(t, "update", "--rollback", "--cancel")
	wantError(t, v, exit, api.ErrorCodeNoPreviousVersion, api.ExitRejected)
	wantHint(t, v, "another install")
	in.wantInstalled(t, wdxBinary)
	if _, err := os.Stat(identity.PreviousDir(in.home)); err != nil {
		t.Fatalf("want the kept version left as it is, got %v", err)
	}
}

// TestUpdateUndoneStopsItsDaemon installs a release whose daemon starts but
// answers as another version: undoing the update stops it before putting
// the old version back, which then answers.
func TestUpdateUndoneStopsItsDaemon(t *testing.T) {
	t.Parallel()
	wrong := filepath.Join(t.TempDir(), identity.Name)
	writeFile(t, wrong, strings.ReplaceAll(brokenRelease, `*) echo "broken release"`, `daemon) exec `+releaseBinary+` "$@" ;;
*) echo "broken release"`))
	base, _ := publish(t, "0.3.0", "0.3.0", map[string]string{"0.3.0": wrong})
	in := install(t, base)
	if _, exit := in.json(t, "daemon", "start"); exit != api.ExitOK {
		t.Fatal("the daemon did not start")
	}
	newProject(t, in.home, "before")

	v, exit := in.json(t, "update", "--apply")
	wantError(t, v, exit, api.ErrorCodeUpdateFailed, api.ExitSystem)
	if v.Details["step"] != "start" || v.Details["undone"] != true || v.Details["version"] != identity.Version {
		t.Fatalf("want the start undone, got %+v", v.Details)
	}
	in.wantInstalled(t, wdxBinary)
	in.wantDaemon(t, identity.Version)
	in.wantProjects(t, "before")
}

// TestDaemonStartGivesUp starts a daemon that never answers: start gives
// up on it and kills it, not leaving it running behind its failure.
func TestDaemonStartGivesUp(t *testing.T) {
	t.Parallel()
	home := shortHome(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	mute := filepath.Join(t.TempDir(), identity.Name)
	writeFile(t, mute, "#!/bin/sh\necho $$ >"+pidFile+"\nexec sleep 60\n")
	out := wdxWith(t, home, func(env *Env) {
		env.Executable = mute
		env.Timeouts.Start = time.Second
	}, "daemon", "start", "--json")
	if out.exit != api.ExitSystem {
		t.Fatalf("want start to fail, got %+v", out)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(readFile(t, pidFile)))
	if err != nil {
		t.Fatal(err)
	}
	if syscall.Kill(pid, 0) == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("want the daemon that did not answer killed, pid %d runs", pid)
	}
}

func TestUpdateSettingsInvalid(t *testing.T) {
	t.Parallel()
	picky := filepath.Join(t.TempDir(), identity.Name)
	writeFile(t, picky, pickyRelease)
	base, _ := publish(t, "0.3.0", "0.3.0", map[string]string{"0.3.0": picky})
	in := install(t, base)
	settings := readFile(t, identity.ConfigPath(in.home))

	v, exit := in.json(t, "update", "--apply")
	wantError(t, v, exit, api.ErrorCodeSettingsInvalid, api.ExitRejected)
	if v.Details["version"] != "0.3.0" || v.Details["settings"] == nil {
		t.Fatalf("want the release and its problems, got %+v", v.Details)
	}
	in.wantInstalled(t, wdxBinary)
	if got := readFile(t, identity.ConfigPath(in.home)); got != settings {
		t.Fatalf("want the settings left alone, got\n%s", got)
	}
}

func TestUpdateChecksumMismatch(t *testing.T) {
	t.Parallel()
	base, dir := publish(t, releaseVersion, releaseVersion, map[string]string{releaseVersion: releaseBinary})
	sums := filepath.Join(dir, "v"+releaseVersion, "checksums.txt")
	b, _ := os.ReadFile(sums)
	// Any other first digit breaks it.
	b[0] = map[bool]byte{true: '1', false: '0'}[b[0] == '0']
	writeFile(t, sums, string(b))
	in := install(t, base)

	v, exit := in.json(t, "update", "--apply")
	wantError(t, v, exit, api.ErrorCodeChecksumMismatch, api.ExitSystem)
	in.wantInstalled(t, wdxBinary)
}

func TestUpdateRunning(t *testing.T) {
	t.Parallel()
	base, _ := publish(t, releaseVersion, releaseVersion, map[string]string{releaseVersion: releaseBinary})
	in := install(t, base)
	f, err := os.OpenFile(identity.UpdateLockPath(in.home), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{"--apply"}, {"--rollback", "--cancel"}} {
		v, exit := in.json(t, append([]string{"update"}, args...)...)
		wantError(t, v, exit, api.ErrorCodeUpdateRunning, api.ExitRejected)
	}
	in.wantInstalled(t, wdxBinary)
}

func TestUpdateText(t *testing.T) {
	t.Parallel()
	base, _ := publish(t, releaseVersion, releaseVersion, map[string]string{releaseVersion: releaseBinary})
	in := install(t, base)
	out := wdxWith(t, in.home, in.env, "update", "--apply")
	if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "updated wdx dev -> 0.2.0\n") ||
		!strings.Contains(out.stderr, "==> downloading wdx 0.2.0\n") || !strings.Contains(out.stderr, "==> starting the daemon\n") {
		t.Fatalf("unexpected text: %+v", out)
	}
	if out = in.runText(t, "update", "--rollback"); out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "rolled back wdx 0.2.0 -> dev\n") || !strings.Contains(out.stdout, "database: kept") {
		t.Fatalf("unexpected text: %+v", out)
	}
}
