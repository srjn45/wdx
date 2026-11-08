package settings

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"

	"go.yaml.in/yaml/v3"

	"github.com/srjn45/wdx/internal/atomicfile"
)

// lineWidth is the width the comments of a written config file wrap at.
const lineWidth = 78

// setSource is where a problem says a value given to Rewrite came from.
const setSource = "--value"

// Rewritten is a config file written anew from a table.
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

// Rewrite returns the config file at path written anew: the table's
// FileHeader, then every setting the file can hold with its description, in
// the order of the table; the values the file sets, as it wrote them; the
// defaults of the others, commented; and the keys no setting has, commented at
// the end under DroppedHeading with those an earlier rewrite put there. The
// values of set, by key, replace those of the file. Comments of the file's own
// are not kept. A missing file gives every setting commented. It fails with an
// *Error when the file cannot be read or parsed, a value it sets is invalid,
// set holds what cannot be set, or the settings do not resolve from the file
// with the sources over layered over it, as the environment is when they
// load; keys the file has that no setting has are no error.
func (t Table[C]) Rewrite(path string, set map[string]string, over ...Source) (Rewritten, error) {
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Rewritten{}, &Error{Problems: []Problem{unreadable(path, err)}}
	}
	r, problems := t.parseFile(path, old)
	problems = slices.DeleteFunc(problems, func(p Problem) bool { return p.Reason == unknownSetting })
	problems = append(problems, r.override(set)...)
	if _, _, err := t.Resolve(append([]Source{r.values}, over...)); err != nil {
		if e, ok := errors.AsType[*Error](err); ok {
			problems = append(problems, e.Problems...)
		}
	}
	if len(problems) > 0 {
		return Rewritten{}, &Error{Problems: problems}
	}

	kept := t.droppedLines(old)
	var dropped []string
	for _, u := range r.unknown {
		dropped = append(dropped, u.key)
		if line := "# " + u.key + ": " + u.value; !slices.Contains(kept, line) {
			kept = append(kept, line)
		}
	}
	listed := t.listedKeys(old)
	added := []string{}
	for _, s := range t.Rows {
		if !s.FromEnv && !listed[s.Key] {
			added = append(added, s.Key)
		}
	}
	content := t.render(r.nodes, kept)
	return Rewritten{
		Content: content,
		Changed: !bytes.Equal(content, old),
		Added:   added,
		Dropped: append([]string{}, dropped...),
	}, nil
}

// override replaces the values read with those of set, which Resolve then
// checks as it does the file's.
func (r *fileReader[C]) override(set map[string]string) []Problem {
	var problems []Problem
	for _, key := range slices.Sorted(maps.Keys(set)) {
		value := set[key]
		if row, ok := r.table.row(key); !ok || row.FromEnv {
			problems = append(problems, Problem{Key: key, From: setSource, Value: &value, Reason: "not a setting of the config file"})
			continue
		}
		r.values[key] = fileValue{value, setSource}
		r.nodes[key] = plain(value)
	}
	return problems
}

// Set sets the setting key in the config file at path, which it writes anew
// as Rewrite does, with over, holding lockPath with Lock meanwhile. The file
// is left as it was when Rewrite fails.
func (t Table[C]) Set(path, lockPath, key, value string, over ...Source) (Rewritten, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Rewritten{}, err
	}
	unlock, err := Lock(lockPath)
	if err != nil {
		return Rewritten{}, err
	}
	defer unlock()
	r, err := t.Rewrite(path, map[string]string{key: value}, over...)
	if err != nil {
		return Rewritten{}, err
	}
	if r.Changed {
		if err := WriteFile(path, r.Content); err != nil {
			return Rewritten{}, err
		}
	}
	return r, nil
}

