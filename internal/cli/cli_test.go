package cli

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/daemon"
	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/service"
)

// wdxBinary is wdx built from this tree, which starting the daemon in the
// background runs; releaseBinary is the same built as release
// releaseVersion, which the update tests install.
var wdxBinary, releaseBinary string

const releaseVersion = "0.2.0"

func TestMain(m *testing.M) {
	gittest.Isolate()
	dir, err := os.MkdirTemp("", "wdx-bin")
	if err != nil {
		panic(err)
	}
	wdxBinary = filepath.Join(dir, identity.Name)
	if out, err := exec.Command("go", "build", "-o", wdxBinary, "../../cmd/wdx").CombinedOutput(); err != nil {
		panic(fmt.Sprintf("build wdx: %v\n%s", err, out))
	}
	releaseBinary = filepath.Join(dir, "release", identity.Name)
	ldflags := "-X github.com/srjn45/wdx/internal/identity.Version=" + releaseVersion
	if out, err := exec.Command("go", "build", "-ldflags", ldflags, "-o", releaseBinary, "../../cmd/wdx").CombinedOutput(); err != nil {
		panic(fmt.Sprintf("build wdx %s: %v\n%s", releaseVersion, err, out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// shortHome returns an empty home under /tmp: its socket path must fit the
// 104 bytes macOS allows, which a path under t.TempDir() may exceed.
func shortHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "wdx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	// Daemons the wdx binary starts read it: they must not send metrics to
	// the collector wdx observe may be running.
	if err := os.WriteFile(identity.ConfigPath(home), []byte("otel:\n  endpoint: \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// testConfig returns the settings of a run against home, sending no metrics.
func testConfig(home string) config.Config {
	cfg := config.Default()
	cfg.Home = home
	cfg.LogLevel = slog.LevelDebug
	cfg.OtelEndpoint = ""
	return cfg
}

// testShell is the shell of every test terminal: it stays alive until it
// is hung up on.
const testShell = "#!/bin/sh\nexec sleep 60\n"

// newHome returns a home with a daemon serving it in this process until the
// test ends. Its terminals run testShell in holders of the wdx binary, and
// are removed when the test ends.
func newHome(t *testing.T) string {
	t.Helper()
	return newHomeWithShell(t, testShell)
}

// newHomeWithShell is newHome with script as its terminals' shell.
func newHomeWithShell(t *testing.T, script string) string {
	t.Helper()
	home := shortHome(t)
	shell := filepath.Join(t.TempDir(), "shell")
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	logs := logging.NewDailyFile(identity.LogsDir(home), 7, time.Now)
	log := logging.New(logs, slog.LevelDebug).With("process", "daemon")
	ctx, stop := context.WithCancel(context.Background())
	d, err := daemon.Listen(ctx, daemon.Options{
		Home: home, Log: log,
		Executable: wdxBinary, TerminalEnv: []string{"PATH=/usr/bin:/bin"}, TerminalShell: shell,
		TerminalHistoryLines: 100_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- d.Serve(ctx) }()
	t.Cleanup(func() {
		removeTerminals(home)
		stop()
		if err := <-served; err != nil {
			t.Errorf("daemon: %v", err)
		}
		logs.Close()
	})
	return home
}

// removeTerminals removes every terminal in home, stopping the live ones,
// so no holder outlives the test.
func removeTerminals(home string) {
	ctx := context.Background()
	c := client.New(client.Options{Socket: identity.SocketPath(home), DebugID: logging.NewDebugID()})
	list, err := c.ListTerminals(ctx, "", "")
	if err != nil {
		return
	}
	for _, term := range list.Terminals {
		_, _ = c.DeleteTerminal(ctx, "id:"+term.Id, "", "", true) // the daemon logs a failure, and the test fails on a leftover holder
	}
}

type output struct {
	exit           int
	stdout, stderr string
	pid            int // the wdx binary's that ran it; 0 for this process
}

// wdx runs the command line args against home, which stands in for ~/.wdx.
func wdx(t *testing.T, home string, args ...string) output {
	t.Helper()
	return wdxService(t, home, nil, args...)
}

// wdxService runs args with svc as the service the daemon is installed as.
func wdxService(t *testing.T, home string, svc *service.Manager, args ...string) output {
	t.Helper()
	return wdxWith(t, home, func(env *Env) { env.Service = svc }, args...)
}

// wdxWith runs args in an environment that adjust changes.
func wdxWith(t *testing.T, home string, adjust func(*Env), args ...string) output {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := Env{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Config: testConfig(home), Executable: wdxBinary}
	adjust(&env)
	exit := Run(context.Background(), args, env)
	out := output{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
	wantLogged(t, home, args, out)
	return out
}

type logLine struct {
	Time     string          `json:"time"`
	Level    string          `json:"level"`
	Msg      string          `json:"msg"`
	DebugID  string          `json:"debug_id"`
	Process  string          `json:"process"`
	Pid      int             `json:"pid"`
	Args     json.RawMessage `json:"args"` // a command's arguments, or SQL's
	Exit     *int            `json:"exit"`
	Code     string          `json:"code"`
	Response json.RawMessage `json:"response"`
}

// wantLogged checks that the run that produced out is logged in home from the
// command line it received to the response it returned, with the daemon's
// lines for its requests under the same debug id, and that every line in the
// log is a JSON object tagged with a debug id and a process.
func wantLogged(t *testing.T, home string, args []string, out output) {
	t.Helper()
	if info, err := os.Stat(home); err != nil || !info.IsDir() {
		return // nowhere to log: the run must still work, which its test checks
	}
	files, err := filepath.Glob(filepath.Join(home, "logs", "wdx-*.log"))
	if err != nil || len(files) == 0 {
		t.Fatalf("%v: no log file in %s", args, home)
	}
	var lines []logLine
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for raw := range strings.Lines(string(b)) {
			var l logLine
			if err := json.Unmarshal([]byte(raw), &l); err != nil || l.Time == "" || l.Level == "" || l.Msg == "" || l.DebugID == "" || l.Process == "" {
				t.Fatalf("%v: bad log line %q: %v", args, raw, err)
			}
			lines = append(lines, l)
		}
	}

	// Runs of the same command line in one home are sequential, so the last
	// one received is this one; a daemon started in the home runs others.
	pid := cmp.Or(out.pid, os.Getpid())
	var id string
	for _, l := range lines {
		if l.Msg != "command received" || l.Pid != pid {
			continue
		}
		var received []string
		if json.Unmarshal(l.Args, &received) == nil && slices.Equal(received, args) {
			id = l.DebugID
		}
	}
	var run []logLine
	called, served := 0, 0
	for _, l := range lines {
		if l.DebugID != id {
			continue
		}
		switch {
		case l.Process == "cli":
			run = append(run, l)
			if l.Msg == "daemon called" {
				called++
			}
		case l.Msg == "request returned":
			served++
		}
	}
	first, last := run[0], run[len(run)-1]
	if first.Msg != "command received" || last.Msg != "command returned" {
		t.Fatalf("%v: want the run logged from received to returned, got %q to %q", args, first.Msg, last.Msg)
	}
	if called != served {
		t.Fatalf("%v: the daemon logged %d requests under %s, the cli %d", args, served, id, called)
	}
	if last.Exit == nil || *last.Exit != out.exit || (last.Level == "ERROR") != (out.exit != api.ExitOK) {
		t.Fatalf("%v: logged exit %v at %s, want exit %d", args, last.Exit, last.Level, out.exit)
	}
	if string(last.Response) == "null" || len(last.Response) == 0 {
		t.Fatalf("%v: no response logged", args)
	}
	if strings.Contains(string(last.Response), `\u001b`) {
		t.Fatalf("%v: want the response logged without color, got %s", args, last.Response)
	}
	var printed struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(last.Response, &printed) // a completion logs text, without a code
	if last.Code != printed.Code {
		t.Fatalf("%v: logged code %q, want the response's %q", args, last.Code, printed.Code)
	}
	if slices.Contains(args, "--json") {
		var logged, printed any
		if err := json.Unmarshal([]byte(out.stdout), &printed); err != nil {
			t.Fatalf("%v: stdout is not one JSON object: %s", args, out.stdout)
		}
		_ = json.Unmarshal(last.Response, &logged) // compared to stdout, checked to be JSON
		if obj, ok := printed.(map[string]any); ok {
			if h, ok := obj["history"].(map[string]any); ok {
				h["lines"] = float64(len(h["lines"].([]any))) // terminal output is counted, not logged
			}
		}
		if !reflect.DeepEqual(logged, printed) {
			t.Fatalf("%v: logged response differs from stdout:\n%s\n%s", args, last.Response, out.stdout)
		}
		if out.exit != api.ExitOK && !strings.Contains(out.stdout, `"debug_id": "`+id+`"`) {
			t.Fatalf("%v: want debug_id %s in the error, got %s", args, id, out.stdout)
		}
	} else if out.exit != api.ExitOK && !strings.HasSuffix(escapes.ReplaceAllString(out.stderr, ""), "\n  debug_id: "+id+"\n") {
		t.Fatalf("%v: want debug_id %s on the last line of the error, got %q", args, id, out.stderr)
	}
}

var escapes = regexp.MustCompile("\x1b\\[[0-9;]*m")

type jsonProject struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Origin    *string `json:"origin"`
	State     string  `json:"state"`
	Orphaned  *bool   `json:"orphaned"`
	Worktrees []struct {
		ID      string `json:"id"`
		Path    string `json:"path"`
		Present *bool  `json:"present"`
	} `json:"worktrees"`
	Terminals *[]api.Terminal `json:"terminals"`
}

// jsonOutput holds every field a command prints with --json: a result, or a
// problem.
type jsonStack struct {
	Runtime      string `json:"runtime"`
	Container    string `json:"container"`
	Image        string `json:"image"`
	Status       string `json:"status"`
	GrafanaURL   string `json:"grafana_url"`
	OTLPEndpoint string `json:"otlp_endpoint"`
	Logs         string `json:"logs"`
}

type jsonOutput struct {
	Result  string `json:"result"`
	Dropped []struct {
		Path string `json:"path"`
	} `json:"dropped"`
	Project  *jsonProject `json:"project"`
	Projects []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		State         string `json:"state"`
		WorktreeCount int    `json:"worktree_count"`
	} `json:"projects"`
	Conflict *struct {
		Code string `json:"code"`
	} `json:"conflict"`

	Terminal  *api.Terminal        `json:"terminal"`
	Terminals []api.Terminal       `json:"terminals"`
	History   *api.TerminalHistory `json:"history"`
	Started   []api.Terminal       `json:"started"`
	Stopped   []api.Terminal       `json:"stopped"`

	CLI     *api.Version `json:"cli"`
	Daemon  *api.Daemon  `json:"daemon"`
	Running *bool        `json:"running"`
	Pid     *int         `json:"pid"`

	Update *struct {
		Current      string  `json:"current"`
		Latest       string  `json:"latest"`
		Available    bool    `json:"available"`
		From         string  `json:"from"`
		To           string  `json:"to"`
		Channel      *string `json:"channel"`
		SchemaFrom   int     `json:"schema_from"`
		SchemaTo     int     `json:"schema_to"`
		DataRestored *bool   `json:"data_restored"`
		Settings     *struct {
			Added   []string `json:"added"`
			Dropped []string `json:"dropped"`
		} `json:"settings"`
	} `json:"update"`
	Warnings []string `json:"warnings"`

	Settings *struct {
		Path    string   `json:"path"`
		Added   []string `json:"added"`
		Dropped []string `json:"dropped"`
	} `json:"settings"`

	Runtime     *string    `json:"runtime"`
	Stack       *jsonStack `json:"stack"`
	DataRemoved *bool      `json:"data_removed"`

	Type    string         `json:"type"`
	Title   string         `json:"title"`
	Status  int            `json:"status"`
	Detail  string         `json:"detail"`
	Code    string         `json:"code"`
	Hint    *string        `json:"hint"`
	Details map[string]any `json:"details"`

	Command *struct {
		Path     string `json:"path"`
		Commands []struct {
			Name string `json:"name"`
		} `json:"commands"`
		Examples []struct {
			Description string `json:"description"`
			Command     string `json:"command"`
		} `json:"examples"`
		Flags []struct {
			Name      string  `json:"name"`
			ValueName *string `json:"value_name"`
		} `json:"flags"`
		Topics []struct {
			Name string `json:"name"`
		} `json:"topics"`
		FlagRules []struct {
			Rule  string   `json:"rule"`
			Flags []string `json:"flags"`
			When  *string  `json:"when"`
		} `json:"flag_rules"`
		Results []string `json:"results"`
		Errors  []struct {
			Code string `json:"code"`
			Exit int    `json:"exit"`
		} `json:"errors"`
	} `json:"command"`

	Topic *struct {
		Name     string `json:"name"`
		Text     string `json:"text"`
		Settings []struct {
			Key     string  `json:"key"`
			EnvVar  *string `json:"env_var"`
			Default *string `json:"default"`
		} `json:"settings"`
	} `json:"topic"`
}

// wdxJSON runs args with --json and checks that stdout holds exactly one
// JSON object and stderr nothing: a problem exactly when the run failed.
func wdxJSON(t *testing.T, home string, args ...string) (jsonOutput, int) {
	t.Helper()
	return wdxJSONWith(t, home, func(*Env) {}, args...)
}

// eventually polls ok until it holds, for up to ten seconds, and reports
// whether it did.
func eventually(ok func() bool) bool {
	for deadline := time.Now().Add(10 * time.Second); !ok(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			return false
		}
	}
	return true
}

func wdxJSONWith(t *testing.T, home string, adjust func(*Env), args ...string) (jsonOutput, int) {
	t.Helper()
	out := wdxWith(t, home, adjust, append(args, "--json")...)
	if out.stderr != "" {
		t.Fatalf("%v: want empty stderr, got %q", args, out.stderr)
	}
	var v jsonOutput
	dec := json.NewDecoder(strings.NewReader(out.stdout))
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("%v: stdout is not JSON: %v\n%s", args, err, out.stdout)
	}
	if dec.More() {
		t.Fatalf("%v: want one JSON object, got more:\n%s", args, out.stdout)
	}
	if failed := out.exit != api.ExitOK; failed != (v.Code != "") {
		t.Fatalf("%v: exit %d and code %q disagree", args, out.exit, v.Code)
	}
	if v.Code != "" {
		info := api.Codes[api.ErrorCode(v.Code)]
		if v.Type != "urn:wdx:error:"+v.Code || v.Title != info.Description || v.Status != info.Status || v.Detail == "" || info.Exit != out.exit {
			t.Fatalf("%v: not the problem of %s: %s", args, v.Code, out.stdout)
		}
	}
	wantDeclared(t, args, v)
	return v, out.exit
}

// wantDeclared checks that the help of the command args ran lists the result
// and error codes it returned.
func wantDeclared(t *testing.T, args []string, v jsonOutput) {
	t.Helper()
	cmd, _, err := newRoot(config.Default()).Find(args)
	if err != nil {
		return
	}
	help := newCommandView(cmd)
	var codes []string
	for _, e := range help.Errors {
		codes = append(codes, e.Code)
	}
	if v.Result != "" && !slices.Contains(help.Results, v.Result) {
		t.Fatalf("%v: result %q is not in the help of %s", args, v.Result, help.Path)
	}
	if v.Code != "" && !slices.Contains(codes, v.Code) {
		t.Fatalf("%v: error %q is not in the help of %s", args, v.Code, help.Path)
	}
	if v.Conflict != nil && !slices.Contains(codes, v.Conflict.Code) {
		t.Fatalf("%v: conflict %q is not in the help of %s", args, v.Conflict.Code, help.Path)
	}
}

func wantError(t *testing.T, v jsonOutput, exit int, code api.ErrorCode, wantExit int) {
	t.Helper()
	if v.Code != string(code) || exit != wantExit {
		t.Fatalf("want %s with exit %d, got exit %d and %+v", code, wantExit, exit, v)
	}
}

// newProject creates a project named name in home and returns its id.
func newProject(t *testing.T, home, name string) string {
	t.Helper()
	v, exit := wdxJSON(t, home, "project", "create", "--name", name, "--cancel")
	if exit != api.ExitOK {
		t.Fatalf("create %s: %+v", name, v)
	}
	return v.Project.ID
}

func TestVersion(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	v, exit := wdxJSON(t, home, "version")
	if exit != api.ExitOK || v.CLI.Name != "wdx" || v.CLI.Version != identity.Version || v.Daemon == nil || v.Daemon.Version != identity.Version {
		t.Fatalf("unexpected version: %+v", v)
	}
	if out := wdx(t, home, "version"); out.stdout != "wdx dev (none)\ndaemon dev (none)\n" {
		t.Fatalf("unexpected text: %q", out.stdout)
	}
}

func TestVersionWithoutDaemon(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	v, exit := wdxJSON(t, home, "version")
	if exit != api.ExitOK || v.CLI.Name != "wdx" || v.Daemon != nil {
		t.Fatalf("unexpected version: %+v", v)
	}
	if out := wdx(t, home, "version"); !strings.HasSuffix(out.stdout, "\ndaemon not running\n") {
		t.Fatalf("unexpected text: %q", out.stdout)
	}
}

func TestUsageErrors(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"bogus"},
		{"project", "bogus"},
		{"project", "close"},
		{"project", "close", "app"},
		{"project", "close", "--id", "x", "--name", "y"},
		{"project", "remove"},
		{"project", "remove", "app"},
		{"project", "remove", "--id", "x", "--name", "y"},
		{"version", "--bogus"},
		{"project", "create"},
		{"project", "create", "--name", "app", "--open", "--cancel"},
		{"project", "open", "--local", ".", "--git", "x"},
		{"project", "list", "--open", "--closed"},
		{"project", "list", "app"},
		{"project", "get"},
		{"project", "get", "--id", "x", "--name", "y"},
		{"project", "get", "app"},
	} {
		v, exit := wdxJSON(t, t.TempDir(), args...)
		wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
		wantHint(t, v, "--help'")
	}
	v, _ := wdxJSON(t, t.TempDir(), "project", "create", "--name", "app")
	wantHint(t, v, "pass --open or --cancel; see 'wdx project create --help'")
	out := wdx(t, t.TempDir(), "project", "bogus")
	if out.exit != api.ExitUsage || out.stdout != "" || !strings.Contains(out.stderr, "\n  hint: see 'wdx project --help'\n") {
		t.Fatalf("unexpected text usage error: %+v", out)
	}
}

func TestDaemonUnavailable(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	for _, args := range [][]string{
		{"project", "list"},
		{"project", "get", "--name", "app"},
		{"project", "create", "--name", "app", "--cancel"},
		{"project", "open", "--local", "."},
		{"project", "close", "--name", "app", "--cancel"},
		{"project", "remove", "--name", "app", "--cancel"},
	} {
		v, exit := wdxJSON(t, home, args...)
		wantError(t, v, exit, api.ErrorCodeDaemonUnavailable, api.ExitSystem)
		wantHint(t, v, "run wdx daemon start")
	}
	out := wdx(t, home, "project", "list")
	if out.exit != api.ExitSystem || !strings.HasPrefix(out.stderr, "wdx: the daemon is not answering on "+identity.SocketPath(home)+": connect: ") {
		t.Fatalf("unexpected text error: %+v", out)
	}
}

func TestNoHome(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(home, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	v, exit := wdxJSON(t, home, "project", "close", "--name", "app", "--cancel")
	wantError(t, v, exit, api.ErrorCodeDaemonUnavailable, api.ExitSystem)
}

func wantHint(t *testing.T, v jsonOutput, hint string) {
	t.Helper()
	if v.Hint == nil || !strings.Contains(*v.Hint, hint) {
		t.Fatalf("want hint containing %q, got %+v", hint, v)
	}
}

// TestCommandDocs checks that every command documents itself, so a new
// command fails here until its help is complete.
func TestCommandDocs(t *testing.T) {
	t.Parallel()
	var check func(cmd *cobra.Command)
	check = func(cmd *cobra.Command) {
		path := cmd.CommandPath()
		if cmd.Short == "" {
			t.Errorf("%s: no summary", path)
		}
		if !cmd.HasAvailableSubCommands() && cmd.Annotations[outputNote] == "" {
			t.Errorf("%s: no output note", path)
		}
		for _, code := range annotationList(cmd, errorList) {
			if _, ok := api.Codes[api.ErrorCode(code)]; !ok || code == string(api.ErrorCodeUsage) || code == string(api.ErrorCodeInternal) {
				t.Errorf("%s: unexpected error code %q", path, code)
			}
		}
		if !cmd.HasAvailableSubCommands() && len(parseExamples(cmd.Example)) == 0 {
			t.Errorf("%s: no examples", path)
		}
		flags := cmd.LocalFlags()
		if !cmd.HasParent() {
			flags = cmd.PersistentFlags()
		}
		flags.VisitAll(func(f *pflag.Flag) {
			if f.Value.Type() == "bool" {
				return
			}
			if name, _ := pflag.UnquoteUsage(f); !strings.Contains(f.Usage, "`"+name+"`") {
				t.Errorf("%s: --%s does not name its value with backticks in %q", path, f.Name, f.Usage)
			}
			if _, ok := cmd.GetFlagCompletionFunc(f.Name); !ok {
				t.Errorf("%s: --%s completes nothing; register its completion, cobra.NoFileCompletions if none", path, f.Name)
			}
		})
		if cmd.Runnable() && cmd.ValidateArgs([]string{"x"}) == nil {
			t.Errorf("%s: takes a positional argument; every input must be a flag", path)
		}
		for _, r := range flagRules(cmd) {
			names := r.Flags
			if r.When != nil {
				names = append(names, *r.When)
			}
			for _, name := range names {
				if cmd.Flags().Lookup(name) == nil {
					t.Errorf("%s: rule %s names unknown flag %q", path, r.Rule, name)
				}
			}
		}
		for _, sub := range cmd.Commands() {
			if sub.IsAvailableCommand() {
				check(sub)
			}
		}
	}
	check(newRoot(config.Default()))
}

func TestHelp(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args       []string
		want, deny []string
	}{
		{[]string{"--help"}, []string{"wdx: manage AI coding agents", "wdx <command> [flags]", "completion ", "daemon ", "observe ", "project ", "version ", "--color <mode>", "Exit codes:", "Help topics:", "settings ", "wdx help <topic>"},
			[]string{"\n  help ", "WDX_HOME", "Global flags:", "Errors:"}},
		{[]string{"daemon", "-h"}, []string{"wdx daemon <command> [flags]", "restart ", "start ", "status ", "stop ", "Global flags: --color <mode>, --json, --no-input, -h/--help"}, []string{"Errors:", "[command]", "Examples:"}},
		{[]string{"project", "-h"}, []string{"close ", "create ", "get ", "list ", "open ", "remove ", "Run wdx project <command> --help"}, []string{"Errors:", "\n  --json"}},
		{[]string{"project", "open", "--help"}, []string{
			"wdx project open: open a git repository as a project\n\nA path inside",
			"Examples:\n  # Open the repository you are in\n  wdx project open --local .\n",
			"--local <path>", "--git <url>", "--reclone ",
			"Flag rules:\n  exactly one of --local, --git\n  --reclone: not with --local, --remove\n  --remove: not with --local, --reclone\n  with --json and --git: one of --reclone, --remove\n",
			"Output:", "result: cloned", "checkout_missing    1", "daemon_unavailable  3", "internal            3", "Global flags:",
		}, []string{"Commands:", "Help topics:", "\n  --json"}},
		{[]string{"project", "create", "--help"}, []string{"Flag rules:\n  --name is required\n  at most one of --open, --cancel\n  with --json: one of --open, --cancel\n"}, nil},
		{[]string{"help", "project", "get"}, []string{"wdx project get (--id <id> | --name <name>)", "exactly one of --id, --name", "not_found"}, nil},
		{[]string{"help", "settings"}, []string{"wdx help settings: ", "WDX_HOME", "log.level", "color ", "NO_COLOR"}, []string{"Usage:"}},
		{[]string{"help", "json"}, []string{"wdx help json: ", "Problem Details"}, nil},
		{[]string{"help", "logs"}, []string{"wdx help logs: ", "debug_id"}, nil},
	} {
		out := wdx(t, t.TempDir(), tc.args...)
		if out.exit != api.ExitOK || out.stderr != "" {
			t.Fatalf("%v: unexpected %+v", tc.args, out)
		}
		for _, s := range tc.want {
			if !strings.Contains(out.stdout, s) {
				t.Errorf("%v: want %q in:\n%s", tc.args, s, out.stdout)
			}
		}
		for _, s := range tc.deny {
			if strings.Contains(out.stdout, s) {
				t.Errorf("%v: unexpected %q in:\n%s", tc.args, s, out.stdout)
			}
		}
	}
}

// TestHelpJSON walks the whole command tree through --help --json.
func TestHelpJSON(t *testing.T) {
	t.Parallel()
	var walk func(path []string)
	walk = func(path []string) {
		v, exit := wdxJSON(t, t.TempDir(), append(path, "--help")...)
		c := v.Command
		if exit != api.ExitOK || c == nil || c.Path != strings.Join(append([]string{"wdx"}, path...), " ") {
			t.Fatalf("%v: unexpected help %+v", path, v)
		}
		if n := len(c.Errors); n < 2 || c.Errors[n-2].Code != string(api.ErrorCodeUsage) || c.Errors[n-1].Code != string(api.ErrorCodeInternal) {
			t.Fatalf("%v: errors must end with usage and internal: %+v", path, c.Errors)
		}
		for _, sub := range c.Commands {
			walk(append(slices.Clone(path), sub.Name))
		}
	}
	walk(nil)

	v, _ := wdxJSON(t, t.TempDir(), "--help")
	if c := v.Command; len(c.Topics) != 3 || c.Topics[0].Name != "json" {
		t.Fatalf("unexpected topics: %+v", c.Topics)
	}
	v, _ = wdxJSON(t, t.TempDir(), "help", "project", "open")
	if c := v.Command; len(c.Examples) != 3 || c.Examples[0].Command != "wdx project open --local ." || c.Examples[0].Description != "Open the repository you are in" || c.Topics != nil {
		t.Fatalf("unexpected examples or topics: %+v", c)
	}
	for _, f := range v.Command.Flags {
		got := "null"
		if f.ValueName != nil {
			got = *f.ValueName
		}
		if want, ok := map[string]string{"local": "path", "git": "url", "reclone": "null"}[f.Name]; ok && got != want {
			t.Fatalf("--%s: want value name %s, got %s", f.Name, want, got)
		}
	}
	rule := v.Command.FlagRules[len(v.Command.FlagRules)-1]
	if rule.Rule != "one_required_with_json" || *rule.When != "git" || !slices.Equal(rule.Flags, []string{"reclone", "remove"}) {
		t.Fatalf("unexpected json rule: %+v", rule)
	}
	if !slices.Equal(v.Command.Results, []string{"cloned", "opened", "registered", "removed"}) {
		t.Fatalf("unexpected results: %v", v.Command.Results)
	}
}

func TestPanicIsInternalError(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "boom", RunE: func(*cobra.Command, []string) error { panic("kaboom") }}
	cmd.SetContext(context.Background())
	wrapRun(cmd)
	err := cmd.RunE(cmd, nil)
	var p *panicError
	if !errors.As(err, &p) || p.Error() != "panic: kaboom" || !strings.Contains(string(p.stack), "TestPanicIsInternalError") {
		t.Fatalf("want a panicError with its stack, got %v", err)
	}
	if p, exit := problem(err); p.Code != api.ErrorCodeInternal || exit != api.ExitSystem {
		t.Fatalf("want internal, got %s", p.Code)
	}
}

