package settings

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// fileSource holds the values of a config file by key, with where each was
// found.
type fileSource map[string]fileValue

type fileValue struct {
	value, from string
}

func (f fileSource) Lookup(key string) (string, string, bool) {
	v, ok := f[key]
	return v.value, v.from, ok
}

// File reads the config file at path, a YAML mapping whose nested keys make
// the dotted keys of the table: "log: {level: debug}" sets log.level. A
// missing file sets nothing. Its problems are those of the file: one that
// cannot be read or parsed, a key twice, a key no row has, a value that is
// not single, a FromEnv row's key; a null value is not set.
func (t Table[C]) File(path string) (Source, []Problem) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fileSource(nil), nil
	}
	if err != nil {
		return fileSource(nil), []Problem{unreadable(path, err)}
	}
	r, problems := t.parseFile(path, data)
	return r.values, problems
}

func unreadable(path string, err error) Problem {
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		err = pe.Err
	}
	return Problem{From: path, Reason: "cannot be read: " + err.Error()}
}

// parseFile reads data, the config file at path.
func (t Table[C]) parseFile(path string, data []byte) (*fileReader[C], []Problem) {
	r := &fileReader[C]{path: path, table: t, values: fileSource{}, nodes: map[string]*yaml.Node{}, lines: map[string]int{}}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return r, yamlProblems(path, err)
	}
	if len(doc.Content) == 0 {
		return r, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return r, []Problem{{From: at(path, root), Reason: "not a mapping of settings"}}
	}
	r.walk("", root)
	return r, r.problems
}

// fileReader collects the values of a config file and its problems.
type fileReader[C any] struct {
	path     string
	table    Table[C]
	values   fileSource
	nodes    map[string]*yaml.Node // the node of each value, as written
	unknown  []unknownKey          // the keys no setting has, nested ones flattened
	lines    map[string]int        // where each key was first seen
	problems []Problem
}

// unknownKey is a key of a config file that no setting has, with its value
// written as YAML on one line.
type unknownKey struct {
	key, value string
}

// addUnknown records key and the keys under it.
func (r *fileReader[C]) addUnknown(key string, v *yaml.Node) {
	if v.Kind != yaml.MappingNode {
		r.unknown = append(r.unknown, unknownKey{key, flow(v)})
		return
	}
	for i := 0; i+1 < len(v.Content); i += 2 {
		r.addUnknown(key+"."+v.Content[i].Value, v.Content[i+1])
	}
}

func (r *fileReader[C]) walk(prefix string, m *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		key := prefix + k.Value
		from := at(r.path, k)
		row, isRow := r.table.row(key)
		if line, ok := r.lines[key]; ok {
			r.problem(key, from, fmt.Sprintf("already set at line %d", line))
			continue
		}
		r.lines[key] = k.Line
		switch {
		case isRow && row.FromEnv:
			r.problem(key, from, "set by "+r.table.envVar(key)+", not the config file")
		case isRow && v.Kind == yaml.ScalarNode:
			if v.Tag != "!!null" {
				r.values[key] = fileValue{v.Value, from}
				r.nodes[key] = v
			}
		case isRow:
			r.problem(key, from, "not a single value")
		case v.Kind == yaml.MappingNode && slices.ContainsFunc(r.table.Rows, func(s Row[C]) bool { return strings.HasPrefix(s.Key, key+".") }):
			r.walk(key+".", v)
		default:
			r.problem(key, from, unknownSetting)
			r.addUnknown(key, v)
		}
	}
}

func (r *fileReader[C]) problem(key, from, reason string) {
	r.problems = append(r.problems, Problem{Key: key, From: from, Reason: reason})
}

var yamlLine = regexp.MustCompile(`^line (\d+): `)

// yamlProblems turns a YAML error, "yaml: line 3: ...", into problems found
// at path:3.
func yamlProblems(path string, err error) []Problem {
	msgs := []string{strings.TrimPrefix(err.Error(), "yaml: ")}
	if te, ok := errors.AsType[*yaml.TypeError](err); ok {
		msgs = te.Errors
	}
	var problems []Problem
	for _, msg := range msgs {
		p := Problem{From: path, Reason: msg}
		if m := yamlLine.FindStringSubmatch(msg); m != nil {
			p.From, p.Reason = path+":"+m[1], msg[len(m[0]):]
		}
		problems = append(problems, p)
	}
	return problems
}

func at(path string, n *yaml.Node) string {
	return fmt.Sprintf("%s:%d", path, n.Line)
}
