package aca

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// A line of TranscriptFile that its rendering wrote, not a message quoting one. Message
// text and thinking are not fenced, so these match strictly and only after a blank line,
// and a fence counts only straight after a tool's or a result's heading.
var (
	transcriptMessageHeading = regexp.MustCompile(`^## (User|Assistant|Compacted|System) · (\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2}))$`)
	transcriptToolHeading    = regexp.MustCompile(`^### Tool: (\S+)$`)
	transcriptResultHeading  = regexp.MustCompile(`^### Result( \(error\))?$`)
	transcriptFence          = regexp.MustCompile("^`{3,}$")
)

const (
	transcriptThinkingHeading = "### Thinking"
	// transcriptTextHeading ends thinking with text in the same message. Lines rendered
	// before it existed have no end to their thinking but the next heading, so such text
	// is not in their brief.
	transcriptTextHeading = "### Text"
)

// compactionsSummarize are the ACAs whose compactions keep a summary the brief can
// read, so that one is a starting point. Codex keeps its summary encrypted: starting at
// its compaction would leave out everything before it.
var compactionsSummarize = map[string]bool{ClaudeCodeID: true, AntigravityID: true, CursorID: true}

// readTranscriptBrief reads TranscriptFile once, keeping no more than limit: a new
// starting point drops all before it, and the oldest messages after it go once they do
// not fit. since is the sessions it may read, nil for all of them.
func readTranscriptBrief(backupDir string, since map[string]bool, limit int) (Brief, error) {
	f, err := os.Open(filepath.Join(backupDir, TranscriptFile))
	if errors.Is(err, os.ErrNotExist) {
		return Brief{}, nil
	}
	if err != nil {
		return Brief{}, err
	}
	defer f.Close()
	r := transcriptBriefReader{lines: bufio.NewReader(f), since: since, inBounds: since == nil, summarized: true, limit: limit, omitted: -1}
	if err := r.read(); err != nil {
		return Brief{}, err
	}
	return r.brief(), nil
}

// briefMessage is a message on its way into a brief.
type briefMessage struct {
	line  int64 // of its heading in TranscriptFile
	lines []*briefLine
	size  int
}

type briefLine struct {
	text string
	// A tool call's line is written at the end, when its result has said if it failed.
	tool   string
	failed bool
}

func (l *briefLine) String() string {
	if l.tool == "" {
		return l.text
	}
	if l.failed {
		return fmt.Sprintf("- %s (line %s, failed)", l.tool, l.text)
	}
	return fmt.Sprintf("- %s (line %s)", l.tool, l.text)
}

func (l *briefLine) blank() bool { return l.text == "" && l.tool == "" }

func (m *briefMessage) add(l *briefLine) {
	n := len(m.lines)
	if l.blank() && (n == 0 || m.lines[n-1].blank()) {
		return // what was left out between leaves its blank lines
	}
	if l.tool != "" && n >= 2 && m.lines[n-1].blank() && m.lines[n-2].tool != "" {
		m.lines = m.lines[:n-1] // a tool's lines read as a list
	}
	m.lines = append(m.lines, l)
	m.size += len(l.text) + len(l.tool) + 1
}

// transcriptBriefReader goes through TranscriptFile line by line.
type transcriptBriefReader struct {
	lines *bufio.Reader
	// back is what a lookahead read and did not use.
	back []string
	n    int64 // the number of the next line read
	eof  bool

	since    map[string]bool
	inBounds bool // past the latest /clear
	// summarized is whether a compaction in the session being read is a starting point.
	summarized bool
	limit      int

	start   *briefMessage
	rest    []*briefMessage
	current *briefMessage
	size    int // of start and rest
	omitted int64
	// unanswered are the tool calls waiting for a result, oldest first: results are
	// rendered in the order they came.
	unanswered []*briefLine
	lastAt     time.Time
	thinking   bool
	prevBlank  bool
}

