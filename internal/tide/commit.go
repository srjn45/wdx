package tide

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
)

// C on a worktree's uncommitted view asks an agent working there to commit. wdx
// commits nothing itself: the agent writes the message and follows the repository's
// rules (docs/TIDE_WORKTREES.md, "A worktree's tab").

const (
	commitKey    = "C"
	commitPrompt = "Commit all uncommitted changes"
)

// commitBox asks what to send, and to which agent.
type commitBox struct {
	review   int // how many comments it sends, with text as their note; 0 asks to commit
	worktree string
	agents   []api.Agent
	chosen   int
	text     string
	sending  bool
	err      string // why the last send failed
}

// commitSent is the answer to sending.
type commitSent struct {
	agent string
	err   error
}

// flipWorktreeView turns a worktree's tab to its other view, while it has two.
func (m *Model) flipWorktreeView(tab string) {
	worktree, file := diffTabOf(tab)
	if file != "" || !m.hasUncommitted(worktree) {
		return
	}
	l := m.list()
	if l.showCommits == nil {
		l.showCommits = map[string]bool{}
	}
	if l.showCommits[worktree] {
		delete(l.showCommits, worktree)
	} else {
		l.showCommits[worktree] = true
	}
}

// runningAgentsIn returns the running agents in the worktree called name.
func (m *Model) runningAgentsIn(name string) []api.Agent {
	var agents []api.Agent
	for _, a := range m.agents() {
		if a.State == api.AgentStateRunning && m.worktreeHolding(a.Dir) == name {
			agents = append(agents, a)
		}
	}
	return agents
}

// askCommit opens the box, or says why it cannot: only on a view of what is not
// committed, as the bar offers it.
func (m *Model) askCommit(tab string, v *diffView) {
	if !v.uncommitted || !strings.HasPrefix(m.diffViewOf(tab), uncommittedPrefix) {
		return
	}
	worktree, _ := diffTabOf(tab)
	agents := m.runningAgentsIn(worktree)
	if len(agents) == 0 {
		v.notice = "no agent is running in " + worktree
		return
	}
	m.commit = &commitBox{worktree: worktree, agents: agents, text: commitPrompt}
	m.log.Info("commit asked", "worktree", worktree)
}

// commitKey takes a key while the box is open.
func (m *Model) commitKey(msg tea.KeyPressMsg) tea.Cmd {
	c := m.commit
	if c.sending {
		return nil
	}
	switch name := msg.String(); {
	case name == "esc":
		m.commit = nil
	case name == "enter":
		text := strings.TrimSpace(c.text)
		if text == "" && c.review == 0 {
			return nil
		}
		c.sending, c.err = true, ""
		a := c.agents[c.chosen]
		if c.review > 0 {
			ref := "id:" + m.following.project.Id
			m.log.Info("review sent", "worktree", c.worktree, "agent_id", a.Id)
			return func() tea.Msg {
				_, err := m.client.SendWorktreeReview(m.ctx, c.worktree, ref, "id:"+a.Id, text)
				return commitSent{agent: a.Name, err: err}
			}
		}
		m.log.Info("commit sent", "worktree", c.worktree, "agent_id", a.Id)
		return func() tea.Msg {
			_, err := m.client.SendAgent(m.ctx, "id:"+a.Id, "", "", text, false)
			return commitSent{agent: a.Name, err: err}
		}
	case name == "left" && len(c.agents) > 1:
		c.chosen = (c.chosen - 1 + len(c.agents)) % len(c.agents)
	case name == "right" && len(c.agents) > 1:
		c.chosen = (c.chosen + 1) % len(c.agents)
	case name == "backspace" && c.text != "":
		r := []rune(c.text)
		c.text = string(r[:len(r)-1])
	case typed(msg):
		c.text += msg.Text
	}
	return nil
}

// commitSent closes the box, or leaves it open with the text and why it failed.
func (m *Model) commitSent(msg commitSent) {
	c := m.commit
	if c == nil {
		return
	}
	c.sending = false
	if msg.err != nil {
		m.log.Info("commit not sent", "agent", msg.agent, "err", msg.err)
		c.err = msg.err.Error()
		return
	}
	m.commit = nil
	m.statusNotice = "sent to " + msg.agent
}

// commitPopup is what the box shows.
func (m *Model) commitPopup() popup {
	c, st := m.commit, m.st
	a := c.agents[c.chosen]
	to := st.Name(a.Name)
	if len(c.agents) > 1 {
		to = st.Muted("← ") + to + st.Muted(" →")
	}
	title := "Ask to commit"
	body := []string{"To " + to + ", in " + c.worktree + ":", "", "> " + c.text + "_"}
	if c.review > 0 {
		title = "Send review"
		body = []string{countOf(c.review) + " to " + to + ", in " + c.worktree + ".", "A note to go first, if any:", "", "> " + c.text + "_"}
	} else if n := len(m.draftComments(c.worktree)); n > 0 {
		body = append(body, "", st.Warning(countOf(n)+" not sent: s sends them, and a commit now leaves them out"))
	}
	if a.Activity != nil && *a.Activity == api.AgentActivityWorking {
		body = append(body, "", st.Warning(a.Name+" is working: a commit now may take in unfinished work"))
	}
	if c.sending {
		body = append(body, "", st.Muted("sending…"))
	}
	if c.err != "" {
		body = append(body, "", st.Failure(c.err))
	}
	return popup{
		title:  title,
		body:   body,
		footer: st.Command("Enter") + " send  " + st.Command("Esc") + " cancel",
	}
}
