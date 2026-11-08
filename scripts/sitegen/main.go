// Command sitegen writes the website's reference pages (site/src/content/docs/
// docs/reference) from what wdx says about itself: each command's help as
// wdx <command> --help --json prints it, the settings and JSON help topics, the
// error codes of internal/api and CHANGELOG.md. make site-ref runs it from the
// repository's root; TestReferenceCurrent fails when the committed pages differ
// from what it writes. The wdx doc SITE, step C2.
//
// It runs a wdx built from the tree rather than calling the cli, which only
// cmd/wdx may import, and runs it in a made-up environment, so that the pages
// say the same on every machine: a home of /home/you and nothing else.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/srjn45/wdx/internal/api"
)

// referenceDir is where the pages go, relative to the repository's root. sitegen
// owns it: every file in it is written by sitegen, and one it no longer writes is
// removed.
const referenceDir = "site/src/content/docs/docs/reference"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sitegen:", err)
		os.Exit(1)
	}
}

func run() error {
	tmp, err := os.MkdirTemp("", "sitegen")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	bin := filepath.Join(tmp, "wdx")
	if err := build(".", bin, nil); err != nil {
		return err
	}
	pages, err := generate(bin, ".")
	if err != nil {
		return err
	}
	return write(".", pages)
}

// build builds wdx from the tree at root into bin, with env, or this process's
// environment when env is nil.
func build(root, bin string, env []string) error {
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/wdx")
	cmd.Dir = root
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build: %w: %s", err, out)
	}
	return nil
}

// write replaces the reference directory under root with pages.
func write(root string, pages map[string]string) error {
	dir := filepath.Join(root, referenceDir)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for name, content := range pages {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// generate returns every reference page, by its path in the reference
// directory, from the wdx at bin and the repository at root.
func generate(bin, root string) (map[string]string, error) {
	w := wdx{bin: bin}
	rootHelp, err := w.command(nil)
	if err != nil {
		return nil, err
	}
	pages := map[string]string{}
	var tops []command
	for _, c := range rootHelp.Commands {
		top, err := w.tree([]string{c.Name})
		if err != nil {
			return nil, err
		}
		tops = append(tops, top)
		pages["cli/"+c.Name+".md"] = commandPage(top)
	}
	pages["cli/index.md"] = cliIndex(rootHelp, tops)

	settings, err := w.topic("settings")
	if err != nil {
		return nil, err
	}
	pages["settings.md"] = settingsPage(settings)
	jsonTopic, err := w.topic("json")
	if err != nil {
		return nil, err
	}
	pages["json.md"] = jsonPage(jsonTopic)
	pages["errors.md"] = errorsPage(tops)

	changelog, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		return nil, err
	}
	pages["changelog.md"] = changelogPage(string(changelog))
	for name, page := range pages {
		pages[name] = blankLines.ReplaceAllString(page, "\n\n")
	}
	return pages, nil
}

// command is a command's help as --help --json prints it, with its
// subcommands' help filled in by tree.
type command struct {
	Path        string     `json:"path"`
	Summary     string     `json:"summary"`
	Description string     `json:"description"`
	Usage       string     `json:"usage"`
	Flags       []flag     `json:"flags"`
	FlagRules   []flagRule `json:"flag_rules"`
	Examples    []example  `json:"examples"`
	Output      string     `json:"output"`
	Results     []string   `json:"results"`
	Errors      []code     `json:"errors"`
	Commands    []struct {
		Name    string `json:"name"`
		Summary string `json:"summary"`
	} `json:"commands"`
	Subcommands []command `json:"-"`
}

type flag struct {
	Name        string          `json:"name"`
	Shorthand   *string         `json:"shorthand"`
	ValueName   string          `json:"value_name"`
	Default     json.RawMessage `json:"default"`
	Description string          `json:"description"`
	Global      bool            `json:"global"`
}

type flagRule struct {
	Rule   string   `json:"rule"`
	Flags  []string `json:"flags"`
	When   *string  `json:"when"`
	Unless *string  `json:"unless"`
}

type example struct {
	Description string `json:"description"`
	Command     string `json:"command"`
}

type code struct {
	Code        string `json:"code"`
	Exit        int    `json:"exit"`
	Description string `json:"description"`
}

type topic struct {
	Name     string    `json:"name"`
	Summary  string    `json:"summary"`
	Text     string    `json:"text"`
	Settings []setting `json:"settings"`
}

type setting struct {
	Key         string  `json:"key"`
	EnvVar      *string `json:"env_var"`
	Description string  `json:"description"`
	Default     string  `json:"default"`
	Required    bool    `json:"required"`
}

// wdx runs the binary in a made-up environment, from a directory of its own.
type wdx struct{ bin string }

func (w wdx) run(args ...string) ([]byte, error) {
	cmd := exec.Command(w.bin, args...)
	cmd.Env = []string{"HOME=/home/you", "LANG=C.UTF-8"}
	cmd.Dir = os.TempDir()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("wdx %s: %w: %s", strings.Join(args, " "), err, stderr.Bytes())
	}
	return out, nil
}

