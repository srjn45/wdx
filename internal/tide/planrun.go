package tide

import (
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
)

// cancelPlanKind is the kind of a confirmation that cancels a plan: it stops its
// agents, and the plan cannot go on.
const cancelPlanKind = "plan cancel"

// planAnswered is the answer to pausing, resuming or cancelling a plan, or to
// retrying, landing or skipping one of its tasks. Their states come with the
// stream.
type planAnswered struct {
	what, planID, task string
	notice             string // what the status line says once done, if anything
	err                error
}

func (m *Model) planAnsweredMsg(msg planAnswered) {
	if m.confirm != nil && m.confirm.r.kind == cancelPlanKind {
		m.confirm = nil
	}
	m.taskBox = nil
	if msg.err != nil {
		m.log.Info("plan not answered", "what", msg.what, "plan_id", msg.planID, "task_id", msg.task, "err", msg.err)
		m.statusNotice = msg.err.Error()
		return
	}
	m.log.Info("plan answered", "what", msg.what, "plan_id", msg.planID, "task_id", msg.task)
	if msg.notice != "" {
		m.statusNotice = msg.notice
	}
}

// pausePlan pauses p, or resumes it when pause is false.
func (m *Model) pausePlan(p api.Plan, pause bool) tea.Cmd {
	what := "resume"
	if pause {
		what = "pause"
	}
	m.log.Info("plan answer asked", "what", what, "plan_id", p.Id, "name", p.Name)
	return func() tea.Msg {
		var err error
		if pause {
			_, err = m.client.PausePlan(m.ctx, "id:"+p.Id, "", "")
		} else {
			_, err = m.client.ResumePlan(m.ctx, "id:"+p.Id, "", "")
		}
		return planAnswered{what: what, planID: p.Id, err: err}
	}
}

// cancelPlan cancels the plan r names, once confirmed.
func (m *Model) cancelPlan(r removal) tea.Cmd {
	m.log.Info("plan cancel confirmed", "plan_id", r.id, "name", r.name)
	return func() tea.Msg {
		_, err := m.client.CancelPlan(m.ctx, "id:"+r.id, "", "")
		return planAnswered{what: "cancel", planID: r.id, err: err}
	}
}

// cancelPlanPopup is the confirmation of cancelling a plan.
func (m *Model) cancelPlanPopup() popup {
	c, st := m.confirm, m.st
	body := []string{
		"Cancel " + st.Name(c.r.name) + "?",
		"Its running agents stop, and it cannot run again. Its worktrees",
		"holding work stay as ordinary worktrees, and so does plan/" + c.r.name + ".",
	}
	if c.busy {
		body = append(body, "", st.Muted("cancelling…"))
	}
	return popup{
		title:  "Cancel plan",
		body:   body,
		footer: st.Command("y") + " cancel it  " + st.Command("n") + " or " + st.Command("Esc") + " keep it",
	}
}

// taskAnswers is the box of the answers to a task that cannot go on: Enter on a
// failed or land_failed task in its plan's tab opens it.
type taskAnswers struct {
	plan api.Plan
	task api.PlanTask
	busy bool // an answer was sent
}

// askTask is what opens the answers' box of task t of plan p.
func (m *Model) askTask(p api.Plan, t api.PlanTask) func() tea.Cmd {
	return func() tea.Cmd {
		m.taskBox = &taskAnswers{plan: p, task: t}
		m.log.Info("plan task answers shown", "plan_id", p.Id, "task_id", t.Id, "state", t.State)
		return nil
	}
}

// taskBoxKey takes a key while the box is open: r retries the task, f retries it
// with a new agent, l lands it again (land_failed only), s skips it, t shows its
// check's terminal (a failed check only); Esc closes the box.
func (m *Model) taskBoxKey(name string) tea.Cmd {
	b := m.taskBox
	if b.busy && name != "esc" {
		return nil
	}
	t, p := b.task, b.plan
	answer := func(what string, send func() error) tea.Cmd {
		b.busy = true
		m.log.Info("plan task answered", "what", what, "plan_id", p.Id, "task_id", t.Id)
		return func() tea.Msg { return planAnswered{what: what, planID: p.Id, task: t.Id, err: send()} }
	}
	ref := "id:" + p.Id
	switch name {
	case "r", "f":
		fresh := name == "f"
		return answer("retry", func() error {
			_, err := m.client.RetryPlanTask(m.ctx, ref, "", "", t.Id, fresh)
			return err
		})
	case "l":
		if t.State != api.PlanTaskStateLandFailed {
			return nil
		}
		return answer("land", func() error {
			_, err := m.client.LandPlanTask(m.ctx, ref, "", "", t.Id)
			return err
		})
	case "s":
		return answer("skip", func() error {
			_, err := m.client.SkipPlanTask(m.ctx, ref, "", "", t.Id)
			return err
		})
	case "t":
		if i := slices.IndexFunc(m.terminals(), func(term api.Terminal) bool { return t.CheckTerminal != nil && term.Name == *t.CheckTerminal }); i >= 0 {
			m.selectTab(i)
			m.focusTerminalPane()
			m.taskBox = nil
			m.log.Info("plan task's check shown", "plan_id", p.Id, "task_id", t.Id, "terminal", *t.CheckTerminal)
		}
	case "esc":
		m.log.Info("plan task answers closed", "plan_id", p.Id, "task_id", t.Id)
		m.taskBox = nil
	}
	return nil
}

