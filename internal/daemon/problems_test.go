package daemon

import (
	"context"
	"fmt"
	"os/exec"
	"testing"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/git"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/hublink"
)

func TestGitMissing(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("open: %w", &git.MissingError{Err: exec.ErrNotFound})
	p := problemOf(context.Background(), err)
	if p.Code != api.ErrorCodeDependencyMissing || p.Status != 503 || p.Details == nil || (*p.Details)["dependency"] != "git" {
		t.Fatalf("got %+v", p)
	}
}

// A hub that refuses a node for its API version, either side, is
// hub_version_unsupported, naming the side to update.
func TestHubVersionRefused(t *testing.T) {
	t.Parallel()
	for code, update := range map[hubapi.ErrorCode]string{
		hubapi.ErrorCodeNodeVersionUnsupported: hubapi.BehindNode,
		hubapi.ErrorCodeHubVersionUnsupported:  hubapi.BehindHub,
	} {
		hp := hubapi.NewProblem(code, "x")
		hp.Details = &map[string]any{"update": update, "api_version": "0"}
		p := problemOf(context.Background(), hubFailure(&hublink.HubError{Problem: hp}))
		if p.Code != api.ErrorCodeHubVersionUnsupported || p.Details == nil || (*p.Details)["update"] != update {
			t.Errorf("%s: got %+v", code, p)
		}
	}
}
