package clikit_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/clikit/clikittest"
	"github.com/srjn45/wdx/internal/settings"
	"github.com/srjn45/wdx/internal/style"
)

// The test program kit: greet, which asks unless --yes or --no answers; a
// group thing with make and list; fail, which fails as the test says;
// setting write; the topics about and settings.

var codes = map[string]clikit.CodeInfo{
	clikit.CodeUsage:    {Status: http.StatusBadRequest, Exit: clikit.ExitUsage, Description: "the command line or a setting is invalid"},
	clikit.CodeInternal: {Status: http.StatusInternalServerError, Exit: clikit.ExitSystem, Description: "an unexpected failure"},
	"failed":            {Status: http.StatusConflict, Exit: clikit.ExitRejected, Description: "the thing failed"},
}

func kitCodes(code string) (clikit.CodeInfo, bool) {
	info, ok := codes[code]
	return info, ok
}

var (
	errFailed = errors.New("it failed")
	errLines  = errors.New("the thing has 2 problems")
)

// newerError is a problem whose code kit does not know, as a newer
// server's would be.
type newerError struct{ status int }

func (e *newerError) Error() string { return "a newer failure" }

// kitProblem maps kit's own errors, as a program's Problem hook does.
func kitProblem(err error) (clikit.Problem, bool) {
	if e, ok := errors.AsType[*newerError](err); ok {
		return clikit.NewProblem("newer_code", clikit.CodeInfo{Status: e.status, Description: "a code from a newer release"}, err.Error()), true
	}
	switch {
	case errors.Is(err, errFailed):
		return clikit.NewProblem("failed", codes["failed"], err.Error()), true
	case errors.Is(err, errLines):
		p := clikit.NewProblem("failed", codes["failed"], err.Error())
		p.Lines = []string{err.Error(), "thing:1: one", "thing:2: two"}
		return p, true
	}
	return clikit.Problem{}, false
}

// kitConf is what kit's settings set.
type kitConf struct{ Name, Mode string }

var table = settings.Table[kitConf]{Rows: []settings.Row[kitConf]{
	{
		Key: "name", Description: "the name greeted by default", Default: "anon",
		Set: func(c *kitConf, v string) error { c.Name = v; return nil },
		Get: func(c kitConf) string { return c.Name },
	},
	{
		Key: "mode", Description: "how it runs: fast or slow", Default: "fast",
		Set: func(c *kitConf, v string) error {
			if v != "fast" && v != "slow" {
				return fmt.Errorf("%q is not fast or slow", v)
			}
			c.Mode = v
			return nil
		},
		Get: func(c kitConf) string { return c.Mode },
	},
}}

// loadSettings is the error of kit's settings read from a config file with
// content.
func loadSettings(t *testing.T, content string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	file, problems := table.File(path)
	_, _, err := table.Resolve([]settings.Source{file})
	if e, ok := errors.AsType[*settings.Error](err); ok {
		problems = append(problems, e.Problems...)
	}
	if len(problems) == 0 {
		t.Fatalf("want problems in %q", content)
	}
	return &settings.Error{Problems: problems}
}

var topics = []clikit.Topic{
	{Name: "about", Summary: "what kit is", Text: func(st style.Style) string { return "kit is a " + st.Name("test") + " program." }},
	{
		Name: "settings", Summary: "every setting and its default",
		Text:     func(st style.Style) string { return clikit.SettingsTable(st, table.Describe()) },
		Settings: table.Describe(),
	},
}

// newKit returns kit's tree, its fail command returning failure, or panicking
// with --panic.
func newKit(failure error) func() *cobra.Command {
	return func() *cobra.Command {
		return clikit.NewRoot(clikit.Root{Use: "kit", Short: "Run the test kit", Long: "kit tests clikit.", Color: style.ColorAuto, Topics: topics},
			newGreetCmd(), newThingCmd(), newFailCmd(failure), newSettingCmd(), clikit.NewCompletionCmd("kit"))
	}
}

type greetResult struct {
	Result   string  `json:"result"`
	Greeting string  `json:"greeting"`
	Dir      *string `json:"dir"`
}

func (r greetResult) Text(w io.Writer, st style.Style) {
	fmt.Fprintf(w, "%s %s\n", st.Success(r.Result), r.Greeting)
}