func TestConfigError(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	path := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, []byte("log:\n  level: loud\n  retention_days: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(func(key string) string { return map[string]string{"WDX_HOME": home}[key] })
	if err == nil {
		t.Fatal("want bad settings rejected")
	}
	var stdout, stderr bytes.Buffer
	env := Env{Stdout: &stdout, Stderr: &stderr}
	if exit := ConfigError([]string{"project", "list", "--json"}, env, err); exit != api.ExitUsage {
		t.Fatalf("want exit %d, got %d", api.ExitUsage, exit)
	}
	var v struct {
		Code    string `json:"code"`
		Details struct {
			Settings []struct {
				From  string `json:"from"`
				Value string `json:"value"`
			} `json:"settings"`
		} `json:"details"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &v); err != nil || stderr.Len() != 0 {
		t.Fatalf("want one JSON error, got %q, %q", stdout.String(), stderr.String())
	}
	if s := v.Details.Settings; v.Code != string(api.ErrorCodeUsage) || len(s) != 2 || s[0].From != path+":2" || s[0].Value != "loud" {
		t.Fatalf("unexpected error %+v", v)
	}

	stdout.Reset()
	ConfigError([]string{"version"}, env, err)
	want := "wdx: invalid settings: " + path + `:2: log.level: "loud" is not one of debug, info, warn, error; ` +
		path + `:3: log.retention_days: "0" is not a whole number of at least 1  [usage]` + "\n  hint: fix or remove them\n"
	if stderr.String() != want || stdout.Len() != 0 {
		t.Fatalf("got %q, want %q", stderr.String(), want)
	}
}