// Lock keeps others from rewriting a config file until unlock is called,
// holding the file at lockPath, whose directory must exist: two rewrites at
// once would lose one's value.
func Lock(lockPath string) (unlock func(), err error) {
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		// Unlock before closing: a child forked meanwhile shares the file
		// until it execs, and closing alone would leave the lock held.
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// WriteFile writes content to path, readable by the user only, so that a
// program starting meanwhile reads the old file or the new one.
func WriteFile(path string, content []byte) error {
	return atomicfile.Write(path, content, 0o600)
}

// render writes the config file: values holds the nodes of the values set,
// by key; dropped the commented lines of the section at the end.
func (t Table[C]) render(values map[string]*yaml.Node, dropped []string) []byte {
	var b strings.Builder
	b.WriteString(t.FileHeader)

	renderLevel(&b, values, t.fileRows(), "", "", false)

	if len(dropped) > 0 {
		b.WriteString("\n" + t.DroppedHeading + "\n")
		for _, line := range dropped {
			b.WriteString(line + "\n")
		}
	}
	return []byte(b.String())
}

// fileRows are the rows the config file holds.
func (t Table[C]) fileRows() []Row[C] {
	return slices.DeleteFunc(slices.Clone(t.Rows), func(s Row[C]) bool { return s.FromEnv })
}

// renderLevel writes the settings of list, every key starting with base, each line
// after prefix: a setting alone, the settings under one section together under its
// heading, in the order the table first names them, at any depth. A section with
// no value set is commented whole; commented says an outer one is.
func renderLevel[C any](b *strings.Builder, values map[string]*yaml.Node, list []Row[C], base, prefix string, commented bool) {
	var order []string
	byName := map[string][]Row[C]{}
	for _, s := range list {
		name, _, nested := strings.Cut(strings.TrimPrefix(s.Key, base), ".")
		if nested {
			name += "."
		}
		if _, seen := byName[name]; !seen {
			order = append(order, name)
		}
		byName[name] = append(byName[name], s)
	}
	for _, name := range order {
		group := byName[name]
		if base == "" {
			b.WriteString("\n")
		}
		section, isSection := strings.CutSuffix(name, ".")
		if !isSection {
			s := group[0]
			comment(b, prefix+"# ", sentence(s.Description))
			v, ok := values[s.Key]
			switch {
			case ok:
				b.WriteString(prefix + name + ": " + scalar(v) + "\n")
			case commented:
				b.WriteString(prefix + name + ": " + scalar(plain(s.Default)) + "\n")
			default:
				b.WriteString(prefix + "# " + name + ": " + scalar(plain(s.Default)) + "\n")
			}
			continue
		}
		set := slices.ContainsFunc(group, func(s Row[C]) bool { _, ok := values[s.Key]; return ok })
		inner := prefix + "  "
		if set || commented {
			b.WriteString(prefix + section + ":\n")
		} else {
			b.WriteString(prefix + "# " + section + ":\n")
			inner = prefix + "#   "
		}
		renderLevel(b, values, group, base+section+".", inner, commented || !set)
	}
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
func (t Table[C]) droppedLines(content []byte) []string {
	_, section, found := strings.Cut(string(content), "\n"+t.DroppedHeading+"\n")
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
// log.level as "log:" and "  level: debug" set it, two columns for each level
// deeper. The section of dropped keys is left out.
func (t Table[C]) listedKeys(content []byte) map[string]bool {
	text, _, _ := strings.Cut(string(content), "\n"+t.DroppedHeading+"\n")
	listed := map[string]bool{}
	var path []string
	for line := range strings.Lines(text) {
		line = strings.TrimRight(line, "\n")
		if rest, ok := strings.CutPrefix(line, "#"); ok {
			line = strings.TrimPrefix(rest, " ")
		}
		trimmed := strings.TrimLeft(line, " ")
		depth := len(line) - len(trimmed)
		if rest, ok := strings.CutPrefix(trimmed, "#"); ok && depth > 0 {
			// "# " stands where the line's own two columns would be.
			trimmed = strings.TrimLeft(rest, " ")
			depth += len(rest) - len(trimmed) - 1
		}
		m := listedKey.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		level := min(depth/2, len(path))
		path = append(path[:level], m[1])
		listed[strings.Join(path, ".")] = true
	}
	return listed
}
