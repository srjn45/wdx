package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/srjn45/wdx/internal/identity"
)

// lineWidth is the width the comments of a written config file wrap at.
const lineWidth = 78

// droppedHeading starts the section at the end of a written config file that
// holds, commented, the keys no setting has.
var droppedHeading = "# No longer read by " + identity.Name + ", kept for reference:"

// Rewritten is a config file written anew from the settings table.
type Rewritten struct {
	Content []byte
	// Changed is false when Content is the file as it was.
	Changed bool
	// Added are the settings the file did not list, set or commented.
	Added []string
	// Dropped are the keys it set that no setting has, now commented at
	// the end.
	Dropped []string
}

// Rewrite returns the config file at path written anew: every setting with
// its description, in the order of the table; the values the file sets,
// as it wrote them; the defaults of the others, commented; and the keys no
// setting has, commented at the end with those an earlier rewrite put
// there. Comments of the file's own are not kept. A missing file gives
// every setting commented. It fails with an *Error when the file cannot be
// read or parsed or a value it sets is invalid; keys no setting has are no
// error.
func Rewrite(path string) (Rewritten, error) {
	return rewrite(settings, path)
}

func rewrite(table []setting, path string) (Rewritten, error) {
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		if pe, ok := errors.AsType[*fs.PathError](err); ok {
			err = pe.Err
		}
		return Rewritten{}, &Error{Problems: []Problem{{From: path, Reason: "cannot be read: " + err.Error()}}}
	}
	r, problems := parseFile(path, old, table)
	problems = slices.DeleteFunc(problems, func(p Problem) bool { return p.Reason == unknownSetting })
	if _, err := resolve(table, []Source{r.values}); err != nil {
		if e, ok := errors.AsType[*Error](err); ok {
			problems = append(problems, e.Problems...)
		}
	}
	if len(problems) > 0 {
		return Rewritten{}, &Error{Problems: problems}
	}

	kept := droppedLines(old)
	var dropped []string
	for _, u := range r.unknown {
		dropped = append(dropped, u.key)
		if line := "# " + u.key + ": " + u.value; !slices.Contains(kept, line) {
			kept = append(kept, line)
		}
	}
	listed := listedKeys(old)
	added := []string{}
	for _, s := range table {
		if !s.fromEnv && !listed[s.key] {
			added = append(added, s.key)
		}
	}
	content := render(table, r.nodes, kept)
	return Rewritten{
		Content: content,
		Changed: !bytes.Equal(content, old),
		Added:   added,
		Dropped: append([]string{}, dropped...),
	}, nil
}

