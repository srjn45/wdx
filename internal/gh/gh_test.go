package gh_test

import (
	"errors"
	"os"
	"testing"

	"github.com/srjn45/wdx/internal/gh"
	"github.com/srjn45/wdx/internal/gh/ghtest"
	"github.com/srjn45/wdx/internal/gittest"
	"github.com/srjn45/wdx/internal/runtest"
)

func TestMain(m *testing.M) { os.Exit(runtest.Main(m)) }

func TestNoProgramIsMissing(t *testing.T) {
	t.Parallel()
	for _, program := range []string{"", t.TempDir() + "/no-such-gh"} {
		c := gh.CLI{Program: program}
		if c.Available() {
			t.Errorf("%q is available", program)
		}
		if _, err := c.View(t.Context(), t.TempDir(), "work"); !errors.Is(err, gh.ErrMissing) {
			t.Errorf("%q: %v", program, err)
		}
	}
}

func TestViewAndCreate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gittest.Git(t, dir, "init", "-q")
	gittest.Git(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	fake := ghtest.New(t, t.TempDir())
	c := gh.CLI{Program: fake.Program}

	if _, err := c.View(t.Context(), dir, "work"); !errors.Is(err, gh.ErrNoPullRequest) {
		t.Fatalf("none: %v", err)
	}
	made, err := c.Create(t.Context(), dir, "main", "work", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if want := (gh.PullRequest{Number: 12, URL: "https://github.test/o/r/pull/12", State: gh.Open, Head: "abc"}); made != want {
		t.Errorf("created: %+v", made)
	}
	fake.Set(t, "MERGED", "def")
	v, err := c.View(t.Context(), dir, "work")
	if err != nil || v.State != gh.Merged || v.Head != "def" || v.Number != 12 {
		t.Errorf("viewed: %+v, %v", v, err)
	}

	// What gh says of a failure is the error, not its exit code.
	fake.Refuse(t, "HTTP 401: Bad credentials")
	if _, err := c.View(t.Context(), dir, "work"); err == nil || err.Error() != "HTTP 401: Bad credentials" {
		t.Errorf("refused: %v", err)
	}
}
