package clikit

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/srjn45/wdx/internal/settings"
	"github.com/srjn45/wdx/internal/style"
)

// Command annotations. Lists are space-separated.
const (
	// FailurePrefix starts each error message in text mode, such as "not created".
	FailurePrefix = "failurePrefix"
	// OutputNote tells what the command prints on success.
	OutputNote = "output"
	// Results holds the results the command can return.
	Results = "results"
	// Errors holds the error codes the command can return besides usage
	// and internal.
	Errors = "errors"
	// JSONRequires holds flags one of which --json requires, after
	// "<flag>,<flag>:" when only together with one of those, each "<flag>!<other>"
	// when not also with other.
	JSONRequires = "jsonRequires"
)

// setHelp makes --help print the command's help as text, or as JSON with
// --json, and <program> help print a command's help or one of topics.
func setHelp(root *cobra.Command, topics []Topic) {
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		r := helpResponse{Command: newCommandView(cmd, topics)}
		InvocationOf(cmd).response = r
		if JSONMode(cmd) {
			_ = WriteJSON(cmd.OutOrStdout(), r)
			return
		}
		r.Text(cmd.OutOrStdout(), StdoutStyle(cmd))
	})
	root.SetHelpCommand(&cobra.Command{
		Use:               "help [command | topic]",
		Short:             "Show the help of a command or a topic",
		Hidden:            true,
		ValidArgsFunction: completeHelp(topics),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				if t, ok := findTopic(topics, args[0]); ok {
					return PrintResult(cmd, topicResponse{Topic: newTopicView(t), program: root.Name(), topic: t})
				}
			}
			target, rest, err := root.Find(args)
			if err != nil {
				return &UsageError{err}
			}
			if len(rest) > 0 {
				return unknownHelp(target, args, topics)
			}
			target.SetContext(cmd.Context())
			return target.Help()
		},
	})
}

// unknownHelp is the error of <program> help for args that name neither a
// command nor one of topics; found is the deepest command they name.
func unknownHelp(found *cobra.Command, args []string, topics []Topic) error {
	name := args[len(args)-1]
	err := &UsageError{fmt.Errorf("no help for %q: it is neither a command nor a topic", strings.Join(args, " "))}
	candidates := suggestions(found, name)
	if !found.HasParent() {
		for _, t := range topics {
			if distance(name, t.Name) <= max(1, len(t.Name)/3) {
				candidates = append(candidates, t.Name)
			}
		}
	}
	if len(candidates) == 0 {
		return err
	}
	program := found.Root().Name()
	parents := strings.TrimPrefix(found.CommandPath(), program)
	return &Hinted{err, "did you mean " + program + " help" + parents + " " + candidates[0]}
}

// distance is the Levenshtein distance between a and b.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

type helpResponse struct {
	Command commandView `json:"command"`
}

func (r helpResponse) Text(w io.Writer, st style.Style) {
	r.Command.text(w, st)
}

type commandView struct {
	Path        string           `json:"path"`
	Usage       string           `json:"usage"`
	Summary     string           `json:"summary"`
	Description string           `json:"description"`
	Examples    []Example        `json:"examples"`
	Flags       []flagView       `json:"flags"`
	FlagRules   []FlagRule       `json:"flag_rules"`
	Commands    []commandSummary `json:"commands"`
	Topics      []topicSummary   `json:"topics"` // the root's only; null on other commands
	Output      *string          `json:"output"`
	Results     []string         `json:"results"`
	Errors      []codeView       `json:"errors"`

	long    string // the description below the summary; "" when there is none
	root    bool
	program string
}

// Example is one of a command's examples.
type Example struct {
	Description string `json:"description"`
	Command     string `json:"command"`
}

type flagView struct {
	Name        string  `json:"name"`
	Shorthand   *string `json:"shorthand"`
	Type        string  `json:"type"`
	ValueName   *string `json:"value_name"` // what the value stands for, such as "path"; null for a bool
	Default     any     `json:"default"`
	Description string  `json:"description"`
	Global      bool    `json:"global"`
}

// FlagRule is a rule on a command's flags: cobra's, or one --json adds.
type FlagRule struct {
	Rule  string   `json:"rule"`
	Flags []string `json:"flags"`
	When  *string  `json:"when"` // the flag that must be given for the rule to apply
	// Unless is a flag that, given too, keeps the rule from applying.
	Unless *string `json:"unless"`
}

