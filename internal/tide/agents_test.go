package tide

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/style"
)

// sentAgents makes m get the project demo from its stream, with agents, whose
// ids are agent-<name>.
func sentAgents(m *Model, agents ...api.Agent) {
	for i := range agents {
		agents[i].Id, agents[i].TerminalId = "agent-"+agents[i].Name, "term-"+agents[i].Name
		agents[i].Dir = "/work/demo"
	}
	m.Update(projectEvent{gen: m.following.gen, event: client.ProjectEvent{Project: api.Project{Id: "proj-demo", Name: "demo", Agents: &agents}}})
}

// newAgentsModel returns newModel's window with agents as they first came.
func newAgentsModel(width, height int, agents ...api.Agent) *Model {
	m, _ := newModel(width, height)
	delete(m.agentLists, "proj-demo")
	sentAgents(m, agents...)
	return m
}

func running(name string) api.Agent { return api.Agent{Name: name, State: api.AgentStateRunning} }

func exited(name string, code int) api.Agent {
	return api.Agent{Name: name, State: api.AgentStateExited, ExitCode: &code}
}

// demoAgents has the cursor and the active agent on swift-falcon, the first
// running.
func demoAgents() []api.Agent {
	return []api.Agent{
		{Name: "bold-heron", State: api.AgentStateFailed},
		exited("calm-otter", 0),
		running("swift-falcon"),
		running("wise-owl"),
	}
}

// row returns what the project pane shows on the row of name.
func row(t *testing.T, m *Model, name string) string {
	t.Helper()
	for line := range strings.Lines(text(m)) {
		if cells := strings.SplitN(line, "│", 3); len(cells) == 3 && strings.Contains(cells[1], name) {
			return "│" + cells[1]
		}
	}
	t.Fatalf("no row of %s:\n%s", name, text(m))
	return ""
}

func wantRow(t *testing.T, m *Model, name, want string) {
	t.Helper()
	if got := row(t, m, name); !strings.Contains(got, want) {
		t.Fatalf("row of %s: %q, want %q", name, got, want)
	}
}

func TestAgentsList(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(100, 41, demoAgents()...)

	wantRow(t, m, " Agents", "4")
	wantRow(t, m, "bold-heron", "│  ✕ bold-heron")
	wantRow(t, m, "bold-heron", "failed")
	wantRow(t, m, "calm-otter", "exited 0")
	// The first running agent is active, on the frame of the agent pane, with
	// the cursor on it.
	wantRow(t, m, "swift-falcon", "│▸ ● swift-falcon")
	wantRow(t, m, "swift-falcon", "◂")
	wantText(t, m, " swift-falcon ─", "↑↓ select  Enter show")

	// The arrows move the cursor alone; it stops at the ends.
	m.Update(key('k', 0))
	wantRow(t, m, "calm-otter", "│▸ ○ calm-otter")
	wantRow(t, m, "swift-falcon", "│  ● swift-falcon")
	wantRow(t, m, "swift-falcon", "◂")
	for range 3 {
		m.Update(keyMsgs("up")[0])
	}
	wantRow(t, m, "bold-heron", "│▸ ✕ bold-heron")
	// ← and → do nothing in a linear list.
	m.Update(keyMsgs("left")[0])
	m.Update(keyMsgs("right")[0])
	wantRow(t, m, "bold-heron", "│▸ ✕ bold-heron")

	// Enter makes it active and leaves the focus in the list.
	m.Update(keyMsgs("enter")[0])
	wantRow(t, m, "bold-heron", "◂")
	if strings.Contains(row(t, m, "swift-falcon"), "◂") {
		t.Fatalf("two active:\n%s", text(m))
	}
	if m.layout.focus != project {
		t.Fatalf("Enter moved the focus to %s", m.layout.focus)
	}
	wantText(t, m, " bold-heron ─")

	// Out of the pane, the cursor hides and the active agent stays.
	m.Update(keyMsgs("alt+right")[0])
	wantRow(t, m, "bold-heron", "│  ✕ bold-heron")
	wantRow(t, m, "bold-heron", "◂")
	wantText(t, m, " demo · agent · bold-heron")
}

