package plan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/worktree"
)

// A stacked plan starts on the branch of the plan before it, its base, and its run
// keeps the commit of that branch it sits on, so that R moves only its own commits
// once that branch was rewritten or merged (docs/PLANS.md, Step 7 in detail).

// BaseNotPulledError is R refused on a stacked plan's worktree whose plan below has
// its PR merged on GitHub, its merge commit not yet in the local default branch.
type BaseNotPulledError struct {
	Below   string // the plan below, by name
	Default string // the default branch
}

func (e *BaseNotPulledError) Error() string {
	return fmt.Sprintf("the PR of %s is merged on GitHub, but its merge is not in the local %s", e.Below, e.Default)
}

// StackedBases returns the bases of the branches of the project's stacked plans,
// and of their tasks, by branch: the branch of the plan before each, while its PR
// does not go on the default branch.
func (s *Store) StackedBases(ctx context.Context, projectID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.branch, r.base, coalesce(t.task_id, '') FROM plan_runs r JOIN docs d ON d.id = r.plan_id
		LEFT JOIN plan_tasks t ON t.plan_id = r.plan_id
		WHERE d.project_id = ? AND r.base_commit IS NOT NULL`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bases := map[string]string{}
	for rows.Next() {
		var branch, base, task string
		if err := rows.Scan(&branch, &base, &task); err != nil {
			return nil, err
		}
		bases[branch] = base
		if task != "" {
			bases[TaskBranch(strings.TrimPrefix(branch, BranchPrefix), task)] = base
		}
	}
	return bases, rows.Err()
}

// StackedRun returns the run of the project's stacked plan whose branch is branch,
// and false when no stacked plan, or one whose PR goes on the default branch, has it.
func (s *Store) StackedRun(ctx context.Context, projectID, branch string) (Run, bool, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT r.plan_id FROM plan_runs r JOIN docs d ON d.id = r.plan_id
		WHERE d.project_id = ? AND r.branch = ? AND r.base_commit IS NOT NULL`, projectID, branch).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, err
	}
	return s.Run(ctx, id)
}

// stackedOnOf returns the plan the plan with id is stacked on, by name, "" for none.
func (s *Store) stackedOnOf(ctx context.Context, id string) (string, error) {
	var base string
	err := s.db.QueryRowContext(ctx, `SELECT base FROM plan_runs WHERE plan_id = ? AND base_commit IS NOT NULL`, id).Scan(&base)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return strings.TrimPrefix(base, BranchPrefix), err
}

// restack writes the stacked run's new base, at baseCommit, "" once its base is the
// default branch.
func (s *Store) restack(ctx context.Context, planID, base, baseCommit string) error {
	if _, err := db.Exec(ctx, s.db, `UPDATE plan_runs SET base = ?, base_commit = ? WHERE plan_id = ?`, base, nullString(baseCommit), planID); err != nil {
		return err
	}
	if err := touch(ctx, s.db, planID); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("plan's base moved", "plan_id", planID, "base", base, "base_commit", baseCommit)
	return nil
}

// StackedOnto says where R takes the worktree of the stacked run, asking gh whether
// the PR of the plan below has merged: merged, with its merge commit in the local
// default branch, onto the default branch; merged but not pulled, nowhere, a
// *BaseNotPulledError; not merged, onto the plan below's branch as it is now. Either
// way only the run's own commits move, those after its base commit.
func (r *Runner) StackedOnto(ctx context.Context, run Run) (worktree.Onto, error) {
	project, err := r.opts.Project(ctx, run.ProjectID)
	if err != nil {
		return worktree.Onto{}, err
	}
	def, err := git.DefaultBranch(ctx, project.Clone)
	if err != nil {
		return worktree.Onto{}, err
	}
	if def == "" {
		return worktree.Onto{}, errors.New("the project has no default branch")
	}
	below := strings.TrimPrefix(run.Base, BranchPrefix)
	merged, err := r.mergedIn(ctx, project.Clone, below, def)
	if err != nil {
		return worktree.Onto{}, err
	}
	onto := worktree.Onto{Base: run.Base, From: run.BaseCommit}
	switch merged {
	case mergedNotPulled:
		return worktree.Onto{}, &BaseNotPulledError{Below: below, Default: def}
	case mergedIn:
		onto.Base = def
	}
	if onto.Commit, err = git.Resolve(ctx, project.Clone, "refs/heads/"+onto.Base); err != nil {
		return worktree.Onto{}, err
	}
	logging.FromContext(ctx).Info("stacked plan's rebase decided", "plan_id", run.PlanID, "below", below, "merged", merged == mergedIn, "base", onto.Base, "onto", onto.Commit, "base_commit", run.BaseCommit)
	return onto, nil
}

// Restacked writes where R took the stacked run's branch, onto: onto the default
// branch, which it is not stacked on from then on, reporting true for its PR to go
// there too; else onto the plan below's commit.
func (r *Runner) Restacked(ctx context.Context, run Run, onto worktree.Onto) (bool, error) {
	defer r.opts.Changed(run.ProjectID)
	if onto.Base != run.Base {
		return true, r.store.restack(ctx, run.PlanID, onto.Base, "")
	}
	return false, r.store.restack(ctx, run.PlanID, onto.Base, onto.Commit)
}
