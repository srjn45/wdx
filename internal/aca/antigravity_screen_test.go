package aca_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/screen"
)

// antigravityRecordings were made by the scripts in testdata/antigravity/record/.
var antigravityRecordings = []string{"turns-120x40", "turns-40x24", "approval-120x40", "approval-40x24", "steer-120x40", "sub-120x40", "status-120x40", "status-user-120x40"}

// antigravityStep is what one step of those scripts left on agy's screen: whether a
// message can go in, whether the box holds what a human put there, and what agy
// was doing, as its screen says; unknown is for the motion fallback.
type antigravityStep struct {
	inputBox, draft bool
	activity        screen.Activity
	inTurn          bool
}

var (
	antigravityIdle       = antigravityStep{inputBox: true, activity: screen.ActivityIdle}
	antigravityInTurn     = antigravityStep{inputBox: true, activity: screen.ActivityWorking, inTurn: true}
	antigravityBackground = antigravityStep{inputBox: true, activity: screen.ActivityWorking}
	antigravityDraft      = antigravityStep{inputBox: true, draft: true, activity: screen.ActivityUnknown}
)

var antigravityAtStep = map[string]antigravityStep{
	// The box is drawn, the model not yet in the footer: at rest, not ready.
	"box_drawn":   {activity: screen.ActivityIdle},
	"ready":       antigravityIdle,
	"draft":       antigravityDraft,
	"draft_multi": antigravityDraft,
	// The box holds the /, and the menu's footer says esc to cancel.
	"slash_menu":       antigravityDraft,
	"rest":             antigravityIdle,
	"generating":       antigravityInTurn,
	"running_command":  antigravityInTurn,
	"queued":           antigravityInTurn,
	"turn_done":        antigravityIdle,
	"sent_now_pending": antigravityInTurn,
	"timeout":          antigravityIdle,
	"sent_now_done":    antigravityIdle,
	// A background task in the panel, during and after the turn that started it.
	"task_in_turn":    antigravityInTurn,
	"task_after_turn": antigravityBackground,
	"task_over":       antigravityBackground,
	"task_over_still": antigravityBackground,
	"before_esc":      antigravityInTurn,
	"interrupted":     antigravityBackground,

	// An approval replaces the box.
	"approval": {activity: screen.ActivityWaiting},
	"denied":   antigravityIdle,

	// A message sent at once shows above the box until agy takes it in.
	"steer_generating":      antigravityInTurn,
	"steer_generating_done": antigravityIdle,
	"steer_command":         antigravityInTurn,
	"steer_command_done":    antigravityInTurn,

	// sub.py marks the screen every 2.5 s: the turn delegates to a subagent, ends
	// while it works, and starts again when it reports back.
	"sub00": antigravityInTurn,
	"sub01": antigravityInTurn,
	"sub02": antigravityInTurn,
	"sub03": antigravityInTurn,
	"sub04": antigravityInTurn,
	"sub05": antigravityBackground,
	"sub06": antigravityBackground,
	"sub07": antigravityBackground,
	"sub08": antigravityBackground,
	"sub09": antigravityBackground,
	"sub10": antigravityInTurn,
	"sub11": antigravityBackground,
	"sub12": antigravityIdle,
	"sub13": antigravityIdle,
	"done":  antigravityIdle,
}

// antigravityAtRecordedStep is a step whose screen differs between recordings: on 40
// columns, a message queued or sent at once leaves no room for the model, so the
// box is not ready and the inbox holds until it is taken (the human's decision,
// 2026-11-06), though the footer's left still says it is in a turn. And on 40
// columns the background task had ended by the time turns.py looked for it, and in
// status.py's run the model waited for it before it answered.
var antigravityAtRecordedStep = map[string]antigravityStep{
	"status-120x40/task_over":            antigravityIdle,
	"status-120x40/task_over_still":      antigravityIdle,
	"status-120x40/interrupted":          antigravityIdle,
	"status-user-120x40/task_over":       antigravityIdle,
	"status-user-120x40/task_over_still": antigravityIdle,
	"status-user-120x40/interrupted":     antigravityIdle,
	"turns-40x24/queued":                 {activity: screen.ActivityWorking, inTurn: true},
	"turns-40x24/sent_now_pending":       {activity: screen.ActivityWorking, inTurn: true},
	"turns-40x24/task_in_turn":           antigravityIdle,
	"turns-40x24/task_after_turn":        antigravityIdle,
	"turns-40x24/task_over":              antigravityIdle,
	"turns-40x24/task_over_still":        antigravityIdle,
	"turns-40x24/interrupted":            antigravityIdle,
}

func antigravityRecordingDir(dir string) string {
	return filepath.Join("..", "..", "antigravity", "1.5.2", dir)
}

