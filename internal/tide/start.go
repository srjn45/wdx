package tide

import (
	"fmt"
	"strings"

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

// restartKey is the menu's key for a new session of the agent in focus; r is taken by
// reloading.
const restartKey = "n"

// restartKind is the kind of a removal that restarts an agent instead: it asks just the
// same, as what the agent was doing is lost.
const restartKind = "restart"

// restartable returns what C-q n would restart: the agent in focus. Stopped is not
// offered: only a closed project's are, which has to be opened first.
func (m *Model) restartable() (removal, bool) {
	if m.layout.focus == terminal {
		return removal{}, false
	}
	a, ok := m.focusedAgent()
	if !ok || a.State == api.AgentStateRemoved || a.State == api.AgentStateStopped {
		return removal{}, false
	}
	r := removal{kind: restartKind, id: a.Id, name: a.Name, running: a.State == api.AgentStateRunning || a.State == api.AgentStateStarting}
	if a.Draft != nil && *a.Draft {
		r.doing = "text in its input box"
	} else if a.Activity != nil && *a.Activity != api.AgentActivityIdle {
		r.doing = string(*a.Activity)
	}
	return r, true
}

// restarted is the answer to a restart.
type restarted struct {
	r       removal
	stopped []string
	err     error
}

// restart asks the daemon for a new session, with force as the person saw what it is
// doing in the confirmation. Its state comes with the stream.
func (m *Model) restart(r removal) tea.Cmd {
	m.log.Info("restart confirmed", "name", r.name)
	return func() tea.Msg {
		result, err := m.client.RestartAgent(m.ctx, "id:"+r.id, "", "", true)
		var stopped []string
		if result.StoppedSessions != nil {
			stopped = *result.StoppedSessions
		}
		return restarted{r: r, stopped: stopped, err: err}
	}
}

func (m *Model) restarted(msg restarted) {
	m.confirm = nil
	if msg.err != nil {
		m.log.Info("not restarted", "name", msg.r.name, "err", msg.err)
		m.statusNotice = msg.err.Error()
		return
	}
	m.log.Info("restarted", "name", msg.r.name, "stopped", len(msg.stopped))
	if len(msg.stopped) > 0 {
		m.statusNotice = fmt.Sprintf("stopped its background session: %s", strings.Join(msg.stopped, ", "))
	}
}

// restartPopup is the confirmation of a restart.
func (m *Model) restartPopup() popup {
	c, st := m.confirm, m.st
	body := []string{"Start " + st.Name(c.r.name) + " again in a new session?", "It reads its history in brief, and what the old session held in memory is lost."}
	if c.r.doing != "" {
		body = append(body, "It is in the middle of something ("+c.r.doing+"): that may be left half done.")
	}
	if c.busy {
		body = append(body, "", st.Muted("restarting…"))
	}
	return popup{
		title:  "Restart agent",
		body:   body,
		footer: st.Command("y") + " restart  " + st.Command("n") + " or " + st.Command("Esc") + " keep",
	}
}
