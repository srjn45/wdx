package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/clikit/clikittest"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

// TestAgentsTopicCommands checks every command line of wdx help agents, the lines
// indented by four spaces, names a command and is one it accepts, as TestExamples
// does the examples.
func TestAgentsTopicCommands(t *testing.T) {
	t.Parallel()
	n := 0
	for line := range strings.Lines(agentsTopic(style.New(false))) {
		command, ok := strings.CutPrefix(line, "    ")
		if !ok {
			continue
		}
		command, _, _ = strings.Cut(strings.TrimSpace(command), "  ") // its comment
		n++
		c, err := checkCommandLine(command)
		switch {
		case !strings.HasPrefix(command, identity.Name+" "):
			t.Errorf("%q is indented as a command line", command)
		case err != nil:
			t.Errorf("%q: %v", command, err)
		case c.HasSubCommands():
			t.Errorf("%q names a group of commands, not one: %s", command, c.CommandPath())
		}
	}
	if n == 0 {
		t.Fatal("no command lines found")
	}
}

func TestHelpTopicJSON(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"json", "settings", "logs", "agents", "plans"} {
		v, exit := wdxJSON(t, t.TempDir(), "help", name)
		if exit != clikit.ExitOK || v.Topic == nil || v.Topic.Name != name || v.Topic.Text == "" || (v.Topic.Settings != nil) != (name == "settings") {
			t.Fatalf("%s: unexpected topic %+v", name, v.Topic)
		}
	}
	v, _ := wdxJSON(t, t.TempDir(), "help", "settings")
	i := slices.IndexFunc(v.Topic.Settings, func(s struct {
		Key     string  `json:"key"`
		EnvVar  *string `json:"env_var"`
		Default *string `json:"default"`
	}) bool {
		return s.Key == "home"
	})
	if s := v.Topic.Settings[i]; s.EnvVar == nil || *s.EnvVar != "WDX_HOME" || s.Default == nil {
		t.Fatalf("unexpected home setting %+v", s)
	}
}

func TestHelpUnknown(t *testing.T) {
	t.Parallel()
	for args, hint := range map[string]string{
		"help setings":        "did you mean wdx help setting; see 'wdx --help'",
		"help projct":         "did you mean wdx help project; see 'wdx --help'",
		"help project crate":  "did you mean wdx help project create; see 'wdx --help'",
		"help bogus":          "see 'wdx --help'",
		"help project zzzzzz": "see 'wdx --help'",
	} {
		v, exit := wdxJSON(t, t.TempDir(), strings.Fields(args)...)
		wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)
		if v.Hint == nil || *v.Hint != hint {
			t.Errorf("%s: want hint %q, got %+v", args, hint, v)
		}
	}
}

func TestHelpColor(t *testing.T) {
	t.Parallel()
	out := wdx(t, t.TempDir(), "project", "open", "--help", "--color", "always")
	for _, want := range []string{"\x1b[1mUsage:\x1b[0m", "\x1b[33m--local\x1b[0m \x1b[2;3m<path>\x1b[0m", "\x1b[1;32mcloned\x1b[0m"} {
		if !strings.Contains(out.stdout, want) {
			t.Errorf("want %q in the help, got:\n%s", want, out.stdout)
		}
	}
	for _, args := range [][]string{{"project", "open", "--help"}, {"help", "settings"}} {
		if out := wdx(t, t.TempDir(), append(args, "--json", "--color", "always")...); strings.Contains(out.stdout, esc) {
			t.Errorf("%v: want no color in the JSON help, got %q", args, out.stdout)
		}
	}
}

func TestExamples(t *testing.T) {
	t.Parallel()
	clikittest.CheckExamples(t, func() *cobra.Command { return newRoot(config.Default()) })
}
