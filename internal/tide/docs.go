package tide

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// The Docs section of the left pane, and a doc's tab, which shows its latest
// revision read-only in the text view of a file. tide never changes a doc: the
// CLI does (docs/DOCS.md).

const (
	docTabPrefix = "doc:"
	docTabLabel  = "doc: "
	docHint      = "↑↓ select  Enter open  m move  / filter  Alt+arrows move  C-q menu"
)

// docKeys are what ? shows on a doc: a file's keys, but editing.
var docKeys = slices.DeleteFunc(slices.Clone(sourceKeys), func(k barKey) bool { return k.keys == editFileKey })

// docTab is the tab of the doc with id, which a rename keeps.
func docTab(id string) string { return docTabPrefix + id }

func isDocTab(tab string) bool { return strings.HasPrefix(tab, docTabPrefix) }

// docs returns the docs of the project shown, by name, as the stream sends them.
func (m *Model) docs() []api.Doc {
	if f := m.following; f != nil && f.project != nil && f.project.Docs != nil {
		return *f.project.Docs
	}
	return nil
}

// docOf returns the doc of the project shown whose id is id.
func (m *Model) docOf(id string) (api.Doc, bool) {
	docs := m.docs()
	if i := slices.IndexFunc(docs, func(d api.Doc) bool { return d.Id == id }); i >= 0 {
		return docs[i], true
	}
	return api.Doc{}, false
}

// docRow is a row of the Docs section: a doc, or a folder when place says so.
type docRow struct {
	doc   api.Doc
	place treePlace
	match bool // it matches the filter
}

// key names the row across events: its doc's id, or its folder.
func (r docRow) key() string {
	if r.place.folder != "" {
		return r.place.key()
	}
	return r.doc.Id
}

// docRows are the rows of the Docs section, in their folders: with a filter, the
// docs whose name or labels hold its words, under their folders unfolded.
func (m *Model) docRows() []docRow {
	l := m.list()
	if l == nil {
		return nil
	}
	words := l.filter.words()
	docs, folded := m.docs(), l.docFolders
	if words != nil {
		docs = slices.DeleteFunc(slices.Clone(docs), func(d api.Doc) bool { return !matches(d.Name+" "+strings.Join(d.Labels, " "), words) })
		folded = nil
	}
	var rows []docRow
	folderTree(docs, func(d api.Doc) string { return d.Folder }, folded, func(p treePlace, d *api.Doc) {
		if d == nil {
			rows = append(rows, docRow{place: p})
			return
		}
		rows = append(rows, docRow{doc: *d, place: p, match: words != nil})
	})
	return rows
}

// cursorDocRow returns the row under the cursor in the Docs section.
func (m *Model) cursorDocRow() (docRow, bool) {
	l, rows := m.list(), m.docRows()
	if l == nil || l.docRow >= len(rows) {
		return docRow{}, false
	}
	return rows[l.docRow], true
}

// docFold folds the folder under the cursor, or unfolds it; ← on a doc in a folder
// folds that folder, the cursor going to it.
func (m *Model) docFold(fold bool) {
	r, ok := m.cursorDocRow()
	if !ok {
		return
	}
	path := r.place.folder
	if path == "" {
		if !fold || atTop(r.doc.Folder) {
			return
		}
		path = r.doc.Folder
	}
	l := m.list()
	if setFolded(&l.docFolders, path, fold) {
		m.log.Info("doc folder folded", "folder", path, "folded", fold)
		m.changed("doc folder")
	}
	l.docCursor = folderRowKey + path
	m.keepDocCursor()
}

// docsChanged keeps the cursor on its doc, and closes the tabs of the docs gone.
// A project from a daemon that sends no docs says nothing of them, and closes none.
func (m *Model) docsChanged() {
	l := m.list()
	if l == nil {
		return
	}
	if f := m.following; f.project.Docs != nil {
		for _, tab := range slices.Clone(l.tabs) {
			id := strings.TrimPrefix(tab, docTabPrefix)
			if _, ok := m.docOf(id); !isDocTab(tab) || ok {
				continue
			}
			name := l.docNames[id]
			if name == "" {
				name = id
			}
			m.closeTab(tab, "doc removed")
			m.statusNotice = "doc " + name + " removed"
		}
		l.docNames = map[string]string{}
		for _, d := range m.docs() {
			l.docNames[d.Id] = d.Name
		}
	}
	m.keepDocCursor()
}

// keepDocCursor keeps the cursor on its doc, or on the one that took its row; with
// none left it goes back to the agents.
func (m *Model) keepDocCursor() {
	l, rows := m.list(), m.docRows()
	if i := slices.IndexFunc(rows, func(r docRow) bool { return r.key() == l.docCursor }); i >= 0 {
		l.docRow = i
		return
	}
	if len(rows) == 0 {
		if l.section == sectionDocs {
			l.section = sectionAgents
		}
		l.docRow, l.docCursor = 0, ""
		return
	}
	l.docRow = max(min(l.docRow, len(rows)-1), 0)
	l.docCursor = rows[l.docRow].key()
}

