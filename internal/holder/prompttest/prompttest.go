// Package prompttest is a program for tests that draws Claude Code's prompts
// as its 2.1.323 recordings show them, and answers keys as Claude does: the
// arrows move the cursor, Space ticks a box, typing or a paste goes into a
// Type something option, and Enter chooses, ticks or submits. A test binary
// runs it as "<binary> prompt-program <scenario>", calling RunIfProgram first
// in its TestMain, in place of a real ACA.
//
// It writes, in its working directory, keys (a line for each key it took),
// answered (a line for how the prompt closed) and held (while it holds a key,
// as Scenario.HoldAfter asks). It ends when the file end appears there.
package prompttest

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// arg is the first argument that makes a test binary this program.
const arg = "prompt-program"

// Scenario is what the program asks.
type Scenario struct {
	// YesNo is a permission prompt; Questions a decision of one or several
	// questions. Exactly one is given.
	YesNo     *YesNo     `json:"yes_no,omitempty"`
	Questions []Question `json:"questions,omitempty"`
	// ChangeAfter, above 0, asks the first question anew, its question ending
	// in " (asked again)", once that many keys were taken: a prompt Claude
	// replaced while it was being answered.
	ChangeAfter int `json:"change_after,omitempty"`
	// HoldAfter, above 0, holds the key of that number, writing held, until one
	// more key comes, and takes both before it draws: a person's key in between.
	HoldAfter int `json:"hold_after,omitempty"`
	// DeafToEnter takes no Enter: a prompt that does not close.
	DeafToEnter bool `json:"deaf_to_enter,omitempty"`
}

// YesNo is a permission: Claude's Do you want to proceed? on a command.
type YesNo struct {
	Title   string   `json:"title"`
	Command string   `json:"command"`
	Options []string `json:"options"`
}

// Question is one question of a decision, to which Claude adds its Type
// something, its Submit row when Multi, and Chat about this.
type Question struct {
	Tab      string   `json:"tab"`
	Question string   `json:"question"`
	Options  []string `json:"options"`
	Multi    bool     `json:"multi,omitempty"`
}

// Answered is how the prompt closed, as the program writes it to answered.
type Answered struct {
	// How is answered, chat, cancelled or rejected (Esc).
	How string `json:"how"`
	// Answers is each question's: the labels chosen or ticked, a typed text as
	// typed; a permission's is the option chosen.
	Answers [][]string `json:"answers"`
}

// Script is the shell script that runs the program on scenario, the file
// Write wrote, with exe the test binary.
func Script(exe, scenario string) string {
	return fmt.Sprintf("stty raw -echo\nexec '%s' %s '%s'\n", exe, arg, scenario)
}

