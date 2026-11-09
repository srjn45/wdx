package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/clikit/clikittest"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/identity"
)

// laptopDir is a working directory on the laptop with the files the
// examples read there.
func laptopDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []string{"docs/auth.md", "notes.md", "auth-rfc.md"} {
		path := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# auth\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	plan := "tasks:\n  - name: one\n    prompt: do it\n"
	if err := os.WriteFile(filepath.Join(dir, "sso.yaml"), []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// onLaptop runs args on the laptop in dir, adjust changing its environment.
func (p *hubPair) onLaptop(dir string, adjust func(*Env), args ...string) clikittest.Output {
	p.laptop.t.Helper()
	return wdxWith(p.laptop.t, p.laptop.home, p.laptop.env(func(env *Env) {
		env.WorkDir = dir
		if adjust != nil {
			adjust(env)
		}
	}), args...)
}

// runID is the debug id the last run of args in home was logged under.
func runID(t *testing.T, home string, args []string) string {
	t.Helper()
	lines := clikittest.LogLines(t, identity.LogsDir(home), args)
	for i := len(lines) - 1; i >= 0; i-- {
		var received []string
		if l := lines[i]; l.Msg == "command received" && json.Unmarshal(l.Args, &received) == nil && slices.Equal(received, clikit.ArgsLogged(args)) {
			return l.DebugID
		}
	}
	t.Fatalf("%v: no run logged", args)
	return ""
}

// served is how many requests home's daemon logged received under id.
func served(t *testing.T, home, id string) int {
	t.Helper()
	n := 0
	for _, l := range clikittest.LogLines(t, identity.LogsDir(home), nil) {
		if l.DebugID == id && l.Process == "daemon" && l.Msg == "request received" {
			n++
		}
	}
	return n
}

// connected is whether home's daemon switched a connect under id to a stream.
func connected(t *testing.T, home, id string) bool {
	t.Helper()
	return lineOf(logLines(t, home, id), "request returned", map[string]any{"status": 101, "response": "byte stream"}) != nil
}

// nodeLeaves are the leaf commands that take --node to run on another node.
func nodeLeaves() []*cobra.Command {
	var leaves []*cobra.Command
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Runnable() && !cmd.HasSubCommands() && nodeOf(cmd) == "" && cmd.Flag(nodeFlag) != nil && cmd.Flag(nodeFlag).Annotations[remoteMark] != nil {
			leaves = append(leaves, cmd)
		}
		for _, sub := range cmd.Commands() {
			if sub.IsAvailableCommand() {
				walk(sub)
			}
		}
	}
	walk(newRoot(config.Default()))
	return leaves
}

// The refusals of --node before the host is asked: the project from the
// working directory, and a relative path the host's daemon would receive.
var nodeRefusals = []string{"pass --project-id or --project-name with --node", "path must be absolute on node desktop",
	// project open with no flag, which is refused without --node too.
	"nothing to open",
	// tide with no terminal to run in, which it needs before it reaches any node.
	"no terminal to run TIDE in"}

// TestNodeExamples runs every example of every command with --node on the
// desktop, from the laptop: each reaches the desktop's daemon under the
// run's debug id, through a stream the laptop's daemon switched, or is
// refused before for what --node does not allow.
func TestNodeExamples(t *testing.T) {
	t.Parallel()
	p := openPair(t)
	newProject(t, p.desktop.home, "app")
	dir := laptopDir(t)
	leaves := nodeLeaves()
	if len(leaves) < 50 {
		t.Fatalf("%d commands with --node, want every command of the groups", len(leaves))
	}
	for _, cmd := range leaves {
		reachedOnce := false
		for _, e := range clikit.ParseExamples(cmd.Example) {
			if strings.Contains(e.Command, "://") {
				continue // it would clone over the network
			}
			args := append(strings.Fields(e.Command)[1:], "--node", "desktop")
			out := p.onLaptop(dir, nil, args...)
			id := runID(t, p.laptop.home, args)
			asJSON := slices.Contains(args, "--json")
			if asJSON {
				var v jsonOutput
				if err := json.Unmarshal(clikittest.JSON(t, args, out, codes), &v); err != nil {
					t.Fatalf("%v: %v", args, err)
				}
				wantDeclared(t, args, v)
			}
			if served(t, p.desktop.home, id) > 0 {
				reachedOnce = true
				if !connected(t, p.laptop.home, id) {
					t.Errorf("%v: reached the desktop with no connect switched by the laptop's daemon", args)
				}
				if !asJSON && !strings.HasPrefix(out.Stderr, "on node desktop\n") {
					t.Errorf("%v: stderr %q, want it to start with on node desktop", args, out.Stderr)
				}
				continue
			}
			refusal := out.Stderr + out.Stdout
			if out.Exit != clikit.ExitUsage || !slices.ContainsFunc(nodeRefusals, func(r string) bool { return strings.Contains(refusal, r) }) {
				t.Errorf("%v: reached nothing on the desktop, exit %d:\n%s%s", args, out.Exit, out.Stdout, out.Stderr)
			}
			if served(t, p.laptop.home, id) > 0 {
				t.Errorf("%v: refused, yet it asked the laptop's daemon", args)
			}
			// Refused for the working directory, the project named reaches it.
			if strings.Contains(refusal, "no terminal to run TIDE in") {
				reachedOnce = true // TestNodeTide reaches the desktop from a terminal
			}
			if strings.Contains(refusal, nodeRefusals[0]) && cmd.Flag("project-name") != nil {
				args = append(args, "--project-name", "app")
				if cmd.Flag("yes") != nil && !slices.Contains(args, "--yes") {
					args = append(args, "--yes") // asked before the daemon is
				}
				p.onLaptop(dir, nil, args...)
				if served(t, p.desktop.home, runID(t, p.laptop.home, args)) == 0 {
					t.Errorf("%v: reached nothing on the desktop", args)
				}
				reachedOnce = true
			}
		}
		if !reachedOnce && cmd.Annotations[noNode] == "" {
			t.Errorf("%s: no example reached the desktop", cmd.CommandPath())
		}
	}
}

func TestNodeSessionRequired(t *testing.T) {
	t.Parallel()
	p := newHubPair(t)
	p.setUp()
	p.remoteAccess(true)
	for _, extra := range [][]string{{"--json"}, {"--no-input", "--json"}} {
		args := append([]string{"agent", "list", "--all", "--node", "desktop"}, extra...)
		out := p.onLaptop("", nil, args...)
		var v jsonOutput
		_ = json.Unmarshal(clikittest.JSON(t, args, out, codes), &v)
		wantError(t, v, out.Exit, api.ErrorCodeSessionRequired, clikit.ExitRejected)
		wantDeclared(t, args, v)
		if v.Hint == nil || !strings.Contains(*v.Hint, "wdx hub session open --node desktop --code <code>") {
			t.Errorf("%v: %s, want the hint to open a session", args, out.Stdout)
		}
	}
	// Without a terminal, in text, it is not asked either.
	out := p.onLaptop("", nil, "agent", "list", "--all", "--node", "desktop")
	if out.Exit != clikit.ExitRejected || strings.Contains(out.Stderr, "code for") || !strings.Contains(out.Stderr, "[session_required]") {
		t.Errorf("without a terminal: %+v", out)
	}
}

func TestNodeAsksCode(t *testing.T) {
	t.Parallel()
	p := newHubPair(t)
	p.setUp()
	p.remoteAccess(true)
	newProject(t, p.desktop.home, "app")
	args := []string{"project", "list", "--node", "desktop"}
	out := p.onLaptop("", answering(t, p.code()), args...)
	if out.Exit != clikit.ExitOK || !strings.HasPrefix(out.Stderr, "on node desktop\ncode for desktop: ") || !strings.Contains(out.Stdout, "app") {
		t.Fatalf("want the code asked, then the desktop's projects: %+v", out)
	}
	if s, _ := p.laptop.sessionJSON("hub", "session", "list"); len(s.Sessions) != 1 || s.Sessions[0].Node.Name != "desktop" {
		t.Errorf("the laptop's sessions %+v, want the one the code opened", s.Sessions)
	}
	if lineOf(logLines(t, p.laptop.home, runID(t, p.laptop.home, args)), "code typed", nil) == nil {
		t.Error("the code typed was not logged as typed")
	}

	// A session open, nothing is asked: the terminal has no answer typed.
	out = p.onLaptop("", answering(t), "project", "get", "--name", "app", "--node", "desktop")
	if out.Exit != clikit.ExitOK || strings.Contains(out.Stderr, "code for") {
		t.Fatalf("with a session open: %+v", out)
	}
}

func TestNodeAsksCodeWrong(t *testing.T) {
	t.Parallel()
	p := newHubPair(t)
	p.setUp()
	p.remoteAccess(true)
	code := p.desktop.wrongCode(p.secret)
	out := p.onLaptop("", answering(t, code), "project", "list", "--node", "desktop")
	if out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "[code_wrong]") || !strings.Contains(out.Stderr, "hint: type the code") {
		t.Fatalf("want code_wrong with its hint: %+v", out)
	}
}