func TestAgentsNoneRunning(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(100, 41, exited("calm-otter", 0))
	if _, ok := m.activeAgent(); ok {
		t.Fatalf("an agent active with none running:\n%s", text(m))
	}
	wantText(t, m, "no agent shown: Enter on one in the list shows it")

	// An agent that runs later does not take the pane by itself.
	sentAgents(m, exited("calm-otter", 0), running("swift-falcon"))
	if _, ok := m.activeAgent(); ok {
		t.Fatalf("an agent became active by itself:\n%s", text(m))
	}
}

func TestAgentsEmpty(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(128, 41)
	wantRow(t, m, " Agents", "0")
	wantText(t, m, " no agents: C-q a creates one")
	m.Update(keyMsgs("down")[0])
	m.Update(keyMsgs("enter")[0])
	if _, ok := m.activeAgent(); ok {
		t.Fatal("an agent active in an empty list")
	}
}

// TestAgentsGone: the row of an agent removed goes to the next, and the active
// agent gone leaves the pane empty.
func TestAgentsGone(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(100, 41, demoAgents()...)
	sentAgents(m, demoAgents()[0], demoAgents()[1], demoAgents()[3])
	wantRow(t, m, "wise-owl", "│▸ ● wise-owl")
	if strings.Contains(row(t, m, "wise-owl"), "◂") {
		t.Fatalf("another agent became active:\n%s", text(m))
	}
	wantText(t, m, " agent ─", "no agent shown")

	// The last row gone, the cursor stays on the list.
	sentAgents(m, demoAgents()[0], demoAgents()[1])
	wantRow(t, m, "calm-otter", "│▸ ○ calm-otter")
}

func TestAgentsScroll(t *testing.T) {
	t.Parallel()
	var agents []api.Agent
	for i := range 20 {
		agents = append(agents, exited(fmt.Sprintf("agent-%02d", i), 0))
	}
	m := newAgentsModel(80, 16, agents...) // the project pane has 11 rows inside: the heading and 10
	for range 12 {
		m.Update(keyMsgs("down")[0])
	}
	wantRow(t, m, " Agents", "20")
	wantRow(t, m, "agent-12", "▸")
	if strings.Contains(text(m), "agent-02") || !strings.Contains(text(m), "agent-03") {
		t.Fatalf("the cursor at the bottom of what shows:\n%s", text(m))
	}
	for range 10 {
		m.Update(keyMsgs("up")[0])
	}
	if !strings.Contains(row(t, m, "agent-02"), "▸") || !strings.Contains(text(m), "│ Agents") {
		t.Fatalf("the cursor at the top of what shows, under the heading:\n%s", text(m))
	}

	// A click takes the row at the scroll: the second row shown, agent-03.
	m.Update(clickAt(5, 3))
	if a, ok := m.activeAgent(); !ok || a.Name != "agent-03" {
		t.Fatalf("active %+v, %v", a, ok)
	}
}

func TestAgentsClick(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(100, 41, demoAgents()...)

	// On the heading and below the rows, nothing happens.
	m.Update(clickAt(5, 1))
	m.Update(clickAt(5, 20))
	if a, _ := m.activeAgent(); a.Name != "swift-falcon" {
		t.Fatalf("active %s", a.Name)
	}
	m.Update(clickAt(5, 2))
	if a, _ := m.activeAgent(); a.Name != "bold-heron" {
		t.Fatalf("active %s, want the row clicked", a.Name)
	}
	wantRow(t, m, "bold-heron", "│▸ ✕ bold-heron")
}

func TestAgentRow(t *testing.T) {
	t.Parallel()
	st := style.New(false)
	a := exited("a-rather-long-agent-name-indeed", 0)
	tests := []struct {
		width int
		want  string
	}{
		{50, "  ○ a-rather-long-agent-name-indeed    exited 0 ◂ "},
		// The detail keeps its place while the name has room.
		{30, "  ○ a-rather-long… exited 0 ◂ "},
		// Then it gives way to the name.
		{20, "  ○ a-rather-lon… ◂ "},
	}
	for _, tt := range tests {
		got := agentRow(st, a, false, true, tt.width)
		if got != tt.want {
			t.Errorf("width %d: %q, want %q", tt.width, got, tt.want)
		}
	}
	for _, width := range []int{1, 5, 8, 20, 50, 100} {
		if got := agentRow(st, a, true, false, width); width >= 20 && ansi.StringWidth(got) != width {
			t.Errorf("width %d: %q is %d wide", width, got, ansi.StringWidth(got))
		}
	}
}

