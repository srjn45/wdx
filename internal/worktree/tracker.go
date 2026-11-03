package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/logging"
)

// DefaultSettle is how long the tracker lets events gather before it reads:
// a commit changes several files one after the other.
const DefaultSettle = 200 * time.Millisecond

// Agent is what the tracker needs of an agent.
type Agent struct {
	ID      string
	Dir     string
	Working bool
}

// Options are what the tracker needs from the daemon.
type Options struct {
	// Checkouts returns the checkouts the project knows of.
	Checkouts func(ctx context.Context, projectID string) (Checkouts, error)
	// Agents returns the project's agents.
	Agents func(ctx context.Context, projectID string) ([]Agent, error)
	// Marks are the files marked verified; nil keeps none.
	Marks *Marks
	// Comments are the comments on the diffs; nil keeps none.
	Comments *Comments
	// Changed is called when what is kept of a project changed.
	Changed func()
	// Settle is DefaultSettle when 0.
	Settle time.Duration

	// listed is called when a read has listed the checkouts and knows what it is to
	// read, before it reads each: a test holds that step open to change one between.
	listed func()
	// reading is called as a read starts on the checkout at path: a test holds one
	// open to see the others read meanwhile.
	reading func(path string)
	// readDone is called when a read has read the checkouts, before it last tells
	// of them: a test holds that step open to act during a read.
	readDone func()
}

// Tracker keeps the status of the checkouts of the projects being followed. No
// timer runs git: it reads when an event says something changed, and never for a
// project nobody follows (docs/TIDE_WORKTREES.md, "The events").
type Tracker struct {
	ctx  context.Context
	opts Options
	wg   sync.WaitGroup

	mu       sync.Mutex
	projects map[string]*tracked
}

// NewTracker returns a tracker working until ctx ends.
func NewTracker(ctx context.Context, opts Options) *Tracker {
	if opts.Settle == 0 {
		opts.Settle = DefaultSettle
	}
	return &Tracker{ctx: ctx, opts: opts, projects: map[string]*tracked{}}
}

// Wait waits for the tracker's goroutines, once its context ended and nobody
// follows.
func (t *Tracker) Wait() { t.wg.Wait() }

// Follow keeps the project's status until the function it returns is called, once
// for each Follow. Everything kept and watched goes with the last.
func (t *Tracker) Follow(projectID string) (stop func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.projects[projectID]
	if !ok {
		ctx, cancel := context.WithCancel(t.ctx)
		p = &tracked{t: t, id: projectID, cancel: cancel, wake: make(chan struct{}, 1), agents: map[string]Agent{}, pending: map[string]reading{}, everything: true}
		t.projects[projectID] = p
		t.wg.Go(func() { p.run(ctx) })
		p.nudge()
	}
	p.followers++
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			if p.followers--; p.followers == 0 {
				p.cancel()
				delete(t.projects, projectID)
			}
		})
	}
}

// Statuses returns what is kept of the project: nil when it is not followed, or
// has not been read yet.
func (t *Tracker) Statuses(projectID string) []Status {
	t.mu.Lock()
	p, ok := t.projects[projectID]
	t.mu.Unlock()
	if !ok {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.statuses
}

// Refresh reads the checkout called name of the project again, or all of them
// when name is "", because a user asked: unlike the other events it does not wait
// for an index lock, which a read is safe beside and a crashed git may have left.
func (t *Tracker) Refresh(projectID, name string) {
	t.mu.Lock()
	p, ok := t.projects[projectID]
	t.mu.Unlock()
	if ok {
		p.refresh(name)
	}
}

// AgentsChanged is told that something changed under the projects: an agent that
// stopped working has its worktree read again.
func (t *Tracker) AgentsChanged() {
	t.mu.Lock()
	projects := make([]*tracked, 0, len(t.projects))
	for _, p := range t.projects {
		projects = append(projects, p)
	}
	t.mu.Unlock()
	for _, p := range projects {
		p.agentsChanged()
	}
}

// reading is what is to be read again of a checkout.
type reading struct {
	files, commits bool
}

func (r reading) with(o reading) reading {
	return reading{files: r.files || o.files, commits: r.commits || o.commits}
}

// tracked is a project being followed.
type tracked struct {
	t         *Tracker
	id        string
	cancel    context.CancelFunc
	wake      chan struct{} // one value: events while it reads make one more read
	followers int           // under t.mu

	mu         sync.Mutex
	statuses   []Status
	agents     map[string]Agent   // as last seen, those working
	agentDirs  map[string]string  // as last seen, where each is
	pending    map[string]reading // by path
	everything bool               // every checkout is to be read
	forced     bool               // a user asked: do not wait for a lock
	gitDirs    map[string]gitDirs // of each checkout, by path
	commonDirs map[string]bool
	bases      map[string]bool // the files that say a base moved: its ref and its reflog
	watching   bool            // the watcher works, so the end of a lock will be seen

	tabsMu       sync.Mutex
	tabWatcher   *fsnotify.Watcher // nil until watchTabs starts, and once it ended
	tabFiles     map[string]bool   // the files tabs show
	tabWorktrees map[string]bool   // the directories of the worktrees tabs name
}

func (p *tracked) nudge() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *tracked) mark(path string, r reading) {
	p.mu.Lock()
	p.pending[path] = p.pending[path].with(r)
	p.mu.Unlock()
	p.nudge()
}

