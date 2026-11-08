package clikit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/clikit/clikittest"
	"github.com/srjn45/wdx/internal/style"
)

const esc = "\x1b["

func TestRunLogged(t *testing.T) {
	t.Parallel()
	home := kitHome(t)
	out := kit(t, home, nil, "greet", "--name", "ann", "--yes")
	if out.Exit != clikit.ExitOK || out.Stdout != "greeted hello ann\n" || out.Stderr != "" {
		t.Fatalf("unexpected %+v", out)
	}
	received := loggedMsg(t, home, "command received")
	if len(received) != 1 || received[0]["version"] != "1.2.3" || received[0]["commit"] != "abc" || received[0]["process"] != "cli" || received[0]["cwd"] != "/work" {
		t.Fatalf("unexpected command received: %v", received)
	}
	if s, _ := received[0]["settings"].(map[string]any); s["mode"].(map[string]any)["value"] != "fast" || s["mode"].(map[string]any)["from"] != "default" {
		t.Fatalf("want every setting with where it came from, got %v", received[0]["settings"])
	}
	started := loggedMsg(t, home, "command started")
	if len(started) != 1 || started[0]["command"] != "kit greet" || !slices.Equal(started[0]["flags_set"].([]any), []any{"name", "yes"}) {
		t.Fatalf("unexpected command started: %v", started)
	}
	answered := loggedMsg(t, home, "question answered")
	if len(answered) != 1 || answered[0]["answer"] != "yes" || answered[0]["by"] != "--yes" {
		t.Fatalf("unexpected answer logged: %v", answered)
	}
}

func TestDailyFileNamedAfterProgram(t *testing.T) {
	t.Parallel()
	home := kitHome(t)
	kit(t, home, nil, "thing", "list")
	if _, err := os.Stat(filepath.Join(logsDir(home), "kit-"+time.Now().Format(time.DateOnly)+".log")); err != nil {
		t.Fatalf("want the log named after kit: %v", err)
	}
}

func TestProcessNamed(t *testing.T) {
	t.Parallel()
	home := kitHome(t)
	env := kitEnv(home)
	env.Stdout, env.Stderr = &bytes.Buffer{}, &bytes.Buffer{}
	env.Logs.Process = "worker"
	clikit.Run(context.Background(), []string{"thing", "list"}, env, newKit(errFailed))
	lines := logged(t, home)
	for _, l := range lines {
		if l["process"] != "worker" {
			t.Fatalf("want every line of the run named worker, got %v", l)
		}
	}
	if len(lines) != 3 {
		t.Fatalf("want the run logged, got %v", lines)
	}
}

func TestJSONOneObject(t *testing.T) {
	t.Parallel()
	v, exit := kitJSON(t, kitHome(t), "greet", "--name", "ann", "--no", "--dir", "sub")
	if exit != clikit.ExitOK || v["result"] != "declined" || v["greeting"] != "goodbye ann" || v["dir"] != "/work/sub" {
		t.Fatalf("exit %d, %v", exit, v)
	}
	v, exit = kitJSON(t, kitHome(t), "thing", "list")
	if exit != clikit.ExitOK || len(v["things"].([]any)) != 2 {
		t.Fatalf("exit %d, %v", exit, v)
	}
}

func TestUsageErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args         []string
		detail, hint string
	}{
		{[]string{"greet", "--name", "ann", "--bogus"}, "unknown flag: --bogus", "see 'kit greet --help'"},
		{[]string{"thng"}, `unknown command "thng" for "kit"`, "did you mean kit thing; see 'kit --help'"},
		{[]string{"thing", "mak"}, `unknown command "mak" for "kit thing"`, "did you mean kit thing make; see 'kit thing --help'"},
		{[]string{"thing", "zzzzzz"}, `unknown command "zzzzzz" for "kit thing"`, "see 'kit thing --help'"},
		{[]string{"greet", "--name", "ann"}, "--json requires --yes or --no", "pass --yes or --no; see 'kit greet --help'"},
		{[]string{"greet", "--yes"}, `required flag(s) "name" not set`, "see 'kit greet --help'"},
	} {
		v, exit := kitJSON(t, kitHome(t), tc.args...)
		if exit != clikit.ExitUsage || v["code"] != clikit.CodeUsage || v["detail"] != tc.detail || v["hint"] != tc.hint {
			t.Errorf("%v: exit %d, %v", tc.args, exit, v)
		}
	}
	out := kit(t, kitHome(t), nil, "thng")
	if !strings.HasPrefix(out.Stderr, `kit: unknown command "thng" for "kit"  [usage]`+"\n  hint: did you mean kit thing; see 'kit --help'\n  debug_id: dbg-") || out.Stdout != "" {
		t.Fatalf("unexpected text %+v", out)
	}
}

