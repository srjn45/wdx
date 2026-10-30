package termview

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// History is a run of history lines, numbered from From, as a FetchFunc
// returns it.
type History struct {
	From  int64
	First int64 // the oldest line kept
	Lines []string
}

// FetchFunc fetches count history lines from line from on; the pane calls
// it in a command, off the program's goroutine.
type FetchFunc func(from int64, count int) (History, error)

const (
	// keptLines is how many of the lines its vt scrolled off the pane keeps;
	// older ones are fetched.
	keptLines = 10_000
	// pageLines is how many lines a fetch asks for, aligned on multiples.
	pageLines = 1_000
	// cachedPages bounds the fetched pages kept.
	cachedPages = 8
	// prefetchLines is how close to lines it does not have the view comes
	// before fetching them.
	prefetchLines = 200
)

// scrollback is what the pane scrolls through: history lines by number,
// those its vt scrolled off since attaching kept here, older ones fetched.
type scrollback struct {
	fetch FetchFunc
	// next is the number of the screen's first line: the lines before it
	// are history. kept holds lines next-len(kept) to next-1.
	next  int64
	kept  []string
	first int64 // the oldest line history keeps, as the last fetch said

	pages    map[int64][]string // by first line
	fetching map[int64]bool
	order    []int64 // pages, latest used last
	fetchErr error

	// offset is how many lines the view is above the live screen; 0 is
	// live.
	offset int64
}

// pageMsg carries a fetched page to the pane that asked for it.
type pageMsg struct {
	pane  *Model
	start int64
	h     History
	err   error
}

func newScrollback(next int64, fetch FetchFunc) scrollback {
	return scrollback{fetch: fetch, next: next, pages: map[int64][]string{}, fetching: map[int64]bool{}}
}

// add appends lines the vt scrolled off. A view scrolled back stays on
// the lines it shows.
func (s *scrollback) add(lines []string) {
	s.kept = append(s.kept, lines...)
	if over := len(s.kept) - keptLines; over > 0 {
		s.kept = append([]string(nil), s.kept[over:]...)
	}
	s.next += int64(len(lines))
	if s.offset > 0 {
		s.offset += int64(len(lines))
	}
}

// line returns history line n, and false when it is not at hand.
func (s *scrollback) line(n int64) (string, bool) {
	if start := s.next - int64(len(s.kept)); n >= start && n < s.next {
		return s.kept[n-start], true
	}
	start := n - n%pageLines
	page, ok := s.pages[start]
	if !ok || n-start >= int64(len(page)) {
		return "", false
	}
	s.used(start)
	return page[n-start], true
}

func (s *scrollback) used(start int64) {
	for i, p := range s.order {
		if p == start {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	s.order = append(s.order, start)
}

// maxOffset is how far up the view goes: to the oldest line kept.
func (s *scrollback) maxOffset() int64 {
	return max(0, s.next-s.first)
}

// scroll moves the view by lines, up when positive, within the history,
// down to bottom at the lowest.
func (s *scrollback) scroll(lines, bottom int64) {
	s.offset = max(min(max(bottom, s.offset+lines), s.maxOffset()), bottom)
}

// needed returns a command fetching the page of lines the view nears and
// does not have, if any. top is the history line at the view's top.
func (s *scrollback) needed(pane *Model, top int64, rows int) tea.Cmd {
	if s.fetch == nil {
		return nil
	}
	for _, n := range []int64{top - prefetchLines, top, top + int64(rows)} {
		n = max(n, s.first)
		if n >= s.next-int64(len(s.kept)) {
			continue
		}
		start := n - n%pageLines
		if _, ok := s.pages[start]; ok || s.fetching[start] {
			continue
		}
		s.fetching[start] = true
		fetch := s.fetch
		return func() tea.Msg {
			h, err := fetch(start, pageLines)
			return pageMsg{pane: pane, start: start, h: h, err: err}
		}
	}
	return nil
}

func (s *scrollback) fetched(msg pageMsg) {
	delete(s.fetching, msg.start)
	s.fetchErr = msg.err
	if msg.err != nil {
		return
	}
	s.first = msg.h.First
	lines := msg.h.Lines
	if msg.h.From > msg.start {
		// Trimmed before it: what is left starts later.
		pad := make([]string, min(msg.h.From-msg.start, pageLines))
		lines = append(pad, lines...)
	}
	s.pages[msg.start] = lines
	s.used(msg.start)
	for len(s.order) > cachedPages {
		delete(s.pages, s.order[0])
		s.order = s.order[1:]
	}
}

// view returns rows lines for a view scrolled back: history lines, then
// the top of the screen, each cut to cols.
func (s *scrollback) view(screen []string, cols, rows int) []string {
	lines := make([]string, rows)
	top := s.next - s.offset
	for i := range lines {
		n := top + int64(i)
		var line string
		switch {
		case n >= s.next:
			if j := int(n - s.next); j < len(screen) {
				line = screen[j]
			}
		case n < s.first:
		default:
			if l, ok := s.line(n); ok {
				line = l
			} else {
				line = "…"
			}
		}
		lines[i] = ansi.Truncate(line, cols, "")
	}
	return lines
}

// ScrollUp scrolls the view up by lines, into the history; down when
// negative, back to the live screen at 0.
func (m *Model) ScrollUp(lines int) tea.Cmd {
	m.back.scroll(int64(lines), m.bottom())
	return m.fetchNeeded()
}

// ScrollPage scrolls a page up, or down when pages is negative.
func (m *Model) ScrollPage(pages int) tea.Cmd {
	_, rows := m.Size()
	return m.ScrollUp(pages * max(1, rows-1))
}

// Live returns to the live screen, or to the end of history alone.
func (m *Model) Live() {
	m.back.offset = m.bottom()
}

// bottom is the lowest the view goes: the live screen, or for history
// alone its last lines filling the pane, or all of it when it is shorter.
func (m *Model) bottom() int64 {
	if m.session != nil {
		return 0
	}
	return min(int64(m.rows), m.back.maxOffset())
}

// Scrolled reports whether the view is scrolled back.
func (m *Model) Scrolled() bool {
	return m.back.offset > m.bottom()
}

// Position is the history line at the top of a scrolled view and the
// number of history lines.
func (m *Model) Position() (top, lines int64) {
	return m.back.next - m.back.offset, m.back.next
}

// FetchErr is why the last fetch of history failed, nil when it did not.
func (m *Model) FetchErr() error {
	return m.back.fetchErr
}

func (m *Model) fetchNeeded() tea.Cmd {
	if m.back.offset == 0 {
		return nil // the live screen
	}
	_, rows := m.Size()
	top, _ := m.Position()
	return m.back.needed(m, top, rows)
}

// scrolledContent is the view scrolled back, as styled lines.
func (m *Model) scrolledContent() string {
	cols, rows := m.Size()
	var screen []string
	if m.screen != nil {
		screen = strings.Split(m.screen.Render(), "\n")
	}
	return strings.Join(m.back.view(screen, cols, rows), "\n")
}
