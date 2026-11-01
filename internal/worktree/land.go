package worktree

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/srjn45/wdx/internal/gh"
	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/logging"
)

// What refuses a landing before it starts.
var (
	ErrNotCommitted   = errors.New("files are not committed")
	ErrNothingToLand  = errors.New("nothing is ahead of the base")
	ErrNoLocalBase    = errors.New("no local branch to land on")
	ErrAgentWorking   = errors.New("an agent is working in the worktree")
	ErrLandingRunning = errors.New("the worktree is already landing")
	ErrWorktreeBusy   = errors.New("a rebase, merge or the like is in progress in the worktree")
	ErrDetachedHead   = errors.New("the worktree is on no branch, so there is none to push")
)

// How a project lands.
const (
	MethodRebase = "rebase"
	MethodPR     = "pr"
)

// LandState is how far a landing got.
type LandState string

// The states of a landing: the last three end it.
const (
	LandRebasing LandState = "rebasing"
	LandChecking LandState = "checking"
	LandMoving   LandState = "moving"
	LandPushing  LandState = "pushing"
	LandOpening  LandState = "opening"
	LandLanded   LandState = "landed"
	LandOpened   LandState = "opened"
	LandFailed   LandState = "failed"
)

// LandFailure says why a landing that started failed.
type LandFailure string

// Why a landing can fail; in every case the base has not moved.
const (
	LandRebaseFailed      LandFailure = "rebase_failed"
	LandCheckFailed       LandFailure = "check_failed"
	LandBaseMoved         LandFailure = "base_moved"
	LandFastForwardFailed LandFailure = "fast_forward_failed"
	LandPushFailed        LandFailure = "push_failed"
	LandPRFailed          LandFailure = "pr_failed"
)

// Landing is a landing of a worktree, the last one or the one running.
type Landing struct {
	ID        string
	State     LandState
	Failure   LandFailure // when failed
	Detail    string      // what went wrong
	Conflicts []string    // the files a rebase stopped on
	Terminal  string      // the terminal running the check, or left by it
	Base      string
	Commit    string // the commit landed, once it is known
	Head      string // the commit the worktree was on when it failed
	Pushed    bool
	PushError string // a push that failed: it landed all the same
	// PullRequest is the one a landing by PR opened or found open.
	PullRequest *gh.PullRequest
}

// Finished reports whether the landing has ended.
func (l Landing) Finished() bool {
	return l.State == LandLanded || l.State == LandOpened || l.State == LandFailed
}

// LandSettings is how a project lands.
type LandSettings struct {
	Method string // MethodRebase when ""
	Check  string // the command run first; "" for none
	Push   bool
}

// LanderOptions is what a Lander needs of the rest of the daemon.
type LanderOptions struct {
	// Check runs command in dir until it ends, saying in started what terminal it
	// runs in: nil only when it passed.
	Check func(ctx context.Context, projectID, name, dir, command string, started func(terminal string)) error
	// Changed is told of every change of a landing, and of the repository's.
	Changed func(projectID string)
	// PullRequests keeps what a landing by PR opened, and through them it reaches gh.
	PullRequests *PullRequests
}

// Lander lands worktrees, each in the background, as a check can take many minutes.
// It keeps them in memory: a daemon restarted forgets one running, so that nothing
// lands that nobody is there to see.
type Lander struct {
	ctx      context.Context
	cancel   context.CancelFunc
	opts     LanderOptions
	mu       sync.Mutex
	landings map[string]Landing // by project and path
	running  sync.WaitGroup
}

// NewLander returns a Lander whose landings end with ctx, or at Close.
func NewLander(ctx context.Context, opts LanderOptions) *Lander {
	ctx, cancel := context.WithCancel(ctx)
	return &Lander{ctx: ctx, cancel: cancel, opts: opts, landings: map[string]Landing{}}
}

// Close stops waiting for the checks and waits for the landings to end. What git is
// doing runs to its end: stopped, it would leave a rebase half done.
func (l *Lander) Close() {
	l.cancel()
	l.running.Wait()
}

func landingKey(projectID, path string) string { return projectID + "\x00" + path }

// Of returns the last landing of the checkout at path.
func (l *Lander) Of(projectID, path string) (Landing, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	v, ok := l.landings[landingKey(projectID, path)]
	return v, ok
}

// Forget drops what is known of the landing of the checkout at path.
func (l *Lander) Forget(projectID, path string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.landings, landingKey(projectID, path))
}