// Write writes s to path, for Script.
func Write(path string, s Scenario) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// ReadAnswered reads the answered file in dir: nil while the prompt is open.
func ReadAnswered(dir string) (*Answered, error) {
	b, err := os.ReadFile(dir + "/answered")
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var a Answered
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// Keys reads the keys file in dir: the names of the keys taken, in order.
func Keys(dir string) []string {
	b, _ := os.ReadFile(dir + "/keys")
	return strings.Fields(string(b))
}

// RunIfProgram runs the program and exits when the test binary was started as
// it.
func RunIfProgram() {
	if len(os.Args) < 3 || os.Args[1] != arg {
		return
	}
	b, err := os.ReadFile(os.Args[2])
	var s Scenario
	if err == nil {
		err = json.Unmarshal(b, &s)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	go func() {
		for {
			if _, err := os.Stat("end"); err == nil {
				os.Exit(0)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	p := newProgram(s, os.Stdout, "")
	p.run(os.Stdin)
	os.Exit(0)
}

// option is one row of a question as drawn.
type option struct {
	label string
	kind  string // choice, text, submit or chat
}

type program struct {
	s   Scenario
	out io.Writer
	// dir is where it writes its files: the working directory when "".
	dir string
	// The prompt's state: the question shown, the cursor, each question's ticks
	// and typed text, and the answers given.
	question int
	cursor   int
	ticks    []map[int]bool
	typed    []string
	answers  [][]string
	review   bool
	closed   bool
	changed  bool
	taken    int // keys
}

func newProgram(s Scenario, out io.Writer, dir string) *program {
	p := &program{s: s, out: out, dir: dir}
	for range s.Questions {
		p.ticks = append(p.ticks, map[int]bool{})
		p.typed = append(p.typed, "")
		p.answers = append(p.answers, nil)
	}
	return p
}

// run takes keys from in and draws after each read, until in ends.
func (p *program) run(in io.Reader) {
	p.draw()
	buf := make([]byte, 4096)
	var pending []byte
	held := false
	for {
		n, err := in.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			for {
				key, rest, ok := nextKey(pending)
				if !ok {
					break
				}
				pending = rest
				p.taken++
				p.appendLine("keys", keyName(key))
				if p.s.HoldAfter > 0 && p.taken == p.s.HoldAfter {
					// Taken with the next, as both arrive before a redraw.
					p.appendLine("held", "")
					held = true
					p.take(key)
					continue
				}
				p.take(key)
				if p.s.ChangeAfter > 0 && p.taken == p.s.ChangeAfter && !p.closed {
					p.changed, p.question, p.cursor, p.review = true, 0, 0, false
				}
				held = false
			}
			if !held {
				p.draw()
			}
		}
		if err != nil {
			return
		}
	}
}

// nextKey splits the first key off p: an escape sequence, a paste whole, or
// one character; false when p holds only the start of one.
func nextKey(p []byte) (key, rest []byte, ok bool) {
	const pasteStart, pasteEnd = "\x1b[200~", "\x1b[201~"
	switch {
	case len(p) == 0:
		return nil, p, false
	case strings.HasPrefix(string(p), pasteStart):
		end := strings.Index(string(p), pasteEnd)
		if end < 0 {
			return nil, p, false
		}
		n := end + len(pasteEnd)
		return p[:n], p[n:], true
	case p[0] == 0x1b:
		if len(p) >= 3 && (p[1] == '[' || p[1] == 'O') {
			return p[:3], p[3:], true
		}
		if len(p) == 1 {
			return p, nil, true // Esc alone
		}
		if p[1] != '[' && p[1] != 'O' {
			return p[:1], p[1:], true
		}
		return nil, p, false
	}
	for n := 1; n <= len(p); n++ {
		if s := string(p[:n]); utf8Full(s) {
			return p[:n], p[n:], true
		}
	}
	return nil, p, false
}

func utf8Full(s string) bool { return strings.ToValidUTF8(s, "�") == s }

func keyName(key []byte) string {
	switch s := string(key); {
	case s == "\x1b[A" || s == "\x1bOA":
		return "up"
	case s == "\x1b[B" || s == "\x1bOB":
		return "down"
	case s == "\r":
		return "enter"
	case s == " ":
		return "space"
	case s == "\x1b":
		return "esc"
	case strings.HasPrefix(s, "\x1b[200~"):
		return "paste"
	default:
		return "char"
	}
}

func (p *program) appendLine(name, line string) {
	f, err := os.OpenFile(filepath.Join(p.dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

// options is the rows of the question shown, or of the review or the
// permission.
func (p *program) options() []option {
	if p.s.YesNo != nil {
		var o []option
		for _, label := range p.s.YesNo.Options {
			o = append(o, option{label, "choice"})
		}
		return o
	}
	if p.review {
		return []option{{"Submit answers", "choice"}, {"Cancel", "choice"}}
	}
	q := p.s.Questions[p.question]
	var o []option
	for _, label := range q.Options {
		o = append(o, option{label, "choice"})
	}
	text := "Type something."
	if q.Multi {
		text = "Type something"
	}
	if p.typed[p.question] != "" {
		text = p.typed[p.question]
	}
	o = append(o, option{text, "text"})
	if q.Multi {
		o = append(o, option{"Submit", "submit"})
	}
	return append(o, option{"Chat about this", "chat"})
}

func (p *program) take(key []byte) {
	if p.closed {
		return
	}
	options := p.options()
	switch keyName(key) {
	case "up":
		p.cursor = max(p.cursor-1, 0)
	case "down":
		p.cursor = min(p.cursor+1, len(options)-1)
	case "esc":
		p.close("rejected")
	case "space":
		if o := options[p.cursor]; p.multi() && (o.kind == "choice" || o.kind == "text") {
			p.toggle(p.cursor)
		}
	case "enter":
		if !p.s.DeafToEnter {
			p.enter(options[p.cursor])
		}
	case "paste", "char":
		if o := options[p.cursor]; p.s.YesNo == nil && !p.review && o.kind == "text" {
			text := strings.TrimSuffix(strings.TrimPrefix(string(key), "\x1b[200~"), "\x1b[201~")
			p.typed[p.question] += text
			if p.multi() {
				p.ticks[p.question][p.cursor] = true
			}
		}
	}
}

func (p *program) multi() bool {
	return p.s.YesNo == nil && !p.review && p.s.Questions[p.question].Multi
}

func (p *program) toggle(i int) {
	t := p.ticks[p.question]
	t[i] = !t[i]
	if !t[i] {
		delete(t, i)
	}
}

func (p *program) enter(o option) {
	switch {
	case p.s.YesNo != nil:
		p.answers = [][]string{{o.label}}
		p.close("answered")
	case p.review:
		if p.cursor == 0 {
			p.close("answered")
		} else {
			p.close("cancelled")
		}
	case o.kind == "chat":
		p.close("chat")
	case p.multi() && o.kind != "submit":
		p.toggle(p.cursor)
	case p.multi():
		var labels []string
		options := p.options()
		for i := range options {
			if p.ticks[p.question][i] {
				labels = append(labels, options[i].label)
			}
		}
		p.next(labels)
	case o.kind == "text" && p.typed[p.question] == "":
		// Nothing typed: nothing to choose.
	default:
		p.next([]string{o.label})
	}
}

// next keeps the answer and shows the next question, the review after the
// last of several or of a multi-select, or closes.
func (p *program) next(answer []string) {
	p.answers[p.question] = answer
	if p.question+1 < len(p.s.Questions) {
		p.question++
		p.cursor = 0
		return
	}
	if len(p.s.Questions) > 1 || p.s.Questions[0].Multi {
		p.review, p.cursor = true, 0
		return
	}
	p.close("answered")
}

func (p *program) close(how string) {
	p.closed = true
	answers := p.answers
	if how != "answered" {
		answers = nil
	}
	b, _ := json.Marshal(Answered{How: how, Answers: answers})
	p.appendLine("answered", string(b))
}

const rule = "────────────────────────────────────────────────────────────────────────────────"

// draw draws the screen whole, as Claude Code 2.1.323 does at 80 columns.
func (p *program) draw() {
	var lines []string
	switch {
	case p.closed:
		lines = []string{"", rule, "❯ ", rule, "  ⏸ manual mode on · ? for shortcuts · ← 1 agent"}
	case p.s.YesNo != nil:
		lines = p.yesNo()
	case p.review:
		lines = p.reviewLines()
	default:
		lines = p.decision()
	}
	fmt.Fprint(p.out, "\x1b[2J\x1b[H"+strings.Join(lines, "\r\n"))
}

func (p *program) yesNo() []string {
	dashed := strings.Repeat("╌", 80)
	lines := []string{"", rule, " " + p.s.YesNo.Title, " Run it", dashed, " " + p.s.YesNo.Command, dashed, " Do you want to proceed?"}
	for i, o := range p.options() {
		lines = append(lines, p.mark(i, " ", "  ")+fmt.Sprintf("%d. %s", i+1, o.label))
	}
	return append(lines, "", " Esc to cancel · Tab to amend")
}

// tabs is the tab line: one question's name, or every question's, with the
// answered ones ☒, between arrows.
func (p *program) tabs() string {
	box := func(i int) string {
		if p.answers[i] != nil || (len(p.s.Questions) == 1 && len(p.ticks[i]) > 0) {
			return "☒"
		}
		return "☐"
	}
	if len(p.s.Questions) == 1 && !p.s.Questions[0].Multi {
		return " " + box(0) + " " + p.s.Questions[0].Tab
	}
	line := "← "
	for i, q := range p.s.Questions {
		line += " " + box(i) + " " + q.Tab + " "
	}
	return line + " ✔ Submit  →"
}

func (p *program) decision() []string {
	q := p.s.Questions[p.question]
	question := q.Question
	if p.changed {
		question += " (asked again)"
	}
	lines := []string{"", rule, p.tabs(), "", question, ""}
	for i, o := range p.options() {
		mark := p.mark(i, "", "  ")
		switch o.kind {
		case "submit":
			lines = append(lines, mark+"   Submit")
		case "chat":
			number := i + 1
			if q.Multi {
				number = i // the Submit row has none
			}
			lines = append(lines, rule, mark+fmt.Sprintf("%d. %s", number, o.label))
		default:
			box := ""
			if q.Multi {
				box = "[ ] "
				if p.ticks[p.question][i] {
					box = "[✔] "
				}
			}
			lines = append(lines, mark+fmt.Sprintf("%d. %s%s", i+1, box, o.label))
			if o.kind == "choice" {
				lines = append(lines, "     "+o.label)
			}
		}
	}
	footer := "Enter to select · ↑/↓ to navigate · Esc to cancel"
	if len(p.s.Questions) > 1 {
		footer = "Enter to select · Tab/Arrow keys to navigate · Esc to cancel"
	}
	return append(lines, "", footer)
}

func (p *program) reviewLines() []string {
	lines := []string{"", rule, p.tabs(), "", "Review your answers", ""}
	for i, q := range p.s.Questions {
		lines = append(lines, " ● "+q.Question, "   → "+strings.Join(p.answers[i], ", "))
	}
	lines = append(lines, "", "Ready to submit your answers?", "")
	for i, o := range p.options() {
		lines = append(lines, p.mark(i, "", "  ")+fmt.Sprintf("%d. %s", i+1, o.label))
	}
	return lines
}

// mark is the cursor before row i, after indent, or the blank as wide.
func (p *program) mark(i int, indent, blank string) string {
	if i == p.cursor {
		return indent + "❯ "
	}
	return indent + blank
}
