package aca_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/screen"
)

// cursorRecordings were made by testdata/cursor/record/ready.py, which records the
// box, and turns.py, which records turns and, in ask mode, an approval: in
// 2026.11.05 without a status line, Cursor drawing its own footer, and in
// 2026.11.05-wdx-status under the status line wdx pins, which prints that footer,
// in ready-user-120x40 with the user's own status line under it (the wdx doc
// CURSOR_FOOTER).
var cursorRecordings = []string{
	"2026.11.05/ready-120x40", "2026.11.05/ready-40x24", "2026.11.05/turns-120x40", "2026.11.05/turns-40x24",
	"2026.11.05/approval-120x40",
	"2026.11.05-wdx-status/ready-120x40", "2026.11.05-wdx-status/ready-40x24", "2026.11.05-wdx-status/ready-user-120x40",
	"2026.11.05-wdx-status/turns-120x40", "2026.11.05-wdx-status/turns-40x24",
	"2026.11.05-wdx-status/approval-120x40", "2026.11.05-wdx-status/approval-40x24",
}

// cursorStep is what one step of those scripts left on Cursor's screen: whether a
// message can go in, whether the box holds what a human put there, what Cursor was
// doing, as its screen says, and whether a follow-up shows steering.
type cursorStep struct {
	inputBox, draft bool
	activity        screen.Activity
	inTurn          bool
	steerPending    bool
}

var (
	cursorIdle      = cursorStep{inputBox: true, activity: screen.ActivityIdle}
	cursorDraft     = cursorStep{inputBox: true, draft: true, activity: screen.ActivityIdle}
	cursorInTurn    = cursorStep{inputBox: true, activity: screen.ActivityWorking, inTurn: true}
	cursorNoBox     = cursorStep{activity: screen.ActivityUnknown}
	cursorWaitingOn = cursorStep{activity: screen.ActivityWaiting}
)

var cursorAtStep = map[string]cursorStep{
	"loading":         cursorNoBox,
	"ready":           cursorIdle,
	"draft":           cursorDraft,
	"cleared":         cursorIdle,
	"draft_multiline": cursorDraft,
	"draft_pasted":    cursorDraft,
	"cleared2":        cursorIdle,
	// Shell mode runs what the box holds, so it is never a place for a message.
	"shell_mode":  cursorDraft,
	"shell_draft": cursorDraft,
	// The & had not shown yet.
	"cloud_mode": cursorIdle,
	// The menu takes the footer's place.
	"slash_menu": cursorNoBox,
	"after_menu": cursorIdle,
	"turn_tool":  cursorInTurn,
	// The draft takes ctrl+c to stop off the box's line; the spinner says it.
	"turn_draft":    {inputBox: true, draft: true, activity: screen.ActivityWorking, inTurn: true},
	"after_turn":    cursorIdle,
	"exit_armed":    cursorIdle,
	"exit_disarmed": cursorIdle,

	"started":      cursorIdle,
	"tool_running": cursorInTurn,
	"queued":       cursorInTurn,
	"after_queue":  cursorIdle,
	"steered":      cursorInTurn,
	// The turn ended, but the command it started runs on.
	"idle_with_task":      {inputBox: true, activity: screen.ActivityWorking},
	"task_finished_wakes": cursorInTurn,
	"after_wake":          cursorIdle,
	"streaming":           cursorInTurn,
	// The follow-up shows steering until the stream ends.
	"steer_pending": {inputBox: true, activity: screen.ActivityWorking, inTurn: true, steerPending: true},
	// Press Ctrl+C again to exit takes the footer's place, and then the turn's
	// message is back in the box.
	"interrupted":       cursorNoBox,
	"interrupted_later": {inputBox: true, draft: true, activity: screen.ActivityIdle, steerPending: true},
	// The follow-up left steering by Ctrl+C stays so through the next turn, which
	// takes it at its end.
	"subagent":       {inputBox: true, activity: screen.ActivityWorking, inTurn: true, steerPending: true},
	"after_subagent": cursorIdle,

	"approval":     cursorWaitingOn,
	"denied_tell":  cursorWaitingOn,
	"after_denial": cursorIdle,
}