func (w wdx) command(path []string) (command, error) {
	out, err := w.run(append(slices.Clone(path), "--help", "--json")...)
	if err != nil {
		return command{}, err
	}
	var r struct {
		Command command `json:"command"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return command{}, fmt.Errorf("wdx %s --help --json: %w", strings.Join(path, " "), err)
	}
	return r.Command, nil
}

// tree is the command at path with every command under it.
func (w wdx) tree(path []string) (command, error) {
	c, err := w.command(path)
	if err != nil {
		return command{}, err
	}
	for _, sub := range c.Commands {
		s, err := w.tree(append(slices.Clone(path), sub.Name))
		if err != nil {
			return command{}, err
		}
		c.Subcommands = append(c.Subcommands, s)
	}
	return c, nil
}

func (w wdx) topic(name string) (topic, error) {
	out, err := w.run("help", name, "--json")
	if err != nil {
		return topic{}, err
	}
	var r struct {
		Topic topic `json:"topic"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return topic{}, fmt.Errorf("wdx help %s --json: %w", name, err)
	}
	return r.Topic, nil
}

// --- pages ------------------------------------------------------------------

// blankLines are the runs of empty lines the sections leave between them.
var blankLines = regexp.MustCompile(`\n{3,}`)

const generated = "<!-- Generated by make site-ref (scripts/sitegen) from wdx itself: do not edit. -->\n\n"

func frontmatter(title, description string, order int) string {
	return fmt.Sprintf("---\ntitle: %s\ndescription: %s\nsidebar:\n  order: %d\neditUrl: false\n---\n\n%s",
		yamlString(title), yamlString(description), order, generated)
}

func cliIndex(root command, tops []command) string {
	var b strings.Builder
	b.WriteString(frontmatter("The wdx CLI", "Every wdx command, its flags, output and errors, as wdx --help says them.", 0))
	b.WriteString(prose(root.Description))
	b.WriteString("\n## Commands\n\n| Command | What it does |\n|---|---|\n")
	for _, c := range tops {
		fmt.Fprintf(&b, "| [`%s`](/wdx/docs/reference/cli/%s/) | %s |\n", c.Path, lastWord(c.Path), cell(c.Summary))
	}
	b.WriteString("\n## Flags every command takes\n\n")
	b.WriteString(flagTable(root.Flags))
	b.WriteString("\n## Exit codes\n\n| Exit | Meaning |\n|---|---|\n")
	fmt.Fprintf(&b, "| %d | ok |\n| %d | rejected: a conflict, not found or cancelled |\n| %d | usage: the command line or a setting is invalid |\n| %d | system: an unexpected failure |\n",
		api.ExitOK, api.ExitRejected, api.ExitUsage, api.ExitSystem)
	b.WriteString("\nEach command lists the error codes it can return; [Errors and exit codes](/wdx/docs/reference/errors/) lists them all. With `--json`, see [JSON output](/wdx/docs/reference/json/).\n")
	return b.String()
}

// commandPage is a top-level command's page: the command itself, or a group
// and every command under it, each a section.
func commandPage(top command) string {
	var b strings.Builder
	b.WriteString(frontmatter(top.Path, top.Summary, 1))
	if len(top.Subcommands) == 0 {
		writeCommand(&b, top, false)
		return b.String()
	}
	b.WriteString(prose(top.Summary + "."))
	if top.Description != "" {
		b.WriteString("\n" + prose(top.Description))
	}
	b.WriteString("\n| Command | What it does |\n|---|---|\n")
	leaves := leavesOf(top)
	for _, c := range leaves {
		fmt.Fprintf(&b, "| [`%s`](#%s) | %s |\n", c.Path, slug(c.Path), cell(c.Summary))
	}
	for _, c := range leaves {
		b.WriteString("\n")
		writeCommand(&b, c, true)
	}
	return b.String()
}

func leavesOf(c command) []command {
	if len(c.Subcommands) == 0 {
		return []command{c}
	}
	var out []command
	for _, s := range c.Subcommands {
		out = append(out, leavesOf(s)...)
	}
	return out
}

