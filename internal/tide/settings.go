package tide

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// The settings are a tab of the agent pane in each project, read from the daemon
// when the tab comes to show (docs/TIDE_SETTINGS.md).

const (
	settingsKey = "s" // of the menu
	// settingsTab is the settings' tab, which is of no agent.
	settingsTab = "settings"
)

// settingsListed is the answer to listing the settings.
type settingsListed struct {
	settings []api.Setting
	err      error
}

// settingSaved is the answer to changing a setting.
type settingSaved struct {
	valueSaved
	setting api.Setting
}

// settingsShown reports whether the agent pane shows the settings.
func (m *Model) settingsShown() bool {
	l := m.list()
	return l != nil && l.active == settingsTab
}

// settingsOffered reports whether C-q s applies: a tab needs a project, and
// nothing moves when they are what has the focus.
func (m *Model) settingsOffered() bool {
	return m.list() != nil && (m.layout.focus != agent || !m.settingsShown())
}

// openSettings opens the settings' tab, or goes to it, and focuses it.
func (m *Model) openSettings() {
	m.list().openTab(settingsTab)
	m.layout.setFocus(agent)
	if m.layout.zoomed != none {
		m.layout.zoomed = agent
	}
	m.changed("settings")
	m.log.Info("settings opened")
}

// listSettings reads the settings.
func (m *Model) listSettings() tea.Cmd {
	m.settingsErr = ""
	return func() tea.Msg {
		r, err := m.client.ListSettings(m.ctx)
		return settingsListed{settings: r.Settings, err: err}
	}
}

func (m *Model) settingsListed(msg settingsListed) {
	if msg.err != nil {
		m.log.Info("settings not listed", "err", msg.err)
		m.settings, m.settingsErr = nil, msg.err.Error()
		return
	}
	m.settings = msg.settings
}

// settingSaved shows the setting as the daemon answered, and ends its edit.
func (m *Model) settingSaved(msg settingSaved) {
	if msg.err != nil {
		m.log.Info("setting not saved", "name", msg.setting.Key, "err", msg.err)
	}
	for i, s := range m.settings {
		if msg.err == nil && s.Key == msg.setting.Key {
			m.settings[i] = msg.setting
		}
	}
	if msg.err == nil && msg.setting.Key == sectionSettingKey {
		m.setSectionOrder(msg.setting.Value)
	}
	if msg.err == nil && msg.setting.Key == themeSettingKey {
		m.st = m.st.WithTheme(msg.setting.Value)
		m.log.Info("theme changed", "theme", msg.setting.Value)
	}
	if msg.err == nil && m.thresholds.set(msg.setting.Key, msg.setting.Value) {
		m.log.Info("context thresholds changed", "warning", m.thresholds.warning, "critical", m.thresholds.critical)
	}
	m.values.saved(msg.valueSaved)
}

// settingRows are the rows of the settings.
func (m *Model) settingRows() []valueRow {
	rows := make([]valueRow, 0, len(m.settings))
	for _, s := range m.settings {
		r := valueRow{key: s.Key, note: s.Description, verb: "edit"}
		if s.Value != "" {
			r.values = []string{s.Value}
		}
		if s.Choices != nil {
			r.choices = *s.Choices
		}
		if s.Editable {
			r.save = m.saveSetting(s.Key)
		}
		rows = append(rows, r)
	}
	return rows
}

// saveSetting is what saves the setting key.
func (m *Model) saveSetting(key string) func(string) tea.Cmd {
	return func(value string) tea.Cmd {
		value = strings.TrimSpace(value)
		owner := m.values.owner
		m.log.Info("setting change asked", "name", key, "value", value)
		return func() tea.Msg {
			r, err := m.client.SetSetting(m.ctx, key, value)
			return settingSaved{valueSaved: valueSaved{owner: owner, err: err}, setting: r.Setting}
		}
	}
}

// drawSettings draws the settings in area, or why there are none.
func (m *Model) drawSettings(buf uv.Screen, area uv.Rectangle, st style.Style) {
	switch {
	case m.settingsErr != "":
		uv.NewStyledString(st.Failure("settings could not be read: "+m.settingsErr)).Draw(buf, area)
	case len(m.settings) == 0:
		uv.NewStyledString(st.Muted("loading…")).Draw(buf, area)
	default:
		m.values.draw(buf, area, m.settingRows(), m.layout.focus == agent, st)
	}
}
