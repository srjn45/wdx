package tide

import (
	"slices"
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
	rows := []valueRow{
		name,
		{key: "id", values: []string{a.Id}},
		{key: "aca", values: []string{a.Aca}},
		{key: "model", values: present(a.Model, identical)},
		{key: "requested_model", values: present(a.RequestedModel, identical)},
		{key: "permissions", values: []string{string(a.Permissions)}},
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
		{key: "limited", values: present(a.Limited, func(l api.AgentLimited) string { return api.LimitedText(a.Aca, l, m.now()) }), paint: m.st.Failure},
	}
	rows = append(rows, promptRows(a)...)
	return append(rows,
		valueRow{key: "draft", values: present(a.Draft, yesNo)},
		valueRow{key: "queued", values: present(a.Queued, strconv.Itoa)},
		valueRow{key: "held", values: []string{strconv.Itoa(a.Held)}},
		valueRow{key: "context", values: present(a.Context, api.ContextText), paint: m.contextPaint(a)},
	)
}

// promptRows are the rows of what a asks, only while a prompt shows: its kind and
// title, its question, its detail and its options, ❯ before the selected one and
// a box before each of a multi-select, ticked or not.
func promptRows(a api.Agent) []valueRow {
	if a.Prompt == nil {
		return nil
	}
	p := *a.Prompt
	kind := promptKindText(p.Kind)
	if p.Title != nil {
		kind += " · " + *p.Title
	}
	var detail []string
	if p.Detail != nil {
		detail = strings.Split(*p.Detail, "\n")
	}
	options := make([]string, 0, len(p.Options))
	for i, option := range p.Options {
		mark := "  "
		if p.Selected != nil && *p.Selected == i {
			mark = "❯ "
		}
		if p.Multi {
			mark += promptBox(p, i)
		}
		options = append(options, mark+option)
	}
	return []valueRow{
		{key: "prompt", values: []string{kind}},
		{key: "question", values: []string{p.Question}},
		{key: "detail", values: detail},
		{key: "options", values: options},
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

// renameAgentAsked is the name typed in the popup r opens on an agent.
type renameAgentAsked struct{ id, name string }

// agentRenamed is the daemon's answer to it; the name comes with the stream.
type agentRenamed struct{ err error }

// renamableAgent is the agent r renames: the one selected, unless it was removed.
func (m *Model) renamableAgent() (api.Agent, bool) {
	a, ok := m.focusedAgent()
	return a, ok && a.State != api.AgentStateRemoved
}

// askRenameAgent opens the popup that renames a, with its name to edit, as its
// info tab's field does.
func (m *Model) askRenameAgent(a api.Agent) {
	m.switcher = nil
	m.push(&fieldScreen{
		heading: "Rename agent",
		prompt:  "New name of " + a.Name + ":",
		value:   a.Name,
		empty:   "type a name",
		working: "renaming…",
		submit:  func(name string) tea.Msg { return renameAgentAsked{id: a.Id, name: name} },
	})
	m.log.Info("agent rename opened", "agent_id", a.Id)
}

// sendAgentRename asks the daemon for the name typed in the popup.
func (m *Model) sendAgentRename(msg renameAgentAsked) tea.Cmd {
	if m.client == nil {
		return nil
	}
	m.log.Info("agent rename asked", "agent_id", msg.id, "name", msg.name)
	return func() tea.Msg {
		_, err := m.client.RenameAgent(m.ctx, "id:"+msg.id, "", "", msg.name)
		return agentRenamed{err: err}
	}
}

// agentRenamed closes the popup, or shows there why the name was refused, to
// correct it or give up.
func (m *Model) agentRenamed(msg agentRenamed) tea.Cmd {
	if msg.err != nil {
		m.log.Info("agent not renamed", "err", msg.err)
		if s := m.openTop(); s != nil {
			return s.update(screenFailed(msg))
		}
		m.statusNotice = msg.err.Error()
		return nil
	}
	m.log.Info("agent renamed")
	m.opening = nil
	return nil
}

// valuesSync keeps the pane's memory to the tab shown: leaving the tab or the pane
// ends an edit, and the settings are read when their tab comes to show.
func (m *Model) valuesSync() tea.Cmd {
	tab := ""
	if l := m.list(); l != nil && (isInfoTab(l.active) || isPlanTab(l.active) || l.active == settingsTab || l.active == statsTab) {
		tab = l.active
	}
	var cmd tea.Cmd
	if tab != m.values.owner {
		m.values = valuePane{owner: tab}
		switch tab {
		case settingsTab:
			cmd = m.listSettings()
		case statsTab:
			cmd = m.listUsage()
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
	if m.statsShown() {
		return m.statsRows(), true
	}
	if a, ok := m.activeAgent(); ok && m.infoShown() {
		return m.infoRows(a), true
	}
	if _, ok := m.planTabView(); m.planShown() && !ok {
		return m.planTabRows(), true
	}
	return nil, false
}

// valueClick takes a click at the column and row of the pane's area.
func (m *Model) valueClick(col, row int, area uv.Rectangle) tea.Cmd {
	if v, ok := m.planTabView(); ok {
		m.planTabClick(v, row, area)
		return nil
	}
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

// promptBox is the box before option i of a multi-select: ticked or not.
func promptBox(p api.AgentPrompt, i int) string {
	if slices.Contains(p.Ticked, i) {
		return "[✔] "
	}
	return "[ ] "
}