func (p *tracked) markAll() {
	p.mu.Lock()
	p.everything = true
	p.mu.Unlock()
	p.nudge()
}

func (p *tracked) refresh(name string) {
	p.mu.Lock()
	p.forced = true
	found := false
	for _, s := range p.statuses {
		if s.Name == name {
			p.pending[s.Path] = reading{files: true, commits: true}
			found = true
		}
	}
	if !found {
		p.everything = true
	}
	p.mu.Unlock()
	p.nudge()
}

// agentsChanged marks the checkouts of the agents that were working and no longer
// are. Asked about every change, it only compares: an agent's keystrokes cost no git.
func (p *tracked) agentsChanged() {
	agents, err := p.t.opts.Agents(p.t.ctx, p.id)
	if err != nil {
		logging.FromContext(p.t.ctx).Info("agents not read for their worktrees", "project_id", p.id, "err", err)
		return
	}
	working := map[string]Agent{}
	dirs := map[string]string{}
	for _, a := range agents {
		dirs[a.ID] = a.Dir
		if a.Working {
			working[a.ID] = a
		}
	}
	p.mu.Lock()
	var stopped []Agent
	// One that moved is learned of after it stopped: where it is now is read.
	for _, a := range agents {
		if was, seen := p.agentDirs[a.ID]; seen && was != a.Dir {
			stopped = append(stopped, a)
		}
	}
	p.agentDirs = dirs
	// A review whose message went gives its comments back with nothing else to say so.
	drafts := p.draftsChanged()
	for id, a := range p.agents {
		if _, still := working[id]; !still {
			stopped = append(stopped, a)
		}
	}
	p.agents = working
	statuses := p.statuses
	p.mu.Unlock()
	for _, path := range drafts {
		p.mark(path, reading{files: true})
	}
	for _, a := range stopped {
		if path, ok := holding(statuses, a.Dir); ok {
			logging.FromContext(p.t.ctx).Debug("worktree to read", "path", path, "reason", "agent stopped working", "agent_id", a.ID)
			p.mark(path, reading{files: true, commits: true})
		}
	}
}

// draftsChanged returns the checkouts whose comments not sent are not those kept; it
// reads the table alone, and p.mu is held.
func (p *tracked) draftsChanged() []string {
	store := p.t.opts.Comments
	if store == nil {
		return nil
	}
	list, err := store.Drafts(p.t.ctx, p.id)
	if err != nil {
		return nil
	}
	now := map[string]int{}
	for _, c := range list {
		now[c.Worktree]++
	}
	var changed []string
	for _, s := range p.statuses {
		if now[s.Path] != len(s.Comments) {
			changed = append(changed, s.Path)
		}
	}
	return changed
}

// holding returns the checkout dir is in: the deepest, since worktrees are often
// inside the clone.
func holding(statuses []Status, dir string) (string, bool) {
	best := ""
	for _, s := range statuses {
		if (dir == s.Path || strings.HasPrefix(dir, s.Path+string(filepath.Separator))) && len(s.Path) > len(best) {
			best = s.Path
		}
	}
	return best, best != ""
}