func TestNodeNotForAgents(t *testing.T) {
	t.Parallel()
	p := openPair(t)
	asAgent := func(env *Env) {
		env.AgentID = "agent-1a2b3c4d"
		env.Stdin = clikittest.Typed(t, "123456")
	}
	for _, args := range [][]string{
		{"project", "list", "--node", "desktop"},
		{"project", "list", "--node", "desktop", "--json"},
		// Before the refusals of the command line.
		{"terminal", "attach", "--name", "server", "--node", "desktop", "--json"},
	} {
		out := p.onLaptop("", asAgent, args...)
		id := runID(t, p.laptop.home, args)
		if out.Exit != clikit.ExitRejected || !strings.Contains(out.Stdout+out.Stderr, string(api.ErrorCodeRemoteNotForAgents)) || strings.Contains(out.Stderr, "code for") {
			t.Errorf("%v: %+v, want remote_not_for_agents with nothing asked", args, out)
		}
		if n := served(t, p.laptop.home, id); n != 0 {
			t.Errorf("%v: the laptop's daemon was asked %d times", args, n)
		}
	}
	// Without --node, an agent's commands run as ever.
	if _, exit := p.laptop.hubJSON(func(env *Env) { env.AgentID = "agent-1a2b3c4d" }, "project", "list"); exit != clikit.ExitOK {
		t.Errorf("an agent's project list without --node: exit %d", exit)
	}
}

