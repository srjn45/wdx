package tide

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/style"
)

// The prompts are a tab of the agent pane in each project, of no agent, as the
// stats tab is: every prompt the project's Claude Code agents wait on, answered
// from one place (the wdx doc PROMPT_TAB). The list comes from the project's
// stream; Enter opens a prompt on a screen of its own, where tide keeps its own
// cursor, ticks and typed text, and Claude's cursor moves only once the answer is
// sent, by the agent's holder, which checks the prompt is still the one shown.
//
// A decision of several questions shows only its current question on Claude's
// screen, so tide cannot show the next before the one shown is answered: each
// question but the last is sent as it is answered, Claude moving on to the next
// and sending nothing to its model until its review. The last is kept in tide, and
// tide's review shows every answer; Submit answers sends the last, and the holder
// passes Claude's own review.
//
// No log line holds a question, a detail, a label or typed text: only kinds,
// indexes, counts.

const (
	promptsKey = "P" // of the menu, and of the left pane
	// promptsTab is the prompts' tab, which is of no agent.
	promptsTab = "prompts"
	// promptsMark is the tab's mark while a prompt waits.
	promptsMark = "?"
)

// The labels of tide's review of a decision of several questions.
const (
	reviewSubmit = "Submit answers"
	reviewBack   = "Back to the questions"
)

// promptsView is what the prompts tab holds in a project.
type promptsView struct {
	cursor string // the id of the agent under the list's cursor
	row    int    // its row, which the next agent takes when it goes
	open   string // the id of the agent whose prompt shows, "" for the list
	// drafts are what the person did in tide to each agent's prompt, by agent id,
	// kept until the prompt closes.
	drafts map[string]*promptDraft
	notice string // why the last answer was not sent, until the next key
}

// promptDraft is the person's answer to a prompt, as it stands in tide.
type promptDraft struct {
	seen    api.AgentPrompt // the prompt as the person sees it
	changed bool            // it was drawn again, as it changed under the person
	option  int             // tide's cursor
	ticks   map[int]bool    // a checkbox list's boxes, ticked in tide
	texts   map[int]string  // what was typed into each text option
	// kept are a decision's questions answered so far, by index: what tide's
	// review shows of them.
	kept map[int]keptAnswer
	// last is the last question's answer, kept in tide while its review shows.
	last         *api.PromptAnswers
	review       bool // tide's review shows
	reviewOption int  // its cursor
	sending      bool // an answer is on its way
	// next says the answer sent was a question of several, after which Claude shows
	// the next: the prompt changing then is no surprise.
	next bool
}

// keptAnswer is an answered question as tide's review shows it.
type keptAnswer struct{ question, answer string }

// promptAnswered is how sending an answer ended.
type promptAnswered struct {
	projectID, agentID string
	chat               bool // it declined the decision, to talk it over in the agent's tab
	next               bool // a question of several, after which Claude shows the next
	err                error
}

// promptsShown reports whether the agent pane shows the prompts.
func (m *Model) promptsShown() bool {
	l := m.list()
	return l != nil && l.active == promptsTab
}

// promptsOffered reports whether C-q P applies: a tab needs a project, and nothing
// moves when the prompts are what has the focus.
func (m *Model) promptsOffered() bool {
	return m.list() != nil && (m.layout.focus != agent || !m.promptsShown())
}

// openPrompts opens the prompts' tab, or goes to it, and focuses it.
func (m *Model) openPrompts() {
	m.list().openTab(promptsTab)
	m.layout.setFocus(agent)
	if m.layout.zoomed != none {
		m.layout.zoomed = agent
	}
	m.changed("prompts")
	m.log.Info("prompts opened", "count", len(m.promptAgents()))
}

// promptAgents are the project's agents waiting on a prompt read from their
// screen, in the agents' order. Only Claude Code's prompts are read.
func (m *Model) promptAgents() []api.Agent {
	var list []api.Agent
	for _, a := range m.agents() {
		if a.State == api.AgentStateRunning && a.Prompt != nil {
			list = append(list, a)
		}
	}
	return list
}