func (r *transcriptBriefReader) next() (string, bool, error) {
	if len(r.back) > 0 {
		line := r.back[0]
		r.back = r.back[1:]
		r.n++
		return line, true, nil
	}
	if r.eof {
		return "", false, nil
	}
	line, err := r.lines.ReadString('\n')
	if errors.Is(err, io.EOF) {
		r.eof = true // a line with no end is one being written
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	r.n++
	return line[:len(line)-1], true, nil
}

func (r *transcriptBriefReader) unread(lines ...string) {
	r.back = append(append([]string{}, lines...), r.back...)
	r.n -= int64(len(lines))
}

func (r *transcriptBriefReader) read() error {
	r.prevBlank = true
	for {
		line, ok, err := r.next()
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		at := r.n - 1
		wasBlank := r.prevBlank
		r.prevBlank = line == ""
		if !wasBlank {
			r.text(line)
			continue
		}
		if m := transcriptSessionHeading.FindStringSubmatch(line); m != nil {
			r.session(m[1], m[2])
			r.text(line)
			continue
		}
		if m := transcriptMessageHeading.FindStringSubmatch(line); m != nil && r.message(at, line, m[1], m[2]) {
			continue
		}
		if line == transcriptThinkingHeading {
			r.thinking = true
			continue
		}
		if line == transcriptTextHeading {
			r.thinking = false
			continue
		}
		name := ""
		if m := transcriptToolHeading.FindStringSubmatch(line); m != nil {
			name = m[1]
		}
		failed, isResult := false, false
		if m := transcriptResultHeading.FindStringSubmatch(line); m != nil && len(r.unanswered) > 0 {
			failed, isResult = m[1] != "", true
		}
		if name == "" && !isResult {
			r.text(line)
			continue
		}
		body, found, err := r.fencedBody(name != "")
		if err != nil {
			return err
		}
		if !found {
			r.text(line)
			continue
		}
		r.prevBlank = true
		r.thinking = false
		if name != "" {
			tool := &briefLine{text: fmt.Sprint(at), tool: name + transcriptShortInput(body)}
			r.unanswered = append(r.unanswered, tool)
			if r.current != nil {
				r.current.add(tool)
			}
			continue
		}
		r.unanswered[0].failed = failed
		r.unanswered = r.unanswered[1:]
	}
	r.finish()
	return nil
}

// session goes past the /clear at the first session heading since it. A heading with no
// ACA is Claude Code's, from before headings named one.
func (r *transcriptBriefReader) session(id, acaID string) {
	r.summarized = acaID == "" || compactionsSummarize[acaID]
	if !r.inBounds && r.since[id] {
		r.inBounds = true
		r.finish()
		r.start, r.rest, r.current, r.size, r.omitted = nil, nil, nil, 0, -1
	}
}

// message starts a message at its heading, and reports whether the heading was one. A
// compaction summary is the one heading that a quotation could do harm by, moving the
// start, so it must not be older than what came before it; a quotation is.
func (r *transcriptBriefReader) message(at int64, line, name, timestamp string) bool {
	when, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return false
	}
	if name == "Compacted" && when.Before(r.lastAt) {
		return false
	}
	if when.After(r.lastAt) {
		r.lastAt = when
	}
	r.finish()
	r.thinking = false
	if !r.inBounds {
		return true
	}
	if name == "Compacted" && r.summarized || r.start == nil && name == "User" {
		r.start, r.rest, r.size, r.omitted = nil, nil, 0, -1
		r.current = &briefMessage{line: at}
		r.current.add(&briefLine{text: line})
		return true
	}
	if r.start != nil {
		r.current = &briefMessage{line: at}
		r.current.add(&briefLine{text: line})
	}
	return true
}

// text adds a line of a message, unless it is thinking, which the brief leaves out.
func (r *transcriptBriefReader) text(line string) {
	if r.thinking || r.current == nil {
		return
	}
	r.current.add(&briefLine{text: line})
}

// fencedBody reads what a tool's or a result's heading has under it, a blank line and a
// fenced block, whose first line it returns if keep. Anything else leaves the lines
// unread: the heading was a message quoting one.
func (r *transcriptBriefReader) fencedBody(keep bool) (string, bool, error) {
	blank, ok, err := r.next()
	if err != nil || !ok {
		return "", false, err
	}
	if blank != "" {
		r.unread(blank)
		return "", false, nil
	}
	fence, ok, err := r.next()
	if err != nil {
		return "", false, err
	}
	if !ok {
		r.unread(blank)
		return "", false, nil
	}
	if !transcriptFence.MatchString(fence) {
		r.unread(blank, fence)
		return "", false, nil
	}
	var body string
	for first := true; ; first = false {
		line, ok, err := r.next()
		if err != nil {
			return "", false, err
		}
		if !ok || line == fence {
			return body, true, nil
		}
		if first && keep {
			body = line[:min(len(line), transcriptInputKept)]
		}
	}
}

