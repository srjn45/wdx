package tide

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/style"
)

var colorCode = regexp.MustCompile("\x1b\\[[0-9;]*m")

// kindAt returns the kind of the text want on the line.
func kindAt(t *testing.T, colors [][]colorSpan, lines []string, row int, want string) tokenKind {
	t.Helper()
	at := strings.Index(shownText(lines[row]), want)
	if at < 0 {
		t.Fatalf("no %q in %q", want, lines[row])
	}
	for _, s := range colors[row] {
		if at < s.end {
			return s.kind
		}
	}
	t.Fatalf("no colour for %q in %+v", want, colors[row])
	return tokenPlain
}

func TestHighlightLanguages(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		file     string
		source   string
		language string
		want     map[string]tokenKind
	}{
		{"a/b.go", "package a // the note\nfunc F(n int) string { return \"s\" + 42 }", "Go", map[string]tokenKind{
			"package": tokenKeyword, "// the note": tokenComment, "F": tokenFunction, "int": tokenType, `"s"`: tokenString, "42": tokenNumber,
		}},
		{"tabs.go", "func g() {\n\treturn \"s\" // \x07 after\n}", "Go", map[string]tokenKind{
			`"s"`: tokenString, "// · after": tokenComment,
		}},
		{"x.py", "class C:\n    def go(self):\n        return 'y'", "Python", map[string]tokenKind{
			"class": tokenKeyword, "C": tokenType, "go": tokenFunction, "'y'": tokenString,
		}},
		{"tool", "#!/bin/bash\necho hi # note", "Bash", map[string]tokenKind{"# note": tokenComment}},
		{"Makefile", "all:\n\t@echo 1 # note", "Makefile", map[string]tokenKind{"# note": tokenComment}},
	} {
		lines := strings.Split(tc.source, "\n")
		colors, language := highlightLines(tc.file, lines)
		if language != tc.language || colors == nil {
			t.Errorf("%s: language %q, colours %v", tc.file, language, colors)
			continue
		}
		for text, want := range tc.want {
			row := 0
			for i, l := range lines {
				if strings.Contains(shownText(l), text) {
					row = i
				}
			}
			if got := kindAt(t, colors, lines, row, text); got != want {
				t.Errorf("%s: %q is %v, want %v", tc.file, text, got, want)
			}
		}
	}
}

func TestHighlightLeavesPlain(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		what  string
		file  string
		lines []string
	}{
		{"no language", "notes.unknownext", []string{"def f(): pass"}},
		{"too large", "a.go", []string{strings.Repeat("x", maxHighlighted+1)}},
	} {
		if colors, language := highlightLines(tc.file, tc.lines); colors != nil || language != "" {
			t.Errorf("%s: %q, %d lines", tc.what, language, len(colors))
		}
	}
	if colors, _ := highlightLines("a.go", []string{strings.Repeat("x", maxHighlighted)}); colors == nil {
		t.Error("left plain at the limit")
	}
}

func TestHighlightByFirstLine(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]string{
		"#!/bin/sh":                    "Bash",
		"#!/usr/bin/env bash":          "Bash",
		"#!/usr/bin/env python3":       "Python",
		"#!/usr/bin/python3.12 -u":     "Python",
		"#!/usr/bin/env -S python3 -u": "Python",
		"#!/usr/bin/env A=1 ruby":      "Ruby",
		"#!/usr/bin/perl":              "Perl",
		"#!/usr/bin/env node":          "JavaScript",
		"#!/usr/bin/awk -f":            "Awk",
		"#!/opt/no-such-interpreter":   "",
		"#!/usr/bin/env":               "",
		"#!":                           "",
		"# not one":                    "",
	} {
		if _, got := highlightLines("noextension", []string{line, "x"}); got != want {
			t.Errorf("%q: %q, want %q", line, got, want)
		}
	}
}

// fakeLexer tokenises as told, to fail as Chroma's own would.
type fakeLexer struct {
	chroma.Lexer
	tokens func() chroma.Token
}

