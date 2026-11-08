package hubapi

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/clikit"
)

func TestGeneratedCodeIsCurrent(t *testing.T) {
	t.Parallel()
	cfg, err := os.ReadFile("oapi-codegen.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// The config names the output file; point a copy of it elsewhere.
	dir := t.TempDir()
	out := filepath.Join(dir, "hubapi.gen.go")
	cfgCopy := filepath.Join(dir, "oapi-codegen.yaml")
	if err := os.WriteFile(cfgCopy, bytes.Replace(cfg, []byte("output: hubapi.gen.go"), []byte("output: "+out), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	spec, err := filepath.Abs("../../api/hub.yaml")
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
	got, err := os.ReadFile("hubapi.gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("hubapi.gen.go is stale; run make generate")
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
		if !ok || info.Description == "" || info.Status < 400 || info.Exit < clikit.ExitRejected || info.Exit > clikit.ExitSystem {
			t.Errorf("code %s: incomplete %+v", code, info)
		}
	}
	if string(ErrorCodeUsage) != clikit.CodeUsage || string(ErrorCodeInternal) != clikit.CodeInternal {
		t.Fatalf("usage and internal are %q and %q in the spec, %q and %q in clikit", ErrorCodeUsage, ErrorCodeInternal, clikit.CodeUsage, clikit.CodeInternal)
	}
}

// endpoints are the tags that say which endpoint serves an operation.
var endpoints = []string{"web", "nodes", "admin"}

func TestEveryOperationHasOneEndpoint(t *testing.T) {
	t.Parallel()
	spec, err := GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	served := map[string]int{}
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			var tags []string
			for _, tag := range op.Tags {
				if slices.Contains(endpoints, tag) {
					tags = append(tags, tag)
				}
			}
			if len(tags) != 1 {
				t.Errorf("%s %s (%s) has the endpoint tags %v, want exactly one of %v", method, path, op.OperationID, tags, endpoints)
				continue
			}
			served[tags[0]]++
		}
	}
	for _, e := range endpoints {
		if served[e] == 0 {
			t.Errorf("no operation is served on the %s endpoint", e)
		}
	}
}
