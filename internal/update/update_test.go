package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/sqlite"
)

func TestMain(m *testing.M) { os.Exit(runtest.Main(m)) }

// release is a tool that says it is version 0.9.0, which preflight checks,
// and writes the settings of its home, TOOL_HOME, unchanged to the file
// after --to.
const release = `#!/bin/sh
if [ "$1" = settings ]; then
	cp "$TOOL_HOME/tool.yaml" "$5"
	echo '{"result": "written", "settings": {"added": [], "dropped": []}}'
	exit
fi
echo '{"cli": {"name": "tool", "version": "0.9.0", "commit": "none"}, "daemon": null}'
`

// toolProgram is a program that is not wdx: another name, its database
// and config file under other names in home, its binary and the previous
// version kept outside it, and its home in another variable.
func toolProgram(t *testing.T, home string) Program {
	t.Helper()
	installed := t.TempDir()
	return Program{
		Name:     "tool",
		Binary:   filepath.Join(installed, "bin", "tool"),
		DB:       filepath.Join(home, "tool-data.db"),
		Config:   filepath.Join(home, "tool.yaml"),
		Previous: filepath.Join(installed, "lib", "previous"),
		Known:    1,
		Command: func(ctx context.Context, binary, home string, args ...string) *exec.Cmd {
			c := exec.CommandContext(ctx, binary, args...)
			c.Env = append(os.Environ(), "TOOL_HOME="+home)
			return c
		},
		GitHub: GitHub{Releases: "https://example.invalid/tool/releases"},
	}
}

// applying returns the options of an update of toolProgram from version
// 0.1.0 to the release, in a home of its own, with a daemon that answers as
// the version installed.
func applying(t *testing.T) Options {
	t.Helper()
	base := t.TempDir()
	home := t.TempDir()
	p := toolProgram(t, home)
	name := newReleases(p, "").archive("0.9.0")
	content := archiveOf(t, map[string]string{p.Name: release})
	sum := sha256.Sum256(content)
	for path, data := range map[string]string{
		filepath.Join(base, "LATEST"):                  "v0.9.0\n",
		filepath.Join(base, "v0.9.0", name):            string(content),
		filepath.Join(base, "v0.9.0", "checksums.txt"): hex.EncodeToString(sum[:]) + "  " + name + "\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(p.Binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runtest.WriteExecutable(p.Binary, "#!/bin/sh\necho old\n"); err != nil {
		t.Fatal(err)
	}
	o := Options{Program: p, Home: home, Current: "0.1.0", BaseURL: "file://" + base}
	o.Daemon = Daemon{
		Stop:  func(context.Context) error { return nil },
		Start: func(context.Context) (string, error) { return installedVersion(t, o.Program.Binary), nil },
	}
	return o
}

// installedVersion is the version the installed binary says it is, as the
// daemon it starts would answer.
func installedVersion(t *testing.T, binary string) string {
	t.Helper()
	b, err := os.ReadFile(binary)
	if err != nil {
		t.Error(err)
		return ""
	}
	if strings.Contains(string(b), `"version": "0.9.0"`) {
		return "0.9.0"
	}
	return "0.1.0"
}