func (p *tracked) run(ctx context.Context) {
	log := logging.FromContext(ctx).With("project_id", p.id)
	ctx = logging.NewContext(ctx, log)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		// Agents stopping and users looking still read it.
		log.Info("worktrees not watched", "err", err)
		watcher = nil
	}
	if watcher != nil {
		defer watcher.Close()
		p.mu.Lock()
		p.watching = true
		p.mu.Unlock()
		p.t.wg.Go(func() { p.watch(ctx, watcher) })
	}
	p.startTabWatch(ctx)
	p.agentsChanged() // what is working now is what may stop
	settle := time.NewTimer(p.t.opts.Settle)
	defer settle.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
		}
		settle.Reset(p.t.opts.Settle)
		select {
		case <-ctx.Done():
			return
		case <-settle.C:
		}
		p.read(ctx, watcher)
	}
}

// read reads what is pending, and tells of what differs, of each checkout as it is
// read. Up to parallelReads checkouts at once, from this goroutine alone, so that a
// project never runs two reads of one checkout at once.
func (p *tracked) read(ctx context.Context, watcher *fsnotify.Watcher) {
	log := logging.FromContext(ctx)
	c, err := p.t.opts.Checkouts(ctx, p.id)
	if err != nil {
		log.Info("checkouts not read", "err", err)
		return
	}
	list, err := List(ctx, c)
	if err != nil {
		log.Info("worktrees not listed", "err", err)
		return
	}

	p.mu.Lock()
	before := p.statuses
	p.mu.Unlock()
	known := make(map[string]Status, len(before))
	for _, s := range before {
		known[s.Path] = s
	}
	for i := range list {
		s := &list[i]
		if s.Missing {
			continue
		}
		was, seen := known[s.Path]
		s.Files, s.Base, s.Ahead, s.Added, s.Removed, s.Committed, s.Comments = was.Files, was.Base, was.Ahead, was.Added, was.Removed, was.Committed, was.Comments
		s.Unread = !seen || was.Unread
	}

	dirs := p.dirsOf(ctx, list)
	// Before reading, with the bases known so far: a change made during the read
	// must fire a read of its own.
	if watcher != nil {
		p.syncWatches(ctx, watcher, list, dirs)
	}
	p.mu.Lock()
	pending, everything, forced := p.pending, p.everything, p.forced
	p.pending, p.everything, p.forced = map[string]reading{}, false, false
	watching := p.watching
	p.gitDirs = dirs
	p.mu.Unlock()
	if p.t.opts.listed != nil {
		p.t.opts.listed()
	}

	// What is told: each checkout replaced in it as it is read. A base read for the
	// first time, or a new one, has its files watched before anyone is told of it,
	// or a move made right after would go unseen; one tell at a time, so that the
	// watches and what is kept agree.
	var told, telling sync.Mutex
	tell := func() {
		telling.Lock()
		defer telling.Unlock()
		told.Lock()
		now := slices.Clone(list)
		told.Unlock()
		if watcher != nil {
			p.syncWatches(ctx, watcher, now, dirs)
		}
		p.mu.Lock()
		changed := !reflect.DeepEqual(p.statuses, now)
		p.statuses = now
		p.mu.Unlock()
		if changed {
			p.t.opts.Changed()
		}
	}
	// The checkouts not read yet are told of at once, so that they show while the
	// others are read.
	if slices.ContainsFunc(list, func(s Status) bool { return s.Unread && !s.Missing }) {
		tell()
	}

	atOnce(ctx, len(list), func(i int) {
		told.Lock()
		s := list[i]
		told.Unlock()
		if s.Missing {
			return
		}
		was := known[s.Path]
		want := pending[s.Path]
		if everything || s.Unread || was.Head != s.Head || was.Branch != s.Branch {
			want = reading{files: true, commits: true}
		}
		if want == (reading{}) {
			return
		}
		// A command like a long pull holds the index: its end fires the watch.
		if d, ok := dirs[s.Path]; ok && watching && !forced && exists(filepath.Join(d.git, "index.lock")) {
			log.Debug("worktree read put off", "path", s.Path, "reason", "index locked")
			p.mu.Lock()
			p.pending[s.Path] = p.pending[s.Path].with(want)
			p.mu.Unlock()
			return
		}
		if p.t.opts.reading != nil {
			p.t.opts.reading(s.Path)
		}
		start := time.Now()
		if want.files {
			if err := readFiles(ctx, &s); err != nil {
				log.Info("files of a worktree not read", "path", s.Path, "err", err)
			}
			// A commit since the list above took files from the changes: they are
			// among the commits, which the old head says need no read.
			if fileLeft(was.Files, s.Files) {
				want.commits = true
			}
		}
		if want.commits {
			if err := readCommits(ctx, &s); err != nil {
				log.Info("commits of a worktree not read", "path", s.Path, "err", err)
			}
		}
		p.markRead(ctx, &s)
		p.commentsRead(ctx, &s)
		if ctx.Err() != nil {
			return
		}
		s.Unread = false
		log.Info("worktree read", "path", s.Path, "files", want.files, "commits", want.commits, logging.Duration(start))
		told.Lock()
		list[i] = s
		told.Unlock()
		tell()
	})
	if ctx.Err() != nil {
		return
	}
	p.forgetGone(ctx, list)

	if p.t.opts.readDone != nil {
		p.t.opts.readDone()
	}
	tell()
}

