package aca_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/screen"
)

// codexRecordings were made by testdata/codex/record/ready.py.
var codexRecordings = []string{"ready-120x40", "ready-40x24"}

// codexAtStep is what each step of ready.py left on Codex's screen: whether a
// message can go in, and whether the box holds what a human put there.
var codexAtStep = map[string]struct{ inputBox, draft bool }{
	"loading":          {false, false},
	"ready":            {true, false},
	"draft":            {true, true},
	"cleared":          {true, false},
	"multi_line_draft": {true, true},
	"shell_mode":       {true, true},
	"slash_menu":       {true, true},
	"menu_closed":      {true, false},
	"working":          {true, false},
	"typed_in_turn":    {true, true},
	"answered":         {true, false},
}

func codexRecordingDir(dir string) string {
	return filepath.Join("..", "..", "codex", "0.163.0", dir)
}

func TestCodexOnRecordedScreens(t *testing.T) {
	t.Parallel()
	for _, dir := range codexRecordings {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			steps := replayed(t, codexRecordingDir(dir)).steps
			if len(steps) != len(codexAtStep) {
				t.Fatalf("%d steps in marks.log, %d in the table", len(steps), len(codexAtStep))
			}
			for _, step := range steps {
				want, ok := codexAtStep[step.name]
				if !ok {
					t.Fatalf("step %s is not in the table", step.name)
				}
				got := aca.CodexScreenParser{}.Parse(step.lines, step.undimmed)
				if got.Activity != screen.ActivityUnknown || got.InTurn {
					t.Errorf("%s: activity %s, in turn %v, want unknown and not in a turn, which motion decides", step.name, got.Activity, got.InTurn)
				}
				if got.InputBox != want.inputBox || got.Draft != want.draft {
					t.Errorf("%s: input box %v, draft %v, want %v, %v\n%s", step.name, got.InputBox, got.Draft, want.inputBox, want.draft, bottom(step.lines))
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
	for _, dir := range codexRecordings {
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