type commandSummary struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

type topicSummary struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

type codeView struct {
	Code        string `json:"code"`
	Exit        int    `json:"exit"`
	Description string `json:"description"`
}

// newCommandView is the help of cmd; topics are the root's.
func newCommandView(cmd *cobra.Command, topics []Topic) commandView {
	v := commandView{
		Path:        cmd.CommandPath(),
		Usage:       cmd.UseLine(),
		Summary:     cmd.Short,
		Description: cmp.Or(cmd.Long, cmd.Short),
		Examples:    ParseExamples(cmd.Example),
		Flags:       []flagView{},
		FlagRules:   FlagRules(cmd),
		Commands:    []commandSummary{},
		Results:     AnnotationList(cmd, Results),
		Errors:      []codeView{},
		long:        cmd.Long,
		root:        !cmd.HasParent(),
		program:     cmd.Root().Name(),
	}
	if cmd.HasAvailableSubCommands() {
		v.Usage = cmd.CommandPath() + " <command> [flags]"
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
	if v.root {
		v.Topics = []topicSummary{}
		for _, t := range topics {
			v.Topics = append(v.Topics, topicSummary{Name: t.Name, Summary: t.Summary})
		}
	}
	if note, ok := cmd.Annotations[OutputNote]; ok {
		v.Output = &note
	}
	codes := InvocationOf(cmd).Env.Codes
	for _, code := range ErrorList(cmd) {
		var info CodeInfo
		if codes != nil {
			info, _ = codes(code)
		}
		v.Errors = append(v.Errors, codeView{Code: code, Exit: info.Exit, Description: info.Description})
	}
	return v
}

func newFlagView(f *pflag.Flag, global bool) flagView {
	valueName, usage := pflag.UnquoteUsage(f)
	v := flagView{Name: f.Name, Type: f.Value.Type(), Description: usage, Global: global}
	if f.Shorthand != "" {
		v.Shorthand = &f.Shorthand
	}
	if v.Type == "bool" {
		v.Default = f.DefValue == "true"
	} else {
		v.ValueName = &valueName
		if f.DefValue != "" {
			v.Default = f.DefValue
		}
	}
	return v
}

// Examples formats pairs of a description and a command line as the
// Example of a command.
func Examples(pairs ...string) string {
	var blocks []string
	for i := 0; i+1 < len(pairs); i += 2 {
		blocks = append(blocks, "  # "+pairs[i]+"\n  "+pairs[i+1])
	}
	return strings.Join(blocks, "\n\n")
}

// ParseExamples reads back what Examples formats.
func ParseExamples(example string) []Example {
	list := []Example{}
	for block := range strings.SplitSeq(example, "\n\n") {
		description, command, ok := strings.Cut(strings.TrimSpace(block), "\n")
		if ok {
			list = append(list, Example{
				Description: strings.TrimPrefix(description, "# "),
				Command:     strings.TrimSpace(command),
			})
		}
	}
	return list
}

// cobraRules maps the flag annotations cobra enforces to rule names.
var cobraRules = []struct{ annotation, rule string }{
	{cobra.BashCompOneRequiredFlag, "required"},
	{"cobra_annotation_one_required", "one_required"},
	{"cobra_annotation_mutually_exclusive", "exclusive"},
	{"cobra_annotation_required_if_others_set", "together"},
}

// FlagRules lists the rules cobra enforces on the command's flags, then the
// flags --json requires.
func FlagRules(cmd *cobra.Command) []FlagRule {
	rules := []FlagRule{}
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
					rules = append(rules, FlagRule{Rule: r.rule, Flags: flags})
				}
			}
		})
	}
	return append(rules, jsonRules(cmd)...)
}

// jsonRules reads the jsonRequires annotation: one rule for each flag it applies
// with, or one that always applies.
func jsonRules(cmd *cobra.Command) []FlagRule {
	value, ok := cmd.Annotations[JSONRequires]
	if !ok {
		return nil
	}
	whens, flags, found := strings.Cut(value, ":")
	if !found {
		return []FlagRule{{Rule: "one_required_with_json", Flags: strings.Fields(whens)}}
	}
	var rules []FlagRule
	for _, condition := range strings.Split(whens, ",") {
		rule := FlagRule{Rule: "one_required_with_json", Flags: strings.Fields(flags)}
		when, unless, found := strings.Cut(condition, "!")
		rule.When = &when
		if found {
			rule.Unless = &unless
		}
		rules = append(rules, rule)
	}
	return rules
}

