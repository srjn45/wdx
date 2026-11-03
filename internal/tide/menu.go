package tide

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// The C-q menu is in parts, one per level, the narrowest first (docs/TIDE_MENU.md):
// what is selected, the left pane, then tide and the project. No two parts shown
// together share a letter, since the left pane takes them without C-q.

// The menu's letters, besides those of the Code section (codefiles.go), C-q's own
// and those named where their action is.
const (
	removeKey        = "x"
	newChildKey      = "c"
	newAgentKey      = "a"
	newTerminalKey   = "t"
	projectMenuKey   = "p"
	quitKey          = "q"
	openProjectKey   = "o" // of the Project submenu
	switchProjectKey = "s" // of the Project submenu
	closeProjectKey  = "c" // of the Project submenu
)

// menuItem is a line of the menu: its key as Bubble Tea names it, what it does,
// the word the status line uses for it, and the action; nil for a line that only
// says what C-q then a key does.
type menuItem struct {
	key, label, short string
	do                func() tea.Cmd
}

// menuPart is a level of the menu, under its title.
type menuPart struct {
	title string
	items []menuItem
}

// menuParts are what the menu shows now: the Project submenu's line while it is
// open, else the parts, each with the lines that apply now and none without one.
func (m *Model) menuParts() []menuPart {
	if m.projectMenu {
		return []menuPart{m.projectPart()}
	}
	var parts []menuPart
	for _, p := range []menuPart{m.selectionPart(), m.panePart(), m.globalPart()} {
		if len(p.items) > 0 {
			parts = append(parts, p)
		}
	}
	return parts
}

// menuItems are the lines of every part shown.
func (m *Model) menuItems() []menuItem {
	var items []menuItem
	for _, p := range m.menuParts() {
		items = append(items, p.items...)
	}
	return items
}

// menuOffers reports whether the menu has a line for the key now. With the
// project pane in focus such a key acts without the menu: nothing there takes
// typing, as the programs in the other panes do.
func (m *Model) menuOffers(name string) bool {
	_, ok := m.menuLine(name)
	return ok
}

func (m *Model) menuLine(name string) (menuItem, bool) {
	for _, it := range m.menuItems() {
		if it.key == name && it.do != nil {
			return it, true
		}
	}
	return menuItem{}, false
}

// menuAction does what the menu's line for the key does, if it has one now.
func (m *Model) menuAction(name string) tea.Cmd {
	if it, ok := m.menuLine(name); ok {
		return it.do()
	}
	return nil
}

// openMenu opens the menu, or its Project submenu.
func (m *Model) openMenu(project bool) {
	m.menu, m.projectMenu = true, project
}

// closeMenu closes the menu and its submenu.
func (m *Model) closeMenu() {
	m.menu, m.projectMenu = false, false
}

