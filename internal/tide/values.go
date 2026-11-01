package tide

import (
	"slices"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/style"
)

// A value pane shows rows of "key: value", of which some can be edited in
// place. What it shows comes anew with each draw, so that it stays live; whether
// a value can be used is for the API to say (docs/TIDE_INFO.md).

const (
	valueEmpty  = "—"
	valueCursor = "▸ "
	// valueIndent is what comes before a key: a cell, then the cursor's room.
	valueIndent = 3
	// valueKeyGap is what follows a key: its colon and two cells.
	valueKeyGap = 3
	// valueWheelStep is how many lines a turn of the wheel scrolls.
	valueWheelStep = 3
)

// valueRow is a row of a value pane.
type valueRow struct {
	key    string
	values []string // one line each; none shows as empty
	gap    bool     // a blank line comes before it
	verb   string   // what the hint calls editing it
	// save sends what was typed, and its answer is a valueSaved; nil is read-only.
	save func(text string) tea.Cmd
}

// valueSaved is the answer to a row's save.
type valueSaved struct {
	owner string // the pane's owner when it was sent
	err   error
}

// valueEdit is a row being edited.
type valueEdit struct {
	row  int
	text string
	busy bool   // the value was sent
	err  string // why the last one was refused
}

// valuePane is what a value pane remembers between draws.
type valuePane struct {
	owner  string // what it shows, whose answers it takes
	offset int    // the lines scrolled past
	cursor int    // the row under the cursor, when it is editable
	reveal bool   // the next draw scrolls the cursor into view
	edit   *valueEdit
}

// cursorRow returns the row under the cursor: the first editable one when it is
// not on one, -1 when there is none.
func (p *valuePane) cursorRow(rows []valueRow) int {
	if p.cursor < len(rows) && rows[p.cursor].save != nil {
		return p.cursor
	}
	return slices.IndexFunc(rows, func(r valueRow) bool { return r.save != nil })
}

// dropStaleEdit ends an edit of a row that can no longer be edited.
func (p *valuePane) dropStaleEdit(rows []valueRow) {
	if p.edit != nil && (p.edit.row >= len(rows) || rows[p.edit.row].save == nil) {
		p.edit = nil
	}
}

// begin edits the row i, starting from its value.
func (p *valuePane) begin(rows []valueRow, i int) {
	p.cursor, p.reveal = i, true
	p.edit = &valueEdit{row: i, text: strings.Join(rows[i].values, "")}
}

// update takes a key or a paste.
func (p *valuePane) update(rows []valueRow, msg tea.Msg) tea.Cmd {
	p.dropStaleEdit(rows)
	if p.edit != nil {
		return p.editInput(rows, msg)
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	i := p.cursorRow(rows)
	switch name := key.String(); {
	case slices.Contains(upKeys, name):
		p.step(rows, i, -1)
	case slices.Contains(downKeys, name):
		p.step(rows, i, 1)
	case name == "enter" && i >= 0:
		p.begin(rows, i)
	}
	return nil
}

// step moves the cursor to the next editable row in direction by, or scrolls
// when there is none.
func (p *valuePane) step(rows []valueRow, from, by int) {
	if from >= 0 {
		for i := from + by; i >= 0 && i < len(rows); i += by {
			if rows[i].save != nil {
				p.cursor, p.reveal = i, true
				return
			}
		}
	}
	p.offset += by
}

// editInput takes a key or a paste while a row is edited.
func (p *valuePane) editInput(rows []valueRow, msg tea.Msg) tea.Cmd {
	e, row := p.edit, rows[p.edit.row]
	switch msg := msg.(type) {
	case tea.PasteMsg:
		if !e.busy {
			e.text += printable(msg.Content)
		}
	case tea.KeyPressMsg:
		switch name := msg.String(); {
		case name == "esc":
			p.edit = nil
		case e.busy:
		case name == "enter":
			if strings.TrimSpace(e.text) == strings.Join(row.values, "") {
				p.edit = nil
				return nil
			}
			e.busy, e.err = true, ""
			return row.save(e.text)
		case name == "backspace" && e.text != "":
			r := []rune(e.text)
			e.text = string(r[:len(r)-1])
		case typed(msg):
			e.text += msg.Text
		}
	}
	return nil
}

// printable is s without what a terminal would act on.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, s)
}

// saved takes the answer to a save: the edit ends, or stays with why not.
func (p *valuePane) saved(msg valueSaved) {
	if p.edit == nil || !p.edit.busy || msg.owner != p.owner {
		return
	}
	if msg.err == nil {
		p.edit = nil
		return
	}
	p.edit.busy, p.edit.err = false, msg.err.Error()
}

// valueLine is a line of a value pane: the row it belongs to, or -1 for a gap.
type valueLine struct {
	row  int
	text string
}

