package aca_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/screen"
)

// draftRecordings have a marks.log: the steps of a scripted session, each a
// moment in seconds when the screen had settled, and the step's name.
var draftRecordings = []struct {
	dir string
	// exceptions are steps of this session that went otherwise.
	exceptions map[string]bool
}{
	{dir: "draft-120x40"},
	{dir: "draft-40x24"},
	{dir: "classic-draft-120x40"},
	// A draft taller than the screen scrolls the classic renderer's box and its
	// ❯ away. With no box there is no draft, and the activity is unknown: the
	// style signal's other known failure.
	{dir: "classic-draft-40x24", exceptions: map[string]bool{"long_draft": false}},
	{dir: "suggestion-120x40"},
	// Typed before Claude was ready, these never reached the box.
	{dir: "suggestion-40x24", exceptions: map[string]bool{"numbered_draft": false}},
	{dir: "classic-suggestion-120x40", exceptions: map[string]bool{"numbered_draft": false}},
	// With NO_COLOR nothing is dim and only the footer can tell, by the veto.
	{dir: "no-color-120x40"},
	{dir: "no-color-40x24"},
	// The style signal's known failure: the footer is cut off before the words
	// the veto needs, so what Claude itself put in the box reads as a draft.
	{dir: "no-color-auto-40x24", exceptions: map[string]bool{
		"placeholder": true, "cleared_idle": true, "suggestion": true, "over_suggestion_erased": true,
		"tab_cleared": true, "tasks_footer": true, "tasks_cleared": true,
	}},
	{dir: "no-color-30x24", exceptions: map[string]bool{
		"placeholder": true, "cleared_idle": true, "tasks_footer": true, "tasks_cleared": true,
	}},
}

// draftAtStep is what each step of those sessions really left in the box.
var draftAtStep = map[string]bool{
	"placeholder": false, "cleared_idle": false, "no_suggestion_after_turn": false, "no_suggestion2": false,
	"suggestion": false, "suggestion2": false, "over_suggestion_erased": false, "tab_cleared": false, "right_cleared": false,
	"multi_cleared": false, "long_cleared": false, "pasted_cleared": false, "bash_cleared": false, "slash_cleared": false,
	"after_turn1": false, "no_suggestion": false, "prompt": false, "working_empty": false, "idle_after_tool": false,
	"tasks_footer": false, "tasks_cleared": false,
	"prompt_over_draft": false, "draft_after_turn_cleared": false, "cleared_turn": false, "shell_running": false,
	"cleared_shell": false, "subagent_running": false, "cleared_subagent": false, "idle_end": false,
	// Bash mode with nothing typed after its !: no text, though not an
	// ordinary box either.
	"bash_alone": false,

	"typed_idle": true, "typed_dim_words": true, "over_suggestion": true, "tab_accepted": true, "tab_accepted_extended": true,
	"right_accepted": true, "multi_line": true, "long_draft": true, "wrapped_draft": true, "pasted_draft": true,
	"bash_typed": true, "slash_menu": true, "typed_turn_start": true, "draft_back_in_turn": true, "draft_after_turn": true,
	"typed_turn": true, "typed_shell": true, "typed_subagent": true, "numbered_draft": true, "draft_with_prompt_mark": true,
	"typed_after_tasks": true,
}

func TestClaudeCodeDraftOnRecordings(t *testing.T) {
	t.Parallel()
	for _, rec := range draftRecordings {
		t.Run(rec.dir, func(t *testing.T) {
			t.Parallel()
			steps := replayed(t, rec.dir).steps
			if len(steps) == 0 {
				t.Fatal("no steps in marks.log")
			}
			for _, step := range steps {
				want, ok := rec.exceptions[step.name]
				if !ok {
					if want, ok = draftAtStep[step.name]; !ok {
						t.Errorf("%s: not in the table", step.name)
						continue
					}
				}
				if got := (aca.ClaudeCodeScreenParser{}).Parse(step.lines, step.undimmed); got.Draft != want {
					t.Errorf("%s at %s: draft %v, want %v; state %+v; bottom of the screen:\n%s", step.name, step.at, got.Draft, want, got, bottom(step.lines))
				}
			}
		})
	}
}

