package tide

import (
	"encoding/json"
	"fmt"
	"os"
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

// Resume is what a tide shows, which C-q r hands to the tide replacing it.
// It crosses releases, so it holds names rather than numbers, and what a
// release does not know it ignores.
type Resume struct {
	Project      string `json:"project"`
	ProjectID    string `json:"project_id"`
	Focus        string `json:"focus"`
	Zoomed       string `json:"zoomed"`
	LastTop      string `json:"last_top"`
	TerminalOpen bool   `json:"terminal_open"`
	Terminal     string `json:"terminal,omitempty"` // the id of the tab selected
	Agent        string `json:"agent,omitempty"`    // the id of the active agent
}

// String is r as the flag carries it.
func (r Resume) String() (string, error) {
	b, err := json.Marshal(r)
	return string(b), err
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

// Reload is what the next run is to show once C-q r quit this one; nil when
// tide just ended.
func (m *Model) Reload() *Resume { return m.reload }

// startReload ends tide so that the cli can replace it with the binary
// installed. A binary that cannot run is found out here, while tide can still
// stay open and say so. The terminals and agents never notice: their holders
// are other processes, and the next run attaches to them again.
func (m *Model) startReload() tea.Cmd {
	if err := runnable(m.executable); err != nil {
		m.log.Info("tide not reloaded", "executable", m.executable, "err", err)
		m.statusNotice = "cannot reload: " + err.Error()
		return nil
	}
	r := &Resume{
		Project:      m.shown,
		Focus:        m.layout.focus.String(),
		Zoomed:       m.layout.zoomed.String(),
		LastTop:      m.layout.lastTop.String(),
		TerminalOpen: m.layout.terminalOpen,
	}
	if f := m.following; f != nil && f.project != nil {
		r.ProjectID = f.project.Id
		r.Terminal = m.selectedTab[f.project.Id]
		if a, ok := m.activeAgent(); ok {
			r.Agent = a.Id
		}
	}
	m.reload = r
	m.log.Info("tide reloading", "executable", m.executable, "project", m.shown)
	m.unfollow() // the daemon ends the stream, and logs it
	m.detachAll()
	return tea.Quit
}

// runnable reports why the file at path cannot be run.
func runnable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s is not executable", path)
	}
	return nil
}

// resumed shows what the run before showed, when its project is still open.
func (m *Model) resumed() (tea.Cmd, bool) {
	r := m.resume
	m.resume = nil
	if r == nil || !m.isOpen(r.Project) {
		return nil, false
	}
	if r.ProjectID != "" {
		if r.Terminal != "" {
			m.selectedTab[r.ProjectID] = r.Terminal
		}
		// Active once the stream brings it, as an agent created here is; the
		// first running one must not take its place, nor take it when none was.
		m.agentLists[r.ProjectID] = &agentList{closed: map[string]bool{}, created: r.Agent, opened: true}
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