func newGreetCmd() *cobra.Command {
	var name, dir, prompt, code, token string
	var yes, no bool
	cmd := &cobra.Command{
		Use:   "greet --name <name> [--yes | --no] [--dir <path>] [--prompt <text>] [--code <code>] [--token <token>]",
		Short: "Greet someone, once you say yes",
		Example: clikit.Examples(
			"Greet ann", "kit greet --name ann --yes",
			"Decline", "kit greet --name ann --no --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote:   "The greeting.",
			clikit.Results:      clikit.Annotation("greeted", "declined"),
			clikit.JSONRequires: "yes no",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			question := "greet " + name + "?"
			answer := "yes"
			switch {
			case yes:
				clikit.LogAnswer(cmd, question, answer, "--yes")
			case no:
				answer = "no"
				clikit.LogAnswer(cmd, question, answer, "--no")
			case clikit.CanPrompt(cmd):
				a, err := clikit.Ask(cmd.ErrOrStderr(), bufio.NewScanner(cmd.InOrStdin()), question+" [yes/no] ", "yes", "no")
				if err != nil {
					return err
				}
				answer = a
				clikit.LogAnswer(cmd, question, answer, "prompt")
			default:
				clikit.LogUnanswered(cmd, question)
				return &clikit.Hinted{Err: &clikit.UsageError{Err: errors.New(question + " needs an answer")}, Hint: "pass --yes or --no"}
			}
			r := greetResult{Result: "greeted", Greeting: "hello " + name}
			if answer == "no" {
				r = greetResult{Result: "declined", Greeting: "goodbye " + name}
			}
			if dir != "" {
				abs, err := clikit.Absolute(cmd, dir)
				if err != nil {
					return err
				}
				r.Dir = &abs
			}
			return clikit.PrintResult(cmd, r)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "`name` to greet")
	cmd.Flags().BoolVar(&yes, "yes", false, "greet without asking")
	cmd.Flags().BoolVar(&no, "no", false, "decline without asking")
	cmd.Flags().StringVar(&dir, "dir", "", "`path` to say where")
	cmd.Flags().StringVar(&prompt, "prompt", "", "`text` never logged")
	cmd.Flags().StringVar(&code, "code", "", "`code` never logged")
	cmd.Flags().StringVar(&token, "token", "", "`token` measured by this command alone")
	clikit.MeasureFlag(cmd, "token")
	cmd.MarkFlagRequired("name")
	cmd.MarkFlagsMutuallyExclusive("yes", "no")
	clikit.CompleteFlag(cmd, "name", func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return []cobra.Completion{"ann", "bob"}, cobra.ShellCompDirectiveNoFileComp
	})
	clikit.CompleteFlag(cmd, "dir", clikit.CompleteDirs)
	clikit.CompleteFlag(cmd, "prompt", cobra.NoFileCompletions)
	clikit.CompleteFlag(cmd, "code", cobra.NoFileCompletions)
	clikit.CompleteFlag(cmd, "token", cobra.NoFileCompletions)
	return cmd
}

type thingResult struct {
	Result string   `json:"result,omitempty"`
	Things []string `json:"things"`
}

func (r thingResult) Text(w io.Writer, _ style.Style) {
	fmt.Fprintln(w, strings.Join(r.Things, "\n"))
}

func newThingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "thing",
		Short: "Make and list things",
		Args:  clikit.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	makeCmd := &cobra.Command{
		Use:         "make",
		Short:       "Make a thing",
		Example:     clikit.Examples("Make one", "kit thing make"),
		Args:        cobra.NoArgs,
		Annotations: map[string]string{clikit.OutputNote: "The thing made.", clikit.Results: "made"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return clikit.PrintResult(cmd, thingResult{Result: "made", Things: []string{"one"}})
		},
	}
	list := &cobra.Command{
		Use:         "list",
		Short:       "List the things",
		Example:     clikit.Examples("List them", "kit thing list"),
		Args:        cobra.NoArgs,
		Annotations: map[string]string{clikit.OutputNote: "Every thing."},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return clikit.PrintResult(cmd, thingResult{Things: []string{"one", "two"}})
		},
	}
	cmd.AddCommand(makeCmd, list)
	return cmd
}