func TestNodePaths(t *testing.T) {
	t.Parallel()
	p := openPair(t)
	newProject(t, p.desktop.home, "app")
	dir := laptopDir(t)

	// A path the desktop's daemon receives must be the desktop's, absolute.
	args := []string{"project", "open", "--local", ".", "--node", "desktop", "--json"}
	out := p.onLaptop(dir, nil, args...)
	var v jsonOutput
	_ = json.Unmarshal(clikittest.JSON(t, args, out, codes), &v)
	wantError(t, v, out.Exit, clikit.CodeUsage, clikit.ExitUsage)
	if !strings.Contains(out.Stdout, "path must be absolute on node desktop") {
		t.Errorf("%v: %s", args, out.Stdout)
	}

	// A file the CLI reads or writes stays the laptop's.
	if out := p.onLaptop(dir, nil, "doc", "create", "--project-name", "app", "--name", "auth", "--path", "notes.md", "--node", "desktop", "--json"); out.Exit != clikit.ExitOK {
		t.Fatalf("doc create from the laptop's file: %+v", out)
	}
	if out := p.onLaptop(dir, nil, "doc", "get", "--project-name", "app", "--name", "auth", "--out", "auth.md", "--node", "desktop", "--json"); out.Exit != clikit.ExitOK {
		t.Fatalf("doc get to the laptop's file: %+v", out)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "auth.md")); err != nil || string(b) != "# auth\n" {
		t.Errorf("auth.md on the laptop: %q, %v", b, err)
	}

	// The project cannot come from the laptop's working directory.
	args = []string{"agent", "list", "--node", "desktop", "--json"}
	out = p.onLaptop(dir, nil, args...)
	_ = json.Unmarshal(clikittest.JSON(t, args, out, codes), &v)
	wantError(t, v, out.Exit, clikit.CodeUsage, clikit.ExitUsage)
	if !strings.Contains(out.Stdout, "pass --project-id or --project-name with --node") {
		t.Errorf("%v: %s", args, out.Stdout)
	}
}

// With --node a terminal or an agent starts in the project's checkout on the
// host, the laptop's working directory meaning nothing there.
func TestNodeCreatesInTheProject(t *testing.T) {
	t.Parallel()
	p := openPair(t)
	app := newProjectDir(t, p.desktop.home, "app")
	for _, args := range [][]string{
		{"terminal", "create", "--project-name", "app", "--node", "desktop", "--json"},
		{"agent", "create", "--project-name", "app", "--aca", "claude_code", "--node", "desktop", "--json"},
	} {
		out := p.onLaptop(laptopDir(t), nil, args...)
		var v jsonOutput
		_ = json.Unmarshal(clikittest.JSON(t, args, out, codes), &v)
		var dir string
		switch {
		case v.Terminal != nil:
			dir = v.Terminal.Dir
		case v.Agent != nil:
			dir = v.Agent.Dir
		}
		if out.Exit != clikit.ExitOK || dir != app {
			t.Errorf("%v: exit %d, dir %q, want %s: %s", args, out.Exit, dir, app, out.Stdout)
		}
	}
}

