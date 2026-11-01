package aca

import (
	"bufio"
	"context"
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
	claudeCodeMessageHeading = regexp.MustCompile(`^## (User|Assistant|Compacted) · (\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2}))$`)
	claudeCodeSessionHeading = regexp.MustCompile(`^# Session (\S+)$`)
	claudeCodeToolHeading    = regexp.MustCompile(`^### Tool: (\S+)$`)
	claudeCodeResultHeading  = regexp.MustCompile(`^### Result( \(error\))?$`)
	claudeCodeFence          = regexp.MustCompile("^`{3,}$")
)

const (
	claudeCodeThinkingHeading = "### Thinking"
	// claudeCodeTextHeading ends thinking with text in the same message. Lines rendered
	// before it existed have no end to their thinking but the next heading, so such text
	// is not in their brief.
	claudeCodeTextHeading = "### Text"
	// claudeCodeInputKept is as much of a tool's input as its short form looks at: a
	// Write's holds a whole file.
	claudeCodeInputKept = 16 << 10
	claudeCodeInputShow = 100
)

// claudeCodeInputKeys are what a tool's input is best told by, in order.
var claudeCodeInputKeys = []string{"command", "file_path", "path", "notebook_path", "pattern", "query", "url", "skill", "description", "prompt"}

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

// Brief implements AiCodingAgent. It reads TranscriptFile once, keeping no more than
// limit: a new starting point drops all before it, and the oldest messages after it go
// once they do not fit.
func (ClaudeCode) Brief(_ context.Context, l Launch, limit int) (Brief, error) {
	starts, err := readClaudeCodeStarts(l.BackupDir)
	if err != nil {
		return Brief{}, err
	}
	since := claudeCodeSinceClear(starts)
	f, err := os.Open(filepath.Join(l.BackupDir, TranscriptFile))
	if errors.Is(err, os.ErrNotExist) {
		return Brief{}, nil
	}
	if err != nil {
		return Brief{}, err
	}
	defer f.Close()
	r := claudeCodeBriefReader{lines: bufio.NewReader(f), since: since, inBounds: since == nil, limit: limit, omitted: -1}
	if err := r.read(); err != nil {
		return Brief{}, err
	}
	return r.brief(), nil
}

// claudeCodeSinceClear is the sessions from the latest /clear on, nil when there was
// none: the brief looks no further back. Those a restart began do not stop it. Which one
// was written first decides, as a /clear's session may have written nothing.
func claudeCodeSinceClear(starts []claudeCodeStart) map[string]bool {
	var since map[string]bool
	for _, s := range starts {
		if s.Source == claudeCodeSourceClear {
			since = map[string]bool{}
		}
		if since != nil {
			since[s.SessionID] = true
		}
	}
	return since
}

// claudeCodeBriefReader goes through TranscriptFile line by line.
type claudeCodeBriefReader struct {
	lines *bufio.Reader
	// back is what a lookahead read and did not use.
	back []string
	n    int64 // the number of the next line read
	eof  bool

	since    map[string]bool
	inBounds bool // past the latest /clear
	limit    int

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

func (r *claudeCodeBriefReader) next() (string, bool, error) {
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

func (r *claudeCodeBriefReader) unread(lines ...string) {
	r.back = append(append([]string{}, lines...), r.back...)
	r.n -= int64(len(lines))
}

func (r *claudeCodeBriefReader) read() error {
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
		if m := claudeCodeSessionHeading.FindStringSubmatch(line); m != nil {
			r.session(m[1])
			r.text(line)
			continue
		}
		if m := claudeCodeMessageHeading.FindStringSubmatch(line); m != nil && r.message(at, line, m[1], m[2]) {
			continue
		}
		if line == claudeCodeThinkingHeading {
			r.thinking = true
			continue
		}
		if line == claudeCodeTextHeading {
			r.thinking = false
			continue
		}
		name := ""
		if m := claudeCodeToolHeading.FindStringSubmatch(line); m != nil {
			name = m[1]
		}
		failed, isResult := false, false
		if m := claudeCodeResultHeading.FindStringSubmatch(line); m != nil && len(r.unanswered) > 0 {
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
			tool := &briefLine{text: fmt.Sprint(at), tool: name + claudeCodeShortInput(body)}
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

// session goes past the /clear at the first session heading since it.
func (r *claudeCodeBriefReader) session(id string) {
	if !r.inBounds && r.since[id] {
		r.inBounds = true
		r.finish()
		r.start, r.rest, r.current, r.size, r.omitted = nil, nil, nil, 0, -1
	}
}

// message starts a message at its heading, and reports whether the heading was one. A
// compaction summary is the one heading that a quotation could do harm by, moving the
// start, so it must not be older than what came before it; a quotation is.
func (r *claudeCodeBriefReader) message(at int64, line, name, timestamp string) bool {
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
	if name == "Compacted" || r.start == nil && name == "User" {
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
func (r *claudeCodeBriefReader) text(line string) {
	if r.thinking || r.current == nil {
		return
	}
	r.current.add(&briefLine{text: line})
}

// fencedBody reads what a tool's or a result's heading has under it, a blank line and a
// fenced block, whose first line it returns if keep. Anything else leaves the lines
// unread: the heading was a message quoting one.
func (r *claudeCodeBriefReader) fencedBody(keep bool) (string, bool, error) {
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
	if !claudeCodeFence.MatchString(fence) {
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
			body = line[:min(len(line), claudeCodeInputKept)]
		}
	}
}

// finish ends the message being read: it starts the brief, or joins the rest, whose
// oldest go while it does not fit. The last always stays.
func (r *claudeCodeBriefReader) finish() {
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

func (r *claudeCodeBriefReader) brief() Brief {
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

// claudeCodeShortInput is a tool's input in a few words, with a space before them.
func claudeCodeShortInput(input string) string {
	values := map[string]string{}
	var first string
	d := json.NewDecoder(strings.NewReader(input))
	if t, err := d.Token(); err != nil || t != json.Delim('{') {
		return ""
	}
	// By its tokens, as an input past claudeCodeInputKept is cut off: what comes first
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
		if text, ok := value.(string); ok {
			if first == "" {
				first = text
			}
			values[fmt.Sprint(key)] = text
		}
	}
	short := first
	for _, key := range claudeCodeInputKeys {
		if v, ok := values[key]; ok {
			short = v
			break
		}
	}
	short = strings.Join(strings.Fields(short), " ")
	if short == "" {
		return ""
	}
	if runes := []rune(short); len(runes) > claudeCodeInputShow {
		short = string(runes[:claudeCodeInputShow]) + "…"
	}
	return " " + short
}
