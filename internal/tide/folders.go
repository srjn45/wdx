package tide

import (
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// Docs and plans in folders (docs/FOLDERS.md): a folder is only a label, which the
// Docs and Plans sections show as a tree, as the Code section shows directories.

const (
	moveKey        = "m" // of the menu, on a doc or a plan
	folderRowKey   = "folder:"
	folderHint     = "↑↓ select  ←→ fold  Enter fold  / filter  C-q menu"
	topFolder      = "/"
	folderMoveText = "type a folder, / for the top"
)

// treePlace is where a row of a folder tree sits.
type treePlace struct {
	folder string // a folder's row: its path; empty on an item's
	depth  int
	guide  string // the tree's lines before the fold mark or the name
	next   string // the lines what is under the row starts with
	fold   string // a folder's foldOpen or foldClosed; " " on an item at the top beside folders
}

// key names a folder's row across events, as an id names an item's.
func (p treePlace) key() string { return folderRowKey + p.folder }

// name is a folder's last part, which its row shows.
func (p treePlace) name() string { return p.folder[strings.LastIndex(p.folder, "/")+1:] }

// treeNode is a folder and what is in it.
type treeNode[T any] struct {
	path    string
	folders map[string]*treeNode[T]
	items   []T
}

// folderTree lays out items in their folders, calling row for every row in order:
// with nil for a folder's, and the item for an item's. At each level come the
// folders first, by name, then the items in the order given; what a folder in folded
// holds is left out. Items at the top have no folder row, so a project that never
// sets one shows as a list.
func folderTree[T any](items []T, folderOf func(T) string, folded map[string]bool, row func(treePlace, *T)) {
	root := &treeNode[T]{path: topFolder}
	for _, it := range items {
		n := root
		for part := range strings.SplitSeq(strings.Trim(folderOf(it), "/"), "/") {
			if part == "" {
				continue
			}
			if n.folders == nil {
				n.folders = map[string]*treeNode[T]{}
			}
			child, ok := n.folders[part]
			if !ok {
				child = &treeNode[T]{path: strings.TrimSuffix(n.path, "/") + "/" + part}
				n.folders[part] = child
			}
			n = child
		}
		n.items = append(n.items, it)
	}
	var walk func(n *treeNode[T], lead string, depth int)
	walk = func(n *treeNode[T], lead string, depth int) {
		names := slices.Sorted(maps.Keys(n.folders))
		total := len(names) + len(n.items)
		for i, name := range names {
			child := n.folders[name]
			p := treePlace{folder: child.path, depth: depth, fold: foldOpen}
			p.guide, p.next = treeGuides(lead, depth, i == total-1)
			if folded[child.path] {
				p.fold = foldClosed
			}
			row(p, nil)
			if p.fold == foldOpen {
				walk(child, p.next, depth+1)
			}
		}
		for i := range n.items {
			p := treePlace{depth: depth}
			p.guide, p.next = treeGuides(lead, depth, len(names)+i == total-1)
			if depth == 0 && len(names) > 0 {
				p.fold = " "
			}
			row(p, &n.items[i])
		}
	}
	walk(root, "", 0)
}

// treeLead is what a row of a folder tree starts with: the tree's lines, then a
// folder's fold mark or the blank that lines an item up with them.
func treeLead(p treePlace) string {
	if p.fold != "" {
		return p.guide + p.fold + " "
	}
	return p.guide
}

// folderLine is a folder's row in the Docs or Plans section, room left for its
// name between pointer and end: the tree's lines, its fold mark, its name and, when
// needsYou, that a plan in it waits for the person.
func folderLine(st style.Style, p treePlace, needsYou bool, pointer, end string, cursor, active bool, room int) string {
	name := ansi.Truncate(p.name(), room, "…")
	room -= ansi.StringWidth(name)
	need := ""
	if needsYou && room >= ansi.StringWidth(" needs you") {
		need = " needs you"
	}
	room -= ansi.StringWidth(need)
	if cursor || active {
		name = st.Name(name)
	}
	row := pointer + st.Muted(p.guide) + p.fold + " " + name + st.Warning(need) + strings.Repeat(" ", max(room, 0)) + end
	if active {
		return st.ActiveRow(row)
	}
	return row
}

// atTop reports whether folder is the top, which has no row: a daemon sends /, and
// one from before folders nothing.
func atTop(folder string) bool { return strings.Trim(folder, "/") == "" }

// inFolder reports whether folder is path or under it.
func inFolder(folder, path string) bool {
	return folder == path || strings.HasPrefix(folder, path+"/")
}

// setFolded folds the folder at path in folds, or unfolds it, reporting whether
// that changed anything.
func setFolded(folds *map[string]bool, path string, fold bool) bool {
	if (*folds)[path] == fold {
		return false
	}
	if *folds == nil {
		*folds = map[string]bool{}
	}
	if fold {
		(*folds)[path] = true
	} else {
		delete(*folds, path)
	}
	return true
}

// moveAsked is the folder typed in the popup m opens on a doc or a plan.
type moveAsked struct {
	doc, plan string // the id of the one moved
	folder    string
}

// moved is the daemon's answer to it; the folder comes with the stream.
type moved struct{ err error }

// askMoveDoc opens the popup that moves d, with its folder to edit.
func (m *Model) askMoveDoc(d api.Doc) {
	m.switcher = nil
	m.push(&fieldScreen{
		heading: "Move doc",
		prompt:  "Folder of " + d.Name + ":",
		value:   d.Folder,
		empty:   folderMoveText,
		working: "moving…",
		submit:  func(folder string) tea.Msg { return moveAsked{doc: d.Id, folder: folder} },
	})
	m.log.Info("doc move opened", "doc_id", d.Id)
}

// askMovePlan opens the popup that moves p, with its folder to edit, and says
// which plans move with it: a plan of plans and its plans share one folder.
func (m *Model) askMovePlan(p api.Plan) {
	prompt := "Folder of " + p.Name + ":"
	switch {
	case isParent(p) && len(*p.Plans) > 0:
		prompt = "Folder of " + p.Name + ", which moves its " + plural(len(*p.Plans), "plan") + " with it:"
	case p.Parent != nil:
		prompt = "Folder of " + p.Name + ", which moves " + *p.Parent + " and its plans with it:"
	}
	m.switcher = nil
	m.push(&fieldScreen{
		heading: "Move plan",
		prompt:  prompt,
		value:   p.Folder,
		empty:   folderMoveText,
		working: "moving…",
		submit:  func(folder string) tea.Msg { return moveAsked{plan: p.Id, folder: folder} },
	})
	m.log.Info("plan move opened", "plan_id", p.Id)
}

// sendMove asks the daemon for the folder typed in the popup.
func (m *Model) sendMove(msg moveAsked) tea.Cmd {
	if m.client == nil {
		return nil
	}
	m.log.Info("move asked", "doc_id", msg.doc, "plan_id", msg.plan, "folder", msg.folder)
	return func() tea.Msg {
		var err error
		if msg.doc != "" {
			_, err = m.client.UpdateDoc(m.ctx, "id:"+msg.doc, "", "", api.UpdateDoc{Folder: &msg.folder})
		} else {
			_, err = m.client.SetPlan(m.ctx, "id:"+msg.plan, "", "", api.UpdatePlan{Folder: &msg.folder})
		}
		return moved{err: err}
	}
}

// moveAnswered closes the popup, or shows there why the folder was refused, to
// correct it or give up.
func (m *Model) moveAnswered(msg moved) tea.Cmd {
	if msg.err != nil {
		m.log.Info("not moved", "err", msg.err)
		if s := m.openTop(); s != nil {
			return s.update(screenFailed(msg))
		}
		m.statusNotice = msg.err.Error()
		return nil
	}
	m.log.Info("moved")
	m.opening = nil
	return nil
}

// movable is what m moves: the doc or the plan under the cursor, not a folder.
func (m *Model) movable() (doc *api.Doc, plan *api.Plan) {
	l := m.list()
	if m.layout.focus != project || l == nil || l.onHeading {
		return nil, nil
	}
	switch l.section {
	case sectionDocs:
		if r, ok := m.cursorDocRow(); ok && r.place.folder == "" {
			return &r.doc, nil
		}
	case sectionPlans:
		if p, ok := m.cursorPlan(); ok {
			return nil, &p
		}
	}
	return nil, nil
}
