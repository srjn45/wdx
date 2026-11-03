package tide

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
)

// What tide shows besides its tabs, kept by the daemon so that tide comes back
// as it was left (docs/TIDE_STATE.md). The daemon reads none of it: these
// structs are its only reader, and a key they lack means what tide does today.

const (
	// defaultViewSettle is how long a view stays the same before it is sent:
	// holding a key sends one view, not one per row.
	defaultViewSettle = 500 * time.Millisecond
	// viewFlush is how long quitting or reloading waits for the view to be sent.
	viewFlush = time.Second
)

// tideView is what tide shows as a whole.
type tideView struct {
	ProjectID    string `json:"project_id,omitempty"`
	Focus        string `json:"focus,omitempty"`
	Zoomed       string `json:"zoomed,omitempty"`
	LastTop      string `json:"last_top,omitempty"`
	TerminalOpen bool   `json:"terminal_open,omitempty"`
}

// projectView is what tide shows of a project besides its tabs.
type projectView struct {
	Tab      string `json:"tab,omitempty"`      // the current one
	Terminal string `json:"terminal,omitempty"` // the id of the terminal tab selected
	Section  string `json:"section,omitempty"`  // the cursor's, a letter as tide.section_order has it
	// The row the cursor is on in each section, held as each follows its row.
	Agent    string `json:"agent,omitempty"`
	Worktree string `json:"worktree,omitempty"`
	Doc      string `json:"doc,omitempty"`
	Code     string `json:"code,omitempty"`

	Folded        []string            `json:"folded,omitempty"`         // the agents folded
	WorktreesOpen []string            `json:"worktrees_open,omitempty"` // the worktrees unfolded
	CodeWorktree  string              `json:"code_worktree,omitempty"`
	CodeOpen      map[string][]string `json:"code_open,omitempty"` // by worktree, the directories unfolded
}

// viewState is how far the views are from what the daemon has.
type viewState struct {
	settle      time.Duration
	read        bool   // tide's view was read at start; none is sent before
	built       []byte // the views as last built, tide's then the project's
	changes     int    // how many times they changed, numbering the waits
	saving      bool   // a sending is on its way
	again       bool   // they settled while one was: send once it is back
	sentTide    []byte
	sentProject map[string][]byte // by project id
}

// The messages of keeping the view.
type (
	viewSettled struct{ change int }
	viewSent    struct {
		tide      []byte // what was sent, nil for what was not
		projectID string
		project   []byte
		err       error
	}
	// tideViewRead is tide's view as the daemon had it at start, and the first
	// list of the projects, which decides with it where tide starts.
	tideViewRead struct {
		view   tideView
		err    error
		listed projectsListed
	}
)

