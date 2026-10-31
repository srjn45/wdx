package terminal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/waittest"
)

// TestAgentLaunchNamesItsACA: the holder of an agent's row is told its ACA and
// watches the screen; a shell's is told none and does not.
func TestAgentLaunchNamesItsACA(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	f.opts.Shell = runtest.LoginShell(t)
	f.opts.AgentLaunch = func(context.Context, Terminal) (AgentLaunch, error) {
		return AgentLaunch{ACA: aca.ClaudeCode{}.ID(), Command: []string{"sleep", "60"}}, nil
	}
	s := f.supervisor(t)
	p := f.project(t, "app")
	agent, err := s.CreateAgent(ctx, p, "helper", t.TempDir(), noLink)
	if err != nil {
		t.Fatal(err)
	}
	shell, err := s.Create(ctx, p, "shell", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Removing it waits for its shell's end, which a watch would have logged before.
	if _, err := s.Remove(ctx, shell.ID, true); err != nil {
		t.Fatal(err)
	}
	watches := func() (agents, shells int) {
		logs, _ := filepath.Glob(filepath.Join(identity.LogsDir(f.home), "*.log"))
		for _, name := range logs {
			b, _ := os.ReadFile(name)
			for line := range strings.Lines(string(b)) {
				if !strings.Contains(line, "screen watch started") {
					continue
				}
				if strings.Contains(line, agent.ID) {
					agents++
				}
				if strings.Contains(line, shell.ID) {
					shells++
				}
			}
		}
		return agents, shells
	}
	waittest.For(t, "the agent's holder to watch its screen", func() bool {
		agents, _ := watches()
		return agents == 1
	})
	if _, shells := watches(); shells != 0 {
		t.Errorf("a shell's holder watches its screen")
	}
}
