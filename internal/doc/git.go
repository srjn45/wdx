package doc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/srjn45/wdx/internal/atomicfile"
	"github.com/srjn45/wdx/internal/logging"
)

// ownedList names, in a git copy's directory, the file listing what wdx wrote
// there, so that a file a person put there is never touched.
const ownedList = ".wdx-docs"

// ownedHeader starts the list, for whoever opens it.
const ownedHeader = "# The docs wdx doc sync-to-git wrote here, from wdx; it rewrites them and nothing else.\n"

// InvalidPathError is returned for a git copy's directory that is not one inside
// the repository.
type InvalidPathError struct {
	Path   string
	Reason string
}

func (e *InvalidPathError) Error() string {
	return fmt.Sprintf("invalid path %q: %s", e.Path, e.Reason)
}

// Synced is what a sync to git did: the directory, and the files written, removed,
// and skipped as a file wdx did not write already has their name.
type Synced struct {
	Path    string
	Written []string
	Removed []string
	Skipped []string
}

// SyncToGit writes the latest revision of every doc of the project with projectID,
// read-only, into dir, a directory of the checkout at root. Files it wrote there
// before whose docs are gone are removed, and only those.
func (s *Store) SyncToGit(ctx context.Context, projectID, root, dir string) (Synced, error) {
	if s.kind != KindDoc {
		return Synced{}, fmt.Errorf("only docs are synced to git, not %ss", s.kind)
	}
	target, err := gitCopyDir(root, dir)
	if err != nil {
		return Synced{}, err
	}
	docs, err := s.query(ctx, s.db, "d.project_id = ?", projectID)
	if err != nil {
		return Synced{}, err
	}
	if err := os.MkdirAll(target, 0o755); err != nil { //nolint:gosec // a directory in the user's repository, as git makes them
		return Synced{}, err
	}
	log := logging.FromContext(ctx).With("path", target)
	owned, err := readOwned(target)
	if err != nil {
		return Synced{}, err
	}
	files := map[string]Doc{}
	var names []string
	for _, d := range docs {
		name := fileName(d.Name, d.Format)
		files[name] = d
		names = append(names, name)
	}
	slices.Sort(names)

	r := Synced{Path: target, Written: []string{}, Removed: []string{}, Skipped: []string{}}
	// Removed first: where case is ignored, writing Auth.md and then removing a
	// renamed auth.md would delete the one just written.
	for _, name := range owned {
		if _, ok := files[name]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(target, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return r, err
		}
		log.Info("doc removed from git copy", "file", name)
		r.Removed = append(r.Removed, name)
	}
	kept := slices.DeleteFunc(slices.Clone(owned), func(name string) bool { return slices.Contains(r.Removed, name) })
	// The list is written whatever happens next, so that a file written is never
	// later taken for a person's.
	defer func() {
		list := slices.Compact(slices.Sorted(slices.Values(append(kept, r.Written...))))
		if werr := writeOwned(target, list); werr != nil {
			log.Error("git copy list not written", "err", werr)
		}
	}()
	for _, name := range names {
		d := files[name]
		path := filepath.Join(target, name)
		if !slices.Contains(owned, name) {
			if _, err := os.Lstat(path); err == nil {
				log.Info("doc skipped in git copy: a file wdx did not write has its name", "doc_id", d.ID, "file", name)
				r.Skipped = append(r.Skipped, name)
				continue
			}
		}
		content, _, err := s.Content(ctx, d.ID, d.Revision)
		if err != nil {
			return r, err
		}
		if err := atomicfile.Write(path, content, 0o444); err != nil {
			return r, err
		}
		log.Info("doc written to git copy", "doc_id", d.ID, "file", name, "revision", d.Revision)
		r.Written = append(r.Written, name)
	}
	log.Info("docs synced to git", "project_id", projectID, "written", len(r.Written), "removed", len(r.Removed), "skipped", len(r.Skipped))
	return r, nil
}

// gitCopyDir is dir in the checkout at root, or an *InvalidPathError when it is not
// a directory inside it.
func gitCopyDir(root, dir string) (string, error) {
	if err := CheckGitPath(dir); err != nil {
		return "", err
	}
	target := filepath.Join(root, dir)
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	// A symlink in what exists of the path can lead out of the checkout.
	existing := target
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		existing = filepath.Dir(existing)
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(realRoot, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", &InvalidPathError{Path: dir, Reason: "a symlink leads out of the repository"}
	}
	if info, err := os.Stat(existing); err == nil && !info.IsDir() {
		return "", &InvalidPathError{Path: dir, Reason: existing + " is not a directory"}
	}
	return target, nil
}

// CheckGitPath fails for a git copy's directory that is not relative, inside the
// repository and outside .git, as far as the path alone says.
func CheckGitPath(dir string) error {
	clean := filepath.Clean(dir)
	switch {
	case dir == "":
		return &InvalidPathError{Path: dir, Reason: "it is empty"}
	case filepath.IsAbs(dir):
		return &InvalidPathError{Path: dir, Reason: "it must be relative to the repository"}
	case clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)):
		return &InvalidPathError{Path: dir, Reason: "it leads out of the repository"}
	case clean == ".git" || strings.HasPrefix(clean, ".git"+string(filepath.Separator)):
		return &InvalidPathError{Path: dir, Reason: "it is in git's own directory"}
	}
	return nil
}

// readOwned returns the files the list in dir names; none when there is no list.
func readOwned(dir string) ([]string, error) {
	f, err := os.Open(filepath.Join(dir, ownedList))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var names []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// A name with a separator would remove a file outside the directory.
		if line == "" || strings.HasPrefix(line, "#") || strings.ContainsRune(line, filepath.Separator) {
			continue
		}
		names = append(names, line)
	}
	return names, scanner.Err()
}

func writeOwned(dir string, names []string) error {
	var b strings.Builder
	b.WriteString(ownedHeader)
	for _, name := range names {
		b.WriteString(name + "\n")
	}
	return atomicfile.Write(filepath.Join(dir, ownedList), []byte(b.String()), 0o444)
}

// fileName is the name of a doc's file: its name and format.
func fileName(name, format string) string {
	if format == "" {
		return name
	}
	return name + "." + format
}