func installed(t *testing.T, o Options) string {
	t.Helper()
	b, err := os.ReadFile(o.Program.Binary)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// withData gives the home of o a database of the schema version the
// program knows, and a config file.
func withData(t *testing.T, o Options) {
	t.Helper()
	db, err := sqlite.Open(context.Background(), o.Program.DB, sqlite.Migrations{{Name: "001_init.sql", Body: "CREATE TABLE things (id TEXT PRIMARY KEY) STRICT;"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(o.Program.Config, []byte("color: never\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestApplyRollBackProgram updates a program that is not wdx and rolls it
// back: every file it keeps and puts back is the program's, at its paths.
func TestApplyRollBackProgram(t *testing.T) {
	t.Parallel()
	o := applying(t)
	withData(t, o)
	old := installed(t, o)

	r, err := Apply(context.Background(), o, "stable", "")
	if err != nil || r.From != "0.1.0" || r.To != "0.9.0" || r.SchemaFrom != 1 || r.SchemaTo != 1 {
		t.Fatalf("want updated from 0.1.0 to 0.9.0 on schema 1, got %+v, %v", r, err)
	}
	if got := installed(t, o); got != release {
		t.Fatalf("want the release installed, got\n%s", got)
	}
	for _, kept := range []string{"tool", "tool-data.db", "config.yaml", "previous.json"} {
		if _, err := os.Stat(filepath.Join(o.Program.Previous, kept)); err != nil {
			t.Errorf("want %s kept in the previous version: %v", kept, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(o.Program.Previous, "previous.json"))
	if err != nil || !strings.Contains(string(b), `"version":"0.1.0","schema":1,"updated_to":"0.9.0"`) {
		t.Fatalf("want previous.json to say 0.1.0 on schema 1 was replaced by 0.9.0, got %s, %v", b, err)
	}
	entries, err := os.ReadDir(o.Home)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "tool") && e.Name() != "update.lock" {
			t.Errorf("want the home to hold only the program's files and the update lock, got %s", e.Name())
		}
	}

	o.Current = "0.9.0"
	r, err = Rollback(context.Background(), o, false)
	if err != nil || r.To != "0.1.0" || r.DataRestored {
		t.Fatalf("want rolled back to 0.1.0 keeping the database, got %+v, %v", r, err)
	}
	if got := installed(t, o); got != old {
		t.Fatalf("want the old binary back, got\n%s", got)
	}
	if _, err := os.Stat(o.Program.Previous); !os.IsNotExist(err) {
		t.Fatalf("want the previous version gone after the rollback, got %v", err)
	}
}

// TestApplyUndoneProgram puts the program's files back when the new
// version does not start.
func TestApplyUndoneProgram(t *testing.T) {
	t.Parallel()
	o := applying(t)
	withData(t, o)
	old := installed(t, o)
	start := o.Daemon.Start
	o.Daemon.Start = func(ctx context.Context) (string, error) {
		if installed(t, o) == release {
			return "", errors.New("it does not answer")
		}
		return start(ctx)
	}

	_, err := Apply(context.Background(), o, "stable", "")
	failed, ok := errors.AsType[*FailedError](err)
	if !ok || failed.Step != "start" || failed.UndoErr != nil || failed.Version != "0.1.0" {
		t.Fatalf("want the failed start undone, got %v", err)
	}
	if got := installed(t, o); got != old {
		t.Fatalf("want the old binary back, got\n%s", got)
	}
	if s, err := sqlite.ReadSchema(context.Background(), o.Program.DB); err != nil || s.Version != 1 {
		t.Fatalf("want the database back, got %+v, %v", s, err)
	}
	if b, err := os.ReadFile(o.Program.Config); err != nil || string(b) != "color: never\n" {
		t.Fatalf("want the config file back, got %q, %v", b, err)
	}
	if _, err := os.Stat(o.Program.Previous); !os.IsNotExist(err) {
		t.Fatalf("want no previous version kept, got %v", err)
	}
}

// TestApplyStagingOwnerBackup updates a program as wdx-hub's is: its lock and
// downloads outside its home, its files given to its user, its database
// backed up before the switch. What the update leaves in the home is the
// program's files alone.
func TestApplyStagingOwnerBackup(t *testing.T) {
	t.Parallel()
	o := applying(t)
	withData(t, o)
	o.Staging = filepath.Join(t.TempDir(), "lib")
	o.Program.Previous = filepath.Join(o.Staging, "previous")
	o.Program.Owner = &Owner{UID: os.Getuid(), GID: os.Getgid()}
	var order []string
	o.Backup = func(context.Context) error {
		order = append(order, "backup")
		return nil
	}
	stop := o.Daemon.Stop
	o.Daemon.Stop = func(ctx context.Context) error {
		order = append(order, "stop")
		return stop(ctx)
	}

	if _, err := Apply(context.Background(), o, "stable", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "backup,stop" {
		t.Fatalf("want the backup before the daemon stops, got %v", order)
	}
	entries, err := os.ReadDir(o.Home)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "tool") {
			t.Errorf("want the home to hold only the program's files, got %s", e.Name())
		}
	}
	for _, path := range []string{filepath.Join(o.Staging, "update.lock"), filepath.Join(o.Program.Previous, "previous.json")} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("want %s in the staging directory: %v", path, err)
		}
	}
}

// TestApplyBackupFails ends the update before the switch when the database
// cannot be backed up: nothing changes.
func TestApplyBackupFails(t *testing.T) {
	t.Parallel()
	o := applying(t)
	withData(t, o)
	old := installed(t, o)
	o.Backup = func(context.Context) error { return errors.New("no room") }
	o.Daemon.Stop = func(context.Context) error {
		t.Error("the daemon was stopped")
		return nil
	}

	_, err := Apply(context.Background(), o, "stable", "")
	if failed, ok := errors.AsType[*FailedError](err); !ok || failed.Step != "back up" || failed.UndoErr != nil {
		t.Fatalf("want the backup failed, got %v", err)
	}
	if got := installed(t, o); got != old {
		t.Fatalf("want the old binary left, got\n%s", got)
	}
}

// TestLockReleasedWhileForked unlocks while a copy of the lock's file is
// open, as in a child another goroutine forked that has not exec'd yet: the
// next update must still get the lock.
func TestLockReleasedWhileForked(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	f, err := lockFile(home)
	if err != nil {
		t.Fatal(err)
	}
	// What a fork does with every open file: the copy shares the lock.
	forked, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(forked)
	unlockFile(f)
	unlock, err := lock(home)
	if err != nil {
		t.Fatalf("lock after the unlock, its file still open in a fork: %v", err)
	}
	unlock()
}