// cursorAtRecordedStep is a step whose screen differs between recordings.
var cursorAtRecordedStep = map[string]cursorStep{
	"2026.11.05/ready-120x40/after_turn": cursorDraft,
	// At 40x24 the mark came while Press Ctrl+C again to exit showed.
	"2026.11.05/ready-40x24/exit_armed": cursorNoBox,
	// Under wdx's status line the & showed, and the mark came while Press Ctrl+C
	// again to exit showed.
	"2026.11.05-wdx-status/ready-120x40/cloud_mode":      cursorDraft,
	"2026.11.05-wdx-status/ready-40x24/cloud_mode":       cursorDraft,
	"2026.11.05-wdx-status/ready-user-120x40/cloud_mode": cursorDraft,
	"2026.11.05-wdx-status/ready-120x40/exit_armed":      cursorNoBox,
	"2026.11.05-wdx-status/ready-40x24/exit_armed":       cursorNoBox,
	"2026.11.05-wdx-status/ready-user-120x40/exit_armed": cursorNoBox,
	// At 40x24 the empty answer to the tell box had the agent ask again, which the
	// script waited out to its timeout.
	"2026.11.05-wdx-status/approval-40x24/timeout":      cursorWaitingOn,
	"2026.11.05-wdx-status/approval-40x24/after_denial": cursorWaitingOn,
}

// cursorBeforeFirstTurn are the steps whose status line names the model alone, as
// before the first turn it has no " · " to tell the model from the mode.
var cursorBeforeFirstTurn = map[string]bool{
	"ready": true, "draft": true, "cleared": true, "draft_multiline": true, "draft_pasted": true, "cleared2": true,
	"shell_mode": true, "shell_draft": true, "cloud_mode": true, "after_menu": true, "started": true,
}

func cursorRecordingDir(dir string) string {
	return filepath.Join("..", "..", "cursor", dir)
}

func TestCursorOnRecordedScreens(t *testing.T) {
	t.Parallel()
	for _, dir := range cursorRecordings {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			for _, step := range replayed(t, cursorRecordingDir(dir)).steps {
				want, ok := cursorAtRecordedStep[dir+"/"+step.name]
				if !ok {
					want, ok = cursorAtStep[step.name]
				}
				if !ok {
					t.Fatalf("step %s is not in the table", step.name)
				}
				got := aca.CursorScreenParser{}.Parse(step.lines, step.undimmed)
				if got.InputBox != want.inputBox || got.Draft != want.draft || got.Activity != want.activity || got.InTurn != want.inTurn || got.SteerPending != want.steerPending {
					t.Errorf("%s: input box %v, draft %v, %s, in turn %v, steer pending %v; want %v, %v, %s, %v, %v\n%s", step.name,
						got.InputBox, got.Draft, got.Activity, got.InTurn, got.SteerPending,
						want.inputBox, want.draft, want.activity, want.inTurn, want.steerPending, bottom(step.lines))
				}
				// At 40 columns Cursor's own status line wraps, with the mode at its
				// right, and the model is not read; wdx's has no mode.
				wantModel := ""
				if want.inputBox && dir != "2026.11.05/ready-40x24" && dir != "2026.11.05/turns-40x24" && !cursorBeforeFirstTurn[step.name] {
					wantModel = "Gemini 3.8 Flash Low"
				}
				if got.Model != wantModel {
					t.Errorf("%s: model %q, want %q\n%s", step.name, got.Model, wantModel, bottom(step.lines))
				}
			}
		})
	}
}

