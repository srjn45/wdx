package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/srjn45/wdx/internal/git"
)

// maxFileContent is the most of a file ReadFile gives.
const maxFileContent = 2 << 20

// NotAFileError is a path that leads to something other than a regular file.
type NotAFileError struct {
	Path string
}

func (e *NotAFileError) Error() string {
	return fmt.Sprintf("%q is not a file", e.Path)
}

// Files are the files of a checkout, as git.ListFiles gives them, and its
// directories that hold none, which git does not list: empty ones, not
// gitignored.
type Files struct {
	Paths     []string
	EmptyDirs []string
}

// ListFiles returns the files of the checkout called name.
func ListFiles(ctx context.Context, c Checkouts, name string) (Files, error) {
	s, err := Find(ctx, c, name)
	if err != nil {
		return Files{}, err
	}
	paths, dirs, err := git.ListFiles(ctx, s.Path)
	if err != nil {
		return Files{}, err
	}
	f := Files{Paths: paths, EmptyDirs: []string{}}
	for _, d := range dirs {
		// One listed with files holds untracked ones; a nested repository is no
		// directory of this one's.
		i, _ := slices.BinarySearch(paths, d+"/")
		holds := i < len(paths) && strings.HasPrefix(paths[i], d+"/")
		if _, err := os.Lstat(filepath.Join(s.Path, d, ".git")); !holds && err != nil {
			f.EmptyDirs = append(f.EmptyDirs, d)
		}
	}
	return f, nil
}

// FileContent is what a file holds. Text is empty when Binary or TooLarge.
type FileContent struct {
	Text     string
	Binary   bool
	TooLarge bool
	Size     int64
}

// ReadFile returns what the file at path in the checkout called name holds on disk.
// A symbolic link is followed only as far as the checkout goes.
func ReadFile(ctx context.Context, c Checkouts, name, path string) (FileContent, error) {
	_, full, info, err := regularFile(ctx, c, name, path)
	if err != nil {
		return FileContent{}, err
	}
	r := FileContent{Size: info.Size()}
	if r.Size > maxFileContent {
		r.TooLarge = true
		return r, nil
	}
	f, err := os.Open(full)
	if err != nil {
		return FileContent{}, gone(err, path)
	}
	defer func() { _ = f.Close() }()
	// It may have grown since, and an agent may be writing it.
	data, err := io.ReadAll(io.LimitReader(f, maxFileContent+1))
	if err != nil {
		return FileContent{}, err
	}
	switch {
	case len(data) > maxFileContent:
		r.TooLarge = true
	case bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data):
		r.Binary = true
	default:
		r.Text = string(data)
	}
	return r, nil
}

// EditableFile returns the directory of the checkout called name, and path
// cleaned, when path there is a regular file inside it, for an editor to run on.
func EditableFile(ctx context.Context, c Checkouts, name, path string) (dir, clean string, err error) {
	s, _, _, err := regularFile(ctx, c, name, path)
	if err != nil {
		return "", "", err
	}
	return s.Path, filepath.Clean(path), nil
}

// regularFile returns the checkout called name, and the file at path in it with its
// symbolic links followed, when that is a regular file inside the checkout.
func regularFile(ctx context.Context, c Checkouts, name, path string) (Status, string, os.FileInfo, error) {
	s, err := findFile(ctx, c, name, path)
	if err != nil {
		return Status{}, "", nil, err
	}
	full, err := resolveInside(s.Path, path)
	if err != nil {
		return Status{}, "", nil, err
	}
	// Asked before opening it, as opening a named pipe waits for a writer.
	info, err := os.Stat(full)
	if err != nil {
		return Status{}, "", nil, gone(err, path)
	}
	if !info.Mode().IsRegular() {
		return Status{}, "", nil, &NotAFileError{Path: path}
	}
	return s, full, info, nil
}

// resolveInside returns the path with its symbolic links followed, which must end
// inside the checkout at root.
func resolveInside(root, path string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, path))
	if err != nil {
		return "", gone(err, path)
	}
	rel, err := filepath.Rel(realRoot, full)
	if err != nil || !filepath.IsLocal(rel) {
		return "", &InvalidPathError{Path: path}
	}
	return full, nil
}

// gone says a file is not there, as much when a directory on its way became a file.
func gone(err error, path string) error {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	return err
}
