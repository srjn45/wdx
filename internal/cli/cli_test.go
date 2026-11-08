package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/aca/acatest"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/clikit/clikittest"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/daemon"
	"github.com/srjn45/wdx/internal/db/dbtest"
	"github.com/srjn45/wdx/internal/gh/ghtest"
	"github.com/srjn45/wdx/internal/holder/holdertest"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/logtest"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/service"
	"github.com/srjn45/wdx/internal/waittest"
)

// wdxBinary is wdx built from this tree, which starting the daemon in the
// background runs; releaseBinary is the same built as release
// releaseVersion, which the update tests install.
var wdxBinary, releaseBinary string

const releaseVersion = "0.2.0"

func TestMain(m *testing.M) {
	done := runtest.Start()
	// make test-changed runs many processes of these tests, which need only one
	// build, in a directory it removes.
	dir := sharedBinaries()
	shared := dir != ""
	if !shared {
		var err error
		if dir, err = os.MkdirTemp("", "bin"); err != nil {
			panic(err)
		}
	}
	wdxBinary = filepath.Join(dir, identity.Name)
	releaseBinary = filepath.Join(dir, "release", identity.Name)
	build := func(path string, args ...string) {
		if _, err := os.Stat(path); shared && err == nil {
			return
		}
		c := exec.Command("go", append(append([]string{"build", "-o", path}, args...), "../../cmd/wdx")...)
		c.Env = runtest.OriginalEnviron() // the go command's caches are the machine's
		if out, err := c.CombinedOutput(); err != nil {
			panic(fmt.Sprintf("build %s: %v\n%s", path, err, out))
		}
	}
	// Without debug tables, as a release: a quicker link, and less to copy.
	var built sync.WaitGroup
	built.Go(func() { build(wdxBinary, "-ldflags", "-s -w") })
	built.Go(func() {
		build(releaseBinary, "-ldflags", "-s -w -X github.com/srjn45/wdx/internal/identity.Version="+releaseVersion)
	})
	built.Wait()
	var err error
	if archivesDir, err = os.MkdirTemp("", "archives"); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(archivesDir)
	if !shared {
		os.RemoveAll(dir)
	}
	os.Exit(done(code))
}

// sharedBinaries is the directory make test-changed gives for the wdx binaries,
// or "".
func sharedBinaries() string {
	for _, kv := range runtest.OriginalEnviron() {
		if dir, ok := strings.CutPrefix(kv, "WDX_TEST_BINARIES="); ok {
			return dir
		}
	}
	return ""
}

// shortHome returns an empty home under /tmp: its socket path must fit the
// 104 bytes macOS allows, which a path under t.TempDir() may exceed.
func shortHome(t *testing.T) string {
	t.Helper()
	home := runtest.Dir(t)
	// Runs before the home goes, after the daemon and holders in it stopped.
	logtest.OnFailure(t, home)
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
// is hung up on. Given a command, as an agent's holder gives it, it runs it
// as a login shell does, skipping the -l and -i.
const testShell = runtest.LoginShellScript

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
	return newHomeOf(t, script, nil)
}

// newHomeWithACAs is newHome whose daemon runs acas.
func newHomeWithACAs(t *testing.T, acas ...aca.AiCodingAgent) string {
	t.Helper()
	return newHomeOf(t, testShell, acas)
}

// newHomeOf is newHome with script as its terminals' shell, whose daemon runs acas,
// the test ACA when nil.
func newHomeOf(t *testing.T, script string, acas []aca.AiCodingAgent) string {
	t.Helper()
	return newHomeOpts(t, script, acas, func(*daemon.Options) {})
}

