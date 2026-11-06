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

// NotCommittedError is a checkout with files not committed, which wdx never stashes.
type NotCommittedError struct {
	Name  string
	Files []string
}

func (e *NotCommittedError) Error() string {
	return fmt.Sprintf("%s: %d in %s", ErrNotCommitted, len(e.Files), e.Name)
}

func (e *NotCommittedError) Is(target error) bool { return target == ErrNotCommitted }

func notCommitted(s Status) error {
	files := make([]string, len(s.Files))
	for i, f := range s.Files {
		files[i] = f.Path
	}
	return &NotCommittedError{Name: s.Name, Files: files}
}

// PushError is a rebase done whose branch origin refused: the rebase is kept.
type PushError struct{ Err error }

func (e *PushError) Error() string { return "rebased, but not pushed: " + e.Err.Error() }

func (e *PushError) Unwrap() error { return e.Err }

// Rebased is how a rebase of a checkout ended.
type Rebased struct {
	Name   string
	Base   string
	Before string // the commit it was on
	After  string // the commit it is on: Before when it already sat on Base
	Pushed bool
}

// UpToDate reports whether the branch already sat on its base, so nothing moved.
func (r Rebased) UpToDate() bool { return r.Before == r.After }

// Onto is where the rebase of a stacked checkout takes it: its commits after From,
// the commit it was stacked on, onto Commit, the tip of the branch Base.
type Onto struct {
	Base, Commit, From string
}

// Rebase rebases the checkout called name onto its base, the step of landing by rebase
// on its own (docs/PLANS.md, "Rebasing a worktree"); a stacked one, for which stacked
// is not nil, onto where stacked says, once it can be rebased. A rebase that stops is
// undone and returns a *git.RebaseError with the files in conflict. A branch on origin
// is pushed again, over its commit there before the rebase only.
func Rebase(ctx context.Context, c Checkouts, name string, agents []Agent, stacked func(context.Context) (Onto, error)) (Rebased, error) {
	log := logging.FromContext(ctx)
	s, err := rebasable(ctx, c, name, agents)
	if err != nil {
		return Rebased{}, err
	}
	var onto Onto
	if stacked != nil {
		if onto, err = stacked(ctx); err != nil {
			return Rebased{}, err
		}
		s.Base = onto.Base
	}
	r := Rebased{Name: s.Name, Base: s.Base, Before: s.Head}
	was, err := git.OriginBranch(ctx, s.Path, s.Branch)
	if err != nil {
		return Rebased{}, err
	}
	log.Info("rebase asked", "worktree", s.Name, "base", s.Base, "head", s.Head, "on_origin", was, "base_commit", onto.From)
	if stacked != nil {
		r.After, err = rebaseFrom(ctx, s.Path, onto)
	} else {
		r.After, err = RebaseOnto(ctx, s.Path, s.Base)
	}
	if err != nil {
		if e, ok := errors.AsType[*git.RebaseError](err); ok {
			log.Info("rebase stopped and undone", "worktree", s.Name, "conflicts", e.Conflicts)
		}
		return Rebased{}, err
	}
	if r.UpToDate() {
		log.Info("rebase up to date", "worktree", s.Name, "base", s.Base)
		return r, nil
	}
	log.Info("rebased", "worktree", s.Name, "base", s.Base, "before", r.Before, "after", r.After)
	if was == "" {
		return r, nil
	}
	if err := git.PushOver(ctx, s.Path, s.Branch, was); err != nil {
		log.Info("rebased but not pushed", "worktree", s.Name, "branch", s.Branch, "err", err)
		return r, &PushError{Err: err}
	}
	log.Info("rebase pushed", "worktree", s.Name, "branch", s.Branch, "over", was)
	r.Pushed = true
	return r, nil
}

// rebasable returns the checkout called name, read now, if it can be rebased: refused
// as landing refuses, but with nothing ahead too, as the base may still have moved on.
func rebasable(ctx context.Context, c Checkouts, name string, agents []Agent) (Status, error) {
	s, err := Find(ctx, c, name)
	if err != nil {
		return Status{}, err
	}
	if s.Path == c.Clone {
		return Status{}, ErrIsClone
	}
	if err := readFiles(ctx, &s); err != nil {
		return Status{}, err
	}
	if err := readCommits(ctx, &s); err != nil {
		return Status{}, err
	}
	busy, err := git.OperationInProgress(ctx, s.Path)
	if err != nil {
		return Status{}, err
	}
	switch {
	case busy:
		return Status{}, ErrWorktreeBusy
	case len(s.Files) > 0:
		return Status{}, notCommitted(s)
	case s.Branch == "":
		return Status{}, ErrDetachedHead
	case s.Base == "" || strings.HasPrefix(s.Base, "origin/"):
		return Status{}, ErrNoLocalBase
	}
	if i := slices.IndexFunc(agents, func(a Agent) bool { return a.Working && s.Holds(a.Dir) }); i >= 0 {
		return Status{}, fmt.Errorf("%w: %s", ErrAgentWorking, agents[i].ID)
	}
	return s, nil
}

// rebaseFrom moves the commits of the checkout at path after onto.From onto
// onto.Commit, unless that is where they are, and returns the commit it is then at.
func rebaseFrom(ctx context.Context, path string, onto Onto) (string, error) {
	if onto.Commit != onto.From {
		logging.FromContext(ctx).Info("rebasing from a kept commit", "path", path, "base", onto.Base, "onto", onto.Commit, "from", onto.From)
		if err := git.RebaseFrom(ctx, path, onto.Commit, onto.From); err != nil {
			return "", err
		}
	}
	return git.Resolve(ctx, path, "HEAD")
}
