package daemon

import (
	"cmp"
	"context"
	"errors"
	"path/filepath"

	"github.com/srjn45/wdx/internal/agent"
	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/plan"
	"github.com/srjn45/wdx/internal/project"
	"github.com/srjn45/wdx/internal/terminal"
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
		CreateAgent: func(ctx context.Context, projectID, name, dir, first string, linked func(ctx context.Context, tx db.Execer, agentID string) error) (string, error) {
			a, err := d.agents.CreateLinked(ctx, projectID, name, dir, first, linked)
			return a.ID, err
		},
		StopAgent: func(ctx context.Context, id string) error {
			_, err := d.agents.Stop(ctx, id)
			return err
		},
		AgentStates: d.agentStates,
		Changed: func(projectID string) {
			d.tracker.Refresh(projectID, "")
			d.notices.notify()
		},
	})
}

// agentStates is what the runner sees of the agents with ids: one removed or not
// found has ended.
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
			states[id] = plan.AgentEnded
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