// ruleLines says the rules in as few lines as they can be: a group that is
// both required and exclusive is "exactly one of", and the other exclusive
// groups are listed by flag.
func ruleLines(st style.Style, rules []FlagRule) []string {
	key := func(flags []string) string { return strings.Join(slices.Sorted(slices.Values(flags)), " ") }
	required, exclusive := map[string]bool{}, map[string]bool{}
	for _, r := range rules {
		switch r.Rule {
		case "one_required":
			required[key(r.Flags)] = true
		case "exclusive":
			exclusive[key(r.Flags)] = true
		}
	}
	exactly := map[string]bool{} // the flags in an "exactly one of" group
	for _, r := range rules {
		if r.Rule == "one_required" && exclusive[key(r.Flags)] {
			for _, f := range r.Flags {
				exactly[f] = true
			}
		}
	}

	// A group whose flags are in no other is said in one line.
	groupsOf := map[string]int{}
	for _, r := range rules {
		if r.Rule == "exclusive" && !required[key(r.Flags)] {
			for _, f := range r.Flags {
				groupsOf[f]++
			}
		}
	}
	alone := func(flags []string) bool {
		return !slices.ContainsFunc(flags, func(f string) bool { return groupsOf[f] > 1 || exactly[f] })
	}

	var lines []string
	conflicts := map[string][]string{}
	for _, r := range rules {
		switch merged := required[key(r.Flags)] && exclusive[key(r.Flags)]; {
		case r.Rule == "required":
			lines = append(lines, flagList(st, r.Flags)+" is required")
		case r.Rule == "one_required" && merged:
			lines = append(lines, "exactly one of "+flagList(st, r.Flags))
		case r.Rule == "one_required":
			lines = append(lines, "at least one of "+flagList(st, r.Flags))
		case r.Rule == "together":
			lines = append(lines, "all or none of "+flagList(st, r.Flags))
		case r.Rule == "exclusive" && !merged && alone(r.Flags):
			lines = append(lines, "at most one of "+flagList(st, r.Flags))
		case r.Rule == "exclusive" && !merged:
			// A flag of an "exactly one of" group needs no line: the flags
			// it excludes say so on theirs.
			for _, a := range r.Flags {
				if exactly[a] && !slices.ContainsFunc(r.Flags, func(b string) bool { return exactly[b] && b != a }) {
					continue
				}
				for _, b := range r.Flags {
					if b != a {
						conflicts[a] = append(conflicts[a], b)
					}
				}
			}
		}
	}
	for _, f := range slices.Sorted(maps.Keys(conflicts)) {
		others := slices.Compact(slices.Sorted(slices.Values(conflicts[f])))
		lines = append(lines, st.Flag("--"+f)+": not with "+flagList(st, others))
	}
	for _, r := range rules {
		if r.Rule != "one_required_with_json" {
			continue
		}
		given := st.Flag("--json")
		if r.When != nil {
			given += " and " + st.Flag("--"+*r.When)
		}
		if r.Unless != nil {
			given += ", unless " + st.Flag("--"+*r.Unless)
		}
		lines = append(lines, "with "+given+": one of "+flagList(st, r.Flags))
	}
	return lines
}

func flagList(st style.Style, flags []string) string {
	var names []string
	for _, f := range flags {
		names = append(names, st.Flag("--"+f))
	}
	return strings.Join(names, ", ")
}

// AnnotationList is the list the annotation key of cmd holds.
func AnnotationList(cmd *cobra.Command, key string) []string {
	return append([]string{}, strings.Fields(cmd.Annotations[key])...)
}

// ErrorList is every error code cmd can return: those its Errors annotation
// lists, sorted, then usage and internal.
func ErrorList(cmd *cobra.Command) []string {
	return append(slices.Sorted(slices.Values(AnnotationList(cmd, Errors))), CodeUsage, CodeInternal)
}

// Annotation is the value of an annotation that lists items.
func Annotation(items ...string) string {
	return strings.Join(items, " ")
}