func (h testHome) newAgent(t *testing.T, project, dir, name string) api.Agent {
	t.Helper()
	r, err := h.client().CreateAgent(context.Background(), api.CreateAgent{Project: &project, Dir: dir, Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	return r.Agent
}

// agentScreen is what the agent pane's agent shows, "" while none is attached.
func agentScreen(m *Model) string {
	if m.agentTerm == nil || m.agentTerm.pane == nil {
		return ""
	}
	return m.agentTerm.pane.Text()
}

// agentAttached reports whether the agent pane shows the agent name live. The
// test shell ignores the command it is given, so an agent there is that shell,
// with its prompt.
func agentAttached(name string) func(*Model) bool {
	return func(m *Model) bool {
		return m.agentTerm != nil && m.agentTerm.name == name && m.agentTerm.live && strings.Contains(agentScreen(m), "$")
	}
}

func agentScreenHas(s string) func(*Model) bool {
	return func(m *Model) bool { return strings.Contains(agentScreen(m), s) }
}

// TestAgentAttached: the first running agent is live in the agent pane, which
// takes the keys and has the pane's size, docked or zoomed; Enter on another
// attaches it alone, and the terminal pane stays as it was.
func TestAgentAttached(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	h.newAgent(t, "api", dir, "a-one")
	h.newAgent(t, "api", dir, "b-two")
	h.newTerminal(t, "api", dir, "shell")
	p := h.openShown(t)
	p.until(t, "a-one attached", agentAttached("a-one"))
	p.until(t, "the terminal attached too", attachedTo("shell"))
	terminalGen := p.m.term.gen

	p.keys("alt+right")
	p.typed("echo he''llo; stty size\n")
	// 128 × 24: the agent pane's frame is 96 wide and, above the terminal
	// pane's 5 rows sharing a border, 19 high.
	p.until(t, "the echo at the pane's size", agentScreenHas("hello\n17 94"))
	if c := p.m.paneCursor(); c == nil || c.X < 32 {
		t.Fatalf("cursor %v, want it in the agent pane", c)
	}
	wantText(t, p.m, "hello")
	if strings.Contains(screenText(p.m), "hello") {
		t.Fatal("the keys reached the terminal too")
	}
	p.keys("C-]")
	p.typed("clear; stty size\n")
	p.until(t, "the zoomed size", agentScreenHas("21 126"))
	p.keys("C-]")

	// Enter in the list attaches b-two and leaves a-one, which keeps running.
	p.keys("alt+left", "down", "enter")
	p.until(t, "b-two attached", agentAttached("b-two"))
	if p.m.layout.focus != project {
		t.Fatalf("focus %s", p.m.layout.focus)
	}
	if p.m.term == nil || p.m.term.gen != terminalGen {
		t.Fatal("want the terminal pane left as it was")
	}
	p.keys("up", "enter")
	p.until(t, "a-one again, as it was", func(m *Model) bool { return agentAttached("a-one")(m) && agentScreenHas("21 126")(m) })
}

// TestAgentExits: an agent that ends shows its last screen, read-only, from
// its history, under its state.
func TestAgentExits(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	h.newAgent(t, "api", dir, "a-one")
	p := h.openShown(t)
	p.until(t, "a-one attached", agentAttached("a-one"))
	p.keys("alt+right")
	p.typed("echo by''e; exit 3\n")
	p.until(t, "its last screen", func(m *Model) bool {
		return m.agentTerm != nil && !m.agentTerm.live && agentScreenHas("bye")(m)
	})
	wantText(t, p.m, "○ a-one exited · exit code 3", "exited 3")
	before := agentScreen(p.m)
	p.typed("x\n")
	if agentScreen(p.m) != before || p.m.paneCursor() != nil {
		t.Fatal("want keys to do nothing, and no cursor, on an agent not running")
	}
}

func TestNewAgent(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	h.newProject(t, "api", api.ProjectStateOpen)
	p := h.openShown(t)
	p.until(t, "no agents", func(m *Model) bool { return strings.Contains(text(m), "no agents: C-q a") })

	p.keys("C-q")
	wantText(t, p.m, "a  new agent", "a agent")
	p.keys("a")
	if p.m.layout.focus != agent {
		t.Fatalf("focus %s", p.m.layout.focus)
	}
	p.until(t, "the agent active", func(m *Model) bool {
		_, ok := m.activeAgent()
		return ok
	})
	first, _ := p.m.activeAgent()
	p.until(t, "it attached", agentAttached(first.Name))

	// A second one takes the pane and the cursor once it comes.
	p.keys("C-q", "a")
	p.until(t, "the new agent active", func(m *Model) bool {
		a, ok := m.activeAgent()
		return ok && a.Name != first.Name
	})
	active, _ := p.m.activeAgent()
	under, _ := p.m.cursorAgent()
	if under.Id != active.Id {
		t.Fatalf("cursor on %s, active %s", under.Name, active.Name)
	}
}

func TestRemoveAgentConfirmed(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := h.newProject(t, "api", api.ProjectStateOpen)
	h.newAgent(t, "api", dir, "a-one")
	h.newAgent(t, "api", dir, "b-two")
	p := h.openShown(t)
	p.until(t, "a-one attached", agentAttached("a-one"))

	// In the list, C-q x takes the agent under the cursor, not the active one.
	p.keys("down", "C-q")
	wantText(t, p.m, "x  remove b-two")
	p.keys("x")
	wantText(t, p.m, "┃ Remove agent", "Remove b-two?", "It is running: it is stopped.")
	p.keys("n")
	if p.m.confirm != nil || len(p.m.agents()) != 2 {
		t.Fatalf("n: confirm %v, agents %v", p.m.confirm, p.m.agents())
	}
	p.keys("C-q", "x", "y")
	p.until(t, "b-two gone", func(m *Model) bool { return m.confirm == nil && len(m.agents()) == 1 })
	if !agentAttached("a-one")(p.m) {
		t.Fatal("want the active agent left as it was")
	}

	// In the agent pane, it takes the active one; the pane empties.
	p.keys("alt+right", "C-q")
	wantText(t, p.m, "x  remove a-one")
	p.keys("x", "y")
	p.until(t, "a-one gone", func(m *Model) bool { return m.confirm == nil && len(m.agents()) == 0 })
	if p.m.agentTerm != nil {
		t.Fatalf("still attached: %+v", p.m.agentTerm)
	}
	wantText(t, p.m, "no agents: C-q a creates one")
	list, err := h.client().ListAgents(context.Background(), "api", "")
	if err != nil || len(list.Agents) != 0 {
		t.Fatalf("agents: %+v, %v", list.Agents, err)
	}
}

// TestAgentsSwitchProject: each project keeps its cursor and active agent, and
// nothing of one shows in the other.
func TestAgentsSwitchProject(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	apiDir := h.newProject(t, "api", api.ProjectStateOpen)
	web := h.newProject(t, "web", api.ProjectStateOpen)
	h.newAgent(t, "api", apiDir, "a-one")
	h.newAgent(t, "api", apiDir, "a-two")
	h.newAgent(t, "web", web, "w-one")
	p := h.openShown(t)
	p.until(t, "a-one attached", agentAttached("a-one"))
	p.keys("down", "enter")
	p.until(t, "a-two attached", agentAttached("a-two"))

	p.keys("alt+shift+right")
	p.until(t, "web's agent attached", agentAttached("w-one"))
	names := func(m *Model) []string {
		var names []string
		for _, a := range m.agents() {
			names = append(names, a.Name)
		}
		return names
	}
	if !slices.Equal(names(p.m), []string{"w-one"}) {
		t.Fatalf("web's agents: %v", names(p.m))
	}
	p.keys("alt+shift+left")
	p.until(t, "a-two again", agentAttached("a-two"))
	under, _ := p.m.cursorAgent()
	if under.Name != "a-two" {
		t.Fatalf("cursor on %s", under.Name)
	}
}
