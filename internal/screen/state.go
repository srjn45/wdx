package screen

import "slices"

// Activity is what an agent's ACA is doing right now, read from its screen.
type Activity string

const (
	ActivityWorking Activity = "working"
	ActivityIdle    Activity = "idle"
	ActivityWaiting Activity = "waiting"
	// ActivityUnknown is a screen the parser cannot read. It is never acted
	// on. With an input box it means the box's footer does not say, and
	// whoever reads the screen over time decides.
	ActivityUnknown Activity = "unknown"
)

// ScreenState is what a parser reads from one screen.
type ScreenState struct {
	Activity Activity
	// InputBox is true when the ACA's input box shows.
	InputBox bool
	// Draft is true when the box holds text a human put there, whatever the
	// activity, or a message would be submitted along with it. An open prompt
	// or a missing box has none. A box in a mode where a message would not be a
	// prompt has one, as Claude Code's bash mode: what it runs is the human's.
	Draft bool
	// DraftText is the box's text as shown, without its prompt mark and what the
	// ACA dims there itself, each line trimmed and the lines joined with \n; ""
	// when Draft is false. A long paste reads as the ACA renders it, as Claude
	// Code's "[Pasted text #1 +58 lines]", so it is compared, never parsed.
	DraftText string
	// InTurn is true when the ACA says it is in a turn its interrupt would stop,
	// not only running something in the background. A footer cut off before it
	// says so reads false, so a turn is never interrupted on a guess.
	InTurn bool
	// SteerPending is true when a message the ACA was told to take into its turn
	// has not been taken yet, and a key that sends another would cut the turn
	// short, as Cursor's Enter on a follow-up marked steering does.
	SteerPending bool
	// Model is the name of the AI model the ACA says it runs, when this screen
	// says so, as "Haiku 5.5"; "" when it does not, which is most screens, so
	// whoever reads the screen over time keeps the last.
	Model string
	// Prompt is what the ACA asks, read only with ActivityWaiting; nil when the
	// parser cannot read the prompt, so waiting alone means what it did.
	Prompt *Prompt
}

// Equal reports whether two states read the same, prompts included.
func (s ScreenState) Equal(o ScreenState) bool {
	a, b := s, o
	a.Prompt, b.Prompt = nil, nil
	return a == b && s.Prompt.Equal(o.Prompt)
}

// PromptKind is what answers a prompt.
type PromptKind string

const (
	// PromptYesNo is answered yes or no, in one of the ACA's variants: a
	// permission, a plan approval, the trust prompt.
	PromptYesNo PromptKind = "yes_no"
	// PromptDecision is a question the ACA's model asks, with options of its own.
	PromptDecision PromptKind = "decision"
)

// Prompt is what an ACA asks on its screen, when the parser can read it. Its
// JSON is what the holder tells the daemon and what the daemon keeps.
type Prompt struct {
	Kind PromptKind `json:"kind"`
	// Title is what the ACA heads it with: "Bash command", "Create file",
	// "Accessing workspace", a decision's tab "Color"; "" when it has none.
	Title string `json:"title"`
	// Question is the line that asks: "Do you want to proceed?", "Which color
	// do you choose?".
	Question string `json:"question"`
	// Detail is what a yes/no prompt is about: the command, the file name, the
	// workspace path; "" when there is none.
	Detail string `json:"detail"`
	// Options is every label, in order as shown, without its number, box or tick.
	Options []string `json:"options"`
	// Kinds is what each option is, one per option, in the same order.
	Kinds []OptionKind `json:"kinds"`
	// Selected is the index of the option the cursor is on; -1 when none shows.
	Selected int `json:"selected"`
	// Multi is a decision whose options are checkboxes.
	Multi bool `json:"multi"`
	// Ticked is the index of each option whose box is ticked, in order; empty,
	// never nil, for a multi-select with none and for every other prompt.
	Ticked []int `json:"ticked"`
	// Questions is the name of each question of a decision that asks several
	// at once, in order as its tabs show them; empty, never nil, for one
	// question and for every other prompt.
	Questions []string `json:"questions"`
	// Answered is the index in Questions of each question already answered, in
	// order; empty, never nil, when none is.
	Answered []int `json:"answered"`
	// Review is a decision's review screen, shown after its questions to send
	// the answers or go back to them.
	Review bool `json:"review"`
}

// OptionKind is what an option of a prompt is, and so how it is answered.
type OptionKind string

const (
	// OptionChoice is a yes/no answer or one of the model's own options:
	// chosen by Enter, or ticked by Space in a checkbox list.
	OptionChoice OptionKind = "choice"
	// OptionText takes words the person types before Enter.
	OptionText OptionKind = "text"
	// OptionChat declines the decision to talk it over with the agent.
	OptionChat OptionKind = "chat"
	// OptionSubmit sends a checkbox list's ticks.
	OptionSubmit OptionKind = "submit"
)

// Equal reports whether two prompts read the same; two nil prompts do.
func (p *Prompt) Equal(o *Prompt) bool {
	if p == nil || o == nil {
		return p == o
	}
	return p.Kind == o.Kind && p.Title == o.Title && p.Question == o.Question && p.Detail == o.Detail &&
		p.Selected == o.Selected && p.Multi == o.Multi && p.Review == o.Review && slices.Equal(p.Options, o.Options) &&
		slices.Equal(p.Kinds, o.Kinds) && slices.Equal(p.Ticked, o.Ticked) && slices.Equal(p.Questions, o.Questions) &&
		slices.Equal(p.Answered, o.Answered)
}
