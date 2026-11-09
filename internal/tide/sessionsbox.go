package tide

import (
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
)

// The Sessions dialog, C-q H or a click on the bar's remote:, lists the
// sessions other nodes have open into this one, as the bar's stream says, and
// cuts them off: x closes one, X every one, D removes a node from the account,
// each after asking (the wdx doc HUB_TIDE_SESSIONS). Only in a local TIDE: no
// /v1/hub route passes through a session.

const (
	sessionsKey       = "H" // of the menu
	closeSessionKey   = "x"
	closeSessionsKey  = "X"
	removeHubNodeKey  = "D"
	remoteAccessHint  = "turn remote access off in Settings: hub.remote_access"
	sessionsTimeShown = "15:04"
)

// sessionsBox is the Sessions dialog, while it is open.
type sessionsBox struct {
	row  int          // the session under the cursor, in the order they opened
	ask  *sessionsAsk // the question asked, until it is answered
	busy bool         // the daemon has not answered yet
	err  string       // why the last answer failed
}

// sessionsAsk is what the dialog asks before it acts: closing session, every
// one of all, or removing session's node.
type sessionsAsk struct {
	key     string
	session api.HubSession
	all     []api.HubSession
}

// sessionsAnswered is the daemon's answer to what the dialog asked: notice
// says what was done.
type sessionsAnswered struct {
	notice string
	err    error
}

// sessionsOffered reports whether C-q H applies: in a local TIDE, with a
// session open into this node.
func (m *Model) sessionsOffered() bool {
	return m.node == "" && len(m.incoming.sessions) > 0
}

// openSessionsBox opens the dialog.
func (m *Model) openSessionsBox(how string) {
	m.switcher, m.opening = nil, nil
	m.sessionsBox = &sessionsBox{}
	m.log.Info("sessions dialog opened", "how", how, "sessions", len(m.incoming.sessions))
}

// sessionsChanged keeps the cursor on a row, and closes the dialog once no
// session is left; its answer, still to come, goes to the status line.
func (m *Model) sessionsChanged() {
	b := m.sessionsBox
	if b == nil {
		return
	}
	n := len(m.incoming.sessions)
	if n == 0 {
		m.sessionsBox = nil
		m.log.Info("sessions dialog closed", "reason", "no session left")
		return
	}
	b.row = min(b.row, n-1)
}

func (m *Model) sessionsBoxKey(msg tea.KeyPressMsg) tea.Cmd {
	b := m.sessionsBox
	if b.busy {
		return nil
	}
	name := msg.String()
	if a := b.ask; a != nil {
		switch name {
		case "y":
			b.busy, b.err = true, ""
			return m.sessionsAct(*a)
		case "n", "esc":
			b.ask = nil
			m.log.Info("sessions dialog question declined", "key", a.key)
		}
		return nil
	}
	list := m.incoming.sessions
	switch name {
	case "esc":
		m.sessionsBox = nil
		m.log.Info("sessions dialog closed", "reason", "esc")
	case "up", "k":
		b.row = max(b.row-1, 0)
	case "down", "j":
		b.row = min(b.row+1, max(len(list)-1, 0))
	case closeSessionKey, removeHubNodeKey:
		if b.row < len(list) {
			b.ask, b.err = &sessionsAsk{key: name, session: list[b.row]}, ""
		}
	case closeSessionsKey:
		if len(list) > 0 {
			b.ask, b.err = &sessionsAsk{key: name, all: list}, ""
		}
	}
	if b.ask != nil {
		m.log.Info("sessions dialog asked", "key", name)
	}
	return nil
}

// sessionsAct does what the dialog asked, once the answer was yes.
func (m *Model) sessionsAct(a sessionsAsk) tea.Cmd {
	c := m.client
	switch a.key {
	case closeSessionKey:
		node := a.session.Node.Name
		m.log.Info("session close confirmed", "session_id", a.session.Id, "node", node)
		return func() tea.Msg {
			_, err := c.CloseHubSession(m.ctx, a.session.Id)
			return sessionsAnswered{notice: "closed the session with " + node, err: err}
		}
	case removeHubNodeKey:
		node := a.session.Node
		m.log.Info("node removal confirmed", "node_id", node.Id, "node", node.Name)
		return func() tea.Msg {
			_, err := c.RemoveHubNode(m.ctx, "id:"+node.Id)
			return sessionsAnswered{notice: "removed " + node.Name + " from the account", err: err}
		}
	}
	m.log.Info("closing every incoming session confirmed", "sessions", len(a.all))
	return func() tea.Msg {
		closed := 0
		for _, s := range a.all {
			// Only those into this node: closeHubSessions would close this
			// node's own into others too.
			_, err := c.CloseHubSession(m.ctx, s.Id)
			if p, ok := errors.AsType[*client.ProblemError](err); ok && p.Problem.Code == api.ErrorCodeSessionNotFound {
				continue // it closed meanwhile
			}
			if err != nil {
				return sessionsAnswered{notice: "closed " + sessionsCount(closed), err: err}
			}
			closed++
		}
		return sessionsAnswered{notice: "closed " + sessionsCount(closed)}
	}
}