// fileLeft says whether a file in before is not in after.
func fileLeft(before, after []File) bool {
	return slices.ContainsFunc(before, func(f File) bool {
		return !slices.ContainsFunc(after, func(a File) bool { return a.Path == f.Path })
	})
}

// markRead sets what the checkout read has verified, and takes away the marks that
// no longer hold: this comparison is all that notices a file edited after it was
// verified, so a mark goes when the checkout is next read.
func (p *tracked) markRead(ctx context.Context, s *Status) {
	marks := p.t.opts.Marks
	if marks == nil {
		return
	}
	list, err := marks.List(ctx, p.id)
	if err != nil {
		logging.FromContext(ctx).Info("verified marks not read", "project_id", p.id, "err", err)
		return
	}
	s.Files, s.Committed = slices.Clone(s.Files), slices.Clone(s.Committed) // what was kept may be read
	for _, m := range applyMarks(ctx, s, list) {
		logging.FromContext(ctx).Info("verified mark no longer holds", "path", m.Worktree, "file", m.File)
		if err := marks.Remove(ctx, p.id, m.Worktree, m.File); err != nil {
			logging.FromContext(ctx).Info("verified mark not removed", "file", m.File, "err", err)
		}
	}
}

// commentsRead gives the checkout read its comments not sent yet.
func (p *tracked) commentsRead(ctx context.Context, s *Status) {
	store := p.t.opts.Comments
	if store == nil {
		return
	}
	drafts, err := store.Drafts(ctx, p.id)
	if err != nil {
		logging.FromContext(ctx).Info("comments not read", "project_id", p.id, "err", err)
		return
	}
	applyComments(ctx, s, drafts)
}

// forgetGone takes away the marks and comments of checkouts git no longer lists.
func (p *tracked) forgetGone(ctx context.Context, list []Status) {
	if store := p.t.opts.Comments; store != nil {
		if drafts, err := store.Drafts(ctx, p.id); err == nil {
			for _, c := range drafts {
				if !slices.ContainsFunc(list, func(s Status) bool { return s.Path == c.Worktree }) {
					if err := store.RemoveWorktree(ctx, p.id, c.Worktree); err != nil {
						logging.FromContext(ctx).Info("comments not removed", "path", c.Worktree, "err", err)
					}
				}
			}
		}
	}
	marks := p.t.opts.Marks
	if marks == nil {
		return
	}
	all, err := marks.List(ctx, p.id)
	if err != nil {
		return
	}
	for _, m := range all {
		if !slices.ContainsFunc(list, func(s Status) bool { return s.Path == m.Worktree }) {
			if err := marks.Remove(ctx, p.id, m.Worktree, m.File); err != nil {
				logging.FromContext(ctx).Info("verified mark not removed", "file", m.File, "err", err)
			}
		}
	}
}

// gitDirs are a checkout's git directory and the one its repository shares.
type gitDirs struct {
	git, common string
}

