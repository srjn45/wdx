package plan

import (
	"bytes"
	"slices"

	"go.yaml.in/yaml/v3"
)

// Dropped is an after a plan's YAML gave that reduction took out: Entry waited for
// After, which Via, another entry Entry waits for, already waits for.
type Dropped struct {
	Entry, After, Via string
}

// Reduced is a plan's YAML checked and reduced: what to store, what it defines,
// and the afters taken out.
type Reduced struct {
	Content    []byte
	Definition Definition
	Dropped    []Dropped
}

// Reduce checks content as Parse does, then takes out every after that another
// path already covers (the transitive reduction): with C after B, B after A, and
// C after A, C's after A goes. With nothing dropped the content is kept byte for
// byte; otherwise it is written again from the YAML's node tree, which keeps
// comments and block text but may change indentation.
func Reduce(content []byte) (Reduced, []Problem) {
	p := parse(content)
	if len(p.problems) > 0 {
		return Reduced{}, p.problems
	}
	dropped := p.reduce()
	if len(dropped) == 0 {
		return Reduced{Content: content, Definition: p.def, Dropped: []Dropped{}}, nil
	}
	p.fill()
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&p.doc); err != nil {
		return Reduced{}, []Problem{{Line: 1, Message: "not written again once reduced: " + err.Error()}}
	}
	if err := enc.Close(); err != nil {
		return Reduced{}, []Problem{{Line: 1, Message: "not written again once reduced: " + err.Error()}}
	}
	return Reduced{Content: buf.Bytes(), Definition: p.def, Dropped: dropped}, nil
}

// reduce takes the redundant afters out of the entries and their nodes. An after
// is redundant when another after of the same entry already waits for it, directly
// or not. In a DAG these can all be dropped at once: what each entry waits for,
// directly or not, stays the same.
func (p *parsed) reduce() []Dropped {
	before := map[string]map[string]bool{} // every entry each waits for, directly or not
	for _, e := range p.ordered {
		all := map[string]bool{}
		for _, a := range e.after {
			all[a] = true
			for b := range before[a] {
				all[b] = true
			}
		}
		before[e.id] = all
	}
	var dropped []Dropped
	for _, e := range p.ordered {
		var keep []string
		var keepNodes []*yaml.Node
		for i, a := range e.after {
			via := slices.IndexFunc(e.after, func(b string) bool { return before[b][a] })
			if via >= 0 {
				dropped = append(dropped, Dropped{Entry: e.id, After: a, Via: e.after[via]})
				continue
			}
			keep, keepNodes = append(keep, a), append(keepNodes, e.afters[i])
		}
		if len(keep) < len(e.after) {
			e.after, e.afters = keep, keepNodes
			e.seq.Content = keepNodes
		}
	}
	return dropped
}
