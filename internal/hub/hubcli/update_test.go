package hubcli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/clikit/clikittest"
	"github.com/srjn45/wdx/internal/hub/account"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/hub/hubdb"
	"github.com/srjn45/wdx/internal/hub/server"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/sqlite/sqlitetest"
	"github.com/srjn45/wdx/internal/waittest"
)

// newVersion is the release the update tests install.
const newVersion = "0.6.0"

// release is a wdx-hub of version %[1]s, as an update runs it: it writes the
// settings of its home anew unchanged, says its version and prints its unit,
// unitOf where it is installed, and completions.
const release = `#!/bin/sh
case "$1 $2" in
"settings write") cp "$WDX_HUB_HOME/config.yaml" "$5"
	echo '{"result": "written", "settings": {"path": "'"$5"'", "added": [], "dropped": []}}' ;;
"version --json") echo '{"cli": {"name": "wdx-hub", "version": "%[1]s", "commit": "none"}, "hub": null}' ;;
"service unit") printf '{"unit": "[Service]\\nExecStart=%%s serve\\nUser=tester\\n# unit of %[1]s\\n"}' "$0" ;;
"completion "*) echo "$2 completion of %[1]s" ;;
*) echo "release %[1]s: $*" >&2; exit 1 ;;
esac
`

// unitOf is the unit release prints as version, installed as binary.
func unitOf(binary, version string) string {
	return "[Service]\nExecStart=" + binary + " serve\nUser=tester\n# unit of " + version + "\n"
}

// releaseOf is release as version.
func releaseOf(version string) string { return fmt.Sprintf(release, version) }

// brokenRelease runs on the settings but cannot be started: it prints no
// unit.
var brokenRelease = strings.Replace(releaseOf(newVersion), `"service unit")`, `"service unit") exit 1; `, 1)

// pickyRelease does not accept the settings.
const pickyRelease = `#!/bin/sh
echo '{"code": "usage", "detail": "invalid settings: registration: not here", "details": {"settings": [{"key": "registration", "from": "config.yaml:1", "value": "closed", "reason": "not here"}]}}'
exit 2
`

// publishHub lays out releases, scripts by version, as scripts/dist.sh does,
// with newVersion the newest on both channels, and returns their file:// URL.
func publishHub(t *testing.T, scripts map[string]string) (base, dir string) {
	t.Helper()
	dir = t.TempDir()
	for version, script := range scripts {
		archive := fmt.Sprintf("%s_%s_%s_%s.tar.gz", name, version, runtime.GOOS, runtime.GOARCH)
		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		tw := tar.NewWriter(gz)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(script))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(script)); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b.Bytes())
		writeTestFile(t, filepath.Join(dir, "v"+version, archive), b.String())
		writeTestFile(t, filepath.Join(dir, "v"+version, "checksums.txt"), hex.EncodeToString(sum[:])+"  "+archive+"\n")
	}
	writeTestFile(t, filepath.Join(dir, "LATEST"), "v"+newVersion+"\n")
	writeTestFile(t, filepath.Join(dir, "LATEST-dev"), "v"+newVersion+"\n")
	return "file://" + dir, dir
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runtest.WriteExecutable(path, content); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// installedHub is a hub installed as install-hub.sh installs one, in a
// layout of the test's own, run as the test's user, with a service that
// serves the home in this process as the version its binary says it is.
type installedHub struct {
	home    string
	layout  hubconfig.Layout
	account hubconfig.Account
	base    string
	service *testService
}

// installHub installs the hub of this tree's version, whose releases are at
// base, its service stopped.
func installHub(t *testing.T, base string) *installedHub {
	t.Helper()
	home := newHome(t)
	sqlitetest.Seed(t, hubconfig.DBPath(home), hubdb.Open)
	lib := t.TempDir()
	in := &installedHub{
		home: home,
		layout: hubconfig.Layout{
			User:   "tester",
			Binary: filepath.Join(lib, "bin", name),
			Unit:   filepath.Join(lib, "systemd", hubconfig.Service),
			Lib:    filepath.Join(lib, "lib", name),
			Completions: []hubconfig.Completion{
				{Shell: "bash", Path: filepath.Join(lib, "bash-completion", name)},
				{Shell: "zsh", Path: filepath.Join(lib, "site-functions", "_"+name)},
				{Shell: "fish", Path: filepath.Join(lib, "vendor_completions.d", name+".fish")},
			},
		},
		base: base,
	}
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	in.account = hubconfig.Account{Name: "tester", UID: os.Getuid(), GID: os.Getgid(), Groups: groups, Home: t.TempDir()}
	writeTestFile(t, in.layout.Binary, releaseOf(identity.Version))
	writeTestFile(t, in.layout.Unit, unitOf(in.layout.Binary, identity.Version))
	in.service = &testService{t: t, home: home, binary: in.layout.Binary}
	t.Cleanup(in.service.halt)
	return in
}