// promptsTitle is what the strip calls the tab: with how many prompts wait.
func (m *Model) promptsTitle() string {
	if n := len(m.promptAgents()); n > 0 {
		return promptsTab + " " + strconv.Itoa(n)
	}
	return promptsTab
}

// promptsChanged keeps the tab to the prompts the project's event brought: the
// list's cursor on its agent, or the row it was on; a draft whose prompt closed
// goes, and one whose prompt changed starts again from it, said changed unless
// it was the next question of an answer sent. Nothing changes under an answer on
// its way: its end looks again.
func (m *Model) promptsChanged() {
	l := m.list()
	if l == nil {
		return
	}
	v := &l.prompts
	agents := m.promptAgents()
	for id, d := range v.drafts {
		if d.sending {
			continue
		}
		i := agentIndex(agents, id)
		if i < 0 {
			delete(v.drafts, id)
			if v.open == id {
				v.open = ""
			}
			m.log.Info("prompt closed", "agent_id", id)
			continue
		}
		p := *agents[i].Prompt
		if samePrompt(d.seen, p) {
			d.seen = p
			continue
		}
		next := d.next && len(p.Questions) > 0 && slices.Equal(p.Questions, d.seen.Questions)
		kept := d.kept
		*d = *newDraft(p)
		if next {
			d.kept = kept
		} else {
			d.changed = true
		}
		m.log.Info("prompt changed", "agent_id", id, "next_question", next)
	}
	if v.open != "" && v.drafts[v.open] == nil {
		v.open = ""
	}
	switch i := agentIndex(agents, v.cursor); {
	case i >= 0:
		v.row = i
	case len(agents) == 0:
		v.row, v.cursor = 0, ""
	default:
		v.row = max(min(v.row, len(agents)-1), 0)
		v.cursor = agents[v.row].Id
	}
}

// samePrompt reports whether b asks what a asked, with the same options: where
// Claude's cursor is or which boxes it shows ticked are not the person's to
// answer, and do not change it.
func samePrompt(a, b api.AgentPrompt) bool {
	value := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	return a.Kind == b.Kind && value(a.Title) == value(b.Title) && a.Question == b.Question &&
		value(a.Detail) == value(b.Detail) && a.Multi == b.Multi && a.Review == b.Review &&
		slices.Equal(a.Options, b.Options) && slices.Equal(a.Kinds, b.Kinds) &&
		slices.Equal(a.Questions, b.Questions) && slices.Equal(a.Answered, b.Answered)
}

// newDraft is a draft of p as Claude shows it: the cursor on its option, its
// boxes as ticked.
func newDraft(p api.AgentPrompt) *promptDraft {
	d := &promptDraft{seen: p, ticks: map[int]bool{}, texts: map[int]string{}, kept: map[int]keptAnswer{}}
	if p.Selected != nil && *p.Selected >= 0 && *p.Selected < len(p.Options) {
		d.option = *p.Selected
	}
	for _, i := range p.Ticked {
		d.ticks[i] = true
	}
	return d
}

// kind is what option i is: choice when the host is too old to tell.
func (d *promptDraft) kind(i int) api.AgentPromptKinds {
	if i < len(d.seen.Kinds) && len(d.seen.Kinds) == len(d.seen.Options) {
		return d.seen.Kinds[i]
	}
	return api.AgentPromptKindsChoice
}

// typing reports whether keys go into the option under the cursor: a decision's
// text option takes words, a yes/no one is only chosen.
func (d *promptDraft) typing() bool {
	return !d.review && d.seen.Kind == api.AgentPromptKindDecision && d.kind(d.option) == api.AgentPromptKindsText
}

// lastQuestion reports whether the question shown is a decision's last of several
// to answer, which tide keeps for its review rather than send.
func (d *promptDraft) lastQuestion() bool {
	p := d.seen
	return len(p.Questions) > 1 && !p.Review && len(p.Questions)-len(p.Answered) <= 1
}

