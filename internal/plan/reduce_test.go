package plan

import (
	"reflect"
	"strings"
	"testing"
)

func TestReduce(t *testing.T) {
	t.Parallel()
	content := `goal: g
# the order matters
tasks:
  - id: a
    aca: claude
    model: opus
    prompt: p
  - id: b
    aca: claude
    model: opus
    after: [a]
    prompt: |
      Two lines
      of prompt.
  - id: c
    aca: claude
    model: opus
    after: [a, b] # a goes
    prompt: p
`
	r, problems := Reduce([]byte(content))
	if problems != nil {
		t.Fatal(problems)
	}
	if want := []Dropped{{Entry: "c", After: "a", Via: "b"}}; !reflect.DeepEqual(r.Dropped, want) {
		t.Errorf("dropped %+v, want %+v", r.Dropped, want)
	}
	if got := r.Definition.Tasks[2]; got.ID != "c" || !reflect.DeepEqual(got.After, []string{"b"}) {
		t.Errorf("the definition's c: %+v", got)
	}
	stored := string(r.Content)
	for _, kept := range []string{"# the order matters", "# a goes", "after: [b]", "Two lines\n      of prompt.\n"} {
		if !strings.Contains(stored, kept) {
			t.Errorf("stored without %q:\n%s", kept, stored)
		}
	}
	def, problems := Parse(r.Content)
	if problems != nil || !reflect.DeepEqual(def, r.Definition) {
		t.Errorf("the stored content defines %+v, %v; want %+v", def, problems, r.Definition)
	}
	again, _ := Reduce(r.Content)
	if len(again.Dropped) != 0 || string(again.Content) != stored {
		t.Errorf("reduced twice: %+v", again)
	}
}

func TestReduceNothing(t *testing.T) {
	t.Parallel()
	content := "goal:   g\ntasks:\n    - id: a\n      aca: claude\n      model: opus\n      prompt: p\n    - id: b\n      aca: claude\n      model: opus\n      after:\n        - a\n      prompt: p\n"
	r, problems := Reduce([]byte(content))
	if problems != nil {
		t.Fatal(problems)
	}
	if string(r.Content) != content || r.Dropped == nil || len(r.Dropped) != 0 {
		t.Errorf("nothing to drop changed it: %q, %+v", r.Content, r.Dropped)
	}
}

func TestReduceDeep(t *testing.T) {
	t.Parallel()
	r, problems := Reduce([]byte(`goal: g
plans:
  - id: a
  - id: b
    after: [a]
  - id: c
    after: [b]
  - id: x
  - id: d
    after: [a, x, c, b]
`))
	if problems != nil {
		t.Fatal(problems)
	}
	want := []Dropped{{Entry: "d", After: "a", Via: "c"}, {Entry: "d", After: "b", Via: "c"}}
	if !reflect.DeepEqual(r.Dropped, want) {
		t.Errorf("dropped %+v, want %+v", r.Dropped, want)
	}
	if got := r.Definition.Plans[4]; got.ID != "d" || !reflect.DeepEqual(got.After, []string{"x", "c"}) {
		t.Errorf("the definition's d: %+v", got)
	}
}

func TestReduceProblems(t *testing.T) {
	t.Parallel()
	if _, problems := Reduce([]byte("goal: g\n")); len(problems) != 1 {
		t.Errorf("problems: %v", problems)
	}
}
