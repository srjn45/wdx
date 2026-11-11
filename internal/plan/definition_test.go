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
		{"unknown key", "goal: g\nowner: me\ntasks:\n  - id: a\n    aca: claude\n    model: opus\n    prompt: p\n", []string{`2: unknown key "owner"`}},
		{"no goal", "tasks:\n  - id: a\n    aca: claude\n    model: opus\n    prompt: p\n", []string{"1: no goal"}},
		{"blank goal", "goal: ''\ntasks:\n  - id: a\n    aca: claude\n    model: opus\n    prompt: p\n", []string{"1: the goal is blank"}},
		{"null goal", "goal:\ntasks:\n  - id: a\n    aca: claude\n    model: opus\n    prompt: p\n", []string{"1: the goal is blank"}},
		{"goal of spaces", "goal: '  '\ntasks:\n  - id: a\n    aca: claude\n    model: opus\n    prompt: p\n", []string{"1: the goal is blank"}},
		{"goal ~", "goal: ~\ntasks:\n  - id: a\n    aca: claude\n    model: opus\n    prompt: p\n", []string{"1: the goal is blank"}},
		{"goal a list", "goal: [a]\ntasks:\n  - id: a\n    aca: claude\n    model: opus\n    prompt: p\n", []string{"1: goal is not text"}},
		{"goal twice", "goal: a\ngoal: b\ntasks:\n  - id: a\n    aca: claude\n    model: opus\n    prompt: p\n", []string{"2: goal given twice, first at line 1"}},
		{"constraints not a list", "goal: g\nconstraints: x\ntasks:\n  - id: a\n    aca: claude\n    model: opus\n    prompt: p\n", []string{"2: constraints is not a list"}},
		{"a constraint not text", "goal: g\nconstraints:\n  - [x]\ntasks:\n  - id: a\n    aca: claude\n    model: opus\n    prompt: p\n", []string{"3: an item of constraints is not text"}},
		{"both", "goal: g\ntasks:\n  - id: a\n    aca: claude\n    model: opus\n    prompt: p\nplans:\n  - id: b\n", []string{"7: both tasks and plans"}},
		{"neither", "goal: g\n", []string{"1: no tasks or plans"}},
		{"no tasks", "goal: g\ntasks: []\n", []string{"2: tasks is not a list of at least one task"}},
		{"tasks not a list", "goal: g\ntasks: a\n", []string{"2: tasks is not a list"}},
		{"a task not a mapping", task("  - a"), []string{"3: a task is not a mapping"}},
		{"no id", task("  - aca: claude", "    model: opus", "    prompt: p"), []string{"3: a task with no id"}},
		{"blank id", task("  - id: ''", "    aca: claude", "    model: opus", "    prompt: p"), []string{"3: the id is blank"}},
		{"an _ in an id", task("  - id: a_b", "    aca: claude", "    model: opus", "    prompt: p"), []string{`3: id "a_b" can only have letters, numbers and -, at most 50`}},
		{"a long id", task("  - id: "+strings.Repeat("a", 51), "    aca: claude", "    model: opus", "    prompt: p"), []string{"at most 50"}},
		{"an id twice", task("  - id: a", "    aca: claude", "    model: opus", "    prompt: p", "  - id: A", "    aca: claude", "    model: opus", "    prompt: p"), []string{`7: id "A" is used twice, first at line 3`}},
		{"an unknown task key", task("  - id: a", "    aca: claude", "    model: opus", "    role: x", "    prompt: p"), []string{`6: unknown key "role": only id, aca, model, after, prompt`}},
		{"no aca", task("  - id: a", "    model: opus", "    prompt: p"), []string{"3: task a has no aca"}},
		{"blank aca", task("  - id: a", "    aca: ' '", "    model: opus", "    prompt: p"), []string{"4: the aca of task a is blank"}},
		{"aca not text", task("  - id: a", "    aca: [claude]", "    model: opus", "    prompt: p"), []string{"4: the aca is not text"}},
		{"no model", task("  - id: a", "    aca: claude", "    prompt: p"), []string{"3: task a has no model"}},
		{"blank model", task("  - id: a", "    aca: claude", "    model: ''", "    prompt: p"), []string{"5: the model of task a is blank"}},
		{"model not text", task("  - id: a", "    aca: claude", "    model: [opus]", "    prompt: p"), []string{"5: the model is not text"}},
		{"no prompt", task("  - id: a", "    aca: claude", "    model: opus"), []string{"3: task a has no prompt"}},
		{"blank prompt", task("  - id: a", "    aca: claude", "    model: opus", "    prompt: ' '"), []string{"6: the prompt of task a is blank"}},
		{"a prompt in a child", "goal: g\nplans:\n  - id: a\n    prompt: p\n", []string{`4: unknown key "prompt": only id, after`}},
		{"a long child", "goal: g\nplans:\n  - id: " + strings.Repeat("a", 96) + "\n", []string{"at most 95"}},
		{"after not a list", task("  - id: a", "    aca: claude", "    model: opus", "    prompt: p", "  - id: b", "    aca: claude", "    model: opus", "    after: a", "    prompt: p"), []string{"10: after is not a list of ids"}},
		{"after holding a list", task("  - id: a", "    aca: claude", "    model: opus", "    prompt: p", "  - id: b", "    aca: claude", "    model: opus", "    after: [[a]]", "    prompt: p"), []string{"10: an item of after is not an id"}},
		{"after naming nothing", task("  - id: a", "    aca: claude", "    model: opus", "    after: [b]", "    prompt: p"), []string{"6: a waits for b, which the plan does not have"}},
		{"after naming itself", task("  - id: a", "    aca: claude", "    model: opus", "    after: [A]", "    prompt: p"), []string{"6: a waits for itself"}},
		{"after naming one twice", task("  - id: a", "    aca: claude", "    model: opus", "    prompt: p", "  - id: b", "    aca: claude", "    model: opus", "    after: [a, A]", "    prompt: p"), []string{"10: b waits for a twice"}},
		{"a cycle", task(
			"  - id: a", "    aca: claude", "    model: opus", "    after: [c]", "    prompt: p",
			"  - id: b", "    aca: claude", "    model: opus", "    after: [a]", "    prompt: p",
			"  - id: c", "    aca: claude", "    model: opus", "    after: [b]", "    prompt: p",
			"  - id: d", "    aca: claude", "    model: opus", "    after: [c]", "    prompt: p",
		), []string{"3: a cycle: a waits for c waits for b waits for a"}},
		{"two cycles", task(
			"  - id: a", "    aca: claude", "    model: opus", "    after: [b]", "    prompt: p",
			"  - id: b", "    aca: claude", "    model: opus", "    after: [a]", "    prompt: p",
			"  - id: c", "    aca: claude", "    model: opus", "    after: [d]", "    prompt: p",
			"  - id: d", "    aca: claude", "    model: opus", "    after: [c]", "    prompt: p",
		), []string{"3: a cycle: a waits for b waits for a", "13: a cycle: c waits for d waits for c"}},
		{"pr not a flag", "goal: g\npr: yes please\ntasks:\n  - id: a\n    aca: claude\n    model: opus\n    prompt: p\n", []string{"2: pr is not true or false"}},
		{"pr a list", "goal: g\npr: [true]\ntasks:\n  - id: a\n    aca: claude\n    model: opus\n    prompt: p\n", []string{"2: pr is not true or false"}},
		{"stacked not a flag", "goal: g\npr: true\nstacked: 1\nplans:\n  - id: a\n", []string{"3: stacked is not true or false"}},
		{"stacked on tasks", "goal: g\npr: true\nstacked: true\ntasks:\n  - id: a\n    aca: claude\n    model: opus\n    prompt: p\n", []string{"3: stacked is for a plan of plans"}},
		{"stacked without pr", "goal: g\nstacked: true\nplans:\n  - id: a\n", []string{"2: stacked needs pr: true"}},
		{"stacked with pr false", "goal: g\npr: false\nstacked: true\nplans:\n  - id: a\n", []string{"2: stacked needs pr: true"}},
		{"every problem", "goal: ''\nextra: 1\ntasks:\n  - id: a_b\n  - aca: claude\n    model: opus\n    prompt: p\n", []string{
			"1: the goal is blank", `2: unknown key "extra"`, `4: id "a_b"`, "4: task a_b has no aca", "4: task a_b has no model", "4: task a_b has no prompt", "5: a task with no id",
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
    aca: claude
    model: sonnet
    after: [store, api]
    prompt: Document it.
  - id: api
    aca: codex
    model: gpt-5
    after: [SCHEMA]
    prompt: |
      Use sessions.
  - id: store
    aca: claude
    model: " opus "
    after: [schema]
    prompt: Write the store.
  - id: schema
    aca: " claude "
    model: opus
    prompt: Add the table.
`))
	if problems != nil {
		t.Fatal(problems)
	}
	want := Definition{
		Goal:        "Keep sessions in the database.",
		Constraints: []string{"The API does not change."},
		Tasks: []Task{
			{ID: "schema", ACA: "claude", Model: "opus", After: []string{}, Prompt: "Add the table.", Line: 23, acaLine: 24, modelLine: 25},
			{ID: "api", ACA: "codex", Model: "gpt-5", After: []string{"schema"}, Prompt: "Use sessions.\n", Line: 12, acaLine: 13, modelLine: 14},
			{ID: "store", ACA: "claude", Model: "opus", After: []string{"schema"}, Prompt: "Write the store.", Line: 18, acaLine: 19, modelLine: 20},
			{ID: "docs", ACA: "claude", Model: "sonnet", After: []string{"store", "api"}, Prompt: "Document it.", Line: 7, acaLine: 8, modelLine: 9},
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
		{"goal: g\ntasks:\n  - id: a\n    aca: claude\n    model: opus\n    prompt: p\n", false, false},
		{"goal: g\npr: true\ntasks:\n  - id: a\n    aca: claude\n    model: opus\n    prompt: p\n", true, false},
		{"goal: g\npr: True\nstacked: false\ntasks:\n  - id: a\n    aca: claude\n    model: opus\n    prompt: p\n", true, false},
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
