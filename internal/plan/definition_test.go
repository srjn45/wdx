package plan

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestParseProblems(t *testing.T) {
	t.Parallel()
	task := func(lines ...string) string {
		return "goal: g\ntasks:\n" + strings.Join(lines, "\n") + "\n"
	}
	for _, c := range []struct {
		name    string
		content string
		want    []string // "line: part of the message", every problem in order
	}{
		{"not yaml", "goal: [a\n", []string{"1: not YAML: did not find"}},
		{"empty", "", []string{"1: the plan is empty"}},
		{"not a mapping", "- a\n", []string{"1: not a mapping"}},
		{"unknown key", "goal: g\nowner: me\ntasks:\n  - id: a\n    prompt: p\n", []string{`2: unknown key "owner"`}},
		{"no goal", "tasks:\n  - id: a\n    prompt: p\n", []string{"1: no goal"}},
		{"blank goal", "goal: ''\ntasks:\n  - id: a\n    prompt: p\n", []string{"1: the goal is blank"}},
		{"null goal", "goal:\ntasks:\n  - id: a\n    prompt: p\n", []string{"1: the goal is blank"}},
		{"goal of spaces", "goal: '  '\ntasks:\n  - id: a\n    prompt: p\n", []string{"1: the goal is blank"}},
		{"goal ~", "goal: ~\ntasks:\n  - id: a\n    prompt: p\n", []string{"1: the goal is blank"}},
		{"goal a list", "goal: [a]\ntasks:\n  - id: a\n    prompt: p\n", []string{"1: goal is not text"}},
		{"goal twice", "goal: a\ngoal: b\ntasks:\n  - id: a\n    prompt: p\n", []string{"2: goal given twice, first at line 1"}},
		{"constraints not a list", "goal: g\nconstraints: x\ntasks:\n  - id: a\n    prompt: p\n", []string{"2: constraints is not a list"}},
		{"a constraint not text", "goal: g\nconstraints:\n  - [x]\ntasks:\n  - id: a\n    prompt: p\n", []string{"3: an item of constraints is not text"}},
		{"both", "goal: g\ntasks:\n  - id: a\n    prompt: p\nplans:\n  - id: b\n", []string{"5: both tasks and plans"}},
		{"neither", "goal: g\n", []string{"1: no tasks or plans"}},
		{"no tasks", "goal: g\ntasks: []\n", []string{"2: tasks is not a list of at least one task"}},
		{"tasks not a list", "goal: g\ntasks: a\n", []string{"2: tasks is not a list"}},
		{"a task not a mapping", task("  - a"), []string{"3: a task is not a mapping"}},
		{"no id", task("  - prompt: p"), []string{"3: a task with no id"}},
		{"blank id", task("  - id: ''", "    prompt: p"), []string{"3: the id is blank"}},
		{"an _ in an id", task("  - id: a_b", "    prompt: p"), []string{`3: id "a_b" can only have letters, numbers and -, at most 50`}},
		{"a long id", task("  - id: "+strings.Repeat("a", 51), "    prompt: p"), []string{"at most 50"}},
		{"an id twice", task("  - id: a", "    prompt: p", "  - id: A", "    prompt: p"), []string{`5: id "A" is used twice, first at line 3`}},
		{"an unknown task key", task("  - id: a", "    role: x", "    prompt: p"), []string{`4: unknown key "role": only id, after, prompt`}},
		{"no prompt", task("  - id: a"), []string{"3: task a has no prompt"}},
		{"blank prompt", task("  - id: a", "    prompt: ' '"), []string{"4: the prompt of task a is blank"}},
		{"a prompt in a child", "goal: g\nplans:\n  - id: a\n    prompt: p\n", []string{`4: unknown key "prompt": only id, after`}},
		{"a long child", "goal: g\nplans:\n  - id: " + strings.Repeat("a", 96) + "\n", []string{"at most 95"}},
		{"after not a list", task("  - id: a", "    prompt: p", "  - id: b", "    after: a", "    prompt: p"), []string{"6: after is not a list of ids"}},
		{"after holding a list", task("  - id: a", "    prompt: p", "  - id: b", "    after: [[a]]", "    prompt: p"), []string{"6: an item of after is not an id"}},
		{"after naming nothing", task("  - id: a", "    after: [b]", "    prompt: p"), []string{"4: a waits for b, which the plan does not have"}},
		{"after naming itself", task("  - id: a", "    after: [A]", "    prompt: p"), []string{"4: a waits for itself"}},
		{"after naming one twice", task("  - id: a", "    prompt: p", "  - id: b", "    after: [a, A]", "    prompt: p"), []string{"6: b waits for a twice"}},
		{"a cycle", task(
			"  - id: a", "    after: [c]", "    prompt: p",
			"  - id: b", "    after: [a]", "    prompt: p",
			"  - id: c", "    after: [b]", "    prompt: p",
			"  - id: d", "    after: [c]", "    prompt: p",
		), []string{"3: a cycle: a waits for c waits for b waits for a"}},
		{"two cycles", task(
			"  - id: a", "    after: [b]", "    prompt: p",
			"  - id: b", "    after: [a]", "    prompt: p",
			"  - id: c", "    after: [d]", "    prompt: p",
			"  - id: d", "    after: [c]", "    prompt: p",
		), []string{"3: a cycle: a waits for b waits for a", "9: a cycle: c waits for d waits for c"}},
		{"pr not a flag", "goal: g\npr: yes please\ntasks:\n  - id: a\n    prompt: p\n", []string{"2: pr is not true or false"}},
		{"pr a list", "goal: g\npr: [true]\ntasks:\n  - id: a\n    prompt: p\n", []string{"2: pr is not true or false"}},
		{"stacked not a flag", "goal: g\npr: true\nstacked: 1\nplans:\n  - id: a\n", []string{"3: stacked is not true or false"}},
		{"stacked on tasks", "goal: g\npr: true\nstacked: true\ntasks:\n  - id: a\n    prompt: p\n", []string{"3: stacked is for a plan of plans"}},
		{"stacked without pr", "goal: g\nstacked: true\nplans:\n  - id: a\n", []string{"2: stacked needs pr: true"}},
		{"stacked with pr false", "goal: g\npr: false\nstacked: true\nplans:\n  - id: a\n", []string{"2: stacked needs pr: true"}},
		{"every problem", "goal: ''\nextra: 1\ntasks:\n  - id: a_b\n  - prompt: p\n", []string{
			"1: the goal is blank", `2: unknown key "extra"`, `4: id "a_b"`, "4: task a_b has no prompt", "5: a task with no id",
		}},
	} {
		_, problems := Parse([]byte(c.content))
		var got []string
		for _, p := range problems {
			got = append(got, fmt.Sprintf("%d: %s", p.Line, p.Message))
		}
		ok := len(got) == len(c.want)
		for i := 0; ok && i < len(got); i++ {
			ok = strings.Contains(got[i], c.want[i])
		}
		if !ok {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestParse(t *testing.T) {
	t.Parallel()
	def, problems := Parse([]byte(`goal: >-
  Keep sessions
  in the database.
constraints:
  - The API does not change.
tasks:
  - id: docs
    after: [store, api]
    prompt: Document it.
  - id: api
    after: [SCHEMA]
    prompt: |
      Use sessions.
  - id: store
    after: [schema]
    prompt: Write the store.
  - id: schema
    prompt: Add the table.
`))
	if problems != nil {
		t.Fatal(problems)
	}
	want := Definition{
		Goal:        "Keep sessions in the database.",
		Constraints: []string{"The API does not change."},
		Tasks: []Task{
			{ID: "schema", After: []string{}, Prompt: "Add the table.", Line: 17},
			{ID: "api", After: []string{"schema"}, Prompt: "Use sessions.\n", Line: 10},
			{ID: "store", After: []string{"schema"}, Prompt: "Write the store.", Line: 14},
			{ID: "docs", After: []string{"store", "api"}, Prompt: "Document it.", Line: 7},
		},
	}
	if !reflect.DeepEqual(def, want) {
		t.Errorf("got %+v\nwant %+v", def, want)
	}
}

func TestParsePlans(t *testing.T) {
	t.Parallel()
	def, problems := Parse([]byte("goal: Rewrite authentication.\nconstraints:\nplans:\n  - id: auth-tokens\n    after: [auth-sessions]\n  - id: auth-sessions\n"))
	if problems != nil {
		t.Fatal(problems)
	}
	want := Definition{
		Goal: "Rewrite authentication.",
		Plans: []Child{
			{ID: "auth-sessions", After: []string{}, Line: 6},
			{ID: "auth-tokens", After: []string{"auth-sessions"}, Line: 4},
		},
	}
	if !reflect.DeepEqual(def, want) {
		t.Errorf("got %+v\nwant %+v", def, want)
	}
}

func TestParseFlags(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		content     string
		pr, stacked bool
	}{
		{"goal: g\ntasks:\n  - id: a\n    prompt: p\n", false, false},
		{"goal: g\npr: true\ntasks:\n  - id: a\n    prompt: p\n", true, false},
		{"goal: g\npr: True\nstacked: false\ntasks:\n  - id: a\n    prompt: p\n", true, false},
		{"goal: g\npr: false\nplans:\n  - id: a\n", false, false},
		{"goal: g\npr: true\nplans:\n  - id: a\n", true, false},
		{"goal: g\nstacked: true\npr: true\nplans:\n  - id: a\n", true, true},
	} {
		def, problems := Parse([]byte(c.content))
		if problems != nil || def.PR != c.pr || def.Stacked != c.stacked {
			t.Errorf("%q: pr %v, stacked %v, %v; want pr %v, stacked %v", c.content, def.PR, def.Stacked, problems, c.pr, c.stacked)
		}
	}
}
