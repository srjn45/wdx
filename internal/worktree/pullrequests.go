package worktree

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/gh"
	"github.com/srjn45/wdx/internal/logging"
)

// PullRequests keeps what gh last said of each worktree's pull request, and asks it
// again when the user looks: nothing that runs by itself uses the network
// (docs/TIDE_WORKTREES.md).
type PullRequests struct {
	db      *sql.DB
	gh      gh.CLI
	changed func(projectID string)
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	asking  map[string]bool // the projects being asked about
	running sync.WaitGroup
}

// NewPullRequests returns the pull requests kept in db. Those asking gh in the
// background end with ctx, or at Close, and tell changed of what they found.
func NewPullRequests(ctx context.Context, database *sql.DB, cli gh.CLI, changed func(projectID string)) *PullRequests {
	ctx, cancel := context.WithCancel(ctx)
	return &PullRequests{db: database, gh: cli, changed: changed, ctx: ctx, cancel: cancel, asking: map[string]bool{}}
}

// Close stops asking gh and waits for it.
func (p *PullRequests) Close() {
	p.cancel()
	p.running.Wait()
}

// List returns the project's pull requests by the path of their checkouts.
func (p *PullRequests) List(ctx context.Context, projectID string) (map[string]gh.PullRequest, error) {
	rows, err := p.db.QueryContext(ctx, "SELECT worktree, number, url, state, head FROM worktree_pull_requests WHERE project_id = ?", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := map[string]gh.PullRequest{}
	for rows.Next() {
		var path string
		var v gh.PullRequest
		if err := rows.Scan(&path, &v.Number, &v.URL, &v.State, &v.Head); err != nil {
			return nil, err
		}
		list[path] = v
	}
	return list, rows.Err()
}

func (p *PullRequests) set(ctx context.Context, projectID, path string, v gh.PullRequest) error {
	_, err := db.Exec(ctx, p.db, `
		INSERT INTO worktree_pull_requests (project_id, worktree, number, url, state, head, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (project_id, worktree) DO UPDATE SET number = excluded.number, url = excluded.url, state = excluded.state, head = excluded.head, updated_at = excluded.updated_at`,
		projectID, path, v.Number, v.URL, v.State, v.Head, time.Now().UnixMilli())
	return err
}

// Forget drops what is known of the pull request of the checkout at path.
func (p *PullRequests) Forget(ctx context.Context, projectID, path string) error {
	_, err := db.Exec(ctx, p.db, "DELETE FROM worktree_pull_requests WHERE project_id = ? AND worktree = ?", projectID, path)
	return err
}

// Merged reports whether gh says the pull request of s merged with the very commit s
// is on. Anything less, a failure of gh included, is no: removing goes by this.
func (p *PullRequests) Merged(ctx context.Context, s Status) bool {
	if s.Branch == "" || s.Head == "" {
		return false
	}
	v, err := p.gh.View(ctx, s.Path, s.Branch)
	if err != nil {
		logging.FromContext(ctx).Info("pull request not known", "path", s.Path, "branch", s.Branch, "err", err)
		return false
	}
	merged := v.State == gh.Merged && v.Head == s.Head
	logging.FromContext(ctx).Info("pull request read", "path", s.Path, "number", v.Number, "state", v.State, "merged_here", merged)
	return merged
}

// Ask has gh say, in the background, how the pull requests of the checkouts given
// stand: those called name, or all when it is "". Asking while it is asking about the
// project does nothing, as the answer is on its way.
func (p *PullRequests) Ask(projectID, clone, name string, list []Status) {
	if !p.gh.Available() {
		return
	}
	p.mu.Lock()
	if p.asking[projectID] || p.ctx.Err() != nil {
		p.mu.Unlock()
		return
	}
	p.asking[projectID] = true
	p.running.Add(1)
	p.mu.Unlock()
	go func() {
		defer p.running.Done()
		defer func() {
			p.mu.Lock()
			delete(p.asking, projectID)
			p.mu.Unlock()
		}()
		p.ask(p.ctx, projectID, clone, name, list)
	}()
}

func (p *PullRequests) ask(ctx context.Context, projectID, clone, name string, list []Status) {
	log := logging.FromContext(ctx)
	known, err := p.List(ctx, projectID)
	if err != nil {
		log.Error("pull requests not read", "project_id", projectID, "err", err)
		return
	}
	changed := false
	for _, s := range list {
		_, isKnown := known[s.Path]
		ahead := s.Ahead != nil && *s.Ahead > 0
		if (name != "" && s.Name != name) || s.Path == clone || s.Missing || s.Branch == "" || (!ahead && !isKnown) {
			continue
		}
		v, err := p.gh.View(ctx, s.Path, s.Branch)
		switch {
		case errors.Is(err, gh.ErrNoPullRequest):
			if isKnown {
				if err := p.Forget(ctx, projectID, s.Path); err != nil {
					log.Error("pull request not forgotten", "path", s.Path, "err", err)
					continue
				}
				changed = true
			}
		case err != nil:
			log.Info("pull request not read", "path", s.Path, "err", err)
		case v != known[s.Path]:
			if err := p.set(ctx, projectID, s.Path, v); err != nil {
				log.Error("pull request not saved", "path", s.Path, "err", err)
				continue
			}
			changed = true
		}
	}
	if changed && p.changed != nil {
		p.changed(projectID)
	}
}