// selectionPart is what acts on the thing selected: the row under the cursor in
// the left pane, the tab in the others.
func (m *Model) selectionPart() menuPart {
	var items []menuItem
	add := func(key, label, short string, do func() tea.Cmd) {
		items = append(items, menuItem{key, label, short, do})
	}
	if r, ok := m.removable(); ok {
		add(removeKey, "remove", "remove", func() tea.Cmd {
			m.confirm = &confirmBox{r: r}
			m.log.Info("removal asked", "kind", r.kind, "name", r.name)
			return nil
		})
	}
	if a, ok := m.childParent(); ok {
		add(newChildKey, "new child", "child", func() tea.Cmd {
			m.switcher, m.opening = nil, nil
			m.child = &childBox{parent: a}
			m.log.Info("child asked", "parent", a.Name)
			return nil
		})
	}
	if a, ok := m.infoTarget(); ok {
		add(infoKey, "info", "info", func() tea.Cmd {
			m.switcher, m.opening = nil, nil
			m.openInfo(a)
			return nil
		})
	}
	if r, ok := m.restartable(); ok {
		add(restartKey, "new session", "restart", func() tea.Cmd {
			m.switcher, m.opening = nil, nil
			m.confirm = &confirmBox{r: r}
			m.log.Info("restart asked", "name", r.name)
			return nil
		})
	}
	if s, ok := m.startable(); ok {
		add(startKey, "start again", "start", func() tea.Cmd {
			m.switcher, m.opening = nil, nil
			return m.startAgain(s)
		})
	}
	items = append(items, m.codeItems()...)
	if m.verifiable() {
		add(verifyKey, "verify", "verify", m.verifyRow)
	}
	if t, ok := m.closableTab(); ok {
		add(closeTabKey, "close tab", "close tab", func() tea.Cmd {
			m.askCloseTab(t.tab, "asked")
			return nil
		})
	}
	if m.programInFocus() {
		send := func(key string) func() tea.Cmd { return func() tea.Cmd { return m.sendToProgram(key) } }
		add(`\`, `send C-\ to the program`, "send", send(`\`))
		add("]", "send C-] to the program", "send", send("]"))
		add(menuKey, "send C-q to the program", "send", send(menuKey))
		add("M-arrow", "send Alt+arrow, with or without Shift, to the program", "send", nil)
	}
	return menuPart{title: m.selectionTitle(), items: items}
}

// codeItems are the Code section's lines, with the cursor there.
func (m *Model) codeItems() []menuItem {
	l := m.list()
	if m.layout.focus != project || l == nil || l.section != sectionCode || m.codeFilesShown() == nil {
		return nil
	}
	var items []menuItem
	n, entry := m.codeEntry()
	if entry && !n.dir {
		items = append(items, menuItem{editFileKey, "edit", "edit", func() tea.Cmd {
			m.openCodeFile(n.path)
			return m.askEditor(m.list().active)
		}})
	}
	items = append(items,
		menuItem{newFileKey, "new file", "new file", func() tea.Cmd { m.newCodeEntry(false); return nil }},
		menuItem{newDirKey, "new directory", "new dir", func() tea.Cmd { m.newCodeEntry(true); return nil }})
	if entry {
		items = append(items,
			menuItem{renameKey, "rename", "rename", func() tea.Cmd { m.renameCodeEntry(); return nil }},
			menuItem{deleteFileKey, "delete", "delete", func() tea.Cmd { m.deleteCodeEntry(); return nil }})
	}
	return items
}

// selectionTitle names what the selection part acts on.
func (m *Model) selectionTitle() string {
	switch m.layout.focus {
	case terminal:
		if t, ok := m.selectedTerminal(); ok {
			return t.Name + " (terminal)"
		}
	case agent:
		t, ok := m.closableTab()
		switch {
		case !ok:
		case m.editShown():
			return t.title() + " (editor)"
		case t.marked() && !isInfoTab(t.tab) && !isTextTab(t.tab):
			return t.title() + " (agent)"
		default:
			return t.title()
		}
	case project:
		return m.listSelectionTitle()
	}
	return "selection"
}

// listSelectionTitle names the row under the cursor in the left pane.
func (m *Model) listSelectionTitle() string {
	l := m.list()
	if l == nil {
		return "selection"
	}
	switch l.section {
	case sectionAgents:
		if a, ok := m.cursorAgent(); ok {
			return a.Name + " (agent)"
		}
	case sectionWorktrees:
		if rows := m.worktreeRows(); l.worktreeRow < len(rows) {
			if r := rows[l.worktreeRow]; r.file != nil {
				return r.file.Path + " (file)"
			}
			return rows[l.worktreeRow].worktree.Name + " (worktree)"
		}
	case sectionCode:
		switch n, ok := m.codeEntry(); {
		case ok && n.dir:
			return n.path + " (directory)"
		case ok:
			return n.path + " (file)"
		case l.code.worktree != "":
			return l.code.worktree + " (worktree)"
		}
	}
	return "selection"
}

// panePart is what acts on the left pane's list as a whole.
func (m *Model) panePart() menuPart {
	if m.layout.focus != project || m.list() == nil {
		return menuPart{title: "list"}
	}
	return menuPart{title: "list", items: []menuItem{
		{filterKey, "filter", "filter", func() tea.Cmd { m.openFilter(); return nil }},
		{refreshKey, "refresh", "refresh", func() tea.Cmd {
			m.codeRefresh()
			return m.refreshWorktrees("", true)
		}},
	}}
}

// globalPart is what acts on tide and the project shown, from anywhere.
func (m *Model) globalPart() menuPart {
	var items []menuItem
	if _, ok := m.checkoutDir(); ok {
		items = append(items,
			menuItem{newAgentKey, "new agent", "agent", func() tea.Cmd {
				m.switcher, m.opening = nil, nil
				return m.createAgent()
			}},
			menuItem{newTerminalKey, "new terminal", "terminal", func() tea.Cmd {
				m.switcher, m.opening = nil, nil
				return m.createTerminal()
			}})
	}
	items = append(items, menuItem{projectMenuKey, "project…", "project", func() tea.Cmd {
		m.openMenu(true)
		return nil
	}})
	if m.settingsOffered() {
		items = append(items, menuItem{settingsKey, "settings", "settings", func() tea.Cmd {
			m.switcher, m.opening = nil, nil
			m.openSettings()
			return nil
		}})
	}
	items = append(items, menuItem{quitKey, "quit", "quit", func() tea.Cmd {
		m.log.Info("tide quit")
		quit := m.flushViews() // before the stream goes, with what it shows
		m.unfollow()           // the daemon ends the stream, and logs it
		m.detachAll()
		return quit
	}})
	return menuPart{title: "global", items: items}
}

// projectPart is the Project submenu's.
func (m *Model) projectPart() menuPart {
	items := []menuItem{
		{openProjectKey, "open", "open", func() tea.Cmd {
			m.switcher = nil
			return m.openProject()
		}},
		{switchProjectKey, "switch", "switch", m.openSwitcher},
	}
	if r, ok := m.closableProject(); ok {
		items = append(items, menuItem{closeProjectKey, "close " + r.name, "close", func() tea.Cmd {
			m.switcher, m.opening = nil, nil
			if r.running {
				m.confirm = &confirmBox{r: r}
				m.log.Info("closing asked", "name", r.name)
				return nil
			}
			return m.closeProject(r, false)
		}})
	}
	return menuPart{title: "Project", items: items}
}

// shownKey is a key as the menu writes it.
func shownKey(key string) string {
	if key == menuKey {
		return "C-q"
	}
	return key
}

// menuHint is the status line's summary of the menu's keys.
func (m *Model) menuHint() string {
	var keys []string
	for _, it := range m.menuItems() {
		keys = append(keys, shownKey(it.key)+" "+it.short)
	}
	return "menu: " + strings.Join(keys, "  ") + "  Esc close"
}

// menuPopup is what the menu shows: each part under its title, set apart by a
// blank line; the Project submenu is its own popup.
func (m *Model) menuPopup() popup {
	title := "Menu"
	if m.projectMenu {
		title = "Project"
	}
	var body []string
	for i, p := range m.menuParts() {
		if !m.projectMenu {
			if i > 0 {
				body = append(body, "")
			}
			body = append(body, m.st.Muted(p.title))
		}
		for _, it := range p.items {
			body = append(body, m.st.Command(shownKey(it.key))+"  "+it.label)
		}
	}
	return popup{title: title, body: body, footer: m.st.Command("Esc") + " close"}
}