// env makes a run root's, updating this installation, as a hub of version.
func (in *installedHub) env(version string) func(*Env) {
	return func(env *Env) {
		env.Root = true
		env.Account = &in.account
		env.Update = UpdateOptions{Layout: &in.layout, BaseURL: in.base, Version: version, Systemctl: in.service.systemctl, LookPath: noFish}
	}
}

// json runs update with args and --json as root on a hub of version.
func (in *installedHub) json(t *testing.T, version string, args ...string) (map[string]any, int) {
	t.Helper()
	return hubJSONWith(t, in.home, in.env(version), append([]string{"update"}, args...)...)
}

// start starts the service, as it runs before an update.
func (in *installedHub) start(t *testing.T) {
	t.Helper()
	if err := in.service.systemctl(context.Background(), "start", hubconfig.Service); err != nil {
		t.Fatal(err)
	}
}

// want checks that the binary and the unit of version are installed, and
// the hub answers as version.
func (in *installedHub) want(t *testing.T, version string) {
	t.Helper()
	if got := readTestFile(t, in.layout.Binary); got != releaseOf(version) {
		t.Errorf("want %s installed, got\n%s", version, got)
	}
	if got := readTestFile(t, in.layout.Unit); got != unitOf(in.layout.Binary, version) {
		t.Errorf("want the unit of %s, got %q", version, got)
	}
	v, exit := hubJSON(t, in.home, "version")
	h, _ := v["hub"].(map[string]any)
	if exit != clikit.ExitOK || h == nil || h["version"] != version {
		t.Fatalf("want the hub answering as %s, got %v", version, v)
	}
}

// noFish finds bash and zsh, and no fish.
func noFish(file string) (string, error) {
	if file == "fish" {
		return "", exec.ErrNotFound
	}
	return "/bin/" + file, nil
}

// testService is the hub's service: systemctl start serves the home in this
// process as the version the installed binary says it is.
type testService struct {
	t            *testing.T
	home, binary string
	mu           sync.Mutex
	stop         func() // nil while stopped
	calls        []string
}

func (s *testService) systemctl(_ context.Context, args ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, strings.Join(args, " "))
	switch args[0] {
	case "stop":
		s.halted()
	case "start":
		if s.stop != nil {
			return nil
		}
		out, err := output(func() *exec.Cmd { return exec.Command(s.binary, "version", "--json") })
		var v versionResponse
		if err == nil {
			err = json.Unmarshal(out, &v)
		}
		if err != nil {
			return fmt.Errorf("the service does not start: %w", err)
		}
		return s.serve(v.CLI.Version)
	}
	return nil
}

// serve serves the home as version until halted.
func (s *testService) serve(version string) error {
	logs := logging.NewDailyFile(hubconfig.LogsDir(s.home), name, 7, time.Now)
	getenv := homeVars(s.home)
	cfg, err := hubconfig.Load(getenv)
	if err != nil {
		logs.Close()
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv, err := server.Listen(ctx, server.Options{
		Config:   cfg,
		Log:      logging.New(logs, slog.LevelDebug).With("process", "hub"),
		Reload:   func() (hubconfig.Config, error) { return hubconfig.Load(getenv) },
		Settings: settingStore{getenv: getenv},
		Argon2:   account.Argon2{Memory: 8, Time: 1, Threads: 1},
		Version:  version,
	})
	if err != nil {
		cancel()
		logs.Close()
		return err
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx) }()
	s.stop = func() {
		cancel()
		if err := <-served; err != nil {
			s.t.Errorf("hub: %v", err)
		}
		logs.Close()
	}
	return nil
}

func (s *testService) halted() {
	if s.stop != nil {
		s.stop()
		s.stop = nil
	}
}

func (s *testService) halt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.halted()
}

func (s *testService) called() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

// hubJSONWith is hubJSON in an environment adjust changes.
func hubJSONWith(t *testing.T, home string, adjust func(*Env), args ...string) (map[string]any, int) {
	t.Helper()
	args = append(args, "--json")
	out := hubWith(t, home, adjust, args...)
	var v map[string]any
	if err := json.Unmarshal(clikittest.JSON(t, args, out, codes), &v); err != nil {
		t.Fatal(err)
	}
	code, _ := v["code"].(string)
	result, _ := v["result"].(string)
	clikittest.WantDeclared(t, newRoot(), args, result, code)
	return v, out.Exit
}

// wantCode fails unless the run failed with code and exit.
func wantCode(t *testing.T, v map[string]any, exit int, code hubapi.ErrorCode, wantExit int) {
	t.Helper()
	if exit != wantExit || v["code"] != string(code) {
		t.Fatalf("want %s with exit %d, got exit %d: %v", code, wantExit, exit, v)
	}
}

func updateOf(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	u, ok := v["update"].(map[string]any)
	if !ok {
		t.Fatalf("no update in %v", v)
	}
	return u
}

