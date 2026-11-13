package cli

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

// promptNextWait is how long, after an answer that moves a decision on to its
// next question, the command waits for the daemon to know that question.
const promptNextWait = 5 * time.Second

// promptRowWidth is the width the text of a prompt's row in the list is cut to.
const promptRowWidth = 60

// The questions the command logs the answers to: never the prompt's own.
const (
	promptWhichQuestion   = "which prompt to answer"
	promptOptionQuestion  = "which option to choose"
	promptTicksQuestion   = "which options to tick"
	promptConfirmQuestion = "send the answer"
	promptReviewQuestion  = "submit the answers"
)

func newPromptCmd() *cobra.Command {
	var scope projectScope
	cmd := &cobra.Command{
		Use:   "prompt [--project-id <id> | --project-name <name>]",
		Short: "Answer the prompts the project's Claude Code agents wait on",
		Long: "Lists the agents of the project with a prompt open, in the agents' order, asks\n" +
			"which one, shows its question, detail and options, and sends the answer through\n" +
			"the agent's holder, which presses the keys one at a time and only while the\n" +
			"screen still shows the prompt you saw.\n\n" +
			"A number picks a choice. A checkbox list takes the numbers to tick, as 1 3,\n" +
			"and asks before sending. A text option asks for the text. Chat about this\n" +
			"declines the prompt, and the agent goes on in its own tab or wdx agent attach.\n" +
			"A decision that asks several questions asks each in turn: Claude Code moves on\n" +
			"to the next as each is answered, nothing is submitted, and after the last the\n" +
			"answers are shown for review and sent only on an explicit yes. A prompt that\n" +
			"changed meanwhile is said, and the list read again.\n\n" +
			"Only Claude Code's prompts are read, so only its agents are listed. --json\n" +
			"lists them and asks nothing; with --no-input or no terminal the command needs\n" +
			"--json. Without a project flag, the project the working directory is in.",
		Example: clikit.Examples(
			"Answer a prompt of the project you are in", identity.Name+" prompt",
			"Those of another project", identity.Name+" prompt --project-name app",
			"List them as JSON, asking nothing", identity.Name+" prompt --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not answered",
			clikit.OutputNote: "What the dialogue asks goes to stderr; stdout holds the answered agent, or no prompts when none waits.\n" +
				`With --json: {"agents": [...]}, the agents with a prompt, each with its "prompt" as the API serves it, children not nested`,
			clikit.Results: "answered",
			clikit.Errors: clikit.CodeList(api.ErrorCodeAcaUnsupported, api.ErrorCodeAgentNotRunning, api.ErrorCodeAgentRemoved,
				api.ErrorCodeAnswerFailed, api.ErrorCodeCancelled, api.ErrorCodeNotFound, api.ErrorCodePromptChanged, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			var dir string
			if !scope.given() {
				var err error
				if dir, err = workDir(cmd); err != nil {
					return err
				}
			}
			d := &promptDialogue{cmd: cmd, c: clientOf(cmd), scope: scope, dir: dir}
			if clikit.JSONMode(cmd) {
				agents, err := d.agents()
				if err != nil {
					return err
				}
				return clikit.PrintResult(cmd, promptList{AgentList: api.AgentList{Agents: agents}})
			}
			if !clikit.CanPrompt(cmd) {
				clikit.LogUnanswered(cmd, promptWhichQuestion)
				return &clikit.Hinted{
					Err:  &clikit.UsageError{Err: fmt.Errorf("%s asks which prompt to answer: %s", identity.Name, clikit.NoPromptReason(cmd))},
					Hint: "use " + identity.Name + " prompt --json to list the prompts without asking",
				}
			}
			d.answers = bufio.NewScanner(cmd.InOrStdin())
			d.out, d.st = cmd.ErrOrStderr(), clikit.StderrStyle(cmd)
			return d.run()
		},
	}
	scope.addFlags(cmd)
	return cmd
}

// promptList is the agents with a prompt open; the text says when there are none.
type promptList struct{ api.AgentList }

func (l promptList) Text(w io.Writer, st style.Style) {
	fmt.Fprintln(w, "no prompts")
}

// LogValue keeps the prompts' text out of the log.
func (l promptList) LogValue() slog.Value { return loggedAgents(l.AgentList) }

// promptAnswered is an agent whose prompt was answered; chat says it was
// declined to talk it over.
type promptAnswered struct {
	agentResult
	chat bool
}

func (r promptAnswered) Text(w io.Writer, st style.Style) {
	r.agentResult.Text(w, st)
	if r.chat {
		fmt.Fprintf(w, "%s %s\n", st.Hint("go on with it in the agent's tab in TIDE, or with"), st.Command(identity.Name+" agent attach --id "+r.Agent.Id))
	}
}

// promptDialogue is one run of the command: what it asks of the person, and what
// it asks of the daemon.
type promptDialogue struct {
	cmd     *cobra.Command
	c       *client.Client
	scope   projectScope
	dir     string
	answers *bufio.Scanner
	out     io.Writer
	st      style.Style
}

// agents are the project's agents with a Claude Code prompt open, flat, in the
// agents' order.
func (d *promptDialogue) agents() ([]api.Agent, error) {
	list, err := d.c.ListAgents(d.cmd.Context(), d.scope.ref(), d.dir)
	if err != nil {
		return nil, outsideHint(err, d.scope, "")
	}
	var agents []api.Agent
	for _, r := range agentTreeRows(list.Agents) {
		if a := r.agent; a.Prompt != nil && a.Aca == (aca.ClaudeCode{}).ID() && a.State == api.AgentStateRunning {
			a.Children = nil
			agents = append(agents, a)
		}
	}
	return agents, nil
}

// run lists the prompts and answers one, again from the list when it changed.
func (d *promptDialogue) run() error {
	for {
		agents, err := d.agents()
		if err != nil {
			return err
		}
		if len(agents) == 0 {
			return clikit.PrintResult(d.cmd, promptList{})
		}
		a, err := d.pick(agents)
		if err != nil {
			return err
		}
		result, chat, err := d.answerPrompt(a)
		if problemCode(err) == api.ErrorCodePromptChanged {
			fmt.Fprintln(d.out, d.st.Warning("the prompt changed, so nothing more was pressed: reading the list again"))
			clikit.LogAnswer(d.cmd, promptConfirmQuestion, "prompt_changed", "prompt")
			d.settle(a)
			continue
		}
		if err != nil {
			return agentHint(err)
		}
		return clikit.PrintResult(d.cmd, promptAnswered{agentResult(result), chat})
	}
}

// pick lists the agents' prompts and asks which to answer.
func (d *promptDialogue) pick(agents []api.Agent) (api.Agent, error) {
	noun := "prompts"
	if len(agents) == 1 {
		noun = "prompt"
	}
	fmt.Fprintf(d.out, "%d %s in %s\n", len(agents), noun, d.st.Name(agents[0].ProjectName))
	width := 0
	for _, a := range agents {
		width = max(width, utf8.RuneCountInString(a.Name))
	}
	for i, a := range agents {
		pad := strings.Repeat(" ", width-utf8.RuneCountInString(a.Name))
		kind := fmt.Sprintf("%-8s", promptKindName(*a.Prompt))
		fmt.Fprintf(d.out, "  %s  %s%s  %s  %s\n", d.st.Command(strconv.Itoa(i+1)), d.st.Name(a.Name), pad, d.st.Warning(kind), cutRunes(promptRowText(*a.Prompt), promptRowWidth))
	}
	choice, err := d.choose("Which one? "+d.st.Command("(1-"+strconv.Itoa(len(agents))+", q to quit)")+": ", len(agents), promptWhichQuestion)
	if err != nil {
		return api.Agent{}, err
	}
	return agents[choice], nil
}

// promptKindName is a prompt's kind as the list shows it.
func promptKindName(p api.AgentPrompt) string {
	switch {
	case p.Review:
		return "review"
	case p.Kind == api.AgentPromptKindYesNo:
		return "yes/no"
	}
	return string(p.Kind)
}

// promptRowText is what a prompt is about: a yes/no prompt's title and detail,
// a decision's question.
func promptRowText(p api.AgentPrompt) string {
	if p.Kind == api.AgentPromptKindDecision {
		return p.Question
	}
	var parts []string
	if p.Title != nil {
		parts = append(parts, *p.Title)
	}
	if p.Detail != nil {
		parts = append(parts, *p.Detail)
	}
	if len(parts) == 0 {
		return p.Question
	}
	return strings.Join(parts, " · ")
}

// cutRunes cuts text, on one line, to width runes, with … where it was cut.
func cutRunes(text string, width int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= width {
		return text
	}
	return string([]rune(text)[:width-1]) + "…"
}

// choose asks for a number from 1 to n, or q, and returns it less one. question
// is what the log calls the choice, never what the prompt said.
func (d *promptDialogue) choose(ask string, n int, question string) (int, error) {
	choices := []string{"q"}
	for i := 1; i <= n; i++ {
		choices = append(choices, strconv.Itoa(i))
	}
	answer, err := clikit.Ask(d.out, d.answers, ask, choices...)
	if err != nil {
		return 0, err
	}
	if answer == "q" {
		clikit.LogAnswer(d.cmd, question, "quit", "prompt")
		return 0, d.quit()
	}
	i, _ := strconv.Atoi(answer)
	clikit.LogAnswer(d.cmd, question, "option "+answer, "prompt")
	return i - 1, nil
}

func (d *promptDialogue) quit() error {
	return &client.ProblemError{Problem: api.NewProblem(api.ErrorCodeCancelled, "you quit: nothing more was answered")}
}

// confirm asks whether to go on; anything but y is a no.
func (d *promptDialogue) confirm(ask, question string) error {
	answer, err := clikit.Ask(d.out, d.answers, ask+" "+d.st.Command("[y/N]")+": ", "y", "n", "")
	if err != nil {
		return err
	}
	if answer != "y" {
		clikit.LogAnswer(d.cmd, question, "no", "prompt")
		return &client.ProblemError{Problem: api.NewProblem(api.ErrorCodeCancelled, "you answered no: nothing more was answered")}
	}
	clikit.LogAnswer(d.cmd, question, "yes", "prompt")
	return nil
}

// reviewed is a question of a decision answered in this run, for its review.
type reviewed struct {
	question string
	answer   string
}

// answerPrompt shows a's prompt and sends the answer, question after question
// for a decision that asks several. It reports whether the answer was chat.
func (d *promptDialogue) answerPrompt(a api.Agent) (api.AgentResult, bool, error) {
	p := *a.Prompt
	var done []reviewed
	for {
		d.show(a, p)
		ans, summary, chat, err := d.ask(p)
		if err != nil {
			return api.AgentResult{}, false, err
		}
		several := len(p.Questions) > 1 && !p.Review
		last := !several || len(p.Questions)-len(p.Answered) <= 1
		switch {
		case p.Review:
			if err := d.confirm("Submit these answers?", promptReviewQuestion); err != nil {
				return api.AgentResult{}, false, err
			}
		case chat:
		case several && last:
			done = append(done, reviewed{p.Question, summary})
			d.review(done)
			if err := d.confirm("Submit these answers?", promptReviewQuestion); err != nil {
				return api.AgentResult{}, false, d.notSubmitted(err, len(done) > 1)
			}
		case several:
			done = append(done, reviewed{p.Question, summary})
		case p.Multi:
			if err := d.confirm("Send "+summary+"?", promptConfirmQuestion); err != nil {
				return api.AgentResult{}, false, err
			}
		}
		result, err := d.send(a, ans)
		if err != nil {
			return api.AgentResult{}, false, err
		}
		if chat || last {
			return result, chat, nil
		}
		if p, err = d.nextQuestion(a, result, p); err != nil {
			return api.AgentResult{}, false, err
		}
	}
}

// notSubmitted says, after the review was not confirmed, that Claude Code still
// holds the questions answered before the last.
func (d *promptDialogue) notSubmitted(err error, earlier bool) error {
	if earlier {
		fmt.Fprintln(d.out, d.st.Muted("nothing was submitted: the agent still shows its last question, the others answered"))
	}
	return err
}

// send gives the daemon the answer to the prompt of a.
func (d *promptDialogue) send(a api.Agent, answer api.PromptAnswer) (api.AgentResult, error) {
	return d.c.AnswerAgentPrompt(d.cmd.Context(), "id:"+a.Id, "", "", api.PromptAnswers{
		Question: &answer.Question, Options: &answer.Options, Choose: answer.Choose, Tick: answer.Tick, Text: answer.Text,
	})
}

// differs reports whether p is not the prompt before: gone, another question or
// options, more answered, or the review.
func differs(p *api.AgentPrompt, before api.AgentPrompt) bool {
	return p == nil || p.Review || len(p.Answered) > len(before.Answered) || p.Question != before.Question || !slices.Equal(p.Options, before.Options)
}

// waitPrompt reads the agent until its prompt differs from before, or the time
// is up, and returns the prompt then: nil when it is gone, an error when the
// daemon did not answer or the prompt is the same.
func (d *promptDialogue) waitPrompt(a api.Agent, before api.AgentPrompt) (*api.AgentPrompt, error) {
	ctx := d.cmd.Context()
	expired := time.After(cmp.Or(envOf(d.cmd).Timeouts.PromptNext, promptNextWait))
	for {
		got, err := d.c.GetAgent(ctx, "id:"+a.Id, "", "")
		if err != nil {
			return nil, err
		}
		if differs(got.Agent.Prompt, before) {
			return got.Agent.Prompt, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-expired:
			return nil, errPromptSame
		case <-time.After(50 * time.Millisecond):
		}
	}
}

var errPromptSame = &client.ProblemError{Problem: api.NewProblem(api.ErrorCodePromptChanged, "the agent did not show its next question")}

// settle waits, as long as it may, until the daemon shows a prompt other than
// the one a was answered on, so that the list read again is not the old one.
func (d *promptDialogue) settle(a api.Agent) {
	_, _ = d.waitPrompt(a, *a.Prompt)
}

// nextQuestion is the prompt of the agent once it moved on from before: from
// the answer's result, or from the agent while the daemon learns it.
func (d *promptDialogue) nextQuestion(a api.Agent, result api.AgentResult, before api.AgentPrompt) (api.AgentPrompt, error) {
	if p := result.Agent.Prompt; p != nil && differs(p, before) {
		return *p, nil
	}
	p, err := d.waitPrompt(a, before)
	switch {
	case err != nil:
		return api.AgentPrompt{}, err
	case p == nil:
		return api.AgentPrompt{}, &client.ProblemError{Problem: api.NewProblem(api.ErrorCodePromptChanged, "the prompt closed before its next question")}
	}
	return *p, nil
}

// show prints the prompt of a: what it is about and each option, numbered as
// the person picks them, the Submit row left out, with its kind.
func (d *promptDialogue) show(a api.Agent, p api.AgentPrompt) {
	st := d.st
	head := st.Name(a.Name) + " · " + st.Warning(promptKindName(p))
	if p.Title != nil {
		head += " · " + *p.Title
	}
	if len(p.Questions) > 1 && !p.Review {
		position := min(len(p.Answered)+1, len(p.Questions))
		head += fmt.Sprintf(" · question %d of %d", position, len(p.Questions))
		if position <= len(p.Questions) {
			head += ": " + p.Questions[position-1]
		}
	}
	fmt.Fprintf(d.out, "\n%s\n%s\n", head, p.Question)
	if p.Detail != nil && *p.Detail != "" {
		fmt.Fprintf(d.out, "%s\n", st.Muted(*p.Detail))
	}
	for n, i := range promptRows(p) {
		box := ""
		if p.Multi {
			box = "[ ] "
			if slices.Contains(p.Ticked, i) {
				box = "[✔] "
			}
		}
		note := ""
		switch promptKindOf(p, i) {
		case api.AgentPromptKindsText:
			note = st.Muted("  (type your own)")
		case api.AgentPromptKindsChat:
			note = st.Muted("  (declines; go on in the agent's tab)")
		}
		fmt.Fprintf(d.out, "  %s  %s%s%s\n", st.Command(strconv.Itoa(n+1)), box, p.Options[i], note)
	}
}

// review prints the answers of a decision as Claude Code reviews them.
func (d *promptDialogue) review(done []reviewed) {
	fmt.Fprintf(d.out, "\n%s\n", d.st.Name("Review your answers"))
	for _, r := range done {
		fmt.Fprintf(d.out, "  %s\n    → %s\n", r.question, r.answer)
	}
}

// promptRows are the indexes of the options a person can pick, in order: all
// but a checkbox list's Submit row, which sending the ticks takes the place of.
func promptRows(p api.AgentPrompt) []int {
	var rows []int
	for i := range p.Options {
		if promptKindOf(p, i) != api.AgentPromptKindsSubmit {
			rows = append(rows, i)
		}
	}
	return rows
}

// promptKindOf is the kind of option i; a choice from a host too old to tell.
func promptKindOf(p api.AgentPrompt, i int) api.AgentPromptKinds {
	if i < len(p.Kinds) {
		return p.Kinds[i]
	}
	return api.AgentPromptKindsChoice
}

// ask asks what to answer p and builds the answer against the question and
// options as shown. summary says it for the review; chat that it declines.
func (d *promptDialogue) ask(p api.AgentPrompt) (answer api.PromptAnswer, summary string, chat bool, err error) {
	answer = api.PromptAnswer{Question: p.Question, Options: p.Options}
	rows := promptRows(p)
	if p.Review {
		// Enter on Claude's first option, Submit answers.
		answer.Choose = ptr(0)
		return answer, "", false, nil
	}
	if p.Multi {
		return d.askTicks(p, answer, rows)
	}
	n, err := d.choose("Which option? "+d.st.Command("(1-"+strconv.Itoa(len(rows))+", q to quit)")+": ", len(rows), promptOptionQuestion)
	if err != nil {
		return answer, "", false, err
	}
	i := rows[n]
	answer.Choose = ptr(i)
	switch kind := promptKindOf(p, i); {
	case kind == api.AgentPromptKindsChat:
		return answer, p.Options[i], true, nil
	case kind == api.AgentPromptKindsText && p.Kind == api.AgentPromptKindDecision:
		text, err := d.askText()
		if err != nil {
			return answer, "", false, err
		}
		answer.Text = &text
		return answer, text, false, nil
	}
	return answer, p.Options[i], false, nil
}

// askText asks for the text of a text option; an empty one quits.
func (d *promptDialogue) askText() (string, error) {
	text, err := clikit.Ask(d.out, d.answers, "Text "+d.st.Command("(empty to quit)")+": ")
	if err != nil {
		return "", err
	}
	if text == "" {
		clikit.LogAnswer(d.cmd, promptOptionQuestion, "no text", "prompt")
		return "", d.quit()
	}
	clikit.LogAnswer(d.cmd, promptOptionQuestion, "text of "+strconv.Itoa(utf8.RuneCountInString(text))+" characters", "prompt")
	return text, nil
}

// askTicks asks which boxes of a checkbox list to have ticked.
func (d *promptDialogue) askTicks(p api.AgentPrompt, answer api.PromptAnswer, rows []int) (api.PromptAnswer, string, bool, error) {
	for {
		fmt.Fprint(d.out, "Tick "+d.st.Command("(numbers, as 1 3; q to quit)")+": ")
		if !d.answers.Scan() {
			return answer, "", false, cmp.Or(d.answers.Err(), errors.New("no answer given"))
		}
		input := strings.ToLower(strings.TrimSpace(d.answers.Text()))
		if input == "q" || input == "" {
			clikit.LogAnswer(d.cmd, promptTicksQuestion, "nothing", "prompt")
			if input == "" {
				fmt.Fprintln(d.out, d.st.Muted("nothing ticked, so nothing is sent"))
			}
			return answer, "", false, d.quit()
		}
		ticks, ok := parseTicks(input, len(rows))
		if !ok {
			fmt.Fprintln(d.out, d.st.Warning("give the numbers of the options, from 1 to "+strconv.Itoa(len(rows))))
			continue
		}
		var picked []int
		var labels []string
		for _, n := range ticks {
			picked = append(picked, rows[n])
			labels = append(labels, p.Options[rows[n]])
		}
		if chat := slices.IndexFunc(picked, func(i int) bool { return promptKindOf(p, i) == api.AgentPromptKindsChat }); chat >= 0 {
			if len(picked) > 1 {
				fmt.Fprintln(d.out, d.st.Warning("Chat about this declines the prompt: give it alone"))
				continue
			}
			answer.Choose = ptr(picked[0])
			clikit.LogAnswer(d.cmd, promptTicksQuestion, "chat", "prompt")
			return answer, labels[0], true, nil
		}
		if text := slices.IndexFunc(picked, func(i int) bool { return promptKindOf(p, i) == api.AgentPromptKindsText }); text >= 0 {
			typed, err := d.askText()
			if err != nil {
				return answer, "", false, err
			}
			answer.Text = &typed
			labels[text] = typed
		}
		answer.Tick = &picked
		clikit.LogAnswer(d.cmd, promptTicksQuestion, strconv.Itoa(len(picked))+" ticked", "prompt")
		return answer, strings.Join(labels, ", "), false, nil
	}
}

// parseTicks reads numbers from 1 to n separated by spaces or commas, as zero
// based indexes in order, each once.
func parseTicks(input string, n int) ([]int, bool) {
	var ticks []int
	for _, field := range strings.FieldsFunc(input, func(r rune) bool { return r == ' ' || r == ',' }) {
		v, err := strconv.Atoi(field)
		if err != nil || v < 1 || v > n || slices.Contains(ticks, v-1) {
			return nil, false
		}
		ticks = append(ticks, v-1)
	}
	slices.Sort(ticks)
	return ticks, len(ticks) > 0
}
