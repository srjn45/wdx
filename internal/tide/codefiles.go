package tide

import (
	"fmt"
	"path"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// The Code section's keys that create, rename and delete files and directories, in
// popups (docs/TIDE_CODE.md), the menu's lines for the selection there.
const (
	newFileKey    = "n"
	newDirKey     = "N"
	renameKey     = "r"
	deleteFileKey = "x"
)

// codeEntry is the file or directory under the cursor; ok is false on the selector.
func (m *Model) codeEntry() (n *codeNode, ok bool) {
	r, ok := m.codeCursorRow()
	if !ok || r.node == nil {
		return nil, false
	}
	return r.node, true
}

// newCodeEntry opens the popup that creates a file, or a directory, in the worktree
// chosen: first where, from the directory under the cursor or the file's.
func (m *Model) newCodeEntry(directory bool) {
	l, f := m.list(), m.codeFilesShown()
	if f == nil || !f.loaded {
		return
	}
	start := ""
	if n, ok := m.codeEntry(); ok {
		start = n.path
		if !n.dir {
			start = parentDir(n.path)
		}
	}
	m.push(&codeDirScreen{worktree: l.code.worktree, children: codeDirs(f), dir: start, directory: directory})
	m.log.Info("new entry asked", "worktree", l.code.worktree, "dir", start, "directory", directory)
}

// parentDir is the directory p is in, "" for the worktree's.
func parentDir(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}

// codeDirs returns the directories of the files known, empty ones too, each with the
// names of those in it, sorted; "" is the worktree's.
func codeDirs(f *codeFiles) map[string][]string {
	children := map[string][]string{"": nil}
	var add func(d string)
	add = func(d string) {
		if _, ok := children[d]; ok {
			return
		}
		children[d] = nil
		parent := parentDir(d)
		add(parent)
		children[parent] = append(children[parent], path.Base(d))
	}
	for _, p := range f.paths {
		add(parentDir(p))
	}
	for _, d := range f.emptyDirs {
		add(d)
	}
	for _, names := range children {
		slices.SortFunc(names, func(a, b string) int {
			if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
				return c
			}
			return strings.Compare(a, b)
		})
	}
	return children
}

// codeDirScreen picks the directory of the worktree a new file or directory goes in,
// as the directory browser of [Open Project] picks one on disk.
type codeDirScreen struct {
	worktree  string
	children  map[string][]string
	dir       string // the directory listed, "" for the worktree's
	directory bool   // a directory is created, not a file
	filter    string
	selected  int
	rows      int
}

func (s *codeDirScreen) kind() string {
	if s.directory {
		return "directory"
	}
	return "file"
}

func (s *codeDirScreen) title() string {
	if s.directory {
		return "New Directory"
	}
	return "New File"
}

func (s *codeDirScreen) hint() string {
	return "type to filter  → into  ← up  enter choose  Esc cancel"
}

func (s *codeDirScreen) here() string { return "create the " + s.kind() + " here" }

// items are what can be selected: here, then the directories in the one listed that
// the filter matches, those starting with it first. Hidden ones show only when the
// filter starts with a dot.
func (s *codeDirScreen) items() []string {
	items := []string{s.here()}
	var rest []string
	filter := strings.ToLower(s.filter)
	for _, name := range s.children[s.dir] {
		lower := strings.ToLower(name)
		switch {
		case strings.HasPrefix(name, ".") && !strings.HasPrefix(filter, "."):
		case strings.HasPrefix(lower, filter):
			items = append(items, name)
		case strings.Contains(lower, filter):
			rest = append(rest, name)
		}
	}
	return append(items, rest...)
}

