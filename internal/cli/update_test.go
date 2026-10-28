package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/daemon"
	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/identity"
)

// brokenRelease says it is version 0.3.0 but cannot run the daemon.
const brokenRelease = `#!/bin/sh
case "$1" in
version) echo '{"cli": {"name": "wdx", "version": "0.3.0", "commit": "none"}, "daemon": null}' ;;
*) echo "broken release" >&2; exit 1 ;;
esac
`

// publish lays out binaries, by version, as releases the way
// scripts/dist.sh does, with latest as the newest stable release and
// latestDev as the newest of either kind, and returns their file:// URL.
func publish(t *testing.T, latest, latestDev string, binaries map[string]string) (base, dir string) {
	t.Helper()
	dir = t.TempDir()
	for version, binary := range binaries {
		b, err := os.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
		var archive bytes.Buffer
		// Compressing the binary under the race detector takes seconds.
		gz, _ := gzip.NewWriterLevel(&archive, gzip.NoCompression)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&tar.Header{Name: identity.Name, Mode: 0o755, Size: int64(len(b))})
		tw.Write(b)
		tw.Close()
		gz.Close()
		name := fmt.Sprintf("%s_%s_%s_%s.tar.gz", identity.Name, version, runtime.GOOS, runtime.GOARCH)
		sum := sha256.Sum256(archive.Bytes())
		writeFile(t, filepath.Join(dir, "v"+version, name), archive.String())
		writeFile(t, filepath.Join(dir, "v"+version, "checksums.txt"), hex.EncodeToString(sum[:])+"  "+name+"\n")
	}
	writeFile(t, filepath.Join(dir, "LATEST"), "v"+latest+"\n")
	writeFile(t, filepath.Join(dir, "LATEST-dev"), "v"+latestDev+"\n")
	return "file://" + dir, dir
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
			syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	// The installed binaries read it when run on their own.
	writeFile(t, identity.ConfigPath(home), "otel:\n  endpoint: \"\"\nupdate:\n  base_url: "+base+"\n")
	binary := filepath.Join(t.TempDir(), identity.Name)
	b, err := os.ReadFile(wdxBinary)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, binary, string(b))
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

// wantInstalled checks that the installed binary is binary.
func (in installation) wantInstalled(t *testing.T, binary string) {
	t.Helper()
	got, err := os.ReadFile(in.binary)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
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

	v, exit := in.json(t, "update", "--apply")
	if exit != api.ExitOK || v.Result != "updated" || v.Update.From != identity.Version || v.Update.To != releaseVersion ||
		*v.Update.Channel != "stable" || v.Update.SchemaFrom != db.Known() || v.Update.SchemaTo != db.Known() || v.Update.DataRestored != nil {
		t.Fatalf("want updated to %s, got exit %d, %+v", releaseVersion, exit, v.Update)
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

	v, exit = in.json(t, "update", "--rollback")
	if exit != api.ExitOK || v.Result != "rolled_back" || v.Update.To != identity.Version || v.Update.Channel != nil || *v.Update.DataRestored {
		t.Fatalf("want rolled back keeping the database, got exit %d, %+v", exit, v.Update)
	}
	in.wantInstalled(t, wdxBinary)
	in.wantDaemon(t, identity.Version)
	in.wantProjects(t, "after", "before")
	if _, err := os.Stat(identity.PreviousDir(in.home)); !os.IsNotExist(err) {
		t.Fatalf("want nothing kept after the rollback, got %v", err)
	}

	v, exit = in.json(t, "update", "--rollback")
	wantError(t, v, exit, api.ErrorCodeNoPreviousVersion, api.ExitRejected)
}

// TestUpdateRollbackRestores rolls back across a migration that breaks the
// previous version, which only the database backup can serve.
func TestUpdateRollbackRestores(t *testing.T) {
	t.Parallel()
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

	v, exit := in.json(t, "update", "--rollback")
	wantError(t, v, exit, api.ErrorCodeRestoreRequired, api.ExitRejected)
	wantHint(t, v, "--discard-changes")
	if known, _ := v.Details["known"].(float64); int(known) != db.Known() {
		t.Fatalf("want the schema the previous version knows in details, got %+v", v.Details)
	}
	in.wantInstalled(t, releaseBinary)

	out := wdxWith(t, in.home, in.env, "update", "--rollback")
	if out.exit != api.ExitRejected || !strings.Contains(out.stderr, "hint: pass --discard-changes") {
		t.Fatalf("want the restore refused without a terminal, got %+v", out)
	}

	v, exit = in.json(t, "update", "--rollback", "--discard-changes")
	if exit != api.ExitOK || v.Result != "rolled_back" || !*v.Update.DataRestored || v.Update.SchemaFrom != breaking || v.Update.SchemaTo != db.Known() {
		t.Fatalf("want rolled back restoring the database, got exit %d, %+v", exit, v.Update)
	}
	in.wantInstalled(t, wdxBinary)
	in.wantDaemon(t, identity.Version)
	in.wantProjects(t, "before")
}

// TestUpdateUndone updates to a release whose daemon does not start: the
// old binary and database are put back and serve again.
func TestUpdateUndone(t *testing.T) {
	t.Parallel()
	broken := filepath.Join(t.TempDir(), identity.Name)
	writeFile(t, broken, brokenRelease)
	base, _ := publish(t, "0.3.0", "0.3.0", map[string]string{"0.3.0": broken})
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
	if _, err := os.Stat(identity.PreviousDir(in.home)); !os.IsNotExist(err) {
		t.Fatalf("want nothing kept after a failed update, got %v", err)
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

	for _, flag := range []string{"--apply", "--rollback"} {
		v, exit := in.json(t, "update", flag)
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
	if out = wdxWith(t, in.home, in.env, "update", "--rollback"); out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "rolled back wdx dev -> dev\n") || !strings.Contains(out.stdout, "database: kept") {
		t.Fatalf("unexpected text: %+v", out)
	}
}
