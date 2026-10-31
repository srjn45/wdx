package aca_test

import (
	"bufio"
	"bytes"
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

const (
	working = aca.ActivityWorking
	idle    = aca.ActivityIdle
	waiting = aca.ActivityWaiting
	unknown = aca.ActivityUnknown
)

// moment is how long watchScreen waits after a screen change before it reads.
// The replays set it: a shorter wait sees the blank screen Claude Code starts
// from, a longer one starts to miss short prompts. docs/AGENT_STATE.md.
const moment = 50 * time.Millisecond

// inputBox stands in the tables for an unknown activity with an input box.
const inputBox aca.Activity = "unknown, with an input box"

var _ aca.ScreenParser = aca.ClaudeCodeScreenParser{}

// reading is what the rule table says of the lines.
func reading(lines []string) aca.Activity {
	state := aca.ClaudeCodeScreenParser{}.Parse(lines, nil)
	if state.Activity == unknown && state.InputBox {
		return inputBox
	}
	return state.Activity
}

// claudeCodeRecording is one real session of Claude Code 2.1.314, in
// testdata/claude_code/2.1.314. The classic- ones ran with
// CLAUDE_CODE_DISABLE_ALTERNATE_SCREEN=1; the others use the alternate screen.
type claudeCodeRecording struct {
	dir        string
	cols, rows int
	// screens gives what the rule table reads at every step in marks.log: a
	// moment in seconds when the screen had settled, and the step's name.
	screens map[string]aca.Activity
	// sequence is the readings a replay gives, repeats dropped.
	sequence []aca.Activity
}

var claudeCodeRecordings = []claudeCodeRecording{
	{
		dir: "prompts", cols: 120, rows: 40,
		screens: map[string]aca.Activity{
			"01_trust": waiting, "02_idle_start": idle, "03_typing": inputBox,
			"04_permission_bash": waiting, "05_permission_bash_moved": waiting, "06_after_deny": idle,
			"08_permission_write": waiting, "10_permission_ping": waiting, "11_working_tool": working,
			"12_after_interrupt": idle, "14_decision_radio": waiting, "15_radio_answered": idle,
			"17_decision_checkbox": waiting, "18_decision_checkbox_ticked": waiting, "19_after_checkbox_esc": idle,
			"21_working_stream": working, "22_after_stream_interrupt": idle, "24_idle_end": idle,
		},
		sequence: []aca.Activity{
			waiting, unknown, idle, inputBox, idle, inputBox, working, waiting, idle, inputBox, working, waiting,
			idle, inputBox, working, waiting, working, idle, inputBox, working, waiting, working, idle, inputBox,
			working, waiting, idle, inputBox, working, idle, inputBox, unknown,
		},
	},
	{
		dir: "background-auto-80x24", cols: 80, rows: 24,
		screens: backgroundAutoScreens,
		sequence: []aca.Activity{
			waiting, unknown, idle, inputBox, working, idle, inputBox, working, inputBox, idle, inputBox, unknown,
		},
	},
	{
		dir: "background-prompts", cols: 120, rows: 40,
		screens: backgroundPromptScreens("03_start_approval_2", "04_start_approval_3", "05_both_running_idle_box",
			"06_permission_while_bg", "07_after_deny_while_bg", "08_decision_while_bg", "09_decision_answered",
			"10_after_decision", "11_bg_ended", "12_idle_end"),
		sequence: []aca.Activity{
			waiting, unknown, idle, inputBox, working, waiting, working, waiting, working, waiting, working, waiting,
			working, idle, inputBox, unknown,
		},
	},
	{
		dir: "background-prompts-60x24", cols: 60, rows: 24,
		screens: backgroundPromptScreens("03_start_approval_2", "04_start_approval_3", "05_both_running_idle_box",
			"06_permission_while_bg", "07_after_deny_while_bg", "08_decision_while_bg", "09_decision_answered",
			"10_after_decision", "11_bg_ended", "12_idle_end"),
		sequence: []aca.Activity{
			waiting, unknown, idle, inputBox, working, waiting, working, waiting, working, waiting, working, waiting,
			working, inputBox, idle, inputBox, unknown,
		},
	},
	{
		dir: "classic-background-prompts", cols: 120, rows: 40,
		screens: backgroundPromptScreens("03_start_approval_2", "", "04_both_running_idle_box",
			"05_permission_while_bg", "06_after_deny_while_bg", "07_decision_while_bg", "08_decision_answered",
			"09_after_decision", "10_bg_ended", "11_idle_end"),
		sequence: []aca.Activity{
			waiting, unknown, idle, inputBox, working, waiting, working, waiting, working, waiting, working, waiting,
			working, idle, inputBox,
		},
	},
	{
		dir: "two-shells", cols: 120, rows: 40,
		screens: map[string]aca.Activity{
			"01_approval_1": waiting, "02_approval_2": waiting, "03_two_shells": working,
			"04_shells_ended": idle, "05_idle_after_shells": idle,
			"06_fg1_backgrounded": working, "07_fg1_approval_1": waiting, "08_fg1_approval_2": waiting,
			"09_fg1_after": working, "10_fg2_backgrounded": inputBox, "11_fg2_approval_1": waiting, "12_fg2_after": working,
		},
		sequence: []aca.Activity{
			waiting, unknown, idle, inputBox, working, waiting, working, waiting, working, idle, inputBox, working,
			waiting, working, idle, inputBox, working, waiting, working, idle, inputBox, unknown,
		},
	},
	{
		dir: "plan", cols: 120, rows: 40,
		screens: planScreens,
		sequence: []aca.Activity{
			waiting, unknown, idle, inputBox, working, waiting, idle, inputBox, unknown,
		},
	},
	{
		dir: "classic-plan", cols: 120, rows: 40,
		screens: planScreens,
		sequence: []aca.Activity{
			waiting, unknown, idle, inputBox, working, waiting, idle, inputBox,
		},
	},
	{
		dir: "narrow-40x24", cols: 40, rows: 24,
		screens: map[string]aca.Activity{
			"01_idle": idle, "02_typing_wrapped": inputBox, "03_working": working, "04_idle_after_story": idle,
			"05_permission": waiting, "06_after_deny": idle, "07_decision": waiting, "08_after_timeout": idle,
			"09_bg_shell_footer": idle, "10_bg_sub_early": working, "11_bg_sub_footer": working, "12_idle_end": inputBox,
		},
		sequence: []aca.Activity{
			waiting, unknown, idle, inputBox, working, idle, inputBox, working, waiting, idle, inputBox, working,
			waiting, working, idle, inputBox, working, inputBox, unknown,
		},
	},
}

var (
	backgroundAutoScreens = map[string]aca.Activity{
		"01_idle_nothing_running": idle, "02_bg_shell_after_turn": working, "03_bg_shell_ended": idle,
		"04_after_bg_shell": idle, "05_bg_subagent_after_turn": working, "06_bg_subagent_ended": idle,
		"07_after_bg_subagent": idle,
	}
	planScreens = map[string]aca.Activity{
		"01_idle_plan_mode": idle, "02_working_plan_mode": working, "03_plan_approval": waiting,
		"04_after_plan_esc": idle, "05_idle_end": idle,
	}
)

// backgroundPromptScreens names the screens of a session with a shell and a
// subagent in the background, whose prompts differ in number by the session.
func backgroundPromptScreens(approval2, approval3, running, permission, afterDeny, decision, answered, afterDecision, ended, end string) map[string]aca.Activity {
	screens := map[string]aca.Activity{
		"01_idle_nothing_running": idle, "02_start_approval_1": waiting, approval2: waiting,
		running: working, permission: waiting, afterDeny: working, decision: waiting, answered: working,
		afterDecision: working, ended: idle, end: idle,
	}
	if approval3 != "" {
		screens[approval3] = waiting
	}
	return screens
}

func TestClaudeCodeActivityOnRecordedScreens(t *testing.T) {
	t.Parallel()
	for _, rec := range claudeCodeRecordings {
		t.Run(rec.dir, func(t *testing.T) {
			t.Parallel()
			chunks, _ := readRecording(t, rec.dir)
			s := screen.New(rec.cols, rec.rows, nil)
			defer s.Close()
			steps := readMarks(t, rec.dir)
			if len(steps) != len(rec.screens) {
				t.Errorf("%d steps in marks.log, %d in the table", len(steps), len(rec.screens))
			}
			for _, step := range steps {
				for len(chunks) > 0 && chunks[0].at <= step.at {
					s.Write(chunks[0].data)
					chunks = chunks[1:]
				}
				want, ok := rec.screens[step.name]
				if !ok {
					t.Errorf("%s: not in the table", step.name)
					continue
				}
				lines := s.Text()
				if got := reading(lines); got != want {
					t.Errorf("%s: %s, want %s; bottom of the screen:\n%s", step.name, got, want, bottom(lines))
				}
			}
		})
	}
}

// TestClaudeCodeActivityReplay feeds each recording through the screen as the
// holder would, chunk by chunk at its recorded times, reading a moment after
// each change: the activities come in the right order, with no false flicker.
func TestClaudeCodeActivityReplay(t *testing.T) {
	t.Parallel()
	for _, rec := range claudeCodeRecordings {
		t.Run(rec.dir, func(t *testing.T) {
			t.Parallel()
			var got []aca.Activity
			replay(t, rec, func(_ time.Duration, lines []string) {
				a := reading(lines)
				if len(got) == 0 || got[len(got)-1] != a {
					got = append(got, a)
				}
			})
			if !slices.Equal(got, rec.sequence) {
				t.Errorf("activities:\n got %v\nwant %v", got, rec.sequence)
			}
		})
	}
}

// TestClaudeCodeActivityCutOffFooters holds the moments of the replays where
// Claude cut a footer that did not fit with an ellipsis, or dropped it to the
// mode while the box held text: the words are gone, so the table says so and
// reads idle only from words it sees.
func TestClaudeCodeActivityCutOffFooters(t *testing.T) {
	t.Parallel()
	cases := []struct {
		dir    string
		at     time.Duration
		footer string
		want   aca.Activity
	}{
		{"background-auto-80x24", 89 * time.Second, "⏵⏵ auto mode on (shift+tab to cycle) · /tasks to see subagents · esc to int…", inputBox},
		{"background-prompts-60x24", 112 * time.Second, "⏸ manual mode on · ? for shortcuts · ← 1 agent · ↓ to m…", working},
		{"background-prompts-60x24", 115 * time.Second, "⏸ manual mode on · /tasks to see subagents · esc to int…", inputBox},
		{"classic-motion-80x24", 25 * time.Second, "⏵⏵ auto mode on (shift+tab to cycle) · /tasks to see subagents · esc to int…", inputBox},
		{"narrow-40x24", 131500 * time.Millisecond, "⏸ manual mode on · ? for shortcuts …", working},
		{"motion-120x40", 117 * time.Second, "⏵⏵ auto mode on (shift+tab to cycle)", inputBox},
		{"draft-120x40", 88612 * time.Millisecond, "⏸ manual mode on", inputBox},
	}
	for _, c := range cases {
		t.Run(c.dir, func(t *testing.T) {
			t.Parallel()
			var seen []string
			cols, rows := recordingSize(c.dir)
			replay(t, claudeCodeRecording{dir: c.dir, cols: cols, rows: rows}, func(at time.Duration, lines []string) {
				if at <= c.at {
					seen = lines
				}
			})
			if !slices.ContainsFunc(seen, func(l string) bool { return strings.TrimSpace(l) == c.footer }) {
				t.Fatalf("footer %q not on the screen at %s:\n%s", c.footer, c.at, bottom(seen))
			}
			if got := reading(seen); got != c.want {
				t.Errorf("%s, want %s; bottom of the screen:\n%s", got, c.want, bottom(seen))
			}
		})
	}
}

// TestClaudeCodeActivityFalseMatches: the words the rule looks for count only
// at the bottom of the screen, in the footer. Parse leaves the lines as it
// found them.
func TestClaudeCodeActivityFalseMatches(t *testing.T) {
	t.Parallel()
	const rule = "────────────────"
	box := func(above []string, footer ...string) []string {
		lines := append(slices.Clone(above), rule, "❯\u00a0", rule)
		return append(lines, footer...)
	}
	cases := []struct {
		name  string
		lines []string
		want  aca.Activity
	}{
		{"no screen", nil, unknown},
		{"blank screen", []string{"", "  "}, unknown},
		{"output, no input box", []string{"some output", "more"}, unknown},
		{"a rule, no prompt", []string{rule, "text", rule, "  footer"}, unknown},
		{"only one rule", []string{"❯ text", rule, "  footer"}, unknown},
		{"a bare box", box(nil), inputBox},
		{"prompt on past messages", box([]string{"❯ earlier", "● reply", "❯ later", "● reply"}, "  ⏸ manual mode on · ? for shortcuts · ← 1 agent"), idle},
		{"reply asks do you want", box([]string{"● Do you want me to go on?", " ❯ 1. Yes", "   2. No"}, "  ⏸ manual mode on · ? for shortcuts · ← 1 agent"), idle},
		{"reply quotes the prompt's footer", box([]string{"● It printed Esc to cancel · Tab to amend"}, "  ⏸ manual mode on · ? for shortcuts · ← 1 agent"), idle},
		{"reply quotes the working footer", box([]string{"● The footer says esc to interrupt and 1 shell, ↓ to manage"}, "  ⏸ manual mode on · ? for shortcuts · ← 1 agent"), idle},
		{"ran a shell, past", box([]string{"  Ran 1 shell command"}, "  ⏸ manual mode on · ? for shortcuts · ← 1 agent"), idle},
		{"always an agent", box(nil, "  ⏸ manual mode on · ← 1 agent"), idle},
		{"subagents finished", box(nil, "  ⏸ manual mode on · /tasks to see subagents · ← 1 agent"), idle},
		{"esc to interrupt", box(nil, "  ⏸ manual mode on · esc to interrupt · ← 1 agent"), working},
		{"one shell", box(nil, "  ⏸ manual mode on · 1 shell · ← 1 agent · ↓ to manage"), working},
		{"two shells", box(nil, "  ⏸ manual mode on · 2 shells · ← 1 agent · ↓ to manage"), working},
		{"ten shells", box(nil, "  ⏸ manual mode on · 10 shells"), working},
		{"a subagent, panel below", box(nil, "  ⏸ manual mode on · ← 1 agent · ↓ to manage", "", "  ● main", "  ◯ general-purpose  Run 8s"), working},
		{"a subagent, footer cut off", box(nil, "  ⏸ manual mode on · ? for shortcuts …", "", "  ● main", "  ◯ general-purpose  2s"), working},
		{"a panel's words are not the footer", box(nil, "  ⏸ manual mode on · ? for shortcuts", "", "  ● main"), idle},
		{"prompt over a box's rules", []string{rule, "❯ past", rule, " Do you want to proceed?", " ❯ 1. Yes", "", " Esc to cancel · Tab to amend"}, waiting},
		{"trailing blank lines", []string{" Enter to confirm · Esc to cancel", "", ""}, waiting},
		{"case matters", box(nil, "  ⏸ manual mode on · Esc to interrupt · ? for shortcuts"), idle},
		{"a prompt's footer wrapped", []string{" Enter to select · ↑/↓ to navigate · Esc", "to cancel"}, waiting},
		{"a footer cut off", box(nil, "  ⏸ manual mode on · /tasks to see su…"), inputBox},
		{"the mode alone, as while typing", box(nil, "  ⏵⏵ auto mode on (shift+tab to cycle)"), inputBox},
		{"plan approval", []string{"  ──────", "   ❯ 1. Yes", "   ctrl+g to edit in VS Code · ~/.claude/plans/plan-creat", "   e-hi.md"}, waiting},
		{"a draft quotes plan approval's footer", []string{rule, "❯\u00a0ctrl+g to edit in", rule, "  ⏵⏵ auto mode on (shift+tab to cycle)"}, inputBox},
		{"a multi-line draft's footer", []string{rule, "❯\u00a0one", "  two", rule, "  ⏸ manual mode on", "             ctrl+g to edit in VS Code"}, inputBox},
		{"a numbered option is no box, whatever the footer", []string{rule, " ☐ Color", "❯ 1. Red", "  2. Blue", rule, "  ⏸ manual mode on · ? for shortcuts"}, unknown},
		{"a draft that starts with a number is", []string{rule, "❯\u00a01. buy milk", rule, "  ⏸ manual mode on"}, inputBox},
		{"bash mode's box", []string{rule, "! ls", rule, "  ⏸ manual mode on"}, inputBox},
		{"a draft quotes a prompt's footer", []string{rule, "❯\u00a0Esc to cancel", rule, "  ⏵⏵ auto mode on (shift+tab to cycle)"}, inputBox},
		{"Esc to cancel above the last two lines", []string{" Esc to cancel", "output", "more"}, unknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			lines := slices.Clone(c.lines)
			if got := reading(lines); got != c.want {
				t.Errorf("%s, want %s", got, c.want)
			}
			if !slices.Equal(lines, c.lines) {
				t.Errorf("Parse changed its lines: %q", lines)
			}
		})
	}
}