func TestProgramError(t *testing.T) {
	t.Parallel()
	v, exit := kitJSON(t, kitHome(t), "fail")
	if exit != clikit.ExitRejected || v["code"] != "failed" || v["detail"] != "it failed" || v["hint"] != nil {
		t.Fatalf("exit %d, %v", exit, v)
	}
	out := kit(t, kitHome(t), nil, "fail")
	if !strings.HasPrefix(out.Stderr, "kit: not done: it failed  [failed]\n  debug_id: dbg-") {
		t.Fatalf("unexpected text %q", out.Stderr)
	}
	out = kitFailing(t, kitHome(t), errLines, nil, "fail")
	if !strings.HasPrefix(out.Stderr, "kit: not done: the thing has 2 problems  [failed]\n  thing:1: one\n  thing:2: two\n  debug_id: dbg-") {
		t.Fatalf("want the problem's lines, got %q", out.Stderr)
	}
}

func TestHinted(t *testing.T) {
	t.Parallel()
	v, exit := kitJSON(t, kitHome(t), "fail", "--hinted")
	if exit != clikit.ExitRejected || v["code"] != "failed" || v["hint"] != "run kit thing make, or pass --panic" {
		t.Fatalf("exit %d, %v", exit, v)
	}
	out := kit(t, kitHome(t), nil, "fail", "--hinted", "--color", "always")
	want := "  " + style.New(true).Hint("hint:") + " run " + style.New(true).Command("kit thing make") + ", or pass " + style.New(true).Flag("--panic") + "\n"
	if !strings.Contains(out.Stderr, want) {
		t.Fatalf("want the hint's command and flag styled, got %q", out.Stderr)
	}
}

func TestPanicIsInternal(t *testing.T) {
	t.Parallel()
	home := kitHome(t)
	v, exit := kitJSON(t, home, "fail", "--panic")
	if exit != clikit.ExitSystem || v["code"] != clikit.CodeInternal || v["detail"] != "panic: kaboom" {
		t.Fatalf("exit %d, %v", exit, v)
	}
	returned := loggedMsg(t, home, "command returned")
	if stack, _ := returned[0]["stack"].(string); !strings.Contains(stack, "newFailCmd") {
		t.Fatalf("want the panic's stack logged, got %v", returned[0])
	}
}

func TestPanicIsInternalError(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "boom", RunE: func(*cobra.Command, []string) error { panic("kaboom") }}
	cmd.SetContext(context.Background())
	clikit.WrapRun(cmd)
	err := cmd.RunE(cmd, nil)
	if stack, ok := clikit.PanicStack(err); !ok || err.Error() != "panic: kaboom" || !strings.Contains(string(stack), "TestPanicIsInternalError") {
		t.Fatalf("want a panicError with its stack, got %v", err)
	}
	if p := clikit.ProblemOf(kitEnv(t.TempDir()), err); p.Code != clikit.CodeInternal {
		t.Fatalf("want internal, got %s", p.Code)
	}
}

