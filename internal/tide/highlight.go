package tide

import (
	"path"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"github.com/srjn45/wdx/internal/style"
)

// A file's tab shows its text at once, and its colours when they are worked out,
// which takes about 7 ms per KiB (docs/TIDE_CODE.md).

// maxHighlighted is the most text that gets colours: a larger file stays plain, as
// its colours would take seconds to come.
const maxHighlighted = 256 << 10

// tokenKind is what a piece of source is, as far as its colour goes.
type tokenKind int

const (
	tokenPlain tokenKind = iota
	tokenKeyword
	tokenString
	tokenComment
	tokenNumber
	tokenType
	tokenFunction
)

// colorSpan is a run of a line in one colour, up to the byte end of the line as
// shownText gives it: what is drawn, and wrapped.
type colorSpan struct {
	end  int
	kind tokenKind
}

func tokenKindOf(t chroma.TokenType) tokenKind {
	switch {
	case t == chroma.CommentPreproc, t == chroma.CommentPreprocFile:
		return tokenKeyword
	case t.InCategory(chroma.Comment):
		return tokenComment
	case t == chroma.KeywordType:
		return tokenType
	case t.InCategory(chroma.Keyword), t == chroma.NameTag, t == chroma.GenericHeading, t == chroma.GenericSubheading:
		return tokenKeyword
	case t.InSubCategory(chroma.LiteralString):
		return tokenString
	case t.InSubCategory(chroma.LiteralNumber):
		return tokenNumber
	case t == chroma.NameClass, t == chroma.NameException, t == chroma.NameNamespace, t == chroma.NameAttribute:
		return tokenType
	case t == chroma.NameFunction, t == chroma.NameFunctionMagic, t == chroma.NameBuiltin, t == chroma.NameDecorator:
		return tokenFunction
	}
	return tokenPlain
}

func (k tokenKind) paint(text string, st style.Style) string {
	switch k {
	case tokenKeyword:
		return st.CodeKeyword(text)
	case tokenString:
		return st.CodeString(text)
	case tokenComment:
		return st.CodeComment(text)
	case tokenNumber:
		return st.CodeNumber(text)
	case tokenType:
		return st.CodeType(text)
	case tokenFunction:
		return st.CodeFunction(text)
	}
	return text
}

// lexerFor chooses by the file's name, then by a first line naming its interpreter;
// nil means no language.
func lexerFor(file, firstLine string) chroma.Lexer {
	if l := lexers.Match(file); l != nil {
		return l
	}
	name := interpreterOf(firstLine)
	switch name {
	case "":
		return nil
	case "node": // Chroma knows it only as javascript
		return lexers.Get("javascript")
	}
	if l := lexers.Get(name); l != nil {
		return l
	}
	return lexers.Get(strings.TrimRight(name, "0123456789.")) // python3.12
}

// interpreterOf names the program a first line runs the file with, past env and its
// flags and variables; "" when the line names none. Chroma's own reading of the line
// knows only the shells.
func interpreterOf(line string) string {
	rest, ok := strings.CutPrefix(line, "#!")
	if !ok {
		return ""
	}
	words := strings.Fields(rest)
	if len(words) == 0 {
		return ""
	}
	name := path.Base(words[0])
	if name != "env" {
		return name
	}
	for _, word := range words[1:] {
		if !strings.HasPrefix(word, "-") && !strings.Contains(word, "=") {
			return path.Base(word)
		}
	}
	return ""
}

// highlightLines returns the colours of the lines of file by line, and the language;
// nil colours leave it plain.
func highlightLines(file string, lines []string) (colors [][]colorSpan, language string) {
	if len(strings.Join(lines, "\n")) > maxHighlighted {
		return nil, ""
	}
	lexer := lexerFor(file, lines[0])
	if lexer == nil {
		return nil, ""
	}
	return highlightWith(lexer, lines)
}

// highlightWith leaves the lines plain on any failure: a pattern over Chroma's time
// limit panics in its iterator, and an odd file must not take tide down with it.
func highlightWith(lexer chroma.Lexer, lines []string) (colors [][]colorSpan, language string) {
	defer func() {
		if recover() != nil {
			colors, language = nil, ""
		}
	}()
	text := strings.Join(lines, "\n")
	tokens, err := chroma.Coalesce(lexer).Tokenise(&chroma.TokeniseOptions{State: "root"}, text)
	if err != nil {
		return nil, ""
	}
	colors = make([][]colorSpan, len(lines))
	var seen strings.Builder
	row, end := 0, 0
	for _, token := range tokens.Tokens() {
		seen.WriteString(token.Value)
		kind := tokenKindOf(token.Type)
		for i, part := range strings.Split(token.Value, "\n") {
			if i > 0 {
				row, end = row+1, 0
			}
			if part == "" || row >= len(lines) {
				continue
			}
			end += len(shownText(part))
			if n := len(colors[row]); n > 0 && colors[row][n-1].kind == kind {
				colors[row][n-1].end = end
			} else {
				colors[row] = append(colors[row], colorSpan{end: end, kind: kind})
			}
		}
	}
	// Colours placed over other text than was read would mislead.
	if got := seen.String(); got != text && got != text+"\n" {
		return nil, ""
	}
	return colors, lexer.Config().Name
}

// paintTokens paints text, the part of a line as shown that starts at byte start.
func paintTokens(text string, start int, spans []colorSpan, st style.Style) string {
	var b strings.Builder
	at, end := start, start+len(text)
	for _, s := range spans {
		if s.end <= at {
			continue
		}
		if at >= end {
			break
		}
		to := min(s.end, end)
		b.WriteString(s.kind.paint(text[at-start:to-start], st))
		at = to
	}
	b.WriteString(text[at-start:])
	return b.String()
}

// fileHighlighted is the answer to working out a file's colours.
type fileHighlighted struct {
	projectID, tab string
	reading        int
	colors         [][]colorSpan
	language       string
}

// highlightFile works out the colours of what the view holds. Its lines are not
// changed once read, only replaced, so the command can read them.
func highlightFile(projectID, tab string, v *diffView) tea.Cmd {
	file, lines, reading := v.file, v.lineOf, v.reading
	return func() tea.Msg {
		colors, language := highlightLines(file, lines)
		return fileHighlighted{projectID: projectID, tab: tab, reading: reading, colors: colors, language: language}
	}
}

// sourceRead takes a file just read, which replaced the lines old. The same text
// keeps its colours, or those on their way: a tab is read again often, and working
// them out again would make it flicker.
func (m *Model) sourceRead(projectID, tab string, v *diffView, old []string) tea.Cmd {
	if slices.Equal(old, v.lineOf) {
		return nil
	}
	v.colors = nil
	v.reading++
	if !m.st.On() || v.lineOf == nil {
		return nil
	}
	return highlightFile(projectID, tab, v)
}

// fileHighlighted takes the colours, unless the file has been read differently since.
func (m *Model) fileHighlighted(msg fileHighlighted) {
	l, ok := m.agentLists[msg.projectID]
	if !ok || l.diffs[msg.tab] == nil || l.diffs[msg.tab].reading != msg.reading {
		return
	}
	if msg.colors == nil {
		m.log.Info("file left plain", "tab", msg.tab)
		return
	}
	m.log.Info("file highlighted", "tab", msg.tab, "language", msg.language)
	v := l.diffs[msg.tab]
	v.colors = msg.colors
	v.width = 0 // what is drawn is to be worked out again
}
