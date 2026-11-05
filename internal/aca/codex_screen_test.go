package aca_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/screen"
)

// codexRecordings were made by testdata/codex/record/ready.py, which records the
// box, and turns.py, which records turns and approvals.
var codexRecordings = []string{"ready-120x40", "ready-40x24", "turns-120x40", "turns-40x24", "approval-120x40", "steer-120x40"}

// codexStep is what one step of those scripts left on Codex's screen: whether a
// message can go in, whether the box holds what a human put there, and what Codex
// was doing, as its screen says; unknown is for the motion fallback.
type codexStep struct {
	inputBox, draft bool
	activity        screen.Activity
	inTurn          bool
}

var (
	codexIdle    = codexStep{inputBox: true, activity: screen.ActivityIdle}
	codexInTurn  = codexStep{inputBox: true, activity: screen.ActivityWorking, inTurn: true}
	codexUnknown = codexStep{inputBox: true, activity: screen.ActivityUnknown}
	codexDraft   = codexStep{inputBox: true, draft: true, activity: screen.ActivityUnknown}
)

var codexAtStep = map[string]codexStep{
	"loading":          {activity: screen.ActivityUnknown},
	"ready":            codexUnknown,
	"draft":            codexDraft,
	"cleared":          codexUnknown,
	"multi_line_draft": codexDraft,
	"shell_mode":       codexDraft,
	"slash_menu":       codexDraft,
	"menu_closed":      codexUnknown,
	"working":          codexInTurn,
	"answered":         codexIdle,

	"started":            codexUnknown,
	"working_background": codexInTurn,
	"tool_done":          codexIdle,
	// Nothing says Codex is in a turn while it streams.
	"streaming":          codexUnknown,
	"stream_done":        codexIdle,
	"queued":             codexInTurn,
	"queued_done":        codexIdle,
	"subagent_waiting":   codexInTurn,
	"subagent_done":      codexIdle,
	"background_in_turn": codexInTurn,
	// The turn ended, but the command it started runs on.
	"interrupted_background": {inputBox: true, activity: screen.ActivityWorking},
	// The narrow screen showed no background terminal once interrupted.
	"interrupted": codexIdle,

	// A message steered into the turn shows below the Working line until Codex
	// takes it in.
	"steer_pending": codexInTurn,
	"steer_done":    codexIdle,

	"approval": {activity: screen.ActivityWaiting},
	"denied":   codexIdle,
}

// codexAtRecordedStep is a step whose screen differs between recordings: ready.py's
// turn is short, so by its typed_in_turn it had ended at 120x40 and was streaming
// at 40x24, the draft typed into the box either way.
var codexAtRecordedStep = map[string]codexStep{
	"ready-120x40/typed_in_turn": {inputBox: true, draft: true, activity: screen.ActivityIdle},
	"ready-40x24/typed_in_turn":  codexDraft,
}

func codexRecordingDir(dir string) string {
	return filepath.Join("..", "..", "codex", "0.163.0", dir)
}

func TestCodexOnRecordedScreens(t *testing.T) {
	t.Parallel()
	for _, dir := range codexRecordings {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			for _, step := range replayed(t, codexRecordingDir(dir)).steps {
				want, ok := codexAtRecordedStep[dir+"/"+step.name]
				if !ok {
					want, ok = codexAtStep[step.name]
				}
				if !ok {
					t.Fatalf("step %s is not in the table", step.name)
				}
				got := aca.CodexScreenParser{}.Parse(step.lines, step.undimmed)
				if got.InputBox != want.inputBox || got.Draft != want.draft || got.Activity != want.activity || got.InTurn != want.inTurn {
					t.Errorf("%s: input box %v, draft %v, %s, in turn %v; want %v, %v, %s, %v\n%s", step.name,
						got.InputBox, got.Draft, got.Activity, got.InTurn, want.inputBox, want.draft, want.activity, want.inTurn, bottom(step.lines))
				}
				if wantModel := map[bool]string{true: "GPT-6-Sol"}[want.inputBox]; got.Model != wantModel {
					t.Errorf("%s: model %q, want %q", step.name, got.Model, wantModel)
				}
			}
		})
	}
}

