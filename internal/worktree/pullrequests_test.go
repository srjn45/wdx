package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/db"
	"github.com/srjn45/wdx/internal/db/dbtest"
	"github.com/srjn45/wdx/internal/gh"
	"github.com/srjn45/wdx/internal/gh/ghtest"
	"github.com/srjn45/wdx/internal/gittest"
)

// prHarness is a repository with an origin and a worktree one commit ahead, a Lander
// and the gh it reaches.
type prHarness struct {
	dir, origin, work string
	checkouts         Checkouts
	fake              *ghtest.Fake
	prs               *PullRequests
	lander            *landerUnderTest
}

func newPRHarness(t *testing.T) *prHarness {
	t.Helper()
	h := &prHarness{dir: repo(t), fake: ghtest.New(t, t.TempDir())}
	h.origin = filepath.Join(t.TempDir(), "origin.git")
	gittest.Git(t, h.dir, "init", "-q", "--bare", h.origin)
	gittest.Git(t, h.dir, "remote", "add", "origin", h.origin)
	gittest.Git(t, h.dir, "push", "-q", "origin", "main")
	h.work = linked(t, h.dir, "work")
	commit(t, h.work, "more", "a\n")
	h.checkouts = Checkouts{Clone: h.dir, Paths: []string{h.dir}}

	path := filepath.Join(t.TempDir(), "wdx.db")
	dbtest.Seed(t, path)
	database, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if _, err := database.ExecContext(t.Context(), "INSERT INTO projects (id, name, state, created_at, updated_at) VALUES ('p', 'p', 'open', 0, 0)"); err != nil {
		t.Fatal(err)
	}
	h.prs = NewPullRequests(t.Context(), database, gh.CLI{Program: h.fake.Program}, nil)
	t.Cleanup(h.prs.Close)
	h.lander = newLanderWith(t, LanderOptions{Check: noCheck, PullRequests: h.prs})
	return h
}

func (h *prHarness) land(t *testing.T, settings LandSettings) Landing {
	t.Helper()
	settings.Method = MethodPR
	if _, err := h.lander.Start(t.Context(), "p", h.checkouts, "work", nil, settings); err != nil {
		t.Fatal(err)
	}
	return h.lander.finish(t, h.work)
}

func (h *prHarness) called(t *testing.T, what string) bool {
	t.Helper()
	return slices.ContainsFunc(h.fake.Calls(t), func(c string) bool { return strings.HasPrefix(c, what) })
}

func TestLandByPROpensOne(t *testing.T) {
	t.Parallel()
	h := newPRHarness(t)
	want, base := head(t, h.work, "HEAD"), head(t, h.dir, "main")

	v := h.land(t, LandSettings{})
	if v.State != LandOpened || v.PullRequest == nil || v.PullRequest.Number != 12 || v.PullRequest.Head != want || !v.Pushed {
		t.Fatalf("landing: %+v", v)
	}
	if got := head(t, h.origin, "work"); got != want {
		t.Errorf("origin has the branch at %s, want %s", got, want)
	}
	if got := head(t, h.dir, "main"); got != base {
		t.Errorf("the base moved to %s", got)
	}
	if !slices.Contains(h.fake.Calls(t), "pr create --fill --base main --head work") {
		t.Errorf("gh was asked: %q", h.fake.Calls(t))
	}
	list, err := h.prs.List(t.Context(), "p")
	if err != nil || list[h.work] != *v.PullRequest {
		t.Errorf("kept: %+v, %v", list, err)
	}
}

// A moved base is the PR's to show: rebasing would mean forcing the pushed branch.
func TestLandByPRDoesNotRebase(t *testing.T) {
	t.Parallel()
	h := newPRHarness(t)
	want := head(t, h.work, "HEAD")
	commit(t, h.dir, "later", "b\n")

	if v := h.land(t, LandSettings{}); v.State != LandOpened {
		t.Fatalf("landing: %+v", v)
	}
	if got := head(t, h.work, "HEAD"); got != want {
		t.Errorf("rebased to %s", got)
	}
}

