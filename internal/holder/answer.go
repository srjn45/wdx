package holder

import (
	"slices"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/screen"
)

// The keys an answer presses, as a terminal sends them.
const (
	keyUp    = "\x1b[A"
	keyDown  = "\x1b[B"
	keySpace = " "
	keyEnter = "\r"
)

// answerStop is why an answer stopped: its result and, when invalid, why.
type answerStop struct {
	result AnswerResult
	reason string
}

func (s *answerStop) Error() string { return string(s.result) + ": " + s.reason }

func stopped(result AnswerResult, reason string) *answerStop {
	return &answerStop{result: result, reason: reason}
}

// answering is one answer under way: the prompt the screen should show after
// the holder's last key, and what it pressed.
type answering struct {
	h      *holder
	expect *screen.Prompt
	keys   int
	// entered is set once Enter was pressed for an answer.
	entered bool
}

// answerPrompt answers the prompt on the screen as req says, one answer at a
// time, and reports what came of it to the daemon. Its log says what kind of
// option each answer took, its index, how many boxes, the keys and the time,
// never a label, question or text, which may be the person's words.
func (h *holder) answerPrompt(req answerRequest) {
	report := AnswerReport{ID: req.ID, Result: Answered}
	refuse := func(result AnswerResult, reason string) {
		report.Result, report.Reason = result, reason
		h.log.Info("prompt not answered", "answer_id", req.ID, "answers", len(req.Answers), "result", result, "reason", reason)
	}
	switch {
	case h.watch == nil:
		refuse(AnswerUnread, "the holder reads no screen")
	case len(req.Answers) == 0:
		refuse(AnswerInvalid, "no answer")
	case !h.answering.CompareAndSwap(false, true):
		refuse(AnswerBusy, "another answer is under way")
	default:
		start := time.Now()
		a := &answering{h: h}
		question, err := a.answerAll(req.Answers)
		h.answering.Store(false)
		// The inbox held its messages meanwhile.
		wake(h.inbox.arrived)
		report.Question, report.Entered = question, a.entered
		attrs := []any{"answer_id", req.ID, "answers", len(req.Answers), "keys", a.keys, "entered", a.entered, "duration_ms", time.Since(start).Milliseconds()}
		if err != nil {
			report.Result, report.Reason = err.result, err.reason
			h.log.Info("prompt not answered", append(attrs, "result", err.result, "reason", err.reason, "question", question)...)
		} else {
			h.log.Info("prompt answered", attrs...)
		}
	}
	h.reportAnswer(report)
}

// reportAnswer tells the daemon what came of an answer. With none there the
// report is lost, as the daemon that asked is gone.
func (h *holder) reportAnswer(report AnswerReport) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conn == nil {
		return
	}
	if err := h.write(h.conn, frameAnswered, report); err != nil {
		h.log.Info("answer not reported", "answer_id", report.ID, "err", err)
		// A write cut short leaves the frames out of step.
		h.conn.Close()
	}
}

// What the screen shows after Enter on an answer.
type afterEnter int

const (
	closes     afterEnter = iota // the prompt goes: a yes/no, a single choice, a chat, the review
	nextShows                    // the next question of several
	reviewShow                   // Claude's review of the answers
)

// answerAll answers each question in turn, checking at each the screen shows it,
// then passes Claude's review with Enter on its first option, Submit answers. It
// returns the index of the answer it stopped at.
func (a *answering) answerAll(answers []Answer) (int, *answerStop) {
	var after afterEnter
	for i, answer := range answers {
		p := a.h.watch.state().Prompt
		if p == nil || p.Question != answer.Question || !slices.Equal(p.Options, answer.Options) {
			return i, stopped(AnswerPromptChanged, "the screen shows another prompt")
		}
		if i > 0 && after != nextShows {
			return i, stopped(AnswerPromptChanged, "the prompt has fewer questions than answers")
		}
		a.expect = p
		var err *answerStop
		if after, err = a.answerOne(i, answer); err != nil {
			return i, err
		}
	}
	if after != reviewShow {
		return len(answers) - 1, nil
	}
	if a.expect == nil || !a.expect.Review {
		return len(answers) - 1, stopped(AnswerPromptChanged, "the review did not show")
	}
	if err := a.moveTo(0); err != nil {
		return len(answers) - 1, err
	}
	if err := a.enter(closes); err != nil {
		return len(answers) - 1, err
	}
	if a.expect != nil && a.expect.Review {
		return len(answers) - 1, stopped(AnswerPromptChanged, "the review showed again")
	}
	a.h.log.Info("prompt review passed", "keys", a.keys)
	return len(answers) - 1, nil
}

