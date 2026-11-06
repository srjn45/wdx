package aca_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

// antigravityRecordings were made by the scripts in testdata/antigravity/record/.
var antigravityRecordings = []string{"turns-120x40", "turns-40x24", "approval-120x40", "approval-40x24", "steer-120x40", "sub-120x40"}

// antigravityStep is what one step of those scripts left on agy's screen: whether a
// message can go in, and whether the box holds what a human put there.
type antigravityStep struct{ inputBox, draft bool }

var (
	antigravityReady = antigravityStep{inputBox: true}
	antigravityDraft = antigravityStep{inputBox: true, draft: true}
)

var antigravityAtStep = map[string]antigravityStep{
	// The box is drawn, the model not yet in the footer.
	"box_drawn":   {},
	"ready":       antigravityReady,
	"draft":       antigravityDraft,
	"draft_multi": antigravityDraft,
	// The box holds the /.
	"slash_menu":       antigravityDraft,
	"rest":             antigravityReady,
	"generating":       antigravityReady,
	"running_command":  antigravityReady,
	"queued":           antigravityReady,
	"turn_done":        antigravityReady,
	"sent_now_pending": antigravityReady,
	"timeout":          antigravityReady,
	"sent_now_done":    antigravityReady,
	"task_in_turn":     antigravityReady,
	"task_after_turn":  antigravityReady,
	"task_over":        antigravityReady,
	"task_over_still":  antigravityReady,
	"before_esc":       antigravityReady,
	"interrupted":      antigravityReady,

	// An approval replaces the box.
	"approval": {},
	"denied":   antigravityReady,

	"steer_generating":      antigravityReady,
	"steer_generating_done": antigravityReady,
	"steer_command":         antigravityReady,
	"steer_command_done":    antigravityReady,
}

// antigravityAtRecordedStep is a step whose screen differs between recordings: on 40
// columns, a message queued or sent at once leaves no room for the model, so the
// box is not ready and the inbox holds until it is taken (the human's decision,
// 2026-11-06).
var antigravityAtRecordedStep = map[string]antigravityStep{
	"turns-40x24/queued":           {},
	"turns-40x24/sent_now_pending": {},
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
				if !ok && strings.HasPrefix(dir, "sub-") {
					// sub.py marks the screen every 2.5 s while a subagent works.
					want, ok = antigravityReady, true
				}
				if !ok {
					t.Fatalf("step %s is not in the table", step.name)
				}
				got := aca.AntigravityScreenParser{}.Parse(step.lines, step.undimmed)
				if got.InputBox != want.inputBox || got.Draft != want.draft {
					t.Errorf("%s: input box %v, draft %v; want %v, %v\n%s", step.name,
						got.InputBox, got.Draft, want.inputBox, want.draft, bottom(step.lines))
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
	for _, dir := range []string{"turns-120x40", "turns-40x24", "approval-120x40", "approval-40x24"} {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			sawBox := false
			for _, m := range replayed(t, antigravityRecordingDir(dir)).moments {
				footer := ""
				for _, line := range m.lines {
					if strings.TrimSpace(line) != "" {
						footer = line
					}
				}
				// The recordings ran on this model.
				if strings.Contains(footer, "Gemini 3.8 Flash") {
					break
				}
				sawBox = sawBox || strings.HasPrefix(footer, "? for shortcuts")
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
		{"empty", []string{"────", ">", "────", footer}, antigravityReady},
		{"draft", []string{"────", "> hi", "────", footer}, antigravityDraft},
		{"a continued draft", []string{"────", ">", "  more", "────", footer}, antigravityDraft},
		{"history", []string{"──", "> earlier", "  more", "", "  answer", "────", footer}, antigravityStep{}},
		{"an approval", []string{"Run this command?", "> 1. Yes, run command", "  4. No, cancel", "", footer}, antigravityStep{}},
		{"history above the box", []string{"> earlier", "", "────", ">", "────", footer}, antigravityReady},
	} {
		undimmed := append([]string(nil), c.lines...)
		undimmed[len(undimmed)-1] = "? for shortcuts"
		got := aca.AntigravityScreenParser{}.Parse(c.lines, undimmed)
		if got.InputBox != c.want.inputBox || got.Draft != c.want.draft {
			t.Errorf("%s: input box %v, draft %v; want %v, %v", c.name, got.InputBox, got.Draft, c.want.inputBox, c.want.draft)
		}
	}
}
