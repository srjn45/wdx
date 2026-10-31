package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/telemetry"
)

func reload(t *testing.T, td *testDaemon) *api.ReloadDaemonResponse {
	t.Helper()
	r, err := td.client.ReloadDaemonWithResponse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReloadTerminalSettings(t *testing.T) {
	t.Parallel()
	shell := filepath.Join(t.TempDir(), "other-shell")
	if err := runtest.WriteExecutable(shell, testShell); err != nil {
		t.Fatal(err)
	}
	td := startOn(t, shortDir(t), telemetry.Options{}, func(o *Options) {
		o.Reload = func() (Settings, error) {
			return Settings{TerminalShell: shell, TerminalHistoryLines: 10}, nil
		}
	})
	td.create(t, "app")
	before := td.createTerminal(t, "app", t.TempDir(), "before")
	if before.Shell == shell {
		t.Fatalf("want the shell of the start first, got %s", before.Shell)
	}

	if r := reload(t, td); r.StatusCode() != http.StatusOK || r.JSON200 == nil || r.JSON200.Result != api.ResultReloaded {
		t.Fatalf("want reloaded, got %d: %s", r.StatusCode(), r.Body)
	}
	if after := td.createTerminal(t, "app", t.TempDir(), "after"); after.Shell != shell {
		t.Fatalf("want a new terminal on %s, got %s", shell, after.Shell)
	}
	got, err := td.client.GetTerminalWithResponse(context.Background(), "id:"+before.Id, &api.GetTerminalParams{})
	if err != nil || got.JSON200 == nil || got.JSON200.Terminal.Shell != before.Shell || got.JSON200.Terminal.State != before.State {
		t.Fatalf("want the running terminal left as it was, got %v: %v", got, err)
	}
}

func TestReloadFails(t *testing.T) {
	t.Parallel()
	td := startOn(t, shortDir(t), telemetry.Options{}, func(o *Options) {
		o.Reload = func() (Settings, error) { return Settings{}, errors.New("cannot read it") }
	})
	r := reload(t, td)
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusUnprocessableEntity, api.ErrorCodeReloadFailed)
}

func TestReloadWithoutSettings(t *testing.T) {
	t.Parallel()
	td := start(t)
	if r := reload(t, td); r.StatusCode() != http.StatusOK {
		t.Fatalf("want it to leave the daemon as it is, got %d: %s", r.StatusCode(), r.Body)
	}
}

// A reload does not queue behind changes: a clone can take minutes.
func TestReloadDoesNotWaitForChanges(t *testing.T) {
	t.Parallel()
	td := start(t)
	td.daemon.changes.Lock()
	defer td.daemon.changes.Unlock()
	if r := reload(t, td); r.StatusCode() != http.StatusOK {
		t.Fatalf("want it served during a change, got %d: %s", r.StatusCode(), r.Body)
	}
}

func TestReloadMovesMetrics(t *testing.T) {
	t.Parallel()
	before, after := &collector{}, &collector{}
	beforeSrv, afterSrv := httptest.NewServer(before), httptest.NewServer(after)
	defer beforeSrv.Close()
	defer afterSrv.Close()
	// The interval never passes: each exporter sends what it has when it ends.
	td := startOn(t, shortDir(t), telemetry.Options{Endpoint: beforeSrv.URL, Interval: time.Hour}, func(o *Options) {
		o.Reload = func() (Settings, error) {
			return Settings{Metrics: telemetry.Options{Endpoint: afterSrv.URL, Interval: time.Hour}}, nil
		}
	})
	ctx := context.Background()
	if r, err := td.client.GetProjectWithResponse(ctx, "first"); err != nil || r.StatusCode() != http.StatusNotFound {
		t.Fatalf("get: %v %v", r, err)
	}
	if r := reload(t, td); r.StatusCode() != http.StatusOK {
		t.Fatalf("reload: %d: %s", r.StatusCode(), r.Body)
	}
	if r, err := td.client.ListProjectsWithResponse(ctx, nil); err != nil || r.StatusCode() != http.StatusOK {
		t.Fatalf("list: %v %v", r, err)
	}
	td.stop()

	first := "code=not_found operation=getProject status=404"
	second := "operation=listProjects status=200"
	if got := before.requestCounts(t); got[first] != 1 || got[second] != 0 {
		t.Errorf("want only what came before the reload sent to the old collector, got %v", got)
	}
	if got := after.requestCounts(t); got[second] != 1 || got[first] != 0 {
		t.Errorf("want what came after it sent to the new collector, got %v", got)
	}
}