func TestAntigravityOnRecordedScreens(t *testing.T) {
	t.Parallel()
	for _, dir := range antigravityRecordings {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			for _, step := range replayed(t, antigravityRecordingDir(dir)).steps {
				want, ok := antigravityAtRecordedStep[dir+"/"+step.name]
				if !ok {
					want, ok = antigravityAtStep[step.name]
				}
				if !ok {
					t.Fatalf("step %s is not in the table", step.name)
				}
				got := aca.AntigravityScreenParser{}.Parse(step.lines, step.undimmed)
				if got.InputBox != want.inputBox || got.Draft != want.draft || got.Activity != want.activity || got.InTurn != want.inTurn {
					t.Errorf("%s: input box %v, draft %v, %s, in turn %v; want %v, %v, %s, %v\n%s", step.name,
						got.InputBox, got.Draft, got.Activity, got.InTurn, want.inputBox, want.draft, want.activity, want.inTurn, bottom(step.lines))
				}
				if wantModel := map[bool]string{true: "Gemini 3.8 Flash"}[want.inputBox]; got.Model != wantModel {
					t.Errorf("%s: model %q, want %q", step.name, got.Model, wantModel)
				}
			}
		})
	}
}

// TestAntigravityNotReadyBeforeTheModel checks every read a holder makes before the
// footer first names the model: the box is drawn by then, but readiness waits.
func TestAntigravityNotReadyBeforeTheModel(t *testing.T) {
	t.Parallel()
	for _, dir := range []string{"turns-120x40", "turns-40x24", "approval-120x40", "approval-40x24", "status-120x40", "status-user-120x40"} {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			sawBox := false
			for _, m := range replayed(t, antigravityRecordingDir(dir)).moments {
				// The recordings ran on this model; only the footer writes it so. In
				// status-user-120x40 the user's line is under the footer.
				if slices.ContainsFunc(m.lines, func(line string) bool { return strings.Contains(line, "Gemini 3.8 Flash · low") }) {
					break
				}
				sawBox = sawBox || slices.ContainsFunc(m.lines, func(line string) bool { return strings.HasPrefix(line, "? for shortcuts") })
				if got := (aca.AntigravityScreenParser{}).Parse(m.lines, m.undimmed); got.InputBox {
					t.Errorf("at %s, before the model shows, the box reads ready\n%s", m.at, bottom(m.lines))
				}
			}
			if !sawBox {
				t.Error("no read of the box before the model: the recording checks nothing")
			}
		})
	}
}

func TestAntigravityModel(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ left, right, want string }{
		{"? for shortcuts", "Gemini 3.8 Flash · low", "Gemini 3.8 Flash"},
		{"esc to cancel", "Gemini 3.8 Flash · low · 1 task(s) · /tasks", "Gemini 3.8 Flash"},
		{"? for shortcuts", "Gemini 3.8 Flash · low · 1 subagent(s)", "Gemini 3.8 Flash"},
		{"", "Claude Sonnet 4.6 (Thinking) · high", "Claude Sonnet 4.6 (Thinking)"},
		// Cut by a narrow screen.
		{"  Press up to edit queued messages", "Gemini 3.8 F", ""},
		{"? for shortcuts", "", ""},
	} {
		footer := c.left + strings.Repeat(" ", 5) + c.right
		lines := []string{"────", ">", "────", strings.TrimRight(footer, " ")}
		got := aca.AntigravityScreenParser{}.Parse(lines, []string{"────", ">", "────", c.left})
		if got.Model != c.want || got.InputBox != (c.want != "") {
			t.Errorf("footer %q: input box %v, model %q, want %q", footer, got.InputBox, got.Model, c.want)
		}
	}
}

// TestAntigravityBoxNeedsItsRule: only the box has a rule right below its lines, not
// an earlier message nor a menu's or an approval's selected entry.
func TestAntigravityBoxNeedsItsRule(t *testing.T) {
	t.Parallel()
	const footer = "? for shortcuts     Gemini 3.8 Flash · low"
	for _, c := range []struct {
		name  string
		lines []string
		want  antigravityStep
	}{
		{"empty", []string{"────", ">", "────", footer}, antigravityIdle},
		{"draft", []string{"────", "> hi", "────", footer}, antigravityDraft},
		{"a continued draft", []string{"────", ">", "  more", "────", footer}, antigravityDraft},
		{"history", []string{"──", "> earlier", "  more", "", "  answer", "────", footer}, antigravityStep{}},
		{"an approval", []string{"Run this command?", "> 1. Yes, run command", "  4. No, cancel", "", footer}, antigravityStep{}},
		{"history above the box", []string{"> earlier", "", "────", ">", "────", footer}, antigravityIdle},
	} {
		undimmed := append([]string(nil), c.lines...)
		undimmed[len(undimmed)-1] = "? for shortcuts"
		got := aca.AntigravityScreenParser{}.Parse(c.lines, undimmed)
		if got.InputBox != c.want.inputBox || got.Draft != c.want.draft {
			t.Errorf("%s: input box %v, draft %v; want %v, %v", c.name, got.InputBox, got.Draft, c.want.inputBox, c.want.draft)
		}
	}
}