func TestUpdateCheck(t *testing.T) {
	t.Parallel()
	base, _ := publishHub(t, map[string]string{newVersion: releaseOf(newVersion)})
	in := installHub(t, base)
	// Any user checks, on any hub.
	check := func(env *Env) { env.Update = UpdateOptions{BaseURL: in.base} }

	v, exit := hubJSONWith(t, in.home, check, "update")
	u := updateOf(t, v)
	if exit != clikit.ExitOK || u["current"] != identity.Version || u["latest"] != newVersion || u["channel"] != "stable" || u["available"] != true {
		t.Fatalf("want %s available, got exit %d: %v", newVersion, exit, v)
	}
	if v, _ := hubJSONWith(t, in.home, check, "update", "--channel", "dev"); updateOf(t, v)["channel"] != "dev" {
		t.Fatalf("want the dev channel, got %v", v)
	}
	v, exit = hubJSONWith(t, in.home, check, "update", "--version", "9.9.9")
	wantCode(t, v, exit, hubapi.ErrorCodeDownloadFailed, clikit.ExitSystem)

	out := hubWith(t, in.home, check, "update")
	if out.Exit != clikit.ExitOK || out.Stdout != "update available: wdx-hub dev -> "+newVersion+" (stable channel)\n  run sudo wdx-hub update --apply\n" {
		t.Fatalf("unexpected text: %+v", out)
	}
}

func TestUpdateUsage(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	for _, args := range [][]string{
		{"update", "--channel", "beta"},
		{"update", "--discard-changes"},
		{"update", "--apply", "--rollback"},
		{"update", "--rollback", "--version", "0.2.0"},
		{"update", "--cancel"},
		{"update", "--rollback"},
		{"update", "--rollback", "--discard-changes", "--cancel"},
	} {
		v, exit := hubJSON(t, home, args...)
		wantCode(t, v, exit, hubapi.ErrorCodeUsage, clikit.ExitUsage)
	}
}

// TestUpdateNotInstalled refuses to update a hub not installed as
// install-hub.sh installs it, or whose user is missing.
func TestUpdateNotInstalled(t *testing.T) {
	t.Parallel()
	base, _ := publishHub(t, map[string]string{newVersion: releaseOf(newVersion)})
	in := installHub(t, base)
	elsewhere := in.layout
	elsewhere.Unit = filepath.Join(t.TempDir(), hubconfig.Service)
	for _, args := range [][]string{{"update", "--apply"}, {"update", "--rollback", "--cancel"}} {
		v, exit := hubJSONWith(t, in.home, func(env *Env) {
			in.env(identity.Version)(env)
			env.Update.Layout = &elsewhere
		}, args...)
		wantCode(t, v, exit, hubapi.ErrorCodeNotInstalled, clikit.ExitUsage)
	}
	v, exit := hubJSONWith(t, in.home, func(env *Env) {
		in.env(identity.Version)(env)
		env.Account = nil
	}, "update", "--apply")
	wantCode(t, v, exit, hubapi.ErrorCodeNotInstalled, clikit.ExitUsage)
	if !strings.Contains(v["detail"].(string), "its user tester") {
		t.Fatalf("want the missing user named, got %v", v)
	}
	if calls := in.service.called(); len(calls) != 0 {
		t.Fatalf("want the service untouched, got %v", calls)
	}
}

func TestUpdateRootRequired(t *testing.T) {
	t.Parallel()
	base, _ := publishHub(t, map[string]string{newVersion: releaseOf(newVersion)})
	in := installHub(t, base)
	in.start(t)
	for _, flag := range []string{"apply", "rollback"} {
		args := []string{"update", "--" + flag}
		if flag == "rollback" {
			args = append(args, "--cancel")
		}
		v, exit := hubJSONWith(t, in.home, func(env *Env) {
			in.env(identity.Version)(env)
			env.Root = false
		}, args...)
		wantCode(t, v, exit, hubapi.ErrorCodeRootRequired, clikit.ExitUsage)
		if v["hint"] != "run sudo wdx-hub update --"+flag {
			t.Fatalf("want sudo hinted, got %v", v["hint"])
		}
	}
	in.want(t, identity.Version)
}

