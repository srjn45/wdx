// Package project manages projects: git repositories that agents work in.
package project

import (
	"path/filepath"
	"slices"
	"time"
)

// State is a project's lifecycle state.
type State string

const (
	StateOpen   State = "open"
	StateClosed State = "closed"
)

// Project is a git repository registered with warden. All clones of the
// repository and all their worktrees belong to one project.
type Project struct {
	ID        string
	Name      string
	OriginURL string // "" when the repo has no origin remote
	State     State
	Worktrees []Worktree
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Summary is a project as listed, without its worktrees.
type Summary struct {
	ID            string
	Name          string
	OriginURL     string // "" when the repo has no origin remote
	State         State
	WorktreeCount int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Worktree is a local checkout of a project: a clone or a linked worktree.
type Worktree struct {
	ID           string
	Path         string // root of the work tree
	GitCommonDir string // shared by all worktrees of one clone
	CreatedAt    time.Time
}

// ClonePath is where the project was first cloned: its earliest checkout that
// is not a linked worktree.
func (p Project) ClonePath() string {
	for _, w := range p.Worktrees {
		if w.GitCommonDir == filepath.Join(w.Path, ".git") {
			return w.Path
		}
	}
	if len(p.Worktrees) > 0 {
		return p.Worktrees[0].Path
	}
	return ""
}

// Holds reports whether path is in one of the project's worktrees.
func (p Project) Holds(path string) bool {
	forms := pathForms(path)
	return slices.ContainsFunc(p.Worktrees, func(w Worktree) bool { return within(w.Path, forms) })
}

// Opened is the result of opening a project.
type Opened struct {
	Project
	Registered bool       // the project did not exist before
	Cloned     bool       // the repository was cloned to open it
	Dropped    []Worktree // checkouts no longer on disk, removed from the project
}
