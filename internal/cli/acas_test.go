package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/aca/acatest"
	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/clikit"
)

func TestAgentAcas(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	v, exit := wdxJSON(t, home, "agent", "acas")
	if exit != clikit.ExitOK || len(v.Acas) != 1 || v.Acas[0].Id != "claude_code" || !v.Acas[0].Default {
		t.Fatalf("exit %d, %+v", exit, v.Acas)
	}
	out := wdx(t, home, "agent", "acas")
	if out.exit != clikit.ExitOK || out.stdout != "AI coding agents\nID\nclaude_code (default)\n" {
		t.Fatalf("unexpected text: %+v", out)
	}
	if got := complete(t, home, "agent", "create", "--aca", ""); !slices.Equal(got, []string{"claude_code"}) {
		t.Errorf("--aca completes %q", got)
	}
}

func TestAgentCreateWithAnACA(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	v, exit := wdxJSONWith(t, home, in(app), "agent", "create", "--aca", "claude_code")
	if exit != clikit.ExitOK || v.Agent == nil || v.Agent.Aca != "claude_code" {
		t.Fatalf("exit %d, %+v", exit, v)
	}
	m, exit := wdxJSON(t, home, "agent", "models", "--aca", "claude_code")
	if exit != clikit.ExitOK || m.Aca != "claude_code" {
		t.Fatalf("models: exit %d, %+v", exit, m)
	}
}

func TestAgentCreateWithAnUnknownACA(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	v, exit := wdxJSONWith(t, home, in(app), "agent", "create", "--aca", "codex")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	wantHint(t, v, "agent acas")
	v, exit = wdxJSON(t, home, "agent", "models", "--aca", "codex")
	wantError(t, v, exit, api.ErrorCodeNotFound, clikit.ExitRejected)
	v, exit = wdxJSON(t, home, "agent", "create", "--aca", "")
	wantError(t, v, exit, api.ErrorCodeUsage, clikit.ExitUsage)
}

// With no ACA installed, agents cannot be created, nor models listed; the rest works.
func TestNoACAInstalled(t *testing.T) {
	t.Parallel()
	home := newHomeWithACAs(t, &acatest.ACA{ProgramName: acatest.NotInstalled})
	app := newProjectDir(t, home, "app")
	v, exit := wdxJSONWith(t, home, in(app), "agent", "create")
	wantError(t, v, exit, api.ErrorCodeAcaUnavailable, clikit.ExitRejected)
	wantHint(t, v, "deps list")
	v, exit = wdxJSONWith(t, home, in(app), "agent", "create", "--aca", "claude_code")
	wantError(t, v, exit, api.ErrorCodeAcaUnavailable, clikit.ExitRejected)
	wantHint(t, v, "install "+acatest.NotInstalled)
	v, exit = wdxJSON(t, home, "agent", "models")
	wantError(t, v, exit, api.ErrorCodeAcaUnavailable, clikit.ExitRejected)
	if a, exit := wdxJSON(t, home, "agent", "acas"); exit != clikit.ExitOK || a.Acas == nil || len(a.Acas) != 0 {
		t.Fatalf("acas: exit %d, %+v", exit, a.Acas)
	}
	if out := wdx(t, home, "agent", "acas"); !strings.HasPrefix(out.stdout, "no AI coding agent installed and enabled\n") {
		t.Errorf("text: %+v", out)
	}
	if _, exit := wdxJSONWith(t, home, in(app), "terminal", "create"); exit != clikit.ExitOK {
		t.Errorf("terminal create: exit %d", exit)
	}
}

// An ACA that has only stubs runs, and says what it cannot do yet.
func TestAnACAWithStubs(t *testing.T) {
	t.Parallel()
	home := newHomeWithACAs(t, &acatest.ACA{Launches: launchesPath(t.TempDir())}, &acatest.Bare{Name: "bare"})
	app := newProjectDir(t, home, "app")
	v, exit := wdxJSONWith(t, home, in(app), "agent", "create", "--aca", "bare", "--name", "helper")
	if exit != clikit.ExitOK || v.Agent == nil || v.Agent.Aca != "bare" {
		t.Fatalf("create: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(app), "agent", "transcript", "--name", "helper")
	wantError(t, v, exit, api.ErrorCodeAcaUnsupported, clikit.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "agent", "transcript", "--name", "helper", "--brief")
	wantError(t, v, exit, api.ErrorCodeAcaUnsupported, clikit.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "agent", "restart", "--name", "helper", "--force")
	wantError(t, v, exit, api.ErrorCodeAcaUnsupported, clikit.ExitRejected)
	if _, exit := wdxJSONWith(t, home, in(app), "agent", "remove", "--name", "helper", "--stop-running"); exit != clikit.ExitOK {
		t.Errorf("remove: exit %d", exit)
	}
}

func TestACAUnavailableHints(t *testing.T) {
	t.Parallel()
	problem := func(details map[string]any) error {
		p := api.NewProblem(api.ErrorCodeAcaUnavailable, "")
		p.Details = &details
		return &client.ProblemError{Problem: p}
	}
	for _, tc := range []struct {
		details map[string]any
		want    string
	}{
		{map[string]any{"aca": "codex", "reason": "disabled"}, "setting set --name aca.codex.enabled --value true"},
		{map[string]any{"aca": "codex", "reason": "not_installed", "program": "codex"}, "install codex, then check"},
		{map[string]any{"aca": nil, "reason": nil}, "deps list says which are installed"},
	} {
		if got := acaUnavailableHint(problem(tc.details)); !strings.Contains(got, tc.want) {
			t.Errorf("%v: hint %q", tc.details, got)
		}
	}
}