// Start lands the checkout called name, or says why it will not: what comes after
// that is in its Landing, ending with its state.
func (l *Lander) Start(ctx context.Context, projectID string, c Checkouts, name string, agents []Agent, settings LandSettings) (Landing, error) {
	s, err := landable(ctx, c, name, agents, settings.Method)
	if err != nil {
		return Landing{}, err
	}
	if settings.Method == MethodPR && (l.opts.PullRequests == nil || !l.opts.PullRequests.gh.Available()) {
		return Landing{}, gh.ErrMissing
	}
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return Landing{}, err
	}
	v := Landing{ID: "land-" + hex.EncodeToString(id), State: LandRebasing, Base: s.Base}
	if settings.Method == MethodPR {
		v.State = LandPushing
	}
	key := landingKey(projectID, s.Path)

	l.mu.Lock()
	if last, ok := l.landings[key]; ok && !last.Finished() {
		l.mu.Unlock()
		return Landing{}, ErrLandingRunning
	}
	l.landings[key] = v
	l.running.Add(1)
	l.mu.Unlock()
	logging.FromContext(ctx).Info("landing started", "project_id", projectID, "worktree", name, "base", s.Base, "landing_id", v.ID)

	// It outlives the request, keeping its debug id.
	run := logging.NewContext(l.ctx, logging.FromContext(ctx))
	go func() {
		defer l.running.Done()
		if settings.Method == MethodPR {
			l.runPR(run, projectID, s, settings, v)
			return
		}
		l.run(run, projectID, c, s, settings, v)
	}()
	return v, nil
}

// landable returns the checkout called name, read now, if it can land.
func landable(ctx context.Context, c Checkouts, name string, agents []Agent, method string) (Status, error) {
	s, err := Find(ctx, c, name)
	if err != nil {
		return Status{}, err
	}
	if s.Path == c.Clone {
		return Status{}, ErrIsClone
	}
	if err := readFiles(ctx, &s); err != nil {
		return Status{}, err
	}
	if err := readCommits(ctx, &s); err != nil {
		return Status{}, err
	}
	busy, err := git.OperationInProgress(ctx, s.Path)
	if err != nil {
		return Status{}, err
	}
	switch {
	case busy:
		return Status{}, ErrWorktreeBusy
	case len(s.Files) > 0:
		return Status{}, fmt.Errorf("%w: %d in %s", ErrNotCommitted, len(s.Files), name)
	case s.Base == "" || (method != MethodPR && strings.HasPrefix(s.Base, "origin/")):
		return Status{}, ErrNoLocalBase
	case method == MethodPR && s.Branch == "":
		return Status{}, ErrDetachedHead
	case s.Ahead == nil || *s.Ahead == 0:
		return Status{}, ErrNothingToLand
	}
	if i := slices.IndexFunc(agents, func(a Agent) bool { return a.Working && s.Holds(a.Dir) }); i >= 0 {
		return Status{}, fmt.Errorf("%w: %s", ErrAgentWorking, agents[i].ID)
	}
	return s, nil
}

func (l *Lander) set(projectID, path string, v Landing) {
	l.mu.Lock()
	l.landings[landingKey(projectID, path)] = v
	l.mu.Unlock()
	if l.opts.Changed != nil {
		l.opts.Changed(projectID)
	}
}

// run takes the landing through its steps. Each failure leaves the base where it was.
func (l *Lander) run(ctx context.Context, projectID string, c Checkouts, s Status, settings LandSettings, v Landing) {
	log := logging.FromContext(ctx).With("project_id", projectID, "worktree", s.Name, "landing_id", v.ID)
	fail := func(failure LandFailure, detail string) {
		log.Info("landing failed", "failure", failure, "detail", detail)
		v.State, v.Failure, v.Detail = LandFailed, failure, detail
		if head, err := git.Resolve(context.WithoutCancel(ctx), s.Path, "HEAD"); err == nil {
			v.Head = head
		}
		l.set(projectID, s.Path, v)
	}
	gitCtx := context.WithoutCancel(ctx)

	onBase, err := git.IsAncestor(gitCtx, s.Path, s.Base, "HEAD")
	if err != nil {
		fail(LandRebaseFailed, err.Error())
		return
	}
	if !onBase {
		log.Info("rebasing", "base", s.Base)
		if err := git.Rebase(gitCtx, s.Path, s.Base); err != nil {
			if e, ok := errors.AsType[*git.RebaseError](err); ok {
				v.Conflicts = e.Conflicts
			}
			fail(LandRebaseFailed, err.Error())
			return
		}
	}
	commit, err := git.Resolve(gitCtx, s.Path, "HEAD")
	if err != nil {
		fail(LandRebaseFailed, err.Error())
		return
	}
	v.Commit = commit

	if settings.Check != "" {
		v.State = LandChecking
		l.set(projectID, s.Path, v)
		err := l.opts.Check(ctx, projectID, s.Name, s.Path, settings.Check, func(terminal string) {
			v.Terminal = terminal
			l.set(projectID, s.Path, v)
		})
		if err != nil {
			fail(LandCheckFailed, err.Error())
			return
		}
	}

	v.State = LandMoving
	l.set(projectID, s.Path, v)
	onBase, err = git.IsAncestor(gitCtx, s.Path, s.Base, commit)
	if err != nil {
		fail(LandFastForwardFailed, err.Error())
		return
	}
	if !onBase {
		fail(LandBaseMoved, s.Base+" moved during the check")
		return
	}
	if err := moveBase(gitCtx, c, s, commit); err != nil {
		fail(LandFastForwardFailed, err.Error())
		return
	}
	if settings.Push {
		if err := git.Push(gitCtx, s.Path, s.Base); err != nil {
			log.Error("landed but not pushed", "base", s.Base, "err", err)
			v.PushError = err.Error()
		} else {
			v.Pushed = true
		}
	}
	v.State = LandLanded
	log.Info("landed", "base", s.Base, "commit", commit)
	l.set(projectID, s.Path, v)
}