// sorted are the keys set in set, in order, so that the same set is the same JSON.
func sorted(set map[string]bool) []string {
	var keys []string
	for k, on := range set {
		if on {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

// setOf is the set of keys.
func setOf(keys []string) map[string]bool {
	set := map[string]bool{}
	for _, k := range keys {
		set[k] = true
	}
	return set
}

// currentTideView is what tide shows as a whole now.
func (m *Model) currentTideView() tideView {
	v := tideView{Focus: m.layout.focus.String(), Zoomed: m.layout.zoomed.String(), LastTop: m.layout.lastTop.String(), TerminalOpen: m.layout.terminalOpen}
	if f := m.following; f != nil && f.project != nil {
		v.ProjectID = f.project.Id
	}
	return v
}

// currentProjectView is what tide shows of the project shown, with its id; false
// with none shown, or before its view was read.
func (m *Model) currentProjectView() (string, projectView, bool) {
	l := m.list()
	if l == nil || !l.viewRead {
		return "", projectView{}, false
	}
	id := m.following.project.Id
	v := projectView{
		Tab: l.active, Terminal: m.selectedTab[id], Section: l.section.letter(),
		Agent: l.cursor, Worktree: l.worktreeKey, Doc: l.docCursor, Code: l.code.cursor,
		Folded: sorted(l.closed), WorktreesOpen: sorted(l.worktreesOpen), CodeWorktree: l.code.worktree,
	}
	for _, name := range slices.Sorted(maps.Keys(l.code.files)) {
		if open := sorted(l.code.files[name].open); len(open) > 0 {
			if v.CodeOpen == nil {
				v.CodeOpen = map[string][]string{}
			}
			v.CodeOpen[name] = open
		}
	}
	return id, v, true
}

// applyProjectView puts what the daemon kept of the project in its list, before
// the first event's rows come: each section's cursor then finds its row, or the
// nearest, as rows come and go.
func (m *Model) applyProjectView(projectID string, l *agentList, v projectView) {
	if v.Tab != "" {
		l.created = v.Tab // current once the stream brings it among the tabs
	}
	if v.Terminal != "" {
		m.selectedTab[projectID] = v.Terminal
	}
	for _, r := range v.Section {
		if s, ok := sectionLetters[r]; ok {
			l.section = s
		}
	}
	l.cursor, l.worktreeKey, l.docCursor, l.code.cursor = v.Agent, v.Worktree, v.Doc, v.Code
	l.closed, l.worktreesOpen = setOf(v.Folded), setOf(v.WorktreesOpen)
	if v.CodeWorktree != "" {
		l.code.worktree = v.CodeWorktree
	}
	for name, open := range v.CodeOpen {
		if l.code.files == nil {
			l.code.files = map[string]*codeFiles{}
		}
		l.code.files[name] = &codeFiles{open: setOf(open)}
	}
	m.log.Info("project view restored", "project_id", projectID, "tab", v.Tab, "section", v.Section)
}

// readProjectView reads what the daemon kept of the project ref; nil when it
// kept nothing, or the read failed, which leaves tide as it would be.
func (m *Model) readProjectView(ctx context.Context, ref string) *projectView {
	if m.client == nil {
		return nil
	}
	b, err := m.client.ProjectView(ctx, ref)
	var v projectView
	if err == nil {
		err = json.Unmarshal(b, &v)
	}
	if err != nil {
		m.log.Error("project view not read", "project", ref, "err", err)
		return nil
	}
	if string(b) == "{}" {
		return nil
	}
	return &v
}

// viewSync starts the wait after the views changed; the views go once it ends
// with nothing changed since.
func (m *Model) viewSync() tea.Cmd {
	if m.client == nil || !m.view.read {
		return nil
	}
	built := m.builtViews()
	if string(built) == string(m.view.built) {
		return nil
	}
	m.view.built = built
	m.view.changes++
	change := m.view.changes
	return m.tick(m.view.settle, func(time.Time) tea.Msg { return viewSettled{change: change} })
}

// builtViews are both views as JSON, to tell a change by.
func (m *Model) builtViews() []byte {
	_, project, _ := m.currentProjectView()
	b, _ := json.Marshal([]any{m.currentTideView(), project})
	return b
}

// viewSettledFor sends the views when they stayed the same through the wait.
func (m *Model) viewSettledFor(msg viewSettled) tea.Cmd {
	if msg.change != m.view.changes {
		return nil // changed again since: a later wait sends it
	}
	if m.view.saving {
		m.view.again = true
		return nil
	}
	return m.sendViews(m.ctx)
}

// sendViews sends the views that differ from what the daemon has, in ctx.
func (m *Model) sendViews(ctx context.Context) tea.Cmd {
	sent := viewSent{}
	if b, _ := json.Marshal(m.currentTideView()); string(b) != string(m.view.sentTide) {
		sent.tide = b
	}
	if id, v, ok := m.currentProjectView(); ok {
		if b, _ := json.Marshal(v); string(b) != string(m.view.sentProject[id]) {
			sent.projectID, sent.project = id, b
		}
	}
	if sent.tide == nil && sent.project == nil {
		return nil
	}
	m.view.saving = true
	m.log.Debug("view sent", "whole", sent.tide != nil, "project_id", sent.projectID)
	return func() tea.Msg {
		if sent.tide != nil {
			sent.err = m.client.ReplaceTideView(ctx, sent.tide)
		}
		if sent.project != nil && sent.err == nil {
			sent.err = m.client.ReplaceProjectView(ctx, "id:"+sent.projectID, sent.project)
		}
		return sent
	}
}

// viewWasSent ends a sending, and sends again what settled meanwhile. A failure
// is not shown: the view is a convenience, and the next change sends it again.
func (m *Model) viewWasSent(msg viewSent) tea.Cmd {
	m.view.saving = false
	if msg.err != nil {
		m.log.Error("view not saved", "err", msg.err)
	} else {
		if msg.tide != nil {
			m.view.sentTide = msg.tide
		}
		if msg.project != nil {
			m.view.sentProject[msg.projectID] = msg.project
		}
	}
	if m.view.again {
		m.view.again = false
		return m.sendViews(m.ctx)
	}
	return nil
}

// flushViews sends what differs at once, as tide quits or reloads, waiting at most
// viewFlush; then quits. The tabs go first: a list still on its way is waited for,
// and what changed since it went is sent after it, so that the older never wins.
func (m *Model) flushViews() tea.Cmd {
	if m.client == nil {
		return tea.Quit
	}
	ctx, cancel := context.WithTimeout(m.ctx, viewFlush)
	tabs := m.flushTabs(ctx)
	var views tea.Cmd
	if m.view.read {
		views = m.sendViews(ctx)
	}
	if tabs == nil && views == nil {
		cancel()
		return tea.Quit
	}
	return func() tea.Msg {
		defer cancel()
		if tabs != nil {
			if err := tabs(); err != nil {
				m.log.Error("tabs not saved on quitting", "err", err)
			}
		}
		if views != nil {
			if msg, ok := views().(viewSent); ok && msg.err != nil {
				m.log.Error("view not saved on quitting", "err", msg.err)
			}
		}
		return tea.QuitMsg{}
	}
}

// flushTabs returns what sends the shown project's tabs when they differ from
// what was sent, after the list on its way; nil when nothing differs.
func (m *Model) flushTabs(ctx context.Context) func() error {
	l := m.list()
	if l == nil || !l.tabsRead {
		return nil
	}
	tabs, sent, inFlight := savable(l.tabs), l.savedTabs, l.tabsInFlight
	if inFlight != nil {
		sent = l.sendingTabs
	}
	if slices.Equal(tabs, sent) {
		return nil
	}
	id, list := m.following.project.Id, apiTabs(tabs)
	m.log.Info("tabs sent on quitting", "project_id", id, "count", len(tabs), "after_one_on_its_way", inFlight != nil)
	return func() error {
		if inFlight != nil {
			select {
			case <-inFlight:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		_, err := m.client.ReplaceTabs(ctx, "id:"+id, list)
		return err
	}
}

// readTideView reads tide's view as the daemon has it, then lists the projects.
func (m *Model) readTideView() tea.Cmd {
	list := m.listProjects()
	return func() tea.Msg {
		var read tideViewRead
		b, err := m.client.TideView(m.ctx)
		if err == nil {
			err = json.Unmarshal(b, &read.view)
		}
		read.err = err
		read.listed, _ = list().(projectsListed)
		return read
	}
}

// tideViewWasRead starts tide where the view says, once the projects are listed.
func (m *Model) tideViewWasRead(msg tideViewRead) tea.Cmd {
	m.view.read = true
	if msg.err != nil {
		m.log.Error("tide view not read", "err", msg.err)
	} else {
		m.startView = &msg.view
	}
	return m.listed(msg.listed)
}

// viewResume is the start the daemon's view gives, as C-q r would; nil when it
// names no project still open. The project's own view comes with its stream.
func (m *Model) viewResume() *Resume {
	v := m.startView
	if v == nil || v.ProjectID == "" {
		return nil
	}
	for _, p := range m.projects.projects {
		if p.Id == v.ProjectID && m.isOpen(p.Name) {
			return &Resume{Project: p.Name, Focus: v.Focus, Zoomed: v.Zoomed, LastTop: v.LastTop, TerminalOpen: v.TerminalOpen}
		}
	}
	return nil
}
