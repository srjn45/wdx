package main

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"
)

// guidesDir holds the guides, relative to the repository's root.
const guidesDir = "site/src/content/docs/docs/guides"

// waiting are the commands no guide covers yet: the guides still to be
// written take them off, and the test fails until they do.
var waiting = []string{
	"doc create",
	"doc get",
	"doc history",
	"doc list",
	"doc remove",
	"doc rename",
	"doc set",
	"doc sync-to-git",
	"doc update",
	"plan archive",
	"plan cancel",
	"plan copy",
	"plan create",
	"plan get",
	"plan history",
	"plan list",
	"plan pause",
	"plan remove",
	"plan rename",
	"plan resume",
	"plan run",
	"plan set",
	"plan status",
	"plan task done",
	"plan task get",
	"plan task land",
	"plan task retry",
	"plan task skip",
	"plan unarchive",
	"plan update",
	"tide",
}

// TestGuideCoverage fails when a command is in no guide, a guide names a
// command that is gone, a command still waiting is in a guide, or a guide
// does not show its commands. The commands are those of commandsFile, which
// TestReferenceCurrent keeps current.
func TestGuideCoverage(t *testing.T) {
	t.Parallel()
	const root = "../.."
	data, err := os.ReadFile(filepath.Join(root, commandsFile))
	if err != nil {
		t.Fatal(err)
	}
	var commands map[string]json.RawMessage
	if err := json.Unmarshal(data, &commands); err != nil {
		t.Fatal(err)
	}
	guides, err := filepath.Glob(filepath.Join(root, guidesDir, "*.mdx"))
	if err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{}
	for _, path := range guides {
		name := filepath.Base(path)
		listed, body, err := guideCommands(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, c := range listed {
			if _, ok := commands[c]; !ok {
				t.Errorf("%s lists %q, which is not a wdx command: commands are named without wdx, as agent create", name, c)
			}
			if slices.Contains(waiting, c) {
				t.Errorf("%s covers %q: take it off waiting in coverage_test.go", name, c)
			}
			covered[c] = true
		}
		if len(listed) > 0 && !bytes.Contains(body, []byte("<Commands />")) {
			t.Errorf("%s lists commands but does not show them: add <Commands /> under its ## Commands", name)
		}
	}
	for _, c := range slices.Sorted(maps.Keys(commands)) {
		if !covered[c] && !slices.Contains(waiting, c) {
			t.Errorf("wdx %s is in no guide: add it to the commands: of the guide about it", c)
		}
	}
	for _, c := range waiting {
		if _, ok := commands[c]; !ok {
			t.Errorf("waiting lists %q, which is not a wdx command", c)
		}
	}
}

// guideCommands reads the commands: of a guide's frontmatter, and the rest.
func guideCommands(path string) ([]string, []byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	rest, ok := bytes.CutPrefix(b, []byte("---\n"))
	front, body, found := bytes.Cut(rest, []byte("\n---\n"))
	if !ok || !found {
		return nil, nil, os.ErrInvalid
	}
	var fm struct {
		Commands []string `yaml:"commands"`
	}
	if err := yaml.Unmarshal(front, &fm); err != nil {
		return nil, nil, err
	}
	return fm.Commands, body, nil
}
