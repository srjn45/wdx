package tide

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
)

// infoModel returns a window with demoAgents' swift-falcon's agent tab current
// and the left pane in focus.
func infoModel() *Model {
	m := newAgentsModel(100, 41, demoAgents()...)
	m.layout.setFocus(project)
	return m
}

func wantInfoOf(t *testing.T, m *Model, name string) {
	t.Helper()
	if !m.infoShown() || m.layout.focus != agent {
		t.Fatalf("tab %q, focus %s", m.list().active, m.layout.focus)
	}
	wantCurrent(t, m, name)
}

func offersInfo(m *Model) bool { return m.menuOffers(infoKey) }

func TestInfoOpens(t *testing.T) {
	t.Parallel()

	// A plain i in the list: the agent under the cursor.
	m := infoModel()
	keys(m, "up", "i")
	wantInfoOf(t, m, "calm-otter")
	if !slices.Equal(m.list().tabs, []string{"agent-swift-falcon", "info:agent-calm-otter"}) {
		t.Fatalf("tabs %v", m.list().tabs)
	}
	if !strings.Contains(titleRow(m), "info: calm-otter(-)]") {
		t.Fatalf("title %q", titleRow(m))
	}

	// C-q i in the list does the same, and a zoom goes with the focus.
	m = infoModel()
	keys(m, "C-]", "C-q", "i")
	wantInfoOf(t, m, "swift-falcon")
	if m.layout.zoomed != agent {
		t.Fatalf("zoomed %s", m.layout.zoomed)
	}
}

func TestInfoKeysOffered(t *testing.T) {
	t.Parallel()
	m := infoModel()
	if !offersInfo(m) {
		t.Error("the list with an agent selected does not offer it")
	}
	m.layout.setFocus(terminal)
	if offersInfo(m) {
		t.Error("the terminal pane offers it")
	}
	keys(m, "i")
	if m.infoShown() {
		t.Error("i in the terminal pane opened it")
	}

	m.layout.setFocus(agent)
	if !offersInfo(m) {
		t.Error("an agent's tab does not offer it")
	}
	keys(m, "C-q", "i")
	wantInfoOf(t, m, "swift-falcon")
	if offersInfo(m) {
		t.Error("its own info offers it")
	}
	tabs := slices.Clone(m.list().tabs)
	keys(m, "C-q", "i")
	if !slices.Equal(tabs, m.list().tabs) {
		t.Errorf("C-q i on the info tab: %v", m.list().tabs)
	}

	// With no tabs, or no agent, nothing to show.
	keys(m, "C-q", "-", "C-q", "-")
	m.layout.setFocus(agent)
	if offersInfo(m) {
		t.Error("no tabs offers it")
	}
	empty := newAgentsModel(100, 41)
	empty.layout.setFocus(project)
	if offersInfo(empty) {
		t.Error("a list with no agents offers it")
	}
}

func TestInfoGoesToAnOpenTab(t *testing.T) {
	t.Parallel()
	m := infoModel()
	keys(m, "i")
	tabs := slices.Clone(m.list().tabs)
	keys(m, "alt+left", "alt+left")
	if m.layout.focus != project {
		t.Fatalf("focus %s", m.layout.focus)
	}
	keys(m, "i")
	wantInfoOf(t, m, "swift-falcon")
	if !slices.Equal(tabs, m.list().tabs) {
		t.Fatalf("tabs %v, were %v", m.list().tabs, tabs)
	}
}

