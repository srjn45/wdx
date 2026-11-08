package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

var (
	// componentTag is a component used in MDX: a tag whose name starts with a
	// capital, as <Since v="0.5.0" />.
	componentTag = regexp.MustCompile(`<([A-Z][A-Za-z0-9]*)`)
	// componentImport is the line that brings one in.
	componentImport = regexp.MustCompile(`(?m)^import ([A-Z][A-Za-z0-9]*) from '([^']+)';`)
	// mdxCode is what the scan passes over: fenced blocks and inline code,
	// where a tag is text.
	mdxCode = regexp.MustCompile("(?s)```.*?```|`[^`\n]*`")
)

// TestComponentsImported fails when a docs page in MDX uses a component it
// does not import, or imports one from a file that is not there. Only the
// site's build catches either, and it runs on push, after the commit.
func TestComponentsImported(t *testing.T) {
	t.Parallel()
	const root = "../.."
	err := filepath.WalkDir(filepath.Join(root, docsDir), func(path string, d fs.DirEntry, err error) error {
		if err != nil || filepath.Ext(path) != ".mdx" {
			return err
		}
		name, _ := filepath.Rel(filepath.Join(root, docsDir), path)
		_, body, err := guideCommands(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		imported := map[string]bool{}
		for _, m := range componentImport.FindAllSubmatch(body, -1) {
			imported[string(m[1])] = true
			from := filepath.Join(filepath.Dir(path), string(m[2]))
			if _, err := os.Stat(from); err != nil {
				t.Errorf("%s imports %s from %s, which is not there", name, m[1], m[2])
			}
		}
		for _, m := range componentTag.FindAllSubmatch(mdxCode.ReplaceAll(body, nil), -1) {
			if c := string(m[1]); !imported[c] {
				t.Errorf("%s uses <%s> without importing it: add import %s from '<path>/%s.astro'; under the frontmatter", name, c, c, c)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestComponentTagSkipsCode checks the scan passes over code, where a tag is
// text: a page may show <Since /> in a block without importing it.
func TestComponentTagSkipsCode(t *testing.T) {
	t.Parallel()
	body := []byte("text `<Inline />` more\n```mdx\n<Fenced v=\"1\" />\n```\n<Used />\n")
	var found []string
	for _, m := range componentTag.FindAllSubmatch(mdxCode.ReplaceAll(body, nil), -1) {
		found = append(found, string(m[1]))
	}
	if len(found) != 1 || found[0] != "Used" {
		t.Fatalf("found %q, want only Used", found)
	}
}
