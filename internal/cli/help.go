package cli

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// conventions follows the root's usage text, so its commands come first.
const conventions = `Output:
  Results go to stdout and errors to stderr. With --json nothing prompts, and
  stdout holds exactly one JSON object and nothing else:
    success: {"ok": true, "action": "created", "project": {...}}
    failure: {"ok": false, "error": {"code": "not_found", "message": "...", "hint": "...", "details": {...}}}
  "action" names what happened; commands that change nothing have none.
  Each command's help lists its actions and error codes; add --json to get
  the help itself as JSON.

Exit codes:
  0  ok
  1  rejected: a conflict, not found or cancelled
  2  usage: the command line is invalid
  3  system: an unexpected failure`

// setHelp makes --help print the flag rules, output and errors of a command
// after cobra's usage text, or all of it as JSON with --json.
func setHelp(root *cobra.Command) {
	usage := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		v := newCommandView(cmd)
		if jsonMode(cmd) {
			writeJSON(cmd.OutOrStdout(), helpResponse{OK: true, Command: v})
			return
		}
		usage(cmd, args)
		if !cmd.HasParent() {
			fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", conventions)
		}
		if !cmd.HasAvailableSubCommands() {
			v.text(cmd.OutOrStdout())
		}
	})
}

type helpResponse struct {
	OK      bool        `json:"ok"`
	Command commandView `json:"command"`
}

type commandView struct {
	Path        string           `json:"path"`
	Usage       string           `json:"usage"`
	Summary     string           `json:"summary"`
	Description string           `json:"description"`
	Flags       []flagView       `json:"flags"`
	FlagRules   []flagRule       `json:"flag_rules"`
	Commands    []commandSummary `json:"commands"`
	Output      *string          `json:"output"`
	Actions     []string         `json:"actions"`
	Errors      []codeView       `json:"errors"`
}

type flagView struct {
	Name        string  `json:"name"`
	Shorthand   *string `json:"shorthand"`
	Type        string  `json:"type"`
	Default     any     `json:"default"`
	Description string  `json:"description"`
	Global      bool    `json:"global"`
}

type flagRule struct {
	Rule  string   `json:"rule"`
	Flags []string `json:"flags"`
	When  *string  `json:"when"` // the flag that must be given for the rule to apply
}

type commandSummary struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

type codeView struct {
	Code        string `json:"code"`
	Exit        int    `json:"exit"`
	Description string `json:"description"`
}

func newCommandView(cmd *cobra.Command) commandView {
	v := commandView{
		Path:        cmd.CommandPath(),
		Usage:       cmd.UseLine(),
		Summary:     cmd.Short,
		Description: cmp.Or(cmd.Long, cmd.Short),
		Flags:       []flagView{},
		FlagRules:   flagRules(cmd),
		Commands:    []commandSummary{},
		Actions:     annotationList(cmd, actionList),
		Errors:      []codeView{},
	}
	addFlags := func(flags *pflag.FlagSet, global bool) {
		flags.VisitAll(func(f *pflag.Flag) {
			if !f.Hidden {
				v.Flags = append(v.Flags, newFlagView(f, global))
			}
		})
	}
	addFlags(cmd.LocalFlags(), false)
	addFlags(cmd.InheritedFlags(), true)
	for _, sub := range cmd.Commands() {
		if sub.IsAvailableCommand() {
			v.Commands = append(v.Commands, commandSummary{Name: sub.Name(), Summary: sub.Short})
		}
	}
	if !cmd.HasParent() {
		v.Description += "\n\n" + conventions
	}
	if note, ok := cmd.Annotations[outputNote]; ok {
		v.Output = &note
	}
	for _, code := range append(slices.Sorted(slices.Values(annotationList(cmd, errorList))), codeUsage, codeInternal) {
		v.Errors = append(v.Errors, codeView{Code: code, Exit: codes[code].exit, Description: codes[code].description})
	}
	return v
}

func newFlagView(f *pflag.Flag, global bool) flagView {
	v := flagView{Name: f.Name, Type: f.Value.Type(), Description: f.Usage, Global: global}
	if f.Shorthand != "" {
		v.Shorthand = &f.Shorthand
	}
	if v.Type == "bool" {
		v.Default = f.DefValue == "true"
	} else if f.DefValue != "" {
		v.Default = f.DefValue
	}
	return v
}

// cobraRules maps the flag annotations cobra enforces to rule names.
var cobraRules = []struct{ annotation, rule string }{
	{cobra.BashCompOneRequiredFlag, "required"},
	{"cobra_annotation_one_required", "one_required"},
	{"cobra_annotation_mutually_exclusive", "exclusive"},
	{"cobra_annotation_required_if_others_set", "together"},
}

// flagRules lists the rules cobra enforces on the command's flags, then the
// flags --json requires.
func flagRules(cmd *cobra.Command) []flagRule {
	rules := []flagRule{}
	seen := map[string]bool{}
	for _, r := range cobraRules {
		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			for _, group := range f.Annotations[r.annotation] {
				flags := strings.Fields(group)
				if r.rule == "required" {
					flags = []string{f.Name}
				}
				if key := r.rule + " " + strings.Join(flags, " "); !seen[key] {
					seen[key] = true
					rules = append(rules, flagRule{Rule: r.rule, Flags: flags})
				}
			}
		})
	}
	if rule, ok := jsonRule(cmd); ok {
		rules = append(rules, rule)
	}
	return rules
}

func jsonRule(cmd *cobra.Command) (flagRule, bool) {
	value, ok := cmd.Annotations[jsonRequires]
	if !ok {
		return flagRule{}, false
	}
	rule := flagRule{Rule: "one_required_with_json"}
	when, flags, found := strings.Cut(value, ":")
	if found {
		rule.When = &when
	} else {
		flags = when
	}
	rule.Flags = strings.Fields(flags)
	return rule, true
}

func annotationList(cmd *cobra.Command, key string) []string {
	return append([]string{}, strings.Fields(cmd.Annotations[key])...)
}

func annotation(items ...string) string {
	return strings.Join(items, " ")
}

// text writes the sections cobra's usage text lacks.
func (v commandView) text(w io.Writer) {
	if len(v.FlagRules) > 0 {
		fmt.Fprintln(w, "\nFlag rules:")
		for _, r := range v.FlagRules {
			fmt.Fprintf(w, "  %s: %s\n", r.label(), flagNames(r.Flags, ", "))
		}
	}
	if v.Output != nil {
		fmt.Fprintln(w, "\nOutput:")
		for line := range strings.Lines(*v.Output) {
			fmt.Fprintf(w, "  %s", line)
		}
		fmt.Fprintln(w)
	}
	if len(v.Actions) > 0 {
		fmt.Fprintf(w, "  action: %s\n", orList(v.Actions))
	}
	fmt.Fprintln(w, "\nErrors:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  CODE\tEXIT\tMEANING")
	for _, e := range v.Errors {
		fmt.Fprintf(tw, "  %s\t%d\t%s\n", e.Code, e.Exit, e.Description)
	}
	tw.Flush()
}

func (r flagRule) label() string {
	switch r.Rule {
	case "required":
		return "required"
	case "one_required":
		return "one of these is required"
	case "exclusive":
		return "at most one of"
	case "together":
		return "all or none of"
	}
	if r.When != nil {
		return "with --json and --" + *r.When + ", one of these is required"
	}
	return "with --json, one of these is required"
}

func flagNames(flags []string, sep string) string {
	return "--" + strings.Join(flags, sep+"--")
}

func orList(items []string) string {
	if len(items) == 1 {
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}
