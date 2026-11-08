package clikittest_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/clikit/clikittest"
	"github.com/srjn45/wdx/internal/style"
)

var codes = map[string]clikit.CodeInfo{
	clikit.CodeUsage:    {Status: http.StatusBadRequest, Exit: clikit.ExitUsage, Description: "the command line is invalid"},
	clikit.CodeInternal: {Status: http.StatusInternalServerError, Exit: clikit.ExitSystem, Description: "an unexpected failure"},
}

func kitCodes(code string) (clikit.CodeInfo, bool) {
	info, ok := codes[code]
	return info, ok
}

type greeting struct {
	Result  string `json:"result"`
	Message string `json:"message"`
	Secret  string `json:"secret"`
}

func (g greeting) Text(w io.Writer, _ style.Style) { fmt.Fprintln(w, g.Message) }

// newKit is a program with greet, which prints a secret it logs measured as
// a server would, and a leaf adjust changes.
func newKit(adjust func(leaf *cobra.Command)) func() *cobra.Command {
	return func() *cobra.Command {
		var name string
		greet := &cobra.Command{
			Use:         "greet --name <name>",
			Short:       "Greet someone",
			Example:     clikit.Examples("Greet ann", "kit greet --name ann"),
			Args:        cobra.NoArgs,
			Annotations: map[string]string{clikit.OutputNote: "The greeting.", clikit.Results: "greeted"},
			RunE: func(cmd *cobra.Command, _ []string) error {
				return clikit.PrintResult(cmd, greeting{Result: "greeted", Message: "hello " + name, Secret: "s3cret"})
			},
		}
		greet.Flags().StringVar(&name, "name", "", "`name` to greet")
		greet.MarkFlagRequired("name")
		clikit.CompleteFlag(greet, "name", cobra.NoFileCompletions)
		if adjust != nil {
			adjust(greet)
		}
		return clikit.NewRoot(clikit.Root{Use: "kit", Short: "Run the test kit", Color: style.ColorAuto}, greet)
	}
}

func run(t *testing.T, home string, args ...string) clikittest.Output {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := clikit.Env{
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr,
		Program: clikit.Program{Name: "kit"},
		Logs:    clikit.Logs{Dir: filepath.Join(home, "logs"), Level: slog.LevelInfo, RetentionDays: 1},
		Codes:   kitCodes,
	}
	exit := clikit.Run(context.Background(), args, env, newKit(nil))
	return clikittest.Output{Exit: exit, Stdout: stdout.String(), Stderr: stderr.String()}
}

// failures records what a check reports.
type failures struct {
	testing.TB
	errors []string
}

func (f *failures) Errorf(format string, args ...any) {
	f.errors = append(f.errors, fmt.Sprintf(format, args...))
}

func TestWantLogged(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	logs := clikittest.Logged{Dir: filepath.Join(home, "logs")}
	args := []string{"greet", "--name", "ann", "--json"}
	out := run(t, home, args...)
	clikittest.WantLogged(t, logs, args, out, nil)
	v := clikittest.JSON(t, args, out, kitCodes)
	if !strings.Contains(string(v), `"result": "greeted"`) {
		t.Fatalf("unexpected %s", v)
	}
	clikittest.WantDeclared(t, newKit(nil)(), args, "greeted")

	failed := run(t, home, "greet", "--json")
	clikittest.WantLogged(t, logs, []string{"greet", "--json"}, failed, nil)
	clikittest.JSON(t, []string{"greet"}, failed, kitCodes)
	clikittest.WantDeclared(t, newKit(nil)(), []string{"greet"}, "", clikit.CodeUsage)

	// A home that does not exist has nowhere to log.
	clikittest.WantLogged(t, clikittest.Logged{Dir: filepath.Join(home, "none", "logs")}, args, out, nil)

	lines := clikittest.LogLines(t, logs.Dir, args)
	if n := len(lines); n != 5 || lines[0].Msg != "command received" || lines[n-1].Msg != "command returned" || *lines[n-1].Exit != clikit.ExitUsage {
		t.Fatalf("unexpected lines %+v", lines)
	}
}

func TestWantLoggedRedacts(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	args := []string{"greet", "--name", "ann", "--json"}
	out := run(t, home, args...)
	called := false
	clikittest.WantLogged(t, clikittest.Logged{Dir: filepath.Join(home, "logs")}, args, out, func(printed map[string]any) {
		called = printed["secret"] == "s3cret"
	})
	if !called {
		t.Fatal("redact was not given what the run printed")
	}
}

func TestCheckDocs(t *testing.T) {
	t.Parallel()
	clikittest.CheckDocs(t, newKit(nil)())
	clikittest.CheckExamples(t, newKit(nil))

	for name, tc := range map[string]struct {
		adjust func(leaf *cobra.Command)
		want   string
	}{
		"output note": {func(leaf *cobra.Command) { delete(leaf.Annotations, clikit.OutputNote) }, "kit greet: no output note"},
		"examples":    {func(leaf *cobra.Command) { leaf.Example = "" }, "kit greet: no examples"},
		"usage listed": {
			func(leaf *cobra.Command) { leaf.Annotations[clikit.Errors] = clikit.CodeUsage },
			`kit greet: lists "usage", which every command can return`,
		},
		"completion": {
			func(leaf *cobra.Command) { leaf.Flags().String("other", "", "`value` of nothing") },
			"kit greet: --other completes nothing",
		},
		"backticks": {
			func(leaf *cobra.Command) {
				leaf.Flags().String("plain", "", "a value")
				clikit.CompleteFlag(leaf, "plain", cobra.NoFileCompletions)
			},
			"kit greet: --plain does not name its value with backticks",
		},
	} {
		f := &failures{TB: t}
		clikittest.CheckDocs(f, newKit(tc.adjust)())
		if len(f.errors) != 1 || !strings.HasPrefix(f.errors[0], tc.want) {
			t.Errorf("%s: want %q, got %q", name, tc.want, f.errors)
		}
	}
}

func TestCheckCommandLine(t *testing.T) {
	t.Parallel()
	if c, err := clikittest.CheckCommandLine(newKit(nil), "kit greet --name ann"); err != nil || c.CommandPath() != "kit greet" {
		t.Fatalf("got %v, %v", c, err)
	}
	for _, line := range []string{"kit greet", "kit greet --name 'ann'", "kit greet --bogus"} {
		if _, err := clikittest.CheckCommandLine(newKit(nil), line); err == nil {
			t.Errorf("%q: want it rejected", line)
		}
	}
}
