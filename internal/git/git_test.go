package git

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"
)

func TestRepoKey(t *testing.T) {
	t.Parallel()
	const want = "github.com/srjn45/warden"
	for _, url := range []string{
		"git@github.com:srjn45/warden.git",
		"git@GitHub.com:srjn45/warden",
		"https://github.com/srjn45/warden",
		"https://github.com/srjn45/warden.git/",
		"https://user@github.com:443/srjn45/warden.git",
		"ssh://git@github.com/srjn45/warden.git",
		"ssh://git@github.com:22/srjn45/warden",
	} {
		if got := RepoKey(url); got != want {
			t.Errorf("RepoKey(%q) = %q, want %q", url, got, want)
		}
	}
	for url, want := range map[string]string{
		"":                                      "",
		"/srv/git/repo.git":                     "/srv/git/repo",
		"file:///srv/git/repo":                  "/srv/git/repo",
		"git@github.com:a/b.git":                "github.com/a/b",
		"https://gitlab.com/group/sub/repo.git": "gitlab.com/group/sub/repo",
		"git@gitlab.com:group/sub/repo.git":     "gitlab.com/group/sub/repo",
	} {
		if got := RepoKey(url); got != want {
			t.Errorf("RepoKey(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestMissing(t *testing.T) {
	t.Parallel()
	err := missing(fmt.Errorf("run git: %w", exec.ErrNotFound))
	if _, ok := errors.AsType[*MissingError](err); !ok || !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("not found: %v", err)
	}
	if _, ok := errors.AsType[*MissingError](missing(errors.New("permission denied"))); ok {
		t.Fatal("another failure became missing")
	}
}