// finish ends the message being read: it starts the brief, or joins the rest, whose
// oldest go while it does not fit. The last always stays.
func (r *transcriptBriefReader) finish() {
	m := r.current
	r.current = nil
	if m == nil {
		return
	}
	for len(m.lines) > 0 && m.lines[len(m.lines)-1].blank() {
		m.lines = m.lines[:len(m.lines)-1]
	}
	r.size += m.size
	if r.start == nil {
		r.start = m
		return
	}
	r.rest = append(r.rest, m)
	for r.size > r.limit && len(r.rest) > 1 {
		if r.omitted < 0 {
			r.omitted = r.rest[0].line
		}
		r.size -= r.rest[0].size
		r.rest = r.rest[1:]
	}
}

func (r *transcriptBriefReader) brief() Brief {
	b := Brief{Next: r.n}
	if r.start == nil {
		b.From = r.n
		return b
	}
	b.From = r.start.line
	render := func(m *briefMessage) []string {
		lines := make([]string, 0, len(m.lines)+1)
		for _, l := range m.lines {
			lines = append(lines, l.String())
		}
		return append(lines, "")
	}
	b.Start = render(r.start)
	for _, m := range r.rest {
		b.Rest = append(b.Rest, render(m)...)
	}
	if r.omitted >= 0 {
		b.OmittedFrom, b.OmittedCount = r.omitted, r.rest[0].line-r.omitted
	}
	return b
}

const (
	// transcriptInputKept is as much of a tool's input as its short form looks at: a
	// Write's holds a whole file.
	transcriptInputKept = 16 << 10
	transcriptInputShow = 100
)

// transcriptInputKeys are what a tool's input is best told by, in order.
// Antigravity's tools name theirs with a capital. Cursor's CallDynamicTool is told by
// the tool it calls, its toolName, as its first string is the tool's namespace.
var transcriptInputKeys = []string{"command", "CommandLine", "file_path", "path", "AbsolutePath", "Path", "TargetFile", "notebook_path", "pattern", "glob_pattern", "query", "search_term", "url", "skill", "description", "prompt", "toolName"}

// transcriptShortInput is a tool's input in a few words, with a space before them. An
// input that is not a JSON object, as Codex's apply_patch writes, is told by its first
// line.
func transcriptShortInput(input string) string {
	if !strings.HasPrefix(strings.TrimSpace(input), "{") {
		first, _, _ := strings.Cut(strings.TrimSpace(input), "\n")
		return transcriptShown(first)
	}
	values := map[string]string{}
	var first string
	d := json.NewDecoder(strings.NewReader(input))
	if _, err := d.Token(); err != nil {
		return ""
	}
	// By its tokens, as an input past transcriptInputKept is cut off: what comes first
	// is still read.
	for d.More() {
		key, err := d.Token()
		if err != nil {
			break
		}
		var value any
		if err := d.Decode(&value); err != nil {
			break
		}
		text, ok := value.(string)
		// Codex's exec gives its command as the shell's arguments, the command line
		// last.
		if words, isList := value.([]any); isList && key == "command" && len(words) > 0 {
			text, ok = words[len(words)-1].(string)
		}
		if ok {
			if first == "" {
				first = text
			}
			values[fmt.Sprint(key)] = text
		}
	}
	short := first
	for _, key := range transcriptInputKeys {
		if v, ok := values[key]; ok {
			short = v
			break
		}
	}
	return transcriptShown(short)
}

// transcriptShown is a short input on one line and cut to transcriptInputShow, with a
// space before it, or nothing when it is empty.
func transcriptShown(short string) string {
	short = strings.Join(strings.Fields(short), " ")
	if short == "" {
		return ""
	}
	if runes := []rune(short); len(runes) > transcriptInputShow {
		short = string(runes[:transcriptInputShow]) + "…"
	}
	return " " + short
}
