package api

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGeneratedCodeIsCurrent(t *testing.T) {
	t.Parallel()
	cfg, err := os.ReadFile("oapi-codegen.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// The config names the output file; point a copy of it elsewhere.
	dir := t.TempDir()
	out := filepath.Join(dir, "api.gen.go")
	cfgCopy := filepath.Join(dir, "oapi-codegen.yaml")
	if err := os.WriteFile(cfgCopy, bytes.Replace(cfg, []byte("output: api.gen.go"), []byte("output: "+out), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	spec, err := filepath.Abs("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "tool", "oapi-codegen", "-config", cfgCopy, spec)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("oapi-codegen: %v\n%s", err, b)
	}
	want, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("api.gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("api.gen.go is stale; run make generate")
	}
}

func TestEveryCodeDescribed(t *testing.T) {
	t.Parallel()
	spec, err := GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	enum := spec.Components.Schemas["ErrorCode"].Value.Enum
	if len(enum) != len(Codes) {
		t.Fatalf("spec has %d codes, the table %d", len(enum), len(Codes))
	}
	for _, code := range enum {
		info, ok := Codes[ErrorCode(code.(string))]
		if !ok || info.Description == "" || info.Status < 400 || info.Exit < ExitRejected || info.Exit > ExitSystem {
			t.Errorf("code %s: incomplete %+v", code, info)
		}
	}
}
