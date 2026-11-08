package tide

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	glamouransi "charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/style"
)

// A Markdown doc's or file's tab shows it rendered by glamour, and m switches it
// to its source and back (the wdx doc TIDE_MARKDOWN).

const sourceKey = "m"

// isMarkdown reports whether the file, by its name, holds Markdown.
func isMarkdown(file string) bool {
	ext := strings.ToLower(path.Ext(file))
	return ext == ".md" || ext == ".markdown"
}

// takeMarkdown says whether the text the view just took is rendered: Markdown no
// larger than what gets colours, as glamour colours its code blocks with chroma.
func (v *diffView) takeMarkdown(size int) {
	v.markdown = isMarkdown(v.file) && v.lineOf != nil && size <= maxHighlighted
	v.tooLargeToRender = isMarkdown(v.file) && v.lineOf != nil && size > maxHighlighted
}

// showsRendered reports whether the view shows rendered lines: once the first
// rendering has come, the lines of an earlier one stay until the next comes.
func (v *diffView) showsRendered() bool {
	return v.markdown && !v.showSource && v.rendered != nil
}

// switchSource shows the source of a Markdown view, or renders it again, from its
// first line, as a line of one says nothing of where it is in the other.
func (v *diffView) switchSource() {
	v.showSource = !v.showSource
	v.cursor, v.offset = 0, 0
	v.rows = nil // the current line is not kept on its row
	v.width = 0
}

// prepareRendered lays the rendered lines out as they are: glamour wrapped them.
func (v *diffView) prepareRendered(st style.Style) {
	for row, line := range v.rendered {
		plain := ansi.Strip(line)
		v.text = append(v.text, plain)
		v.starts = append(v.starts, len(v.lines))
		v.add(v.markMatches(line, plain, st), row, 0)
	}
}

// markMatches makes what the search finds stand out on a rendered line, cutting
// it around each match so that the colours either side stay.
func (v *diffView) markMatches(line, plain string, st style.Style) string {
	if v.search == nil {
		return line
	}
	var b strings.Builder
	at := 0
	for _, m := range v.search.FindAllStringIndex(plain, -1) {
		from, to := ansi.StringWidth(plain[:m[0]]), ansi.StringWidth(plain[:m[1]])
		b.WriteString(ansi.Cut(line, at, from))
		b.WriteString(st.Match(plain[m[0]:m[1]]))
		at = to
	}
	b.WriteString(ansi.Cut(line, at, ansi.StringWidth(plain)))
	return b.String()
}

// markdownRendered is glamour's lines for the text of a view as it was read, at a
// width.
type markdownRendered struct {
	projectID, tab string
	reading        int
	width          int
	lines          []string
	err            error
}

// markdownSync renders the Markdown of the tab shown for the width it has, when
// it has not been for this text and width and is not on its way.
func (m *Model) markdownSync() tea.Cmd {
	l := m.list()
	v := m.shownDiff()
	if l == nil || v == nil || !v.markdown || v.showSource || v.lineOf == nil || m.following == nil || m.following.project == nil {
		return nil
	}
	width, ok := m.textWidth()
	if !ok {
		return nil
	}
	want := renderKey{reading: v.reading, width: width}
	if v.renderedFor == want || v.renderingFor == want {
		return nil
	}
	v.renderingFor = want
	projectID, tab, text, on := m.following.project.Id, m.diffViewOf(l.active), strings.Join(v.lineOf, "\n"), m.st.On()
	return func() tea.Msg {
		lines, err := renderMarkdown(text, width, on)
		return markdownRendered{projectID: projectID, tab: tab, reading: want.reading, width: width, lines: lines, err: err}
	}
}

// renderKey is the text, by its reading, and the width lines were rendered for.
type renderKey struct{ reading, width int }

// textWidth is how wide the text view's lines are in the agent pane: all but the
// column that marks the current line.
func (m *Model) textWidth() (int, bool) {
	frames, ok := m.frames()
	if !ok {
		return 0, false
	}
	f, shown := frames[agent]
	if !shown {
		return 0, false
	}
	return max(f.Inset(1).Dx()-1, 1), true
}

// markdownDone takes glamour's lines, unless the text has been read differently
// since; one that fails is shown as source.
func (m *Model) markdownDone(msg markdownRendered) {
	l, ok := m.agentLists[msg.projectID]
	if !ok || l.diffs[msg.tab] == nil || l.diffs[msg.tab].reading != msg.reading {
		return
	}
	v := l.diffs[msg.tab]
	if v.renderingFor == (renderKey{reading: msg.reading, width: msg.width}) {
		v.renderingFor = renderKey{}
	}
	if msg.err != nil {
		m.log.Info("markdown not rendered", "tab", msg.tab, "err", msg.err)
		v.markdown = false
		v.width = 0
		return
	}
	m.log.Info("markdown rendered", "tab", msg.tab, "width", msg.width, "lines", len(msg.lines))
	v.rendered, v.renderedFor = msg.lines, renderKey{reading: msg.reading, width: msg.width}
	v.width = 0 // what is drawn is to be worked out again
}

// renderMarkdown renders text for width: in glamour's dark style, with no margin,
// as tide's pane has its own; with no colours when on is false.
func renderMarkdown(text string, width int, on bool) ([]string, error) {
	config := styles.NoTTYStyleConfig
	if on {
		config = styles.DarkStyleConfig
	}
	noMargin := uint(0)
	config.Document = glamouransi.StyleBlock{StylePrimitive: config.Document.StylePrimitive, Margin: &noMargin}
	r, err := glamour.NewTermRenderer(glamour.WithStyles(config), glamour.WithWordWrap(width))
	if err != nil {
		return nil, fmt.Errorf("start glamour: %w", err)
	}
	out, err := r.Render(shownMarkdown(text))
	if err != nil {
		return nil, fmt.Errorf("render: %w", err)
	}
	out = safeLinks(strings.ReplaceAll(out, "\x1b[m", "\x1b[0m")) // tide's tints carry across "\x1b[0m"
	lines := strings.Split(out, "\n")
	blank := func(line string) bool { return strings.TrimSpace(ansi.Strip(line)) == "" }
	for len(lines) > 0 && blank(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && blank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}

// shownMarkdown is the text with what is not text replaced, line by line, as the
// source view shows it: glamour passes it through, to the terminal.
func shownMarkdown(text string) string {
	lines := sourceLines(text)
	for i, line := range lines {
		lines[i] = shownText(line)
	}
	return strings.Join(lines, "\n")
}

var hyperlink = regexp.MustCompile("\x1b\\]8;([^;\x07\x1b]*);([^\x07\x1b]*)(\x07|\x1b\\\\)")

// linkSchemes are the links a click may open: a doc must not make one run
// something.
var linkSchemes = []string{"http", "https", "mailto"}

// safeLinks takes out the OSC 8 links of any other scheme, leaving their text.
func safeLinks(rendered string) string {
	return hyperlink.ReplaceAllStringFunc(rendered, func(link string) string {
		target := hyperlink.FindStringSubmatch(link)[2]
		if target == "" {
			return link // the end of a link
		}
		if u, err := url.Parse(target); err == nil && slices.Contains(linkSchemes, strings.ToLower(u.Scheme)) {
			return link
		}
		return ""
	})
}