// sessionsAnsweredMsg says on the status line what was done, or keeps in the
// dialog why it failed; the stream brings the rows that are left.
func (m *Model) sessionsAnsweredMsg(msg sessionsAnswered) {
	b := m.sessionsBox
	if msg.err != nil {
		m.log.Info("sessions dialog refused", "err", msg.err)
		if b == nil {
			m.statusNotice = msg.err.Error()
			return
		}
		b.busy, b.ask, b.err = false, nil, msg.err.Error()
		return
	}
	m.log.Info("sessions dialog answered", "notice", msg.notice)
	m.statusNotice = msg.notice
	if b != nil {
		b.busy, b.ask = false, nil
	}
}

func sessionsCount(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
}

// question is what the dialog asks before it acts.
func (a sessionsAsk) question() string {
	switch a.key {
	case closeSessionKey:
		return "close " + a.session.Node.Name + "'s session?"
	case removeHubNodeKey:
		return "remove " + a.session.Node.Name + " from your account? It can never connect again; it logs in anew to come back."
	}
	if len(a.all) == 1 {
		return "close the 1 session?"
	}
	return fmt.Sprintf("close all %d sessions?", len(a.all))
}

// sessionsHint is what the status line says while the dialog is open.
func (m *Model) sessionsHint() string {
	switch b := m.sessionsBox; {
	case m.statusNotice != "":
		return m.statusNotice
	case b.busy:
		return "sessions: waiting for the daemon"
	case b.ask != nil:
		return "confirm: y yes  n or Esc no"
	}
	return "sessions: x close  X close all  D remove node  Esc back"
}

// sessionsPopup is what the dialog shows: a row per session, then the
// question asked or the failure.
func (m *Model) sessionsPopup() popup {
	b, st := m.sessionsBox, m.st
	list := m.incoming.sessions
	width := 0
	for _, s := range list {
		width = max(width, ansi.StringWidth(s.Node.Name))
	}
	var body []string
	for i, s := range list {
		name := s.Node.Name + strings.Repeat(" ", width-ansi.StringWidth(s.Node.Name))
		times := "  opened " + s.OpenedAt.Local().Format(sessionsTimeShown) +
			"  used " + s.LastUsedAt.Local().Format(sessionsTimeShown) +
			"  idle " + s.IdleExpiresAt.Local().Format(sessionsTimeShown)
		if i == b.row {
			body = append(body, st.Focused("▸ ")+st.Name(name)+times)
		} else {
			body = append(body, "  "+name+times)
		}
	}
	footer := st.Command("x") + " close  " + st.Command("X") + " close all  " + st.Command("D") + " remove node  " + st.Command("Esc") + " back"
	if len(list) == 0 {
		body = append(body, st.Muted("no session is open into this node"))
		footer = remoteAccessHint + "  " + st.Command("Esc") + " back"
	}
	switch {
	case b.busy:
		body = append(body, "", st.Muted("asking the daemon…"))
	case b.ask != nil:
		body = append(body, "", b.ask.question()+" "+st.Command("y")+"/"+st.Command("n"))
		footer = st.Command("y") + " yes  " + st.Command("n") + " or " + st.Command("Esc") + " no"
	}
	if b.err != "" {
		body = append(body, "", st.Failure(b.err))
	}
	return popup{title: "Sessions into this node", body: body, footer: footer}
}

// barRemoteClicked reports whether x is on the bar's remote:, which a local
// TIDE shows with a session open into it.
func (m *Model) barRemoteClicked(x, y int) bool {
	r := m.remoteSessions()
	return m.node == "" && r != "" && y == m.height-1 && x >= 1 && x < 1+ansi.StringWidth(r)
}