// TestCodexNotReadyWhileLoading checks every read a holder makes before the status
// line first shows: a message delivered then loses its Tab.
func TestCodexNotReadyWhileLoading(t *testing.T) {
	t.Parallel()
	for _, dir := range []string{"ready-120x40", "ready-40x24"} {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			rep := replayed(t, codexRecordingDir(dir))
			var ready *markedRead
			for i := range rep.steps {
				if rep.steps[i].name == "ready" {
					ready = &rep.steps[i]
				}
			}
			if ready == nil {
				t.Fatal("no ready step")
			}
			sawLoading := false
			for _, m := range rep.moments {
				if m.at >= ready.at {
					break
				}
				got := aca.CodexScreenParser{}.Parse(m.lines, m.undimmed)
				status := false
				for _, line := range m.lines {
					// The recordings ran on this model.
					status = status || strings.Contains(line, "GPT-6-Sol")
				}
				if !status {
					sawLoading = true
					if got.InputBox {
						t.Errorf("at %s, before the status line shows, the box reads ready\n%s", m.at, bottom(m.lines))
					}
				}
			}
			if !sawLoading {
				t.Error("no read before the status line: the recording checks nothing")
			}
		})
	}
}

func TestCodexModel(t *testing.T) {
	t.Parallel()
	for status, want := range map[string]string{
		"  GPT-6-Sol medium · ~/dev/wdx":              "GPT-6-Sol",
		"  GPT-6-Sol default · ~/dev/wdx":             "GPT-6-Sol",
		"  GPT-6-Sol xhigh · ~/dev/wdx · Count to 40": "GPT-6-Sol",
		"  gpt-5.4 codex · /tmp/w":                    "gpt-5.4 codex",
		"  GPT-6-Sol medium · /tmp/codexrec/wkrk…":    "GPT-6-Sol",
		"  something else":                            "",
	} {
		lines := []string{"› Ask Codex to do anything", "", status, "  ? for shortcuts"}
		got := aca.CodexScreenParser{}.Parse(lines, []string{"› ", "", status, "  ? for shortcuts"})
		if !got.InputBox || got.Model != want {
			t.Errorf("status line %q: input box %v, model %q, want true, %q", status, got.InputBox, got.Model, want)
		}
	}
}

// codexTurns are stretches of turns.py's recordings where Codex was in a turn: from
// a step in it to the step after it ended, less a second, as the end shows a moment
// before its step is marked.
var codexTurns = [][2]string{
	{"working", "tool_done"},
	{"streaming", "stream_done"},
	{"queued", "queued_done"},
	{"subagent_waiting", "subagent_done"},
}

// TestCodexNeverIdleInATurn checks every read a holder makes during a turn: an
// idle there would let --instant think nothing runs, and tide show the agent
// waiting for you while it works.
func TestCodexNeverIdleInATurn(t *testing.T) {
	t.Parallel()
	for dir, turns := range map[string][][2]string{"turns-120x40": codexTurns, "turns-40x24": codexTurns, "steer-120x40": {{"working", "steer_done"}}} {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			rep := replayed(t, codexRecordingDir(dir))
			at := map[string]time.Duration{}
			for _, step := range rep.steps {
				at[step.name] = step.at
			}
			for _, turn := range turns {
				from, to := at[turn[0]], at[turn[1]]-time.Second
				if from == 0 || to <= from {
					t.Fatalf("no turn from %s to %s", turn[0], turn[1])
				}
				read := 0
				for _, m := range rep.moments {
					if m.at < from || m.at > to {
						continue
					}
					read++
					if got := (aca.CodexScreenParser{}).Parse(m.lines, m.undimmed); got.Activity == screen.ActivityIdle {
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
