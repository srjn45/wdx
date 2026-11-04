package tide

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

// agentNamed waits for the agent named name in the project shown, and for the box
// to close with the answer, which the stream can come before; and returns it.
func agentNamed(t *testing.T, p *program, name string) api.Agent {
	t.Helper()
	var found api.Agent
	p.until(t, name+" created", func(m *Model) bool {
		i := slices.IndexFunc(m.agents(), func(a api.Agent) bool { return a.Name == name })
		if i >= 0 {
			found = m.agents()[i]
		}
		return i >= 0 && m.agentForm == nil
	})
	return found
}

// openAgentForm opens the new agent's box and waits for its models.
func openAgentForm(t *testing.T, p *program) {
	t.Helper()
	p.keys("C-q", "a")
	p.until(t, "the models", func(m *Model) bool { return m.agentForm != nil && m.agentForm.modelsRead })
}

// TestNewAgentForm: each field reaches the agent created; a model typed that the
// list does not have goes through as typed; a name taken keeps the box, its cursor
// on the name.
func TestNewAgentForm(t *testing.T) {
	t.Parallel()
	h := newTestHome(t)
	h.serve(t)
	h.newProject(t, "api", api.ProjectStateOpen)
	p := h.openShown(t)
	p.until(t, "no agents", func(m *Model) bool { return strings.Contains(text(m), "no agents: C-q a") })

	openAgentForm(t, p)
	wantText(t, p.m, "fable", "opus", "sonnet", "haiku")
	p.keys("h", "e", "l", "p", "e", "r", "\t", "s", "o", "n")
	wantText(t, p.m, "▸ sonnet")
	p.keys("\t", "\t", "left", "\t", "h", "i", "enter")
	a := agentNamed(t, p, "helper")
	if a.RequestedModel == nil || *a.RequestedModel != "sonnet" || a.Permissions != api.AgentPermissionsAsk {
		t.Fatalf("agent %+v", a)
	}

	openAgentForm(t, p)
	p.keys("helper", "enter")
	p.until(t, "the failure in the box", func(m *Model) bool {
		f := m.agentForm
		return f != nil && !f.busy && f.err != "" && f.field == formName
	})
	p.keys("esc")

	openAgentForm(t, p)
	p.keys("own", "\t")
	for _, r := range "claude-opus-4-8" {
		p.keys(string(r))
	}
	wantText(t, p.m, "none matches: what is typed is used")
	p.keys("enter")
	a = agentNamed(t, p, "own")
	if a.RequestedModel == nil || *a.RequestedModel != "claude-opus-4-8" || a.Permissions != api.AgentPermissionsFull {
		t.Fatalf("agent %+v", a)
	}

	// With nothing typed, Default is the last row, and passes no model.
	openAgentForm(t, p)
	p.keys("def", "\t", "up", "down", "down", "down", "down", "enter")
	if a = agentNamed(t, p, "def"); a.RequestedModel != nil {
		t.Fatalf("default: %v", *a.RequestedModel)
	}
}

// TestModelListScrolls: the list is always four rows high, and ↑↓ scroll it.
func TestModelListScrolls(t *testing.T) {
	t.Parallel()
	m := newAgentsModel(100, 41)
	f := &agentForm{modelsRead: true, field: formModel}
	for i := range 10 {
		f.models = append(f.models, api.AcaModel{Id: fmt.Sprintf("m-%d", i), Name: fmt.Sprintf("Model %d", i)})
	}
	m.agentForm = f
	shown := func() []string {
		t.Helper()
		rows := m.modelRows()
		if len(rows) != modelRows {
			t.Fatalf("%d rows: %q", len(rows), rows)
		}
		var ids []string
		for _, r := range rows {
			if words := strings.Fields(strings.TrimPrefix(strings.TrimSpace(r), "▸ ")); len(words) > 0 {
				ids = append(ids, words[0])
			}
		}
		return ids
	}
	if got := shown(); !slices.Equal(got, []string{"m-0", "m-1", "m-2", "m-3"}) {
		t.Fatalf("first rows %q", got)
	}
	for range 5 {
		f.moveSelection(1)
	}
	if got := shown(); f.chosenModel() != "m-5" || !slices.Equal(got, []string{"m-2", "m-3", "m-4", "m-5"}) {
		t.Fatalf("after five down: chose %q, rows %q", f.chosenModel(), got)
	}
	for range 20 {
		f.moveSelection(1)
	}
	if got := shown(); f.chosenModel() != "" || !slices.Equal(got, []string{"m-7", "m-8", "m-9", "Default"}) {
		t.Fatalf("at the end: chose %q, rows %q", f.chosenModel(), got)
	}
	keys(m, "1")
	if got := shown(); f.chosenModel() != "m-1" || got[0] != "m-1" {
		t.Fatalf("typed 1: chose %q, rows %q", f.chosenModel(), got)
	}
	if rows := m.modelRows(); strings.TrimSpace(rows[1]) != "" {
		t.Fatalf("one match takes one row, the rest kept empty: %q", rows)
	}
}
