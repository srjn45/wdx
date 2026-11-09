package tide

import (
	"encoding/json"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	// VersionCheckEvery is how often a view that stays open asks the daemon its
	// version: an update restarts the daemon and nothing tells the view.
	VersionCheckEvery = 30 * time.Second
	// VersionCheckTimeout bounds an answer, so that a stuck daemon shows as
	// one that does not answer.
	VersionCheckTimeout = 5 * time.Second
)

// The messages of asking the daemon its version.
type (
	daemonVersionMsg   struct{ version string }
	checkDaemonVersion struct{}
)

// checkDaemonVersion asks the daemon its version. No answer shows only the
// version running here.
func (m *Model) checkDaemonVersion() tea.Cmd {
	if m.versionOf == nil {
		return nil
	}
	return func() tea.Msg {
		version, _ := m.versionOf(m.ctx)
		return daemonVersionMsg{version}
	}
}

// DaemonVersion is what the daemon last answered, "" when it did not.
func (m *Model) DaemonVersion() string { return m.daemonVersion }

// Resume is what a tide showed, which an older release's C-q r hands through
// --resume to the tide replacing it: this one has no reload, as quitting and
// starting again shows the same from the daemon's view, but it must keep taking
// it. It crosses releases, so it holds names rather than numbers, and what a
// release does not know it ignores.
type Resume struct {
	Project      string `json:"project"`
	ProjectID    string `json:"project_id"`
	Focus        string `json:"focus"`
	Zoomed       string `json:"zoomed"`
	LastTop      string `json:"last_top"`
	TerminalOpen bool   `json:"terminal_open"`
	Terminal     string `json:"terminal,omitempty"` // the id of the tab selected
	Agent        string `json:"agent,omitempty"`    // the tab current: an agent's id, "info:" and its id, or settings
	// Tabs are the tabs open, as Agent is. A release that does not know one
	// drops it, as it does a tab whose agent is gone.
	Tabs []string `json:"tabs,omitempty"`
	// CodeWorktree is the worktree the Code section shows.
	CodeWorktree string `json:"code_worktree,omitempty"`
}

// ParseResume reads what String wrote. Without a usable one tide starts as
// it would otherwise.
func ParseResume(s string) (*Resume, error) {
	var r Resume
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// resumed shows what the run before showed, when its project is still open.
func (m *Model) resumed() (tea.Cmd, bool) {
	r := m.resume
	if v := m.viewResume(); v != nil {
		r = v // the daemon's, which the next release keeps: --resume is for one before it
	}
	m.resume = nil
	if r == nil || !m.isOpen(r.Project) {
		return nil, false
	}
	if m.node != "" {
		// No settings through a session.
		kept := *r
		r = &kept
		r.Tabs = slices.DeleteFunc(slices.Clone(r.Tabs), func(tab string) bool { return tab == settingsTab })
		if r.Agent == settingsTab {
			r.Agent = ""
		}
	}
	if r.ProjectID != "" {
		if r.Terminal != "" {
			m.selectedTab[r.ProjectID] = r.Terminal
		}
		// Active once the stream brings it, as an agent created here is; the
		// first running one must not take its place, nor take it when none was.
		m.agentLists[r.ProjectID] = &agentList{closed: map[string]bool{}, tabs: r.Tabs, created: r.Agent, opened: true, code: codeSection{worktree: r.CodeWorktree}}
	}
	cmd := m.show(r.Project)
	m.layout.terminalOpen = r.TerminalOpen
	m.layout.lastTop = paneNamed(r.LastTop, m.layout.lastTop)
	m.layout.zoomed = paneNamed(r.Zoomed, m.layout.zoomed)
	m.layout.focus = paneNamed(r.Focus, m.layout.focus)
	m.log.Info("tide starts", "on", "the project before the reload", "project", r.Project, "focus", r.Focus)
	return cmd, true
}

func (m *Model) isOpen(name string) bool {
	for _, n := range m.projects.open() {
		if n == name {
			return true
		}
	}
	return false
}

// paneNamed is the pane named name, or otherwise when there is none.
func paneNamed(name string, otherwise pane) pane {
	for _, p := range append([]pane{none}, panes...) {
		if p.String() == name {
			return p
		}
	}
	return otherwise
}
