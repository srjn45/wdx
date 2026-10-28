// Package style colors the text wdx prints to a terminal. Its styles are
// named by what the text is, not by its color, and use only the 16 basic
// ANSI colors so they follow the terminal's theme.
package style

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Style colors text, or returns it unchanged when off.
type Style struct {
	on bool
}

// New returns a style that colors text when on.
func New(on bool) Style {
	return Style{on: on}
}

// On reports whether s colors text.
func (s Style) On() bool { return s.on }

const (
	bold   = "1"
	dim    = "2"
	italic = "3"
	red    = "31"
	green  = "32"
	yellow = "33"
	cyan   = "36"
)

// Heading styles the title of a section or the header of a table.
func (s Style) Heading(text string) string { return s.apply(text, bold) }

// Command styles a command line, such as "wdx daemon start".
func (s Style) Command(text string) string { return s.apply(text, cyan) }

// Flag styles a flag, such as "--name".
func (s Style) Flag(text string) string { return s.apply(text, yellow) }

// Placeholder styles what stands for a value, such as "<path>".
func (s Style) Placeholder(text string) string { return s.apply(text, dim, italic) }

// Success styles what a command did, such as "created".
func (s Style) Success(text string) string { return s.apply(text, bold, green) }

// Warning styles what needs attention but is not a failure.
func (s Style) Warning(text string) string { return s.apply(text, yellow) }

// Failure styles an error.
func (s Style) Failure(text string) string { return s.apply(text, bold, red) }

// Hint styles the label of a hint.
func (s Style) Hint(text string) string { return s.apply(text, cyan) }

// Name styles the name of an entity, such as a project.
func (s Style) Name(text string) string { return s.apply(text, bold) }

// Muted styles details that matter less: ids, timestamps, labels.
func (s Style) Muted(text string) string { return s.apply(text, dim) }

func (s Style) apply(text string, codes ...string) string {
	if !s.on || text == "" {
		return text
	}
	return "\x1b[" + strings.Join(codes, ";") + "m" + text + "\x1b[0m"
}

var escape = regexp.MustCompile("\x1b\\[[0-9;]*m")

// Width returns how many columns text takes on a terminal.
func Width(text string) int {
	return utf8.RuneCountInString(escape.ReplaceAllString(text, ""))
}

// Table lays out rows in columns two spaces apart. Unlike text/tabwriter it
// measures cells without their escape codes, so styled cells line up.
type Table struct {
	rows [][]string
}

// Row adds a row of cells.
func (t *Table) Row(cells ...string) {
	t.rows = append(t.rows, cells)
}

// Empty reports whether t has no rows.
func (t *Table) Empty() bool { return len(t.rows) == 0 }

// Write writes the rows to w, the last cell of each unpadded.
func (t *Table) Write(w io.Writer) {
	var widths []int
	for _, row := range t.rows {
		for i, cell := range row {
			if i == len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], Width(cell))
		}
	}
	for _, row := range t.rows {
		var b strings.Builder
		for i, cell := range row {
			b.WriteString(cell)
			if i < len(row)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-Width(cell)+2))
			}
		}
		fmt.Fprintln(w, b.String())
	}
}
