package tide

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
)

// R on a worktree's row or tab rebases it onto its base: wdx tries it, and a conflict
// or what is not committed goes to an agent the person picks, in C's box
// (docs/PLANS.md, "Rebasing a worktree").

const rebaseKey = "R"

// worktreeRebased is the answer to a rebase.
type worktreeRebased struct {
	worktree string
	result   api.WorktreeRebased
	err      error
}

// baseMovedOn reports whether the worktree's base has commits it has not.
func baseMovedOn(w api.WorktreeStatus) bool {
	return w.Behind != nil && *w.Behind > 0
}

// rebasable returns the worktree called name if R can be asked for on it: not the
// clone, not one gone, and not a slot of a plan going on, which its plan rebases.
func (m *Model) rebasable(name string) (api.WorktreeStatus, bool) {
	w, ok := m.worktreeNamed(name)
	if !ok || name == "main" || w.Missing {
		return api.WorktreeStatus{}, false
	}
	if _, slot := m.slotAt(w.Path); slot {
		return api.WorktreeStatus{}, false
	}
	return w, true
}

// rebaseBarKey is what the bar of a worktree's tab says of R: only once its base has
// moved on, though R works on any.
func (m *Model) rebaseBarKey(name string) (barKey, bool) {
	w, ok := m.rebasable(name)
	if !ok || !baseMovedOn(w) {
		return barKey{}, false
	}
	return barKey{rebaseKey, "rebase", 8}, true
}

// rebaseRow is the worktree under the cursor in the Worktrees section, which R
// rebases.
func (m *Model) rebaseRow() (string, bool) {
	l, rows := m.list(), m.worktreeRows()
	if m.layout.focus != project || l == nil || l.section != sectionWorktrees || l.worktreeRow >= len(rows) {
		return "", false
	}
	r := rows[l.worktreeRow]
	if r.file != nil || r.plan != nil {
		return "", false
	}
	_, ok := m.rebasable(r.worktree.Name)
	return r.worktree.Name, ok
}

// askRebaseWorktree starts the rebase of the worktree called name, or says why not.
func (m *Model) askRebaseWorktree(name string) tea.Cmd {
	if s, ok := m.slotNamed(name); ok {
		m.statusNotice = fmt.Sprintf("%s is a slot of plan %s, which rebases it onto %s", name, s.plan.Name, planBranch(s.plan.Name))
		return nil
	}
	if _, ok := m.rebasable(name); !ok {
		return nil
	}
	if agents := m.workingAgentsIn(name); len(agents) > 0 {
		m.statusNotice = strings.Join(agents, ", ") + " is working here: rebase once it stops"
		return nil
	}
	ref := "id:" + m.following.project.Id
	m.statusNotice = "rebasing " + name + "…"
	m.log.Info("rebase started", "worktree", name)
	return func() tea.Msg {
		r, err := m.client.RebaseWorktree(m.ctx, name, ref, "", "")
		return worktreeRebased{worktree: name, result: r, err: err}
	}
}

// worktreeRebased says how the rebase ended, and opens the box that asks an agent to
// do what wdx would not.
func (m *Model) worktreeRebased(msg worktreeRebased) {
	if msg.err == nil {
		r := msg.result
		m.log.Info("rebase ended", "worktree", msg.worktree, "result", r.Result)
		if r.Result == api.ResultUpToDate {
			m.statusNotice = msg.worktree + " is up to date with " + r.Base
			return
		}
		m.statusNotice = "rebased " + msg.worktree + " onto " + r.Base
		if r.Pushed {
			m.statusNotice += ", pushed"
		}
		return
	}
	m.log.Info("rebase refused", "worktree", msg.worktree, "err", msg.err)
	w, ok := m.worktreeNamed(msg.worktree)
	p, isProblem := errors.AsType[*client.ProblemError](msg.err)
	if !ok || !isProblem || (p.Problem.Code != api.ErrorCodeRebaseFailed && p.Problem.Code != api.ErrorCodeNotCommitted) {
		m.statusNotice = msg.worktree + ": " + msg.err.Error()
		return
	}
	agents, chosen := m.agentsToAsk(w.Name)
	if len(agents) == 0 {
		m.statusNotice = msg.worktree + ": " + msg.err.Error() + ", and no agent is running in this project"
		return
	}
	m.statusNotice = ""
	branch, base := w.Name, deref(w.Base)
	if w.Branch != nil {
		branch = *w.Branch
	}
	c := &commitBox{worktree: w.Name, agents: agents, chosen: chosen}
	if p.Problem.Code == api.ErrorCodeNotCommitted {
		c.text = "Commit what is not committed in " + w.Path + ", then rebase " + branch + " onto " + base
	} else {
		c.rebase = problemFiles(p.Problem, "conflicts")
		c.text = "Rebase " + branch + " onto " + base + " in " + w.Path + ", resolving the conflicts"
	}
	m.commit = c
	m.log.Info("agent asked for", "worktree", w.Name, "code", p.Problem.Code)
}

// problemFiles are the files a problem's details list under key; never nil.
func problemFiles(p api.Problem, key string) []string {
	files := []string{}
	if p.Details == nil {
		return files
	}
	list, _ := (*p.Details)[key].([]any)
	for _, f := range list {
		if s, ok := f.(string); ok {
			files = append(files, s)
		}
	}
	return files
}

// planWorktree returns the worktree on the branch of the plan called name.
func (m *Model) planWorktree(name string) (api.WorktreeStatus, bool) {
	list := m.worktreeStatuses()
	i := slices.IndexFunc(list, func(w api.WorktreeStatus) bool { return !w.Missing && w.Branch != nil && *w.Branch == planBranch(name) })
	if i < 0 {
		return api.WorktreeStatus{}, false
	}
	return list[i], true
}

// stackedMovedOn returns the stacked plans the plan of plans c lists whose worktree's
// base has moved on, by name, for its tab to say R.
func (m *Model) stackedMovedOn(c *api.PlanContent) map[string]bool {
	if c == nil || c.Definition.Plans == nil {
		return nil
	}
	moved := map[string]bool{}
	for _, p := range *c.Definition.Plans {
		if p.StackedOn == nil {
			continue
		}
		if w, ok := m.planWorktree(p.Id); ok && baseMovedOn(w) {
			moved[p.Id] = true
		}
	}
	return moved
}