func TestSettingsErrorIsUsage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ content, hint string }{
		{"mode: loud\nname: [a, b]\n", "fix or remove them"},
		{"bogus: 1\n", "run kit setting write, which comments them out at the end of the file"},
	} {
		failure := loadSettings(t, tc.content)
		out := kitFailing(t, kitHome(t), failure, nil, "fail", "--json")
		var v struct {
			Code    string `json:"code"`
			Hint    string `json:"hint"`
			Detail  string `json:"detail"`
			Details struct {
				Settings []struct {
					Key string `json:"key"`
				} `json:"settings"`
			} `json:"details"`
		}
		if err := json.Unmarshal(clikittest.JSON(t, []string{"fail"}, out, kitCodes), &v); err != nil {
			t.Fatal(err)
		}
		if out.Exit != clikit.ExitUsage || v.Code != clikit.CodeUsage || v.Hint != tc.hint+"; see 'kit fail --help'" || v.Detail != failure.Error() || len(v.Details.Settings) != strings.Count(tc.content, "\n") {
			t.Errorf("%q: exit %d, %+v", tc.content, out.Exit, v)
		}
	}
}

func TestConfigError(t *testing.T) {
	t.Parallel()
	failure := loadSettings(t, "mode: loud\n")
	var stdout, stderr bytes.Buffer
	env := kitEnv(t.TempDir())
	env.Stdout, env.Stderr = &stdout, &stderr
	if exit := clikit.ConfigError([]string{"thing", "list", "--json"}, env, failure); exit != clikit.ExitUsage {
		t.Fatalf("want exit %d, got %d", clikit.ExitUsage, exit)
	}
	var v struct {
		Code    string `json:"code"`
		DebugID string `json:"debug_id"`
		Details struct {
			Settings []struct {
				Value string `json:"value"`
			} `json:"settings"`
		} `json:"details"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &v); err != nil || stderr.Len() != 0 {
		t.Fatalf("want one JSON error, got %q, %q", stdout.String(), stderr.String())
	}
	if s := v.Details.Settings; v.Code != clikit.CodeUsage || v.DebugID != "" || len(s) != 1 || s[0].Value != "loud" {
		t.Fatalf("unexpected error %+v", v)
	}

	stdout.Reset()
	clikit.ConfigError([]string{"thing", "list"}, env, errors.New("no settings"))
	if want := "kit: no settings  [usage]\n  hint: fix or remove them\n"; stderr.String() != want || stdout.Len() != 0 {
		t.Fatalf("got %q, want %q", stderr.String(), want)
	}
}

func TestRewritesSettings(t *testing.T) {
	t.Parallel()
	unknown, invalid := loadSettings(t, "bogus: 1\n"), loadSettings(t, "mode: loud\n")
	for _, tc := range []struct {
		args []string
		err  error
		want bool
	}{
		{[]string{"setting", "write"}, unknown, true},
		{[]string{"setting", "write", "--json"}, unknown, true},
		{[]string{"setting", "write"}, invalid, false},
		{[]string{"thing", "list"}, unknown, false},
		{[]string{"setting", "write"}, errors.New("other"), false},
	} {
		if got := clikit.RewritesSettings(newKit(errFailed)(), tc.args, tc.err); got != tc.want {
			t.Errorf("%v, %v: got %v", tc.args, tc.err, got)
		}
	}
}

func TestColor(t *testing.T) {
	t.Parallel()
	terminal := func(env *clikit.Env) { env.Stdout = clikittest.Typed(t) }
	for _, tc := range []struct {
		name   string
		adjust func(env *clikit.Env)
		args   []string
		want   bool
	}{
		{"always", nil, []string{"--color", "always"}, true},
		{"never on a terminal", terminal, []string{"--color", "never"}, false},
		{"auto on a terminal", terminal, nil, true},
		{"auto not on a terminal", nil, nil, false},
		{"auto with NO_COLOR", func(env *clikit.Env) { terminal(env); env.NoColor = true }, nil, false},
		{"always with --json", nil, []string{"--color", "always", "--json"}, false},
	} {
		home := kitHome(t)
		out := kit(t, home, tc.adjust, append([]string{"greet", "--name", "ann", "--yes"}, tc.args...)...)
		started := loggedMsg(t, home, "command started")
		if on := started[0]["color_stdout"] == true; on != tc.want || out.Exit != clikit.ExitOK || strings.Contains(out.Stdout, esc) && !tc.want {
			t.Errorf("%s: want color %v, logged %v, printed %q", tc.name, tc.want, started[0]["color_stdout"], out.Stdout)
		}
	}
	if out := kit(t, kitHome(t), nil, "greet", "--name", "ann", "--yes", "--color", "always"); out.Stdout != style.New(true).Success("greeted")+" hello ann\n" {
		t.Fatalf("want the result colored, got %q", out.Stdout)
	}
	v, exit := kitJSON(t, kitHome(t), "greet", "--color", "loud")
	if exit != clikit.ExitUsage || !strings.Contains(v["detail"].(string), `"loud" is not one of auto, always, never`) {
		t.Fatalf("exit %d, %v", exit, v)
	}
}

func TestHelpText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args       []string
		want, deny []string
	}{
		{[]string{"--help"}, []string{
			"kit: run the test kit\n\nkit tests clikit.\n\nUsage:\n  kit <command> [flags]\n",
			"Commands:\n", "  greet ", "  thing ", "--color <mode>", "Exit codes:\n  0  ok\n", "  3  system: an unexpected failure\n",
			"Help topics:\n  about     what kit is\n  settings  every setting and its default\n",
			"Run kit <command> --help for a command, kit help <topic> for a topic.\n",
		}, []string{"Global flags:", "Errors:", "\n  help "}},
		{[]string{"thing", "--help"}, []string{
			"kit thing: make and list things\n", "kit thing <command> [flags]", "  list ", "  make ",
			"Global flags: --color <mode>, --json, --no-input, -h/--help (see kit --help)\n",
			"Run kit thing <command> --help for more about a command.\n",
		}, []string{"Errors:", "Help topics:", "Exit codes:"}},
		{[]string{"help", "greet"}, []string{
			"kit greet: greet someone, once you say yes\n",
			"Examples:\n  # Greet ann\n  kit greet --name ann --yes\n\n  # Decline\n  kit greet --name ann --no --json\n",
			"  --name <name>  ", "  --dir <path>   ",
			"Flag rules:\n  --name is required\n  at most one of --yes, --no\n  with --json: one of --yes, --no\n",
			"Output:\n  The greeting.\n  result: greeted or declined\n",
			"Errors:\n  CODE      EXIT  MEANING\n  usage     2     the command line or a setting is invalid\n  internal  3     an unexpected failure\n",
			"Global flags: --color <mode>, --json, --no-input, -h/--help (see kit --help)\n",
		}, []string{"Commands:", "Help topics:", "\n  --json"}},
		{[]string{"fail", "-h"}, []string{"  failed    1     the thing failed\n  usage "}, nil},
		{[]string{"help", "about"}, []string{"kit help about: what kit is\n\nkit is a test program.\n"}, []string{"Usage:"}},
		{[]string{"help", "settings"}, []string{"kit help settings: every setting and its default\n\n  name  the name greeted by default (default anon)\n  mode  how it runs: fast or slow (default fast)\n"}, nil},
	} {
		out := kit(t, kitHome(t), nil, tc.args...)
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
	if out := kit(t, kitHome(t), nil, "greet", "--help", "--color", "always"); !strings.Contains(out.Stdout, style.New(true).Heading("Usage:")) {
		t.Fatalf("want the help colored, got %q", out.Stdout)
	}
}

type helpJSON struct {
	Command *struct {
		Path      string `json:"path"`
		Examples  []clikit.Example
		FlagRules []clikit.FlagRule `json:"flag_rules"`
		Commands  []struct {
			Name string `json:"name"`
		} `json:"commands"`
		Topics []struct {
			Name string `json:"name"`
		} `json:"topics"`
		Results []string `json:"results"`
		Errors  []struct {
			Code string `json:"code"`
			Exit int    `json:"exit"`
		} `json:"errors"`
	} `json:"command"`
	Topic *struct {
		Name     string            `json:"name"`
		Text     string            `json:"text"`
		Settings []json.RawMessage `json:"settings"`
	} `json:"topic"`
}

func helpOf(t *testing.T, args ...string) helpJSON {
	t.Helper()
	args = append(args, "--json")
	out := kit(t, kitHome(t), nil, args...)
	var v helpJSON
	if err := json.Unmarshal(clikittest.JSON(t, args, out, kitCodes), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestHelpJSON(t *testing.T) {
	t.Parallel()
	root := helpOf(t, "--help").Command
	if len(root.Topics) != 2 || root.Topics[0].Name != "about" || len(root.Commands) != 5 {
		t.Fatalf("unexpected root %+v", root)
	}
	greet := helpOf(t, "greet", "--help").Command
	errs := greet.Errors
	if len(errs) != 2 || errs[0].Code != clikit.CodeUsage || errs[0].Exit != clikit.ExitUsage || errs[1].Code != clikit.CodeInternal || errs[1].Exit != clikit.ExitSystem {
		t.Fatalf("want the errors to end with usage and internal, got %+v", errs)
	}
	if greet.Topics != nil || len(greet.Examples) != 2 || !slices.Equal(greet.Results, []string{"greeted", "declined"}) {
		t.Fatalf("unexpected greet %+v", greet)
	}
	if rule := greet.FlagRules[len(greet.FlagRules)-1]; rule.Rule != "one_required_with_json" || !slices.Equal(rule.Flags, []string{"yes", "no"}) {
		t.Fatalf("unexpected rule %+v", rule)
	}
	if errs := helpOf(t, "help", "fail").Command.Errors; len(errs) != 3 || errs[0].Code != "failed" || errs[0].Exit != clikit.ExitRejected {
		t.Fatalf("unexpected errors of fail %+v", errs)
	}
	if topic := helpOf(t, "help", "settings").Topic; len(topic.Settings) != 2 || strings.Contains(topic.Text, esc) {
		t.Fatalf("want the settings topic to list its settings, got %+v", topic)
	}
	if topic := helpOf(t, "help", "about").Topic; topic.Settings != nil || topic.Text != "kit is a test program." {
		t.Fatalf("want no settings on about, got %+v", topic)
	}
}

func TestHelpUnknown(t *testing.T) {
	t.Parallel()
	for args, hint := range map[string]string{
		"help thng":         "did you mean kit help thing; see 'kit --help'",
		"help abot":         "did you mean kit help about; see 'kit --help'",
		"help thing mak":    "did you mean kit help thing make; see 'kit --help'",
		"help bogus":        "see 'kit --help'",
		"help thing zzzzzz": "see 'kit --help'",
	} {
		v, exit := kitJSON(t, kitHome(t), strings.Fields(args)...)
		if exit != clikit.ExitUsage || v["code"] != clikit.CodeUsage || v["hint"] != hint {
			t.Errorf("%s: want hint %q, got exit %d, %v", args, hint, exit, v)
		}
	}
}

func TestCompletionsLeftToCompletingRuns(t *testing.T) {
	t.Parallel()
	root := newKit(errFailed)()
	cmd, _, err := root.Find([]string{"greet"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := clikit.FlagCompletion(cmd, "name"); !ok {
		t.Fatal("--name completes nothing")
	}
	if _, ok := cmd.GetFlagCompletionFunc("name"); ok {
		t.Fatal("cobra was given --name's completion")
	}
	clikit.RegisterCompletions(root)
	if _, ok := cmd.GetFlagCompletionFunc("name"); !ok {
		t.Fatal("cobra was not given --name's completion")
	}

	for args, want := range map[string]bool{"greet --name ann --yes": false, "__complete greet --name ": true} {
		var built *cobra.Command
		env := kitEnv(t.TempDir())
		env.Stdout, env.Stderr = &bytes.Buffer{}, &bytes.Buffer{}
		clikit.Run(context.Background(), strings.Split(args, " "), env, func() *cobra.Command {
			built = newKit(errFailed)()
			return built
		})
		greet, _, _ := built.Find([]string{"greet"})
		if _, given := greet.GetFlagCompletionFunc("name"); given != want {
			t.Errorf("%q: cobra given --name's completion: %v, want %v", args, given, want)
		}
	}
}

func TestCompletion(t *testing.T) {
	t.Parallel()
	home := kitHome(t)
	for args, want := range map[string]string{
		"greet --name ":  "ann\nbob\n:4\n",
		"greet --color ": "auto\nalways\nnever\n:4\n",
		"help ":          "greet\tGreet someone, once you say yes\n",
	} {
		out := kit(t, home, nil, append([]string{"__complete"}, strings.Split(args, " ")...)...)
		if out.Exit != clikit.ExitOK || !strings.Contains(out.Stdout, want) {
			t.Errorf("%q: want %q in %q", args, want, out.Stdout)
		}
	}
	if out := kit(t, home, nil, "__complete", "help", ""); !strings.Contains(out.Stdout, "\nabout\twhat kit is\nsettings\tevery setting and its default\n") {
		t.Errorf("want the topics in help's completions, got %q", out.Stdout)
	}
	for _, l := range logged(t, home) {
		if l["level"] != "DEBUG" {
			t.Fatalf("want a completing run logged at debug, got %v", l)
		}
	}
}

func TestCompletionScripts(t *testing.T) {
	t.Parallel()
	for _, shell := range []string{"bash", "zsh", "fish"} {
		out := kit(t, kitHome(t), nil, "completion", shell, "--color", "always")
		if out.Exit != clikit.ExitOK || !strings.Contains(out.Stdout, "__complete") || !strings.Contains(out.Stdout, "kit") || strings.Contains(out.Stdout, esc) {
			t.Fatalf("%s: want an uncolored script, got exit %d:\n%.300s", shell, out.Exit, out.Stdout)
		}
		v, _ := kitJSON(t, kitHome(t), "completion", shell)
		if v["shell"] != shell || v["script"] != out.Stdout {
			t.Fatalf("%s: unexpected JSON %.300v", shell, v)
		}
	}
	if out := kit(t, kitHome(t), nil, "completion", "zsh", "--help"); !strings.Contains(out.Stdout, "Save it as _kit in a directory of your fpath") {
		t.Fatalf("want the zsh script's help to name kit, got %s", out.Stdout)
	}
}

func TestAsk(t *testing.T) {
	t.Parallel()
	home := kitHome(t)
	out := kit(t, home, func(env *clikit.Env) { env.Stdin = clikittest.Typed(t, "maybe", "NO") }, "greet", "--name", "ann")
	if out.Exit != clikit.ExitOK || out.Stdout != "declined goodbye ann\n" || strings.Count(out.Stderr, "greet ann? [yes/no] ") != 2 {
		t.Fatalf("want it asked until answered, got %+v", out)
	}
	if answered := loggedMsg(t, home, "question answered"); answered[0]["by"] != "prompt" || answered[0]["answer"] != "no" {
		t.Fatalf("unexpected answer logged: %v", answered)
	}
}

func TestNoPrompt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args   []string
		stdin  func() io.Reader
		reason string
	}{
		{nil, func() io.Reader { return strings.NewReader("") }, "stdin is not a terminal"},
		{[]string{"--no-input"}, func() io.Reader { return clikittest.Typed(t, "yes") }, "--no-input"},
	} {
		home := kitHome(t)
		args := append([]string{"greet", "--name", "ann"}, tc.args...)
		out := kit(t, home, func(env *clikit.Env) { env.Stdin = tc.stdin() }, args...)
		if out.Exit != clikit.ExitUsage || !strings.Contains(out.Stderr, "hint: pass --yes or --no; see 'kit greet --help'") {
			t.Errorf("%v: unexpected %+v", args, out)
		}
		if unanswered := loggedMsg(t, home, "question unanswered"); len(unanswered) != 1 || unanswered[0]["reason"] != tc.reason {
			t.Errorf("%v: want the reason %q logged, got %v", args, tc.reason, unanswered)
		}
	}
	root := newKit(errFailed)()
	cmd, _, _ := root.Find([]string{"greet"})
	cmd.SetIn(clikittest.Typed(t))
	if !clikit.CanPrompt(cmd) {
		t.Fatalf("want a prompt on a terminal, got %q", clikit.NoPromptReason(cmd))
	}
	if err := cmd.ParseFlags([]string{"--json"}); err != nil {
		t.Fatal(err)
	}
	if clikit.CanPrompt(cmd) || clikit.NoPromptReason(cmd) != "--json" {
		t.Fatalf("want no prompt with --json, got %q", clikit.NoPromptReason(cmd))
	}
}

func TestPromptMeasured(t *testing.T) {
	t.Parallel()
	home := kitHome(t)
	kit(t, home, nil, "greet", "--name", "ann", "--yes", "--prompt", "secret")
	kit(t, home, nil, "greet", "--name", "ann", "--yes", "--prompt=secret!")
	for _, l := range logged(t, home) {
		if b, _ := json.Marshal(l); strings.Contains(string(b), "secret") {
			t.Fatalf("a prompt logged: %s", b)
		}
	}
	received := loggedMsg(t, home, "command received")
	if args := received[0]["args"].([]any); args[len(args)-1] != "<6 bytes>" {
		t.Fatalf("want the prompt measured, got %v", args)
	}
	if args := received[1]["args"].([]any); args[len(args)-1] != "--prompt=<7 bytes>" {
		t.Fatalf("want the prompt measured, got %v", args)
	}
	if flags := loggedMsg(t, home, "command started")[0]["flags"].(map[string]any); flags["prompt"] != "<6 bytes>" {
		t.Fatalf("want the prompt flag measured, got %v", flags)
	}
}

func TestAbsolute(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ dir, workDir, want string }{
		{"sub", "/work", "/work/sub"},
		{"/abs/../path/", "/work", "/path"},
		{"sub", "", ""},
	} {
		home := kitHome(t)
		out := kit(t, home, func(env *clikit.Env) { env.WorkDir = tc.workDir }, "greet", "--name", "ann", "--yes", "--dir", tc.dir, "--json")
		var v struct {
			Dir    *string `json:"dir"`
			Detail string  `json:"detail"`
		}
		_ = json.Unmarshal([]byte(out.Stdout), &v)
		switch {
		case tc.want == "" && (out.Exit != clikit.ExitSystem || !strings.Contains(v.Detail, "the working directory is unknown")):
			t.Errorf("%s with no working directory: %+v", tc.dir, out)
		case tc.want != "" && (v.Dir == nil || *v.Dir != tc.want):
			t.Errorf("%s in %s: want %s, got %+v", tc.dir, tc.workDir, tc.want, out)
		}
	}
}

func TestRuleLines(t *testing.T) {
	t.Parallel()
	git := "git"
	for _, tc := range []struct {
		rules []clikit.FlagRule
		want  []string
	}{
		{nil, nil},
		{
			[]clikit.FlagRule{{Rule: "required", Flags: []string{"name"}}, {Rule: "exclusive", Flags: []string{"open", "cancel"}}, {Rule: "one_required_with_json", Flags: []string{"open", "cancel"}}},
			[]string{"--name is required", "at most one of --open, --cancel", "with --json: one of --open, --cancel"},
		},
		{
			[]clikit.FlagRule{{Rule: "one_required", Flags: []string{"id", "name"}}, {Rule: "exclusive", Flags: []string{"id", "name"}}},
			[]string{"exactly one of --id, --name"},
		},
		{
			[]clikit.FlagRule{
				{Rule: "one_required", Flags: []string{"local", "git"}},
				{Rule: "exclusive", Flags: []string{"local", "git"}},
				{Rule: "exclusive", Flags: []string{"reclone", "remove"}},
				{Rule: "exclusive", Flags: []string{"local", "reclone"}},
				{Rule: "exclusive", Flags: []string{"local", "remove"}},
				{Rule: "one_required_with_json", Flags: []string{"reclone", "remove"}, When: &git},
			},
			[]string{"exactly one of --local, --git", "--reclone: not with --local, --remove", "--remove: not with --local, --reclone", "with --json and --git: one of --reclone, --remove"},
		},
		{
			[]clikit.FlagRule{{Rule: "one_required", Flags: []string{"a", "b"}}, {Rule: "together", Flags: []string{"c", "d"}}},
			[]string{"at least one of --a, --b", "all or none of --c, --d"},
		},
	} {
		if got := clikit.RuleLines(style.New(false), tc.rules); !slices.Equal(got, tc.want) {
			t.Errorf("%+v:\ngot  %q\nwant %q", tc.rules, got, tc.want)
		}
	}
}

func TestDocsAndExamples(t *testing.T) {
	t.Parallel()
	clikittest.CheckDocs(t, newKit(errFailed)())
	clikittest.CheckExamples(t, newKit(errFailed))
}