// question is the index in Questions of the question shown, -1 with one.
func (d *promptDraft) question() int {
	p := d.seen
	if len(p.Questions) < 2 || p.Title == nil {
		return -1
	}
	return slices.Index(p.Questions, *p.Title)
}

// numbered is the number Claude gives option i, 0 for its Submit row.
func (d *promptDraft) numbered(i int) int {
	n := 0
	for j := 0; j <= i; j++ {
		if d.kind(j) != api.AgentPromptKindsSubmit {
			n++
		}
	}
	if d.kind(i) == api.AgentPromptKindsSubmit {
		return 0
	}
	return n
}

// promptsKey takes a key, or a paste, given to the tab.
func (m *Model) promptsKey(msg tea.Msg) tea.Cmd {
	v := &m.list().prompts
	v.notice = ""
	if v.open == "" {
		if k, ok := msg.(tea.KeyPressMsg); ok {
			return m.promptsListKey(k.String())
		}
		return nil
	}
	d := v.drafts[v.open]
	if paste, ok := msg.(tea.PasteMsg); ok {
		if d.typing() && !d.sending {
			m.typeIntoPrompt(d, strings.Join(strings.Fields(paste.Content), " "))
		}
		return nil
	}
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	name := k.String()
	switch {
	case name == "esc":
		v.open = ""
		return nil
	case d.sending:
		return nil // nothing changes under an answer on its way, and Enter never goes twice
	case d.review:
		return m.promptReviewKey(d, name)
	}
	options := len(d.seen.Options)
	switch {
	case name == "up":
		d.option = max(d.option-1, 0)
	case name == "down":
		d.option = min(d.option+1, options-1)
	case name == "enter":
		return m.promptEnter(d)
	case d.typing() && name == "backspace":
		if r := []rune(d.texts[d.option]); len(r) > 0 {
			d.texts[d.option] = string(r[:len(r)-1])
			if d.seen.Multi && len(r) == 1 {
				delete(d.ticks, d.option)
			}
		}
	case d.typing() && typed(k):
		m.typeIntoPrompt(d, k.Text)
	case name == "space" && d.seen.Multi && d.kind(d.option) == api.AgentPromptKindsChoice:
		d.ticks[d.option] = !d.ticks[d.option]
	case len(name) == 1 && name >= "1" && name <= "9":
		for i := range options {
			if strconv.Itoa(d.numbered(i)) == name {
				d.option = i
			}
		}
	case name == openAndGoKey:
		m.openPromptAgent(v.open, "asked")
	}
	return nil
}

// typeIntoPrompt types text into the text option under the cursor; in a checkbox list
// it ticks it.
func (m *Model) typeIntoPrompt(d *promptDraft, text string) {
	if text == "" {
		return
	}
	d.texts[d.option] += text
	if d.seen.Multi {
		d.ticks[d.option] = true
	}
}

// promptsListKey moves through the list, opens a prompt, or its agent's tab.
func (m *Model) promptsListKey(name string) tea.Cmd {
	v := &m.list().prompts
	agents := m.promptAgents()
	if len(agents) == 0 {
		return nil
	}
	row := max(min(v.row, len(agents)-1), 0)
	switch name {
	case "up":
		row = max(row-1, 0)
	case "down":
		row = min(row+1, len(agents)-1)
	case "enter":
		a := agents[row]
		if v.drafts == nil {
			v.drafts = map[string]*promptDraft{}
		}
		if v.drafts[a.Id] == nil {
			v.drafts[a.Id] = newDraft(*a.Prompt)
		}
		v.open = a.Id
		m.log.Info("prompt opened", "agent_id", a.Id, "kind", a.Prompt.Kind, "options", len(a.Prompt.Options))
	case openAndGoKey:
		m.openPromptAgent(agents[row].Id, "asked")
	}
	v.row, v.cursor = row, agents[row].Id
	return nil
}