func TestNodeVersion(t *testing.T) {
	t.Parallel()
	p := openPair(t)
	args := []string{"version", "--node", "desktop", "--json"}
	out := p.onLaptop("", nil, args...)
	var v struct {
		CLI    versionOf  `json:"cli"`
		Daemon *versionOf `json:"daemon"`
	}
	if err := json.Unmarshal([]byte(out.Stdout), &v); err != nil || out.Exit != clikit.ExitOK || v.Daemon == nil || v.Daemon.Version != identity.Version || v.Daemon.APIVersion != api.CurrentVersion || v.CLI.APIVersion != api.CurrentVersion {
		t.Fatalf("%v: %+v %v", args, out, err)
	}
	if served(t, p.desktop.home, runID(t, p.laptop.home, args)) != 1 {
		t.Error("the version was not the desktop's")
	}
	// The text, with no warning of another version.
	text := p.onLaptop("", nil, "version", "--node", "desktop")
	if text.Exit != clikit.ExitOK || text.Stderr != "on node desktop\n" || !strings.Contains(text.Stdout, "daemon dev (none), API 1") {
		t.Errorf("text: %+v", text)
	}
}

func TestNodeSessionClosedMidCommand(t *testing.T) {
	t.Parallel()
	p := openPair(t)
	newProject(t, p.desktop.home, "app")
	r := p.laptop.startOnTerminal("project", "create", "--name", "app", "--node", "desktop")
	r.asked(t, "choose [o/n/c]: ", 1)
	if v, exit := p.desktop.sessionJSON("hub", "session", "close", "--all"); exit != clikit.ExitOK {
		t.Fatalf("close: %d %+v", exit, v.jsonOutput)
	}
	r.typeLine(t, "o")
	out := p.laptop.wait(r.running)
	if out.Exit != clikit.ExitRejected || !strings.Contains(out.Stderr, "[session_closed]") || strings.Contains(out.Stderr, "code for") {
		t.Fatalf("want session_closed, nothing asked: %+v", out)
	}
}

func TestNodeCompletions(t *testing.T) {
	t.Parallel()
	p := openPair(t)
	newProject(t, p.desktop.home, "on-desktop")
	newProject(t, p.laptop.home, "on-laptop")
	if got := complete(t, p.laptop.home, "project", "get", "--node", "desktop", "--name", ""); len(got) != 1 || !strings.HasPrefix(got[0], "on-desktop\t") {
		t.Errorf("the desktop's projects: %v", got)
	}
	got := complete(t, p.laptop.home, "project", "get", "--node", "")
	slices.Sort(got)
	if len(got) != 2 || !strings.HasPrefix(got[0], "desktop\t") || !strings.HasPrefix(got[1], "laptop\t") {
		t.Errorf("the account's nodes: %v", got)
	}

	// No session, nothing offered and nothing asked.
	if v, exit := p.laptop.sessionJSON("hub", "session", "close", "--all"); exit != clikit.ExitOK {
		t.Fatalf("close: %d %+v", exit, v.jsonOutput)
	}
	out := wdxWith(t, p.laptop.home, func(env *Env) { env.Stdin = clikittest.Typed(t, p.code()) }, "__complete", "project", "get", "--node", "desktop", "--name", "")
	if out.Exit != clikit.ExitOK || strings.Contains(out.Stdout, "on-") || strings.Contains(out.Stderr, "code for") {
		t.Errorf("with no session: %+v", out)
	}
	if s, _ := p.laptop.sessionJSON("hub", "session", "list"); len(s.Sessions) != 0 {
		t.Errorf("a completion opened a session: %+v", s.Sessions)
	}
}

func TestNodeHints(t *testing.T) {
	t.Parallel()
	problem := func(code api.ErrorCode, details map[string]any) error {
		p := api.NewProblem(code, "x")
		if details != nil {
			p.Details = &details
		}
		return &client.ProblemError{Problem: p}
	}
	for _, tc := range []struct {
		err  error
		hint string
	}{
		{problem(api.ErrorCodeRouteNotAllowed, map[string]any{"update": "host"}), "update wdx on desktop"},
		{problem(api.ErrorCodeNodeVersionUnsupported, map[string]any{"update": "host"}), "update wdx on desktop"},
		{problem(api.ErrorCodeNodeVersionUnsupported, map[string]any{"update": "client"}), "update wdx here"},
		{problem(api.ErrorCodeSessionClosed, nil), "run the command again"},
		{problem(api.ErrorCodeRouteNotAllowed, map[string]any{"operation": "getDaemon"}), ""},
		{problem(api.ErrorCodeNotFound, nil), ""},
	} {
		hinted, ok := errors.AsType[*clikit.Hinted](nodeHint(tc.err, "desktop"))
		if got := ""; ok {
			got = hinted.Hint
			if !strings.Contains(got, tc.hint) || tc.hint == "" {
				t.Errorf("%v: hint %q, want %q", tc.err, got, tc.hint)
			}
		} else if tc.hint != "" {
			t.Errorf("%v: no hint, want %q", tc.err, tc.hint)
		}
	}
}