// answerOne answers the question on the screen, a.expect, and says what showed
// after its Enter.
func (a *answering) answerOne(i int, answer Answer) (afterEnter, *answerStop) {
	p := a.expect
	choose, err := checkAnswer(p, answer)
	if err != nil {
		return 0, err
	}
	if p.Multi && choose < 0 {
		// The boxes top to bottom, then Submit.
		for option, kind := range p.Kinds {
			want := slices.Contains(answer.Tick, option)
			switch {
			case kind == screen.OptionText && want && answer.Text != "":
				if err := a.typeIn(option, answer.Text); err != nil {
					return 0, err
				}
				if !slices.Contains(a.expect.Ticked, option) {
					if err := a.tick(option); err != nil {
						return 0, err
					}
				}
			case kind != screen.OptionChoice && kind != screen.OptionText:
			case want != slices.Contains(a.expect.Ticked, option):
				if err := a.tick(option); err != nil {
					return 0, err
				}
			}
		}
		choose = slices.Index(p.Kinds, screen.OptionSubmit)
	} else if answer.Text != "" {
		if err := a.typeIn(choose, answer.Text); err != nil {
			return 0, err
		}
	}
	if err := a.moveTo(choose); err != nil {
		return 0, err
	}
	after := afterEnterOn(p, p.Kinds[choose])
	a.h.log.Info("prompt question answering", "question", i, "option", choose, "option_kind", p.Kinds[choose], "ticked", len(answer.Tick), "keys", a.keys)
	if err := a.enter(after); err != nil {
		return 0, err
	}
	return a.after(after), nil
}

// checkAnswer is the option Enter goes on, -1 for a checkbox answer's Submit, or
// why the answer does not fit p.
func checkAnswer(p *screen.Prompt, answer Answer) (int, *answerStop) {
	invalid := func(reason string) (int, *answerStop) { return 0, stopped(AnswerInvalid, reason) }
	if len(p.Kinds) != len(p.Options) {
		return invalid("the prompt's options are not read")
	}
	if p.Selected < 0 {
		return invalid("the prompt shows no cursor")
	}
	if strings.ContainsAny(answer.Text, "\r\n") {
		return invalid("the text is not one line")
	}
	for _, t := range answer.Tick {
		if !p.Multi {
			return invalid("only a checkbox list takes ticks")
		}
		if t < 0 || t >= len(p.Kinds) || p.Kinds[t] != screen.OptionChoice && p.Kinds[t] != screen.OptionText {
			return invalid("a tick is not on a box")
		}
	}
	if answer.Choose == nil {
		if !p.Multi {
			return invalid("no option chosen")
		}
		if len(answer.Tick) == 0 {
			return invalid("nothing ticked")
		}
		return -1, checkTickedText(p, answer)
	}
	choose := *answer.Choose
	if choose < 0 || choose >= len(p.Kinds) {
		return invalid("the option chosen is not one of the prompt's")
	}
	switch kind := p.Kinds[choose]; {
	case p.Multi && kind == screen.OptionSubmit:
		if len(answer.Tick) == 0 {
			return invalid("nothing ticked")
		}
		return -1, checkTickedText(p, answer)
	case p.Multi && kind != screen.OptionChat:
		return invalid("a checkbox list is answered by its ticks or its chat")
	case kind == screen.OptionSubmit:
		return invalid("the option chosen is a Submit row")
	case len(answer.Tick) > 0:
		return invalid("ticks with a chat")
	case answer.Text != "" && (kind != screen.OptionText || p.Kind != screen.PromptDecision):
		return invalid("only a decision's text option takes text")
	}
	return choose, nil
}

// checkTickedText checks a checkbox answer's text goes with its text option
// ticked.
func checkTickedText(p *screen.Prompt, answer Answer) *answerStop {
	text := slices.Index(p.Kinds, screen.OptionText)
	switch {
	case answer.Text != "" && !slices.Contains(answer.Tick, text):
		return stopped(AnswerInvalid, "text for a text option not ticked")
	case answer.Text == "" && text >= 0 && slices.Contains(answer.Tick, text) && !slices.Contains(p.Ticked, text):
		return stopped(AnswerInvalid, "a text option ticked without text")
	}
	return nil
}

// afterEnterOn is what Enter on an option of kind shows: the next question
// while more than one is not answered, the review after the last of several
// or a checkbox list's Submit, else nothing, as for a chat.
func afterEnterOn(p *screen.Prompt, kind screen.OptionKind) afterEnter {
	switch {
	case kind == screen.OptionChat, p.Review, p.Kind != screen.PromptDecision:
		return closes
	case len(p.Questions) > 1 && len(p.Questions)-len(p.Answered) > 1:
		return nextShows
	case len(p.Questions) > 1, p.Multi:
		return reviewShow
	}
	return closes
}

// after is what showed after Enter, as enter left it in a.expect: nil when the
// prompt closed.
func (a *answering) after(want afterEnter) afterEnter {
	switch p := a.expect; {
	case p == nil:
		return closes
	case p.Review:
		return reviewShow
	case want == closes:
		return closes // another prompt came
	}
	return nextShows
}

// moveTo moves the cursor to option target, one key at a time.
func (a *answering) moveTo(target int) *answerStop {
	for a.expect.Selected != target {
		key, next := keyDown, a.expect.Selected+1
		if target < a.expect.Selected {
			key, next = keyUp, a.expect.Selected-1
		}
		want := *a.expect
		want.Selected = next
		if err := a.press(key, func(p *screen.Prompt) bool { return p.Equal(&want) }); err != nil {
			return err
		}
	}
	return nil
}