// TestUpdateApplyAndRollback updates the running hub to a release and rolls
// back to the version it replaced, the database staying as it is.
func TestUpdateApplyAndRollback(t *testing.T) {
	t.Parallel()
	base, _ := publishHub(t, map[string]string{newVersion: releaseOf(newVersion), "0.1.0": releaseOf("0.1.0")})
	in := installHub(t, base)
	in.start(t)
	settings := readTestFile(t, hubconfig.ConfigPath(in.home))

	v, exit := in.json(t, identity.Version, "--apply")
	u := updateOf(t, v)
	if exit != clikit.ExitOK || v["result"] != resultUpdated || u["from"] != identity.Version || u["to"] != newVersion || u["channel"] != "stable" ||
		u["schema_from"] != float64(hubdb.Known()) || u["schema_to"] != float64(hubdb.Known()) || u["data_restored"] != nil {
		t.Fatalf("want updated to %s, got exit %d: %v", newVersion, exit, v)
	}
	if w, ok := v["warnings"].([]any); !ok || len(w) != 0 {
		t.Fatalf("want no warnings, got %v", v["warnings"])
	}
	in.want(t, newVersion)
	if got := strings.Join(in.service.called(), ","); got != "start wdx-hub.service,stop wdx-hub.service,daemon-reload,reset-failed wdx-hub.service,start wdx-hub.service" {
		t.Fatalf("want the service stopped, reloaded, reset and started, got %s", got)
	}
	if _, err := os.Stat(filepath.Join(hubconfig.BackupsDir(in.home), "hub-"+identity.Version+".db")); err != nil {
		t.Fatalf("want the database backed up, got %v", err)
	}
	for _, kept := range []string{name, "hub.db", "config.yaml", "previous.json"} {
		if _, err := os.Stat(filepath.Join(in.layout.Previous(), kept)); err != nil {
			t.Errorf("want %s kept for a rollback: %v", kept, err)
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(in.home, "update*")); len(matches) != 0 {
		t.Fatalf("want nothing of the update's in the home, got %v", matches)
	}

	if v, exit := in.json(t, newVersion, "--apply"); exit != clikit.ExitOK || v["result"] != resultUpToDate {
		t.Fatalf("want up to date, got exit %d: %v", exit, v)
	}
	v, exit = in.json(t, newVersion, "--apply", "--version", "0.1.0")
	wantCode(t, v, exit, hubapi.ErrorCodeOlderVersion, clikit.ExitRejected)

	v, exit = in.json(t, newVersion, "--rollback", "--cancel")
	u = updateOf(t, v)
	if exit != clikit.ExitOK || v["result"] != resultRolledBack || u["to"] != identity.Version || u["channel"] != nil || u["data_restored"] != false {
		t.Fatalf("want rolled back keeping the database, got exit %d: %v", exit, v)
	}
	in.want(t, identity.Version)
	if got := readTestFile(t, hubconfig.ConfigPath(in.home)); got != settings {
		t.Fatalf("want the settings from before the update, got\n%s", got)
	}
	if _, err := os.Stat(in.layout.Previous()); !os.IsNotExist(err) {
		t.Fatalf("want nothing kept after the rollback, got %v", err)
	}

	v, exit = in.json(t, identity.Version, "--rollback", "--cancel")
	wantCode(t, v, exit, hubapi.ErrorCodeNoPreviousVersion, clikit.ExitRejected)
}

// updatedAcrossBreaking updates the running hub to a release whose migration
// breaks the previous version, which only the database copy can then serve.
func updatedAcrossBreaking(t *testing.T) *installedHub {
	t.Helper()
	base, _ := publishHub(t, map[string]string{newVersion: releaseOf(newVersion)})
	in := installHub(t, base)
	in.start(t)
	if v, exit := in.json(t, identity.Version, "--apply"); exit != clikit.ExitOK {
		t.Fatalf("the update failed: %v", v)
	}
	// As a migration of the new version that breaks older ones would. The
	// hub just started may be writing its first cleanup: wait for it.
	conn, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)", hubconfig.DBPath(in.home), waittest.Limit(t).Milliseconds()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	breaking := hubdb.Known() + 1
	if _, err := conn.Exec(fmt.Sprintf("PRAGMA user_version = %d; UPDATE schema_readers SET oldest = %d", breaking, breaking)); err != nil {
		t.Fatal(err)
	}
	return in
}

// TestUpdateRollbackRestores rolls back across a migration that breaks the
// previous version, restoring the copy only when told to.
// --apply writes the completion of each shell found, from the new binary,
// and again when up to date; one it cannot write is a warning.
func TestUpdateWritesCompletions(t *testing.T) {
	t.Parallel()
	noCompletion := strings.Replace(releaseOf("0.7.0"), `"completion "*)`, `"completion "*) exit 1; `, 1)
	base, _ := publishHub(t, map[string]string{newVersion: releaseOf(newVersion), "0.7.0": noCompletion})
	in := installHub(t, base)
	in.start(t)
	wantCompletions := func(version string) {
		t.Helper()
		for _, c := range in.layout.Completions[:2] {
			if got := readTestFile(t, c.Path); got != c.Shell+" completion of "+version+"\n" {
				t.Errorf("want the %s completion of %s, got %q", c.Shell, version, got)
			}
		}
		if _, err := os.Stat(in.layout.Completions[2].Path); !os.IsNotExist(err) {
			t.Errorf("want no fish completion without fish, got %v", err)
		}
	}
	if v, exit := in.json(t, identity.Version, "--apply"); exit != clikit.ExitOK || v["result"] != resultUpdated {
		t.Fatalf("want updated, got exit %d: %v", exit, v)
	}
	wantCompletions(newVersion)

	if err := os.Remove(in.layout.Completions[0].Path); err != nil {
		t.Fatal(err)
	}
	if v, exit := in.json(t, newVersion, "--apply"); exit != clikit.ExitOK || v["result"] != resultUpToDate {
		t.Fatalf("want up to date, got exit %d: %v", exit, v)
	}
	wantCompletions(newVersion)

	v, exit := in.json(t, newVersion, "--apply", "--version", "0.7.0")
	w, _ := v["warnings"].([]any)
	if exit != clikit.ExitOK || v["result"] != resultUpdated || len(w) != 2 || !strings.Contains(w[0].(string), "the bash completion is not written to "+in.layout.Completions[0].Path) {
		t.Fatalf("want updated with a warning per shell, got exit %d: %v", exit, v)
	}
	wantCompletions(newVersion)
}

