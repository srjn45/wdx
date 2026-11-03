package tide

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/daemon"
)

// testSettings is a daemon's settings: tide may not use the real ones, and how the
// cli keeps them is tested there.
type testSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func (s *testSettings) view(key string) api.Setting {
	value, ok := s.values[key]
	v := api.Setting{Key: key, Value: value, Editable: true}
	switch key {
	case "home":
		v.Value, v.Editable, v.Description = "/home/me/.wdx", false, "the home"
	case "log.level":
		if !ok {
			v.Value = "info"
		}
		choices := []string{"debug", "info", "warn", "error"}
		v.Choices, v.Description = &choices, "lowest level logged"
	case "log.retention_days":
		if !ok {
			v.Value = "7"
		}
		v.Description = "days of log files kept"
	case "otel.endpoint":
		v.Description = "the collector; empty sends none"
	}
	return v
}

func (s *testSettings) List() ([]api.Setting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var list []api.Setting
	for _, key := range []string{"home", "log.level", "log.retention_days", "otel.endpoint"} {
		list = append(list, s.view(key))
	}
	return list, nil
}

func (s *testSettings) Set(key, value string) (api.Setting, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch key {
	case "home":
		return api.Setting{}, false, &daemon.InvalidSettingError{Key: key, Err: errors.New("home is read from the environment")}
	case "log.level":
		if value == "error" {
			return api.Setting{}, false, &daemon.InvalidSettingError{Key: key, Err: errors.New("the disk is full")}
		}
	case "log.retention_days":
		if _, err := strconv.Atoi(value); err != nil {
			return api.Setting{}, false, &daemon.InvalidSettingError{Key: key, Err: errors.New(strconv.Quote(value) + " is not a whole number")}
		}
	}
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[key] = value
	return s.view(key), true, nil
}

// settingsModel returns a window with the settings' tab open and read, the
// answers to its saves left to the test.
func settingsModel() *Model {
	m := newAgentsModel(100, 41, demoAgents()...)
	m.layout.setFocus(project)
	m.Update(key('s', 0))
	store := &testSettings{}
	list, _ := store.List()
	m.Update(settingsListed{settings: list})
	return m
}