// dirsOf returns the git directories of the checkouts on disk, asking git only of
// those not known yet: a checkout's do not change.
func (p *tracked) dirsOf(ctx context.Context, list []Status) map[string]gitDirs {
	p.mu.Lock()
	known := p.gitDirs
	p.mu.Unlock()
	dirs := make(map[string]gitDirs, len(list))
	for _, s := range list {
		if s.Missing {
			continue
		}
		if d, ok := known[s.Path]; ok {
			dirs[s.Path] = d
			continue
		}
		gitDir, common, err := git.Dirs(ctx, s.Path)
		if err != nil {
			logging.FromContext(ctx).Info("git directory not read", "path", s.Path, "err", err)
			continue
		}
		dirs[s.Path] = gitDirs{git: gitDir, common: common}
	}
	return dirs
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// syncWatches watches what git changes for each checkout, and never its files,
// which would take a watch per directory (WatchTabs watches the few tabs show): its git directory and logs, what the
// repository's checkouts share, and where its base's ref and reflog are. A directory not there yet is added when its
// parent says it came, or at the next read.
func (p *tracked) syncWatches(ctx context.Context, watcher *fsnotify.Watcher, list []Status, dirs map[string]gitDirs) {
	want := map[string]bool{}
	commons := map[string]bool{}
	bases := map[string]bool{}
	for _, s := range list {
		d, ok := dirs[s.Path]
		if !ok {
			continue
		}
		want[d.git], want[filepath.Join(d.git, "logs")] = true, true
		commons[d.common] = true
		want[d.common], want[filepath.Join(d.common, "worktrees")] = true, true
		if s.Base != "" {
			// git writes the reflog before it moves the ref, so a read on the reflog
			// alone can find the old value. Neither is enough alone: a repository may
			// keep no reflogs, and a packed ref may have no directory to watch.
			ref := filepath.Join(d.common, baseRef(s.Base))
			log := filepath.Join(d.common, "logs", baseRef(s.Base))
			bases[ref], bases[log] = true, true
			want[filepath.Dir(ref)], want[filepath.Dir(log)] = true, true
		}
	}
	for _, have := range watcher.WatchList() {
		if !want[have] {
			_ = watcher.Remove(have) // a directory gone took its watch with it
		}
	}
	for dir := range want {
		if err := watcher.Add(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
			logging.FromContext(ctx).Info("directory not watched", "dir", dir, "err", err)
		}
	}
	p.mu.Lock()
	p.commonDirs, p.bases = commons, bases
	p.mu.Unlock()
}

// baseRef is the reference a base names, as its place in a git directory, and of
// its reflog under logs.
func baseRef(base string) string {
	if rest, ok := strings.CutPrefix(base, "origin/"); ok {
		return filepath.Join("refs", "remotes", "origin", rest)
	}
	return filepath.Join("refs", "heads", base)
}

// watch takes what the watcher says until the project is left.
func (p *tracked) watch(ctx context.Context, watcher *fsnotify.Watcher) {
	log := logging.FromContext(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-watcher.Events:
			if !ok {
				return
			}
			p.event(e)
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Info("worktree watch failed", "err", err)
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				p.markAll() // events were lost
			}
		}
	}
}

// event marks what a change to a file of git's means. Only these few say the
// checkout changed: git writes others, such as FETCH_HEAD, all the time.
func (p *tracked) event(e fsnotify.Event) {
	dir, name := filepath.Split(e.Name)
	dir = filepath.Clean(dir)
	p.mu.Lock()
	var checkout string
	inGitDir, inLogs := false, false
	for path, d := range p.gitDirs {
		if dir == d.git {
			checkout, inGitDir = path, true
		} else if dir == filepath.Join(d.git, "logs") {
			checkout, inLogs = path, true
		}
	}
	base := p.bases[e.Name]
	worktrees := p.commonDirs[filepath.Dir(dir)] && filepath.Base(dir) == "worktrees"
	appeared := p.commonDirs[dir] && name == "worktrees"
	p.mu.Unlock()

	switch {
	case base:
		p.markCommits()
	case worktrees || appeared, inGitDir && name == "logs":
		p.nudge() // every read lists the checkouts again, and watches what came
	case inGitDir && name == "index":
		p.mark(checkout, reading{files: true})
	case inGitDir && name == "index.lock" && !e.Has(fsnotify.Create):
		p.mark(checkout, reading{files: true})
	case inGitDir && name == "HEAD", inLogs && name == "HEAD":
		p.mark(checkout, reading{files: true, commits: true})
	}
}

// markCommits marks the commits of every checkout: their base moved.
func (p *tracked) markCommits() {
	p.mu.Lock()
	for _, s := range p.statuses {
		p.pending[s.Path] = p.pending[s.Path].with(reading{commits: true})
	}
	p.mu.Unlock()
	p.nudge()
}