func TestUpdateRollbackRestores(t *testing.T) {
	t.Parallel()
	in := updatedAcrossBreaking(t)

	v, exit := in.json(t, newVersion, "--rollback", "--cancel")
	wantCode(t, v, exit, hubapi.ErrorCodeRestoreRequired, clikit.ExitRejected)
	if v["hint"] != "pass --discard-changes to restore the copy" {
		t.Fatalf("want --discard-changes hinted, got %v", v["hint"])
	}
	out := hubWith(t, in.home, in.env(newVersion), "update", "--rollback")
	if out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "hint: pass --discard-changes") {
		t.Fatalf("want the restore refused without a terminal, got %+v", out)
	}
	in.want(t, newVersion)

	v, exit = in.json(t, newVersion, "--rollback", "--discard-changes")
	u := updateOf(t, v)
	if exit != clikit.ExitOK || v["result"] != resultRolledBack || u["data_restored"] != true || u["schema_from"] != float64(hubdb.Known()+1) || u["schema_to"] != float64(hubdb.Known()) {
		t.Fatalf("want rolled back restoring the database, got exit %d: %v", exit, v)
	}
	in.want(t, identity.Version)
}

// TestUpdateRollbackPromptYes restores the copy when asked.
func TestUpdateRollbackPromptYes(t *testing.T) {
	t.Parallel()
	in := updatedAcrossBreaking(t)
	out := hubWith(t, in.home, func(env *Env) {
		in.env(newVersion)(env)
		env.Stdin = clikittest.Typed(t, "y")
	}, "update", "--rollback")
	if out.Exit != clikit.ExitOK || !strings.Contains(out.Stderr, "restore the copy? [y/N]: ") || !strings.HasPrefix(out.Stdout, "rolled back wdx-hub "+newVersion+" -> dev\n") {
		t.Fatalf("want rolled back, got %+v", out)
	}
	in.want(t, identity.Version)
}

// TestUpdateRollbackPromptNo answers with Enter, which is no.
func TestUpdateRollbackPromptNo(t *testing.T) {
	t.Parallel()
	in := updatedAcrossBreaking(t)
	out := hubWith(t, in.home, func(env *Env) {
		in.env(newVersion)(env)
		env.Stdin = clikittest.Typed(t, "")
	}, "update", "--rollback")
	if out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "[restore_required]") {
		t.Fatalf("want the rollback refused, got %+v", out)
	}
	in.want(t, newVersion)
}

// TestUpdateUndone updates to a release whose service does not start: the
// old binary, database, settings and unit are put back and serve again.
func TestUpdateUndone(t *testing.T) {
	t.Parallel()
	base, _ := publishHub(t, map[string]string{newVersion: brokenRelease})
	in := installHub(t, base)
	in.start(t)
	settings := readTestFile(t, hubconfig.ConfigPath(in.home))

	v, exit := in.json(t, identity.Version, "--apply")
	wantCode(t, v, exit, hubapi.ErrorCodeUpdateFailed, clikit.ExitSystem)
	details, _ := v["details"].(map[string]any)
	if details["step"] != "start" || details["undone"] != true || details["version"] != identity.Version {
		t.Fatalf("want the start undone, got %v", v)
	}
	if hint, _ := v["hint"].(string); !strings.Contains(hint, "journalctl -u wdx-hub") {
		t.Fatalf("want journalctl named, got %q", hint)
	}
	in.want(t, identity.Version)
	if got := readTestFile(t, hubconfig.ConfigPath(in.home)); got != settings {
		t.Fatalf("want the settings put back, got\n%s", got)
	}
	if _, err := os.Stat(in.layout.Previous()); !os.IsNotExist(err) {
		t.Fatalf("want nothing kept after a failed update, got %v", err)
	}
}

