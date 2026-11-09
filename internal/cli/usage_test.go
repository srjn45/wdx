package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/aca/acatest"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/identity"
)

// TestUsageNoneTold: before any ACA has told of its limits there are no rows.
func TestUsageNoneTold(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	v, exit := wdxJSON(t, home, "usage")
	if exit != clikit.ExitOK || v.Usage == nil || len(v.Usage) != 0 {
		t.Fatalf("exit %d, %+v", exit, v)
	}
	out := wdx(t, home, "usage")
	if out.Exit != clikit.ExitOK || out.Stdout != "no usage told yet: no AI coding agent has answered, or its plan has no limits\n" {
		t.Fatalf("unexpected text: %+v", out)
	}
}

// TestUsage: what an agent's ACA tells of its account's limits is listed by ACA,
// window and bucket, and the text says whether the ACA can be used.
func TestUsage(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	a := newAgent(t, home, app, "helper")
	resets := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	rows := []aca.Usage{
		{Window: "five_hour", UsedPercent: 54, ResetsAt: resets, ObservedAt: time.Now()},
		{Window: "seven_day", UsedPercent: 100, ResetsAt: resets.Add(48 * time.Hour), ObservedAt: time.Now()},
	}
	if err := acatest.WriteUsage(identity.AgentDir(home, a.Id), rows); err != nil {
		t.Fatal(err)
	}
	var v jsonOutput
	if !eventually(t, func() bool {
		var exit int
		v, exit = wdxJSON(t, home, "usage")
		return exit == clikit.ExitOK && len(v.Usage) == 2
	}) {
		t.Fatalf("usage not listed: %+v", v)
	}
	if u := v.Usage[0]; u.Aca != "claude_code" || u.Window != "five_hour" || u.Bucket != "" || u.UsedPercent != 54 || u.Limited || u.ResetsAt == nil || !u.ResetsAt.Equal(resets) {
		t.Errorf("five_hour = %+v", u)
	}
	if u := v.Usage[1]; u.Window != "seven_day" || u.UsedPercent != 100 || !u.Limited {
		t.Errorf("seven_day = %+v", u)
	}
	out := wdx(t, home, "usage")
	lines := strings.Split(strings.TrimSuffix(out.Stdout, "\n"), "\n")
	if out.Exit != clikit.ExitOK || len(lines) != 4 || !strings.HasPrefix(lines[0], "claude_code") || !strings.Contains(lines[0], "limited, resets ") || !strings.HasPrefix(lines[1], "  WINDOW") || !strings.HasPrefix(lines[2], "  Session (5 h)") || !strings.Contains(lines[2], "54%") || !strings.Contains(lines[3], "100% limited") {
		t.Fatalf("unexpected text: %+v", out)
	}
}

// TestAgentLimited: an agent whose account is limited for every model reads limited
// in agent list's ACTIVITY column, and agent get says until when.
func TestAgentLimited(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	a := newAgent(t, home, app, "helper")
	resets := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	rows := []aca.Usage{{Window: "seven_day", UsedPercent: 100, ResetsAt: resets, ObservedAt: time.Now()}}
	if err := acatest.WriteUsage(identity.AgentDir(home, a.Id), rows); err != nil {
		t.Fatal(err)
	}
	var v jsonOutput
	if !eventually(t, func() bool {
		var exit int
		v, exit = wdxJSON(t, home, "agent", "get", "--id", a.Id)
		return exit == clikit.ExitOK && v.Agent != nil && v.Agent.Limited != nil
	}) {
		t.Fatalf("agent not limited: %+v", v)
	}
	if l := v.Agent.Limited; l.Bucket != "" || l.ResetsAt == nil || !l.ResetsAt.Equal(resets) {
		t.Errorf("limited = %+v", l)
	}
	out := wdx(t, home, "agent", "list", "--project-name", "app")
	lines := strings.Split(strings.TrimSuffix(out.Stdout, "\n"), "\n")
	if out.Exit != clikit.ExitOK || len(lines) != 2 || !strings.Contains(lines[0], "ACTIVITY") || !strings.Contains(lines[1], " limited ") {
		t.Fatalf("unexpected list: %+v", out)
	}
	out = wdx(t, home, "agent", "get", "--id", a.Id)
	if out.Exit != clikit.ExitOK || !strings.Contains(out.Stdout, "\n  limited     resets in 1 h ") {
		t.Fatalf("unexpected get: %+v", out)
	}
}
