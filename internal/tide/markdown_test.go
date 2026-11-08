package tide

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

const markdownText = "# Auth\n\nSome **bold** text, with [a link](https://example.com/auth).\n\n- one\n- two\n"

// renderedDoc is a model showing the doc auth, of markdownText, rendered, with the
// focus in its tab.
func renderedDoc(t *testing.T, on bool) (*Model, *diffView) {
	t.Helper()
	m := docsModel(t)
	m.st = style.New(on)
	keys(m, "enter")
	d := aDoc("doc-auth", "auth", api.DocStatusApproved, 2)
	_, cmd := m.Update(diffLoaded{projectID: "proj-demo", tab: authTab, doc: &api.DocContent{Doc: d, Revision: 2, Content: markdownText}})
	run(m, cmd)
	keys(m, "alt+right")
	v := m.shownDiff()
	if !v.showsRendered() {
		t.Fatalf("not rendered:\n%s", text(m))
	}
	return m, v
}

func TestMarkdownDocRendered(t *testing.T) {
	t.Parallel()
	m, _ := renderedDoc(t, true)
	wantText(t, m, "Auth", "Some bold text, with a link", "• one", "m source", "line 1 of")
	for _, source := range []string{"**bold**", "1 # Auth", "- one"} {
		if strings.Contains(text(m), source) {
			t.Errorf("shows the source %q:\n%s", source, text(m))
		}
	}
	if strings.Contains(statusRow(m)+text(m), ": line") {
		t.Errorf("the bar offers a line of the source:\n%s", text(m))
	}
	keys(m, "?")
	wantText(t, m, "show the source, or render it again")
	if strings.Contains(text(m), "go to that line") {
		t.Errorf("? offers a line of the source:\n%s", text(m))
	}
	keys(m, "esc")

	keys(m, "m")
	wantText(t, m, "1 # Auth", "3 Some **bold** text", "m rendered", ": line")
	keys(m, "m")
	wantText(t, m, "Some bold text", "m source")
}

func TestMarkdownSearch(t *testing.T) {
	t.Parallel()
	m, v := renderedDoc(t, true)
	keys(m, "/", "b", "o", "l", "d", "enter")
	text(m)
	match := style.New(true).Match("bold")
	for _, line := range v.lines {
		if strings.Contains(line, match) {
			if !strings.Contains(ansiPlain(line), "Some bold text") {
				t.Errorf("the mark changed the text: %q", line)
			}
			return
		}
	}
	t.Errorf("no mark in:\n%q", v.lines)
}

func ansiPlain(s string) string {
	return colorCode.ReplaceAllString(hyperlink.ReplaceAllString(s, ""), "")
}

// TestMarkdownRenderedOnce: the lines are rendered once for a text and width, and
// again for another width.
func TestMarkdownRenderedOnce(t *testing.T) {
	t.Parallel()
	m, v := renderedDoc(t, true)
	if cmd := m.markdownSync(); cmd != nil {
		t.Fatal("rendered again with nothing changed")
	}
	width := v.renderedFor.width
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	run(m, cmd)
	if v.renderedFor.width == width || !v.showsRendered() {
		t.Errorf("not rendered again for the width: %d", v.renderedFor.width)
	}
}

func TestMarkdownOff(t *testing.T) {
	t.Parallel()
	_, v := renderedDoc(t, false)
	if lines := strings.Join(v.rendered, "\n"); colorCode.MatchString(lines) {
		t.Errorf("colours with colour off: %q", lines)
	}
}

func TestMarkdownFile(t *testing.T) {
	t.Parallel()
	m := fileModel(t)
	m.st = style.New(true)
	tab := fileTab("main", "docs/README.md")
	m.list().openTab(tab)
	m.Update(nil)
	_, cmd := m.Update(diffLoaded{projectID: "proj-demo", tab: tab, content: sourceOf(markdownText)})
	run(m, cmd)
	wantText(t, m, "Some bold text", "m source", "e edit")
	keys(m, "alt+right", "m")
	wantText(t, m, "3 Some **bold** text")
}

func TestMarkdownTooLarge(t *testing.T) {
	t.Parallel()
	m := docsModel(t)
	keys(m, "enter")
	d := aDoc("doc-auth", "auth", api.DocStatusApproved, 2)
	large := "# Big\n\n" + strings.Repeat("word ", maxHighlighted/5+1)
	_, cmd := m.Update(diffLoaded{projectID: "proj-demo", tab: authTab, doc: &api.DocContent{Doc: d, Revision: 2, Content: large}})
	run(m, cmd)
	keys(m, "alt+right", "m")
	wantText(t, m, "1 # Big")
	if !strings.Contains(titleRow(m), "too large to render") {
		t.Errorf("title %q", titleRow(m))
	}
	if v := m.shownDiff(); v.markdown || v.rendered != nil {
		t.Error("rendered")
	}
}

func TestMarkdownNotADoc(t *testing.T) {
	t.Parallel()
	m := docsModel(t)
	keys(m, "enter")
	d := aDoc("doc-auth", "auth", api.DocStatusApproved, 2)
	d.Format = "txt"
	_, cmd := m.Update(diffLoaded{projectID: "proj-demo", tab: authTab, doc: &api.DocContent{Doc: d, Revision: 2, Content: "# plain\n"}})
	run(m, cmd)
	keys(m, "alt+right")
	wantText(t, m, "1 # plain")
	if strings.Contains(text(m), "m source") {
		t.Errorf("offers to render text:\n%s", text(m))
	}
}

// TestMarkdownLinks: only links a click may safely open stay links.
func TestMarkdownLinks(t *testing.T) {
	t.Parallel()
	lines, err := renderMarkdown("[web](https://example.com) [mail](mailto:a@example.com) [run](javascript:alert(1)) [file](file:///etc/passwd) [here](docs/A.md)", 200, true)
	if err != nil {
		t.Fatal(err)
	}
	var targets []string
	for _, link := range hyperlink.FindAllStringSubmatch(strings.Join(lines, "\n"), -1) {
		if link[2] != "" {
			targets = append(targets, link[2])
		}
	}
	for _, target := range targets {
		if target != "https://example.com" && target != "mailto:a@example.com" {
			t.Errorf("a link to %q", target)
		}
	}
	if len(targets) == 0 {
		t.Errorf("no links in %q", lines)
	}
}

// TestMarkdownControls: what is not text in a doc never reaches the terminal.
func TestMarkdownControls(t *testing.T) {
	t.Parallel()
	lines, err := renderMarkdown("bell\x07 and \x1b]0;title\x07 here", 80, false)
	if err != nil {
		t.Fatal(err)
	}
	if out := strings.Join(lines, "\n"); strings.ContainsAny(out, "\x07\x1b") {
		t.Errorf("controls passed: %q", out)
	}
}
