package worktree

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/logging"
)

// ErrIsClone is a request to remove the project's clone, which is never removed.
var ErrIsClone = errors.New("the project's clone cannot be removed")

// NotLandedError is a worktree whose removal could lose work.
type NotLandedError struct {
	Name   string
	Reason string
}

func (e *NotLandedError) Error() string {
	return fmt.Sprintf("worktree %s has not landed: %s", e.Name, e.Reason)
}

// Removable returns the checkout called name, read now, unless removing it could
// lose work. One whose directory is gone always can be removed: only git's entry
// for it goes, and its branch keeps its commits.
func Removable(ctx context.Context, c Checkouts, name string) (Status, error) {
	list, err := List(ctx, c)
	if err != nil {
		return Status{}, err
	}
	i := slices.IndexFunc(list, func(s Status) bool { return s.Name == name })
	if i < 0 {
		return Status{}, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	s := list[i]
	if s.Path == c.Clone {
		return Status{}, ErrIsClone
	}
	if s.Missing {
		return s, nil
	}
	if err := readFiles(ctx, &s); err != nil {
		return Status{}, err
	}
	if err := readCommits(ctx, &s); err != nil {
		return Status{}, err
	}
	if reason := notLanded(s); reason != "" {
		return Status{}, &NotLandedError{Name: name, Reason: reason}
	}
	return s, nil
}

// notLanded says what keeps s from counting as landed, as tide's row does: "" when
// nothing does. A base on origin means there is no local one to have landed on.
func notLanded(s Status) string {
	switch {
	case len(s.Files) > 0:
		return fmt.Sprintf("%d files are not committed", len(s.Files))
	case s.Base == "" || s.Ahead == nil || strings.HasPrefix(s.Base, "origin/"):
		return "it has no local base to compare its commits with"
	case *s.Ahead > 0:
		return fmt.Sprintf("%d commits are not on %s", *s.Ahead, s.Base)
	}
	return ""
}

// Removal is what removing a checkout did to its branch.
type Removal struct {
	// BranchDeleted is the branch that went with it, BranchKept the one that stayed,
	// as git refused or it is the default branch; "" with no branch.
	BranchDeleted, BranchKept string
}

// Remove removes the checkout s, as Removable returned it, and its branch if that
// is merged. git decides again: a file written since the check makes it refuse, and
// a commit made since keeps the branch.
func Remove(ctx context.Context, s Status) (Removal, error) {
	if s.repository == "" {
		return Removal{}, nil // no repository lists it: only the project's record of it is left
	}
	if err := git.RemoveWorktree(ctx, s.repository, s.Path); err != nil {
		return Removal{}, err
	}
	if s.Branch == "" {
		return Removal{}, nil
	}
	keep := Removal{BranchKept: s.Branch}
	def, err := git.DefaultBranch(ctx, s.repository)
	if err != nil {
		logging.FromContext(ctx).Info("branch kept: default branch not known", "branch", s.Branch, "err", err)
		return keep, nil
	}
	if s.Branch == def {
		logging.FromContext(ctx).Info("branch kept: it is the default branch", "branch", s.Branch)
		return keep, nil
	}
	deleted, err := git.DeleteBranch(ctx, s.repository, s.Branch)
	if err != nil {
		logging.FromContext(ctx).Info("branch kept", "branch", s.Branch, "err", err)
		return keep, nil
	}
	if !deleted {
		return keep, nil
	}
	return Removal{BranchDeleted: s.Branch}, nil
}

// RemovedMessage tells an agent that was working in the checkout s where to go on.
func RemovedMessage(s Status, clone string) string {
	on := ""
	if s.Base != "" {
		on = ", on " + s.Base
	}
	return fmt.Sprintf("The worktree %s at %s was removed: its directory no longer exists. Work from the project's clone at %s%s from now on.", s.Name, s.Path, clone, on)
}

// Holds reports whether dir is the checkout s or inside it.
func (s Status) Holds(dir string) bool {
	return dir == s.Path || strings.HasPrefix(dir, s.Path+"/")
}
