package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"

	"github.com/srjn45/wdx/internal/logging"
)

// WatchTabs watches what the project's tabs show, while the project is followed:
// the files of file tabs and the directories of the worktrees tabs name, as
// absolute paths. The watches on git say nothing of a file removed outside git,
// nor of a worktree's directory removed without it. One of those files created,
// removed or renamed calls Changed, which has the streams look at the disk again;
// a worktree's directory has the checkouts read again, which finds it missing.
// Each call replaces the paths of the one before.
func (t *Tracker) WatchTabs(projectID string, files, worktrees []string) {
	t.mu.Lock()
	p, ok := t.projects[projectID]
	t.mu.Unlock()
	if !ok {
		return
	}
	p.tabsMu.Lock()
	defer p.tabsMu.Unlock()
	p.tabFiles, p.tabWorktrees = cleanSet(files), cleanSet(worktrees)
	p.syncTabWatches(p.t.ctx)
}

func cleanSet(paths []string) map[string]bool {
	set := make(map[string]bool, len(paths))
	for _, path := range paths {
		set[filepath.Clean(path)] = true
	}
	return set
}

// startTabWatch watches the tabs' paths until the project is left, from before its
// first read: what WatchTabs is given after that read is watched at once. Their
// parent directories are watched, not the paths, so that one coming back is seen:
// one watch for each directory, whatever the number of tabs in it.
func (p *tracked) startTabWatch(ctx context.Context) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logging.FromContext(ctx).Info("tabs not watched", "err", err)
		return
	}
	p.tabsMu.Lock()
	p.tabWatcher = watcher
	p.syncTabWatches(ctx)
	p.tabsMu.Unlock()
	p.t.wg.Go(func() { p.watchTabs(ctx, watcher) })
}

func (p *tracked) watchTabs(ctx context.Context, watcher *fsnotify.Watcher) {
	log := logging.FromContext(ctx)
	defer func() {
		p.tabsMu.Lock()
		p.tabWatcher = nil
		p.tabsMu.Unlock()
		_ = watcher.Close()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-watcher.Events:
			if !ok {
				return
			}
			p.tabEvent(ctx, e)
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Info("tab watch failed", "err", err)
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				p.t.opts.Changed() // events were lost
				p.nudge()
			}
		}
	}
}

// syncTabWatches watches the parents of the tabs' paths and no other directory. A
// parent not there is not watched: a tab of a file in it is missing until something
// else changes. p.tabsMu is held.
func (p *tracked) syncTabWatches(ctx context.Context) {
	if p.tabWatcher == nil {
		return
	}
	want := map[string]bool{}
	for path := range p.tabFiles {
		want[filepath.Dir(path)] = true
	}
	for path := range p.tabWorktrees {
		want[filepath.Dir(path)] = true
	}
	for _, have := range p.tabWatcher.WatchList() {
		if !want[have] {
			_ = p.tabWatcher.Remove(have) // a directory gone took its watch with it
		}
	}
	for dir := range want {
		if err := p.tabWatcher.Add(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
			logging.FromContext(ctx).Info("directory of tabs not watched", "dir", dir, "err", err)
		}
	}
}

// tabEvent tells of a tab's path that came or went. A write is not told: it
// changes nothing a tab says.
func (p *tracked) tabEvent(ctx context.Context, e fsnotify.Event) {
	if !e.Has(fsnotify.Create) && !e.Has(fsnotify.Remove) && !e.Has(fsnotify.Rename) {
		return
	}
	p.tabsMu.Lock()
	file, worktree := p.tabFiles[e.Name], p.tabWorktrees[e.Name]
	p.tabsMu.Unlock()
	switch {
	case worktree:
		logging.FromContext(ctx).Debug("worktree of a tab came or went", "path", e.Name, "op", e.Op.String())
		p.nudge() // every read lists the checkouts again
	case file:
		logging.FromContext(ctx).Debug("file of a tab came or went", "path", e.Name, "op", e.Op.String())
		p.t.opts.Changed()
	}
}
