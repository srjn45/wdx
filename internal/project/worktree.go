package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/logging"
)

// mainWorktree names the project's clone, among its worktrees.
const mainWorktree = "main"

// worktreesDir holds the worktrees wdx creates, in the project's root.
const worktreesDir = ".worktrees"

// WorktreeNamed returns the path of the worktree of p called name, created
// when there is none: "main" is the clone, and any other name matches the base
// name of a worktree's path. A new one goes in <root>/.worktrees/<name> on a
// new branch of that name, from the HEAD of the checkout holding from.
func (s *Store) WorktreeNamed(ctx context.Context, p Project, name, from string) (path string, created bool, err error) {
	log := logging.FromContext(ctx).With("project_id", p.ID, "worktree", name)
	if err := checkWorktreeName(ctx, from, name); err != nil {
		return "", false, err
	}
	if name == mainWorktree {
		log.Info("worktree is the clone", "path", p.ClonePath())
		return p.ClonePath(), false, nil
	}
	root := p.root()
	path = filepath.Join(root, worktreesDir, name)
	if known, ok := p.worktreeNamed(name, path); ok {
		log.Info("worktree found", "path", known)
		return known, false, nil
	}
	if _, err := os.Stat(path); err == nil {
		return s.adoptWorktree(ctx, p, path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	exists, err := git.BranchExists(ctx, from, name)
	if err != nil {
		return "", false, err
	}
	if exists {
		return "", false, &BranchExistsError{Branch: name}
	}
	if err := excludeWorktrees(ctx, root); err != nil {
		return "", false, err
	}
	if err := git.AddWorktree(ctx, from, path, name); err != nil {
		return "", false, err
	}
	if path, err = s.recordWorktree(ctx, p.ID, path); err != nil {
		return "", false, err
	}
	return path, true, nil
}

// checkWorktreeName refuses what cannot name a worktree and its branch. One
// path segment, so that the name is the base name of its path.
func checkWorktreeName(ctx context.Context, dir, name string) error {
	if name == "" {
		return &InvalidWorktreeNameError{Name: name, Reason: "empty"}
	}
	if strings.ContainsAny(name, "/:") {
		return &InvalidWorktreeNameError{Name: name, Reason: `cannot contain "/" or ":"`}
	}
	valid, err := git.ValidBranch(ctx, dir, name)
	if err != nil {
		return err
	}
	if !valid {
		return &InvalidWorktreeNameError{Name: name, Reason: "git does not accept it as a branch name"}
	}
	return nil
}

// root returns where the project's .worktrees goes: the checkout holding the
// .git directory its worktrees share, though the project may not know it.
func (p Project) root() string {
	for _, w := range p.Worktrees {
		if filepath.Base(w.GitCommonDir) == ".git" {
			return filepath.Dir(w.GitCommonDir)
		}
	}
	return p.ClonePath()
}

// worktreeNamed returns the path of the worktree called name, the one at
// preferred if several are.
func (p Project) worktreeNamed(name, preferred string) (string, bool) {
	found := ""
	for _, w := range p.Worktrees {
		if w.Path == preferred {
			return w.Path, true
		}
		if found == "" && filepath.Base(w.Path) == name {
			found = w.Path
		}
	}
	return found, found != ""
}

// adoptWorktree records the directory at path, the place of a worktree the
// project does not know, if it is a worktree of the project's repository.
func (s *Store) adoptWorktree(ctx context.Context, p Project, path string) (string, bool, error) {
	checkout, err := isCheckout(ctx, path)
	if err != nil {
		return "", false, err
	}
	if !checkout {
		return "", false, &PathExistsError{Path: path}
	}
	commonDir, err := git.CommonDir(ctx, path)
	if err != nil {
		return "", false, err
	}
	if commonDir != filepath.Join(p.root(), ".git") {
		return "", false, &PathExistsError{Path: path}
	}
	recorded, err := s.recordWorktree(ctx, p.ID, path)
	return recorded, false, err
}

// recordWorktree adds the checkout at path to the project, as git reports it.
func (s *Store) recordWorktree(ctx context.Context, projectID, path string) (string, error) {
	root, err := git.TopLevel(ctx, path)
	if err != nil {
		return "", err
	}
	commonDir, err := git.CommonDir(ctx, root)
	if err != nil {
		return "", err
	}
	id, err := db.NewID("wt")
	if err != nil {
		return "", err
	}
	if _, err := db.Exec(ctx, s.db, `
		INSERT INTO project_worktrees (id, project_id, path, git_common_dir, created_at)
		VALUES (?, ?, ?, ?, ?)`, id, projectID, root, commonDir, time.Now().UnixMilli()); err != nil {
		return "", err
	}
	logging.FromContext(ctx).Info("worktree recorded", "project_id", projectID, "path", root, "git_common_dir", commonDir)
	return root, nil
}

// excludeWorktrees makes git ignore .worktrees in the repository at root,
// unless it already does, in a file of the repository's own that is not
// tracked.
func excludeWorktrees(ctx context.Context, root string) error {
	ignored, err := git.Ignored(ctx, root, worktreesDir+"/")
	if err != nil || ignored {
		return err
	}
	commonDir, err := git.CommonDir(ctx, root)
	if err != nil {
		return err
	}
	exclude := filepath.Join(commonDir, "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	line := worktreesDir + "/\n"
	if info.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, info.Size()-1); err != nil {
			return err
		}
		if last[0] != '\n' {
			line = "\n" + line
		}
	}
	if _, err := f.WriteString(line); err != nil {
		return fmt.Errorf("write %s: %w", exclude, err)
	}
	logging.FromContext(ctx).Info("worktrees excluded from git", "path", exclude)
	return nil
}
