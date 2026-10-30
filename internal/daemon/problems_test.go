package daemon

import (
	"context"
	"fmt"
	"os/exec"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/git"
)

func TestGitMissing(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("open: %w", &git.MissingError{Err: exec.ErrNotFound})
	p := problemOf(context.Background(), err)
	if p.Code != api.ErrorCodeDependencyMissing || p.Status != 503 || p.Details == nil || (*p.Details)["dependency"] != "git" {
		t.Fatalf("got %+v", p)
	}
}