// newHomeOpts is newHomeOf whose daemon's options adjust changes.
func newHomeOpts(t *testing.T, script string, acas []aca.AiCodingAgent, adjust func(*daemon.Options)) string {
	t.Helper()
	home := shortHome(t)
	if acas == nil {
		acas = []aca.AiCodingAgent{&acatest.ACA{Launches: launchesPath(home)}}
	}
	dbtest.Seed(t, identity.DBPath(home))
	shell := filepath.Join(t.TempDir(), "shell")
	if err := runtest.WriteExecutable(shell, script); err != nil {
		t.Fatal(err)
	}
	logs := logging.NewDailyFile(identity.LogsDir(home), identity.Name, 7, time.Now)
	log := logging.New(logs, slog.LevelDebug).With("process", "daemon")
	ctx, stop := context.WithCancel(context.Background())
	opts := daemon.Options{
		Home: home, Log: log, GH: ghtest.New(t, home).Program,
		Executable: wdxBinary, ShellEnv: []string{"PATH=/usr/bin:/bin"}, TerminalShell: shell,
		TerminalHistoryLines: 100_000,
		StopReport:           waittest.Limit(t), HolderLimits: holdertest.Limits(t),
		ACAs: acas, LookProgram: acatest.LookProgram, SettingStore: settingStore{home: home},
	}
	adjust(&opts)
	d, err := daemon.Listen(ctx, opts)
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

// launchesPath is the file where the test agents of home record the session
// of each start.
func launchesPath(home string) string {
	return filepath.Join(home, "test-launches")
}

// removeAgents removes agents and all under them, children first: a list holds
// the top level only, and a parent with children would stay as a tombstone.
func removeAgents(ctx context.Context, c *client.Client, agents []api.Agent) {
	for _, a := range agents {
		if a.Children != nil {
			removeAgents(ctx, c, *a.Children)
		}
		_, _ = c.DeleteAgent(ctx, "id:"+a.Id, "", "", true) // the daemon logs a failure, and the test fails on a leftover holder
	}
}

// removeTerminals removes every terminal and agent in home, stopping the live
// ones, so no holder outlives the test.
func removeTerminals(home string) {
	ctx := context.Background()
	c := client.New(client.Options{Socket: identity.SocketPath(home), DebugID: logging.NewDebugID()})
	if agents, err := c.ListAgents(ctx, "", ""); err == nil {
		removeAgents(ctx, c, agents.Agents)
	}
	list, err := c.ListTerminals(ctx, "", "")
	if err != nil {
		return
	}
	for _, term := range list.Terminals {
		_, _ = c.DeleteTerminal(ctx, "id:"+term.Id, "", "", true) // the daemon logs a failure, and the test fails on a leftover holder
	}
}

// wdx runs the command line args against home, which stands in for ~/.wdx.
func wdx(t *testing.T, home string, args ...string) clikittest.Output {
	t.Helper()
	return wdxService(t, home, nil, args...)
}

// wdxService runs args with svc as the service the daemon is installed as.
func wdxService(t *testing.T, home string, svc *service.Manager, args ...string) clikittest.Output {
	t.Helper()
	return wdxWith(t, home, func(env *Env) { env.Service = svc }, args...)
}

// wdxWith runs args in an environment that adjust changes.
func wdxWith(t *testing.T, home string, adjust func(*Env), args ...string) clikittest.Output {
	t.Helper()
	out := runWdx(context.Background(), t, home, adjust, args...)
	wantLogged(t, home, args, out)
	return out
}

// runWdx runs args in an environment that adjust changes, until ctx ends,
// checking nothing, so that a goroutine may: the caller checks the run with
// wantLogged. Its stderr is what adjust sets, else kept in the output.
func runWdx(ctx context.Context, t *testing.T, home string, adjust func(*Env), args ...string) clikittest.Output {
	var stdout, stderr bytes.Buffer
	env := Env{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Config: testConfig(home), Executable: wdxBinary, Environ: os.Environ()}
	// A machine slowed down by other tests slows a run rather than fails it.
	env.Timeouts.Completion = waittest.Limit(t)
	limit := holdertest.Patience(t)
	env.Timeouts.Start, env.Timeouts.AttachExit, env.Timeouts.UpdatePreflight = limit, limit, limit
	env.Timeouts.Holder = holdertest.Limits(t)
	adjust(&env)
	exit := Run(ctx, args, env)
	out := clikittest.Output{Exit: exit, Stdout: stdout.String(), Stderr: stderr.String()}
	if s, ok := env.Stderr.(fmt.Stringer); ok {
		out.Stderr = s.String()
	}
	return out
}

// wantLogged checks that wdx and its daemon logged the run of args in home.
func wantLogged(t *testing.T, home string, args []string, out clikittest.Output) {
	t.Helper()
	clikittest.WantLogged(t, clikittest.Logged{Dir: identity.LogsDir(home), Called: "daemon called", Abandoned: "daemon unreachable", Served: "request returned"}, args, out, redacted(args, out))
}

// redacted turns what the run of args printed into what it logged: what wdx
// measures or counts rather than logs.
func redacted(args []string, out clikittest.Output) func(printed map[string]any) {
	return func(obj map[string]any) {
		replace := func(b []byte, err error) {
			if err != nil {
				return
			}
			clear(obj)
			_ = json.Unmarshal(b, &obj)
		}
		if (slices.Contains(args, "worktree") || slices.Contains(args, "message")) && out.Exit == clikit.ExitOK {
			// A file's contents and comments are measured, not logged.
			replace(json.Marshal(api.WorktreeResponseLogged([]byte(out.Stdout))))
		}
		if obj["agent"] != nil || obj["agents"] != nil || obj["project"] != nil {
			// A prompt's text is the agent's: its kind and options are counted, not logged.
			replace(json.Marshal(api.AgentResponseLogged([]byte(out.Stdout))))
		}
		if h, ok := obj["history"].(map[string]any); ok {
			h["lines"] = float64(len(h["lines"].([]any))) // terminal output is counted, not logged
		}
		if r, ok := obj["transcript"].(map[string]any); ok {
			r["lines"] = float64(len(r["lines"].([]any))) // a conversation is counted, not logged
		}
		if content, ok := obj["content"].(string); ok && obj["doc"] != nil {
			delete(obj, "content") // a doc is measured, not logged
			obj["content_bytes"] = float64(len(content))
		}
		if obj["plan"] != nil && (obj["content"] != nil || obj["definition"] != nil) {
			// A plan's text is measured, not logged.
			replace(json.Marshal(api.PlanResponseLogged([]byte(out.Stdout))))
		}
		if obj["task"] != nil && obj["definition"] == nil {
			// A task's prompt and summary are measured, not logged.
			replace(json.Marshal(api.PlanTaskResponseLogged([]byte(out.Stdout))))
		}
	}
}

// wantDeclared checks that the help of the command args run lists what v
// holds: its result, or its codes.
func wantDeclared(t *testing.T, args []string, v jsonOutput) {
	t.Helper()
	var conflict string
	if v.Conflict != nil {
		conflict = v.Conflict.Code
	}
	clikittest.WantDeclared(t, newRoot(config.Default()), args, v.Result, v.Code, conflict)
}

// codes is what wdx's error codes say about code.
func codes(code string) (clikit.CodeInfo, bool) {
	info, ok := api.Codes[api.ErrorCode(code)]
	return info, ok
}

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
	Agents    *[]api.Agent    `json:"agents"`
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
	WorktreeCreated *bool    `json:"worktree_created"`
	Result          string   `json:"result"`
	Moved           []string `json:"moved"`
	Dropped         []struct {
		Path  string `json:"path"`  // a worktree project open dropped
		Entry string `json:"entry"` // an after a plan's reduction dropped
		After string `json:"after"`
		Via   string `json:"via"`
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

	Sync           *api.ProjectSync    `json:"sync"`
	Doc            *api.Doc            `json:"doc"`
	Docs           []api.Doc           `json:"docs"`
	Revisions      []api.DocRevision   `json:"revisions"`
	Revision       *int64              `json:"revision"`
	Content        *string             `json:"content"`
	PreviousStatus *api.DocStatus      `json:"previous_status"`
	Plan           *api.Plan           `json:"plan"`
	Plans          []api.Plan          `json:"plans"`
	Definition     *api.PlanDefinition `json:"definition"`
	Run            *api.PlanRun        `json:"run"`
	Task           *api.PlanTask       `json:"task"`
	Path           string              `json:"path"`
	Written        []string            `json:"written"`
	Removed        []string            `json:"removed"`
	Skipped        []string            `json:"skipped"`

	Terminal   *api.Terminal        `json:"terminal"`
	Terminals  []api.Terminal       `json:"terminals"`
	Agent      *api.Agent           `json:"agent"`
	Agents     []api.Agent          `json:"agents"`
	Aca        string               `json:"aca"`
	Models     []api.AcaModel       `json:"models"`
	Usage      []api.UsageWindow    `json:"usage"`
	Acas       []api.Aca            `json:"acas"`
	History    *api.TerminalHistory `json:"history"`
	Transcript *api.AgentTranscript `json:"transcript"`
	Started    []api.Terminal       `json:"started"`
	Stopped    []api.Terminal       `json:"stopped"`

	StartedAgents []api.Agent `json:"started_agents"`
	StoppedAgents []api.Agent `json:"stopped_agents"`

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

	Setting *struct {
		Key     string `json:"key"`
		Value   string `json:"value"`
		Default string `json:"default"`
		From    string `json:"from"`
	} `json:"setting"`
	Reload          string   `json:"reload"`
	DroppedSettings []string `json:"dropped_settings"`

	Settings *struct {
		Path    string   `json:"path"`
		Added   []string `json:"added"`
		Dropped []string `json:"dropped"`
	} `json:"settings"`

	System *struct {
		OS             string  `json:"os"`
		Distribution   *string `json:"distribution"`
		PackageManager *string `json:"package_manager"`
		Session        *string `json:"session"`
	} `json:"system"`
	Dependencies []struct {
		Name      string   `json:"name"`
		Mandatory bool     `json:"mandatory"`
		Features  []string `json:"features"`
		Status    string   `json:"status"`
		Program   *string  `json:"program"`
		Version   *string  `json:"version"`
		Detail    *string  `json:"detail"`
	} `json:"dependencies"`

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
			Rule   string   `json:"rule"`
			Flags  []string `json:"flags"`
			When   *string  `json:"when"`
			Unless *string  `json:"unless"`
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

// eventually polls ok until it holds, until t's deadline, and reports
// whether it did.
func eventually(t *testing.T, ok func() bool) bool { return waittest.Until(t, ok) }

func wdxJSONWith(t *testing.T, home string, adjust func(*Env), args ...string) (jsonOutput, int) {
	t.Helper()
	out := wdxWith(t, home, adjust, append(args, "--json")...)
	var v jsonOutput
	if err := json.Unmarshal(clikittest.JSON(t, args, out, codes), &v); err != nil {
		t.Fatalf("%v: stdout is not JSON: %v\n%s", args, err, out.Stdout)
	}
	wantDeclared(t, args, v)
	return v, out.Exit
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
	if exit != clikit.ExitOK {
		t.Fatalf("create %s: %+v", name, v)
	}
	return v.Project.ID
}

func TestVersion(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	v, exit := wdxJSON(t, home, "version")
	if exit != clikit.ExitOK || v.CLI.Name != "wdx" || v.CLI.Version != identity.Version || v.Daemon == nil || v.Daemon.Version != identity.Version {
		t.Fatalf("unexpected version: %+v", v)
	}
	if out := wdx(t, home, "version"); out.Stdout != "wdx dev (none), API 1\ndaemon dev (none), API 1\n" {
		t.Fatalf("unexpected text: %q", out.Stdout)
	}
}

func TestVersionWithoutDaemon(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	v, exit := wdxJSON(t, home, "version")
	if exit != clikit.ExitOK || v.CLI.Name != "wdx" || v.Daemon != nil {
		t.Fatalf("unexpected version: %+v", v)
	}
	if out := wdx(t, home, "version"); !strings.HasSuffix(out.Stdout, "\ndaemon not running\n") {
		t.Fatalf("unexpected text: %q", out.Stdout)
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
		wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)
		wantHint(t, v, "--help'")
	}
	v, _ := wdxJSON(t, t.TempDir(), "project", "create", "--name", "app")
	wantHint(t, v, "pass --open or --cancel; see 'wdx project create --help'")
	out := wdx(t, t.TempDir(), "project", "bogus")
	if out.Exit != clikit.ExitUsage || out.Stdout != "" || !strings.Contains(out.Stderr, "\n  hint: see 'wdx project --help'\n") {
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
		{"project", "open", "--local", home},
		{"project", "close", "--name", "app", "--cancel"},
		{"project", "remove", "--name", "app", "--cancel"},
	} {
		v, exit := wdxJSON(t, home, args...)
		wantError(t, v, exit, api.ErrorCodeDaemonUnavailable, clikit.ExitSystem)
		wantHint(t, v, "run wdx daemon start")
	}
	out := wdx(t, home, "project", "list")
	if out.Exit != clikit.ExitSystem || !strings.HasPrefix(out.Stderr, "wdx: the daemon is not answering on "+identity.SocketPath(home)+": connect: ") {
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
	wantError(t, v, exit, api.ErrorCodeDaemonUnavailable, clikit.ExitSystem)
}

func wantHint(t *testing.T, v jsonOutput, hint string) {
	t.Helper()
	if v.Hint == nil || !strings.Contains(*v.Hint, hint) {
		t.Fatalf("want hint containing %q, got %+v", hint, v)
	}
}

// TestCommandDocs checks that every command documents itself, so a new
// command fails here until its help is complete, and lists only wdx's codes.
func TestCommandDocs(t *testing.T) {
	t.Parallel()
	root := newRoot(config.Default())
	clikittest.CheckDocs(t, root)
	var check func(cmd *cobra.Command)
	check = func(cmd *cobra.Command) {
		for _, code := range clikit.AnnotationList(cmd, clikit.Errors) {
			if _, ok := api.Codes[api.ErrorCode(code)]; !ok {
				t.Errorf("%s: unexpected error code %q", cmd.CommandPath(), code)
			}
		}
		for _, sub := range cmd.Commands() {
			if sub.IsAvailableCommand() {
				check(sub)
			}
		}
	}
	check(root)
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
			"wdx project open: open a project: a known one, a local repository or a clone\n\nWith no flags",
			"Examples:\n  # Open the repository you are in\n  wdx project open --local .\n",
			"--local <path>", "--git <url>", "--reclone ",
			"--id: not with --git, --local, --name\n  --local: not with --git, --id, --reclone, --remove\n  --name: not with --id\n  --reclone: not with --local, --remove\n  --remove: not with --local, --reclone\n  with --json and --git: one of --reclone, --remove\n  with --json and --id: one of --reclone, --remove\n  with --json and --name, unless --local: one of --reclone, --remove\n",
			"Output:", "result: cloned", "checkout_missing    1", "daemon_unavailable  3", "internal            3", "Global flags:",
		}, []string{"Commands:", "Help topics:", "\n  --json"}},
		{[]string{"project", "create", "--help"}, []string{"Flag rules:\n  --name is required\n  at most one of --open, --cancel\n  with --json: one of --open, --cancel\n"}, nil},
		{[]string{"help", "project", "get"}, []string{"wdx project get (--id <id> | --name <name>)", "exactly one of --id, --name", "not_found"}, nil},
		{[]string{"help", "settings"}, []string{"wdx help settings: ", "WDX_HOME", "log.level", "color ", "NO_COLOR"}, []string{"Usage:"}},
		{[]string{"help", "json"}, []string{"wdx help json: ", "Problem Details"}, nil},
		{[]string{"help", "logs"}, []string{"wdx help logs: ", "debug_id"}, nil},
		{[]string{"help", "agents"}, []string{"wdx help agents: ", "WDX_AGENT_ID", "wdx doc update --name <doc> --path <file> --base 4", "wdx plan task done --agent-id $WDX_AGENT_ID --summary <text>"}, nil},
		{[]string{"help", "plans"}, []string{"wdx plan run --name sso", "plan.parallel", "wdx plan task done --agent-id $WDX_AGENT_ID --summary <text>"}, nil},
	} {
		out := wdx(t, t.TempDir(), tc.args...)
		if out.Exit != clikit.ExitOK || out.Stderr != "" {
			t.Fatalf("%v: unexpected %+v", tc.args, out)
		}
		for _, s := range tc.want {
			if !strings.Contains(out.Stdout, s) {
				t.Errorf("%v: want %q in:\n%s", tc.args, s, out.Stdout)
			}
		}
		for _, s := range tc.deny {
			if strings.Contains(out.Stdout, s) {
				t.Errorf("%v: unexpected %q in:\n%s", tc.args, s, out.Stdout)
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
		if exit != clikit.ExitOK || c == nil || c.Path != strings.Join(append([]string{"wdx"}, path...), " ") {
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
	if c := v.Command; len(c.Topics) != 5 || c.Topics[0].Name != "json" || c.Topics[4].Name != "plans" {
		t.Fatalf("unexpected topics: %+v", c.Topics)
	}
	v, _ = wdxJSON(t, t.TempDir(), "help", "project", "open")
	if c := v.Command; len(c.Examples) != 5 || c.Examples[0].Command != "wdx project open --local ." || c.Examples[0].Description != "Open the repository you are in" || c.Topics != nil {
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
	if rule.Rule != "one_required_with_json" || *rule.When != "name" || *rule.Unless != "local" || !slices.Equal(rule.Flags, []string{"reclone", "remove"}) {
		t.Fatalf("unexpected json rule: %+v", rule)
	}
	if !slices.Equal(v.Command.Results, []string{"cloned", "created", "opened", "registered", "removed"}) {
		t.Fatalf("unexpected results: %v", v.Command.Results)
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
	if exit := ConfigError([]string{"project", "list", "--json"}, env, err); exit != clikit.ExitUsage {
		t.Fatalf("want exit %d, got %d", clikit.ExitUsage, exit)
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