// WriteFile writes content to path, readable by the user only, renamed into
// place so a wdx starting meanwhile reads the old file or the new one.
func WriteFile(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// render writes the config file of table: values holds the nodes of the
// values set, by key; dropped the commented lines of the section at the end.
func render(table []setting, values map[string]*yaml.Node, dropped []string) []byte {
	var b strings.Builder
	name := identity.Name
	b.WriteString("# " + name + " settings, read when " + name + " and its daemon start; after changing them run:\n")
	b.WriteString("#   " + name + " daemon restart\n")
	b.WriteString("# Every setting is optional; uncomment one to change it. See " + name + " help settings.\n")
	comment(&b, "# ", name+" update and "+name+" settings write rewrite this file: they keep the values set and add new settings, but not comments of your own.")

	for _, group := range groups(table) {
		b.WriteString("\n")
		section, _, nested := strings.Cut(group[0].key, ".")
		if !nested {
			s := group[0]
			comment(&b, "# ", sentence(s.description))
			if v, ok := values[s.key]; ok {
				b.WriteString(s.key + ": " + scalar(v) + "\n")
			} else {
				b.WriteString("# " + s.key + ": " + scalar(plain(s.def)) + "\n")
			}
			continue
		}
		set := slices.ContainsFunc(group, func(s setting) bool { _, ok := values[s.key]; return ok })
		prefix := "#   " // the whole section commented
		if set {
			b.WriteString(section + ":\n")
			prefix = "  "
		} else {
			b.WriteString("# " + section + ":\n")
		}
		for _, s := range group {
			leaf := strings.TrimPrefix(s.key, section+".")
			comment(&b, prefix+"# ", sentence(s.description))
			if v, ok := values[s.key]; ok {
				b.WriteString(prefix + leaf + ": " + scalar(v) + "\n")
			} else if set {
				b.WriteString(prefix + "# " + leaf + ": " + scalar(plain(s.def)) + "\n")
			} else {
				b.WriteString(prefix + leaf + ": " + scalar(plain(s.def)) + "\n")
			}
		}
	}

	if len(dropped) > 0 {
		b.WriteString("\n" + droppedHeading + "\n")
		for _, line := range dropped {
			b.WriteString(line + "\n")
		}
	}
	return []byte(b.String())
}

// groups splits the settings of the config file by section: each top-level
// setting alone, the settings under one section together, in the order the
// table first names them.
func groups(table []setting) [][]setting {
	var order []string
	bySection := map[string][]setting{}
	for _, s := range table {
		if s.fromEnv {
			continue
		}
		section := s.key
		if before, _, nested := strings.Cut(s.key, "."); nested {
			section = before + "."
		}
		if _, seen := bySection[section]; !seen {
			order = append(order, section)
		}
		bySection[section] = append(bySection[section], s)
	}
	var list [][]setting
	for _, section := range order {
		list = append(list, bySection[section])
	}
	return list
}

// comment writes text wrapped at lineWidth, each line after prefix.
func comment(b *strings.Builder, prefix, text string) {
	line := prefix
	for _, word := range strings.Fields(text) {
		if line != prefix && len(line)+1+len(word) > lineWidth {
			b.WriteString(line + "\n")
			line = prefix
		}
		if line != prefix {
			line += " "
		}
		line += word
	}
	b.WriteString(line + "\n")
}

// sentence makes a description a sentence: capitalized, ending in a period.
func sentence(description string) string {
	return strings.ToUpper(description[:1]) + description[1:] + "."
}

// plain is the node of a default, written plain unless YAML needs quotes.
func plain(value string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Value: value}
	if value == "" {
		n.Style = yaml.DoubleQuotedStyle // empty, not null
	}
	return n
}

// scalar writes the scalar n as YAML: in the style the file wrote it, or
// quoted when YAML would not read it back as the same value.
func scalar(n *yaml.Node) string {
	v := &yaml.Node{Kind: yaml.ScalarNode, Tag: n.Tag, Value: n.Value, Style: n.Style}
	if strings.Contains(n.Value, "\n") {
		v.Style = yaml.DoubleQuotedStyle
	}
	out, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%q", n.Value)
	}
	return strings.TrimSuffix(string(out), "\n")
}

// flow writes any node as YAML on one line.
func flow(n *yaml.Node) string {
	if n.Kind == yaml.ScalarNode {
		return scalar(n)
	}
	c := *n
	c.Style = yaml.FlowStyle
	out, err := yaml.Marshal(&c)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(string(out), "\n")
}

// droppedLines returns the lines of the section a rewrite put at the end of
// content.
func droppedLines(content []byte) []string {
	_, section, found := strings.Cut(string(content), "\n"+droppedHeading+"\n")
	if !found {
		return nil
	}
	var lines []string
	for line := range strings.Lines(section) {
		if line = strings.TrimRight(line, "\n"); strings.HasPrefix(line, "# ") {
			lines = append(lines, line)
		}
	}
	return lines
}

var listedKey = regexp.MustCompile(`^([a-z][a-z0-9_]*):(\s|$)`)

// listedKeys returns the keys content lists, set or commented the way a
// written config file comments them: "# log:" and "#   level: info" list
// log.level as "log:" and "  level: debug" set it. The section of dropped
// keys is left out.
func listedKeys(content []byte) map[string]bool {
	text, _, _ := strings.Cut(string(content), "\n"+droppedHeading+"\n")
	listed := map[string]bool{}
	section := ""
	for line := range strings.Lines(text) {
		line = strings.TrimRight(line, "\n")
		if rest, ok := strings.CutPrefix(line, "#"); ok {
			line = strings.TrimPrefix(rest, " ")
		}
		trimmed := strings.TrimLeft(line, " ")
		indented := len(trimmed) < len(line)
		if rest, ok := strings.CutPrefix(trimmed, "#"); ok && indented {
			trimmed = strings.TrimLeft(rest, " ")
		}
		m := listedKey.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		if indented {
			listed[section+"."+m[1]] = true
		} else {
			section = m[1]
			listed[m[1]] = true
		}
	}
	return listed
}
