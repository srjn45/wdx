package aca_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
)

// remembered is the model a reader over time knows after each moment of a replay,
// as the holder's watchScreen keeps it, repeats dropped; it starts with "", as
// nothing says it at first.
func remembered(moments []screenRead) []string {
	names := []string{""}
	for _, m := range moments {
		if model := (aca.ClaudeCodeScreenParser{}).Parse(m.lines, m.undimmed).Model; model != "" && model != names[len(names)-1] {
			names = append(names, model)
		}
	}
	return names
}

// TestClaudeCodeModelOnRecordings replays real sessions of /model, where nothing
// but the sequence of names read matters: every change is seen, and nothing else
// on the screen is taken for one.
func TestClaudeCodeModelOnRecordings(t *testing.T) {
	t.Parallel()
	const haiku, haiku45, sonnet, opus = "Haiku 5.5", "Haiku 4.5", "Sonnet 5.5", "Opus 5.5"
	for _, c := range []struct {
		dir  string
		want []string
	}{
		// The menu, an argument, an unknown name, a turn, /clear and /compact.
		{filepath.Join("..", "2.1.316", "model-120x40"), []string{"", haiku, haiku45, sonnet, haiku, sonnet}},
		{filepath.Join("..", "2.1.316", "model-fullscreen-120x40"), []string{"", haiku, haiku45, sonnet, haiku, sonnet}},
		// Session only, /model default, and /model opus[1m], whose banner stays.
		{filepath.Join("..", "2.1.316", "model-args-120x40"), []string{"", haiku, haiku45, sonnet, opus, sonnet}},
		{filepath.Join("..", "2.1.316", "model-args-30x24"), []string{"", haiku, haiku45, sonnet, opus, sonnet}},
		// Scrolled back over the older names and to the top, and the transcript view.
		{filepath.Join("..", "2.1.316", "model-scrolled-40x24"), []string{"", haiku, sonnet, haiku, sonnet, haiku, sonnet, haiku, opus}},
		// Typing /model, whose suggestions cover the newest name, and a tall draft.
		{filepath.Join("..", "2.1.316", "model-typing-40x24"), []string{"", haiku, sonnet, opus}},
		// A resume: the banner goes by in moments, then what the old session said.
		{filepath.Join("..", "2.1.316", "model-resume-40x24"), []string{"", haiku}},
		{filepath.Join("..", "2.1.316", "model-resume-120x40"), []string{"", sonnet}},
		// A reply of the one word "Haiku", and no model named anywhere else.
		{"background-auto-80x24", []string{"", haiku}},
		// No /model: the classic renderer, the alternate screen, narrow, no color.
		{"prompts", []string{"", haiku}},
		{"classic-background-prompts", []string{"", haiku}},
		{"narrow-40x24", []string{"", haiku}},
		{"no-color-120x40", []string{"", haiku}},
		{"no-color-40x24", []string{"", haiku}},
		{"no-color-30x24", []string{"", haiku}},
		{"no-color-auto-40x24", []string{"", haiku}},
	} {
		t.Run(filepath.Base(c.dir), func(t *testing.T) {
			t.Parallel()
			if got := remembered(replayed(t, c.dir).moments); !slices.Equal(got, c.want) {
				t.Errorf("models read: %q, want %q", got, c.want)
			}
		})
	}
}

