package project

import (
	"errors"
	"fmt"
)

// CheckoutMissingError is returned when none of a known project's checkouts
// exist on disk anymore. Reclone or Remove resolves it.
type CheckoutMissingError struct {
	Project Project
}

func (e *CheckoutMissingError) Error() string {
	return fmt.Sprintf("project %s: no checkout found on disk", e.Project.Name)
}

// NameTakenError is returned when a project would get a name another project
// already uses.
type NameTakenError struct {
	Name    string
	Project Project
}

func (e *NameTakenError) Error() string {
	return fmt.Sprintf("project name %q is already used by project %s", e.Name, e.Project.ID)
}

// RepoExistsError is returned when creating a project at Dest, which is or is
// inside the git repository Root. Project is set when Root is a known checkout.
type RepoExistsError struct {
	Dest    string
	Root    string
	Project *Project
}

func (e *RepoExistsError) Error() string {
	msg := fmt.Sprintf("%s is already a git repository", e.Dest)
	if e.Root != e.Dest {
		msg = fmt.Sprintf("%s is inside git repository %s", e.Dest, e.Root)
	}
	if e.Project != nil {
		msg += fmt.Sprintf(" (project %s)", e.Project.Name)
	}
	return msg
}

// InvalidNameError is returned for a project name that cannot be used.
type InvalidNameError struct {
	Name   string
	Reason string
}

func (e *InvalidNameError) Error() string {
	return fmt.Sprintf("invalid project name %q: %s", e.Name, e.Reason)
}

// InvalidWorktreeNameError is returned for a worktree name that cannot be used.
type InvalidWorktreeNameError struct {
	Name   string
	Reason string
}

func (e *InvalidWorktreeNameError) Error() string {
	return fmt.Sprintf("invalid worktree name %q: %s", e.Name, e.Reason)
}

// BranchExistsError is returned when a new worktree's branch exists: a new
// worktree always gets a new branch of its own name.
type BranchExistsError struct {
	Branch string
}

func (e *BranchExistsError) Error() string {
	return fmt.Sprintf("branch %s already exists", e.Branch)
}

// InvalidPathError is returned for a path that cannot be used as asked.
type InvalidPathError struct {
	Path   string
	Reason string
}

func (e *InvalidPathError) Error() string {
	return e.Path + ": " + e.Reason
}

// PathExistsError is returned when a project would be cloned to a path that
// is already taken by something else.
type PathExistsError struct {
	Path string
}

func (e *PathExistsError) Error() string {
	return e.Path + " already exists"
}

// ProjectMismatchError is returned when the work tree at Root is a checkout of
// project ByClone but its origin belongs to project ByOrigin.
type ProjectMismatchError struct {
	Root     string
	Origin   string
	ByClone  Project
	ByOrigin Project
}

func (e *ProjectMismatchError) Error() string {
	return fmt.Sprintf("%s is a worktree of project %s but its origin %s belongs to project %s",
		e.Root, e.ByClone.Name, e.Origin, e.ByOrigin.Name)
}

// ErrNoOrigin is returned when recloning a project that has no origin.
var ErrNoOrigin = errors.New("project has no origin to clone from")

// ErrEmptyURL is returned when opening an empty git URL.
var ErrEmptyURL = errors.New("git URL is empty")

// ErrNotGitRepo is returned when opening a directory that is not a git repository.
var ErrNotGitRepo = errors.New("not a git repository")

// ErrNotFound is returned when no project matches a name or id.
var ErrNotFound = errors.New("project not found")