// lines lays out rows in width cells.
func (p *valuePane) lines(rows []valueRow, width int, focused bool, st style.Style) []valueLine {
	keys := 0
	for _, r := range rows {
		keys = max(keys, ansi.StringWidth(r.key))
	}
	room := max(width-valueIndent-keys-valueKeyGap, 1)
	continued := strings.Repeat(" ", valueIndent+keys+valueKeyGap)
	cursor := p.cursorRow(rows)

	var lines []valueLine
	for i, r := range rows {
		if r.gap {
			lines = append(lines, valueLine{row: -1})
		}
		pointer := "  "
		if focused && i == cursor {
			pointer = st.Focused(valueCursor)
		}
		key := r.key + ":" + strings.Repeat(" ", keys-ansi.StringWidth(r.key)+valueKeyGap-1)
		lead := " " + pointer + st.Muted(key)
		for _, v := range p.valueLines(r, i, room, focused && i == cursor, st) {
			lines = append(lines, valueLine{row: i, text: lead + v})
			lead = continued
		}
		if e := p.edit; e != nil && e.row == i && e.err != "" {
			for _, l := range wrapValue("✗ "+e.err, room) {
				lines = append(lines, valueLine{row: i, text: continued + st.Failure(l)})
			}
		}
	}
	return lines
}

// valueLines is what the row i shows after its key: its value, or what is typed.
func (p *valuePane) valueLines(r valueRow, i, room int, current bool, st style.Style) []string {
	if e := p.edit; e != nil && e.row == i {
		return wrapValue(ansi.Strip(e.text)+"_", room)
	}
	if len(r.values) == 0 {
		return []string{st.Muted(valueEmpty)}
	}
	var lines []string
	for _, v := range r.values {
		for _, l := range wrapValue(v, room) {
			if current {
				l = st.Name(l)
			}
			lines = append(lines, l)
		}
	}
	return lines
}

// wrapValue cuts v into lines of room cells, its escape codes gone.
func wrapValue(v string, room int) []string {
	return strings.Split(ansi.Hardwrap(ansi.Strip(v), room, true), "\n")
}

// hint is what the pane's last line says of the keys, "" when nothing is editable.
func (p *valuePane) hint(rows []valueRow, st style.Style) string {
	if e := p.edit; e != nil {
		if e.busy {
			return st.Muted("saving…")
		}
		return st.Command("Enter") + " save  " + st.Command("Esc") + " cancel"
	}
	if i := p.cursorRow(rows); i >= 0 {
		return st.Command("Enter") + " " + rows[i].verb
	}
	return ""
}

// bodyRows is how many of the height's lines the rows get, the hint taking one.
func (p *valuePane) bodyRows(rows []valueRow, height int, st style.Style) int {
	if p.hint(rows, st) != "" {
		height--
	}
	return max(height, 0)
}

// draw shows rows in area.
func (p *valuePane) draw(buf uv.Screen, area uv.Rectangle, rows []valueRow, focused bool, st style.Style) {
	p.dropStaleEdit(rows)
	lines := p.lines(rows, area.Dx(), focused, st)
	body := p.bodyRows(rows, area.Dy(), st)
	if p.reveal || p.edit != nil {
		p.show(lines, p.shownRow(rows), body)
		p.reveal = false
	}
	p.offset = max(min(p.offset, len(lines)-body), 0)
	shown := make([]string, 0, body)
	for _, l := range lines[p.offset:min(p.offset+body, len(lines))] {
		shown = append(shown, l.text)
	}
	uv.NewStyledString(strings.Join(shown, "\n")).Draw(buf, uv.Rect(area.Min.X, area.Min.Y, area.Dx(), body))
	if hint := p.hint(rows, st); hint != "" {
		uv.NewStyledString(" "+hint).Draw(buf, uv.Rect(area.Min.X, area.Max.Y-1, area.Dx(), 1))
	}
}

// shownRow is the row a draw keeps in view.
func (p *valuePane) shownRow(rows []valueRow) int {
	if p.edit != nil {
		return p.edit.row
	}
	return p.cursorRow(rows)
}

// show scrolls so that the lines of the row are in view.
func (p *valuePane) show(lines []valueLine, row, body int) {
	first := slices.IndexFunc(lines, func(l valueLine) bool { return l.row == row })
	if first < 0 {
		return
	}
	last := first
	for last+1 < len(lines) && lines[last+1].row == row {
		last++
	}
	p.offset = max(p.offset, last-body+1)
	p.offset = min(p.offset, first)
}

// click takes a click at the row of the area of the height and width given: on
// an editable row it starts editing it.
func (p *valuePane) click(rows []valueRow, row, width, height int, st style.Style) {
	lines := p.lines(rows, width, false, st)
	at := p.offset + row
	if row >= p.bodyRows(rows, height, st) || at >= len(lines) {
		return
	}
	i := lines[at].row
	if i < 0 || rows[i].save == nil || (p.edit != nil && p.edit.row == i) {
		return
	}
	p.begin(rows, i)
}

// wheel scrolls by a turn of the wheel; the next draw keeps it in range.
func (p *valuePane) wheel(msg tea.MouseWheelMsg) {
	switch msg.Button {
	case tea.MouseWheelUp:
		p.offset = max(p.offset-valueWheelStep, 0)
	case tea.MouseWheelDown:
		p.offset += valueWheelStep
	}
}