func TestLandByPRAddsToTheOneOpen(t *testing.T) {
	t.Parallel()
	h := newPRHarness(t)
	h.fake.Set(t, "OPEN", "older")

	v := h.land(t, LandSettings{})
	want := head(t, h.work, "HEAD")
	if v.State != LandOpened || v.PullRequest == nil || v.PullRequest.Head != want {
		t.Fatalf("landing: %+v", v)
	}
	if h.called(t, "pr create") {
		t.Errorf("opened another: %q", h.fake.Calls(t))
	}
	if got := head(t, h.origin, "work"); got != want {
		t.Errorf("origin is at %s, want %s", got, want)
	}
}

// A PR closed or merged with other commits says nothing of this one: it opens anew.
func TestLandByPROpensAfterAnOldOne(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"CLOSED", "MERGED"} {
		h := newPRHarness(t)
		h.fake.Set(t, state, "older")
		if v := h.land(t, LandSettings{}); v.State != LandOpened || !h.called(t, "pr create") {
			t.Errorf("%s: %+v, %q", state, v, h.fake.Calls(t))
		}
	}
}

func TestLandByPRFails(t *testing.T) {
	t.Parallel()
	t.Run("merged with this commit", func(t *testing.T) {
		t.Parallel()
		h := newPRHarness(t)
		h.fake.Set(t, "MERGED", head(t, h.work, "HEAD"))
		if v := h.land(t, LandSettings{}); v.Failure != LandPRFailed || !strings.Contains(v.Detail, "has merged") {
			t.Fatalf("landing: %+v", v)
		}
	})
	t.Run("gh cannot work here, so nothing is pushed", func(t *testing.T) {
		t.Parallel()
		h := newPRHarness(t)
		h.fake.Refuse(t, "HTTP 401: Bad credentials")
		v := h.land(t, LandSettings{})
		if v.Failure != LandPRFailed || v.Detail != "HTTP 401: Bad credentials" {
			t.Fatalf("landing: %+v", v)
		}
		if _, err := os.Stat(filepath.Join(h.origin, "refs", "heads", "work")); !os.IsNotExist(err) {
			t.Errorf("pushed: %v", err)
		}
	})
	t.Run("the check, so nothing is pushed", func(t *testing.T) {
		t.Parallel()
		h := newPRHarness(t)
		h.lander.opts.Check = failingCheck
		if v := h.land(t, LandSettings{Check: "make test"}); v.Failure != LandCheckFailed {
			t.Fatalf("landing: %+v", v)
		}
		if h.called(t, "pr ") {
			t.Errorf("gh was asked: %q", h.fake.Calls(t))
		}
	})
	t.Run("a push refused, which is not forced", func(t *testing.T) {
		t.Parallel()
		h := newPRHarness(t)
		gittest.Git(t, h.origin, "branch", "work", "main")
		other := filepath.Join(t.TempDir(), "other")
		gittest.Git(t, h.dir, "clone", "-q", "--branch", "work", h.origin, other)
		commit(t, other, "elsewhere", "c\n")
		gittest.Git(t, other, "push", "-q", "origin", "work")
		theirs := head(t, h.origin, "work")

		v := h.land(t, LandSettings{})
		if v.Failure != LandPushFailed {
			t.Fatalf("landing: %+v", v)
		}
		if got := head(t, h.origin, "work"); got != theirs {
			t.Errorf("origin went from %s to %s", theirs, got)
		}
		if h.called(t, "pr create") {
			t.Errorf("opened one: %q", h.fake.Calls(t))
		}
	})
}

