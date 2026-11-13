package screen

import (
	"encoding/json"
	"testing"
)

// TestPromptEqual: every field of a prompt tells two apart, the kinds, the
// questions, the answered and the review included, so that the holder sends a
// status when one alone changes.
func TestPromptEqual(t *testing.T) {
	t.Parallel()
	base := func() *Prompt {
		return &Prompt{Kind: PromptDecision, Title: "Size", Question: "Which size?", Options: []string{"Small", "Type something.", "Chat about this"},
			Kinds: []OptionKind{OptionChoice, OptionText, OptionChat}, Selected: 0, Ticked: []int{}, Questions: []string{"Color", "Size"}, Answered: []int{0}}
	}
	if !base().Equal(base()) {
		t.Fatal("two equal prompts read different")
	}
	changes := map[string]func(*Prompt){
		"kinds":     func(p *Prompt) { p.Kinds[1] = OptionChoice },
		"questions": func(p *Prompt) { p.Questions[1] = "Shape" },
		"answered":  func(p *Prompt) { p.Answered = []int{} },
		"review":    func(p *Prompt) { p.Review = true },
	}
	for name, change := range changes {
		p := base()
		change(p)
		if p.Equal(base()) {
			t.Errorf("a change of %s reads the same", name)
		}
	}
}

// TestPromptJSON: the new fields are in the JSON the holder sends and the
// daemon keeps, under these names.
func TestPromptJSON(t *testing.T) {
	t.Parallel()
	p := &Prompt{Kind: PromptDecision, Options: []string{"Submit answers", "Cancel"}, Kinds: []OptionKind{OptionChoice, OptionChoice},
		Ticked: []int{}, Questions: []string{"Color", "Size"}, Answered: []int{0, 1}, Review: true}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"kinds": `["choice","choice"]`, "questions": `["Color","Size"]`, "answered": `[0,1]`, "review": `true`}
	for key, value := range want {
		if string(fields[key]) != value {
			t.Errorf("%s = %s, want %s", key, fields[key], value)
		}
	}
	var back Prompt
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !back.Equal(p) {
		t.Errorf("read back as %+v, want %+v", back, *p)
	}
}