// promptEnter does what Enter does on the option under the cursor: answers it,
// ticks it, or keeps it for tide's review.
func (m *Model) promptEnter(d *promptDraft) tea.Cmd {
	v := &m.list().prompts
	p, i := d.seen, d.option
	if len(p.Kinds) != len(p.Options) {
		v.notice = "its options are not read: o answers it in the agent's tab"
		return nil
	}
	answer := func() api.PromptAnswers {
		choose := i
		return api.PromptAnswers{Question: &p.Question, Options: &p.Options, Choose: &choose}
	}
	switch kind := d.kind(i); {
	case kind == api.AgentPromptKindsChat:
		return m.sendAnswer(d, answer(), true)
	case p.Multi && kind == api.AgentPromptKindsSubmit:
		var tick []int
		for _, j := range slices.Sorted(maps.Keys(d.ticks)) {
			if d.ticks[j] {
				tick = append(tick, j)
			}
		}
		if len(tick) == 0 {
			v.notice = "nothing ticked: nothing sent"
			return nil
		}
		a := api.PromptAnswers{Question: &p.Question, Options: &p.Options, Tick: &tick}
		for _, j := range tick {
			if text := d.texts[j]; text != "" && d.kind(j) == api.AgentPromptKindsText {
				a.Text = &text
			}
		}
		return m.answerQuestion(d, a)
	case p.Multi && kind == api.AgentPromptKindsText && d.texts[i] == "":
		v.notice = "type something first"
	case p.Multi:
		d.ticks[i] = !d.ticks[i]
	case d.typing() && d.texts[i] == "":
		v.notice = "type an answer first"
	case d.typing():
		a, text := answer(), d.texts[i]
		a.Text = &text
		return m.answerQuestion(d, a)
	default:
		return m.answerQuestion(d, answer())
	}
	return nil
}

// answerQuestion sends a, or keeps it for tide's review when it is the last of
// several questions.
func (m *Model) answerQuestion(d *promptDraft, a api.PromptAnswers) tea.Cmd {
	if d.lastQuestion() {
		if q := d.question(); q >= 0 {
			d.kept[q] = keptAnswer{question: d.seen.Question, answer: d.answerText(a)}
		}
		d.last, d.review, d.reviewOption = &a, true, 0
		m.log.Info("prompt kept for review", "agent_id", m.list().prompts.open, "questions", len(d.seen.Questions))
		return nil
	}
	return m.sendAnswer(d, a, false)
}

// promptReviewKey moves through tide's review, sends the answers, or goes back to
// the question still open on Claude's screen with its answer kept.
func (m *Model) promptReviewKey(d *promptDraft, name string) tea.Cmd {
	switch name {
	case "up", "1":
		d.reviewOption = 0
	case "down", "2":
		d.reviewOption = 1
	case "enter":
		if d.reviewOption == 0 {
			return m.sendAnswer(d, *d.last, false)
		}
		d.review = false
		if d.last.Choose != nil {
			d.option = *d.last.Choose
		}
	}
	return nil
}

// sendAnswer sends a to the open prompt's agent. The draft waits for the answer;
// what is kept of it for a later review is its text as shown.
func (m *Model) sendAnswer(d *promptDraft, a api.PromptAnswers, chat bool) tea.Cmd {
	l := m.list()
	id, projectID := l.prompts.open, m.following.project.Id
	if q := d.question(); q >= 0 {
		d.kept[q] = keptAnswer{question: d.seen.Question, answer: d.answerText(a)}
	}
	next := !chat && len(d.seen.Questions) > 1 && !d.lastQuestion() && !d.seen.Review
	d.sending, d.next = true, false
	choose, ticked := -1, 0
	if a.Choose != nil {
		choose = *a.Choose
	}
	if a.Tick != nil {
		ticked = len(*a.Tick)
	}
	m.log.Info("prompt answer sent", "agent_id", id, "kind", d.seen.Kind, "option", choose, "ticked", ticked,
		"typed", a.Text != nil, "questions", len(d.seen.Questions), "chat", chat)
	answer := m.answerPrompt
	if answer == nil {
		answer = func(ctx context.Context, agentID string, a api.PromptAnswers) error {
			_, err := m.client.AnswerAgentPrompt(ctx, "id:"+agentID, "", "", a)
			return err
		}
	}
	ctx := m.ctx
	return func() tea.Msg {
		return promptAnswered{projectID: projectID, agentID: id, chat: chat, next: next, err: answer(ctx, id, a)}
	}
}