func (s *codeDirScreen) update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	items := s.items()
	last := len(items) - 1
	s.selected = max(min(s.selected, last), 0)
	item := items[s.selected]
	page := max(s.rows, 1)
	switch name := key.String(); {
	case name == "esc":
		return pop
	case name == "up":
		s.selected = max(s.selected-1, 0)
	case name == "down":
		s.selected = min(s.selected+1, last)
	case name == "pgup":
		s.selected = max(s.selected-page, 0)
	case name == "pgdown":
		s.selected = min(s.selected+page, last)
	case name == "home":
		s.selected = 0
	case name == "end":
		s.selected = last
	case name == "enter" && s.selected == 0:
		return send(pushScreen{s.nameField()})
	case (name == "enter" || name == "right") && s.selected > 0:
		s.dir, s.filter, s.selected = path.Join(s.dir, item), "", 0
	case name == "left" || name == "backspace" && s.filter == "":
		if s.dir != "" {
			from := path.Base(s.dir)
			s.dir, s.filter = parentDir(s.dir), ""
			s.selected = max(slices.Index(s.items(), from), 0)
		}
	case name == "backspace":
		r := []rune(s.filter)
		s.filter, s.selected = string(r[:len(r)-1]), 0
	case typed(key):
		s.filter, s.selected = s.filter+key.Text, 0
	}
	return nil
}

// shownDir is the directory listed as the person reads it: in the worktree, by name.
func (s *codeDirScreen) shownDir() string {
	return path.Join(s.worktree, s.dir) + "/"
}

// nameField asks the name of the file or directory to create in the directory
// listed.
func (s *codeDirScreen) nameField() *fieldScreen {
	worktree, dir, directory := s.worktree, s.dir, s.directory
	return &fieldScreen{
		heading: s.title() + " › " + s.shownDir(),
		prompt:  "Name of the new " + s.kind() + ", in " + s.shownDir() + ":",
		empty:   "type a name",
		working: "creating…",
		submit: func(name string) tea.Msg {
			return createCodeEntry{worktree: worktree, path: path.Join(dir, name), directory: directory}
		},
	}
}

func (s *codeDirScreen) draw(buf uv.Screen, area uv.Rectangle, st style.Style) {
	// A long path loses its start: where it ends is what matters.
	dir := s.shownDir()
	if over := ansi.StringWidth(dir) - area.Dx(); over > 0 {
		dir = ansi.TruncateLeft(dir, over+1, "…")
	}
	lines := []string{st.Name(dir)}
	if s.filter != "" {
		lines = append(lines, "> "+s.filter+"_")
	}
	items := s.items()
	s.rows = max(area.Dy()-len(lines)-1, 1)
	selected := max(min(s.selected, len(items)-1), 0)
	from := max(min(selected-s.rows/2, len(items)-s.rows), 0)
	for i := from; i < min(from+s.rows, len(items)); i++ {
		text := items[i] + "/"
		if i == 0 {
			text = st.Command(items[i])
		}
		if i == selected {
			lines = append(lines, st.Focused("▸ ")+st.Name(text))
		} else {
			lines = append(lines, "  "+text)
		}
	}
	if len(items) == 1 {
		note := "no directories here"
		if s.filter != "" {
			note = "no directory matches"
		}
		lines = append(lines, st.Muted(note))
	}
	uv.NewStyledString(strings.Join(lines, "\n")).Draw(buf, area)
}

// editorsOn returns the editors that have the file at p of the worktree open, or a
// file under it.
func (m *Model) editorsOn(worktree, p string) []string {
	var names []string
	for _, e := range m.editors() {
		if e.Worktree == worktree && (e.Path == p || strings.HasPrefix(e.Path, p+"/")) {
			names = append(names, e.Path)
		}
	}
	return names
}

// renameCodeEntry opens the popup that renames the file or directory under the
// cursor, in its directory, unless an editor has it open.
func (m *Model) renameCodeEntry() {
	n, ok := m.codeEntry()
	if !ok {
		return
	}
	worktree := m.list().code.worktree
	if m.refusedForEditors(worktree, n.path, "renaming") {
		return
	}
	from := n.path
	m.push(&fieldScreen{
		heading: "Rename",
		prompt:  "New name of " + path.Join(worktree, from) + ":",
		value:   n.name,
		empty:   "type a name",
		working: "renaming…",
		submit: func(name string) tea.Msg {
			return renameCodeEntry{worktree: worktree, path: from, name: name}
		},
	})
	m.log.Info("rename asked", "worktree", worktree, "path", from)
}

