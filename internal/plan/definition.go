// Package plan keeps a project's plans: the work of a feature broken into tasks
// for agents, or a big plan broken into smaller ones, each with what it waits for.
// See docs/PLANS.md.
package plan

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Definition is what a plan's YAML says. It holds either Tasks or Plans, never
// both, each in DAG order.
type Definition struct {
	Goal        string
	Constraints []string
	Tasks       []Task  // nil for a plan of plans
	Plans       []Child // nil for a plan of tasks
}

// Task is one task of a plan: a prompt for an agent, and the tasks it waits for.
type Task struct {
	ID     string
	After  []string
	Prompt string
	Line   int
}

// Child is one plan a plan of plans lists, by name, and the plans it waits for.
type Child struct {
	ID    string
	After []string
	Line  int // so that a check of the child against the store can name it
}

// Problem is one thing wrong with a plan's YAML, at a line of it.
type Problem struct {
	Line    int
	Message string
}

func (p Problem) String() string { return fmt.Sprintf("line %d: %s", p.Line, p.Message) }

// A task's id goes into its agent's and its branch's names, which cannot hold _;
// a child's is a plan's name, which is at most 95 so that it fits a doc's with the
// prefix plans are stored with.
var idChars = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

const (
	maxTaskID  = 50
	maxChildID = 95
)

// entry is a task or a child, as the check sees both.
type entry struct {
	id     string
	after  []string
	prompt string
	line   int
	key    *yaml.Node // the entry's id key, for its line
	value  *yaml.Node // the id itself, which a copy renames
	item   *yaml.Node // the entry's mapping, which a copy may leave out
	afters []*yaml.Node
	seq    *yaml.Node // its after's sequence, which reduction edits
}

// parsed is a plan's YAML read, with the nodes reduction rewrites.
type parsed struct {
	doc      yaml.Node
	def      Definition
	tasks    bool       // a plan of tasks, not of plans
	entries  []*entry   // as in the file
	listed   *yaml.Node // the sequence of tasks or plans
	ordered  []*entry   // in DAG order, once checked
	problems []Problem
}

// Parse reads a plan's YAML and checks it, returning every problem found, by
// line. The definition is only of use when there are none.
func Parse(content []byte) (Definition, []Problem) {
	p := parse(content)
	return p.def, p.problems
}

func parse(content []byte) *parsed {
	p := &parsed{}
	if err := yaml.Unmarshal(content, &p.doc); err != nil {
		p.problems = yamlProblems(err)
		return p
	}
	if len(p.doc.Content) == 0 {
		p.problem(1, "the plan is empty: it needs a goal, and tasks or plans")
		return p
	}
	root := p.doc.Content[0]
	if root.Kind != yaml.MappingNode {
		p.problem(root.Line, "not a mapping of goal, constraints, and tasks or plans")
		return p
	}
	p.top(root)
	if len(p.problems) == 0 {
		p.order()
	}
	if len(p.problems) == 0 {
		p.fill()
	}
	slices.SortStableFunc(p.problems, func(a, b Problem) int { return a.Line - b.Line })
	return p
}

func (p *parsed) problem(line int, format string, args ...any) {
	p.problems = append(p.problems, Problem{Line: line, Message: fmt.Sprintf(format, args...)})
}

// top reads the plan's keys.
func (p *parsed) top(root *yaml.Node) {
	var goal, tasks, plans *yaml.Node
	for _, kv := range p.pairs(root, "goal", "constraints", "tasks", "plans") {
		switch kv.key.Value {
		case "goal":
			goal = kv.key
			if text, ok := p.text(kv.value, "goal"); ok {
				p.def.Goal = text
			}
		case "constraints":
			p.def.Constraints = p.texts(kv.value, "constraints")
		case "tasks":
			tasks, p.listed = kv.key, kv.value
			p.entries = p.list(kv.value, "tasks", "task", maxTaskID, true)
		case "plans":
			plans, p.listed = kv.key, kv.value
			p.entries = p.list(kv.value, "plans", "plan", maxChildID, false)
		}
	}
	switch {
	case goal == nil:
		p.problem(root.Line, "no goal: every task's agent is told it")
	case strings.TrimSpace(p.def.Goal) == "" && !p.reported(goal.Line):
		p.problem(goal.Line, "the goal is blank")
	}
	switch {
	case tasks != nil && plans != nil:
		p.problem(plans.Line, "both tasks and plans: a plan has one or the other")
		p.entries = nil
	case tasks == nil && plans == nil:
		p.problem(root.Line, "no tasks or plans")
	}
	if len(p.problems) > 0 {
		return
	}
	p.tasks = tasks != nil
	p.checkAfter()
}