// runPR takes a landing by PR through its steps: the check, what gh knows, the push,
// and the PR. It never rebases: a branch already pushed could then only go up forced.
func (l *Lander) runPR(ctx context.Context, projectID string, s Status, settings LandSettings, v Landing) {
	log := logging.FromContext(ctx).With("project_id", projectID, "worktree", s.Name, "landing_id", v.ID)
	prs := l.opts.PullRequests
	fail := func(failure LandFailure, detail string) {
		log.Info("landing failed", "failure", failure, "detail", detail)
		v.State, v.Failure, v.Detail, v.Head = LandFailed, failure, detail, s.Head
		l.set(projectID, s.Path, v)
	}
	gitCtx := context.WithoutCancel(ctx)
	v.Commit = s.Head

	if settings.Check != "" {
		v.State = LandChecking
		l.set(projectID, s.Path, v)
		err := l.opts.Check(ctx, projectID, s.Name, s.Path, settings.Check, func(terminal string) {
			v.Terminal = terminal
			l.set(projectID, s.Path, v)
		})
		if err != nil {
			fail(LandCheckFailed, err.Error())
			return
		}
	}

	// Asked before anything is pushed: a repository gh cannot work with changes nothing.
	open, err := prs.gh.View(ctx, s.Path, s.Branch)
	found := err == nil
	switch {
	case errors.Is(err, gh.ErrNoPullRequest):
	case err != nil:
		fail(LandPRFailed, err.Error())
		return
	case open.State == gh.Merged && open.Head == s.Head:
		fail(LandPRFailed, fmt.Sprintf("PR #%d has merged with this commit: nothing is left to land", open.Number))
		return
	}

	v.State = LandPushing
	l.set(projectID, s.Path, v)
	if err := git.Push(gitCtx, s.Path, s.Branch); err != nil {
		fail(LandPushFailed, err.Error())
		return
	}
	v.Pushed = true

	var pr gh.PullRequest
	if found && open.State == gh.Open {
		log.Info("pull request already open", "number", open.Number)
		pr = open
		pr.Head = s.Head // the push just took it there
	} else {
		v.State = LandOpening
		l.set(projectID, s.Path, v)
		pr, err = prs.gh.Create(ctx, s.Path, strings.TrimPrefix(s.Base, "origin/"), s.Branch, s.Head)
		if err != nil {
			fail(LandPRFailed, err.Error())
			return
		}
		log.Info("pull request opened", "number", pr.Number, "url", pr.URL)
	}
	if err := prs.set(gitCtx, projectID, s.Path, pr); err != nil {
		log.Error("pull request not saved", "number", pr.Number, "err", err)
	}
	v.State, v.PullRequest = LandOpened, &pr
	l.set(projectID, s.Path, v)
}

// moveBase moves the base of s forward to commit: with a merge where a checkout has
// it, so that its files follow and git refuses changes in the way, else the ref alone.
func moveBase(ctx context.Context, c Checkouts, s Status, commit string) error {
	list, err := List(ctx, c)
	if err != nil {
		return err
	}
	if i := slices.IndexFunc(list, func(o Status) bool { return o.Branch == s.Base && !o.Missing && o.repository == s.repository }); i >= 0 {
		return git.FastForward(ctx, list[i].Path, commit)
	}
	return git.MoveBranch(ctx, s.Path, s.Base, commit)
}