func TestInfoShowsTheFields(t *testing.T) {
	t.Parallel()
	worktree, parent, activity, draft, queued, model := "auth-fix", "planner", api.AgentActivityWorking, false, 2, "Haiku 5.5"
	kids := []api.Agent{running("tests"), running("docs")}
	a := api.Agent{Name: "lead", State: api.AgentStateRunning, Aca: "claude_code", ProjectName: "demo", Worktree: &worktree,
		ParentAgentName: &parent, Children: &kids, Activity: &activity, Draft: &draft, Queued: &queued, Model: &model}
	m := newAgentsModel(120, 41, a)
	m.layout.setFocus(project)
	keys(m, "i")

	got := text(m)
	for _, want := range []string{
		"▸ name:               lead",
		"id:                 agent-lead",
		"aca:                claude_code",
		"model:              Haiku 5.5",
		"project_name:       demo",
		"dir:                /work/demo",
		"worktree:           auth-fix",
		"parent_agent_name:  planner",
		"children:           tests",
		"                    docs",
		"state:              running",
		"exit_code:          —",
		"error:              —",
		"activity:           working",
		"draft:              no",
		"queued:             2",
		"Enter rename",
		"[● info: lead(-)]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
	if aca, model, project := strings.Index(got, "aca:"), strings.Index(got, "model:"), strings.Index(got, "project_name:"); aca > model || model > project {
		t.Errorf("the model is not next to the aca:\n%s", got)
	}
}

func TestInfoIsLive(t *testing.T) {
	t.Parallel()
	m := infoModel()
	keys(m, "i")
	sentAgents(m, api.Agent{Name: "swift-falcon", State: api.AgentStateExited, ExitCode: new(int)})
	if got := text(m); !strings.Contains(got, "state:") || !strings.Contains(got, "exited") || !strings.Contains(got, "exit_code:") {
		t.Fatalf("not updated:\n%s", got)
	}
}

// The info tab attaches nothing, and its agent's list mark stays.
func TestInfoAttachesNothing(t *testing.T) {
	t.Parallel()
	m := infoModel()
	if a, _, ok := m.attachedTerminal(); !ok || a.Name != "swift-falcon" {
		t.Fatalf("attached %q, %v", a.Name, ok)
	}
	keys(m, "i")
	if a, _, ok := m.attachedTerminal(); ok {
		t.Fatalf("attached %q", a.Name)
	}
	if a, ok := m.activeAgent(); !ok || a.Name != "swift-falcon" {
		t.Fatalf("active %q, %v", a.Name, ok)
	}
	if !strings.Contains(row(t, m, "swift-falcon"), "◂") {
		t.Fatalf("no mark: %s", row(t, m, "swift-falcon"))
	}
}

func TestInfoEdit(t *testing.T) {
	t.Parallel()
	m := infoModel()
	keys(m, "i", "enter")
	if !strings.Contains(text(m), "swift-falcon_") || !strings.Contains(text(m), "Enter save  Esc cancel") {
		t.Fatalf("not editing:\n%s", text(m))
	}

	// Typing goes to the value, not the menu's letters; Esc changes nothing.
	keys(m, "x", "backspace", "backspace")
	if !strings.Contains(text(m), "swift-falco_") {
		t.Fatalf("typing:\n%s", text(m))
	}
	keys(m, "esc")
	if strings.Contains(text(m), "swift-falcon_") || strings.Contains(text(m), "swift-falco_") || !strings.Contains(text(m), "swift-falcon") {
		t.Fatalf("Esc:\n%s", text(m))
	}

	// A value not changed sends nothing.
	keys(m, "enter")
	if _, cmd := m.Update(keyMsgs("enter")[0]); cmd != nil && m.values.edit != nil {
		t.Fatalf("an unchanged name: edit %+v", m.values.edit)
	}
	if m.values.edit != nil {
		t.Fatalf("an unchanged name stays in editing: %+v", m.values.edit)
	}

	// A paste takes only what a terminal does not act on.
	keys(m, "enter")
	m.Update(tea.PasteMsg{Content: "-2\n\x1b[2J"})
	if m.values.edit.text != "swift-falcon-2[2J" {
		t.Fatalf("pasted %q", m.values.edit.text)
	}
}

// A failure stays in editing and says why; what comes of an edit ended is dropped.
func TestInfoEditRefused(t *testing.T) {
	t.Parallel()
	m := infoModel()
	m.client = client.New(client.Options{Socket: "/nonexistent/wdx.sock"})
	keys(m, "i", "enter", "backspace")
	_, cmd := m.Update(keyMsgs("enter")[0])
	if m.values.edit == nil || !m.values.edit.busy || !strings.Contains(text(m), "saving…") {
		t.Fatalf("not saving: %+v", m.values.edit)
	}
	keys(m, "x") // ignored while it is sent
	msg := drain(cmd)
	saved, ok := msg.(valueSaved)
	if !ok || saved.err == nil {
		t.Fatalf("answer %#v", msg)
	}
	m.Update(saved)
	if m.values.edit == nil || m.values.edit.busy || m.values.edit.text != "swift-falco" {
		t.Fatalf("after the refusal: %+v", m.values.edit)
	}
	if got := text(m); !strings.Contains(got, "✗ ") || !strings.Contains(got, "not answering") {
		t.Fatalf("no reason:\n%s", got)
	}

	keys(m, "esc")
	m.Update(saved)
	if m.values.edit != nil {
		t.Fatalf("a late answer: %+v", m.values.edit)
	}
}

// drain runs cmd, a batch or not, down to the first message it gives.
func drain(cmd tea.Cmd) tea.Msg {
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil {
				if got := drain(c); got != nil {
					return got
				}
			}
		}
		return nil
	}
	return msg
}