func TestUpdateSettingsInvalid(t *testing.T) {
	t.Parallel()
	base, _ := publishHub(t, map[string]string{newVersion: pickyRelease})
	in := installHub(t, base)
	in.start(t)
	settings := readTestFile(t, hubconfig.ConfigPath(in.home))

	v, exit := in.json(t, identity.Version, "--apply")
	wantCode(t, v, exit, hubapi.ErrorCodeSettingsInvalid, clikit.ExitRejected)
	if details, _ := v["details"].(map[string]any); details["version"] != newVersion || details["settings"] == nil {
		t.Fatalf("want the release and its problems, got %v", v)
	}
	in.want(t, identity.Version)
	if got := readTestFile(t, hubconfig.ConfigPath(in.home)); got != settings {
		t.Fatalf("want the settings left alone, got\n%s", got)
	}
	if got := in.service.called(); len(got) != 1 {
		t.Fatalf("want the service left running, got %v", got)
	}
}

// TestUpdateBackupsKept updates a hub that is not running: the update backs
// up its database itself and keeps the newest 3 backups.
func TestUpdateBackupsKept(t *testing.T) {
	t.Parallel()
	base, _ := publishHub(t, map[string]string{newVersion: releaseOf(newVersion)})
	in := installHub(t, base)
	dir := hubconfig.BackupsDir(in.home)
	old := time.Now().Add(-time.Hour)
	for i, version := range []string{"0.0.1", "0.0.2", "0.0.3"} {
		path := filepath.Join(dir, "hub-"+version+".db")
		writeTestFile(t, path, "")
		at := old.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}

	if v, exit := in.json(t, identity.Version, "--apply"); exit != clikit.ExitOK || v["result"] != resultUpdated {
		t.Fatalf("want updated, got exit %d: %v", exit, v)
	}
	in.want(t, newVersion)
	matches, err := filepath.Glob(filepath.Join(dir, "hub-*.db"))
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, m := range matches {
		kept = append(kept, filepath.Base(m))
	}
	slices.Sort(kept)
	if want := []string{"hub-0.0.2.db", "hub-0.0.3.db", "hub-" + identity.Version + ".db"}; !slices.Equal(kept, want) {
		t.Fatalf("want backups %v, got %v", want, kept)
	}
}

func TestUpdateChecksumMismatch(t *testing.T) {
	t.Parallel()
	base, dir := publishHub(t, map[string]string{newVersion: releaseOf(newVersion)})
	sums := filepath.Join(dir, "v"+newVersion, "checksums.txt")
	b := []byte(readTestFile(t, sums))
	// Any other first digit breaks it.
	b[0] = map[bool]byte{true: '1', false: '0'}[b[0] == '0']
	writeTestFile(t, sums, string(b))
	in := installHub(t, base)

	v, exit := in.json(t, identity.Version, "--apply")
	wantCode(t, v, exit, hubapi.ErrorCodeChecksumMismatch, clikit.ExitSystem)
	if got := readTestFile(t, in.layout.Binary); got != releaseOf(identity.Version) {
		t.Fatalf("want the binary left, got\n%s", got)
	}
}

func TestUpdateRunning(t *testing.T) {
	t.Parallel()
	base, _ := publishHub(t, map[string]string{newVersion: releaseOf(newVersion)})
	in := installHub(t, base)
	if err := os.MkdirAll(in.layout.Lib, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(in.layout.Lib, "update.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--apply"}, {"--rollback", "--cancel"}} {
		v, exit := in.json(t, identity.Version, args...)
		wantCode(t, v, exit, hubapi.ErrorCodeUpdateRunning, clikit.ExitRejected)
	}
}

func TestUpdateText(t *testing.T) {
	t.Parallel()
	base, _ := publishHub(t, map[string]string{newVersion: releaseOf(newVersion)})
	in := installHub(t, base)
	in.start(t)
	out := hubWith(t, in.home, in.env(identity.Version), "update", "--apply")
	backup := filepath.Join(hubconfig.BackupsDir(in.home), "hub-"+identity.Version+".db")
	if out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, "updated wdx-hub dev -> "+newVersion+"\n") ||
		!strings.Contains(out.Stdout, "sudo wdx-hub update --rollback goes back to dev") ||
		!strings.Contains(out.Stderr, "==> backing up the database to "+backup+"\n==> stopping the daemon\n") {
		t.Fatalf("unexpected text: %+v", out)
	}
	if out = hubWith(t, in.home, in.env(newVersion), "update", "--rollback"); out.Exit != clikit.ExitOK || !strings.Contains(out.Stdout, "database: kept") {
		t.Fatalf("unexpected text: %+v", out)
	}
}