// TestCursorNotReadyWhileLoading checks every read a holder makes before the
// status line first shows: a message delivered then loses its Enter.
func TestCursorNotReadyWhileLoading(t *testing.T) {
	t.Parallel()
	for _, dir := range []string{"2026.11.05/ready-120x40", "2026.11.05/ready-40x24", "2026.11.05-wdx-status/ready-120x40", "2026.11.05-wdx-status/ready-40x24", "2026.11.05-wdx-status/ready-user-120x40"} {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			rep := replayed(t, cursorRecordingDir(dir))
			loading := 0
			for _, m := range rep.moments {
				status := false
				for _, line := range m.lines {
					// The recordings ran on this model.
					status = status || strings.Contains(line, "Gemini 3.8")
				}
				if status {
					break
				}
				loading++
				if got := (aca.CursorScreenParser{}).Parse(m.lines, m.undimmed); got.InputBox {
					t.Errorf("at %s, before the status line shows, the box reads ready\n%s", m.at, bottom(m.lines))
				}
			}
			if loading == 0 {
				t.Error("no read before the status line: the recording checks nothing")
			}
		})
	}
}

func TestCursorModel(t *testing.T) {
	t.Parallel()
	for status, want := range map[string]string{
		"  Gemini 3.8 Flash Low · 0.8%                Run Everything": "Gemini 3.8 Flash Low",
		"  Gemini 3.8 Flash Low · 0.9% · 1 file edited":               "Gemini 3.8 Flash Low",
		"  Grok 4.7  Low · 1.2%                       Run Everything": "Grok 4.7 Low",
		"  Gemini 3.8 Flash Low                       Run Everything": "",
		"  Gemini 3.8 Flash Low":                                      "",
	} {
		lines := []string{"  → Add a follow-up", "", "", status, "  ~/w/repo · main"}
		got := aca.CursorScreenParser{}.Parse(lines, []string{"    A", "", "", "", ""})
		if !got.InputBox || got.Model != want {
			t.Errorf("status line %q: input box %v, model %q, want true, %q", status, got.InputBox, got.Model, want)
		}
	}
}

// A directory too long for the screen wraps onto lines that do not start with a
// path, as on cursor-agent 2026.11.05 run from a long scratch folder: the box is
// still ready, and the status line above it, whole, names the model. A status line
// that wrapped too names none.
func TestCursorWrappedDirectory(t *testing.T) {
	t.Parallel()
	status := "  Gemini 3.8 Flash Low · 0.9%                                Run Everything"
	wrappedStatus := []string{"  Gemini 3.8     · 0.8  Run Everything", "  Flash Low        %"}
	for _, c := range []struct {
		name   string
		footer []string
		model  string
	}{
		{"wrapped once", []string{status, "  /tmp/claude-1000/scratchpad/e2e/", "  proj · master"}, "Gemini 3.8 Flash Low"},
		{"wrapped twice", []string{status, "  ~/dev/wdx/.worktrees/a-long-name/", "  internal/aca/testdata/cursor/", "  proj · master"}, "Gemini 3.8 Flash Low"},
		{"status wrapped too", append(wrappedStatus, "  ~/dev/wdx/.worktrees/a-long-name/", "  proj · master"), ""},
	} {
		lines := append([]string{"  DONE1", "", "", "  → Add a follow-up", "", ""}, c.footer...)
		undimmed := make([]string, len(lines))
		undimmed[3] = "    A"
		got := aca.CursorScreenParser{}.Parse(lines, undimmed)
		if !got.InputBox || got.Draft || got.Activity != screen.ActivityIdle || got.Model != c.model {
			t.Errorf("%s: input box %v, draft %v, %s, model %q; want true, false, idle, %q", c.name, got.InputBox, got.Draft, got.Activity, got.Model, c.model)
		}
	}
}