func TestLandByPRRefuses(t *testing.T) {
	t.Parallel()
	h := newPRHarness(t)
	pr := LandSettings{Method: MethodPR}

	none := newLanderWith(t, LanderOptions{Check: noCheck, PullRequests: NewPullRequests(t.Context(), nil, gh.CLI{}, nil)})
	if _, err := none.Start(t.Context(), "p", h.checkouts, "work", nil, pr); !errors.Is(err, gh.ErrMissing) {
		t.Errorf("no gh: %v", err)
	}
	gittest.Git(t, h.work, "checkout", "-q", "--detach")
	if _, err := h.lander.Start(t.Context(), "p", h.checkouts, "work", nil, pr); !errors.Is(err, ErrDetachedHead) {
		t.Errorf("detached: %v", err)
	}
}

// With no local base, rebasing has nowhere to land, but a PR does.
func TestLandByPROnABaseOnlyOriginHas(t *testing.T) {
	t.Parallel()
	h := newPRHarness(t)
	gittest.Git(t, h.dir, "checkout", "-q", "--detach")
	gittest.Git(t, h.dir, "fetch", "-q", "origin")
	gittest.Git(t, h.dir, "branch", "-qD", "main")

	if _, err := h.lander.Start(t.Context(), "p", h.checkouts, "work", nil, LandSettings{}); !errors.Is(err, ErrNoLocalBase) {
		t.Fatalf("by rebase: %v", err)
	}
	if v := h.land(t, LandSettings{}); v.State != LandOpened || !slices.Contains(h.fake.Calls(t), "pr create --fill --base main --head work") {
		t.Fatalf("by PR: %+v, %q", v, h.fake.Calls(t))
	}
}

func TestRemovableThroughAPullRequest(t *testing.T) {
	t.Parallel()
	h := newPRHarness(t)
	here := head(t, h.work, "HEAD")
	removable := func() error {
		_, err := Removable(t.Context(), h.checkouts, "work", h.prs)
		return err
	}

	if err := removable(); !isNotLanded(err) {
		t.Fatalf("no PR: %v", err)
	}
	h.fake.Set(t, "OPEN", here)
	if err := removable(); !isNotLanded(err) {
		t.Errorf("open: %v", err)
	}
	// A branch name used again finds an old PR, which says nothing of these commits.
	h.fake.Set(t, "MERGED", "older")
	if err := removable(); !isNotLanded(err) {
		t.Errorf("merged with other commits: %v", err)
	}
	h.fake.Set(t, "MERGED", here)
	if err := removable(); err != nil {
		t.Errorf("merged: %v", err)
	}
	write(t, h.work, "file", "changed\n")
	if err := removable(); !isNotLanded(err) {
		t.Errorf("merged, but with a file changed: %v", err)
	}
	if err := func() error {
		_, err := Removable(t.Context(), h.checkouts, "work", nil)
		return err
	}(); !isNotLanded(err) {
		t.Errorf("no way to ask: %v", err)
	}
}

func TestAskKeepsWhatGhSays(t *testing.T) {
	t.Parallel()
	h := newPRHarness(t)
	list, err := Read(t.Context(), h.checkouts, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	here := head(t, h.work, "HEAD")
	ask := func() map[string]gh.PullRequest {
		t.Helper()
		h.prs.ask(t.Context(), "p", h.dir, "", list)
		got, err := h.prs.List(t.Context(), "p")
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	// No PR yet, and the clone and a row with nothing ahead are not asked about.
	if got := ask(); len(got) != 0 {
		t.Fatalf("none: %+v", got)
	}
	if calls := h.fake.Calls(t); len(calls) != 1 || !strings.HasPrefix(calls[0], "pr view work") {
		t.Errorf("asked: %q", calls)
	}
	// One opened some other way is found, and follows what gh says.
	h.fake.Set(t, "OPEN", here)
	if got := ask(); got[h.work].State != gh.Open {
		t.Errorf("open: %+v", got)
	}
	h.fake.Set(t, "MERGED", here)
	if got := ask(); got[h.work].State != gh.Merged {
		t.Errorf("merged: %+v", got)
	}
	// A failure of gh changes nothing.
	h.fake.Refuse(t, "no network")
	if got := ask(); got[h.work].State != gh.Merged {
		t.Errorf("refused: %+v", got)
	}
}
