package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/srjn45/wdx/internal/agent"
	"github.com/srjn45/wdx/internal/gh"
	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/plan"
	"github.com/srjn45/wdx/internal/project"
	"github.com/srjn45/wdx/internal/sqlite"
	"github.com/srjn45/wdx/internal/terminal"
	"github.com/srjn45/wdx/internal/worktree"
)

// startRunner starts what runs the plans, before the terminals' supervisor, which
// tells it of agents that end. Its options read the daemon's fields when a plan is
// woken, which is once they are all set.
func (d *Daemon) startRunner() {
	d.planParallel.Store(int64(cmp.Or(d.opts.PlanParallel, DefaultPlanParallel)))
	ctx, cancel := context.WithCancel(logging.NewContext(context.Background(), d.opts.Log))
	d.stopRunner = cancel
	d.runner = plan.NewRunner(ctx, d.plans, plan.RunnerOptions{
		Changes:  &d.changes,
		Parallel: func() int { return int(d.planParallel.Load()) },
		Project: func(ctx context.Context, id string) (plan.ProjectInfo, error) {
			p, err := d.projects.GetByID(ctx, id)
			return plan.ProjectInfo{Clone: p.ClonePath(), WorktreesDir: p.WorktreesDir(), Open: p.State == project.StateOpen}, err
		},
		AddWorktree: func(ctx context.Context, projectID, path, branch string) error {
			p, err := d.projects.GetByID(ctx, projectID)
			if err != nil {
				return err
			}
			_, err = d.projects.AddWorktreeOn(ctx, p, path, branch)
			return err
		},
		RemoveWorktree: func(ctx context.Context, projectID, path string) error {
			p, err := d.projects.GetByID(ctx, projectID)
			if err != nil {
				return err
			}
			if err := git.RemoveWorktree(ctx, p.ClonePath(), path); err != nil {
				return err
			}
			if err := d.projects.ForgetWorktree(ctx, projectID, path); err != nil {
				return err
			}
			return d.tabs.RemoveWorktree(ctx, projectID, filepath.Base(path))
		},
		CreateAgent: func(ctx context.Context, projectID, name, dir, first string, linked func(ctx context.Context, tx sqlite.Execer, agentID string) error) (string, error) {
			a, err := d.agents.CreateLinked(ctx, projectID, name, dir, agent.Launch{}, first, linked)
			return a.ID, err
		},
		ResumeAgent: func(ctx context.Context, id, text string, linked func(ctx context.Context, tx sqlite.Execer, agentID string) error) error {
			_, err := d.agents.ResumeLinked(ctx, id, text, linked)
			return err
		},
		AgentIn: func(ctx context.Context, projectID, dir string) (string, error) {
			list, err := d.agents.Store().List(ctx, projectID)
			if err != nil {
				return "", err
			}
			slot := worktree.Status{Path: dir}
			for _, a := range list {
				if a.Terminal.State.Live() && slot.Holds(workingDir(a)) {
					return a.Terminal.Name, nil
				}
			}
			return "", nil
		},
		AgentNameTaken: func(ctx context.Context, projectID, name string) (bool, error) {
			_, err := d.agents.Store().GetByName(ctx, projectID, name)
			if errors.Is(err, agent.ErrNotFound) {
				return false, nil
			}
			return err == nil, err
		},
		StopAgent: func(ctx context.Context, id string) error {
			_, err := d.agents.Stop(ctx, id)
			return err
		},
		RemoveAgent: func(ctx context.Context, id string) error {
			_, _, err := d.agents.Remove(ctx, id, false)
			return err
		},
		AgentStates: d.agentStates,
		CheckCommand: func(ctx context.Context, projectID string) (string, error) {
			v, _, err := d.projects.LandSettings(ctx, projectID)
			return v.Check, err
		},
		Check: d.runPlanCheck,
		PullRequest: func(ctx context.Context, dir, branch string) (plan.PullRequest, bool, error) {
			pr, err := gh.CLI{Program: d.opts.GH}.View(ctx, dir, branch)
			switch {
			case errors.Is(err, gh.ErrMissing):
				logging.FromContext(ctx).Info("no gh to read the PR of a plan", "branch", branch)
				return plan.PullRequest{}, false, nil
			case errors.Is(err, gh.ErrNoPullRequest):
				return plan.PullRequest{}, false, nil
			case err != nil:
				return plan.PullRequest{}, false, err
			}
			return plan.PullRequest{Number: pr.Number, MergeCommit: pr.MergeCommit}, true, nil
		},
		LandByPR: d.landPlanByPR,
		Changed: func(projectID string) {
			d.tracker.Refresh(projectID, "")
			d.notices.notify()
		},
	})
}