// TestDecide checks who a command run as root continues as.
func TestDecide(t *testing.T) {
	t.Parallel()
	hub := &hubconfig.Account{Name: "wdx-hub", UID: 990, GID: 990, Groups: []int{990}, Home: "/var/lib/wdx-hub"}
	become := RunAs{Switch: true, Account: *hub, Home: hub.Home}
	keepRoot := RunAs{Home: hub.Home}
	for _, c := range []struct {
		args []string
		uid  int
		hub  *hubconfig.Account
		want RunAs
	}{
		{[]string{"user", "list"}, 0, hub, become},
		{[]string{"update"}, 0, hub, become},
		{[]string{"update", "--channel", "dev"}, 0, hub, become},
		{[]string{"update", "--apply"}, 0, hub, keepRoot},
		{[]string{"update", "--apply", "--json"}, 0, hub, keepRoot},
		{[]string{"--json", "update", "--rollback", "--cancel"}, 0, hub, keepRoot},
		{[]string{"update", "--apply=false"}, 0, hub, become},
		{[]string{"update", "--no-such-flag", "--apply"}, 0, hub, become},
		{[]string{"serve"}, 0, hub, become},
		{[]string{"user", "list"}, 1000, hub, RunAs{}},
		{[]string{"update", "--apply"}, 1000, hub, RunAs{}},
		{[]string{"user", "list"}, 0, nil, RunAs{}},
	} {
		if got := Decide(c.args, c.uid, c.hub); !equalRunAs(got, c.want) {
			t.Errorf("%v as uid %d: want %+v, got %+v", c.args, c.uid, c.want, got)
		}
	}
}

func equalRunAs(a, b RunAs) bool {
	return a.Switch == b.Switch && a.Home == b.Home && a.Account.Name == b.Account.Name && a.Account.UID == b.Account.UID &&
		a.Account.GID == b.Account.GID && slices.Equal(a.Account.Groups, b.Account.Groups) && a.Account.Home == b.Account.Home
}

// TestServiceUnit checks that the unit deploy/hub ships is the one the hub
// prints, which install-hub.sh installs.
func TestServiceUnit(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	out := hub(t, home, "service", "unit")
	if out.Exit != clikit.ExitOK {
		t.Fatalf("unexpected run: %+v", out)
	}
	if shipped := readTestFile(t, "../../../deploy/hub/wdx-hub.service"); shipped != out.Stdout {
		t.Fatalf("deploy/hub/wdx-hub.service differs from %s service unit: write it anew with %s service unit > deploy/hub/wdx-hub.service\n%s", name, name, out.Stdout)
	}
	v, exit := hubJSON(t, home, "service", "unit", "--"+privilegedPortFlag)
	if unit, _ := v["unit"].(string); exit != clikit.ExitOK || !strings.Contains(unit, "AmbientCapabilities=CAP_NET_BIND_SERVICE\n") {
		t.Fatalf("want the capability given, got %v", v)
	}
	if strings.Contains(out.Stdout, "CAP_NET_BIND_SERVICE") {
		t.Fatal("want no capability without --" + privilegedPortFlag)
	}
}

// rootUnitRelease is the release of REVIEW_SEC_CODEX S3: its unit runs a
// command of its own as root.
var rootUnitRelease = regexp.MustCompile(`"service unit"\)[^\n]*`).
	ReplaceAllLiteralString(releaseOf(newVersion), `"service unit") printf '%s\n' '{"unit": "[Service]\nUser=root\nExecStart=/usr/bin/id\n"}' ;;`)

// TestUpdateSourceFromConfig is REVIEW_SEC_CODEX S3: the hub's user sets
// update.base_url in the config file to releases of its own, and root's
// update refuses them, installing nothing.
func TestUpdateSourceFromConfig(t *testing.T) {
	t.Parallel()
	base, _ := publishHub(t, map[string]string{newVersion: rootUnitRelease})
	in := installHub(t, base)
	in.start(t)
	if _, exit := hubJSON(t, in.home, "setting", "set", "--name", "update.base_url", "--value", base); exit != clikit.ExitOK {
		t.Fatal("update.base_url not set")
	}
	v, exit := hubJSONWith(t, in.home, func(env *Env) {
		in.env(identity.Version)(env)
		env.Config = load(t, in.home)
		env.Update.BaseURL = ""
	}, "update", "--apply")
	wantCode(t, v, exit, hubapi.ErrorCodeUpdateSourceUntrusted, clikit.ExitRejected)
	if details, _ := v["details"].(map[string]any); details["url"] != base || !strings.HasPrefix(details["from"].(string), hubconfig.ConfigPath(in.home)+":") {
		t.Fatalf("want the address and the config file's line, got %v", v)
	}
	if hint, _ := v["hint"].(string); !strings.Contains(hint, "sudo WDX_HUB_UPDATE_BASE_URL="+base+" wdx-hub update --apply") {
		t.Fatalf("want the variable in the hint, got %q", hint)
	}
	in.want(t, identity.Version)
}

