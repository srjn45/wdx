package worktree

import (
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
	p.tabFiles, p.tabWorktrees = cleanSet(files), cleanSet(worktrees)
	// Their parents and no other directory. A parent not there is not watched: a
	// tab of a file in it is missing until something else changes.
	p.tabWatches = map[string]bool{}
	for path := range p.tabFiles {
		p.tabWatches[filepath.Dir(path)] = true
	}
	for path := range p.tabWorktrees {
		p.tabWatches[filepath.Dir(path)] = true
	}
	p.tabsMu.Unlock()
	p.syncWatchesWith()
}

func cleanSet(paths []string) map[string]bool {
	set := make(map[string]bool, len(paths))
	for _, path := range paths {
		set[filepath.Clean(path)] = true
	}
	return set
}

// tabEvent tells of a tab's path that came or went. A write is not told: it
// changes nothing a tab says.
func (p *tracked) tabEvent(e fsnotify.Event) {
	if !e.Has(fsnotify.Create) && !e.Has(fsnotify.Remove) && !e.Has(fsnotify.Rename) {
		return
	}
	p.tabsMu.Lock()
	file, worktree := p.tabFiles[e.Name], p.tabWorktrees[e.Name]
	p.tabsMu.Unlock()
	switch {
	case worktree:
		logging.FromContext(p.ctx).Debug("worktree of a tab came or went", "path", e.Name, "op", e.Op.String())
		p.nudge() // every read lists the checkouts again
	case file:
		logging.FromContext(p.ctx).Debug("file of a tab came or went", "path", e.Name, "op", e.Op.String())
		p.t.opts.Changed()
	}
}