func TestSettingsOpens(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(100, 41, demoAgents()...)
	m.client = client.New(client.Options{Socket: "/nonexistent/wdx.sock"})
	m.layout.setFocus(project)
	if !m.menuOffers(settingsKey) {
		t.Fatal("the list does not offer it")
	}
	m.Update(key('s', 0))
	if !m.settingsShown() || m.layout.focus != agent {
		t.Fatalf("tab %q, focus %s", m.list().active, m.layout.focus)
	}
	if !slices.Equal(m.list().tabs, []string{"agent-swift-falcon", settingsTab}) {
		t.Fatalf("tabs %v", m.list().tabs)
	}
	if !strings.Contains(titleRow(m), "─[settings(-)]─") {
		t.Fatalf("title %q", titleRow(m))
	}
	wantText(t, m, "loading…")

	// On its own tab it offers nothing; from any other it goes back to it.
	if m.menuOffers(settingsKey) {
		t.Error("its own tab offers it")
	}
	keys(m, "alt+left", "C-q", "s")
	if !m.settingsShown() || len(m.list().tabs) != 2 {
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
	if m.settingsShown() || !slices.Equal(m.list().tabs, []string{"agent-swift-falcon"}) {
		t.Fatalf("tab %q, tabs %v", m.list().active, m.list().tabs)
	}
}

// A project with no agents has the tab too, and an event does not close it.
func TestSettingsWithoutAgents(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(100, 41)
	m.layout.setFocus(project)
	m.Update(key('s', 0))
	sentAgents(m)
	if !m.settingsShown() {
		t.Fatalf("tabs %v", m.list().tabs)
	}
	if m.layout.focus != agent {
		t.Fatalf("focus %s", m.layout.focus)
	}
}

func TestSettingsReadWhenShown(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(100, 41, demoAgents()...)
	m.layout.setFocus(project)
	if _, cmd := m.Update(key('s', 0)); cmd == nil {
		t.Fatal("opening reads nothing")
	}
	if _, cmd := m.Update(key('k', 0)); cmd != nil {
		t.Error("a key on the tab reads them again")
	}
	m.Update(key(tea.KeyLeft, tea.ModAlt))
	if m.settingsShown() {
		t.Fatal("still on it")
	}
	if _, cmd := m.Update(key(tea.KeyRight, tea.ModAlt)); cmd == nil {
		t.Error("coming back reads nothing")
	}

	m.Update(settingsListed{err: errors.New("no answer")})
	wantText(t, m, "settings could not be read: no answer")
}

func TestSettingsRows(t *testing.T) {
	t.Parallel()
	m := settingsModel()
	wantText(t, m,
		"home:                /home/me/.wdx",
		"▸ log.level:           ← info →",
		"log.retention_days:  7",
		"otel.endpoint:       —",
		"lowest level logged",
		"←→ change",
	)
	if strings.Contains(text(m), "the home") {
		t.Error("the note of a row the cursor skips")
	}

	keys(m, "down")
	wantText(t, m, "▸ log.retention_days:  7", "days of log files kept", "Enter edit")
	keys(m, "down")
	wantText(t, m, "▸ otel.endpoint:       —", "Enter edit")
}

// A step sends the next value, going round, and the row moves on the answer.
func TestSettingsChoice(t *testing.T) {
	t.Parallel()
	save := func(values *[]string) func(string) tea.Cmd {
		return func(v string) tea.Cmd {
			*values = append(*values, v)
			return func() tea.Msg { return nil }
		}
	}
	var sent []string
	rows := []valueRow{{key: "level", values: []string{"info"}, choices: []string{"debug", "info", "warn"}, save: save(&sent)}}
	p := valuePane{owner: "o"}
	step := func(name string) {
		p.update(rows, keyMsgs(name)[0])
		p.saved(valueSaved{owner: "o"})
	}
	step("right")
	step("left")
	rows[0].values = []string{"debug"}
	step("left")
	rows[0].values = []string{"warn"}
	step("right")
	if !slices.Equal(sent, []string{"warn", "debug", "warn", "debug"}) {
		t.Fatalf("sent %v", sent)
	}

	// While one is sent, another waits for nothing: it is dropped.
	p.update(rows, keyMsgs("right")[0])
	p.update(rows, keyMsgs("right")[0])
	if len(sent) != 5 {
		t.Fatalf("sent %v", sent)
	}
}

func TestSettingsChoiceAnswered(t *testing.T) {
	t.Parallel()
	m := settingsModel()
	_, cmd := m.Update(key(tea.KeyRight, 0))
	if cmd == nil || m.values.edit == nil || !m.values.edit.busy {
		t.Fatalf("not sent: %+v", m.values.edit)
	}
	wantText(t, m, "saving…", "← info →")

	warn := m.settings[1]
	warn.Value = "warn"
	m.Update(settingSaved{valueSaved: valueSaved{owner: settingsTab}, setting: warn})
	wantText(t, m, "← warn →")
	if m.values.edit != nil {
		t.Fatalf("still sending: %+v", m.values.edit)
	}

	// A refusal leaves the value and says why, until the next key.
	m.Update(key(tea.KeyRight, 0))
	m.Update(settingSaved{valueSaved: valueSaved{owner: settingsTab, err: errors.New("the disk is full")}})
	wantText(t, m, "← warn →", "✗ the disk is full", "←→ change")
	m.Update(keyMsgs("down")[0])
	if strings.Contains(text(m), "the disk is full") {
		t.Fatalf("still says it:\n%s", text(m))
	}
}

func TestSettingsClicks(t *testing.T) {
	t.Parallel()
	m := settingsModel()
	frames, _ := m.frames()
	area := frames[agent].Inset(1)
	start := area.Min.X + valueIndent + len("log.retention_days") + valueKeyGap

	// A click on the read-only row does nothing, on a choice moves the cursor, on
	// its arrows steps it, and on an open row edits it.
	if _, cmd := m.Update(clickAt(start, area.Min.Y)); cmd != nil || m.values.edit != nil {
		t.Fatalf("a click on home: %+v", m.values.edit)
	}
	keys(m, "down")
	if _, cmd := m.Update(clickAt(start+2, area.Min.Y+1)); cmd != nil || m.values.cursorRow(m.settingRows()) != 1 {
		t.Fatalf("a click on the value: cursor %d", m.values.cursor)
	}
	if _, cmd := m.Update(clickAt(start, area.Min.Y+1)); cmd == nil || m.values.edit.text != "debug" {
		t.Fatalf("a click on ←: %+v", m.values.edit)
	}
	m.Update(settingSaved{valueSaved: valueSaved{owner: settingsTab}, setting: m.settings[1]})
	if _, cmd := m.Update(clickAt(start+7, area.Min.Y+1)); cmd == nil || m.values.edit.text != "warn" {
		t.Fatalf("a click on →: %+v", m.values.edit)
	}
	m.Update(settingSaved{valueSaved: valueSaved{owner: settingsTab}, setting: m.settings[1]})
	m.Update(clickAt(start, area.Min.Y+2))
	if m.values.edit == nil || m.values.edit.text != "7" {
		t.Fatalf("a click on the number: %+v", m.values.edit)
	}
}

func TestSettingsResume(t *testing.T) {
	t.Parallel()
	m := settingsModel()
	r := &Resume{Project: "demo", ProjectID: "proj-demo", Focus: "agent", Agent: settingsTab, Tabs: []string{settingsTab}}
	next := New(m.ctx, Options{Style: m.st, Resume: r})
	next.tick = noTick
	next.Update(tea.WindowSizeMsg{Width: 100, Height: 41})
	next.Update(projectsListed{projects: []api.ProjectSummary{{Name: "demo", State: api.ProjectStateOpen}}})
	sentAgents(next, demoAgents()...)
	if !next.settingsShown() || !slices.Contains(next.list().tabs, settingsTab) {
		t.Fatalf("tab %q, tabs %v", next.list().active, next.list().tabs)
	}
}

// TestSettingsLive changes settings against a daemon: a choice, a number refused
// and then taken, and a value emptied.
func TestSettingsLive(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	h.newProject(t, "api", api.ProjectStateOpen)
	p := h.openShown(t)
	p.until(t, "the project", func(m *Model) bool { return m.list() != nil })
	p.keys("s")
	p.until(t, "the settings read", func(m *Model) bool { return len(m.settings) == 4 })
	wantText(t, p.m, "← info →")

	value := func(m *Model, key string) string {
		for _, s := range m.settings {
			if s.Key == key {
				return s.Value
			}
		}
		return ""
	}
	p.keys("right")
	p.until(t, "the level changed", func(m *Model) bool { return value(m, "log.level") == "warn" && m.values.edit == nil })
	wantText(t, p.m, "← warn →")

	p.keys("right")
	p.until(t, "the level refused", func(m *Model) bool { return m.values.failed != "" })
	wantText(t, p.m, "← warn →", "✗ the disk is full")

	p.keys("down", "enter", "backspace", "x", "enter")
	p.until(t, "the number refused", func(m *Model) bool { return m.values.edit != nil && m.values.edit.err != "" })
	wantText(t, p.m, `✗ "x" is not a whole number`)
	p.keys("backspace", "30", "enter")
	p.until(t, "the number taken", func(m *Model) bool { return value(m, "log.retention_days") == "30" && m.values.edit == nil })

	p.keys("down", "enter", "http://here", "enter")
	p.until(t, "the endpoint taken", func(m *Model) bool { return value(m, "otel.endpoint") == "http://here" && m.values.edit == nil })
	p.keys("enter")
	for range "http://here" {
		p.keys("backspace")
	}
	p.keys("enter")
	p.until(t, "the endpoint emptied", func(m *Model) bool { return value(m, "otel.endpoint") == "" && m.values.edit == nil })
	wantText(t, p.m, "otel.endpoint:       —")
}
