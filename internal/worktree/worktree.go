// Package worktree reads what git knows of a project's checkouts: every one
// `git worktree list` gives, the files with uncommitted changes and the commits
// ahead of a base. It reads with the reads of internal/git, so that it can run
// beside any git command of a human or an agent (docs/TIDE_WORKTREES.md). The one
// thing it writes is the removal of a worktree that landed, when asked to.
package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/logging"
)

// mainName names the project's clone, as an agent's worktree does.
const mainName = "main"

// ErrNotFound is a worktree no checkout of the project has as its name, or one
// whose directory is gone.
var ErrNotFound = errors.New("no such worktree")

// InvalidPathError is a file's path that is not inside its worktree.
type InvalidPathError struct {
	Path string
}

func (e *InvalidPathError) Error() string {
	return fmt.Sprintf("%q is not a path inside the worktree", e.Path)
}

// Status is what is known of a checkout.
type Status struct {
	Name    string
	Path    string
	Branch  string // "" when HEAD is detached
	Head    string // "" with no commit yet
	Missing bool   // its directory is gone
	Base    string // what its commits are measured against; "" when nothing fits
	// Ahead is how many commits it has that Base has not; nil when not known.
	Ahead *int
	// Added and Removed are the lines of those commits; nil when not known.
	Added, Removed *int
	Files          []File
	// Committed are the files its commits changed since it left Base.
	Committed []CommittedFile
	// Comments are its comments not sent yet.
	Comments []Comment
	// repository is the git directory that lists it; "" when none does.
	repository string
}

// CommittedFile is a file a checkout's commits changed.
type CommittedFile struct {
	Path string
	// Status is one letter: M changed, A added, D deleted.
	Status   string
	Verified bool
}

// File is a file with uncommitted changes.
type File struct {
	Path string
	// Status is one letter: M changed, A added, D deleted, ? untracked, U in
	// conflict.
	Status string
	// Size and ModifiedAt tell an edit from the last: git's status of a file
	// edited twice stays M. A deleted file has neither.
	Size       int64
	ModifiedAt *time.Time
	Verified   bool
}

// Checkouts are the checkouts a project knows of.
type Checkouts struct {
	Clone string // the one named main
	Paths []string
}

// Read returns everything known of the project's checkouts, reading it all now, with
// the marks that still hold.
func Read(ctx context.Context, c Checkouts, marks []Mark, drafts []Comment) ([]Status, error) {
	list, err := List(ctx, c)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if err := readFiles(ctx, &list[i]); err != nil {
			return nil, err
		}
		if err := readCommits(ctx, &list[i]); err != nil {
			return nil, err
		}
		applyMarks(ctx, &list[i], marks)
		applyComments(ctx, &list[i], drafts)
	}
	return list, nil
}

// List returns the checkouts git lists for the repositories of c, then those of c
// no repository lists any more, without reading their files or commits.
func List(ctx context.Context, c Checkouts) ([]Status, error) {
	var list []Status
	repositories := map[string]bool{}
	for _, path := range c.Paths {
		if !isDir(path) {
			continue
		}
		_, common, err := git.Dirs(ctx, path)
		if err != nil {
			logging.FromContext(ctx).Info("checkout not read", "path", path, "err", err)
			continue
		}
		if repositories[common] {
			continue
		}
		repositories[common] = true
		checkouts, err := git.Checkouts(ctx, path)
		if err != nil {
			return nil, err
		}
		for _, o := range checkouts {
			list = append(list, Status{Path: o.Path, Branch: o.Branch, Head: o.Head, Missing: o.Prunable || !isDir(o.Path), repository: common})
		}
	}
	for _, path := range c.Paths {
		if !slices.ContainsFunc(list, func(s Status) bool { return s.Path == path }) {
			list = append(list, Status{Path: path, Missing: true})
		}
	}
	named(list, c.Clone)
	return list, nil
}

