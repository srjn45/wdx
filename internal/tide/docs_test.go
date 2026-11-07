package tide

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

func aDoc(id, name string, status api.DocStatus, revision int64, labels ...string) api.Doc {
	if labels == nil {
		labels = []string{}
	}
	return api.Doc{Id: id, Name: name, Format: "md", Status: status, Revision: revision, Labels: labels}
}

// setDocs makes the stream bring the project shown with docs.
func setDocs(m *Model, docs ...api.Doc) {
	m.following.project.Docs = &docs
	m.docsChanged()
}

// docsModel is a model whose project has the docs auth and notes, with the Docs
// section first and the cursor on auth.
func docsModel(t *testing.T) *Model {
	t.Helper()
	m := worktreesModel(api.WorktreeStatus{Name: "main", Path: "/r"})
	m.sections = sectionOrder("dawc")
	setDocs(m, aDoc("doc-auth", "auth", api.DocStatusApproved, 2, "rfc", "security"), aDoc("doc-notes", "notes", api.DocStatusWip, 1))
	m.moveToDoc(0)
	if l := m.list(); l.section != sectionDocs || l.docCursor != "doc-auth" {
		t.Fatalf("on %v %q", l.section, l.docCursor)
	}
	return m
}

const authTab = "doc:doc-auth"

func loadDoc(m *Model, d api.Doc, content string) {
	m.Update(diffLoaded{projectID: "proj-demo", tab: docTab(d.Id), doc: &api.DocContent{Doc: d, Revision: d.Revision, Content: content}})
}

func TestDocsListed(t *testing.T) {
	t.Parallel()
	m := docsModel(t)
	wantText(t, m, "Docs", "auth approved rfc security", "notes wip")
	if got := headingRows(m); !slices.Equal(got, []string{"Docs", "Agents", "Worktrees", "Code"}) {
		t.Errorf("headings %q", got)
	}
	if heading := paneLines(m)[1]; !strings.Contains(heading, "Docs") || !strings.HasSuffix(strings.TrimRight(heading, "│ "), "2") {
		t.Errorf("the heading has no count: %q", heading)
	}
	if !strings.Contains(statusRow(m), "Enter open") {
		t.Errorf("hint %q", statusRow(m))
	}

	// The cursor goes on to the next section, and back.
	keys(m, "down", "down")
	if l := m.list(); l.section != sectionAgents {
		t.Errorf("down past the docs: %v", l.section)
	}
	keys(m, "up")
	if l := m.list(); l.section != sectionDocs || l.docCursor != "doc-notes" {
		t.Errorf("back up: %v %q", l.section, l.docCursor)
	}

	// With none, the row says how to add one, and the cursor leaves the section.
	setDocs(m)
	wantText(t, m, "no docs: see wdx doc create")
	if l := m.list(); l.section != sectionAgents {
		t.Errorf("the cursor stayed in an empty section: %v", l.section)
	}
}

// TestDocRowFits: a narrow pane cuts the labels first, then the status whole.
func TestDocRowFits(t *testing.T) {
	t.Parallel()
	st := docsModel(t).st
	r := docRow{doc: aDoc("doc-a", "auth", api.DocStatusApproved, 1, "rfc", "security")}
	for width, want := range map[int]string{
		40: "  auth approved rfc security            ",
		20: "  auth approved rf… ",
		16: "  auth approved ",
		15: "  auth         ", // no labels without the status
		10: "  auth    ",
	} {
		if got := docLine(st, r, rowLook{}, width); got != want {
			t.Errorf("%d: %q, want %q", width, got, want)
		}
	}
}

func TestDocsFiltered(t *testing.T) {
	t.Parallel()
	m := docsModel(t)
	m.moveCursor(0)
	keys(m, "/", "s", "e", "c")
	rows := m.docRows()
	if len(rows) != 1 || rows[0].doc.Name != "auth" || !rows[0].match {
		t.Fatalf("by label: %+v", rows)
	}
	if l := m.list(); l.section != sectionDocs || l.docCursor != "doc-auth" {
		t.Errorf("the cursor is not on the match: %v %q", l.section, l.docCursor)
	}
	keys(m, "esc", "/", "n", "o", "t")
	if rows := m.docRows(); len(rows) != 1 || rows[0].doc.Name != "notes" {
		t.Errorf("by name: %+v", rows)
	}
	if m.filterMatches() != 1 {
		t.Errorf("matches %d", m.filterMatches())
	}
}

func TestDocOpens(t *testing.T) {
	t.Parallel()
	m := docsModel(t)
	keys(m, "enter")
	l := m.list()
	if l.active != authTab || !m.textShown() {
		t.Fatalf("tab %q", l.active)
	}
	if m.layout.focus != project {
		t.Errorf("the focus left the list: %v", m.layout.focus)
	}
	if !strings.Contains(titleRow(m), "[doc: auth r2 approved") {
		t.Errorf("title %q", titleRow(m))
	}
	wantText(t, m, "loading…", "▌")
	loadDoc(m, aDoc("doc-auth", "auth", api.DocStatusApproved, 2), "# Auth\n\nTokens.\n")
	wantText(t, m, "1 # Auth", "3 Tokens.", "line 1 of 3")
	if v := m.shownDiff(); v.file != "auth.md" {
		t.Errorf("highlighted as %q", v.file)
	}

	// Its keys are a file's, but editing.
	keys(m, "alt+right")
	if strings.Contains(statusRow(m)+text(m), "edit") {
		t.Errorf("the bar offers to edit:\n%s", text(m))
	}
	keys(m, "?")
	wantText(t, m, "go to that line")
	if strings.Contains(text(m), "edit") {
		t.Errorf("? lists editing:\n%s", text(m))
	}
	keys(m, "esc")
	if _, cmd := m.Update(key('e', 0)); cmd != nil || m.editShown() || m.statusNotice != "" {
		t.Errorf("e acted on a doc")
	}

	// A click opens one too.
	s := &projectScreen{m: m}
	shown, _ := m.visibleItems(100)
	row := slices.IndexFunc(shown, func(it paneItem) bool { return it.section == sectionDocs && it.row == 1 }) + 1
	s.click(6, row, 31, 100)
	if l.active != "doc:doc-notes" {
		t.Errorf("after a click: tab %q", l.active)
	}
}