func BenchmarkClaudeCodeScreen(b *testing.B) {
	chunks, _ := readRecording(b, "background-prompts")
	s := screen.New(120, 40, nil)
	defer s.Close()
	for _, c := range chunks[:len(chunks)/2] {
		s.Write(c.data)
	}
	parser := aca.ClaudeCodeScreenParser{}
	b.Run("parse", func(b *testing.B) {
		lines, undimmed := s.Text(), s.Undimmed()
		b.ReportAllocs()
		for b.Loop() {
			parser.Parse(lines, undimmed)
		}
	})
	b.Run("read and parse", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			parser.Parse(s.Text(), s.Undimmed())
		}
	})
}

// recordingSize is the size in a recording's name, or the 120x40 of those
// without one.
func recordingSize(dir string) (cols, rows int) {
	if _, err := fmt.Sscanf(dir[strings.LastIndex(dir, "-")+1:], "%dx%d", &cols, &rows); err != nil {
		return 120, 40
	}
	return cols, rows
}

func recordingDir(dir string) string {
	return filepath.Join("testdata", "claude_code", "2.1.314", dir)
}

func bottom(lines []string) string {
	return strings.Join(lines[max(0, len(lines)-8):], "\n")
}

type recordedChunk struct {
	at   time.Duration
	data []byte
}