// refusedForEditors says on the status line that what is at p cannot be changed while
// an editor has it, or a file in it, open; it reports whether it said so.
func (m *Model) refusedForEditors(worktree, p, doing string) bool {
	open := m.editorsOn(worktree, p)
	if len(open) == 0 {
		return false
	}
	m.statusNotice = "close the editor of " + strings.Join(open, ", ") + " before " + doing + " " + p
	m.log.Info("change refused: an editor is open", "worktree", worktree, "path", p, "editors", open)
	return true
}

// deleteCodeEntry opens the popup that deletes the file or directory under the cursor,
// unless an editor has it open. It says what git cannot bring back.
func (m *Model) deleteCodeEntry() {
	n, ok := m.codeEntry()
	if !ok {
		return
	}
	worktree := m.list().code.worktree
	if m.refusedForEditors(worktree, n.path, "deleting") {
		return
	}
	m.push(&choiceScreen{
		heading:  "Delete",
		question: m.deleteQuestion(worktree, n),
		choices: []choice{
			{label: "keep it", msg: popScreen{}},
			{label: "delete it", msg: removeCodeEntry{worktree: worktree, path: n.path}, working: "deleting…"},
		},
	})
	m.log.Info("delete asked", "worktree", worktree, "path", n.path)
}

// deleteQuestion asks whether to delete n, saying what it holds and what of that git
// cannot bring back: what is untracked, and uncommitted changes.
func (m *Model) deleteQuestion(worktree string, n *codeNode) string {
	changed := map[string]api.WorktreeFileStatus{}
	for _, w := range m.worktreeStatuses() {
		if w.Name == worktree {
			for _, f := range w.Files {
				changed[f.Path] = f.Status
			}
		}
	}
	shown := path.Join(worktree, n.path)
	const forGood = " Nothing goes to a trash."
	if !n.dir {
		switch status, ok := changed[n.path]; {
		case ok && status == api.WorktreeFileStatusEmpty:
			return "Delete " + shown + "? It is untracked: git cannot bring it back." + forGood
		case ok:
			return "Delete " + shown + "? Its uncommitted changes are lost: git can bring back only what was committed." + forGood
		}
		return "Delete " + shown + "? git can bring it back, as it was committed." + forGood
	}
	var files, lost int
	for _, p := range m.codeFilesShown().paths {
		if strings.HasPrefix(p, n.path+"/") {
			files++
			if _, ok := changed[p]; ok {
				lost++
			}
		}
	}
	q := fmt.Sprintf("Delete %s/ and the %d files in it?", shown, files)
	switch files {
	case 0:
		q = "Delete " + shown + "/? It holds no file."
	case 1:
		q = "Delete " + shown + "/ and the file in it?"
	}
	if lost > 0 {
		q += fmt.Sprintf(" %d of them untracked or changed: git cannot bring that back.", lost)
	}
	return q + forGood
}

// createCodeEntry, renameCodeEntry and removeCodeEntry are asked of the daemon by the
// popups.
type (
	createCodeEntry struct {
		worktree, path string
		directory      bool
	}
	renameCodeEntry struct{ worktree, path, name string }
	removeCodeEntry struct{ worktree, path string }
)

// codeEntryChanged is the daemon's answer to one of them.
type codeEntryChanged struct {
	projectID, worktree string
	result              api.Result
	path, from          string // from is where it was, when renamed
	directory           bool   // a directory was created
	err                 error
}

// changeCodeEntry asks the daemon to create, rename or delete what msg says.
func (m *Model) changeCodeEntry(msg tea.Msg) tea.Cmd {
	following := m.following
	if following == nil || following.project == nil || m.client == nil {
		return nil
	}
	id := following.project.Id
	ref := "id:" + id
	return func() tea.Msg {
		var r api.WorktreeFileChange
		var err error
		answer := codeEntryChanged{projectID: id}
		switch msg := msg.(type) {
		case createCodeEntry:
			answer.worktree, answer.directory = msg.worktree, msg.directory
			r, err = m.client.CreateWorktreeFile(m.ctx, msg.worktree, msg.path, msg.directory, ref)
		case renameCodeEntry:
			answer.worktree = msg.worktree
			r, err = m.client.RenameWorktreeFile(m.ctx, msg.worktree, msg.path, msg.name, ref)
		case removeCodeEntry:
			answer.worktree = msg.worktree
			r, err = m.client.RemoveWorktreeFile(m.ctx, msg.worktree, msg.path, ref)
		}
		answer.result, answer.path, answer.err = r.Result, r.Path, err
		if r.From != nil {
			answer.from = *r.From
		}
		return answer
	}
}

