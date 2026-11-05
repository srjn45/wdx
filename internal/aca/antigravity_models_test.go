package aca

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseAntigravityModels reads what agy 1.5.0 printed, saved in testdata, as the
// tests never run agy.
func TestParseAntigravityModels(t *testing.T) {
	t.Parallel()
	out, err := os.ReadFile(filepath.Join("testdata", "antigravity", "models.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// A login shell's startup files can print before it.
	models, err := parseAntigravityModels("Welcome back\n" + string(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 13 {
		t.Fatalf("%d models, want 13: %v", len(models), models)
	}
	if first := models[0]; first != (Model{ID: "gemini-4-argon-medium", Name: "Gemini 4 Argon (Medium)"}) {
		t.Errorf("first = %+v", first)
	}
	if last := models[12]; last != (Model{ID: "gpt-oss-120b-medium", Name: "GPT-OSS 120B (Medium)"}) {
		t.Errorf("last = %+v", last)
	}
	for _, m := range models {
		if m.Default {
			t.Errorf("%s is the default, which the list does not say", m.ID)
		}
	}
}

func TestParseAntigravityModelsFindsNone(t *testing.T) {
	t.Parallel()
	for _, out := range []string{"", "Fetching available models...\n", "no tab here\n", "has space\tName\n"} {
		if models, err := parseAntigravityModels(out); err == nil {
			t.Errorf("%q: models %v, want an error", out, models)
		}
	}
}
