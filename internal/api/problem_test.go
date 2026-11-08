package api

import (
	"encoding/json"
	"testing"

	"github.com/srjn45/wdx/internal/clikit"
)

// TestProblemShape checks the CLI's problems are the API's: the same keys in
// the same order, and the codes the framework reports are the spec's.
func TestProblemShape(t *testing.T) {
	t.Parallel()
	hint := "h"
	details := map[string]any{"k": "v"}
	want, err := json.Marshal(Problem{Code: ErrorCodeNotFound, DebugId: "dbg-1", Detail: "d", Details: &details, Hint: &hint, Status: 404, Title: "t", Type: "urn"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(clikit.Problem{Code: string(ErrorCodeNotFound), DebugId: "dbg-1", Detail: "d", Details: &details, Hint: &hint, Status: 404, Title: "t", Type: "urn", Lines: []string{"not in the JSON"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("clikit.Problem marshals to\n%s\nwant api.Problem's\n%s", got, want)
	}
	if empty, _ := json.Marshal(clikit.Problem{}); string(empty) != mustMarshal(t, Problem{}) {
		t.Fatalf("an empty clikit.Problem marshals to %s, want %s", empty, mustMarshal(t, Problem{}))
	}
	if string(ErrorCodeUsage) != clikit.CodeUsage || string(ErrorCodeInternal) != clikit.CodeInternal {
		t.Fatalf("usage and internal are %q and %q in the spec, %q and %q in clikit", ErrorCodeUsage, ErrorCodeInternal, clikit.CodeUsage, clikit.CodeInternal)
	}
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
