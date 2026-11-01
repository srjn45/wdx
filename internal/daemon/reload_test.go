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

// fakeSettings takes any value but "bad" and has one setting.
type fakeSettings struct{ value string }

func (f *fakeSettings) List() ([]api.Setting, error) {
	return []api.Setting{{Key: "a.b", Value: f.value, Editable: true}}, nil
}

func (f *fakeSettings) Set(key, value string) (api.Setting, bool, error) {
	if key != "a.b" {
		return api.Setting{}, false, &UnknownSettingError{Key: key}
	}
	if value == "bad" {
		return api.Setting{}, false, &InvalidSettingError{Key: key, Err: errors.New("bad is bad")}
	}
	changed := value != f.value
	f.value = value
	return api.Setting{Key: key, Value: value, Editable: true}, changed, nil
}

func TestSettings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reloads := 0
	td := startOn(t, shortDir(t), telemetry.Options{}, func(o *Options) {
		o.SettingStore = &fakeSettings{value: "one"}
		o.Reload = func() (Settings, error) {
			reloads++
			if reloads == 3 {
				return Settings{}, errors.New("cannot read it")
			}
			return Settings{TerminalHistoryLines: 1}, nil
		}
	})
	list, err := td.client.ListSettingsWithResponse(ctx)
	if err != nil || list.JSON200 == nil || len(list.JSON200.Settings) != 1 || list.JSON200.Settings[0].Value != "one" {
		t.Fatalf("list: %v %s", err, list.Body)
	}
	set := func(key, value string) *api.UpdateSettingResponse {
		t.Helper()
		r, err := td.client.UpdateSettingWithResponse(ctx, key, api.UpdateSetting{Value: value})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := set("a.b", "two"); r.JSON200 == nil || r.JSON200.Result != api.ResultUpdated || r.JSON200.Setting.Value != "two" || reloads != 1 {
		t.Fatalf("updated: %s, %d reloads", r.Body, reloads)
	}
	// Unchanged, it reloads all the same: that recovers from a reload that failed.
	if r := set("a.b", "two"); r.JSON200 == nil || r.JSON200.Result != api.ResultUnchanged || reloads != 2 {
		t.Fatalf("unchanged: %s, %d reloads", r.Body, reloads)
	}
	wantProblem(t, set("a.b", "three").ApplicationproblemJSONDefault, http.StatusUnprocessableEntity, api.ErrorCodeReloadFailed)
	wantProblem(t, set("a.b", "bad").ApplicationproblemJSONDefault, http.StatusUnprocessableEntity, api.ErrorCodeInvalidSetting)
	wantProblem(t, set("no.such", "x").ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
}

// A setting does not wait for a change in flight, which a clone can make last
// minutes: if it did, this would never return.
func TestSettingDoesNotWaitForChanges(t *testing.T) {
	t.Parallel()
	td := startOn(t, shortDir(t), telemetry.Options{}, func(o *Options) { o.SettingStore = &fakeSettings{} })
	td.daemon.changes.Lock()
	defer td.daemon.changes.Unlock()
	r, err := td.client.UpdateSettingWithResponse(context.Background(), "a.b", api.UpdateSetting{Value: "x"})
	if err != nil || r.JSON200 == nil {
		t.Fatalf("%v %s", err, r.Body)
	}
}

func TestSettingsWithoutAStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := start(t)
	list, err := td.client.ListSettingsWithResponse(ctx)
	if err != nil || list.JSON200 == nil || len(list.JSON200.Settings) != 0 {
		t.Fatalf("list: %v %s", err, list.Body)
	}
	r, err := td.client.UpdateSettingWithResponse(ctx, "a.b", api.UpdateSetting{Value: "x"})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, r.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
}