// TestClaudeCodeDraftComesFromInput replays everything with input times and
// color: text nobody typed never reads as a draft, so a draft only ever appears
// just after input, and the prompt it came back from was answered by input too.
// Going by the styling alone, it also checks the footer's veto never removes
// one: no text that is not dim ever sits above a footer the veto looks for.
func TestClaudeCodeDraftComesFromInput(t *testing.T) {
	t.Parallel()
	const followsInput = 800 * time.Millisecond
	dirs := map[string]bool{}
	for dir := range motionRecordings {
		dirs[dir] = true
	}
	for _, rec := range draftRecordings {
		if !strings.HasPrefix(rec.dir, "no-color") {
			dirs[rec.dir] = true
		}
	}
	for dir := range dirs {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			rep := replayed(t, dir)
			had := false
			for _, m := range rep.moments {
				draft := aca.ClaudeCodeScreenParser{}.Parse(m.lines, m.undimmed).Draft
				if styled := (aca.ClaudeCodeScreenParser{}).Parse(withoutFooter(m.lines), m.undimmed).Draft; styled && !draft {
					t.Errorf("the footer vetoes text that is not dim at %s:\n%s", m.at, bottom(m.lines))
				}
				if draft && !had && !slices.ContainsFunc(rep.inputs, func(in time.Duration) bool { return in < m.at && m.at-in < followsInput }) {
					t.Errorf("a draft at %s, with no input before it:\n%s", m.at, bottom(m.lines))
				}
				had = draft
			}
		})
	}
}

