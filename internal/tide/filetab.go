package tide

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/style"
)

// A file's tab shows what the file holds now, read-only, in the text view of the
// diffs without what only changes have (docs/TIDE_CODE.md).

const (
	fileTabPrefix = "file:"
	fileTabLabel  = "file: "
)

// diffOnlyKeys are the keys of the text view that act on changes, and mean nothing
// on a file.
var diffOnlyKeys = []string{"[", "]", "{", "}", "tab", commitKey, landKey, commentKey, editKey, deleteKey, reviewKey, verifyKey}

// sourceKeys are what ? shows on a file.
var sourceKeys = []barKey{
	{"↑ ↓", "the line above or below", 0}, {"PgUp PgDn", "a page up or down", 0}, {"b Space", "a page up or down", 0},
	{"g G", "the first or last line", 0}, {": and a number", "go to that line of the file", 0},
	{"/ and text", "search; capitals make it exact", 0}, {"n N", "the next or last match", 0},
	{"Esc", "end the search", 0}, {"C-q -", "close the tab", 0}, {"Alt+←", "the tab before", 0},
}

func fileTab(worktree, file string) string { return fileTabPrefix + worktree + rowSeparator + file }

func isFileTab(tab string) bool { return strings.HasPrefix(tab, fileTabPrefix) }

// hasText reports whether the view has lines to show, rather than why it has none.
func (v *diffView) hasText() bool { return v.lineOf != nil || v.split != nil || v.unified != nil }

// setSource takes what a file holds, or says why it is not shown.
func (v *diffView) setSource(c api.WorktreeFileContent) {
	switch {
	case c.Binary:
		v.message = "a binary file: not shown"
	case c.TooLarge:
		v.message = fmt.Sprintf("too large to show: %.1f MiB", float64(c.Size)/(1<<20))
	case c.Content == nil || *c.Content == "":
		v.message = "an empty file"
	default:
		v.lineOf = strings.Split(strings.TrimSuffix(*c.Content, "\n"), "\n")
		for i, line := range v.lineOf {
			v.lineOf[i] = strings.TrimSuffix(line, "\r")
		}
	}
}

// setSourceError says why a file cannot be read, when that is the file's doing, and
// reports whether it did: what it showed is no longer so.
func (v *diffView) setSourceError(err error) bool {
	e, ok := errors.AsType[*client.ProblemError](err)
	if !v.source || !ok {
		return false
	}
	switch e.Problem.Code {
	case api.ErrorCodeNotFound:
		v.message = "not on disk"
	case api.ErrorCodeInvalidPath:
		v.message = "not a file in this worktree: it leads out of it, or is a directory"
	default:
		return false
	}
	v.err, v.lineOf, v.lines, v.width = "", nil, nil, 0
	return true
}

// prepareSource lays the file's lines out with their numbers; a long line wraps.
func (v *diffView) prepareSource(width int, st style.Style) {
	digits := len(strconv.Itoa(len(v.lineOf)))
	text := max(width-digits-1, 1)
	blank := strings.Repeat(" ", digits)
	plain := func(s string) string { return s }
	for row, line := range v.lineOf {
		v.text = append(v.text, shownText(line))
		v.starts = append(v.starts, len(v.lines))
		pieces := wrapText(line, text)
		if len(pieces) == 0 {
			pieces = []string{""}
		}
		for i, piece := range pieces {
			number, shown := blank, 0
			if i == 0 {
				number, shown = fmt.Sprintf("%*d", digits, row+1), row+1
			}
			v.add(st.Muted(number)+" "+v.paint(piece, plain, st), row, shown)
		}
	}
}
