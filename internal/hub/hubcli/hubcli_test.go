package hubcli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/clikit/clikittest"
	"github.com/srjn45/wdx/internal/hub/account"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/hub/hubdb"
	"github.com/srjn45/wdx/internal/hub/server"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/logtest"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/sqlite/sqlitetest"
	"github.com/srjn45/wdx/internal/unixsocket"
	"github.com/srjn45/wdx/internal/waittest"
)

// hubBinary is wdx-hub built from this tree, which the signal test runs.
var hubBinary string

func TestMain(m *testing.M) {
	done := runtest.Start()
	dir, err := os.MkdirTemp("", "bin")
	if err != nil {
		panic(err)
	}
	hubBinary = filepath.Join(dir, name)
	c := exec.Command("go", "build", "-ldflags", "-s -w", "-o", hubBinary, "../../../cmd/wdx-hub")
	c.Env = runtest.OriginalEnviron() // the go command's caches are the machine's
	if out, err := c.CombinedOutput(); err != nil {
		panic(fmt.Sprintf("build %s: %v\n%s", hubBinary, err, out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(done(code))
}

// testFile is a test home's config file: a hub on ports the system chooses,
// logging in detail, with a master key made for the home. It sends no
// metrics, otel.endpoint being empty.
func testFile(t *testing.T) string {
	t.Helper()
	key := make([]byte, hubconfig.MasterKeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return "public_url: http://localhost:8080\nlisten:\n  web: 127.0.0.1:0\n  nodes: 127.0.0.1:0\nlog:\n  level: debug\n" +
		"master_key: " + base64.StdEncoding.EncodeToString(key) + "\n"
}

// newHome returns a home with its config file and no hub serving it.
func newHome(t *testing.T) string {
	t.Helper()
	home := runtest.Dir(t) // it holds the socket
	logtest.OnFailure(t, home)
	if err := os.WriteFile(hubconfig.ConfigPath(home), []byte(testFile(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// homeVars is the environment of a run against home.
func homeVars(home string) func(string) string {
	return func(key string) string {
		if key == hubconfig.EnvPrefix+"HOME" {
			return home
		}
		return ""
	}
}

// load returns the settings of home, as main loads them.
func load(t *testing.T, home string) hubconfig.Config {
	t.Helper()
	cfg, err := hubconfig.Load(homeVars(home))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// newHub returns a home with a hub serving it in this process until the test
// ends, logging to the home's logs as the commands do.
func newHub(t *testing.T) string {
	t.Helper()
	home, _ := newHubServer(t)
	return home
}

// newHubServer is newHub, with the hub.
func newHubServer(t *testing.T) (string, *server.Server) {
	t.Helper()
	home := newHome(t)
	sqlitetest.Seed(t, hubconfig.DBPath(home), hubdb.Open)
	logs := logging.NewDailyFile(hubconfig.LogsDir(home), name, 7, time.Now)
	getenv := homeVars(home)
	ctx, stop := context.WithCancel(context.Background())
	s, err := server.Listen(ctx, server.Options{
		Config:   load(t, home),
		Log:      logging.New(logs, slog.LevelDebug).With("process", "hub"),
		Reload:   func() (hubconfig.Config, error) { return hubconfig.Load(getenv) },
		Settings: settingStore{getenv: getenv},
		Argon2:   account.Argon2{Memory: 8, Time: 1, Threads: 1},
	})
	if err != nil {
		stop()
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx) }()
	t.Cleanup(func() {
		stop()
		if err := <-served; err != nil {
			t.Errorf("hub: %v", err)
		}
		logs.Close()
	})
	return home, s
}

// testEnv is the Env of a run against home.
func testEnv(t *testing.T, home string, stdout, stderr io.Writer) Env {
	t.Helper()
	return Env{Stdin: strings.NewReader(""), Stdout: stdout, Stderr: stderr, Config: load(t, home), Getenv: homeVars(home), WorkDir: home}
}

// hub runs the command line args against home and checks it is logged, with
// the hub's lines for its requests, from received to returned.
func hub(t *testing.T, home string, args ...string) clikittest.Output {
	t.Helper()
	return hubWith(t, home, nil, args...)
}

// hubWith is hub in an environment adjust changes when not nil.
func hubWith(t *testing.T, home string, adjust func(*Env), args ...string) clikittest.Output {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := testEnv(t, home, &stdout, &stderr)
	if adjust != nil {
		adjust(&env)
	}
	exit := Run(context.Background(), args, env)
	out := clikittest.Output{Exit: exit, Stdout: stdout.String(), Stderr: stderr.String()}
	logs := clikittest.Logged{Dir: hubconfig.LogsDir(home), Called: "hub called", Abandoned: "hub unreachable", Served: "request returned", Root: newRoot}
	clikittest.WantLogged(t, logs, args, out, measureLinks)
	return out
}

// measureLinks changes printed into what is logged of it: an invite's or a
// reset's link measured.
func measureLinks(printed map[string]any) {
	for _, key := range []string{"invite", "reset"} {
		if m, ok := printed[key].(map[string]any); ok {
			if link, ok := m["link"].(string); ok {
				delete(m, "link")
				m["link_bytes"] = float64(len(link))
			}
		}
	}
}

func codes(code string) (clikit.CodeInfo, bool) {
	info, ok := hubapi.Codes[hubapi.ErrorCode(code)]
	return info, ok
}

// hubJSON runs args with --json against home and returns what it printed,
// checked to be one object its command's help declares, and the exit.
func hubJSON(t *testing.T, home string, args ...string) (map[string]any, int) {
	t.Helper()
	args = append(args, "--json")
	out := hub(t, home, args...)
	var v map[string]any
	if err := json.Unmarshal(clikittest.JSON(t, args, out, codes), &v); err != nil {
		t.Fatal(err)
	}
	code, _ := v["code"].(string)
	result, _ := v["result"].(string)
	clikittest.WantDeclared(t, newRoot(), args, result, code)
	return v, out.Exit
}

// want fails unless the run returned exit and, when not empty, key holding
// value.
func want(t *testing.T, v map[string]any, exit, wantExit int, key, value string) {
	t.Helper()
	if exit != wantExit || key != "" && v[key] != value {
		t.Fatalf("want exit %d and %s %q, got exit %d: %v", wantExit, key, value, exit, v)
	}
}

func TestCommandDocs(t *testing.T) {
	t.Parallel()
	root := newRoot()
	clikittest.CheckDocs(t, root)
	var check func(cmd *cobra.Command)
	check = func(cmd *cobra.Command) {
		for _, code := range clikit.AnnotationList(cmd, clikit.Errors) {
			if _, ok := codes(code); !ok {
				t.Errorf("%s: unexpected error code %q", cmd.CommandPath(), code)
			}
		}
		for _, sub := range cmd.Commands() {
			check(sub)
		}
	}
	check(root)
}

func TestExamples(t *testing.T) {
	t.Parallel()
	clikittest.CheckExamples(t, newRoot)
}

func TestVersion(t *testing.T) {
	t.Parallel()
	v, exit := hubJSON(t, newHub(t), "version")
	h, _ := v["hub"].(map[string]any)
	cli, _ := v["cli"].(map[string]any)
	if exit != clikit.ExitOK || h["name"] != name || h["api_version"] != "1" || cli["name"] != name {
		t.Fatalf("want both versions, got exit %d: %v", exit, v)
	}
	if out := hub(t, newHub(t), "version"); out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stdout, name+" ") || !strings.Contains(out.Stdout, "\nhub ") {
		t.Fatalf("unexpected text %+v", out)
	}
}

// TestUserLogged checks a run logs once who it runs as, from Env.User,
// before command started: as main says it, root's run gone on as the hub's
// user (the wdx doc HUB_INSTALL, Decided 8).
func TestUserLogged(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	const as = "wdx-hub, become from root"
	hubWith(t, home, func(env *Env) { env.User = as }, "version")
	files, err := filepath.Glob(filepath.Join(hubconfig.LogsDir(home), "*.log"))
	if err != nil || len(files) != 1 {
		t.Fatalf("log files %v: %v", files, err)
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var msgs []string
	started := false
	for l := range strings.Lines(string(b)) {
		var line map[string]any
		if err := json.Unmarshal([]byte(l), &line); err != nil {
			t.Fatal(err)
		}
		if line["msg"] == "command started" {
			started = true
		}
		if user, ok := line["user"]; ok {
			if started || user != as {
				t.Errorf("logged user %v in %v, want %q before command started", user, line["msg"], as)
			}
			msgs = append(msgs, line["msg"].(string))
		}
	}
	if len(msgs) != 1 {
		t.Errorf("the user logged in %v, want one line", msgs)
	}
}

func TestVersionWithoutHub(t *testing.T) {
	t.Parallel()
	v, exit := hubJSON(t, newHome(t), "version")
	if exit != clikit.ExitOK || v["hub"] != nil || v["cli"] == nil {
		t.Fatalf("want hub null, got exit %d: %v", exit, v)
	}
	if out := hub(t, newHome(t), "version"); !strings.Contains(out.Stdout, "hub not running") {
		t.Fatalf("unexpected text %+v", out)
	}
}

func TestSettingList(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	v, exit := hubJSON(t, home, "setting", "list")
	list, _ := v["settings"].([]any)
	if exit != clikit.ExitOK || len(list) != len(hubconfig.Settings()) {
		t.Fatalf("want every setting, got exit %d: %v", exit, v)
	}
	first := list[0].(map[string]any)
	if first["key"] != "home" || first["value"] != home || first["editable"] != false || first["from"] != "WDX_HUB_HOME" {
		t.Fatalf("want home, from the environment only, got %v", first)
	}
	if out := hub(t, home, "setting", "list"); out.Exit != clikit.ExitOK || !strings.Contains(out.Stdout, "public_url") {
		t.Fatalf("unexpected text %+v", out)
	}
}

func TestSettingGet(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	v, exit := hubJSON(t, home, "setting", "get", "--name", "listen.web")
	s, _ := v["setting"].(map[string]any)
	if exit != clikit.ExitOK || s["value"] != "127.0.0.1:0" || s["default"] != ":8080" || s["editable"] != true {
		t.Fatalf("unexpected exit %d: %v", exit, v)
	}
	v, exit = hubJSON(t, home, "setting", "get", "--name", "no.such")
	want(t, v, exit, clikit.ExitUsage, "code", clikit.CodeUsage)
	if out := hub(t, home, "setting", "get", "--name", "log.level"); !strings.HasPrefix(out.Stdout, "log.level debug\n") {
		t.Fatalf("unexpected text %+v", out)
	}
}

func TestSettingSetApplied(t *testing.T) {
	t.Parallel()
	home := newHub(t)
	v, exit := hubJSON(t, home, "setting", "set", "--name", "log.level", "--value", "info")
	want(t, v, exit, clikit.ExitOK, "result", resultUpdated)
	want(t, v, exit, clikit.ExitOK, "reload", reloadApplied)
	v, exit = hubJSON(t, home, "setting", "set", "--name", "log.level", "--value", "info")
	want(t, v, exit, clikit.ExitOK, "result", resultUnchanged)
	if got := load(t, home); got.Value("log.level") != "info" {
		t.Fatalf("want log.level saved, got %s", got.Value("log.level"))
	}
	if out := hub(t, home, "setting", "set", "--name", "log.level", "--value", "debug"); out.Stdout != "set log.level to debug\n  the hub applied it\n" || out.Stderr != "" {
		t.Fatalf("unexpected text %+v", out)
	}
}

// What the hub applies only when it starts is saved, with a warning.
func TestSettingSetNotApplied(t *testing.T) {
	t.Parallel()
	home := newHub(t)
	out := hub(t, home, "setting", "set", "--name", "listen.web", "--value", "127.0.0.1:1", "--json")
	var v map[string]any
	if err := json.Unmarshal([]byte(out.Stdout), &v); err != nil {
		t.Fatal(err)
	}
	want(t, v, out.Exit, clikit.ExitOK, "reload", reloadNotApplied)
	if !strings.Contains(out.Stderr, "listen.web take effect when the hub next starts") {
		t.Fatalf("want a warning, got %q", out.Stderr)
	}
}

func TestSettingSetNotRunning(t *testing.T) {
	t.Parallel()
	v, exit := hubJSON(t, newHome(t), "setting", "set", "--name", "otel.interval_seconds", "--value", "30")
	want(t, v, exit, clikit.ExitOK, "reload", reloadNotRunning)
}

func TestSettingSetRefused(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	for _, args := range [][]string{
		{"--name", "log.level", "--value", "loud"},
		{"--name", "home", "--value", "/elsewhere"},
		{"--name", "web.tls", "--value", "files"}, // without its files
		{"--name", "no.such", "--value", "1"},
	} {
		v, exit := hubJSON(t, home, append([]string{"setting", "set"}, args...)...)
		want(t, v, exit, clikit.ExitUsage, "code", clikit.CodeUsage)
	}
	v, _ := hubJSON(t, home, "setting", "set", "--name", "home", "--value", "/elsewhere")
	if v["hint"] != "set WDX_HUB_HOME in the environment; see 'wdx-hub setting set --help'" {
		t.Fatalf("want the variable named, got %v", v)
	}
	if got := load(t, home); got.Value("log.level") != "debug" || got.WebTLS != hubconfig.TLSOff {
		t.Fatalf("want nothing saved, got %+v", got)
	}
}

// A secret set with --value or read from stdin with --value - is saved, and
// no part of it reaches the day's log.
func TestSettingSetSecretNotLogged(t *testing.T) {
	t.Parallel()
	for _, fromStdin := range []bool{false, true} {
		home := newHome(t)
		key := make([]byte, hubconfig.MasterKeySize)
		if _, err := rand.Read(key); err != nil {
			t.Fatal(err)
		}
		secret := base64.StdEncoding.EncodeToString(key)
		args := []string{"setting", "set", "--name", "master_key", "--value", secret, "--json"}
		stdin := ""
		if fromStdin {
			args[5], stdin = "-", secret+"\n"
		}
		out := hubWith(t, home, func(env *Env) { env.Stdin = strings.NewReader(stdin) }, args...)
		var v map[string]any
		if err := json.Unmarshal(clikittest.JSON(t, args, out, codes), &v); err != nil {
			t.Fatal(err)
		}
		want(t, v, out.Exit, clikit.ExitOK, "result", resultUpdated)
		if got := load(t, home); !bytes.Equal(got.MasterKey, key) {
			t.Fatalf("stdin %v: want the key saved, got %v", fromStdin, v)
		}
		wantNotLogged(t, home, secret)
	}
}

// --value - with nothing on stdin is refused.
func TestSettingSetEmptyStdin(t *testing.T) {
	t.Parallel()
	v, exit := hubJSON(t, newHome(t), "setting", "set", "--name", "log.level", "--value", "-")
	want(t, v, exit, clikit.ExitUsage, "code", clikit.CodeUsage)
}

// A fresh home, its public_url not set, takes it from setting set, the one
// command that can set it.
func TestSettingSetPublicURLOnAFreshHome(t *testing.T) {
	t.Parallel()
	home := runtest.Dir(t)
	_, err := hubconfig.Load(homeVars(home))
	args := []string{"setting", "set", "--name", "public_url", "--value", "https://hub.example.com", "--json"}
	if err == nil || !RewritesSettings(args, err) {
		t.Fatalf("want public_url missing, and setting set run anyway, got %v", err)
	}
	var stdout bytes.Buffer
	env := Env{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: io.Discard, Config: hubconfig.Default(), Getenv: homeVars(home)}
	env.Config.Home = home
	if exit := Run(context.Background(), args, env); exit != clikit.ExitOK {
		t.Fatalf("want it set, got %d: %s", exit, stdout.String())
	}
	if got := load(t, home); got.PublicURL != "https://hub.example.com" {
		t.Fatalf("want it saved, got %+v", got)
	}
	if RewritesSettings([]string{"version"}, err) {
		t.Fatal("want every other command refused")
	}
}

func TestSettingWrite(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	v, exit := hubJSON(t, home, "setting", "write")
	want(t, v, exit, clikit.ExitOK, "result", resultWritten)
	v, exit = hubJSON(t, home, "setting", "write")
	want(t, v, exit, clikit.ExitOK, "result", resultUnchanged)
	to := filepath.Join(t.TempDir(), "out", "config.yaml")
	v, exit = hubJSON(t, home, "setting", "write", "--to", to)
	want(t, v, exit, clikit.ExitOK, "result", resultWritten)
	if b, err := os.ReadFile(to); err != nil || !strings.HasPrefix(string(b), "# "+name+" settings.") {
		t.Fatalf("want the file written to --to, got %v", err)
	}
	if out := hub(t, home, "setting", "write"); !strings.HasPrefix(out.Stdout, "up to date: ") {
		t.Fatalf("unexpected text %+v", out)
	}
}

func TestBackup(t *testing.T) {
	t.Parallel()
	home := newHub(t)
	path := filepath.Join(t.TempDir(), "hub.db")
	v, exit := hubJSON(t, home, "backup", "--out", path)
	want(t, v, exit, clikit.ExitOK, "result", string(hubapi.ResultBackedUp))
	if v["path"] != path || v["ca_path"] != path+".ca" {
		t.Fatalf("want the paths, got %v", v)
	}
	for _, p := range []string{path, filepath.Join(path+".ca", "ca.key")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
	}
	v, exit = hubJSON(t, home, "backup", "--out", path)
	want(t, v, exit, clikit.ExitRejected, "code", string(hubapi.ErrorCodePathExists))
	// Relative to the working directory, the home in these runs.
	copied := filepath.Join(home, "copy.db")
	if out := hub(t, home, "backup", "--out", "copy.db"); out.Stdout != "backed up the database to "+copied+" and the certificate authority to "+copied+".ca\n" {
		t.Fatalf("unexpected text %+v", out)
	}
}

func TestBackupWithoutHub(t *testing.T) {
	t.Parallel()
	v, exit := hubJSON(t, newHome(t), "backup", "--out", filepath.Join(t.TempDir(), "hub.db"))
	want(t, v, exit, clikit.ExitSystem, "code", string(hubapi.ErrorCodeHubUnavailable))
	if v["hint"] != "run "+name+" serve" {
		t.Fatalf("want the hint, got %v", v)
	}
}

// serving is a serve run in this process, stopped by its test as a signal
// would stop it.
type serving struct {
	args           []string
	stopped        chan context.CancelFunc // what serve took for its signals
	listening      chan server.Addresses
	finished       chan struct{} // closed once Run returned
	code           int
	stdout, stderr bytes.Buffer
}

// serve runs wdx-hub serve with args against home in the background, in an
// environment adjust changes; it is stopped when the test ends.
func serve(t *testing.T, home string, adjust func(*Env), args ...string) *serving {
	t.Helper()
	s := &serving{
		args:    append([]string{"serve"}, args...),
		stopped: make(chan context.CancelFunc, 1), listening: make(chan server.Addresses, 1), finished: make(chan struct{}),
	}
	env := testEnv(t, home, &s.stdout, &s.stderr)
	env.Serve.Stop = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(ctx)
		s.stopped <- cancel
		return ctx, cancel
	}
	env.Serve.Serving = func(a server.Addresses) { s.listening <- a }
	if adjust != nil {
		adjust(&env)
	}
	go func() {
		defer close(s.finished)
		s.code = Run(context.Background(), s.args, env)
	}()
	t.Cleanup(func() {
		s.stop()
		<-s.finished
	})
	return s
}

// stop stops the run, as SIGTERM does, once it is waiting for a signal.
func (s *serving) stop() {
	select {
	case cancel := <-s.stopped:
		cancel()
	case <-s.finished:
	}
}

// addresses waits for the run to listen and returns where.
func (s *serving) addresses(t *testing.T) server.Addresses {
	t.Helper()
	select {
	case a := <-s.listening:
		return a
	case <-s.finished:
		t.Fatalf("%v ended: %s", s.args, s.stderr.String())
	case <-waittest.After(t):
		t.Fatalf("%v did not listen", s.args)
	}
	return server.Addresses{}
}

// exit waits for the run to end and returns what it printed, after checking
// its log.
func (s *serving) exit(t *testing.T) clikittest.Output {
	t.Helper()
	select {
	case <-s.finished:
	case <-waittest.After(t):
		t.Fatalf("%v did not end", s.args)
	}
	out := clikittest.Output{Exit: s.code, Stdout: s.stdout.String(), Stderr: s.stderr.String()}
	wantServeLogged(t, s.args, out)
	return out
}

// wantAnswering fails unless a hub answers on home's socket. A hub serve runs
// logs to its output, where WantLogged does not look.
func wantAnswering(t *testing.T, home string) {
	t.Helper()
	c := newClient(hubconfig.SocketPath(home), logging.NewDebugID(), false)
	defer c.close()
	v, err := c.version(context.Background())
	if err != nil || v.Name != name {
		t.Fatalf("want the hub answering, got %+v, %v", v, err)
	}
}

// wantServeLogged checks that the stdout of a serve run is its log, JSON
// lines tagged with a debug id and a process, with the run from the command
// line it received to what it returned.
func wantServeLogged(t *testing.T, args []string, out clikittest.Output) {
	t.Helper()
	var run []clikittest.LogLine
	var id string
	sc := bufio.NewScanner(strings.NewReader(out.Stdout))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var l clikittest.LogLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil || l.Msg == "" || l.DebugID == "" || l.Process == "" {
			t.Fatalf("%v: bad log line %q: %v", args, sc.Text(), err)
		}
		if l.Msg == "command received" {
			id = l.DebugID
		}
		if l.DebugID == id {
			run = append(run, l)
		}
	}
	if len(run) < 2 || run[0].Msg != "command received" || run[len(run)-1].Msg != "command returned" {
		t.Fatalf("%v: want the run logged from received to returned, got %+v", args, run)
	}
	last := run[len(run)-1]
	if last.Exit == nil || *last.Exit != out.Exit {
		t.Fatalf("%v: logged exit %v, want %d", args, last.Exit, out.Exit)
	}
}

func TestServe(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	s := serve(t, home, nil)
	a := s.addresses(t)
	// The first start made the database and the certificate authority.
	for _, path := range []string{hubconfig.DBPath(home), filepath.Join(hubconfig.CADir(home), "ca.key")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	wantAnswering(t, home)

	second := serve(t, home, nil)
	out := second.exit(t)
	if out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "[already_running]") {
		t.Fatalf("want the second refused, got %+v", out)
	}

	s.stop()
	out = s.exit(t)
	if want := fmt.Sprintf("serving on %s (web), %s (nodes) and %s (admin)\n", a.Web, a.Nodes, a.Admin); out.Exit != clikit.ExitOK || out.Stderr != want {
		t.Fatalf("want it stopped, got %+v, want stderr %q", out, want)
	}
	if !strings.Contains(out.Stdout, `"msg":"hub stopped"`) || !strings.Contains(out.Stdout, `"process":"hub"`) {
		t.Fatalf("want the hub's own lines on stdout too, got %s", out.Stdout)
	}
}

// A hub whose database has nodes and whose home lost its CA does not start.
func TestServeCAMissing(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	path := hubconfig.DBPath(home)
	sqlitetest.Seed(t, path, hubdb.Open)
	db, err := hubdb.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO users (id, username, created_at) VALUES ('usr-1', 'alice', 1);
		INSERT INTO nodes (id, user_id, name, public_key, os, version, created_at) VALUES ('node-1', 'usr-1', 'desktop', x'00', 'linux/amd64', '0.5.0', 1)`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	out := serve(t, home, nil).exit(t)
	if out.Exit != clikit.ExitSystem || !strings.Contains(out.Stderr, "[ca_missing]") || !strings.Contains(out.Stderr, "hint: restore ") {
		t.Fatalf("want ca_missing with its hint, got %+v", out)
	}
}

func TestServeJSON(t *testing.T) {
	t.Parallel()
	v, exit := hubJSON(t, newHome(t), "serve")
	want(t, v, exit, clikit.ExitUsage, "code", clikit.CodeUsage)
	if servesToOutput([]string{"serve", "--json=true"}) || !servesToOutput([]string{"--color", "never", "serve"}) {
		t.Fatal("want serve to log to stdout exactly when it runs without --json")
	}
}

// web.tls files needs its two files: as settings, or the settings fail to
// load; on disk, or serve fails before it starts.
func TestServeTLSFiles(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	vars := func(key string) string {
		if key == hubconfig.EnvPrefix+"WEB_TLS" {
			return hubconfig.TLSFiles
		}
		return homeVars(home)(key)
	}
	_, err := hubconfig.Load(vars)
	if err == nil || RewritesSettings([]string{"serve"}, err) {
		t.Fatalf("want web.tls_cert and web.tls_key required, got %v", err)
	}
	var stdout bytes.Buffer
	if exit := ConfigError([]string{"serve", "--json"}, Env{Stdout: &stdout, Stderr: io.Discard}, err); exit != clikit.ExitUsage || !strings.Contains(stdout.String(), `"code": "usage"`) {
		t.Fatalf("want usage, got %d: %s", exit, stdout.String())
	}

	missing := filepath.Join(t.TempDir(), "missing.pem")
	s := serve(t, home, func(env *Env) {
		env.Config.WebTLS, env.Config.WebTLSCert, env.Config.WebTLSKey = hubconfig.TLSFiles, missing, missing
	})
	out := s.exit(t)
	if out.Exit != clikit.ExitUsage || !strings.Contains(out.Stderr, "web.tls_cert is "+missing+", which cannot be read") {
		t.Fatalf("want the files refused, got %+v", out)
	}
	if _, err := os.Stat(hubconfig.DBPath(home)); err == nil {
		t.Fatal("want nothing started")
	}
}

// A home too deep for the admin socket fails naming the path, its length
// and the limit, with a shorter home as the hint.
func TestServeSocketTooLong(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	deep := filepath.Join(home, strings.Repeat("h", unixsocket.MaxPath))
	s := serve(t, home, func(env *Env) { env.Config.Home = deep })
	out := s.exit(t)
	socket := hubconfig.SocketPath(deep)
	want := fmt.Sprintf("%s is %d bytes, more than the %d", socket, len(socket), unixsocket.MaxPath)
	if out.Exit != clikit.ExitSystem || !strings.Contains(out.Stderr, want) || !strings.Contains(out.Stderr, "hint: a shorter home makes it shorter: set WDX_HUB_HOME to one") {
		t.Fatalf("want the path refused, got %+v", out)
	}
}

// serve refuses to start without the master key, saying how to make one.
func TestServeWithoutMasterKey(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	s := serve(t, home, func(env *Env) { env.Config.MasterKey = nil })
	out := s.exit(t)
	if out.Exit != clikit.ExitUsage || !strings.Contains(out.Stderr, "master_key is not set") || !strings.Contains(out.Stderr, "hint: "+hubconfig.MasterKeyHint) {
		t.Fatalf("want the key asked for, got %+v", out)
	}
	if _, err := os.Stat(hubconfig.DBPath(home)); err == nil {
		t.Fatal("want nothing started")
	}
}

// The binary stops on SIGTERM, letting the requests in flight finish, and
// logs to stdout from the command line received to what it returned.
func TestServeStoppedBySignal(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	cmd := exec.Command(hubBinary, "serve")
	cmd.Env = []string{hubconfig.EnvPrefix + "HOME=" + home}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	first, waited := make(chan string, 1), make(chan error, 1)
	go func() {
		lines := bufio.NewScanner(stderr)
		lines.Scan()
		first <- lines.Text()
		_, _ = io.Copy(io.Discard, stderr)
		waited <- cmd.Wait()
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill() // ended already, unless the test failed
		if err := <-waited; err != nil {
			t.Logf("serve: %v", err) // the test failed before it stopped
		}
	})
	select {
	case line := <-first:
		if !strings.HasPrefix(line, "serving on ") {
			t.Fatalf("want serving on stderr, got %q", line)
		}
	case <-waittest.After(t):
		t.Fatal("serve did not listen")
	}
	wantAnswering(t, home)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waited:
		waited <- err
		if err != nil {
			t.Fatalf("want exit 0, got %v", err)
		}
	case <-waittest.After(t):
		t.Fatal("serve did not stop")
	}
	out := clikittest.Output{Exit: 0, Stdout: stdout.String()}
	wantServeLogged(t, []string{"serve"}, out)
	if !strings.Contains(out.Stdout, `"msg":"hub stopping"`) {
		t.Fatalf("want the hub stopping logged, got %s", out.Stdout)
	}
}