func TestClaudeCodeDraft(t *testing.T) {
	t.Parallel()
	const rule = "────────────────"
	cases := []struct {
		name            string
		lines, undimmed []string
		want            screen.ScreenState
	}{
		{
			name:  "no undimmed lines",
			lines: []string{rule, "❯ text", rule, "  ⏸ manual mode on · ? for shortcuts"},
			want:  screen.ScreenState{Activity: idle, InputBox: true},
		},
		{
			name:     "dim text",
			lines:    []string{rule, "❯ show the ping output", rule, "  ⏸ manual mode on · ? for shortcuts"},
			undimmed: []string{rule, "❯ ", rule, ""},
			want:     screen.ScreenState{Activity: idle, InputBox: true},
		},
		{
			name:     "typed text, footer cut off",
			lines:    []string{rule, "❯ hello", rule, "  ⏸ manual mode on"},
			undimmed: []string{rule, "❯ hello", rule, "  ⏸ manual mode on"},
			want:     screen.ScreenState{Activity: unknown, InputBox: true, Draft: true},
		},
		{
			name:     "a past message above an empty box",
			lines:    []string{"❯ earlier", "● reply", rule, "❯ ", rule, "  ⏸ manual mode on · ? for shortcuts"},
			undimmed: []string{"❯ earlier", "● reply", rule, "❯ ", rule, ""},
			want:     screen.ScreenState{Activity: idle, InputBox: true},
		},
		{
			name:     "text only on a later row of the box",
			lines:    []string{rule, "❯ ", "  two", rule, "  ⏸ manual mode on"},
			undimmed: []string{rule, "❯ ", "  two", rule, ""},
			want:     screen.ScreenState{Activity: unknown, InputBox: true, Draft: true},
		},
		{
			name:     "working, typed text",
			lines:    []string{rule, "❯ abc", rule, "  ⏸ manual mode on · 1 shell"},
			undimmed: []string{rule, "❯ abc", rule, "  ⏸ manual mode on · 1 shell"},
			want:     screen.ScreenState{Activity: working, InputBox: true, Draft: true},
		},
		{
			name:     "bash mode, nothing typed",
			lines:    []string{rule, "! ", rule, "  ⏸ manual mode on"},
			undimmed: []string{rule, "! ", rule, ""},
			want:     screen.ScreenState{Activity: unknown, InputBox: true},
		},
		{
			name:     "bash mode, typed",
			lines:    []string{rule, "! ls", rule, "  ⏸ manual mode on"},
			undimmed: []string{rule, "! ls", rule, ""},
			want:     screen.ScreenState{Activity: unknown, InputBox: true, Draft: true},
		},
		{
			name:     "an open prompt has none",
			lines:    []string{rule, " Do you want to proceed?", " ❯ 1. Yes", "", " Esc to cancel · Tab to amend"},
			undimmed: []string{rule, " Do you want to proceed?", " ❯ 1. Yes", "", " Esc to cancel · Tab to amend"},
			want:     screen.ScreenState{Activity: waiting},
		},
		{
			name:     "a numbered option is no box and no draft, whatever the footer",
			lines:    []string{rule, " ☐ Color", "❯ 1. Red", "  2. Blue", rule, "  ⏸ manual mode on · ? for shortcuts"},
			undimmed: []string{rule, " ☐ Color", "❯ 1. Red", "  2. Blue", rule, "  ⏸ manual mode on · ? for shortcuts"},
			want:     screen.ScreenState{Activity: unknown},
		},
		{
			name:     "a draft that starts with a number",
			lines:    []string{rule, "❯ 1. buy milk", rule, "  ⏸ manual mode on"},
			undimmed: []string{rule, "❯ 1. buy milk", rule, ""},
			want:     screen.ScreenState{Activity: unknown, InputBox: true, Draft: true},
		},
		{
			name:     "a draft taller than the classic screen: its box is gone, so unknown, never waiting",
			lines:    []string{"  line27", "  last", rule, "  ⏸ manual mode on", "             ctrl+g to edit in VS Code"},
			undimmed: []string{"  line27", "  last", rule, "  ⏸ manual mode on", "             ctrl+g to edit in VS Code"},
			want:     screen.ScreenState{Activity: unknown},
		},
		{
			name:     "the footer overrules text that is not dim",
			lines:    []string{rule, "❯\u00a0Try \"fix lint errors\"", rule, "  ⏸ manual mode on · ? for shortcuts · ← 1 agent"},
			undimmed: []string{rule, "❯\u00a0Try \"fix lint errors\"", rule, "  ⏸ manual mode on · ? for shortcuts · ← 1 agent"},
			want:     screen.ScreenState{Activity: idle, InputBox: true},
		},
		{
			name:     "the veto's words, cut off and with another count",
			lines:    []string{rule, "❯\u00a0text", rule, "  ⏸ manual mode on · 1 shell · ← 2 ag…"},
			undimmed: []string{rule, "❯\u00a0text", rule, "  ⏸ manual mode on · 1 shell · ← 2 ag…"},
			want:     screen.ScreenState{Activity: working, InputBox: true},
		},
		{
			name:     "a footer without the veto's words",
			lines:    []string{rule, "❯\u00a0text", rule, "  ⏵⏵ auto mode on (shift+tab to cycle)"},
			undimmed: []string{rule, "❯\u00a0text", rule, "  ⏵⏵ auto mode on (shift+tab to cycle)"},
			want:     screen.ScreenState{Activity: unknown, InputBox: true, Draft: true},
		},
		{
			name:     "no box has none",
			lines:    []string{"some output"},
			undimmed: []string{"some output"},
			want:     screen.ScreenState{Activity: unknown},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			lines, undimmed := slices.Clone(c.lines), slices.Clone(c.undimmed)
			if got := (aca.ClaudeCodeScreenParser{}).Parse(lines, undimmed); got != c.want {
				t.Errorf("%+v, want %+v", got, c.want)
			}
			if !slices.Equal(lines, c.lines) || !slices.Equal(undimmed, c.undimmed) {
				t.Errorf("Parse changed its lines: %q, %q", lines, undimmed)
			}
		})
	}
}

// withoutFooter is the lines up to the last rule, which a box closes with.
func withoutFooter(lines []string) []string {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "─") {
			return lines[:i+1]
		}
	}
	return lines
}

type recordedStep struct {
	at   time.Duration
	name string
}

// readMarks reads a recording's marks.log, if it has one.
func readMarks(dir string) ([]recordedStep, error) {
	marks, err := os.ReadFile(filepath.Join(recordingDir(dir), "marks.log"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var steps []recordedStep
	for line := range strings.Lines(string(marks)) {
		at, name, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			return nil, fmt.Errorf("marks line %q", line)
		}
		sec, err := strconv.ParseFloat(at, 64)
		if err != nil {
			return nil, err
		}
		steps = append(steps, recordedStep{seconds(sec), name})
	}
	return steps, nil
}