// codeEntryChanged takes the answer: a failure stays in the popup, to correct the
// name or give up. Otherwise the popup closes, the files are read again, and the
// cursor goes to what was created or renamed, staying on it until the read that
// shows it, or to where the deleted was. A new file opens in the editor.
func (m *Model) codeEntryChanged(msg codeEntryChanged) tea.Cmd {
	if msg.err != nil {
		m.log.Info("entry not changed", "worktree", msg.worktree, "err", msg.err)
		if s := m.openTop(); s != nil {
			return s.update(screenFailed{msg.err})
		}
		m.statusNotice = msg.err.Error()
		return nil
	}
	m.opening = nil
	m.log.Info("entry changed", "worktree", msg.worktree, "result", msg.result, "path", msg.path, "from", msg.from)
	l, ok := m.agentLists[msg.projectID]
	if !ok {
		return nil
	}
	f := l.code.files[msg.worktree]
	if f == nil {
		return nil
	}
	f.asked = false
	f.changes++
	if f.open == nil {
		f.open = map[string]bool{}
	}
	shown := l == m.list() && l.code.worktree == msg.worktree
	switch msg.result {
	case api.ResultCreated:
		for d := parentDir(msg.path); d != ""; d = parentDir(d) {
			f.open[d] = true
		}
		if shown {
			l.section, l.code.cursor, l.code.awaiting = sectionCode, msg.path, msg.worktree
		}
		m.statusNotice = "created " + path.Join(msg.worktree, msg.path)
		if shown && !msg.directory {
			m.openCodeFile(msg.path)
			return m.askEditor(l.active)
		}
	case api.ResultRenamed:
		moveOpen(f.open, msg.from, msg.path)
		m.moveFileTabs(l, msg.worktree, msg.from, msg.path)
		if shown {
			l.code.cursor, l.code.awaiting = msg.path, msg.worktree
		}
		m.statusNotice = "renamed " + path.Join(msg.worktree, msg.from) + " to " + path.Base(msg.path)
	case api.ResultRemoved:
		moveOpen(f.open, msg.path, "")
		for _, tab := range slices.Clone(l.tabs) {
			if w, file := diffTabOf(tab); isFileTab(tab) && w == msg.worktree && under(file, msg.path) {
				m.closeTab(tab, "file deleted")
			}
		}
		if shown && under(l.code.cursor, msg.path) {
			l.code.cursor = parentDir(msg.path)
		}
		m.statusNotice = "deleted " + path.Join(msg.worktree, msg.path)
	}
	return nil
}

// under reports whether p is dir, or in it.
func under(p, dir string) bool { return p == dir || strings.HasPrefix(p, dir+"/") }

// moveOpen moves the folds open under from to the same places under to, or forgets
// them when to is "".
func moveOpen(open map[string]bool, from, to string) {
	for d := range open {
		if !under(d, from) {
			continue
		}
		delete(open, d)
		if to != "" {
			open[to+strings.TrimPrefix(d, from)] = true
		}
	}
}

// moveFileTabs has the file tabs of what was at from show it at to, in their places.
func (m *Model) moveFileTabs(l *agentList, worktree, from, to string) {
	for i, tab := range l.tabs {
		w, file := diffTabOf(tab)
		if !isFileTab(tab) || w != worktree || !under(file, from) {
			continue
		}
		moved := fileTab(worktree, to+strings.TrimPrefix(file, from))
		l.tabs[i] = moved
		delete(l.diffs, tab)
		if l.active == tab {
			l.active = moved
		}
		m.log.Info("tab moved", "tab", tab, "to", moved)
	}
}

// screenFailed is a request a screen sent that failed: it says why.
type screenFailed struct{ err error }
