package aca_test

import (
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

// draftTextAtStep is the text of a draft each ACA's recordings show, by
// recording and step: one typed, one of several lines, and a long paste as the
// ACA renders it, where a recording has one.
var draftTextAtStep = []struct {
	parser aca.ScreenParser
	dir    string
	steps  map[string]string
}{
	{aca.ClaudeCodeScreenParser{}, "draft-120x40", map[string]string{
		"typed_idle":   "hello there",
		"multi_line":   "one\ntwo\nthree",
		"pasted_draft": "[Pasted text #1 +11 lines]",
		// What Claude dims there itself is left out.
		"typed_dim_words": `Try "create a util"`,
		"bash_typed":      "! ls",
		"typed_turn":      "abc",
	}},
	{aca.ClaudeCodeScreenParser{}, "classic-draft-40x24", map[string]string{
		"pasted_draft": "[Pasted text #1 +11 lines]",
	}},
	{aca.ClaudeCodeScreenParser{}, "suggestion-120x40", map[string]string{
		// Only the box's own mark goes.
		"draft_with_prompt_mark": "❯ quoted",
		"tab_accepted":           "Say hi in one word.",
	}},
	{aca.CodexScreenParser{}, codexRecordingDir("ready-120x40"), map[string]string{
		"draft":            "hello draft",
		"multi_line_draft": "line one\nline two\nline three",
		"shell_mode":       "! ls",
	}},
	{aca.CursorScreenParser{}, cursorRecordingDir("ready-40x24"), map[string]string{
		"draft":           "hello there",
		"draft_multiline": "first line\nsecond line\nthird line",
		// The box shows the paste's last lines.
		"draft_pasted": "pasted line 34\npasted line 35\npasted line 36\npasted line 37\npasted line 38\npasted line 39",
		"turn_draft":   "typed during the turn",
	}},
	{aca.AntigravityScreenParser{}, antigravityRecordingDir("turns-40x24"), map[string]string{
		"draft":       "a draft",
		"draft_multi": "line one\nline two",
	}},
}

func TestDraftTextOnRecordings(t *testing.T) {
	t.Parallel()
	for _, rec := range draftTextAtStep {
		t.Run(rec.dir, func(t *testing.T) {
			t.Parallel()
			seen := map[string]bool{}
			for _, step := range replayed(t, rec.dir).steps {
				want, ok := rec.steps[step.name]
				if !ok {
					continue
				}
				seen[step.name] = true
				if got := rec.parser.Parse(step.lines, step.undimmed); !got.Draft || got.DraftText != want {
					t.Errorf("%s: draft %v, text %q, want %q\n%s", step.name, got.Draft, got.DraftText, want, bottom(step.lines))
				}
			}
			for name := range rec.steps {
				if !seen[name] {
					t.Errorf("no step %s", name)
				}
			}
		})
	}
}

// TestDraftTextOnlyWithDraft reads every moment of every ACA's recordings: a
// screen with no draft has no text.
func TestDraftTextOnlyWithDraft(t *testing.T) {
	t.Parallel()
	var recordings []struct {
		parser aca.ScreenParser
		dir    string
	}
	add := func(parser aca.ScreenParser, dir string) {
		recordings = append(recordings, struct {
			parser aca.ScreenParser
			dir    string
		}{parser, dir})
	}
	for _, rec := range draftRecordings {
		add(aca.ClaudeCodeScreenParser{}, rec.dir)
	}
	for _, dir := range codexRecordings {
		add(aca.CodexScreenParser{}, codexRecordingDir(dir))
	}
	for _, dir := range cursorRecordings {
		add(aca.CursorScreenParser{}, cursorRecordingDir(dir))
	}
	for _, dir := range antigravityRecordings {
		add(aca.AntigravityScreenParser{}, antigravityRecordingDir(dir))
	}
	for _, rec := range recordings {
		t.Run(rec.dir, func(t *testing.T) {
			t.Parallel()
			drafts := 0
			for _, m := range replayed(t, rec.dir).moments {
				got := rec.parser.Parse(m.lines, m.undimmed)
				if got.Draft {
					drafts++
				} else if got.DraftText != "" {
					t.Errorf("at %s: no draft, but text %q\n%s", m.at, got.DraftText, bottom(m.lines))
				}
			}
			t.Logf("%d moments with a draft", drafts)
		})
	}
}

func TestClaudeCodeDraftText(t *testing.T) {
	t.Parallel()
	const rule = "────────────────"
	for _, c := range []struct {
		name            string
		lines, undimmed []string
		draft           bool
		want            string
	}{
		{
			// As the holder's spool of agent-d471b867 showed it on 2026-11-09.
			name:     "a long paste",
			lines:    []string{rule, "❯ [Pasted text #1 +58 lines]", rule, "  paste again to expand                 ◐ medium · /effort"},
			undimmed: []string{rule, "❯ [Pasted text #1 +58 lines]", rule, ""},
			draft:    true,
			want:     "[Pasted text #1 +58 lines]",
		},
		{
			name:     "lines of the box, indented and with blank ones at the end",
			lines:    []string{rule, "❯ one ", "  two", "   ", rule, "  ⏸ manual mode on"},
			undimmed: []string{rule, "❯ one ", "  two", "   ", rule, ""},
			draft:    true,
			want:     "one\ntwo",
		},
		{
			name:     "a blank line inside stays",
			lines:    []string{rule, "❯ one", "", "  three", rule, "  ⏸ manual mode on"},
			undimmed: []string{rule, "❯ one", "", "  three", rule, ""},
			draft:    true,
			want:     "one\n\nthree",
		},
		{
			name:     "a dim placeholder is no draft",
			lines:    []string{rule, "❯ Try \"fix lint errors\"", rule, "  ⏸ manual mode on"},
			undimmed: []string{rule, "❯ ", rule, ""},
			want:     "",
		},
		{
			name:     "an empty box",
			lines:    []string{rule, "❯ ", rule, "  ⏸ manual mode on · ? for shortcuts"},
			undimmed: []string{rule, "❯ ", rule, ""},
			want:     "",
		},
		{
			// A draft whatever the styling, but with nothing to read the text from.
			name:  "bash mode with no undimmed lines",
			lines: []string{rule, "! ls", rule, "  ⏸ manual mode on"},
			draft: true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := aca.ClaudeCodeScreenParser{}.Parse(c.lines, c.undimmed)
			if got.Draft != c.draft || got.DraftText != c.want {
				t.Errorf("%+v, want draft %v, text %q", got, c.draft, c.want)
			}
		})
	}
}
