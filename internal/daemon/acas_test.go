package daemon

import (
	"context"
	"net/http"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/aca/acatest"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/telemetry"
)

func wantUnavailable(t *testing.T, p *api.Problem, reason, program string) {
	t.Helper()
	wantProblem(t, p, http.StatusConflict, api.ErrorCodeAcaUnavailable)
	if p.Details == nil {
		t.Fatal("no details")
	}
	d := *p.Details
	if d["aca"] != "claude_code" || d["reason"] != reason || (program == "" && d["program"] != nil) || (program != "" && d["program"] != program) {
		t.Errorf("details = %v", d)
	}
}

func TestAnACANotInstalledIsUnavailable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	td := startOn(t, shortDir(t), telemetry.Options{}, func(o *Options) {
		o.ACAs = []aca.AiCodingAgent{&acatest.ACA{ProgramName: acatest.NotInstalled}}
	})
	app := td.create(t, "app").Worktrees[0].Path
	c, err := td.client.CreateAgentWithResponse(ctx, api.CreateAgent{Dir: app, Aca: optional("claude_code")})
	if err != nil {
		t.Fatal(err)
	}
	wantUnavailable(t, c.ApplicationproblemJSONDefault, "not_installed", acatest.NotInstalled)
	m, err := td.client.ListAcaModelsWithResponse(ctx, "claude_code")
	if err != nil {
		t.Fatal(err)
	}
	wantUnavailable(t, m.ApplicationproblemJSONDefault, "not_installed", acatest.NotInstalled)
	// With none named and none usable, none is named in the answer either.
	c, err = td.client.CreateAgentWithResponse(ctx, api.CreateAgent{Dir: app})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, c.ApplicationproblemJSONDefault, http.StatusConflict, api.ErrorCodeAcaUnavailable)
	if d := *c.ApplicationproblemJSONDefault.Details; d["aca"] != nil || d["reason"] != nil {
		t.Errorf("details = %v", d)
	}
}

// Disabled by a setting, then enabled again by a reload of the settings.
func TestADisabledACAIsUnavailableUntilEnabled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	enabled := false
	td := startOn(t, shortDir(t), telemetry.Options{}, func(o *Options) {
		o.ACAEnabled = map[string]bool{"claude_code": false}
		o.Reload = func() (Settings, error) { return Settings{ACAEnabled: map[string]bool{"claude_code": enabled}}, nil }
	})
	app := td.create(t, "app").Worktrees[0].Path
	c, err := td.client.CreateAgentWithResponse(ctx, api.CreateAgent{Dir: app, Aca: optional("claude_code")})
	if err != nil {
		t.Fatal(err)
	}
	wantUnavailable(t, c.ApplicationproblemJSONDefault, "disabled", "")
	enabled = true
	reload(t, td)
	td.createAgent(t, "", app, "helper")
}

func TestAnUnknownACAIsNotFound(t *testing.T) {
	t.Parallel()
	td := start(t)
	app := td.create(t, "app").Worktrees[0].Path
	c, err := td.client.CreateAgentWithResponse(context.Background(), api.CreateAgent{Dir: app, Aca: optional("codex")})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, c.ApplicationproblemJSONDefault, http.StatusNotFound, api.ErrorCodeNotFound)
	if d := *c.ApplicationproblemJSONDefault.Details; d["argument"] != "aca" {
		t.Errorf("details = %v", d)
	}
}
