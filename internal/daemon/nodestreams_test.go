package daemon

import (
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

// TestRemoteOperationsClassifyEvery checks every operation of the spec is
// served through a session or refused with a reason, and nothing else is
// classified: a new operation must be decided on.
func TestRemoteOperationsClassifyEvery(t *testing.T) {
	t.Parallel()
	spec, err := api.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			id := op.OperationID
			seen[id] = true
			rule, ok := remoteOperations[id]
			switch {
			case !ok:
				t.Errorf("%s (%s %s) is not in remoteOperations: decide whether a session may reach it", id, method, path)
			case !rule.allowed && rule.reason == "":
				t.Errorf("%s is refused with no reason", id)
			case rule.allowed && (strings.HasPrefix(path, hubPath) || strings.HasPrefix(path, settingsPath) || path == "/v1/settings" || path == reloadPath || path == tideViewPath || strings.HasSuffix(path, "/view") || strings.HasSuffix(path, "/tabs")):
				t.Errorf("%s (%s %s) is allowed, which no session may reach", id, method, path)
			}
		}
	}
	for id := range remoteOperations {
		if !seen[id] {
			t.Errorf("remoteOperations has %s, which the spec does not", id)
		}
	}
}
