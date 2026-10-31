package aca_test

import (
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
	dir        string
	cols, rows int
	// exceptions are steps of this session that went otherwise.
	exceptions map[string]bool
}{
	{dir: "draft-120x40", cols: 120, rows: 40},
	{dir: "draft-40x24", cols: 40, rows: 24},
	{dir: "classic-draft-120x40", cols: 120, rows: 40},
	// A draft taller than the screen scrolls the classic renderer's box and its
	// ❯ away. With no box there is no draft, and the activity is unknown: the
	// style signal's other known failure.
	{dir: "classic-draft-40x24", cols: 40, rows: 24, exceptions: map[string]bool{"long_draft": false}},
	{dir: "suggestion-120x40", cols: 120, rows: 40},
	// Typed before Claude was ready, these never reached the box.
	{dir: "suggestion-40x24", cols: 40, rows: 24, exceptions: map[string]bool{"numbered_draft": false}},
	{dir: "classic-suggestion-120x40", cols: 120, rows: 40, exceptions: map[string]bool{"numbered_draft": false}},
	// With NO_COLOR nothing is dim and only the footer can tell, by the veto.
	{dir: "no-color-120x40", cols: 120, rows: 40},
	{dir: "no-color-40x24", cols: 40, rows: 24},
	// The style signal's known failure: the footer is cut off before the words
	// the veto needs, so what Claude itself put in the box reads as a draft.
	{dir: "no-color-auto-40x24", cols: 40, rows: 24, exceptions: map[string]bool{
		"placeholder": true, "cleared_idle": true, "suggestion": true, "over_suggestion_erased": true,
		"tab_cleared": true, "tasks_footer": true, "tasks_cleared": true,
	}},
	{dir: "no-color-30x24", cols: 30, rows: 24, exceptions: map[string]bool{
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
			chunks, _ := readRecording(t, rec.dir)
			s := screen.New(rec.cols, rec.rows, nil)
			defer s.Close()
			for _, step := range readMarks(t, rec.dir) {
				for len(chunks) > 0 && chunks[0].at <= step.at {
					s.Write(chunks[0].data)
					chunks = chunks[1:]
				}
				want, ok := rec.exceptions[step.name]
				if !ok {
					if want, ok = draftAtStep[step.name]; !ok {
						t.Errorf("%s: not in the table", step.name)
						continue
					}
				}
				lines := s.Text()
				if got := (aca.ClaudeCodeScreenParser{}).Parse(lines, s.Undimmed()); got.Draft != want {
					t.Errorf("%s at %s: draft %v, want %v; state %+v; bottom of the screen:\n%s", step.name, step.at, got.Draft, want, got, bottom(lines))
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
	sizes := map[string][2]int{}
	for dir, rec := range motionRecordings {
		sizes[dir] = [2]int{rec.cols, rec.rows}
	}
	for _, rec := range draftRecordings {
		if !strings.HasPrefix(rec.dir, "no-color") {
			sizes[rec.dir] = [2]int{rec.cols, rec.rows}
		}
	}
	for dir, size := range sizes {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			_, inputs := readRecording(t, dir)
			had := false
			replayStyled(t, dir, size[0], size[1], func(at time.Duration, lines, undimmed []string) {
				draft := aca.ClaudeCodeScreenParser{}.Parse(lines, undimmed).Draft
				if styled := (aca.ClaudeCodeScreenParser{}).Parse(withoutFooter(lines), undimmed).Draft; styled && !draft {
					t.Errorf("the footer vetoes text that is not dim at %s:\n%s", at, bottom(lines))
				}
				if draft && !had && !slices.ContainsFunc(inputs, func(in time.Duration) bool { return in < at && at-in < followsInput }) {
					t.Errorf("a draft at %s, with no input before it:\n%s", at, bottom(lines))
				}
				had = draft
			})
		})
	}
}

func TestClaudeCodeDraft(t *testing.T) {
	t.Parallel()
	const rule = "────────────────"
	cases := []struct {
		name            string
		lines, undimmed []string
		want            aca.ScreenState
	}{
		{
			name:  "no undimmed lines",
			lines: []string{rule, "❯ text", rule, "  ⏸ manual mode on · ? for shortcuts"},
			want:  aca.ScreenState{Activity: idle, InputBox: true},
		},
		{
			name:     "dim text",
			lines:    []string{rule, "❯ show the ping output", rule, "  ⏸ manual mode on · ? for shortcuts"},
			undimmed: []string{rule, "❯ ", rule, ""},
			want:     aca.ScreenState{Activity: idle, InputBox: true},
		},
		{
			name:     "typed text, footer cut off",
			lines:    []string{rule, "❯ hello", rule, "  ⏸ manual mode on"},
			undimmed: []string{rule, "❯ hello", rule, "  ⏸ manual mode on"},
			want:     aca.ScreenState{Activity: unknown, InputBox: true, Draft: true},
		},
		{
			name:     "a past message above an empty box",
			lines:    []string{"❯ earlier", "● reply", rule, "❯ ", rule, "  ⏸ manual mode on · ? for shortcuts"},
			undimmed: []string{"❯ earlier", "● reply", rule, "❯ ", rule, ""},
			want:     aca.ScreenState{Activity: idle, InputBox: true},
		},
		{
			name:     "text only on a later row of the box",
			lines:    []string{rule, "❯ ", "  two", rule, "  ⏸ manual mode on"},
			undimmed: []string{rule, "❯ ", "  two", rule, ""},
			want:     aca.ScreenState{Activity: unknown, InputBox: true, Draft: true},
		},
		{
			name:     "working, typed text",
			lines:    []string{rule, "❯ abc", rule, "  ⏸ manual mode on · 1 shell"},
			undimmed: []string{rule, "❯ abc", rule, "  ⏸ manual mode on · 1 shell"},
			want:     aca.ScreenState{Activity: working, InputBox: true, Draft: true},
		},
		{
			name:     "bash mode, nothing typed",
			lines:    []string{rule, "! ", rule, "  ⏸ manual mode on"},
			undimmed: []string{rule, "! ", rule, ""},
			want:     aca.ScreenState{Activity: unknown, InputBox: true},
		},
		{
			name:     "bash mode, typed",
			lines:    []string{rule, "! ls", rule, "  ⏸ manual mode on"},
			undimmed: []string{rule, "! ls", rule, ""},
			want:     aca.ScreenState{Activity: unknown, InputBox: true, Draft: true},
		},
		{
			name:     "an open prompt has none",
			lines:    []string{rule, " Do you want to proceed?", " ❯ 1. Yes", "", " Esc to cancel · Tab to amend"},
			undimmed: []string{rule, " Do you want to proceed?", " ❯ 1. Yes", "", " Esc to cancel · Tab to amend"},
			want:     aca.ScreenState{Activity: waiting},
		},
		{
			name:     "a numbered option is no box and no draft, whatever the footer",
			lines:    []string{rule, " ☐ Color", "❯ 1. Red", "  2. Blue", rule, "  ⏸ manual mode on · ? for shortcuts"},
			undimmed: []string{rule, " ☐ Color", "❯ 1. Red", "  2. Blue", rule, "  ⏸ manual mode on · ? for shortcuts"},
			want:     aca.ScreenState{Activity: unknown},
		},
		{
			name:     "a draft that starts with a number",
			lines:    []string{rule, "❯ 1. buy milk", rule, "  ⏸ manual mode on"},
			undimmed: []string{rule, "❯ 1. buy milk", rule, ""},
			want:     aca.ScreenState{Activity: unknown, InputBox: true, Draft: true},
		},
		{
			name:     "a draft taller than the classic screen: its box is gone, so unknown, never waiting",
			lines:    []string{"  line27", "  last", rule, "  ⏸ manual mode on", "             ctrl+g to edit in VS Code"},
			undimmed: []string{"  line27", "  last", rule, "  ⏸ manual mode on", "             ctrl+g to edit in VS Code"},
			want:     aca.ScreenState{Activity: unknown},
		},
		{
			name:     "the footer overrules text that is not dim",
			lines:    []string{rule, "❯\u00a0Try \"fix lint errors\"", rule, "  ⏸ manual mode on · ? for shortcuts · ← 1 agent"},
			undimmed: []string{rule, "❯\u00a0Try \"fix lint errors\"", rule, "  ⏸ manual mode on · ? for shortcuts · ← 1 agent"},
			want:     aca.ScreenState{Activity: idle, InputBox: true},
		},
		{
			name:     "the veto's words, cut off and with another count",
			lines:    []string{rule, "❯\u00a0text", rule, "  ⏸ manual mode on · 1 shell · ← 2 ag…"},
			undimmed: []string{rule, "❯\u00a0text", rule, "  ⏸ manual mode on · 1 shell · ← 2 ag…"},
			want:     aca.ScreenState{Activity: working, InputBox: true},
		},
		{
			name:     "a footer without the veto's words",
			lines:    []string{rule, "❯\u00a0text", rule, "  ⏵⏵ auto mode on (shift+tab to cycle)"},
			undimmed: []string{rule, "❯\u00a0text", rule, "  ⏵⏵ auto mode on (shift+tab to cycle)"},
			want:     aca.ScreenState{Activity: unknown, InputBox: true, Draft: true},
		},
		{
			name:     "no box has none",
			lines:    []string{"some output"},
			undimmed: []string{"some output"},
			want:     aca.ScreenState{Activity: unknown},
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

func readMarks(t *testing.T, dir string) []recordedStep {
	t.Helper()
	marks, err := os.ReadFile(filepath.Join(recordingDir(dir), "marks.log"))
	if err != nil {
		t.Fatal(err)
	}
	var steps []recordedStep
	for line := range strings.Lines(string(marks)) {
		at, name, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			t.Fatalf("marks line %q", line)
		}
		sec, err := strconv.ParseFloat(at, 64)
		if err != nil {
			t.Fatal(err)
		}
		steps = append(steps, recordedStep{seconds(sec), name})
	}
	return steps
}

// replayStyled is replay with the undimmed lines too.
func replayStyled(t *testing.T, dir string, cols, rows int, read func(at time.Duration, lines, undimmed []string)) {
	t.Helper()
	chunks, _ := readRecording(t, dir)
	s := screen.New(cols, rows, nil)
	defer s.Close()
	for i := 0; i < len(chunks); {
		at := chunks[i].at + moment
		for i < len(chunks) && chunks[i].at <= at {
			s.Write(chunks[i].data)
			i++
		}
		read(at, s.Text(), s.Undimmed())
	}
}