func TestInfoLeavingEndsTheEdit(t *testing.T) {
	t.Parallel()
	for name, leave := range map[string]func(m *Model){
		"to the tab beside it": func(m *Model) { keys(m, "alt+left") },
		"closing it":           func(m *Model) { keys(m, "C-q", "-") },
		"to the terminals":     func(m *Model) { keys(m, "alt+down") },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := infoModel()
			keys(m, "i", "enter", "x")
			leave(m)
			if m.values.edit != nil {
				t.Fatalf("still editing: %+v", m.values.edit)
			}
		})
	}
}

func TestInfoClick(t *testing.T) {
	t.Parallel()
	m := infoModel()
	keys(m, "i")
	frames, _ := m.frames()
	area := frames[agent].Inset(1)

	// A click on a read-only row edits nothing; one on the name does.
	m.Update(clickAt(area.Min.X+5, area.Min.Y+1))
	if m.values.edit != nil {
		t.Fatalf("a click on id: %+v", m.values.edit)
	}
	m.Update(clickAt(area.Min.X+5, area.Min.Y))
	if m.values.edit == nil {
		t.Fatal("a click on the name does not edit it")
	}

	// A click on the tab's (-) closes it, and the agent's tab takes over.
	keys(m, "esc")
	for _, l := range m.labels(agent, frames[agent]) {
		if l.closes && m.openTabs()[l.tab].tab == infoTab("agent-swift-falcon") {
			m.Update(clickAt(l.area.Min.X, l.area.Min.Y))
			break
		}
	}
	if m.infoShown() || !slices.Equal(m.list().tabs, []string{"agent-swift-falcon"}) {
		t.Fatalf("tabs %v, current %s", m.list().tabs, m.list().active)
	}
}

func TestInfoTombstone(t *testing.T) {
	t.Parallel()
	kids := []api.Agent{running("child")}
	m := newAgentsModel(100, 41, api.Agent{Name: "parent", State: api.AgentStateRemoved, Children: &kids})
	m.layout.setFocus(project)
	keys(m, "up", "i")
	wantInfoOf(t, m, "parent")
	keys(m, "enter")
	if m.values.edit != nil || strings.Contains(text(m), "Enter rename") {
		t.Fatalf("a tombstone's name can be edited:\n%s", text(m))
	}
}

// Removing the agent closes both its tabs, even when it stays as a tombstone.
func TestInfoClosesWithItsAgent(t *testing.T) {
	t.Parallel()
	m := infoModel()
	keys(m, "i", "enter")
	sentAgents(m, running("wise-owl"))
	for _, tab := range m.list().tabs {
		if tabAgent(tab) == "agent-swift-falcon" {
			t.Fatalf("tabs %v", m.list().tabs)
		}
	}
	if m.values.edit != nil {
		t.Fatalf("still editing: %+v", m.values.edit)
	}
}