func newFailCmd(failure error) *cobra.Command {
	var panics, hinted bool
	cmd := &cobra.Command{
		Use:     "fail [--panic] [--hinted]",
		Short:   "Fail as the test says",
		Example: clikit.Examples("Fail", "kit fail", "Panic", "kit fail --panic"),
		Args:    cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote:    "Nothing: it fails.",
			clikit.Errors:        clikit.CodeList("failed"),
			clikit.FailurePrefix: "not done",
		},
		RunE: func(*cobra.Command, []string) error {
			if panics {
				panic("kaboom")
			}
			err := failure
			if hinted {
				err = &clikit.Hinted{Err: err, Hint: "run kit thing make, or pass --panic"}
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&panics, "panic", false, "panic instead")
	cmd.Flags().BoolVar(&hinted, "hinted", false, "fail with a hint")
	return cmd
}

func newSettingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setting",
		Short: "Write the settings",
		Args:  clikit.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(&cobra.Command{
		Use:         "write",
		Short:       "Write the config file",
		Example:     clikit.Examples("Write it", "kit setting write"),
		Args:        cobra.NoArgs,
		Annotations: map[string]string{clikit.OutputNote: "The things it wrote."},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return clikit.PrintResult(cmd, thingResult{Result: "written", Things: []string{}})
		},
	})
	return cmd
}

// kitHome is a home for kit's logs.
func kitHome(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func logsDir(home string) string { return filepath.Join(home, "logs") }

func kitEnv(home string) clikit.Env {
	var values []clikit.SettingValue
	c, from, _ := table.Resolve(nil)
	for _, s := range table.Describe() {
		values = append(values, clikit.SettingValue{Key: s.Key, Value: table.Shown(c, s.Key), From: from[s.Key]})
	}
	return clikit.Env{
		Stdin:    strings.NewReader(""),
		Program:  clikit.Program{Name: "kit", Version: "1.2.3", Commit: "abc"},
		Logs:     clikit.Logs{Dir: logsDir(home), Level: slog.LevelDebug, RetentionDays: 7},
		Settings: values,
		WorkDir:  "/work",
		Codes:    kitCodes,
		Problem:  kitProblem,
	}
}

// kit runs args in home, in an environment adjust changes, and checks the
// run is logged from received to returned.
func kit(t *testing.T, home string, adjust func(*clikit.Env), args ...string) clikittest.Output {
	t.Helper()
	return kitFailing(t, home, errFailed, adjust, args...)
}

// kitFailing is kit with fail returning failure.
func kitFailing(t *testing.T, home string, failure error, adjust func(*clikit.Env), args ...string) clikittest.Output {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := kitEnv(home)
	env.Stdout, env.Stderr = &stdout, &stderr
	if adjust != nil {
		adjust(&env)
	}
	exit := clikit.Run(context.Background(), args, env, newKit(failure))
	out := clikittest.Output{Exit: exit, Stdout: stdout.String(), Stderr: stderr.String()}
	clikittest.WantLogged(t, clikittest.Logged{Dir: logsDir(home), Root: newKit(failure)}, args, out, nil)
	return out
}

// kitJSON runs args with --json and returns what it printed, checked to be
// one object, and the exit.
func kitJSON(t *testing.T, home string, args ...string) (map[string]any, int) {
	t.Helper()
	args = append(args, "--json")
	out := kit(t, home, nil, args...)
	var v map[string]any
	if err := json.Unmarshal(clikittest.JSON(t, args, out, kitCodes), &v); err != nil {
		t.Fatal(err)
	}
	code, _ := v["code"].(string)
	result, _ := v["result"].(string)
	clikittest.WantDeclared(t, newKit(errFailed)(), args, result, code)
	return v, out.Exit
}

// logged returns every line logged in home, as JSON objects.
func logged(t *testing.T, home string) []map[string]any {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(logsDir(home), "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	var lines []map[string]any
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.Lines(string(b)) {
			var l map[string]any
			if err := json.Unmarshal([]byte(line), &l); err != nil {
				t.Fatalf("%s: a line that is not JSON: %q", f, line)
			}
			lines = append(lines, l)
		}
	}
	return lines
}

// loggedMsg returns the lines logged in home with the message msg.
func loggedMsg(t *testing.T, home, msg string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, l := range logged(t, home) {
		if l["msg"] == msg {
			lines = append(lines, l)
		}
	}
	return lines
}