// reported says whether a problem was already found at line.
func (p *parsed) reported(line int) bool {
	return slices.ContainsFunc(p.problems, func(pr Problem) bool { return pr.Line == line })
}

type pair struct{ key, value *yaml.Node }

// pairs returns m's keys and values, reporting a key not in known, or one given
// twice.
func (p *parsed) pairs(m *yaml.Node, known ...string) []pair {
	var list []pair
	seen := map[string]int{}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		switch line, twice := seen[k.Value]; {
		case !slices.Contains(known, k.Value):
			p.problem(k.Line, "unknown key %q: only %s", k.Value, strings.Join(known, ", "))
		case twice:
			p.problem(k.Line, "%s given twice, first at line %d", k.Value, line)
		default:
			seen[k.Value] = k.Line
			list = append(list, pair{k, v})
		}
	}
	return list
}

// text returns n's value if it is a single one, as text.
func (p *parsed) text(n *yaml.Node, what string) (string, bool) {
	if n.Kind != yaml.ScalarNode {
		p.problem(n.Line, "%s is not text", what)
		return "", false
	}
	if n.Tag == "!!null" {
		return "", true
	}
	return n.Value, true
}

// texts returns n's values if it is a list of texts.
func (p *parsed) texts(n *yaml.Node, what string) []string {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.SequenceNode {
		p.problem(n.Line, "%s is not a list", what)
		return nil
	}
	var list []string
	for _, item := range n.Content {
		if text, ok := p.text(item, "an item of "+what); ok {
			list = append(list, text)
		}
	}
	return list
}

// list reads the entries of tasks or plans.
func (p *parsed) list(n *yaml.Node, what, one string, maxID int, prompted bool) []*entry {
	if n.Kind != yaml.SequenceNode || len(n.Content) == 0 {
		p.problem(n.Line, "%s is not a list of at least one %s", what, one)
		return nil
	}
	known := []string{"id", "after"}
	if prompted {
		known = []string{"id", "after", "prompt"}
	}
	var entries []*entry
	ids := map[string]int{}
	for _, item := range n.Content {
		if item.Kind != yaml.MappingNode {
			p.problem(item.Line, "a %s is not a mapping of %s", one, strings.Join(known, ", "))
			continue
		}
		e := &entry{line: item.Line, item: item}
		var prompt *yaml.Node
		for _, kv := range p.pairs(item, known...) {
			switch kv.key.Value {
			case "id":
				e.key, e.value = kv.key, kv.value
				e.id, _ = p.text(kv.value, "the id")
			case "after":
				e.seq = p.afterList(kv.value)
			case "prompt":
				prompt = kv.key
				e.prompt, _ = p.text(kv.value, "the prompt")
			}
		}
		if e.key != nil {
			e.line = e.key.Line
		}
		switch {
		case e.key == nil:
			p.problem(item.Line, "a %s with no id", one)
		case e.id == "":
			if !p.reported(e.line) {
				p.problem(e.line, "the id is blank")
			}
		case !idChars.MatchString(e.id) || len(e.id) > maxID:
			p.problem(e.line, "id %q can only have letters, numbers and -, at most %d", e.id, maxID)
		default:
			if line, twice := ids[strings.ToLower(e.id)]; twice {
				p.problem(e.line, "id %q is used twice, first at line %d", e.id, line)
			}
			ids[strings.ToLower(e.id)] = e.line
		}
		if prompted {
			switch {
			case prompt == nil:
				p.problem(item.Line, "task %s has no prompt", e.id)
			case strings.TrimSpace(e.prompt) == "" && !p.reported(prompt.Line):
				p.problem(prompt.Line, "the prompt of task %s is blank", e.id)
			}
		}
		entries = append(entries, e)
	}
	return entries
}

// afterList returns n if it is a list of ids, or nil and a problem.
func (p *parsed) afterList(n *yaml.Node) *yaml.Node {
	if n.Kind != yaml.SequenceNode {
		p.problem(n.Line, "after is not a list of ids")
		return nil
	}
	for _, item := range n.Content {
		if item.Kind != yaml.ScalarNode || item.Tag == "!!null" {
			p.problem(item.Line, "an item of after is not an id")
			return nil
		}
	}
	return n
}