// answerText is a as tide's review shows it: the option's label, the text typed,
// or the boxes ticked.
func (d *promptDraft) answerText(a api.PromptAnswers) string {
	label := func(i int) string {
		if d.kind(i) == api.AgentPromptKindsText && d.texts[i] != "" {
			return d.texts[i]
		}
		return d.seen.Options[i]
	}
	if a.Tick != nil {
		var labels []string
		for _, i := range *a.Tick {
			labels = append(labels, label(i))
		}
		return strings.Join(labels, ", ")
	}
	if a.Choose != nil {
		return label(*a.Choose)
	}
	return ""
}

// promptWasAnswered ends a sending: a chat goes to the agent's tab, an answer
// refused says why on the tab, and the prompt is looked at again.
func (m *Model) promptWasAnswered(msg promptAnswered) {
	l, ok := m.agentLists[msg.projectID]
	if !ok {
		return
	}
	d := l.prompts.drafts[msg.agentID]
	if d == nil {
		return
	}
	d.sending = false
	if msg.err != nil {
		code := ""
		if e, ok := errors.AsType[*client.ProblemError](msg.err); ok {
			code = string(e.Problem.Code)
		}
		m.log.Info("prompt not answered", "agent_id", msg.agentID, "code", code)
		if l == m.list() {
			l.prompts.notice = refusal(msg.err, code)
		}
		if d.review {
			d.review = false // to change the answer or look at it again
		}
	} else {
		m.log.Info("prompt answered", "agent_id", msg.agentID, "chat", msg.chat, "next_question", msg.next)
		d.next = msg.next
		if !msg.next {
			delete(l.prompts.drafts, msg.agentID)
			if l.prompts.open == msg.agentID {
				l.prompts.open = ""
			}
		}
	}
	if l != m.list() {
		return
	}
	m.promptsChanged()
	if msg.chat && msg.err == nil {
		m.openPromptAgent(msg.agentID, "chat")
	}
}

// refusal says why an answer was not sent, from its error.
func refusal(err error, code string) string {
	switch code {
	case string(api.ErrorCodePromptChanged):
		return "not answered: the prompt changed before it was"
	case string(api.ErrorCodeAgentNotRunning):
		return "not answered: the agent is not running"
	}
	return "not answered: " + err.Error()
}

// openPromptAgent opens the agent's tab and goes to it.
func (m *Model) openPromptAgent(id, reason string) {
	if agentIndex(m.agents(), id) < 0 {
		return
	}
	m.list().openTab(id)
	m.layout.setFocus(agent)
	if m.layout.zoomed != none {
		m.layout.zoomed = agent
	}
	m.changed("prompt's agent")
	m.log.Info("agent active", "agent_id", id, "reason", "prompts tab, "+reason)
}

// promptsHint is what the status line says of the keys, in each state of the tab.
func (m *Model) promptsHint() string {
	v := m.list().prompts
	if v.open == "" {
		if len(m.promptAgents()) == 0 {
			return "Alt+← list  C-q menu"
		}
		return "↑↓ move  Enter options  o open agent  C-q menu"
	}
	d := v.drafts[v.open]
	switch k := d.kind(d.option); {
	case d.sending:
		return "answering…  Esc back"
	case d.review && d.reviewOption == 0:
		return "Enter submit all answers  ↑↓ move  Esc back"
	case d.review:
		return "Enter back to the question  ↑↓ move  Esc back"
	case k == api.AgentPromptKindsChat:
		return "Enter chat in the agent's tab  ↑↓ move  Esc back"
	case d.seen.Multi && k == api.AgentPromptKindsSubmit:
		return fmt.Sprintf("Enter send %d ticked  ↑↓ move  Esc back", d.tickedCount())
	case d.seen.Multi && d.typing():
		return "type to tick it  Enter tick  ↑↓ move  Esc back"
	case d.seen.Multi:
		return "Space or Enter tick  Submit sends  ↑↓ move  o open agent  Esc back"
	case d.typing():
		return "type your answer  Enter send  ↑↓ move  Esc back"
	case d.lastQuestion():
		return "Enter keep, then review  ↑↓ move  o open agent  Esc back"
	case len(d.seen.Questions) > 1:
		return "Enter answer, next question  ↑↓ move  o open agent  Esc back"
	}
	return "Enter choose  ↑↓ move  o open agent  Esc back"
}

