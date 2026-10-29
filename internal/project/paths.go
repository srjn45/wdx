package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/logging"
)

// within reports whether one of the forms of a path is root or below it.
func within(root string, forms []string) bool {
	return slices.ContainsFunc(forms, func(path string) bool {
		rel, err := filepath.Rel(root, path)
		return err == nil && filepath.IsLocal(rel)
	})
}

// pathForms returns path cleaned and, when it differs, with the symlinks in
// its deepest existing ancestor resolved, as git resolves them in the
// worktree paths it reports; the rest of path may not exist yet.
func pathForms(path string) []string {
	path = filepath.Clean(path)
	forms := []string{path}
	ancestor, err := existingAncestor(path)
	if err != nil {
		return forms
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return forms
	}
	rest, _ := filepath.Rel(ancestor, path)
	if resolved = filepath.Join(resolved, rest); resolved != path {
		forms = append(forms, resolved)
	}
	return forms
}

// existingAncestor returns path or its nearest ancestor that exists, which
// must be a directory.
func existingAncestor(path string) (string, error) {
	for {
		info, err := os.Stat(path)
		if err == nil {
			if !info.IsDir() {
				return "", &InvalidPathError{Path: path, Reason: "not a directory"}
			}
			return path, nil
		}
		// ENOTDIR: an ancestor is a file, which the loop reaches and reports.
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		path = parent
	}
}

// isCheckout reports whether path is still the root of a git work tree.
func isCheckout(ctx context.Context, path string) (bool, error) {
	root, err := git.TopLevel(ctx, path)
	if errors.Is(err, git.ErrNotRepo) {
		logging.FromContext(ctx).Info("checkout checked", "path", path, "present", false)
		return false, nil
	}
	if err != nil {
		return false, err
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, err
	}
	logging.FromContext(ctx).Info("checkout checked", "path", path, "root", root, "present", root == real)
	return root == real, nil
}

// checkDirName fails if name cannot be used as a directory name.
func checkDirName(name string) error {
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return &InvalidNameError{Name: name, Reason: "cannot be used as a directory name"}
	}
	return nil
}
