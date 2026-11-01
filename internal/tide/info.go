package tide

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// An agent's info is a tab of the agent pane, showing what the project's stream
// says of the agent, with its name editable (docs/TIDE_INFO.md).

const (
	infoKey       = "i" // of the menu
	infoTabPrefix = "info:"
	infoLabel     = "info: "
)

// infoTab is the id of the agent's info tab; an agent's own tab is its id.
func infoTab(agentID string) string { return infoTabPrefix + agentID }

func isInfoTab(tab string) bool { return strings.HasPrefix(tab, infoTabPrefix) }

// tabAgent is the id of the agent a tab is of.
func tabAgent(tab string) string { return strings.TrimPrefix(tab, infoTabPrefix) }

// infoShown reports whether the agent pane shows an agent's info.
func (m *Model) infoShown() bool {
	l := m.list()
	return l != nil && isInfoTab(l.active)
}

// infoTarget returns the agent C-q i would show the info of: the one in focus,
// unless its info is what has the focus.
func (m *Model) infoTarget() (api.Agent, bool) {
	if m.layout.focus == agent && m.infoShown() {
		return api.Agent{}, false
	}
	return m.focusedAgent()
}

// openInfo opens a's info tab, or goes to it, and focuses it: it is opened to be
// read.
func (m *Model) openInfo(a api.Agent) {
	m.list().openTab(infoTab(a.Id))
	m.layout.setFocus(agent)
	if m.layout.zoomed != none {
		m.layout.zoomed = agent
	}
	m.changed("agent info")
	m.log.Info("agent info opened", "agent_id", a.Id)
}

// infoRows are the rows of a's info.
func (m *Model) infoRows(a api.Agent) []valueRow {
	name := valueRow{key: "name", values: []string{a.Name}, verb: "rename"}
	if a.State != api.AgentStateRemoved {
		name.save = m.renameAgent(a.Id)
	}
	var children []string
	for _, c := range childrenOf(a) {
		children = append(children, c.Name)
	}
	return []valueRow{
		name,
		{key: "id", values: []string{a.Id}},
		{key: "aca", values: []string{a.Aca}},
		{key: "model", values: present(a.Model, identical)},
		{key: "project_name", values: []string{a.ProjectName}, gap: true},
		{key: "dir", values: []string{a.Dir}},
		{key: "current_dir", values: present(a.CurrentDir, identical)},
		{key: "worktree", values: present(a.Worktree, identical)},
		{key: "parent_agent_name", values: present(a.ParentAgentName, identical)},
		{key: "children", values: children},
		{key: "state", values: []string{string(a.State)}, gap: true},
		{key: "exit_code", values: present(a.ExitCode, strconv.Itoa)},
		{key: "error", values: present(a.Error, identical)},
		{key: "activity", values: present(a.Activity, func(v api.AgentActivity) string { return string(v) })},
		{key: "draft", values: present(a.Draft, yesNo)},
		{key: "queued", values: present(a.Queued, strconv.Itoa)},
	}
}

// present is v as the lines of a row, none when it is nil.
func present[T any](v *T, text func(T) string) []string {
	if v == nil {
		return nil
	}
	return []string{text(*v)}
}

func identical(s string) string { return s }

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// renameAgent is what saves the name of the agent with id.
func (m *Model) renameAgent(id string) func(string) tea.Cmd {
	return func(name string) tea.Cmd {
		owner := m.values.owner
		m.log.Info("agent rename asked", "agent_id", id, "name", name)
		return func() tea.Msg {
			_, err := m.client.RenameAgent(m.ctx, "id:"+id, "", "", name)
			return valueSaved{owner: owner, err: err}
		}
	}
}

// valuesSync keeps the pane's memory to the tab shown: leaving the tab or the pane
// ends an edit, and the settings are read when their tab comes to show.
func (m *Model) valuesSync() tea.Cmd {
	tab := ""
	if l := m.list(); l != nil && (isInfoTab(l.active) || l.active == settingsTab) {
		tab = l.active
	}
	var cmd tea.Cmd
	if tab != m.values.owner {
		m.values = valuePane{owner: tab}
		if tab == settingsTab {
			cmd = m.listSettings()
		}
	}
	if m.layout.focus != agent {
		m.values.edit = nil
	}
	return cmd
}

// valueRows returns the rows the agent pane shows, when it shows a value pane.
func (m *Model) valueRows() ([]valueRow, bool) {
	if m.settingsShown() {
		return m.settingRows(), true
	}
	if a, ok := m.activeAgent(); ok && m.infoShown() {
		return m.infoRows(a), true
	}
	return nil, false
}

// valueClick takes a click at the column and row of the pane's area.
func (m *Model) valueClick(col, row int, area uv.Rectangle) tea.Cmd {
	rows, ok := m.valueRows()
	if !ok {
		return nil
	}
	return m.values.click(rows, col, row, area.Dx(), area.Dy(), m.st)
}

// drawInfo draws a's info in area.
func (m *Model) drawInfo(buf uv.Screen, area uv.Rectangle, st style.Style, a api.Agent) {
	m.values.draw(buf, area, m.infoRows(a), m.layout.focus == agent, st)
}

// valueSavedMsg logs and takes the answer to a save.
func (m *Model) valueSavedMsg(msg valueSaved) {
	if msg.err != nil {
		m.log.Info("value not saved", "owner", msg.owner, "err", msg.err)
	}
	m.values.saved(msg)
}
