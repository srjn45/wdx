package tide

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/aca/acatest"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/identity"
)

func TestStatsOpens(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(100, 41, demoAgents()...)
	m.client = client.New(client.Options{Socket: "/nonexistent/wdx.sock"})
	m.layout.setFocus(project)
	if !m.menuOffers(statsKey) {
		t.Fatal("the list does not offer it")
	}
	m.Update(key('S', 0))
	if !m.statsShown() || m.layout.focus != agent {
		t.Fatalf("tab %q, focus %s", m.list().active, m.layout.focus)
	}
	if !slices.Equal(m.list().tabs, []string{"agent-swift-falcon", statsTab}) {
		t.Fatalf("tabs %v", m.list().tabs)
	}
	if !strings.Contains(titleRow(m), "━[stats(-)]━") {
		t.Fatalf("title %q", titleRow(m))
	}
	wantText(t, m, "loading…")

	// On its own tab it offers nothing; from any other it goes back to it.
	if m.menuOffers(statsKey) {
		t.Error("its own tab offers it")
	}
	keys(m, "alt+left", "C-q", "S")
	if !m.statsShown() || len(m.list().tabs) != 2 {
		t.Fatalf("tab %q, tabs %v", m.list().active, m.list().tabs)
	}

	// It is of no agent, so nothing asks about one, and it attaches nothing.
	if _, ok := m.focusedAgent(); ok {
		t.Error("an agent has the focus")
	}
	if _, _, ok := m.attachedTerminal(); ok {
		t.Error("an agent attaches")
	}
	for _, key := range []string{"x", "c", infoKey} {
		if m.menuOffers(key) {
			t.Errorf("it offers %s", key)
		}
	}
	if strings.Contains(row(t, m, "swift-falcon"), "◂") {
		t.Errorf("an agent is marked: %s", row(t, m, "swift-falcon"))
	}

	// Closing it closes it; the agents' tabs are all that stay.
	keys(m, "C-q", "-")
	if m.statsShown() || !slices.Equal(m.list().tabs, []string{"agent-swift-falcon"}) {
		t.Fatalf("tab %q, tabs %v", m.list().active, m.list().tabs)
	}
}

// TestStatsReadWhenShown: the usage is read when the tab comes to show and on each
// project event while it shows, not on a key; an error is said.
func TestStatsReadWhenShown(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(100, 41, demoAgents()...)
	m.layout.setFocus(project)
	if _, cmd := m.Update(key('S', 0)); cmd == nil {
		t.Fatal("opening reads nothing")
	}
	if _, cmd := m.Update(key('k', 0)); cmd != nil {
		t.Error("a key on the tab reads them again")
	}
	m.Update(key(tea.KeyLeft, tea.ModAlt))
	if m.statsShown() {
		t.Fatal("still on it")
	}
	if _, cmd := m.Update(key(tea.KeyRight, tea.ModAlt)); cmd == nil {
		t.Error("coming back reads nothing")
	}
	m.Update(usageListed{err: errors.New("no answer")})
	wantText(t, m, "stats could not be read: no answer")
}

func demoUsage(now time.Time) []api.UsageWindow {
	session, week := now.Add(2*time.Hour+10*time.Minute), now.Add(72*time.Hour)
	return []api.UsageWindow{
		{Aca: "claude_code", Window: "five_hour", UsedPercent: 54, ResetsAt: &session, ObservedAt: now.Add(-time.Minute)},
		{Aca: "claude_code", Window: "seven_day", UsedPercent: 92, ResetsAt: &week, ObservedAt: now},
	}
}

// statsModel returns a window with the stats' tab open and read.
func statsModel(usage []api.UsageWindow, acas ...api.Aca) *Model {
	m := newAgentsModel(100, 41, demoAgents()...)
	m.layout.setFocus(project)
	m.Update(key('S', 0))
	m.Update(usageListed{usage: usage, acas: acas})
	return m
}