func writeCommand(b *strings.Builder, c command, heading bool) {
	h := "###"
	if heading {
		fmt.Fprintf(b, "## %s\n\n%s\n", c.Path, prose(c.Summary+"."))
	} else {
		h = "##"
		b.WriteString(prose(c.Summary + "."))
	}
	fmt.Fprintf(b, "\n```sh\n%s\n```\n\n", c.Usage)
	if c.Description != "" {
		b.WriteString(prose(c.Description))
		b.WriteString("\n")
	}
	if flags := ownFlags(c.Flags); len(flags) > 0 {
		fmt.Fprintf(b, "%s Flags\n\n%s", h, flagTable(flags))
		if rules := ruleLines(c.FlagRules); rules != "" {
			b.WriteString("\n" + rules)
		}
		b.WriteString("\n")
	}
	if len(c.Examples) > 0 {
		fmt.Fprintf(b, "%s Examples\n\n```sh\n", h)
		for i, e := range c.Examples {
			if i > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(b, "# %s\n%s\n", e.Description, e.Command)
		}
		b.WriteString("```\n\n")
	}
	if c.Output != "" {
		fmt.Fprintf(b, "%s Output\n\n%s\n", h, prose(c.Output))
	}
	if len(c.Results) > 0 {
		fmt.Fprintf(b, "With `--json`, `result` is one of: %s.\n\n", codeList(c.Results))
	}
	if len(c.Errors) > 0 {
		fmt.Fprintf(b, "%s Errors\n\n| Code | Exit | When |\n|---|---|---|\n", h)
		for _, e := range c.Errors {
			fmt.Fprintf(b, "| [`%s`](/wdx/docs/reference/errors/#%s) | %d | %s |\n", e.Code, slug(e.Code), e.Exit, cell(e.Description))
		}
		b.WriteString("\n")
	}
}

// ownFlags leaves out the flags every command takes, listed once on the index.
func ownFlags(flags []flag) []flag {
	var out []flag
	for _, f := range flags {
		if f.Name == "help" || f.Global || f.Name == "json" || f.Name == "color" || f.Name == "no-input" {
			continue
		}
		out = append(out, f)
	}
	return out
}

func flagTable(flags []flag) string {
	var b strings.Builder
	b.WriteString("| Flag | Default | Description |\n|---|---|---|\n")
	for _, f := range flags {
		name := "--" + f.Name
		if f.Shorthand != nil && *f.Shorthand != "" {
			name = "-" + *f.Shorthand + ", " + name
		}
		if f.ValueName != "" {
			name += " <" + f.ValueName + ">"
		}
		def := ""
		if d := defaultText(f.Default); d != "" {
			def = "`" + d + "`"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", name, def, cell(f.Description))
	}
	return b.String()
}

// defaultText is a flag's default as help shows it: a string as it is, a
// number or a boolean as JSON writes it, and nothing for null, false or "".
func defaultText(raw json.RawMessage) string {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str
	}
	if t := string(raw); t != "null" && t != "false" && t != "" {
		return t
	}
	return ""
}

