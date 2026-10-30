package tide

import (
	"cmp"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/style"
)

// dirBrowser is the directory browser of [Open Project › Local] and
// [Open Project › Create]: the directories under one, the git repositories
// among them marked, to find a repository to open or where to create a
// project (docs/TIDE.md).
type dirBrowser struct {
	create   bool // to create a project, rather than open a repository
	userHome string
	dir      string     // the directory listed, absolute
	entries  []dirEntry // its directories, by name
	inRepo   bool       // dir is in a git repository
	readErr  error      // why dir could not be read
	reading  bool
	reselect string // the entry to select once dir is read: the one gone up from
	filter   string
	selected int // an index into items
	rows     int // how many items the last draw showed, a page
	opening  bool
	failure  string // why the last open failed
}

// dirEntry is a directory in the one listed.
type dirEntry struct {
	name string
	repo bool // holds a .git
}

// dirRead is a directory's listing, read in the background.
type dirRead struct {
	dir     string
	entries []dirEntry
	inRepo  bool
	err     error
}

// openHere is the item that opens the repository the directory listed is
// in; createHere the one that creates a project in the directory listed.
const (
	openHere   = "open this directory"
	createHere = "create the project here"
)

func newDirBrowser(dir, userHome string, create bool) (*dirBrowser, tea.Cmd) {
	b := &dirBrowser{userHome: userHome, create: create}
	return b, b.read(dir, "")
}

func (b *dirBrowser) title() string {
	if b.create {
		return createTitle
	}
	return localTitle
}

func (b *dirBrowser) hint() string {
	if b.create {
		return "type to filter  → into  ← up  enter choose  Esc back"
	}
	return "type to filter  → into  ← up  enter open  Esc back"
}

// read lists dir, then selects the entry named sel.
func (b *dirBrowser) read(dir, sel string) tea.Cmd {
	b.reading, b.reselect = true, sel
	return func() tea.Msg { return readDir(dir) }
}

func readDir(dir string) dirRead {
	files, err := os.ReadDir(dir)
	r := dirRead{dir: dir, err: err}
	for _, f := range files {
		path := filepath.Join(dir, f.Name())
		// A link to a directory is listed as one.
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			continue
		}
		r.entries = append(r.entries, dirEntry{name: f.Name(), repo: exists(filepath.Join(path, ".git"))})
	}
	slices.SortFunc(r.entries, func(a, b dirEntry) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.name), strings.ToLower(b.name)), cmp.Compare(a.name, b.name))
	})
	for d := dir; ; d = filepath.Dir(d) {
		if exists(filepath.Join(d, ".git")) {
			r.inRepo = true
			break
		}
		if d == filepath.Dir(d) {
			break
		}
	}
	return r
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// items returns what can be selected: createHere, or openHere when the
// directory is in a repository, then the entries the filter matches, those
// starting with it first. Hidden directories show only when the filter
// starts with a dot.
func (b *dirBrowser) items() []string {
	var items, prefixed, rest []string
	switch {
	case b.create && b.readErr == nil:
		items = append(items, createHere)
	case b.create:
	case b.inRepo:
		items = append(items, openHere)
	}
	filter := strings.ToLower(b.filter)
	for _, e := range b.entries {
		name := strings.ToLower(e.name)
		switch {
		case strings.HasPrefix(e.name, ".") && !strings.HasPrefix(filter, "."):
		case strings.HasPrefix(name, filter):
			prefixed = append(prefixed, e.name)
		case strings.Contains(name, filter):
			rest = append(rest, e.name)
		}
	}
	return append(append(items, prefixed...), rest...)
}

func (b *dirBrowser) entry(name string) dirEntry {
	for _, e := range b.entries {
		if e.name == name {
			return e
		}
	}
	return dirEntry{}
}

func (b *dirBrowser) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case dirRead:
		b.reading, b.dir, b.entries, b.inRepo, b.readErr = false, msg.dir, msg.entries, msg.inRepo, msg.err
		b.filter, b.selected = "", 0
		if i := slices.Index(b.items(), b.reselect); i >= 0 {
			b.selected = i
		}
	case projectOpened: // only a failure reaches the screen
		b.opening, b.failure = false, msg.err.Error()
	case tea.KeyPressMsg:
		if b.opening || b.reading {
			if msg.String() == "esc" {
				return pop
			}
			return nil
		}
		b.failure = ""
		return b.key(msg)
	}
	return nil
}