func TestInfoScrolls(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(100, 10, demoAgents()...)
	m.layout.setFocus(project)
	keys(m, "i")
	if !strings.Contains(text(m), "name:") || strings.Contains(text(m), "queued:") {
		t.Fatalf("too tall to show all:\n%s", text(m))
	}
	for range 30 {
		keys(m, "down")
	}
	got := text(m)
	if !strings.Contains(got, "queued:") || strings.Contains(got, "name:") {
		t.Fatalf("scrolled:\n%s", got)
	}

	// The wheel scrolls back, and editing brings the row into view.
	frames, _ := m.frames()
	f := frames[agent].Inset(1)
	m.Update(tea.MouseWheelMsg{X: f.Min.X, Y: f.Min.Y, Button: tea.MouseWheelUp})
	if m.values.offset == 0 {
		t.Fatalf("offset %d", m.values.offset)
	}
	keys(m, "enter")
	if !strings.Contains(text(m), "name:") {
		t.Fatalf("editing out of view:\n%s", text(m))
	}
}

// A long value wraps instead of being cut.
func TestValuesWrap(t *testing.T) {
	t.Parallel()
	p := valuePane{}
	st := infoModel().st
	rows := []valueRow{{key: "dir", values: []string{strings.Repeat("a", 25)}}}
	lines := p.lines(rows, 16, false, st)
	if len(lines) != 4 {
		t.Fatalf("%d lines: %+v", len(lines), lines)
	}
	if got := lines[1].text; !strings.HasPrefix(got, strings.Repeat(" ", valueIndent+3+valueKeyGap)+"a") {
		t.Fatalf("continued %q", got)
	}
}

// Up and down go between editable rows, as the settings will need, and skip the
// rest.
func TestValuesCursor(t *testing.T) {
	t.Parallel()
	save := func(string) tea.Cmd { return nil }
	rows := []valueRow{
		{key: "a", values: []string{"1"}},
		{key: "b", values: []string{"2"}, save: save, verb: "edit"},
		{key: "c", values: []string{"3"}},
		{key: "d", values: []string{"4"}, save: save, verb: "edit"},
	}
	p := valuePane{}
	if got := p.cursorRow(rows); got != 1 {
		t.Fatalf("first cursor %d", got)
	}
	p.update(rows, keyMsgs("down")[0])
	if got := p.cursorRow(rows); got != 3 {
		t.Fatalf("down: %d", got)
	}
	p.update(rows, keyMsgs("up")[0])
	if got := p.cursorRow(rows); got != 1 {
		t.Fatalf("up: %d", got)
	}
	p.update(rows, keyMsgs("enter")[0])
	if p.edit == nil || p.edit.row != 1 || p.edit.text != "2" {
		t.Fatalf("edit %+v", p.edit)
	}
	buf := uv.NewScreenBuffer(40, 1)
	p.draw(buf, uv.Rect(0, 0, 40, 1), rows, true, infoModel().st) // no room: nothing to panic on
}

func TestInfoResume(t *testing.T) {
	t.Parallel()
	m := infoModel()
	keys(m, "i")
	m.executable = executableFile(t)
	keys(m, "C-q", "r")
	r := m.Reload()
	if r == nil || r.Agent != "info:agent-swift-falcon" || !slices.Contains(r.Tabs, "info:agent-swift-falcon") {
		t.Fatalf("resume %+v", r)
	}
}

// TestInfoLive renames against a daemon: the name comes back through the stream,
// and one taken is refused in place.
func TestInfoLive(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	h.newAgent(t, "api", dir, "a-one")
	h.newAgent(t, "api", dir, "b-two")
	p := h.openShown(t)
	p.until(t, "a-one attached", agentAttached("a-one"))

	p.keys("alt+left", "i")
	p.until(t, "a-one detached", func(m *Model) bool { return m.attachedIn(agent) == nil })

	p.keys("enter")
	for range "a-one" {
		p.keys("backspace")
	}
	p.keys("b-two", "enter")
	p.until(t, "the name refused", func(m *Model) bool {
		return m.values.edit != nil && strings.Contains(m.values.edit.err, "already used")
	})

	for range "b-two" {
		p.keys("backspace")
	}
	p.keys("c-three", "enter")
	p.until(t, "the new name shown", func(m *Model) bool {
		a, ok := m.activeAgent()
		return ok && a.Name == "c-three" && m.values.edit == nil
	})
	if a, _ := p.m.activeAgent(); a.State != api.AgentStateRunning {
		t.Fatalf("after the rename: %+v", a)
	}
}