func ruleLines(rules []flagRule) string {
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
	// The same wording as wdx --help, without its merging of exclusive groups.
	var lines []string
	for _, r := range rules {
		flags := make([]string, len(r.Flags))
		for i, f := range r.Flags {
			flags[i] = "`--" + f + "`"
		}
		list := orJoin(flags)
		switch both := required[key(r.Flags)] && exclusive[key(r.Flags)]; {
		case r.Rule == "required":
			lines = append(lines, list+" is required")
		case r.Rule == "one_required" && both:
			lines = append(lines, "exactly one of "+list)
		case r.Rule == "one_required":
			lines = append(lines, "at least one of "+list)
		case r.Rule == "together":
			lines = append(lines, "all or none of "+strings.Join(flags, ", "))
		case r.Rule == "exclusive" && !both:
			lines = append(lines, "at most one of "+list)
		case r.Rule == "one_required_with_json":
			given := "`--json`"
			if r.When != nil {
				given += " and `--" + *r.When + "`"
			}
			if r.Unless != nil {
				given += ", unless `--" + *r.Unless + "`"
			}
			lines = append(lines, "with "+given+": one of "+list)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "- " + strings.Join(lines, "\n- ") + "\n"
}

func settingsPage(t topic) string {
	var b strings.Builder
	b.WriteString(frontmatter("Settings", "Every wdx setting, its default and where it is read from.", 2))
	b.WriteString(prose(withoutLaidOut(t.Text)))
	b.WriteString("\n| Setting | Default | Description |\n|---|---|---|\n")
	for _, s := range t.Settings {
		key := "`" + s.Key + "`"
		if s.EnvVar != nil {
			key += " (`" + *s.EnvVar + "`)"
		}
		def := ""
		switch {
		case s.Required:
			def = "required"
		case s.Default != "":
			def = "`" + s.Default + "`"
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", key, def, cell(s.Description))
	}
	b.WriteString("\n`wdx setting list` shows each setting's value and where it came from; `wdx setting set --name <key> --value <value>` changes one in `~/.wdx/config.yaml`. See [`wdx setting`](/wdx/docs/reference/cli/setting/).\n")
	return b.String()
}

func jsonPage(t topic) string {
	return frontmatter("JSON output", "What --json prints: results, errors and the help itself.", 3) + prose(t.Text)
}

// errorsPage lists every error code with its status and exit code, and the
// commands that can return it.
func errorsPage(tops []command) string {
	returnedBy := map[string][]string{}
	for _, top := range tops {
		for _, c := range leavesOf(top) {
			for _, e := range c.Errors {
				returnedBy[e.Code] = append(returnedBy[e.Code], c.Path)
			}
		}
	}
	codes := make([]string, 0, len(api.Codes))
	for c := range api.Codes {
		codes = append(codes, string(c))
	}
	sort.Strings(codes)
	var b strings.Builder
	b.WriteString(frontmatter("Errors and exit codes", "Every error code wdx returns, its HTTP status, exit code and meaning.", 4))
	b.WriteString("A failure carries a stable `code`. Text output ends its error line with it, as `wdx: ... [not_found]`; with `--json`, stdout holds Problem Details with the same `code` (see [JSON output](/wdx/docs/reference/json/)). The exit code says what kind of failure it was: 1 rejected, 2 usage, 3 a system failure.\n\n")
	for _, c := range codes {
		info := api.Codes[api.ErrorCode(c)]
		fmt.Fprintf(&b, "## %s\n\n%s.\n\nExit %d · HTTP %d", c, upperFirst(info.Description), info.Exit, info.Status)
		if by := returnedBy[c]; len(by) > 0 {
			sort.Strings(by)
			links := make([]string, len(by))
			for i, p := range by {
				links[i] = fmt.Sprintf("[`%s`](%s)", p, commandLink(p))
			}
			b.WriteString(" · returned by " + strings.Join(links, ", "))
		}
		b.WriteString("\n\n")
	}
	return b.String()
}

// commandLink is where a leaf command is described: its own page when it is a
// top-level command, a section of its group's page otherwise.
func commandLink(path string) string {
	words := strings.Fields(path)
	page := "/wdx/docs/reference/cli/" + words[1] + "/"
	if len(words) == 2 {
		return page
	}
	return page + "#" + slug(path)
}

func changelogPage(changelog string) string {
	body := changelog
	if i := strings.Index(body, "\n"); strings.HasPrefix(body, "# ") && i >= 0 {
		body = strings.TrimLeft(body[i+1:], "\n")
	}
	return frontmatter("Changelog", "Every release of wdx and what changed in it.", 5) + body
}

// --- text -------------------------------------------------------------------

// prose turns help text, wrapped for a terminal, into Markdown: the lines of a
// paragraph joined, and a paragraph that is laid out, with lines indented or
// starting with -, kept as it is in a text block.
func prose(text string) string {
	var b strings.Builder
	for _, para := range strings.Split(strings.TrimSpace(text), "\n\n") {
		lines := strings.Split(para, "\n")
		laidOut := false
		for _, l := range lines {
			if strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") {
				laidOut = true
			}
		}
		if laidOut {
			fmt.Fprintf(&b, "```text\n%s\n```\n\n", para)
			continue
		}
		var out []string
		for _, l := range lines {
			if strings.HasPrefix(l, "- ") && len(out) > 0 {
				b.WriteString(escape(strings.Join(out, " ")) + "\n")
				out = nil
			}
			out = append(out, strings.TrimSpace(l))
		}
		b.WriteString(escape(strings.Join(out, " ")) + "\n\n")
	}
	return b.String()
}

// withoutLaidOut is text without its laid-out paragraphs, as the settings
// topic's own list, which the page shows as a table instead.
func withoutLaidOut(text string) string {
	var kept []string
	for _, para := range strings.Split(strings.TrimSpace(text), "\n\n") {
		if !strings.Contains("\n"+para, "\n ") {
			kept = append(kept, para)
		}
	}
	return strings.Join(kept, "\n\n")
}

// escape keeps help text from being read as HTML or Markdown it is not, and
// puts its flags in code, where -- is not made a dash.
func escape(s string) string {
	s = strings.NewReplacer("<", "&lt;", ">", "&gt;", "*", "\\*", "_", "\\_", "`", "\\`").Replace(s)
	return flagWords.ReplaceAllString(s, "$1`$2`")
}

// flagWords are the flags help text names, as --name.
var flagWords = regexp.MustCompile(`(^|[\s(])(--[a-z][a-z0-9-]*)`)

func cell(s string) string {
	return strings.ReplaceAll(escape(strings.Join(strings.Fields(s), " ")), "|", "\\|")
}

func codeList(items []string) string {
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = "`" + s + "`"
	}
	return orJoin(out)
}

func orJoin(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

// slug is the anchor Starlight gives a heading.
func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

func lastWord(path string) string {
	f := strings.Fields(path)
	return f[len(f)-1]
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func yamlString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
