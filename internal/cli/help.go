package cli

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

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

// setHelp makes --help print the command's help as text, or as JSON with
// --json, and wdx help print a command's help or a help topic.
func setHelp(root *cobra.Command) {
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		r := helpResponse{Command: newCommandView(cmd)}
		invocationOf(cmd).response = r
		if jsonMode(cmd) {
			_ = writeJSON(cmd.OutOrStdout(), r)
			return
		}
		r.text(cmd.OutOrStdout(), stdoutStyle(cmd))
	})
	root.SetHelpCommand(&cobra.Command{
		Use:               "help [command | topic]",
		Short:             "Show the help of a command or a topic",
		Hidden:            true,
		ValidArgsFunction: completeHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				if t, ok := findTopic(args[0]); ok {
					return printResult(cmd, topicResponse{Topic: newTopicView(t)})
				}
			}
			target, rest, err := root.Find(args)
			if err != nil {
				return &usageError{err}
			}
			if len(rest) > 0 {
				return unknownHelp(target, args)
			}
			target.SetContext(cmd.Context())
			return target.Help()
		},
	})
}

// unknownHelp is the error of wdx help for args that name neither a command
// nor a topic; found is the deepest command they name.
func unknownHelp(found *cobra.Command, args []string) error {
	name := args[len(args)-1]
	err := &usageError{fmt.Errorf("no help for %q: it is neither a command nor a topic", strings.Join(args, " "))}
	candidates := suggestions(found, name)
	if !found.HasParent() {
		for _, t := range topics {
			if distance(name, t.name) <= max(1, len(t.name)/3) {
				candidates = append(candidates, t.name)
			}
		}
	}
	if len(candidates) == 0 {
		return err
	}
	parents := strings.TrimPrefix(found.CommandPath(), identity.Name)
	return &hinted{err, "did you mean " + identity.Name + " help" + parents + " " + candidates[0]}
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

func (r helpResponse) text(w io.Writer, st style.Style) {
	r.Command.text(w, st)
}

type commandView struct {
	Path        string           `json:"path"`
	Usage       string           `json:"usage"`
	Summary     string           `json:"summary"`
	Description string           `json:"description"`
	Examples    []exampleView    `json:"examples"`
	Flags       []flagView       `json:"flags"`
	FlagRules   []flagRule       `json:"flag_rules"`
	Commands    []commandSummary `json:"commands"`
	Topics      []topicSummary   `json:"topics"` // the root's only; null on other commands
	Output      *string          `json:"output"`
	Results     []string         `json:"results"`
	Errors      []codeView       `json:"errors"`

	long string // the description below the summary; "" when there is none
	root bool
}

type exampleView struct {
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

type flagRule struct {
	Rule  string   `json:"rule"`
	Flags []string `json:"flags"`
	When  *string  `json:"when"` // the flag that must be given for the rule to apply
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

func newCommandView(cmd *cobra.Command) commandView {
	v := commandView{
		Path:        cmd.CommandPath(),
		Usage:       cmd.UseLine(),
		Summary:     cmd.Short,
		Description: cmp.Or(cmd.Long, cmd.Short),
		Examples:    parseExamples(cmd.Example),
		Flags:       []flagView{},
		FlagRules:   flagRules(cmd),
		Commands:    []commandSummary{},
		Results:     annotationList(cmd, resultList),
		Errors:      []codeView{},
		long:        cmd.Long,
		root:        !cmd.HasParent(),
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
			v.Topics = append(v.Topics, topicSummary{Name: t.name, Summary: t.summary})
		}
	}
	if note, ok := cmd.Annotations[outputNote]; ok {
		v.Output = &note
	}
	for _, code := range append(slices.Sorted(slices.Values(annotationList(cmd, errorList))), string(api.ErrorCodeUsage), string(api.ErrorCodeInternal)) {
		info := api.Codes[api.ErrorCode(code)]
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

// examples formats pairs of a description and a command line as the
// Example of a command.
func examples(pairs ...string) string {
	var blocks []string
	for i := 0; i+1 < len(pairs); i += 2 {
		blocks = append(blocks, "  # "+pairs[i]+"\n  "+pairs[i+1])
	}
	return strings.Join(blocks, "\n\n")
}

// parseExamples reads back what examples formats.
func parseExamples(example string) []exampleView {
	list := []exampleView{}
	for block := range strings.SplitSeq(example, "\n\n") {
		description, command, ok := strings.Cut(strings.TrimSpace(block), "\n")
		if ok {
			list = append(list, exampleView{
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

// ruleLines says the rules in as few lines as they can be: a group that is
// both required and exclusive is "exactly one of", and the other exclusive
// groups are listed by flag.
func ruleLines(st style.Style, rules []flagRule) []string {
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
	if r, ok := lastJSONRule(rules); ok {
		given := st.Flag("--json")
		if r.When != nil {
			given += " and " + st.Flag("--"+*r.When)
		}
		lines = append(lines, "with "+given+": one of "+flagList(st, r.Flags))
	}
	return lines
}

func lastJSONRule(rules []flagRule) (flagRule, bool) {
	i := slices.IndexFunc(rules, func(r flagRule) bool { return r.Rule == "one_required_with_json" })
	if i < 0 {
		return flagRule{}, false
	}
	return rules[i], true
}

func flagList(st style.Style, flags []string) string {
	var names []string
	for _, f := range flags {
		names = append(names, st.Flag("--"+f))
	}
	return strings.Join(names, ", ")
}

func annotationList(cmd *cobra.Command, key string) []string {
	return append([]string{}, strings.Fields(cmd.Annotations[key])...)
}

func annotation(items ...string) string {
	return strings.Join(items, " ")
}

func codeList(codes ...api.ErrorCode) string {
	var items []string
	for _, c := range codes {
		items = append(items, string(c))
	}
	return annotation(items...)
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
	fmt.Fprintf(w, "  %s\n", usageText(st, v.Usage))

	if len(v.Examples) > 0 {
		section("Examples")
		for i, e := range v.Examples {
			if i > 0 {
				fmt.Fprintln(w)
			}
			fmt.Fprintf(w, "  %s\n  %s\n", st.Muted("# "+e.Description), usageText(st, e.Command))
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
			usageText(st, identity.Name+" <command> --help"), usageText(st, identity.Name+" help <topic>"))
		return
	}

	var global []string
	for _, f := range v.Flags {
		if f.Global {
			global = append(global, flagText(st, f))
		}
	}
	global = append(global, st.Flag("-h")+"/"+st.Flag("--help"))
	fmt.Fprintf(w, "\n%s %s %s\n", st.Heading("Global flags:"), strings.Join(global, ", "), st.Muted("(see "+identity.Name+" --help)"))
	if len(v.Commands) > 0 {
		fmt.Fprintf(w, "\nRun %s for more about a command.\n", usageText(st, v.Path+" <command> --help"))
	}
}

// exitCodes are what the exit status of wdx means.
var exitCodes = []struct {
	exit    int
	meaning string
}{
	{api.ExitOK, "ok"},
	{api.ExitRejected, "rejected: a conflict, not found or cancelled"},
	{api.ExitUsage, "usage: the command line or a setting is invalid"},
	{api.ExitSystem, "system: an unexpected failure"},
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

// usageText styles a command line: the command, its flags and the
// placeholders of their values.
func usageText(st style.Style, line string) string {
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

// topic is help that is about no single command.
type topic struct {
	name, summary string
	text          func(st style.Style) string
}

var topics = []topic{
	{"json", "what --json prints: results, errors and the help itself", jsonTopic},
	{"settings", "every setting, its default and where it is read from", settingsTopic},
	{"logs", "where runs are logged, and finding one by its debug_id", logsTopic},
}

func findTopic(name string) (topic, bool) {
	i := slices.IndexFunc(topics, func(t topic) bool { return t.name == name })
	if i < 0 {
		return topic{}, false
	}
	return topics[i], true
}

type topicResponse struct {
	Topic topicView `json:"topic"`
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

func newTopicView(t topic) topicView {
	v := topicView{Name: t.name, Summary: t.summary, Text: t.text(style.New(false))}
	if t.name == "settings" {
		v.Settings = []settingView{}
		for _, s := range config.Settings() {
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

func (r topicResponse) text(w io.Writer, st style.Style) {
	t, _ := findTopic(r.Topic.Name)
	fmt.Fprintf(w, "%s: %s\n\n%s\n", st.Heading(st.Command(identity.Name+" help "+t.name)), t.summary, t.text(st))
}

func jsonTopic(st style.Style) string {
	return `With ` + st.Flag("--json") + `, nothing prompts and stdout holds exactly one JSON object and
nothing else, the same the daemon's API returns. The flags that answer a
command's questions are then required up front.

  success: {"result": "created", "project": {...}}
  failure: Problem Details (RFC 9457):
           {"type": "urn:wdx:error:not_found", "title": "...", "status": 404,
            "detail": "...", "code": "not_found", "hint": "...",
            "details": {...}, "debug_id": "dbg-..."}

"result" names what happened; commands that change nothing have none. Keys
are snake_case, absent values null and timestamps RFC 3339. Each command's
help lists its results and error codes, and ` + usageText(st, identity.Name+" <command> --help --json") + `
prints the help itself as JSON.`
}

func settingsTopic(st style.Style) string {
	var b strings.Builder
	b.WriteString(`Read when ` + identity.Name + ` starts; an invalid one fails every command. The home is
read from the environment, the rest from <home>/config.yaml, where
log.level is written as "log: {level: debug}". The daemon reads them when
it starts: run ` + st.Command(identity.Name+" daemon restart") + ` after changing them. ` + st.Command(identity.Name+" settings write") + `
lists every setting in the file, as ` + st.Command(identity.Name+" update") + ` does with a new release's.

`)
	var table style.Table
	for _, s := range config.Settings() {
		def := "required"
		if !s.Required {
			def = "default " + cmp.Or(s.Default, `""`)
		}
		table.Row("  "+st.Name(cmp.Or(s.EnvVar, s.Key)), s.Description+" "+st.Muted("("+def+")"))
	}
	table.Write(&b)
	b.WriteString(`
` + st.Flag("--no-input") + ` and ` + st.Flag("--color") + ` override no_input and color for one command.
NO_COLOR set to anything, or TERM=dumb, turns color off where it is auto.`)
	return b.String()
}

func logsTopic(st style.Style) string {
	return `Every run of ` + identity.Name + ` and every request to the daemon is logged as JSON
lines to <home>/logs/` + identity.Name + `-<date>.log, kept for log.retention_days days.

A failure prints its debug_id; every line of that run, in ` + identity.Name + ` and in the
daemon, carries it. ` + usageText(st, identity.Name+" observe start") + ` shows the logs in Grafana, where
the debug_id finds them too.`
}