// tick moves to option and presses Space, which ticks or unticks its box.
func (a *answering) tick(option int) *answerStop {
	if err := a.moveTo(option); err != nil {
		return err
	}
	want := *a.expect
	if slices.Contains(want.Ticked, option) {
		want.Ticked = slices.DeleteFunc(slices.Clone(want.Ticked), func(t int) bool { return t == option })
	} else {
		want.Ticked = append(slices.Clone(want.Ticked), option)
		slices.Sort(want.Ticked)
	}
	return a.press(keySpace, func(p *screen.Prompt) bool { return p.Equal(&want) })
}

// typeIn moves to text option and pastes text, unless its label shows it
// already, and checks the label shows it then: in a checkbox list its box
// ticks too.
func (a *answering) typeIn(option int, text string) *answerStop {
	if err := a.moveTo(option); err != nil {
		return err
	}
	if labelShows(a.expect.Options[option], text) {
		return nil
	}
	before := *a.expect
	shown := func(p *screen.Prompt) bool {
		if len(p.Options) != len(before.Options) || !labelShows(p.Options[option], text) {
			return false
		}
		got := *p
		got.Options = slices.Clone(p.Options)
		got.Options[option] = before.Options[option]
		got.Ticked = slices.DeleteFunc(slices.Clone(got.Ticked), func(t int) bool { return t == option && !slices.Contains(before.Ticked, option) })
		return got.Equal(&before)
	}
	return a.write(func() error { return a.h.paste(ptyInput{a.h}, text) }, shown)
}

// labelShows reports whether an option's label shows text, as the screen wraps
// or cuts it.
func labelShows(label, text string) bool {
	label, text = strings.Join(strings.Fields(label), " "), strings.Join(strings.Fields(text), " ")
	if stem, cut := strings.CutSuffix(label, "…"); cut {
		return stem != "" && strings.HasPrefix(text, strings.TrimSpace(stem))
	}
	return label == text
}

// enter presses Enter, once, and waits for what it should show: the prompt
// gone, or the next question or the review, which becomes a.expect.
func (a *answering) enter(want afterEnter) *answerStop {
	if p := a.h.watch.state().Prompt; !p.Equal(a.expect) {
		return stopped(AnswerPromptChanged, "the prompt changed before Enter")
	}
	if _, err := (ptyInput{a.h}).Write([]byte(keyEnter)); err != nil {
		return stopped(AnswerEnded, err.Error())
	}
	a.keys++
	a.entered = true
	timer := time.NewTimer(a.h.sendTimes.AnswerClose)
	defer timer.Stop()
	for {
		changes := a.h.watch.changes()
		p := a.h.watch.state().Prompt
		switch {
		case p.Equal(a.expect):
		case p == nil:
			if want == closes {
				a.expect = nil
				return nil
			}
			// A redraw between two prompts.
		case p.Review == a.expect.Review && p.Question == a.expect.Question && slices.Equal(p.Options, a.expect.Options):
			// The same question, its cursor or boxes moved: not by Enter.
			return stopped(AnswerPromptChanged, "the prompt changed after Enter")
		default:
			a.expect = p
			return nil
		}
		select {
		case <-changes:
		case <-timer.C:
			if p == nil {
				a.expect = nil
				return nil
			}
			return stopped(AnswerNotConfirmed, "the prompt did not close after Enter")
		case <-a.h.ended:
			return stopped(AnswerEnded, errEnded.Error())
		}
	}
}

// press presses key, once the screen still shows a.expect, and waits until the
// prompt reads as shown wants, which becomes a.expect.
func (a *answering) press(key string, shown func(*screen.Prompt) bool) *answerStop {
	return a.write(func() error {
		_, err := ptyInput{a.h}.Write([]byte(key))
		return err
	}, shown)
}

// write writes what a key or a paste sends, once the screen still shows
// a.expect, and waits until the prompt reads as shown wants. A prompt that is
// neither is a change the holder did not make, and stops it.
func (a *answering) write(send func() error, shown func(*screen.Prompt) bool) *answerStop {
	if p := a.h.watch.state().Prompt; !p.Equal(a.expect) {
		return stopped(AnswerPromptChanged, "the prompt changed before a key")
	}
	if err := send(); err != nil {
		return stopped(AnswerEnded, err.Error())
	}
	a.keys++
	timer := time.NewTimer(a.h.sendTimes.AnswerKey)
	defer timer.Stop()
	for {
		changes := a.h.watch.changes()
		p := a.h.watch.state().Prompt
		switch {
		case p != nil && shown(p):
			a.expect = p
			return nil
		case p != nil && !p.Equal(a.expect):
			return stopped(AnswerPromptChanged, "the prompt changed after a key")
		}
		select {
		case <-changes:
		case <-timer.C:
			if p == nil {
				return stopped(AnswerPromptChanged, "the prompt closed")
			}
			return stopped(AnswerNotConfirmed, "a key did not show on the screen")
		case <-a.h.ended:
			return stopped(AnswerEnded, errEnded.Error())
		}
	}
}
