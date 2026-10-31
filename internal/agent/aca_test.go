package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/waittest"
)

// TestCreateTellsTheHolderItsACA: an agent's holder is told what to read its
// screen as.
func TestCreateTellsTheHolderItsACA(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "")
	f.create(t, "helper")
	waittest.For(t, "the holder to watch its screen", func() bool {
		logs, _ := filepath.Glob(filepath.Join(identity.LogsDir(f.home), "*.log"))
		for _, name := range logs {
			if b, err := os.ReadFile(name); err == nil && strings.Contains(string(b), "screen watch started") {
				return true
			}
		}
		return false
	})
}
