package daemon

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"sync/atomic"

	"github.com/srjn45/wdx/internal/doc"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/project"
)

// gitCopy keeps each project's docs written into the directory docs.git_path
// names in its repository, when it names one. Its failures are logged and fail
// nothing: the database holds the docs.
type gitCopy struct {
	path     atomic.Pointer[string]
	projects *project.Store
	docs     *doc.Store
}

func newGitCopy(projects *project.Store, docs *doc.Store, path string) *gitCopy {
	g := &gitCopy{projects: projects, docs: docs}
	g.path.Store(&path)
	return g
}

// setPath makes path the directory, and reports whether it changed.
func (g *gitCopy) setPath(path string) bool {
	return *g.path.Swap(&path) != path
}

// syncProject writes the docs of the project with projectID, when a directory is
// set.
func (g *gitCopy) syncProject(ctx context.Context, projectID string) {
	path := *g.path.Load()
	if path == "" {
		return
	}
	log := logging.FromContext(ctx)
	p, err := g.projects.GetByID(ctx, projectID)
	if err != nil {
		log.Error("docs not synced to git", "project_id", projectID, "err", err)
		return
	}
	root, err := checkoutOf(p)
	if err != nil {
		log.Info("docs not synced to git: no checkout", "project_id", projectID, "path", root)
		return
	}
	if _, err := g.docs.SyncToGit(ctx, p.ID, root, path); err != nil {
		log.Error("docs not synced to git", "project_id", projectID, "err", err)
	}
}

// syncAll writes every project's docs, when a directory is set.
func (g *gitCopy) syncAll(ctx context.Context) {
	if *g.path.Load() == "" {
		return
	}
	list, err := g.projects.List(ctx, "")
	if err != nil {
		logging.FromContext(ctx).Error("docs not synced to git", "err", err)
		return
	}
	for _, p := range list {
		g.syncProject(ctx, p.ID)
	}
}

// checkoutOf is the project's main checkout, or a *project.CheckoutMissingError
// when it is not on disk.
func checkoutOf(p project.Project) (string, error) {
	root := p.ClonePath()
	if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) || root == "" {
		return root, &project.CheckoutMissingError{Project: p}
	} else if err != nil {
		return root, err
	}
	return root, nil
}
