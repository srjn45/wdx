package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A setting two sections deep is written under both headings, and read back.
func TestSetNested(t *testing.T) {
	t.Parallel()
	home, _ := homeWith(t, "color: never\n")
	if _, err := Set(home, "aca.claude_code.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "\naca:\n  claude_code:\n") || !strings.Contains(string(content), "\n    enabled: false\n") {
		t.Errorf("written:\n%s", content)
	}
	c, err := LoadHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if c.ACAEnabled["claude_code"] || c.Value("color") != "never" {
		t.Fatalf("read back: enabled %v, color %s", c.ACAEnabled, c.Value("color"))
	}
	if r, err := Set(home, "aca.claude_code.enabled", "false"); err != nil || r.Changed {
		t.Fatalf("want nothing changed the second time, got %+v: %v", r, err)
	}
}
