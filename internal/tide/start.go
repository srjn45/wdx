package tide

import (
	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
)

// startKey is the menu's key for starting what ended; s and r are the settings
// and reloading.
const startKey = "g"

// startTarget is an agent or a terminal whose process ended, which C-q g would start.
type startTarget struct {
	kind, id, name string
}

// startable returns what C-q g would start: what is in focus, as for C-q x, when
// it has ended. Stopped is not offered: only a closed project's are, and opening
// it starts them.
func (m *Model) startable() (startTarget, bool) {
	if m.layout.focus == terminal {
		t, ok := m.selectedTerminal()
		if !ok || !terminalEnded(t.State) {
			return startTarget{}, false
		}
		return startTarget{kind: "terminal", id: t.Id, name: t.Name}, true
	}
	a, ok := m.focusedAgent()
	if !ok || !agentEnded(a.State) {
		return startTarget{}, false
	}
	return startTarget{kind: "agent", id: a.Id, name: a.Name}, true
}

func terminalEnded(s api.TerminalState) bool {
	return s == api.TerminalStateExited || s == api.TerminalStateFailed || s == api.TerminalStateLost
}

func agentEnded(s api.AgentState) bool {
	return s == api.AgentStateExited || s == api.AgentStateFailed || s == api.AgentStateLost
}

// startedAgain is the answer to C-q g.
type startedAgain struct {
	target startTarget
	err    error
}

// startAgain asks the daemon to start s. Its state comes with the stream, which
// attaches its pane once it runs.
func (m *Model) startAgain(s startTarget) tea.Cmd {
	m.log.Info("start requested", "kind", s.kind, "name", s.name)
	return func() tea.Msg {
		var err error
		if s.kind == "agent" {
			_, err = m.client.StartAgent(m.ctx, "id:"+s.id, "", "")
		} else {
			_, err = m.client.StartTerminal(m.ctx, "id:"+s.id, "", "")
		}
		return startedAgain{target: s, err: err}
	}
}

func (m *Model) startedAgain(msg startedAgain) {
	s := msg.target
	if msg.err == nil {
		m.log.Info("started", "kind", s.kind, "name", s.name)
		return
	}
	m.log.Info("not started", "kind", s.kind, "name", s.name, "err", msg.err)
	if s.kind == "agent" {
		m.statusNotice = msg.err.Error()
	} else {
		m.terminalNotice = msg.err.Error()
	}
}