// named gives each checkout its name: main for the clone, else the last part of
// its path. A later one with a name taken gets a number, so that a name is one
// checkout.
func named(list []Status, clone string) {
	taken := map[string]bool{}
	for i := range list {
		base := filepath.Base(list[i].Path)
		if list[i].Path == clone {
			base = mainName
		}
		name := base
		for n := 2; taken[name]; n++ {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		taken[name] = true
		list[i].Name = name
	}
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// readFiles reads the files of s with uncommitted changes.
func readFiles(ctx context.Context, s *Status) error {
	s.Files = nil
	if s.Missing {
		return nil
	}
	changes, err := git.Changes(ctx, s.Path)
	if err != nil {
		return err
	}
	for _, c := range changes {
		f := File{Path: c.Path, Status: c.Status}
		if info, err := os.Lstat(filepath.Join(s.Path, c.Path)); err == nil {
			at := info.ModTime().UTC()
			f.Size, f.ModifiedAt = info.Size(), &at
		}
		s.Files = append(s.Files, f)
	}
	return nil
}

// readCommits reads how the commits of s differ from its base.
func readCommits(ctx context.Context, s *Status) error {
	s.Base, s.Ahead, s.Added, s.Removed, s.Committed = "", nil, nil, nil, nil
	if s.Missing || s.Head == "" {
		return nil
	}
	base, err := git.BaseOf(ctx, s.Path, s.Branch)
	if err != nil {
		return err
	}
	s.Base = base
	if base == "" {
		return nil
	}
	sum, err := git.Summarize(ctx, s.Path, base)
	if err != nil {
		if _, ok := errors.AsType[*git.ReadError](err); ok {
			logging.FromContext(ctx).Info("commits of a worktree not known", "path", s.Path, "base", base, "err", err)
			return nil
		}
		return err
	}
	s.Ahead = &sum.Ahead
	if sum.Lines {
		s.Added, s.Removed = &sum.Added, &sum.Removed
	}
	files, err := git.CommittedFiles(ctx, s.Path, base)
	if err != nil {
		if _, ok := errors.AsType[*git.ReadError](err); ok {
			logging.FromContext(ctx).Info("committed files of a worktree not known", "path", s.Path, "base", base, "err", err)
			s.Ahead = nil // not all is known, so no mark may go by it
			return nil
		}
		return err
	}
	for _, f := range files {
		s.Committed = append(s.Committed, CommittedFile{Path: f.Path, Status: f.Status})
	}
	return nil
}

// Find returns the checkout called name, on disk.
func Find(ctx context.Context, c Checkouts, name string) (Status, error) {
	list, err := List(ctx, c)
	if err != nil {
		return Status{}, err
	}
	i := slices.IndexFunc(list, func(s Status) bool { return s.Name == name })
	if i < 0 || list[i].Missing {
		return Status{}, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return list[i], nil
}

// FileDiff returns the uncommitted changes of the file at path in the checkout
// called name, with every line of the file.
func FileDiff(ctx context.Context, c Checkouts, name, path string) (git.Patch, error) {
	if !filepath.IsLocal(path) {
		return git.Patch{}, &InvalidPathError{Path: path}
	}
	s, err := Find(ctx, c, name)
	if err != nil {
		return git.Patch{}, err
	}
	return git.FileDiff(ctx, s.Path, path)
}

// UncommittedDiff returns everything not committed in the checkout called name, as
// one patch of the changes only.
func UncommittedDiff(ctx context.Context, c Checkouts, name string) (Committed, error) {
	s, err := Find(ctx, c, name)
	if err != nil {
		return Committed{}, err
	}
	r := Committed{Status: s}
	r.Patch, err = git.UncommittedDiff(ctx, s.Path)
	return r, err
}

// Committed is a checkout with a patch of it: for what is committed, since it left
// its base, with MergeBase saying where that was.
type Committed struct {
	Status    Status
	MergeBase string // "" when it shares no history with its base
	Patch     git.Patch
}

// CommittedDiff returns what the checkout called name has committed since it left
// its base, as a PR would show it.
func CommittedDiff(ctx context.Context, c Checkouts, name string) (Committed, error) {
	s, err := Find(ctx, c, name)
	if err != nil {
		return Committed{}, err
	}
	if err := readCommits(ctx, &s); err != nil {
		return Committed{}, err
	}
	r := Committed{Status: s}
	if s.Base == "" {
		return r, nil
	}
	if r.MergeBase, err = git.MergeBase(ctx, s.Path, s.Base); err != nil || r.MergeBase == "" {
		return r, err
	}
	r.Patch, err = git.RangeDiff(ctx, s.Path, s.Base)
	return r, err
}