// checkAfter resolves each after to the entry it names, ignoring case as ids are
// unique ignoring it, and reports one naming no entry, the entry itself, or
// another twice.
func (p *parsed) checkAfter() {
	byID := map[string]*entry{}
	for _, e := range p.entries {
		byID[strings.ToLower(e.id)] = e
	}
	for _, e := range p.entries {
		if e.seq == nil {
			continue
		}
		for _, item := range e.seq.Content {
			target, ok := byID[strings.ToLower(item.Value)]
			switch {
			case !ok:
				p.problem(item.Line, "%s waits for %s, which the plan does not have", e.id, item.Value)
			case target == e:
				p.problem(item.Line, "%s waits for itself", e.id)
			case slices.Contains(e.after, target.id):
				p.problem(item.Line, "%s waits for %s twice", e.id, target.id)
			default:
				e.after = append(e.after, target.id)
				e.afters = append(e.afters, item)
			}
		}
	}
}

// order puts the entries in DAG order, reporting every cycle. Kahn's algorithm,
// always taking the ready entry that comes first in the file, so that a plan
// written in order reads as written, and the order is the same every time.
func (p *parsed) order() {
	done := map[string]bool{}
	var ordered []*entry
	for len(ordered) < len(p.entries) {
		i := slices.IndexFunc(p.entries, func(e *entry) bool {
			return !done[e.id] && !slices.ContainsFunc(e.after, func(a string) bool { return !done[a] })
		})
		if i < 0 {
			p.cycles(done)
			return
		}
		done[p.entries[i].id] = true
		ordered = append(ordered, p.entries[i])
	}
	p.ordered = ordered
}

// fill puts the entries in the definition, in DAG order.
func (p *parsed) fill() {
	p.def.Tasks, p.def.Plans = nil, nil
	for _, e := range p.ordered {
		after := slices.Clone(e.after)
		if after == nil {
			after = []string{}
		}
		if p.tasks {
			p.def.Tasks = append(p.def.Tasks, Task{ID: e.id, After: after, Prompt: e.prompt, Line: e.line})
		} else {
			p.def.Plans = append(p.def.Plans, Child{ID: e.id, After: after, Line: e.line})
		}
	}
}

// cycles reports each cycle among the entries order could not take, each once,
// naming its entries in the order they wait for each other.
func (p *parsed) cycles(done map[string]bool) {
	byID := map[string]*entry{}
	for _, e := range p.entries {
		byID[e.id] = e
	}
	inCycle := map[string]bool{}
	for _, start := range p.entries {
		if done[start.id] || inCycle[start.id] {
			continue
		}
		// Follow the first after not taken until an entry comes again: every entry
		// left waits for one left, so this always ends on a cycle.
		path, at := []string{}, map[string]int{}
		for id := start.id; ; {
			if i, seen := at[id]; seen {
				cycle := append(slices.Clone(path[i:]), id)
				if !slices.ContainsFunc(cycle, func(c string) bool { return inCycle[c] }) {
					p.problem(byID[cycle[0]].line, "a cycle: %s", strings.Join(cycle, " waits for "))
				}
				for _, c := range cycle {
					inCycle[c] = true
				}
				break
			}
			at[id] = len(path)
			path = append(path, id)
			next := slices.IndexFunc(byID[id].after, func(a string) bool { return !done[a] })
			id = byID[id].after[next]
		}
	}
}

var yamlLine = regexp.MustCompile(`^line (\d+): `)

// yamlProblems turns a YAML error, "yaml: line 3: ...", into problems at line 3.
func yamlProblems(err error) []Problem {
	msgs := []string{strings.TrimPrefix(err.Error(), "yaml: ")}
	if te, ok := errors.AsType[*yaml.TypeError](err); ok {
		msgs = te.Errors
	}
	var problems []Problem
	for _, msg := range msgs {
		pr := Problem{Line: 1, Message: "not YAML: " + msg}
		if m := yamlLine.FindStringSubmatch(msg); m != nil {
			pr.Line, _ = strconv.Atoi(m[1])
			pr.Message = "not YAML: " + msg[len(m[0]):]
		}
		problems = append(problems, pr)
	}
	return problems
}