// TestCursorInTurnOnTheBoxLine: "ctrl+c to stop" on the box's line says a turn
// without the spinner, which the recordings always show with it.
func TestCursorInTurnOnTheBoxLine(t *testing.T) {
	t.Parallel()
	lines := []string{"  $ sleep 8; echo SLEPT 915ms", "", "", "  → Add a follow-up      ctrl+c to stop", "", "", "  Gemini 3.8 Flash Low · 0.8%      Run Everything", "  ~/w/repo · main"}
	undimmed := []string{"  $ sleep 8; echo SLEPT", "", "", "    A", "", "", "", ""}
	got := aca.CursorScreenParser{}.Parse(lines, undimmed)
	if !got.InputBox || got.Draft || got.Activity != screen.ActivityWorking || !got.InTurn {
		t.Errorf("input box %v, draft %v, %s, in turn %v; want true, false, working, true", got.InputBox, got.Draft, got.Activity, got.InTurn)
	}
}

// cursorTurn is a stretch of a recording where Cursor was in a turn: from a step in
// it, or from when a line that starts with start shows past that step, as Cursor
// writes a message into the chat when it runs it, until the turn's answer shows, a line that is end or ends in it, or until the
// step that stopped it, less a second, as the scripts mark a step after its keys.
type cursorTurn struct{ from, start, end, stoppedAt string }

var cursorTurns = []cursorTurn{
	{from: "tool_running", end: "DONE1"},
	{from: "tool_running", start: "After that, reply", end: "QUEUED"},
	{from: "steered", end: "STEERED"},
	{from: "task_finished_wakes", end: "successfully."},
	{from: "streaming", stoppedAt: "interrupted"},
	{from: "subagent", end: "DONE3"},
}

// TestCursorNeverIdleInATurn checks every read a holder makes during a turn. The
// parser reads idle from what is missing, so an idle there would let --instant
// think nothing runs, and tide show the agent waiting for you while it works.
func TestCursorNeverIdleInATurn(t *testing.T) {
	t.Parallel()
	ready := []cursorTurn{{from: "turn_tool", end: "DONE"}}
	turnsIn := map[string][]cursorTurn{}
	for _, version := range []string{"2026.11.05", "2026.11.05-wdx-status"} {
		turnsIn[version+"/turns-120x40"] = cursorTurns
		turnsIn[version+"/turns-40x24"] = cursorTurns
		turnsIn[version+"/ready-120x40"] = ready
		turnsIn[version+"/ready-40x24"] = ready
		turnsIn[version+"/approval-120x40"] = []cursorTurn{{from: "approval", end: "DONE"}}
	}
	turnsIn["2026.11.05-wdx-status/ready-user-120x40"] = ready
	for dir, turns := range turnsIn {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			rep := replayed(t, cursorRecordingDir(dir))
			at := map[string]time.Duration{}
			for _, step := range rep.steps {
				at[step.name] = step.at
			}
			for _, turn := range turns {
				from, ok := at[turn.from]
				if !ok {
					t.Fatalf("no step %s", turn.from)
				}
				to := at[turn.stoppedAt] - time.Second
				read, started, ended := 0, turn.start == "", false
				for _, m := range rep.moments {
					started = started || m.at >= from && cursorStarts(m.lines, turn.start)
					if m.at < from || !started {
						continue
					}
					if turn.end != "" && cursorShows(m.lines, turn.end) || turn.end == "" && m.at > to {
						ended = true
						break
					}
					read++
					if got := (aca.CursorScreenParser{}).Parse(m.lines, m.undimmed); got.Activity == screen.ActivityIdle {
						t.Errorf("%s at %s, in the turn from %s, reads idle\n%s", dir, m.at, turn.from, bottom(m.lines))
					}
				}
				if read == 0 || !ended {
					t.Errorf("the turn from %s: %d reads in it, ended %v", turn.from, read, ended)
				}
			}
		})
	}
}

// cursorShows is true when a line of the screen is text, or ends in it after a
// space.
func cursorShows(lines []string, text string) bool {
	for _, line := range lines {
		if line = strings.TrimSpace(line); line == text || strings.HasSuffix(line, " "+text) {
			return true
		}
	}
	return false
}

// cursorStarts is true when a line of the screen starts with text.
func cursorStarts(lines []string, text string) bool {
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), text) {
			return true
		}
	}
	return false
}