// CodeList is Annotation for error codes.
func CodeList[C ~string](codes ...C) string {
	var items []string
	for _, c := range codes {
		items = append(items, string(c))
	}
	return Annotation(items...)
}

// text writes the help of the command: what it is, how to call it, what it
// prints and how it fails.
func (v commandView) text(w io.Writer, st style.Style) {
	section := func(title string) { fmt.Fprintf(w, "\n%s\n", st.Heading(title+":")) }

	fmt.Fprintf(w, "%s: %s\n", st.Heading(st.Command(v.Path)), lowerFirst(v.Summary))
	if v.long != "" {
		fmt.Fprintf(w, "\n%s\n", v.long)
	}

	section("Usage")
	fmt.Fprintf(w, "  %s\n", UsageText(st, v.Usage))

	if len(v.Examples) > 0 {
		section("Examples")
		for i, e := range v.Examples {
			if i > 0 {
				fmt.Fprintln(w)
			}
			fmt.Fprintf(w, "  %s\n  %s\n", st.Muted("# "+e.Description), UsageText(st, e.Command))
		}
	}

	if len(v.Commands) > 0 {
		section("Commands")
		var table style.Table
		for _, c := range v.Commands {
			table.Row("  "+st.Command(c.Name), c.Summary)
		}
		table.Write(w)
	}

	var flags style.Table
	for _, f := range v.Flags {
		// Other commands list the global flags, help among them, on one line.
		if !v.root && (f.Global || f.Name == "help") {
			continue
		}
		flags.Row("  "+flagText(st, f), flagDescription(st, f))
	}
	if !flags.Empty() {
		section("Flags")
		flags.Write(w)
	}

	if lines := ruleLines(st, v.FlagRules); len(lines) > 0 {
		section("Flag rules")
		for _, l := range lines {
			fmt.Fprintf(w, "  %s\n", l)
		}
	}

	if v.Output != nil {
		section("Output")
		for line := range strings.Lines(*v.Output) {
			fmt.Fprintf(w, "  %s", line)
		}
		fmt.Fprintln(w)
		if len(v.Results) > 0 {
			var results []string
			for _, r := range v.Results {
				results = append(results, st.Success(r))
			}
			fmt.Fprintf(w, "  result: %s\n", orList(results))
		}
	}

	if len(v.Commands) == 0 {
		section("Errors")
		var table style.Table
		table.Row("  "+st.Heading("CODE"), st.Heading("EXIT"), st.Heading("MEANING"))
		for _, e := range v.Errors {
			table.Row("  "+st.Name(e.Code), st.Muted(fmt.Sprint(e.Exit)), e.Description)
		}
		table.Write(w)
	}

	if v.root {
		section("Exit codes")
		var table style.Table
		for _, e := range exitCodes {
			table.Row("  "+st.Name(fmt.Sprint(e.exit)), e.meaning)
		}
		table.Write(w)

		section("Help topics")
		var topicTable style.Table
		for _, t := range v.Topics {
			topicTable.Row("  "+st.Command(t.Name), t.Summary)
		}
		topicTable.Write(w)
		fmt.Fprintf(w, "\nRun %s for a command, %s for a topic.\n",
			UsageText(st, v.program+" <command> --help"), UsageText(st, v.program+" help <topic>"))
		return
	}

	var global []string
	for _, f := range v.Flags {
		if f.Global {
			global = append(global, flagText(st, f))
		}
	}
	global = append(global, st.Flag("-h")+"/"+st.Flag("--help"))
	fmt.Fprintf(w, "\n%s %s %s\n", st.Heading("Global flags:"), strings.Join(global, ", "), st.Muted("(see "+v.program+" --help)"))
	if len(v.Commands) > 0 {
		fmt.Fprintf(w, "\nRun %s for more about a command.\n", UsageText(st, v.Path+" <command> --help"))
	}
}

// exitCodes are what the exit status of a program means.
var exitCodes = []struct {
	exit    int
	meaning string
}{
	{ExitOK, "ok"},
	{ExitRejected, "rejected: a conflict, not found or cancelled"},
	{ExitUsage, "usage: the command line or a setting is invalid"},
	{ExitSystem, "system: an unexpected failure"},
}