// TestAntigravityFooterAboveTheUsersLine: the footer is the line under the box's
// rule, past the panel and the "/" menu; what agy draws under it, the user's own
// status line that wdx runs with its own, is not read, and a prompt to choose from
// still reads waiting with that line among the last three.
func TestAntigravityFooterAboveTheUsersLine(t *testing.T) {
	t.Parallel()
	const (
		rule  = "────────────────────────────────────────"
		model = "Gemini 3.8 Flash · low"
		users = "their line: 1639 bytes"
	)
	footer := func(left, right string) string { return left + strings.Repeat(" ", 5) + right }
	for _, c := range []struct {
		name  string
		lines []string
		// undimmed is the footer's left part, which is not dim; "" for a screen with no footer.
		undimmed string
		want     antigravityStep
		model    string
	}{
		{"at rest", []string{rule, ">", rule, footer("? for shortcuts", model), users}, "? for shortcuts", antigravityIdle, "Gemini 3.8 Flash"},
		{"in a turn", []string{rule, ">", rule, footer("esc to cancel", model), users}, "esc to cancel", antigravityInTurn, "Gemini 3.8 Flash"},
		{"a draft", []string{rule, "> hi", rule, footer("", model), users}, "", antigravityDraft, "Gemini 3.8 Flash"},
		{"a panel", []string{rule, ">", rule, "  ● [09:54:17] sleep 25 running", rule, footer("? for shortcuts", model+" · 1 task(s) · /tasks"), users}, "? for shortcuts", antigravityBackground, "Gemini 3.8 Flash"},
		{"the / menu", []string{rule, "> /", rule, "  /add-dir   Add a directory", "  ↑/↓ Navigate · enter Select · tab Complete", footer("esc to cancel", model), users}, "esc to cancel", antigravityDraft, "Gemini 3.8 Flash"},
		{"an approval", []string{"Run this command?", "> 1. Yes, run command", "  4. No, cancel", "", "↑/↓ Navigate · tab Amend", footer("esc to cancel", model), users}, "esc to cancel", antigravityStep{activity: screen.ActivityWaiting}, ""},
		{"the model not shown yet", []string{rule, ">", rule, "? for shortcuts", users}, "? for shortcuts", antigravityStep{activity: screen.ActivityIdle}, ""},
	} {
		undimmed := append([]string(nil), c.lines...)
		for i, line := range c.lines {
			if strings.Contains(line, model) {
				undimmed[i] = c.undimmed
			}
		}
		got := aca.AntigravityScreenParser{}.Parse(c.lines, undimmed)
		if got.InputBox != c.want.inputBox || got.Draft != c.want.draft || got.Activity != c.want.activity || got.InTurn != c.want.inTurn || got.Model != c.model {
			t.Errorf("%s: input box %v, draft %v, %s, in turn %v, model %q; want %v, %v, %s, %v, %q", c.name,
				got.InputBox, got.Draft, got.Activity, got.InTurn, got.Model, c.want.inputBox, c.want.draft, c.want.activity, c.want.inTurn, c.model)
		}
	}
}

// antigravityTurns are stretches of the recordings where agy was in a turn: from a
// step in it to the step after it ended, less two seconds, as the scripts mark the
// end up to 1.5 s after it shows.
var antigravityTurns = map[string][][2]string{
	"turns-120x40": {{"generating", "turn_done"}, {"task_in_turn", "task_after_turn"}},
	// Recorded with wdx's status line on, with stack_with_default, and with a user's
	// line under the footer (the wdx doc ANTIGRAVITY_CONTEXT).
	"status-120x40":      {{"generating", "turn_done"}, {"task_in_turn", "task_after_turn"}},
	"status-user-120x40": {{"generating", "turn_done"}, {"task_in_turn", "task_after_turn"}},
	"turns-40x24":        {{"generating", "turn_done"}, {"sent_now_pending", "sent_now_done"}},
	"steer-120x40":       {{"steer_generating", "steer_generating_done"}},
	"sub-120x40":         {{"sub00", "sub05"}},
}

