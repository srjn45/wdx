package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// What a plan's run does to its branches and slots (docs/PLANS.md).

// CreateBranch makes the branch at start, refusing one already there, or one whose
// name a branch is in the way of, as plan in the way of plan/x.
func CreateBranch(ctx context.Context, dir, branch, start string) error {
	said, err := writeRepo(ctx, dir, nil, "branch", "--no-track", "--", branch, start)
	if err != nil {
		return fmt.Errorf("git branch %s: %s", branch, said)
	}
	return nil
}

// ResetBranch makes the branch, or moves it, to start; git refuses when a checkout
// has it.
func ResetBranch(ctx context.Context, dir, branch, start string) error {
	said, err := writeRepo(ctx, dir, nil, "branch", "--force", "--no-track", "--", branch, start)
	if err != nil {
		return fmt.Errorf("git branch --force %s: %s", branch, said)
	}
	return nil
}

// SwitchReset has the checkout at dir switch to the branch, made or moved to start;
// git refuses when changes are in the way, or another checkout has the branch.
func SwitchReset(ctx context.Context, dir, branch, start string) error {
	said, err := writeRepo(ctx, dir, nil, "switch", "--quiet", "--no-track", "-C", branch, start)
	if err != nil {
		return fmt.Errorf("git switch -C %s: %s", branch, said)
	}
	return nil
}

// Switch has the checkout at dir switch to the branch.
func Switch(ctx context.Context, dir, branch string) error {
	said, err := writeRepo(ctx, dir, nil, "switch", "--quiet", branch)
	if err != nil {
		return fmt.Errorf("git switch %s: %s", branch, said)
	}
	return nil
}

// Detach has the checkout at dir leave its branch for commit, so that it holds no
// branch.
func Detach(ctx context.Context, dir, commit string) error {
	said, err := writeRepo(ctx, dir, nil, "switch", "--quiet", "--detach", commit)
	if err != nil {
		return fmt.Errorf("git switch --detach: %s", said)
	}
	return nil
}

// AddWorktreeOn adds a worktree at path on the branch, which is there and no checkout
// has.
func AddWorktreeOn(ctx context.Context, dir, path, branch string) error {
	said, err := writeRepo(ctx, dir, nil, "worktree", "add", "--quiet", "--", path, branch)
	if err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); ok {
			return &WorktreeError{Path: path, Output: said, Err: err}
		}
		return err
	}
	return nil
}

// CurrentBranch returns the branch of the checkout at dir; "" when it is detached.
func CurrentBranch(ctx context.Context, dir string) (string, error) {
	out, err := readText(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil && isAnswer(err) {
		return "", nil
	}
	return out, err
}

// DeleteNewBranch deletes a branch just made, which holds nothing of anyone's, as
// when what it was made for failed.
func DeleteNewBranch(ctx context.Context, dir, branch string) error {
	said, err := writeRepo(ctx, dir, nil, "branch", "-D", "--", branch)
	if err != nil {
		return fmt.Errorf("git branch -D %s: %s", branch, said)
	}
	return nil
}