// flagText is how f is written on a command line: "--name <name>", or
// "-h, --help" with its shorthand.
func flagText(st style.Style, f flagView) string {
	s := st.Flag("--" + f.Name)
	if f.Shorthand != nil {
		s = st.Flag("-"+*f.Shorthand) + ", " + s
	}
	if f.ValueName != nil {
		s += " " + st.Placeholder("<"+*f.ValueName+">")
	}
	return s
}

func flagDescription(st style.Style, f flagView) string {
	if d, ok := f.Default.(string); ok {
		return f.Description + " " + st.Muted("(default "+d+")")
	}
	return f.Description
}

var usageToken = regexp.MustCompile(`<[a-z][a-z-]*>|--?[a-z][a-z-]*|\[flags\]`)

// UsageText styles a command line: the command, its flags and the
// placeholders of their values.
func UsageText(st style.Style, line string) string {
	end := len(line)
	if i := usageToken.FindStringIndex(line); i != nil {
		end = i[0]
	}
	for _, c := range []string{"(", "["} {
		if i := strings.Index(line, c); i >= 0 && i < end {
			end = i
		}
	}
	command := strings.TrimRight(line[:end], " ")
	rest := usageToken.ReplaceAllStringFunc(line[len(command):], func(m string) string {
		switch {
		case strings.HasPrefix(m, "<"):
			return st.Placeholder(m)
		case m == "[flags]":
			return st.Muted(m)
		}
		return st.Flag(m)
	})
	return st.Command(command) + rest
}

func lowerFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	// An acronym such as "AI" keeps its case.
	if next, _ := utf8.DecodeRuneInString(s[n:]); unicode.IsUpper(next) {
		return s
	}
	return string(unicode.ToLower(r)) + s[n:]
}

func orList(items []string) string {
	if len(items) == 1 {
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

// Topic is help that is about no single command.
type Topic struct {
	Name, Summary string
	Text          func(st style.Style) string
	// Settings are the settings the topic's JSON lists; nil for a topic about
	// something else.
	Settings []settings.Setting
}

func findTopic(topics []Topic, name string) (Topic, bool) {
	i := slices.IndexFunc(topics, func(t Topic) bool { return t.Name == name })
	if i < 0 {
		return Topic{}, false
	}
	return topics[i], true
}

type topicResponse struct {
	Topic topicView `json:"topic"`

	program string
	topic   Topic
}

type topicView struct {
	Name     string        `json:"name"`
	Summary  string        `json:"summary"`
	Text     string        `json:"text"`
	Settings []settingView `json:"settings"` // the settings topic's only; null on others
}

type settingView struct {
	Key         string  `json:"key"`
	EnvVar      *string `json:"env_var"` // the variable it is read from; null for the config file
	Description string  `json:"description"`
	Default     *string `json:"default"` // null when required
	Required    bool    `json:"required"`
}

func newTopicView(t Topic) topicView {
	v := topicView{Name: t.Name, Summary: t.Summary, Text: t.Text(style.New(false))}
	if t.Settings != nil {
		v.Settings = []settingView{}
		for _, s := range t.Settings {
			sv := settingView{Key: s.Key, Description: s.Description, Required: s.Required}
			if s.EnvVar != "" {
				sv.EnvVar = &s.EnvVar
			}
			if !s.Required {
				sv.Default = &s.Default
			}
			v.Settings = append(v.Settings, sv)
		}
	}
	return v
}

func (r topicResponse) Text(w io.Writer, st style.Style) {
	fmt.Fprintf(w, "%s: %s\n\n%s\n", st.Heading(st.Command(r.program+" help "+r.topic.Name)), r.topic.Summary, r.topic.Text(st))
}

// SettingsTable is the table of settings a settings topic shows: each
// setting's key, or the variable of one read from the environment only, its
// description and its default.
func SettingsTable(st style.Style, list []settings.Setting) string {
	var b strings.Builder
	var table style.Table
	for _, s := range list {
		def := "required"
		if !s.Required {
			def = "default " + cmp.Or(s.Default, `""`)
		}
		name := s.Key
		if s.EnvOnly {
			name = s.EnvVar
		}
		table.Row("  "+st.Name(name), s.Description+" "+st.Muted("("+def+")"))
	}
	table.Write(&b)
	return b.String()
}