func (d *promptDraft) tickedCount() int {
	n := 0
	for _, t := range d.ticks {
		if t {
			n++
		}
	}
	return n
}

func (m *Model) drawPrompts(buf uv.Screen, area uv.Rectangle, st style.Style) {
	v := m.list().prompts
	var lines []string
	cursor := 0
	if v.open == "" {
		lines, cursor = m.promptListLines(st)
	} else {
		lines, cursor = m.promptLines(st, area.Dx())
	}
	if v.notice != "" {
		lines = append([]string{st.Failure(v.notice)}, lines...)
		cursor++
	}
	// The cursor's row stays in sight.
	from := max(cursor-area.Dy()+1, 0)
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], area.Dx(), "…")
	}
	uv.NewStyledString(strings.Join(lines[from:], "\n")).Draw(buf, area)
}

// promptListLines are the list's rows, and the cursor's row.
func (m *Model) promptListLines(st style.Style) ([]string, int) {
	agents := m.promptAgents()
	if len(agents) == 0 {
		return []string{st.Muted("no prompts")}, 0
	}
	v := m.list().prompts
	names := 0
	for _, a := range agents {
		names = max(names, ansi.StringWidth(a.Name))
	}
	var lines []string
	for i, a := range agents {
		p := a.Prompt
		rest := "  " + st.Warning(pad(promptKindText(p.Kind), len("decision"))) + "  " + promptAbout(*p)
		if i != v.row {
			lines = append(lines, "  "+pad(a.Name, names)+rest)
			continue
		}
		mark := " "
		if m.layout.focus == agent {
			mark = st.Focused("▸")
		}
		lines = append(lines, mark+" "+st.CursorRow(st.Name(pad(a.Name, names))+rest))
	}
	return lines, v.row
}

// promptAbout is what a list's row says of p: a yes/no prompt's title and detail,
// or a decision's question.
func promptAbout(p api.AgentPrompt) string {
	if p.Kind == api.AgentPromptKindYesNo && p.Title != nil {
		if p.Detail != nil && *p.Detail != "" {
			return *p.Title + " · " + *p.Detail
		}
		return *p.Title + " · " + p.Question
	}
	return p.Question
}

func pad(s string, width int) string {
	return s + strings.Repeat(" ", max(width-ansi.StringWidth(s), 0))
}