// agentStates is what the runner sees of the agents with ids: one removed is
// AgentRemoved, and one not found is left out, which the runner takes as removed.
func (d *Daemon) agentStates(ctx context.Context, ids []string) (map[string]plan.AgentState, error) {
	states := map[string]plan.AgentState{}
	for _, id := range ids {
		a, err := d.agents.Store().Get(ctx, id)
		if errors.Is(err, agent.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		switch {
		case a.Tombstone():
			states[id] = plan.AgentRemoved
		case a.Terminal.State.Live():
			states[id] = plan.AgentLive
		case a.Terminal.State == terminal.StateStopped:
			states[id] = plan.AgentStopped
		default:
			states[id] = plan.AgentEnded
		}
	}
	return states, nil
}

// agentEnded wakes the plan whose task the agent on terminalID does, if any.
func (d *Daemon) agentEnded(terminalID string) {
	ctx := logging.NewContext(context.Background(), d.log)
	a, err := d.agents.Store().GetByTerminal(ctx, terminalID)
	if err != nil {
		logging.FromContext(ctx).Info("ended agent not found", "terminal_id", terminalID, "err", err)
		return
	}
	planID, taskID, ok, err := d.plans.TaskOfAgent(ctx, a.ID)
	if err != nil {
		logging.FromContext(ctx).Error("plan of ended agent not found", "agent_id", a.ID, "err", err)
		return
	}
	if ok {
		logging.FromContext(ctx).Info("task's agent ended", "agent_id", a.ID, "plan_id", planID, "task_id", taskID)
		d.runner.Wake(planID)
	}
}

// clearPrompt forgets the prompt of the agent whose terminal ended: its text stays
// only while it is open, and the stream tells of the change.
func (d *Daemon) clearPrompt(terminalID string) {
	ctx := logging.NewContext(context.Background(), d.log)
	changed, err := d.agents.Store().ClearPrompt(ctx, terminalID)
	if err != nil {
		logging.FromContext(ctx).Error("ended agent's prompt not cleared", "terminal_id", terminalID, "err", err)
	}
	if changed {
		d.notices.notify()
	}
}

// runPlanCheck runs the project's landing check in a slot, as a worktree's landing
// does, in the terminal plan.CheckTerminal names. A check of the slot's still
// running is a landing's that was cut short, as slots are the plan's: it is stopped
// first. One cut short now is stopped too.
func (d *Daemon) runPlanCheck(ctx context.Context, projectID, dir, command string) error {
	stop := func(ctx context.Context) {
		t, err := d.terminals.Store().GetByName(ctx, projectID, plan.CheckTerminal(dir))
		if err != nil || !t.State.Live() {
			return
		}
		if _, err := d.terminals.Remove(ctx, t.ID, true); err != nil {
			logging.FromContext(ctx).Error("plan check not stopped", "terminal_id", t.ID, "err", err)
			return
		}
		logging.FromContext(ctx).Info("plan check stopped", "terminal_id", t.ID)
	}
	stop(ctx)
	err := d.runLandCheck(ctx, projectID, filepath.Base(dir), dir, command, func(string) {})
	if ctx.Err() != nil {
		stop(context.WithoutCancel(ctx))
	}
	return err
}

// landPlanByPR starts landing the project's worktree at path by PR, a done plan's,
// with the project's check, as wdx worktree land --method pr does.
func (d *Daemon) landPlanByPR(ctx context.Context, projectID, path string) error {
	p, err := d.projects.GetByID(ctx, projectID)
	if err != nil {
		return err
	}
	settings, ok, err := d.projects.LandSettings(ctx, p.ID)
	if err != nil {
		return err
	}
	if !ok {
		return errLandNotConfigured
	}
	c, err := checkoutsOf(ctx, d.plans, p)
	if err != nil {
		return err
	}
	list, err := worktree.List(ctx, c)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(list, func(s worktree.Status) bool { return s.Path == path })
	if i < 0 {
		return fmt.Errorf("%w: %s", worktree.ErrNotFound, path)
	}
	agents, err := d.agents.Store().List(ctx, p.ID)
	if err != nil {
		return err
	}
	_, err = d.lander.Start(ctx, p.ID, c, list[i].Name, workingAgents(agents), worktree.LandSettings{Method: worktree.MethodPR, Check: settings.Check, Push: settings.Push})
	return err
}