func TestStatsRows(t *testing.T) {
	t.Parallel()
	now := time.Now()
	m := statsModel(demoUsage(now), api.Aca{Id: "claude_code", Default: true}, api.Aca{Id: "codex"})
	wantText(t, m,
		"claude_code:      available",
		"  Session (5 h):  54% ▰▰▰▰▰▱▱▱▱▱  resets in 2 h",
		"  Week:           92% ▰▰▰▰▰▰▰▰▰▱  resets "+now.Add(72*time.Hour).Local().Format("Jan 2 15:04"),
		"  as of:          "+now.Local().Format("15:04"),
		"codex:            nothing observed yet",
	)
	if !m.statsTiming {
		t.Error("the minute timer is not running")
	}

	// A limited window closes its ACA until its reset; a bucket is a block of its own.
	usage := demoUsage(now)
	usage[1].UsedPercent, usage[1].Limited = 100, true
	fable := usage[1]
	fable.Bucket, fable.UsedPercent, fable.Limited = "Fable", 35, false
	usage = append(usage, fable)
	m.Update(usageListed{usage: usage, acas: []api.Aca{{Id: "claude_code"}}})
	wantText(t, m,
		"claude_code:        limited, resets "+now.Add(72*time.Hour).Local().Format("Jan 2 15:04"),
		"  Week:             100% ▰▰▰▰▰▰▰▰▰▰  limited, resets ",
		"claude_code Fable:  available",
		"  Week:             35% ▰▰▰▰▱▱▱▱▱▱  resets ",
	)
	if strings.Contains(text(m), "nothing observed yet") {
		t.Error("an ACA that told something is shown as silent")
	}
}

// TestStatsEmpty: with no ACA at all the tab says so; the minute passing starts the
// timer again only while the tab shows.
func TestStatsEmpty(t *testing.T) {
	t.Parallel()
	m := statsModel(nil)
	wantText(t, m, "no AI coding agent is installed and enabled")
	m.Update(statsMinute{})
	if !m.statsTiming {
		t.Error("a minute on the tab does not start the timer again")
	}
	keys(m, "alt+left")
	m.Update(statsMinute{})
	if m.statsTiming {
		t.Error("a minute off the tab starts the timer")
	}
}

func TestStatsResume(t *testing.T) {
	t.Parallel()
	m := statsModel(nil)
	r := &Resume{Project: "demo", ProjectID: "proj-demo", Focus: "agent", Agent: statsTab, Tabs: []string{statsTab}}
	next := New(m.ctx, Options{Style: m.st, Resume: r})
	next.tick = noTick
	next.Update(tea.WindowSizeMsg{Width: 100, Height: 41})
	next.Update(projectsListed{projects: []api.ProjectSummary{{Name: "demo", State: api.ProjectStateOpen}}})
	sentAgents(next, demoAgents()...)
	if !next.statsShown() || !slices.Contains(next.list().tabs, statsTab) {
		t.Fatalf("tab %q, tabs %v", next.list().active, next.list().tabs)
	}
}

// TestStatsLive reads the usage from a daemon: what an agent's ACA tells reaches the
// tab through the project's events, without a key.
func TestStatsLive(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	a := h.newAgent(t, "api", dir, "helper")
	p := h.openShown(t)
	// The running agent's tab comes with the first events; the stats open after it,
	// so that it does not take the place of the tab just opened.
	p.until(t, "the agent's tab", func(m *Model) bool { return m.list() != nil && len(m.list().tabs) == 1 })
	p.keys("C-q", "S")
	p.until(t, "the usage read", func(m *Model) bool { return m.usageRead })
	wantText(t, p.m, "claude_code:  nothing observed yet")
	resets := time.Now().Add(3*time.Hour + 30*time.Minute)
	if err := acatest.WriteUsage(identity.AgentDir(h.home, a.Id), []aca.Usage{{Window: "five_hour", UsedPercent: 100, ResetsAt: resets, ObservedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	p.until(t, "the usage shown", func(m *Model) bool { return len(m.usage) == 1 })
	wantText(t, p.m, "claude_code:      limited, resets in 3 h", "  Session (5 h):  100% ▰▰▰▰▰▰▰▰▰▰  limited, resets in 3 h")
}