// promptLines are the open prompt's screen, and the cursor's row: a header with
// the agent, the kind and which prompt of the list it is, then the prompt.
func (m *Model) promptLines(st style.Style, width int) ([]string, int) {
	v := m.list().prompts
	agents := m.promptAgents()
	d := v.drafts[v.open]
	p := d.seen
	name := v.open
	if i := agentIndex(m.agents(), v.open); i >= 0 {
		name = m.agents()[i].Name
	}
	kind := promptKindText(p.Kind)
	switch {
	case d.review, p.Review:
		kind += " (review)"
	case d.question() >= 0:
		kind += fmt.Sprintf(" (%d of %d)", d.question()+1, len(p.Questions))
	}
	dot := st.Muted("  ·  ")
	header := st.Muted("‹ "+promptsTab) + dot + st.Name(name) + dot + st.Muted(kind) + dot +
		st.Muted(fmt.Sprintf("%d of %d", max(agentIndex(agents, v.open)+1, 1), max(len(agents), 1)))
	switch {
	case d.sending:
		header += dot + st.Warning("answering…")
	case d.changed:
		header += dot + st.Warning("changed")
	}
	lines := []string{header, st.Muted(strings.Repeat("─", max(min(width, 60), 1)))}
	if len(p.Questions) > 1 {
		var tabs []string
		for i, q := range p.Questions {
			switch {
			case slices.Contains(p.Answered, i), d.review:
				tabs = append(tabs, st.Success("☒")+" "+q)
			case i == d.question():
				tabs = append(tabs, st.Name("☐ "+q))
			default:
				tabs = append(tabs, "☐ "+q)
			}
		}
		lines = append(lines, "    "+strings.Join(tabs, "  ")+"  "+st.Muted("✔ Submit"), "")
	}
	if d.review {
		return m.reviewLines(st, d, lines)
	}
	if p.Kind == api.AgentPromptKindYesNo && p.Title != nil {
		lines = append(lines, "    "+st.Heading(*p.Title))
	}
	if p.Detail != nil && *p.Detail != "" {
		lines = append(lines, "    "+st.Warning(*p.Detail))
	}
	lines = append(lines, "    "+p.Question, "")
	cursor := 0
	for i, label := range p.Options {
		here := i == d.option
		kind := d.kind(i)
		if kind == api.AgentPromptKindsChat {
			lines = append(lines, "      "+st.Muted("─────"))
		}
		var text string
		switch {
		case kind == api.AgentPromptKindsSubmit:
			text = st.Name("Submit") + st.Muted(fmt.Sprintf(" · %d ticked", d.tickedCount()))
		case kind == api.AgentPromptKindsText && p.Kind == api.AgentPromptKindDecision:
			typed := d.texts[i]
			switch {
			case here && typed != "":
				text = typed + st.Focused("▏")
			case here:
				text = st.Muted(label) + st.Focused("▏")
			case typed != "":
				text = typed
			default:
				text = st.Muted(label)
			}
		default:
			text = label
		}
		box := ""
		switch {
		case kind == api.AgentPromptKindsSubmit || kind == api.AgentPromptKindsChat:
		case p.Multi && d.ticks[i]:
			box = st.Success("[✔]") + " "
		case p.Multi:
			box = "[ ] "
		case p.Kind == api.AgentPromptKindDecision && !p.Review && here:
			box = st.Success("(•)") + " "
		case p.Kind == api.AgentPromptKindDecision && !p.Review:
			box = "( ) "
		}
		number := "   "
		if n := d.numbered(i); n > 0 {
			number = fmt.Sprintf("%d. ", n)
		}
		mark := "  "
		if here {
			mark, cursor = st.Focused("❯")+" ", len(lines)
		}
		line := "  " + mark + number + box + text
		if here {
			line = st.CursorRow(line)
		}
		lines = append(lines, line)
	}
	return lines, cursor
}

// reviewLines are tide's review of a decision of several questions after lines:
// each question answered and its answer, then Submit answers and Back to the
// questions.
func (m *Model) reviewLines(st style.Style, d *promptDraft, lines []string) ([]string, int) {
	lines = append(lines, "    "+st.Heading("Review your answers"), "")
	for i, q := range d.seen.Questions {
		a, ok := d.kept[i]
		switch {
		case ok:
			lines = append(lines, "    ● "+a.question, "      "+st.Success("→ "+a.answer))
		default:
			lines = append(lines, "    ● "+q, "      "+st.Muted("→ answered in the agent's tab"))
		}
	}
	lines = append(lines, "", "    Ready to submit your answers?", "")
	cursor := 0
	for i, label := range []string{reviewSubmit, reviewBack} {
		mark := "  "
		line := fmt.Sprintf("%d. %s", i+1, label)
		if i == d.reviewOption {
			mark, cursor = st.Focused("❯")+" ", len(lines)
			line = st.CursorRow(line)
		}
		lines = append(lines, "  "+mark+line)
	}
	return lines, cursor
}