func (b *dirBrowser) key(msg tea.KeyPressMsg) tea.Cmd {
	items := b.items()
	last := len(items) - 1
	b.selected = max(min(b.selected, last), 0)
	var item string
	if len(items) > 0 {
		item = items[b.selected]
	}
	page := max(b.rows, 1)
	switch key := msg.String(); {
	case key == "esc":
		return pop
	case key == "up":
		b.selected = max(b.selected-1, 0)
	case key == "down":
		b.selected = max(min(b.selected+1, last), 0)
	case key == "pgup":
		b.selected = max(b.selected-page, 0)
	case key == "pgdown":
		b.selected = max(min(b.selected+page, last), 0)
	case key == "home":
		b.selected = 0
	case key == "end":
		b.selected = max(last, 0)
	case key == "enter" && item == createHere:
		return send(pushScreen{b.nameField()})
	case key == "enter" && item == openHere:
		return b.open(b.dir)
	case key == "enter" && !b.create && item != "" && b.entry(item).repo:
		return b.open(filepath.Join(b.dir, item))
	case (key == "enter" || key == "right") && item != "" && item != openHere && item != createHere:
		return b.read(filepath.Join(b.dir, item), "")
	case key == "left" || key == "backspace" && b.filter == "":
		if parent := filepath.Dir(b.dir); parent != b.dir {
			return b.read(parent, filepath.Base(b.dir))
		}
	case key == "backspace":
		r := []rune(b.filter)
		b.filter, b.selected = string(r[:len(r)-1]), 0
	case typed(msg):
		b.filter, b.selected = b.filter+msg.Text, 0
	}
	return nil
}

func (b *dirBrowser) open(path string) tea.Cmd {
	b.opening = true
	return send(createProject{req: localRequest(path)})
}

// nameField asks the name of the project to create in the directory
// listed.
func (b *dirBrowser) nameField() *fieldScreen {
	dir := b.dir
	return &fieldScreen{
		heading: createTitle,
		prompt:  "Name of the project, created in " + tildePath(dir, b.userHome) + ":",
		empty:   "type a name",
		working: "creating…",
		submit:  func(name string) tea.Msg { return createProject{req: newRequest(dir, name)} },
	}
}

func (b *dirBrowser) draw(buf uv.Screen, area uv.Rectangle, st style.Style) {
	// A long path loses its start: where it ends is what matters.
	path := tildePath(b.dir, b.userHome)
	if over := ansi.StringWidth(path) - area.Dx(); over > 0 {
		path = ansi.TruncateLeft(path, over+1, "…")
	}
	lines := []string{st.Name(path)}
	if b.filter != "" {
		lines = append(lines, "> "+b.filter+"_")
	}
	var foot []string
	switch {
	case b.opening:
		foot = []string{"", st.Muted(wrap("opening…", area.Dx()))}
	case b.failure != "":
		foot = append([]string{""}, strings.Split(wrap(b.failure, area.Dx()), "\n")...)
		for i := 1; i < len(foot); i++ {
			foot[i] = st.Failure(foot[i])
		}
	}
	items := b.items()
	b.rows = max(area.Dy()-len(lines)-len(foot), 1)
	switch {
	case b.reading && b.dir == "":
		lines = append(lines, st.Muted("reading…"))
	case b.readErr != nil:
		lines = append(lines, strings.Split(st.Failure(wrap("cannot read it: "+errText(b.readErr), area.Dx())), "\n")...)
	case len(items) == 0 && b.filter != "":
		lines = append(lines, st.Muted("no directory matches"))
	case len(items) == 0:
		lines = append(lines, st.Muted("no directories here"))
	}
	selected := max(min(b.selected, len(items)-1), 0)
	from := max(min(selected-b.rows/2, len(items)-b.rows), 0)
	for i := from; i < min(from+b.rows, len(items)); i++ {
		text := items[i] + "/"
		switch {
		case items[i] == openHere || items[i] == createHere:
			text = st.Command(items[i])
		case b.entry(items[i]).repo:
			text += " " + st.Muted("(git)")
		}
		if i == selected {
			lines = append(lines, st.Focused("▸ ")+st.Name(text))
		} else {
			lines = append(lines, "  "+text)
		}
	}
	uv.NewStyledString(strings.Join(append(lines, foot...), "\n")).Draw(buf, area)
}

// tildePath returns path with the user's home as ~.
func tildePath(path, home string) string {
	switch {
	case home == "" || home == "/":
		return path
	case path == home:
		return "~"
	case strings.HasPrefix(path, home+string(filepath.Separator)):
		return "~" + path[len(home):]
	}
	return path
}

// errText is what a filesystem error says without the path, which the
// browser already shows.
func errText(err error) string {
	if e, ok := errors.AsType[*os.PathError](err); ok {
		return e.Err.Error()
	}
	return err.Error()
}