// readRecording reads what script(1) logged: raw.out, the bytes, and
// raw.timing, a line per read with the seconds since the last and its length.
// The recordings with input times mark each line O, output, or I, input.
func readRecording(tb testing.TB, dir string) (chunks []recordedChunk, inputs []time.Duration) {
	tb.Helper()
	raw, err := os.ReadFile(filepath.Join(recordingDir(dir), "raw.out"))
	if err != nil {
		tb.Fatal(err)
	}
	timing, err := os.ReadFile(filepath.Join(recordingDir(dir), "raw.timing"))
	if err != nil {
		tb.Fatal(err)
	}
	var at time.Duration
	lines := bufio.NewScanner(bytes.NewReader(timing))
	for lines.Scan() {
		fields := strings.Fields(lines.Text())
		kind := "O"
		if len(fields) == 3 {
			kind, fields = fields[0], fields[1:]
		}
		if len(fields) != 2 {
			tb.Fatalf("timing line %q", lines.Text())
		}
		delay, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			tb.Fatal(err)
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			tb.Fatal(err)
		}
		at += time.Duration(delay * float64(time.Second))
		if kind == "I" {
			inputs = append(inputs, at)
			continue
		}
		chunks = append(chunks, recordedChunk{at: at, data: raw[:n]})
		raw = raw[n:]
	}
	if len(raw) != 0 {
		tb.Fatalf("%d bytes of %s have no timing", len(raw), dir)
	}
	return chunks, inputs
}

// replay writes a recording to a screen in order, and calls read as
// watchScreen would: a moment after a change, with what has arrived by then.
// The clock is the recording's, so a run takes no time and depends on none.
func replay(t *testing.T, rec claudeCodeRecording, read func(at time.Duration, lines []string)) {
	t.Helper()
	chunks, _ := readRecording(t, rec.dir)
	s := screen.New(rec.cols, rec.rows, nil)
	defer s.Close()
	for i := 0; i < len(chunks); {
		at := chunks[i].at + moment
		for i < len(chunks) && chunks[i].at <= at {
			if _, reset := s.Write(chunks[i].data); reset {
				t.Fatalf("the vt reset at %s", chunks[i].at)
			}
			i++
		}
		read(at, s.Text())
	}
}
