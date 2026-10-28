package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/style"
)

// TestExamples checks every example is a command line its command accepts:
// it names the command, and its flags parse and follow the command's rules.
func TestExamples(t *testing.T) {
	t.Parallel()
	var check func(cmd *cobra.Command)
	check = func(cmd *cobra.Command) {
		for _, e := range parseExamples(cmd.Example) {
			if strings.ContainsAny(e.Command, `'"\`) {
				t.Errorf("%s: example %q quotes; keep examples free of shell quoting", cmd.CommandPath(), e.Command)
				continue
			}
			args := strings.Fields(e.Command)
			// A fresh tree, since parsing sets the flags.
			c, rest, err := newRoot(config.Default()).Find(args[1:])
			if err != nil || c.CommandPath() != cmd.CommandPath() {
				t.Errorf("%s: example %q runs %v, %v", cmd.CommandPath(), e.Command, c.CommandPath(), err)
				continue
			}
			for _, validate := range []func() error{
				func() error { return c.ParseFlags(rest) },
				func() error { return c.ValidateArgs(c.Flags().Args()) },
				c.ValidateRequiredFlags,
				c.ValidateFlagGroups,
				func() error { return checkJSONRequires(c) },
			} {
				if err := validate(); err != nil {
					t.Errorf("%s: example %q: %v", cmd.CommandPath(), e.Command, err)
					break
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

func TestHelpTopicJSON(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"json", "settings", "logs"} {
		v, exit := wdxJSON(t, t.TempDir(), "help", name)
		if exit != api.ExitOK || v.Topic == nil || v.Topic.Name != name || v.Topic.Text == "" || (v.Topic.Settings != nil) != (name == "settings") {
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
		"help setings":        "did you mean wdx help settings; see 'wdx --help'",
		"help projct":         "did you mean wdx help project; see 'wdx --help'",
		"help project crate":  "did you mean wdx help project create; see 'wdx --help'",
		"help bogus":          "see 'wdx --help'",
		"help project zzzzzz": "see 'wdx --help'",
	} {
		v, exit := wdxJSON(t, t.TempDir(), strings.Fields(args)...)
		wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
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

func TestRuleLines(t *testing.T) {
	t.Parallel()
	git := "git"
	for _, tc := range []struct {
		rules []flagRule
		want  []string
	}{
		{nil, nil},
		{
			[]flagRule{{Rule: "required", Flags: []string{"name"}}, {Rule: "exclusive", Flags: []string{"open", "cancel"}}, {Rule: "one_required_with_json", Flags: []string{"open", "cancel"}}},
			[]string{"--name is required", "at most one of --open, --cancel", "with --json: one of --open, --cancel"},
		},
		{
			[]flagRule{{Rule: "one_required", Flags: []string{"id", "name"}}, {Rule: "exclusive", Flags: []string{"id", "name"}}},
			[]string{"exactly one of --id, --name"},
		},
		{
			[]flagRule{
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
			[]flagRule{{Rule: "one_required", Flags: []string{"a", "b"}}, {Rule: "together", Flags: []string{"c", "d"}}},
			[]string{"at least one of --a, --b", "all or none of --c, --d"},
		},
	} {
		if got := ruleLines(style.New(false), tc.rules); !slices.Equal(got, tc.want) {
			t.Errorf("%+v:\ngot  %q\nwant %q", tc.rules, got, tc.want)
		}
	}
}