func TestClaudeCodeModel(t *testing.T) {
	t.Parallel()
	const rule, footer = "────────────", "  ⏸ manual mode on · ? for shortcuts"
	box := []string{rule, "❯ ", rule, footer}
	banner := []string{" ▐▛███▛█   Claude Code v2.1.316", "▝▜██████▀  Haiku 5.5 · Claude Max", " ▝▝   ▝▝   ~/dev"}
	for _, c := range []struct {
		name  string
		above []string
		want  string
	}{
		{"nothing says", []string{"● Done"}, ""},
		{"the banner", banner, "Haiku 5.5"},
		{"the banner on a narrow screen", []string{"           Claude Code", " ▐▛███▛█   v2.1.316", "▝▜██████▀  Haiku 5.5", " ▝▝   ▝▝   Claude Max"}, "Haiku 5.5"},
		{"the banner cut off", []string{" ▐▛███▛█   Claude Code v2.1.316", "▝▜██████▀  Haiku 5.5 · Claude…"}, "Haiku 5.5"},
		{"the banner of a whole number", []string{" ▐▛███▛█   Claude Code v2.1.316", "▝▜██████▀  Fable 5 · Claude Max"}, "Fable 5"},
		{"the banner with its context", []string{" ▐▛███▛█   Claude Code v2.1.316", "▝▜██████▀  Opus 5.5 (1M context) · Claude Max"}, "Opus 5.5"},
		{"a banner's row alone", []string{"● A reply", "▝▜██████▀  Haiku 5.5 · Claude Max"}, ""},
		{"a banner's name of another shape", []string{" ▐▛███▛█   Claude Code v2.1.316", "▝▜██████▀  rm -rf / · Claude Max"}, ""},
		{"set", []string{"❯ /model sonnet", "  ⎿  Set model to Sonnet 5.5 and saved as your default for new sessions"}, "Sonnet 5.5"},
		{"set by the menu", []string{"❯ /model", "  ⎿  Set model to Haiku 4.5 for this session only"}, "Haiku 4.5"},
		{"set to the default", []string{"❯ /model default", "  ⎿  Set model to Sonnet 5.5 (default) and saved as"}, "Sonnet 5.5"},
		{"set, wrapped", []string{"❯ /model sonnet", "  ⎿ Set model to Sonnet 5.5", "    and saved as your default"}, "Sonnet 5.5"},
		{"kept", []string{"❯ /model", "  ⎿  Kept model as Haiku 5.5"}, "Haiku 5.5"},
		{"a name a wrap cut", []string{"❯ /model sonnet", "  ⎿  Set model to Sonnet", "     5.5 and saved"}, ""},
		{"a confirmation under no /model", []string{"❯ cat notes", "  ⎿  Set model to Opus 5.5 and saved"}, ""},
		{"a command starting the same", []string{"❯ /modelling", "  ⎿  Set model to Opus 5.5 and saved"}, ""},
		{"a reply naming it", []string{"● Set model to Opus 5.5 and saved"}, ""},
		{"not found", []string{"❯ /model nonsense", "  ⎿  Model 'nonsense' not found"}, ""},
		{"the lowest wins", append(slices.Clone(banner), "❯ /model sonnet", "  ⎿  Set model to Sonnet 5.5 and saved", "❯ /model opus", "  ⎿  Set model to Opus 5.5 and saved"), "Opus 5.5"},
		{"scrolled back", []string{"❯ /model sonnet", "  ⎿  Set model to Sonnet 5.5 and saved", "❯ /mod Jump to bottom (ctrl+End) ↓"}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			lines := append(slices.Clone(c.above), box...)
			if got := (aca.ClaudeCodeScreenParser{}).Parse(lines, lines).Model; got != c.want {
				t.Errorf("read %q, want %q", got, c.want)
			}
		})
	}
	t.Run("with no input box", func(t *testing.T) {
		t.Parallel()
		lines := append(slices.Clone(banner), rule, "  Showing detailed transcript · ctrl+o to toggle")
		if got := (aca.ClaudeCodeScreenParser{}).Parse(lines, lines).Model; got != "" {
			t.Errorf("read %q", got)
		}
	})
	t.Run("at a prompt", func(t *testing.T) {
		t.Parallel()
		lines := append(slices.Clone(banner), rule, "  Select model", "  ❯ 5.  Haiku 5.5 ✔", "  Enter to set as default · Esc to cancel")
		if got := (aca.ClaudeCodeScreenParser{}).Parse(lines, lines).Model; got != "" {
			t.Errorf("read %q", got)
		}
	})
	t.Run("with a draft", func(t *testing.T) {
		t.Parallel()
		lines := []string{"❯ /model sonnet", "  ⎿  Set model to Sonnet 5.5 and saved", "  ❯ /model   Set the AI model", rule, "❯\u00a0/model", rule, "  ⏸ manual mode on"}
		if got := (aca.ClaudeCodeScreenParser{}).Parse(lines, lines).Model; got != "" {
			t.Errorf("suggestions over the newest name, and read %q", got)
		}
	})
	t.Run("in the input box", func(t *testing.T) {
		t.Parallel()
		lines := []string{rule, "❯ /model", "  ⎿  Set model to Opus 5.5 and saved", rule, footer}
		if got := (aca.ClaudeCodeScreenParser{}).Parse(lines, lines).Model; got != "" {
			t.Errorf("a draft read as the model: %q", got)
		}
	})
}