func TestDocEmpty(t *testing.T) {
	t.Parallel()
	m := docsModel(t)
	keys(m, "enter")
	loadDoc(m, aDoc("doc-auth", "auth", api.DocStatusApproved, 2), "")
	wantText(t, m, "an empty doc")
}

// TestDocReadsAgain: a doc's tab is read again for a new revision, not for a new
// status or labels, which only its title shows.
func TestDocReadsAgain(t *testing.T) {
	t.Parallel()
	m := docsModel(t)
	keys(m, "enter")
	loadDoc(m, aDoc("doc-auth", "auth", api.DocStatusApproved, 2), "two\n")
	v := m.shownDiff()
	m.Update(nil)
	if v.loading {
		t.Fatal("read again with nothing changed")
	}
	notes := aDoc("doc-notes", "notes", api.DocStatusWip, 1)
	setDocs(m, aDoc("doc-auth", "auth", api.DocStatusReview, 2, "rfc"), notes)
	m.Update(nil)
	if v.loading {
		t.Error("read again on a new status")
	}
	if !strings.Contains(titleRow(m), "[doc: auth r2 review") {
		t.Errorf("title %q", titleRow(m))
	}

	// It comes to show after another tab: nothing changed, nothing to read.
	l := m.list()
	l.openTab(settingsTab)
	m.Update(nil)
	l.active = authTab
	m.Update(nil)
	if v.loading {
		t.Error("read again on coming to show")
	}

	setDocs(m, aDoc("doc-auth", "auth", api.DocStatusWip, 3, "rfc"), notes)
	m.Update(nil)
	if !v.loading {
		t.Error("not read again on a new revision")
	}
}

func TestDocRenamedKeepsItsTab(t *testing.T) {
	t.Parallel()
	m := docsModel(t)
	keys(m, "enter")
	setDocs(m, aDoc("doc-auth", "auth-design", api.DocStatusApproved, 2), aDoc("doc-notes", "notes", api.DocStatusWip, 1))
	l := m.list()
	if l.active != authTab || !strings.Contains(titleRow(m), "[doc: auth-design r2") {
		t.Errorf("tab %q, title %q", l.active, titleRow(m))
	}
	if l.docCursor != "doc-auth" {
		t.Errorf("the cursor left the doc: %q", l.docCursor)
	}

	// A doc coming before it moves its row, not the cursor off it.
	setDocs(m, aDoc("doc-api", "api", api.DocStatusWip, 1), aDoc("doc-auth", "auth-design", api.DocStatusApproved, 2), aDoc("doc-notes", "notes", api.DocStatusWip, 1))
	if l.docCursor != "doc-auth" || l.docRow != 1 {
		t.Errorf("the cursor is on %q, row %d", l.docCursor, l.docRow)
	}
}

func TestDocRemovedClosesItsTab(t *testing.T) {
	t.Parallel()
	m := docsModel(t)
	keys(m, "enter")
	l := m.list()
	notes := aDoc("doc-notes", "notes", api.DocStatusWip, 1)
	setDocs(m, notes)
	if slices.Contains(l.tabs, authTab) {
		t.Errorf("tabs %v", l.tabs)
	}
	if m.statusNotice != "doc auth removed" {
		t.Errorf("notice %q", m.statusNotice)
	}
	if l.docCursor != "doc-notes" {
		t.Errorf("the cursor is on %q", l.docCursor)
	}

	// A daemon that sends no docs closes none.
	l.openTab("doc:doc-notes")
	m.following.project.Docs = nil
	m.docsChanged()
	if !slices.Contains(l.tabs, "doc:doc-notes") {
		t.Errorf("closed with no docs sent: %v", l.tabs)
	}
}

func TestDocLive(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	h.newProject(t, "app", api.ProjectStateOpen)
	ctx, c, app := context.Background(), h.client(), "app"
	if _, err := c.CreateDoc(ctx, api.CreateDoc{Project: &app, Name: "auth", Content: new("# One\n")}); err != nil {
		t.Fatal(err)
	}

	p := h.follow(t)
	p.until(t, "the projects listed", func(m *Model) bool { return m.projects.loaded })
	p.keys("enter")
	p.until(t, "the doc listed", func(m *Model) bool { return len(m.docRows()) == 1 })
	m := p.m
	m.layout.setFocus(project)
	m.moveToDoc(0)
	p.keys("enter")
	p.until(t, "the doc's text", func(m *Model) bool { return strings.Contains(text(m), "1 # One") })

	if _, err := c.UpdateDocContent(ctx, "auth", app, "", 1, "# Two\n"); err != nil {
		t.Fatal(err)
	}
	p.until(t, "revision 2", func(m *Model) bool { return strings.Contains(text(m), "1 # Two") })
}
