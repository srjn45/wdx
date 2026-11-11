package worktree

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/srjn45/wdx/internal/logging"
)

// ExistsError is a path something is already at.
type ExistsError struct {
	Path string
}

func (e *ExistsError) Error() string {
	return fmt.Sprintf("%q already exists", e.Path)
}

// InvalidNameError is a name a file or directory cannot be given.
type InvalidNameError struct {
	Name string
}

func (e *InvalidNameError) Error() string {
	return fmt.Sprintf("%q cannot name a file or directory", e.Name)
}

// checkName refuses a name that is not one entry of a directory, and .git, which
// would be taken for the repository's own.
func checkName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") || strings.EqualFold(name, ".git") {
		return &InvalidNameError{Name: name}
	}
	return nil
}

// entry returns the checkout called name and where path is in it: its directory,
// with its symbolic links followed and inside the checkout, and its last name, not
// followed. path is never the checkout itself, nor in .git.
func entry(ctx context.Context, c Checkouts, name, path string) (s Status, dir, base string, err error) {
	s, err = findFile(ctx, c, name, path)
	if err != nil {
		return Status{}, "", "", err
	}
	clean := filepath.Clean(path)
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if part == "." || strings.EqualFold(part, ".git") {
			return Status{}, "", "", &InvalidPathError{Path: path}
		}
	}
	dir, err = resolveInside(s.Path, filepath.Dir(clean))
	if err != nil {
		return Status{}, "", "", err
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return Status{}, "", "", gone(fs.ErrNotExist, filepath.Dir(clean))
	}
	return s, dir, filepath.Base(clean), nil
}

// CreateEntry creates an empty file, or a directory when directory, at path in the
// checkout called name, whose directory must be there. It returns path cleaned.
func CreateEntry(ctx context.Context, c Checkouts, name, path string, directory bool) (string, error) {
	_, dir, base, err := entry(ctx, c, name, path)
	if err != nil {
		return "", err
	}
	if err := checkName(base); err != nil {
		return "", err
	}
	full := filepath.Join(dir, base)
	if directory {
		err = os.Mkdir(full, 0o777) //nolint:gosec // a directory the user makes in their checkout, under their umask
	} else {
		var f *os.File
		if f, err = os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666); err == nil { //nolint:gosec // a file the user makes in their checkout, under their umask
			err = f.Close()
		}
	}
	clean := filepath.Clean(path)
	if errors.Is(err, fs.ErrExist) {
		return "", &ExistsError{Path: clean}
	}
	if err != nil {
		return "", err
	}
	logging.FromContext(ctx).Info("worktree entry created", "worktree", name, "path", clean, "directory", directory)
	return clean, nil
}

// RenameEntry gives the file or directory at path in the checkout called name the
// name newName, in the same directory. It returns the new path.
func RenameEntry(ctx context.Context, c Checkouts, name, path, newName string) (string, error) {
	_, dir, base, err := entry(ctx, c, name, path)
	if err != nil {
		return "", err
	}
	if err := checkName(newName); err != nil {
		return "", err
	}
	from, to := filepath.Join(dir, base), filepath.Join(dir, newName)
	old, err := os.Lstat(from)
	if err != nil {
		return "", gone(err, path)
	}
	newPath := filepath.Join(filepath.Dir(filepath.Clean(path)), newName)
	// The same entry where names differ only in case, on a file system that does not
	// tell them apart, is renamed.
	if there, err := os.Lstat(to); err == nil && !os.SameFile(old, there) {
		return "", &ExistsError{Path: newPath}
	}
	if err := os.Rename(from, to); err != nil {
		return "", err
	}
	logging.FromContext(ctx).Info("worktree entry renamed", "worktree", name, "path", filepath.Clean(path), "new_path", newPath)
	return newPath, nil
}

// RemoveEntry removes the file, or the directory with all it holds, at path in the
// checkout called name. A symbolic link is removed, not what it leads to.
func RemoveEntry(ctx context.Context, c Checkouts, name, path string) error {
	_, dir, base, err := entry(ctx, c, name, path)
	if err != nil {
		return err
	}
	full := filepath.Join(dir, base)
	info, err := os.Lstat(full)
	if err != nil {
		return gone(err, path)
	}
	if info.IsDir() {
		err = os.RemoveAll(full)
	} else {
		err = os.Remove(full)
	}
	if err != nil {
		return err
	}
	logging.FromContext(ctx).Info("worktree entry removed", "worktree", name, "path", filepath.Clean(path), "directory", info.IsDir())
	return nil
}