// moveToDoc puts the cursor on the row i of the section.
func (m *Model) moveToDoc(i int) {
	l, rows := m.list(), m.docRows()
	if i < 0 || i >= len(rows) {
		return
	}
	l.section, l.docRow, l.docCursor, l.onHeading = sectionDocs, i, rows[i].key(), false
}

// openDoc opens the tab of the doc under the cursor, or goes to it, the focus
// following as showOpened says; on a folder it folds or unfolds it.
func (m *Model) openDoc(goToTab bool) {
	r, ok := m.cursorDocRow()
	if !ok {
		return
	}
	if r.place.folder != "" {
		m.docFold(r.place.fold == foldOpen)
		return
	}
	d, l := r.doc, m.list()
	l.openTab(docTab(d.Id))
	m.showOpened(goToTab)
	m.changed("doc")
	m.log.Info("doc opened", "doc_id", d.Id, "name", d.Name)
}

// docTitle is what the tab strip calls a doc's tab: its name, revision and status,
// or its id until the stream brings it.
func (m *Model) docTitle(tab string) string {
	id := strings.TrimPrefix(tab, docTabPrefix)
	d, ok := m.docOf(id)
	if !ok {
		return docTabLabel + id
	}
	return fmt.Sprintf("%s%s r%d %s", docTabLabel, d.Name, d.Revision, d.Status)
}

// docSeen is what a doc's tab depends on: its revision. A change of status or
// labels makes no new revision, and is not read again.
func (m *Model) docSeen(tab string) string {
	if d, ok := m.docOf(strings.TrimPrefix(tab, docTabPrefix)); ok {
		return fmt.Sprint(d.Revision)
	}
	return ""
}

// docFile is the doc's name as a file's, which says how to highlight it.
func docFile(d api.Doc) string {
	if d.Format == "" {
		return d.Name
	}
	return d.Name + "." + d.Format
}

// setDoc takes the content of a doc.
func (v *diffView) setDoc(c api.DocContent) {
	v.file = docFile(c.Doc)
	if c.Content == "" {
		v.message = "an empty doc"
		return
	}
	v.lineOf = sourceLines(c.Content)
}

// docRowActive reports whether the row's doc is the current tab's, or the row is a
// folded folder holding it.
func (m *Model) docRowActive(r docRow) bool {
	l := m.list()
	if l == nil {
		return false
	}
	if r.place.folder != "" {
		d, ok := m.docOf(strings.TrimPrefix(l.active, docTabPrefix))
		return r.place.fold == foldClosed && isDocTab(l.active) && ok && inFolder(d.Folder, r.place.folder)
	}
	return l.active == docTab(r.doc.Id)
}

// docLine is the row r of the section, width wide: the tree's lines, then a
// folder's fold mark and name, or a doc's name, status and labels, which give way
// first.
func docLine(st style.Style, r docRow, cursor, active bool, width int) string {
	pointer := "  "
	if cursor {
		pointer = st.Focused("▸ ")
	}
	end := " "
	if active {
		end = " " + st.Focused("◂") + " "
	}
	lead := treeLead(r.place)
	room := max(width-style.Width(pointer)-style.Width(end)-ansi.StringWidth(lead), 0)
	if r.place.folder != "" {
		return folderLine(st, r.place, false, pointer, end, cursor, active, room)
	}
	name := ansi.Truncate(r.doc.Name, room, "…")
	status := string(r.doc.Status)
	room -= ansi.StringWidth(name)
	status = " " + status
	if ansi.StringWidth(status) > room {
		status = "" // a word cut short would say another
	}
	room -= ansi.StringWidth(status)
	labels := ""
	if status != "" && len(r.doc.Labels) > 0 {
		labels = ansi.Truncate(" "+strings.Join(r.doc.Labels, " "), max(room, 0), "…")
		room -= ansi.StringWidth(labels)
	}
	gap := strings.Repeat(" ", max(room, 0))
	if cursor || active {
		name = st.Name(name)
	}
	row := pointer + st.Muted(lead) + name + paintStatus(st, r.doc.Status, status) + st.Muted(labels) + gap + end
	if active {
		return st.ActiveRow(row)
	}
	return row
}

// paintStatus styles text, which says a doc's status, by what it is.
func paintStatus(st style.Style, status api.DocStatus, text string) string {
	switch status {
	case api.DocStatusApproved:
		return st.Success(text)
	case api.DocStatusRejected:
		return st.Failure(text)
	}
	return st.Muted(text)
}