// TestAntigravityNeverIdleInATurn checks every read a holder makes during a turn:
// an idle there would let --instant think nothing runs, and tide show the agent
// waiting for you while it works.
func TestAntigravityNeverIdleInATurn(t *testing.T) {
	t.Parallel()
	for dir, turns := range antigravityTurns {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			rep := replayed(t, antigravityRecordingDir(dir))
			at := map[string]time.Duration{}
			for _, step := range rep.steps {
				at[step.name] = step.at
			}
			for _, turn := range turns {
				from, to := at[turn[0]], at[turn[1]]-2*time.Second
				if from == 0 || to <= from {
					t.Fatalf("no turn from %s to %s", turn[0], turn[1])
				}
				read := 0
				for _, m := range rep.moments {
					if m.at < from || m.at > to {
						continue
					}
					read++
					if got := (aca.AntigravityScreenParser{}).Parse(m.lines, m.undimmed); got.Activity == screen.ActivityIdle {
						t.Errorf("%s at %s, in the turn from %s, reads idle\n%s", dir, m.at, turn[0], bottom(m.lines))
					}
				}
				if read == 0 {
					t.Errorf("no read in the turn from %s", turn[0])
				}
			}
		})
	}
}

// TestAntigravityActivity covers the screens no recording has: the trust prompt and
// the model picker, a draft typed during a turn, which empties the footer's left, a
// turn's footer with no spinner above the box, a spinner that is not just above it,
// and background work on a narrow screen, which cuts the footer's count.
func TestAntigravityActivity(t *testing.T) {
	t.Parallel()
	const (
		rule  = "────────────────────────────────────────"
		model = "Gemini 3.8 Flash · low"
	)
	for _, c := range []struct {
		name         string
		lines        []string
		left         string
		want         screen.Activity
		wantInTurn   bool
		wantInputBox bool
	}{
		{"the trust prompt", []string{"Do you trust the contents of this project?", "> 1. Yes", "  2. No", "", "↑/↓ Navigate · enter Confirm"}, "↑/↓ Navigate · enter Confirm", screen.ActivityWaiting, false, false},
		{"the model picker", []string{"Switch Model", "> Gemini 3.8 Flash", "  Gemini 3.8 Pro", "", "Keyboard: ↑/↓ Navigate  ←/→ Effort  enter Select  esc Go Back"}, "Keyboard: ↑/↓ Navigate  ←/→ Effort  enter Select  esc Go Back", screen.ActivityWaiting, false, false},
		{"a draft in a turn", []string{"> earlier", "⣷  Generating...", "└ Tip: Press esc to interrupt generation.", "▸ Then reply LIMA. (alt+m to send immediately)", rule, "> a draft", rule, "                  " + model}, "", screen.ActivityWorking, true, true},
		{"a draft at rest", []string{"  LIMA", "", rule, "> a draft", rule, "                  " + model}, "", screen.ActivityUnknown, false, true},
		{"a task on a narrow screen", []string{"  PAPA", "", rule, ">", rule, "  ● [09:54:17] sleep 25 running", rule, "? for shortcuts   Gemini 3.8 F"}, "? for shortcuts", screen.ActivityWorking, false, false},
		{"a subagent counted", []string{"  ROMEO", "", rule, ">", rule, "? for shortcuts   " + model + " · 1 subagent(s)"}, "? for shortcuts", screen.ActivityWorking, false, true},
		{"a turn showing no spinner", []string{"  Waiting for subagent to complete.", "", rule, ">", rule, "esc to cancel     " + model}, "esc to cancel", screen.ActivityWorking, true, true},
		{"a message queued, no spinner", []string{"▸ Then reply LIMA. (alt+m to send immediately)", rule, ">", rule, "  Press up to edit queued messages   " + model}, "  Press up to edit queued messages", screen.ActivityWorking, true, true},
		{"an answer quoting the spinner", []string{"  ⣷  Generating...", rule, ">", rule, "? for shortcuts   " + model}, "? for shortcuts", screen.ActivityIdle, false, true},
		{"a spinner in an earlier block", []string{"⣷  Generating...", "", rule, ">", rule, "? for shortcuts   " + model}, "? for shortcuts", screen.ActivityIdle, false, true},
		{"a footer not known", []string{rule, ">", rule, "something new     " + model}, "something new", screen.ActivityUnknown, false, true},
	} {
		undimmed := append([]string(nil), c.lines...)
		undimmed[len(undimmed)-1] = c.left
		got := aca.AntigravityScreenParser{}.Parse(c.lines, undimmed)
		if got.Activity != c.want || got.InTurn != c.wantInTurn || got.InputBox != c.wantInputBox {
			t.Errorf("%s: %s, in turn %v, input box %v; want %s, %v, %v", c.name, got.Activity, got.InTurn, got.InputBox, c.want, c.wantInTurn, c.wantInputBox)
		}
	}
}