// taskBoxPopup is what the box shows: why the task stopped, and the keys that
// answer it.
func (m *Model) taskBoxPopup() popup {
	b, st := m.taskBox, m.st
	t := b.task
	body := []string{st.Name(t.Id) + " of " + st.Name(b.plan.Name) + ": " + taskLine(t)}
	if t.Conflicts != nil {
		body = append(body, "conflicts in "+strings.Join(*t.Conflicts, ", "))
	}
	body = append(body, "",
		st.Command("r")+" retry: its agent resumes, told what went wrong",
		st.Command("f")+" retry with a new agent, told of the earlier commits")
	if t.State == api.PlanTaskStateLandFailed {
		body = append(body, st.Command("l")+" land it again: you dealt with it in its worktree")
	}
	body = append(body, st.Command("s")+" skip it: what waits for it goes on without it")
	if t.CheckTerminal != nil {
		body = append(body, st.Command("t")+" show the check's output")
	}
	if b.busy {
		body = append(body, "", st.Muted("sending…"))
	}
	return popup{title: "Task " + t.Id, body: body, footer: st.Command("Esc") + " close"}
}

// The keys of a plan's tab for the rest of its lifecycle (docs/PLANS.md, step 4).
const (
	archiveKey = "a"
	copyKey    = "y" // as vim yanks
)

// archivePlan archives p, or unarchives it when archive is false; nothing is lost
// either way, so nothing is asked.
func (m *Model) archivePlan(p api.Plan, archive bool) tea.Cmd {
	what := "unarchive"
	if archive {
		what = "archive"
	}
	m.log.Info("plan answer asked", "what", what, "plan_id", p.Id, "name", p.Name)
	return func() tea.Msg {
		var err error
		if archive {
			_, err = m.client.ArchivePlan(m.ctx, "id:"+p.Id, "", "")
		} else {
			_, err = m.client.UnarchivePlan(m.ctx, "id:"+p.Id, "", "")
		}
		return planAnswered{what: what, planID: p.Id, notice: "plan " + p.Name + " " + what + "d", err: err}
	}
}

// copyAsked is the name typed in the popup y opens on a plan's tab.
type copyAsked struct{ id, from, name string }

// planCopied is the daemon's answer to it: the copy, whose tab then opens.
type planCopied struct {
	from string
	plan api.Plan
	err  error
}

// askCopyPlan opens the popup that copies p, with the first name free to edit. A
// plan of plans' copy would be refused, so it says so instead.
func (m *Model) askCopyPlan(p api.Plan) {
	if isParent(p) {
		m.statusNotice = "a plan of plans is copied with the CLI once step 6 of plans says how"
		return
	}
	m.switcher = nil
	m.push(&fieldScreen{
		heading: "Copy plan",
		prompt:  "Name of the copy of " + p.Name + ", a draft to run:",
		value:   m.freeCopyName(p.Name),
		empty:   "type a name",
		working: "copying…",
		submit:  func(name string) tea.Msg { return copyAsked{id: p.Id, from: p.Name, name: name} },
	})
	m.log.Info("plan copy opened", "plan_id", p.Id)
}

// freeCopyName is the first name of name-2, name-3, … no plan of the project has,
// a copy's own -<n> taken off first.
func (m *Model) freeCopyName(name string) string {
	if i := strings.LastIndex(name, "-"); i > 0 {
		if _, err := strconv.Atoi(name[i+1:]); err == nil {
			name = name[:i]
		}
	}
	for n := 2; ; n++ {
		candidate := name + "-" + strconv.Itoa(n)
		if _, taken := m.planNamed(candidate); !taken {
			return candidate
		}
	}
}

// sendCopy asks the daemon for the copy named in the popup.
func (m *Model) sendCopy(msg copyAsked) tea.Cmd {
	if m.client == nil {
		return nil
	}
	m.log.Info("plan copy asked", "plan_id", msg.id, "name", msg.name)
	return func() tea.Msg {
		r, err := m.client.CopyPlan(m.ctx, "id:"+msg.id, "", "", api.CopyPlan{Name: msg.name})
		return planCopied{from: msg.from, plan: r.Plan, err: err}
	}
}

// planCopiedMsg closes the popup and opens the copy's tab, or shows in the popup why
// it was refused, to correct it or give up.
func (m *Model) planCopiedMsg(msg planCopied) tea.Cmd {
	if msg.err != nil {
		m.log.Info("plan not copied", "err", msg.err)
		if s := m.openTop(); s != nil {
			return s.update(screenFailed{err: msg.err})
		}
		m.statusNotice = msg.err.Error()
		return nil
	}
	m.log.Info("plan copied", "plan_id", msg.plan.Id, "name", msg.plan.Name)
	m.opening = nil
	m.statusNotice = "plan " + msg.plan.Name + " created from " + msg.from
	if l := m.list(); l != nil {
		l.openTab(planTab(msg.plan.Id))
		m.showOpened(true)
	}
	return nil
}