func (l fakeLexer) Tokenise(*chroma.TokeniseOptions, string) (chroma.Iterator, error) {
	return l.tokens, nil
}

func TestHighlightFailureLeavesPlain(t *testing.T) {
	t.Parallel()
	for what, tokens := range map[string]func() chroma.Token{
		"a panic while reading": func() chroma.Token { panic("over the time limit") },
		"other text than given": chroma.Literator(chroma.Token{Type: chroma.Keyword, Value: "other"}),
	} {
		lexer := fakeLexer{Lexer: lexers.Fallback, tokens: tokens}
		if colors, _ := highlightWith(lexer, []string{"text"}); colors != nil {
			t.Errorf("%s: %+v", what, colors)
		}
	}
}

// highlightedModel is a model with a file's tab open and read, with colour as on,
// and the command that works out its colours not yet run.
func highlightedModel(t *testing.T, on bool, source string) (*Model, *diffView, func()) {
	t.Helper()
	m := fileModel(t)
	m.st = style.New(on)
	keys(m, "enter")
	_, cmd := m.Update(diffLoaded{projectID: "proj-demo", tab: aFile, content: sourceOf(source)})
	return m, m.shownDiff(), func() { run(m, cmd) }
}

func TestHighlightKeepsTheText(t *testing.T) {
	t.Parallel()
	source := "package a\n\tfunc é() { return \"日本語 tab\there\" } // caf\u00e9\nmid\rline\x07 \x1b[31m\n\n" + strings.Repeat("var long = \"wrap\" + ", 40) + "\n"
	m, v, colors := highlightedModel(t, true, source)
	colors()
	if v.colors == nil {
		t.Fatal("no colours")
	}
	text(m)
	var got strings.Builder
	painted := false
	for i, line := range v.lines {
		if strings.Contains(line, "\x1b[35m") {
			painted = true
		}
		plain := colorCode.ReplaceAllString(line, "")
		if v.numbers[i] != 0 && i > 0 {
			got.WriteString("\n")
		}
		got.WriteString(plain[len("1 "):]) // after the number, of one digit here
	}
	want := make([]string, 0, len(v.lineOf))
	for _, l := range v.lineOf {
		want = append(want, shownText(l))
	}
	if got.String() != strings.Join(want, "\n") {
		t.Errorf("the text changed:\n%q\nwant\n%q", got.String(), strings.Join(want, "\n"))
	}
	if !painted {
		t.Error("nothing was coloured")
	}
}

// A colour goes on across a break, and comes back on the row after a mark.
func TestHighlightWrapsAndSearches(t *testing.T) {
	t.Parallel()
	source := `var s = "` + strings.Repeat("x", 300) + `needle` + strings.Repeat("y", 300) + `"`
	m, v, colors := highlightedModel(t, true, source)
	colors()
	text(m)
	if len(v.lines) < 3 {
		t.Fatalf("not wrapped: %d lines", len(v.lines))
	}
	for i, line := range v.lines {
		if !strings.Contains(line, "\x1b[32m") {
			t.Errorf("row %d lost the string's colour: %q", i, line)
		}
		if i > 0 && strings.Contains(line, "\x1b[35m") {
			t.Errorf("row %d has the colour of the line's start: %q", i, line)
		}
	}
	keys(m, "alt+right", "/", "n", "e", "e", "d", "l", "e", "enter")
	text(m)
	if v.text[0] != shownText(source) {
		t.Errorf("search reads other than the plain text: %q", v.text[0])
	}
	match := style.New(true).Match("needle")
	for _, line := range v.lines {
		if at := strings.Index(line, match); at >= 0 {
			if !strings.Contains(line[at+len(match):], "\x1b[32my") {
				t.Errorf("no colour after the mark: %q", line)
			}
			return
		}
	}
	t.Errorf("no mark in:\n%q", v.lines)
}

