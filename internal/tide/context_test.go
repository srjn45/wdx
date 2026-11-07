package tide

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// The colors of the context styles, as the screen writes them.
const (
	red    = "31"
	green  = "32"
	yellow = "33"
)

// wantColored fails unless m's screen shows text in color: the screen writes a
// color among other attributes, as \x1b[31;22m.
func wantColored(t *testing.T, m *Model, color, text string) {
	t.Helper()
	colored := regexp.MustCompile(`\x1b\[(?:[0-9;]*;)?` + color + `(?:;[0-9;]*)?m` + regexp.QuoteMeta(text))
	if screen := m.render(); !colored.MatchString(screen) {
		t.Fatalf("no %s in color %s in:\n%q", text, color, screen)
	}
}

func withContext(a api.Agent, tokens int64, window int64) api.Agent {
	a.Context = &api.AgentContext{Tokens: tokens}
	if window > 0 {
		a.Context.Window = &window
	}
	return a
}

// TestAgentRowContextSize: a row's size is at its end, before the active mark, in
// the color of the thresholds it reached; none before the agent's ACA told.
func TestAgentRowContextSize(t *testing.T) {
	t.Parallel()
	off, on := style.New(false), style.New(true)
	thresholds := contextThresholds{warning: 1000, critical: 2000}
	working := api.AgentActivityWorking
	a := api.Agent{Name: "helper", State: api.AgentStateRunning, Activity: &working}

	if got := agentRow(off, thresholds, agentListRow{agent: withContext(a, 150_000, 0)}, false, true, 40); !strings.HasPrefix(got, "  ● helper  ") || !strings.HasSuffix(got, " working 150k ◂ ") || ansi.StringWidth(got) != 40 {
		t.Errorf("plain: %q", got)
	}
	if got := agentRow(off, thresholds, agentListRow{agent: a}, false, false, 40); got != "  ● helper                      working " {
		t.Errorf("no size: %q", got)
	}
	for _, tc := range []struct {
		tokens int64
		color  func(string) string
	}{
		{999, on.ContextFine}, {1000, on.ContextWarning}, {1001, on.ContextWarning},
		{1999, on.ContextWarning}, {2000, on.ContextCritical}, {2001, on.ContextCritical},
	} {
		got := agentRow(on, thresholds, agentListRow{agent: withContext(a, tc.tokens, 0)}, false, false, 40)
		if want := " " + tc.color(api.TokenCount(tc.tokens)) + " "; !strings.HasSuffix(got, want) {
			t.Errorf("%d: %q, want it to end %q", tc.tokens, got, want)
		}
	}
}

// TestAgentRowContextGivesWay: on a narrow pane the size goes first, then the
// detail, before the name.
func TestAgentRowContextGivesWay(t *testing.T) {
	t.Parallel()
	st := style.New(false)
	a := withContext(exited("a-rather-long-agent-name-indeed", 0), 1_500_000, 0)
	for _, tt := range []struct {
		width int
		want  string
	}{
		{50, "  ○ a-rather-long-agent-name-indeed exited 0 1.5M "},
		{30, "  ○ a-rather-l… exited 0 1.5M "},
		{28, "  ○ a-rather-long… exited 0 "},
		{22, "  ○ a-rather-long-ag… "},
	} {
		got := agentRow(st, newContextThresholds(0, 0), agentListRow{agent: a}, false, false, tt.width)
		if got != tt.want || ansi.StringWidth(got) != tt.width {
			t.Errorf("width %d: %q, want %q", tt.width, got, tt.want)
		}
	}
}

// TestInfoContext: the info tab says how full the context is, out of its window
// when known, in the row's color.
func TestInfoContext(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		agent api.Agent
		want  string
	}{
		{withContext(running("lead"), 70_086, 200_000), "context:            70k of 200k (35%)"},
		{withContext(running("lead"), 70_086, 0), "context:            70k"},
		{running("lead"), "context:            —"},
	} {
		m := newAgentsModel(120, 41, tc.agent)
		m.layout.setFocus(project)
		keys(m, "i")
		wantText(t, m, tc.want)
	}

	m := newAgentsModel(120, 41, withContext(running("lead"), 450_000, 1_000_000))
	m.st = style.New(true)
	m.layout.setFocus(project)
	keys(m, "i")
	wantColored(t, m, red, "450k of 1M (45%)")
}

// TestContextThresholdsSaved: saving a threshold in the settings tab colors the
// sizes by it at once; a save that failed changes nothing.
func TestContextThresholdsSaved(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(100, 41, withContext(running("lead"), 150_000, 0))
	m.st = style.New(true)
	wantColored(t, m, green, "150k")
	m.Update(settingSaved{valueSaved: valueSaved{err: errors.New("refused")}, setting: api.Setting{Key: contextWarningKey, Value: "100000"}})
	if m.thresholds != newContextThresholds(0, 0) {
		t.Fatalf("after a failed save %+v", m.thresholds)
	}
	m.Update(settingSaved{setting: api.Setting{Key: contextWarningKey, Value: "100000"}})
	wantColored(t, m, yellow, "150k")
	m.Update(settingSaved{setting: api.Setting{Key: contextCriticalKey, Value: "150000"}})
	wantColored(t, m, red, "150k")
}