// TestUpdateSourceFromEnvironment updates from an address root gives in its
// own environment.
func TestUpdateSourceFromEnvironment(t *testing.T) {
	t.Parallel()
	base, _ := publishHub(t, map[string]string{newVersion: releaseOf(newVersion)})
	in := installHub(t, base)
	in.start(t)
	getenv := func(key string) string {
		if key == "WDX_HUB_UPDATE_BASE_URL" {
			return base
		}
		return homeVars(in.home)(key)
	}
	v, exit := hubJSONWith(t, in.home, func(env *Env) {
		in.env(identity.Version)(env)
		cfg, err := hubconfig.Load(getenv)
		if err != nil {
			t.Fatal(err)
		}
		env.Config, env.Getenv = cfg, getenv
		env.Update.BaseURL = ""
	}, "update", "--apply")
	if exit != clikit.ExitOK || v["result"] != resultUpdated {
		t.Fatalf("want updated, got exit %d: %v", exit, v)
	}
	in.want(t, newVersion)
}

// TestUpdateUnitRefused updates to a release whose unit runs as root, from
// an address root chose: the old version is put back.
func TestUpdateUnitRefused(t *testing.T) {
	t.Parallel()
	base, _ := publishHub(t, map[string]string{newVersion: rootUnitRelease})
	in := installHub(t, base)
	in.start(t)
	v, exit := in.json(t, identity.Version, "--apply")
	wantCode(t, v, exit, hubapi.ErrorCodeUpdateFailed, clikit.ExitSystem)
	if details, _ := v["details"].(map[string]any); details["step"] != "start" || details["undone"] != true {
		t.Fatalf("want the start undone, got %v", v)
	}
	in.want(t, identity.Version)
}

func TestUnitSafe(t *testing.T) {
	t.Parallel()
	l := hubconfig.Installed
	if err := unitSafe(unit(l, false), l); err != nil {
		t.Fatalf("the hub's own unit refused: %v", err)
	}
	if err := unitSafe(unit(l, true), l); err != nil {
		t.Fatalf("the hub's own unit with the capability refused: %v", err)
	}
	for _, text := range []string{
		"[Service]\nUser=root\nExecStart=/usr/bin/id\n",
		"[Service]\nExecStart=/usr/local/bin/wdx-hub serve\n",
		"[Service]\nExecStart=/usr/local/bin/wdx-hub serve\nUser=wdx-hub\nUser=root\n",
		"[Service]\nExecStart=+/usr/local/bin/wdx-hub serve\nUser=wdx-hub\n",
		"[Service]\nExecStart=/usr/local/bin/wdx-hub serve\nExecStart=/bin/sh\nUser=wdx-hub\n",
		"[Service]\nExecStart=\nUser=wdx-hub\n",
		"[Service]\nExecStart=/usr/local/bin/wdx-hub serve\nUser=wdx-hub\nGroup=root\n",
		"[Unit]\nExecStart=/usr/local/bin/wdx-hub serve\nUser=wdx-hub\n",
	} {
		if err := unitSafe(text, l); err == nil {
			t.Errorf("want refused:\n%s", text)
		}
	}
}

// TestUpdateHomeUnsafe updates a hub whose logs/ the hub's user made a
// symlink, or a file: refused before root writes anything there, the log
// of the run's included.
func TestUpdateHomeUnsafe(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"symlink", "file"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			base, _ := publishHub(t, map[string]string{newVersion: releaseOf(newVersion)})
			in := installHub(t, base)
			logs := hubconfig.LogsDir(in.home)
			if err := os.RemoveAll(logs); err != nil {
				t.Fatal(err)
			}
			target := t.TempDir()
			if kind == "symlink" {
				if err := os.Symlink(target, logs); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(logs, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			// Not through hubWith, which reads the run's log: there is none.
			var stdout, stderr bytes.Buffer
			env := testEnv(t, in.home, &stdout, &stderr)
			in.env(identity.Version)(&env)
			args := []string{"update", "--apply", "--json"}
			exit := Run(context.Background(), args, env)
			var v map[string]any
			if err := json.Unmarshal(clikittest.JSON(t, args, clikittest.Output{Exit: exit, Stdout: stdout.String(), Stderr: stderr.String()}, codes), &v); err != nil {
				t.Fatal(err)
			}
			wantCode(t, v, exit, hubapi.ErrorCodeHomeUnsafe, clikit.ExitSystem)
			if details, _ := v["details"].(map[string]any); details["path"] != logs {
				t.Fatalf("want %s named, got %v", logs, v)
			}
			if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
				t.Fatalf("want nothing written through the symlink, got %v, %v", entries, err)
			}
			if info, err := os.Lstat(logs); err != nil || info.Size() != 0 && kind == "file" {
				t.Fatalf("want %s left as it was, got %v, %v", logs, info, err)
			}
			if got := readTestFile(t, in.layout.Binary); got != releaseOf(identity.Version) {
				t.Fatalf("want nothing installed, got\n%s", got)
			}
			if _, err := os.Stat(hubconfig.BackupsDir(in.home)); !os.IsNotExist(err) {
				t.Fatalf("want no backup made, got %v", err)
			}
		})
	}
}