// After a mark on a wrapped row, the colours are those of where the text is on its
// line, not on its row.
func TestHighlightAfterAMarkOnAWrappedRow(t *testing.T) {
	t.Parallel()
	source := `var s = "` + strings.Repeat("x", 200) + `" + needle + 42`
	m, v, colors := highlightedModel(t, true, source)
	colors()
	keys(m, "alt+right", "/", "n", "e", "e", "d", "l", "e", "enter")
	text(m)
	match := style.New(true).Match("needle")
	for i, line := range v.lines {
		if at := strings.Index(line, match); at >= 0 {
			if i == 0 || !strings.Contains(line[at:], "\x1b[33m42") {
				t.Errorf("row %d: %q", i, line)
			}
			return
		}
	}
	t.Errorf("no mark in:\n%q", v.lines)
}

func TestHighlightComesAfterTheText(t *testing.T) {
	t.Parallel()
	m, v, colors := highlightedModel(t, true, "package a\n")
	wantText(t, m, "1 package a")
	if v.colors != nil {
		t.Fatal("colours before they were worked out")
	}
	line := v.lines[0]
	colors()
	text(m)
	if v.colors == nil || v.lines[0] == line {
		t.Errorf("not drawn again in colour: %q", v.lines[0])
	}
}

func TestHighlightOff(t *testing.T) {
	t.Parallel()
	m, v, colors := highlightedModel(t, false, "package a\n")
	colors()
	text(m)
	if v.colors != nil || v.reading != 1 {
		t.Errorf("worked out with colour off: %v", v.colors)
	}
	if strings.Contains(strings.Join(v.lines, ""), "\x1b") {
		t.Errorf("escapes with colour off: %q", v.lines)
	}
}

func TestHighlightFollowsTheFile(t *testing.T) {
	t.Parallel()
	m, v, first := highlightedModel(t, true, "package a\n")

	// What was asked for the text before does not colour the text now.
	_, second := m.Update(diffLoaded{projectID: "proj-demo", tab: aFile, content: sourceOf("// now a comment\n")})
	first()
	if v.colors != nil {
		t.Fatalf("the old text's colours: %+v", v.colors)
	}
	run(m, second)
	if v.colors == nil || v.colors[0][0].kind != tokenComment {
		t.Fatalf("the new text's: %+v", v.colors)
	}

	// Read again unchanged, it keeps them.
	_, cmd := m.Update(diffLoaded{projectID: "proj-demo", tab: aFile, content: sourceOf("// now a comment\n")})
	if v.colors == nil {
		t.Error("the same text lost its colours")
	}
	run(m, cmd)
	if v.reading != 2 {
		t.Errorf("worked out again: reading %d", v.reading)
	}

	// Gone, then back, it gets them again.
	m.Update(diffLoaded{projectID: "proj-demo", tab: aFile, content: sourceOf("")})
	if v.colors != nil {
		t.Error("colours with no text")
	}
	_, cmd = m.Update(diffLoaded{projectID: "proj-demo", tab: aFile, content: sourceOf("// now a comment\n")})
	run(m, cmd)
	if v.colors == nil {
		t.Error("no colours when it came back")
	}
}

func TestHighlightLive(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	dir := filepath.Join(h.newProject(t, "app", api.ProjectStateOpen), "app")
	gittest.Git(t, dir, "branch", "-M", "main")
	writeFile(t, filepath.Join(dir, "tool"), "#!/usr/bin/env python3\ndef go():\n    pass\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-qm", "base")

	p := h.follow(t)
	p.until(t, "the projects listed", func(m *Model) bool { return m.projects.loaded })
	p.keys("enter")
	p.until(t, "the file listed", func(m *Model) bool { return strings.Contains(text(m), "tool") })
	m := p.m
	m.st = style.New(true)
	m.layout.setFocus(project)
	m.moveToCode(slices.IndexFunc(m.codeRowsShown(), func(r codeRow) bool { return r.key() == "tool" }))
	p.keys("enter")
	p.until(t, "the file's colours", func(m *Model) bool {
		v := m.shownDiff()
		if v == nil || v.colors == nil {
			return false
		}
		text(m)
		return strings.Contains(strings.Join(v.lines, "\n"), style.New(true).CodeKeyword("def"))
	})
}
