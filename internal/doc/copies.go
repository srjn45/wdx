package doc

import (
	"context"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/atomicfile"
	"github.com/srjn45/wdx/internal/logging"
)

// copies keeps a read-only file of each doc's latest revision, to recover docs
// from by hand if the database is lost. The database stays the doc: a copy that
// fails to write is logged and fails nothing.
type copies struct {
	dir string
}

// path is where d's copy is: a directory per project, named so that the files
// still say which project each belongs to.
func (c copies) path(projectName, name, format string) string {
	return filepath.Join(c.projectDir(projectName), fileName(name, format))
}

// projectDir escapes the project's name as in a URL path, since a project's name
// may hold a / or be .., and a leading . too, so it is never hidden.
func (c copies) projectDir(projectName string) string {
	escaped := url.PathEscape(projectName)
	if strings.HasPrefix(escaped, ".") {
		escaped = "%2E" + escaped[1:]
	}
	return filepath.Join(c.dir, escaped)
}

// write puts content in d's copy, its modified time set to when the revision was
// made, which is how catchUp knows the copy is current.
func (c copies) write(ctx context.Context, d Doc, content []byte, made time.Time) {
	path := c.path(d.ProjectName, d.Name, d.Format)
	err := os.MkdirAll(filepath.Dir(path), 0o700)
	if err == nil {
		err = atomicfile.Write(path, content, 0o444)
	}
	if err == nil {
		err = os.Chtimes(path, made, made)
	}
	if err != nil {
		logging.FromContext(ctx).Error("doc copy not written", "doc_id", d.ID, "path", path, "err", err)
		return
	}
	logging.FromContext(ctx).Info("doc copy written", "doc_id", d.ID, "path", path, "revision", d.Revision)
}

// rename moves d's copy to the name name. A rename, not a write and a removal: on
// a filesystem that ignores case, auth.md and Auth.md are one file.
func (c copies) rename(ctx context.Context, d Doc, name string) {
	from, to := c.path(d.ProjectName, d.Name, d.Format), c.path(d.ProjectName, name, d.Format)
	if err := os.Rename(from, to); err != nil {
		logging.FromContext(ctx).Error("doc copy not renamed", "doc_id", d.ID, "path", from, "to", to, "err", err)
		return
	}
	logging.FromContext(ctx).Info("doc copy renamed", "doc_id", d.ID, "path", from, "to", to)
}

func (c copies) remove(ctx context.Context, d Doc) {
	path := c.path(d.ProjectName, d.Name, d.Format)
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		logging.FromContext(ctx).Error("doc copy not removed", "doc_id", d.ID, "path", path, "err", err)
		return
	}
	logging.FromContext(ctx).Info("doc copy removed", "doc_id", d.ID, "path", path)
}

func (c copies) removeProject(ctx context.Context, projectName string) {
	dir := c.projectDir(projectName)
	if err := os.RemoveAll(dir); err != nil {
		logging.FromContext(ctx).Error("doc copies not removed", "path", dir, "err", err)
		return
	}
	logging.FromContext(ctx).Info("doc copies removed", "path", dir)
}

// current reports whether the copy at path is of the revision made at made.
func current(path string, made time.Time) bool {
	info, err := os.Stat(path)
	return err == nil && info.ModTime().UnixMilli() == made.UnixMilli()
}
