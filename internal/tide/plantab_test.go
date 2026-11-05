package tide

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// ssoRun is the run of the plan sso-login drawn in docs/PLANS.md, Step 8, 23
// minutes in: ui-button failed its check, callback runs, e2e waits.
func ssoRun() (*planView, time.Time) {
	start := time.Unix(1_800_000_000, 0).UTC()
	at := func(m int) *time.Time { t := start.Add(time.Duration(m) * time.Minute); return &t }
	agent := func(name string) *api.PlanTaskAgent { return &api.PlanTaskAgent{Id: "agent-" + name, Name: name} }
	check := api.PlanTaskFailureCheck
	terminal := "t-91"
	summary := func(s string) *string { return &s }
	tasks := []api.PlanTask{
		{Id: "schema", After: []string{}, Prompt: "Add the sessions table.", State: api.PlanTaskStateDone, Summary: summary("Added sessions, with an index on its user."), Agent: agent("claude-a1"), StartedAt: at(0), FinishedAt: at(4)},
		{Id: "oidc-client", After: []string{"schema"}, Prompt: "Add an OIDC client.", State: api.PlanTaskStateDone, Summary: summary("Added it."), Agent: agent("claude-b2"), StartedAt: at(4), FinishedAt: at(13)},
		{Id: "docs", After: []string{"schema"}, Prompt: "Document the sessions.", State: api.PlanTaskStateDone, Summary: summary("Documented them."), Agent: agent("claude-e5"), StartedAt: at(4), FinishedAt: at(6)},
		{Id: "callback", After: []string{"oidc-client"}, Prompt: "Handle the IdP's callback.", State: api.PlanTaskStateRunning, Agent: agent("claude-c3"), StartedAt: at(17)},
		{Id: "ui-button", After: []string{"oidc-client"}, Prompt: "Add a \"Sign in with SSO\" button to the login page, next to the password form, styled as its other buttons are, and shown only when SSO is set up.", State: api.PlanTaskStateFailed, Failure: &check, CheckTerminal: &terminal, Agent: agent("codex-d4"), StartedAt: at(13), FinishedAt: at(16)},
		{Id: "e2e", After: []string{"callback", "ui-button"}, Prompt: "Test signing in end to end.", State: api.PlanTaskStatePending, WaitingFor: []string{"callback", "ui-button"}},
	}
	p := api.Plan{Id: "doc-sso", Name: "sso-login", State: api.PlanStateRunning, Revision: 3}
	c := &api.PlanContent{Plan: p, Revision: 3, Run: &api.PlanRun{State: api.PlanStateRunning, Branch: "plan/sso-login", StartedAt: start},
		Definition: api.PlanDefinition{Goal: "Let users sign in with the company's SSO, keeping password login.",
			Constraints: []string{"No new API.", "Keep password login.", "One migration."}, Tasks: &tasks}}
	return &planView{content: c, cursor: "ui-button", reveal: true}, *at(23)
}

// authRun is the plan of plans auth, 23 minutes in: sso-login done, mfa
// waiting for the person as sso-login is not in the default branch, audit
// running, cleanup waiting for both; mfa's goal read.
func authRun() (*planView, time.Time) {
	start := time.Unix(1_800_000_000, 0).UTC()
	n := func(i int64) *int64 { return &i }
	plans := []api.PlanChild{
		{Id: "sso-login", After: []string{}, State: api.PlanStateDone, Tasks: 6, TasksDone: n(6)},
		{Id: "mfa", After: []string{"sso-login"}, State: api.PlanStateDraft, Tasks: 3, Waiting: true},
		{Id: "audit", After: []string{}, State: api.PlanStateRunning, Tasks: 5, TasksDone: n(2)},
		{Id: "cleanup", After: []string{"mfa", "audit"}, State: api.PlanStateDraft, Tasks: 2},
	}
	p := api.Plan{Id: "doc-auth", Name: "auth", State: api.PlanStateRunning, Revision: 2}
	c := &api.PlanContent{Plan: p, Revision: 2, Run: &api.PlanRun{State: api.PlanStateRunning, StartedAt: start},
		Definition: api.PlanDefinition{Goal: "Rework sign in.", Constraints: []string{}, Plans: &plans}}
	goals := map[string]*childGoal{"mfa": {revision: 1, goal: "Ask for a second factor when a user signs in from a new device."}}
	return &planView{content: c, cursor: "mfa", reveal: true, goals: goals}, start.Add(23 * time.Minute)
}

// planTabText is the tab of v drawn width × height, without color or trailing
// spaces.
func planTabText(v *planView, width, height int, now time.Time) string {
	lines := layoutPlanTab(v, width, height, now, style.New(false)).lines
	for i, l := range lines {
		lines[i] = strings.TrimRight(ansi.Strip(l), " ")
	}
	return strings.Join(lines, "\n")
}

// ssoDraft is sso-login before it runs.
func ssoDraft() *planView {
	v, _ := ssoRun()
	c := *v.content
	tasks := slices.Clone(*c.Definition.Tasks)
	for i, t := range tasks {
		tasks[i] = api.PlanTask{Id: t.Id, After: t.After, Prompt: t.Prompt, State: api.PlanTaskStatePending, WaitingFor: t.After}
	}
	c.Definition.Tasks, c.Run = &tasks, nil
	c.Plan.State = api.PlanStateDraft
	return &planView{content: &c, reveal: true}
}

// TestPlanTabDrawn: the tab of a run with a failed task and of a draft, wide with
// the panel's room at the right and narrow with it under the list, character
// for character.
func TestPlanTabDrawn(t *testing.T) {
	t.Parallel()
	const authWide = ` auth                                                                             ● running   rev 2
 ━━━━━━━━━━──────────────────────────────  1/4 plans · 23m
 ▸ goal
┌─ Plans ──────────────────────────────────────────────────────┬─ mfa ─────────────────────────────┐
│ ✓     sso-login done            6/6                  ▲ needs │ ○ waiting for you · 3 tasks       │
│▌○     mfa       waiting for you 3 tasks            needs you │                                   │
│ │ ●   audit     running         2/5                          │ Needs                             │
│ ├─╯                                                          │   ✓ sso-login                     │
│ ○     cleanup   waiting         2 tasks           ▼ unblocks │ Unblocks                          │
│                                                              │   ○ cleanup, also after audit     │
│                                                              │                                   │
│                                                              │ Goal                              │
│                                                              │   Ask for a second factor …+2     │
│                                                              │                                   │
│                                                              │ r starts it, once sso-login is in │
│                                                              │ the default branch                │
│                                                              │ Enter open it                     │
└──────────────────────────────────────────────────────────────┴───────────────────────────────────┘
 p pause  c cancel  y copy  ↑↓ select  Enter open it  → goal`
	const authNarrow = ` auth                                                 ● running   rev 2
 ━━━━━━━━━━──────────────────────────────  1/4 plans · 23m
 ▸ goal
┌─ Plans ──────────────────────────────────────────────────────────────┐
│ ✓     sso-login done            6/6                          ▲ needs │
│▌○     mfa       waiting for you 3 tasks                    needs you │
│ │ ●   audit     running         2/5                                  │
│ ├─╯                                                                  │
│ ○     cleanup   waiting         2 tasks                   ▼ unblocks │
├─ mfa ────────────────────────────────────────────────────────────────┤
│ ○ waiting for you · 3 tasks                                          │
│ Needs ✓ sso-login · Unblocks ○ cleanup (also after audit)            │
│ r starts it, once sso-login is in the default branch                 │
│ Enter open it                                                        │
└──────────────────────────────────────────────────────────────────────┘
 p pause  c cancel  y copy  ↑↓ select  Enter open it  → goal`
	const ssoRunWide = ` sso-login                                                                         ● running   rev 3
 ━━━━━━━━━━━━━━━━━━━━▬▬▬▬▬▬──────────────  3/6 tasks · 1 failed · on plan/sso-login · 23m
 ▸ goal and 3 constraints
┌─ Tasks ───────────────────────────────────────────────────────┬─ ui-button ───────────────────────┐
│ ✓       schema      done          claude-a1 4m                │ ✗ failed: check · 3m              │
│ ├─╮                                                           │ codex-d4 · terminal t-91          │
│ ✓ │     oidc-client done          claude-b2 9m        ▲ needs │                                   │
│ ├─┼─╮                                                         │ Needs                             │
│ │ ✓ │   docs        done          claude-e5 2m                │   ✓ oidc-client                   │
│ ●   │   callback    running       claude-c3 6m                │ Unblocks                          │
│▌│   ✗   ui-button   failed: check codex-d4  3m      needs you │   ○ e2e, also after callback      │
│ ├───╯                                                         │                                   │
│ ○       e2e         waiting       —         —      ▼ unblocks │ Prompt                            │
│                                                               │   Add a "Sign in with SSO" button │
│                                                               │   to the login page, next to …+3  │
│                                                               │                                   │
│                                                               │ Enter answer it                   │
└───────────────────────────────────────────────────────────────┴───────────────────────────────────┘
 p pause  c cancel  y copy  ↑↓ select  Enter answer it  → constraints`
	const ssoRunNarrow = ` sso-login                                            ● running   rev 3
 ━━━━━━━━━━━▬▬▬────────  3/6 tasks · 1 failed · on plan/sso-login · 23m
 ▸ goal and 3 constraints
┌─ Tasks ──────────────────────────────────────────────────────────────┐
│ ✓       schema      done          claude-a1 4m                       │
│ ├─╮                                                                  │
│ ✓ │     oidc-client done          claude-b2 9m               ▲ needs │
│ ├─┼─╮                                                                │
│ │ ✓ │   docs        done          claude-e5 2m                       │
│ ●   │   callback    running       claude-c3 6m                       │
│▌│   ✗   ui-button   failed: check codex-d4  3m             needs you │
│ ▾ 1 more                                                             │
├─ ui-button ──────────────────────────────────────────────────────────┤
│ ✗ failed: check · 3m · codex-d4 · terminal t-91                      │
│ Needs ✓ oidc-client · Unblocks ○ e2e (also after callback)           │
│ Add a "Sign in with SSO" button to the login page, next to the …+2   │
│ Enter answer it                                                      │
└──────────────────────────────────────────────────────────────────────┘
 p pause  c cancel  y copy  ↑↓ select  Enter answer it  → constraints`
	const ssoDraftWide = ` sso-login                                                                          ○ draft   rev 3
 6 tasks, not run yet
 ▸ goal and 3 constraints
┌─ Tasks ──────────────────────────────────────────────────────┬─ schema ──────────────────────────┐
│▌○       schema      ready                                    │ ○ ready                           │
│ ├─╮                                                          │                                   │
│ ○ │     oidc-client waiting                       ▼ unblocks │ Unblocks                          │
│ ├─┼─╮                                                        │   ○ oidc-client                   │
│ │ ○ │   docs        waiting                       ▼ unblocks │   ○ docs                          │
│ ○   │   callback    waiting                                  │                                   │
│ │   ○   ui-button   waiting                                  │ Enter read it                     │
│ ▾ 1 more                                                     │                                   │
└──────────────────────────────────────────────────────────────┴───────────────────────────────────┘
 r run  y copy  ↑↓ select  Enter read it  → constraints`
	const ssoDraftNarrow = ` sso-login                                  ○ draft   rev 3
 6 tasks, not run yet
 ▸ goal and 3 constraints
┌─ Tasks ──────────────────────────────────────────────────┐
│▌○       schema      ready                                │
│ ├─╮                                                      │
│ ○ │     oidc-client waiting                   ▼ unblocks │
│ ├─┼─╮                                                    │
│ ▾ 4 more                                                 │
├─ schema ─────────────────────────────────────────────────┤
│ ○ ready                                                  │
│ Unblocks ○ oidc-client, ○ docs                           │
│ Add the sessions table.                                  │
│ Enter read it                                            │
└──────────────────────────────────────────────────────────┘
 r run  y copy  ↑↓ select  Enter read it  → constraints`
	run := func() *planView { v, _ := ssoRun(); return v }
	parent := func() *planView { v, _ := authRun(); return v }
	_, now := ssoRun()
	for _, c := range []struct {
		name          string
		v             *planView
		width, height int
		want          string
	}{
		{"a run, wide", run(), 101, 19, ssoRunWide},
		{"a run, narrow", run(), 72, 19, ssoRunNarrow},
		{"a draft, wide", ssoDraft(), 100, 14, ssoDraftWide},
		{"a draft, narrow", ssoDraft(), 60, 16, ssoDraftNarrow},
		{"a plan of plans, wide", parent(), 100, 19, authWide},
		{"a plan of plans, narrow", parent(), 72, 16, authNarrow},
	} {
		if got := planTabText(c.v, c.width, c.height, now); got != c.want {
			t.Errorf("%s: got\n%s\nwant\n%s", c.name, got, c.want)
		}
	}
}

// cursorLine is the task on the line drawn with the cursor's edge.
func cursorLine(t *testing.T, v *planView, now time.Time) string {
	t.Helper()
	d := layoutPlanTab(v, 101, 22, now, style.New(false))
	for i, id := range d.items {
		if strings.HasPrefix(ansi.Strip(d.lines[i]), "│▌") {
			return id
		}
	}
	t.Fatalf("no line with the cursor:\n%s", planTabText(v, 101, 22, now))
	return ""
}

// TestPlanTabCursor: ↑ ↓ go from task to task over the rail's connector rows,
// stopping at the ends, and a click on a task's row moves the cursor to it, on a
// connector's to none.
func TestPlanTabCursor(t *testing.T) {
	t.Parallel()
	v, now := ssoRun()
	v.cursor = ""
	for _, c := range []struct {
		by   int
		want string
	}{{0, "schema"}, {-1, "schema"}, {1, "oidc-client"}, {1, "docs"}, {1, "callback"}, {1, "ui-button"}, {1, "e2e"}, {1, "e2e"}, {-1, "ui-button"}} {
		if c.by != 0 {
			v.step(c.by)
		}
		if got := cursorLine(t, v, now); got != c.want || v.cursor != c.want {
			t.Fatalf("after %+d: the cursor is on %q (%q), want %q", c.by, got, v.cursor, c.want)
		}
	}

	m := plansModel(t)
	m.now = func() time.Time { return now }
	area := uv.Rect(0, 0, 101, 22)
	lines := layoutPlanTab(v, 101, 22, now, style.New(false)).lines
	line := func(prefix string) int {
		return slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(ansi.Strip(l), prefix) })
	}
	m.planTabClick(v, line("│ ✓ │     oidc-client"), area)
	if v.cursor != "oidc-client" {
		t.Errorf("a click on oidc-client's row: the cursor is on %q", v.cursor)
	}
	m.planTabClick(v, line("│ ├─┼─╮"), area)
	if v.cursor != "oidc-client" {
		t.Errorf("a click on a connector moved the cursor to %q", v.cursor)
	}
}

// TestPlanTabMarks: the task under the cursor is in reverse video, its edges in
// the accent style and the others muted, the tasks it needs marked ▲ and those
// waiting for it ▼; a task that needs the person says so while no mark is on it.
func TestPlanTabMarks(t *testing.T) {
	t.Parallel()
	v, now := ssoRun()
	marks := func() map[string]string {
		got := map[string]string{}
		d := layoutPlanTab(v, 101, 22, now, style.New(false))
		for i, id := range d.items {
			for _, mark := range []string{planMarkNeeds, planMarkUnblocks, planMarkNeedsYou} {
				if strings.Contains(d.lines[i], mark) {
					got[id] = mark
				}
			}
		}
		return got
	}
	for _, c := range []struct {
		cursor string
		want   map[string]string
	}{
		{"oidc-client", map[string]string{"schema": planMarkNeeds, "callback": planMarkUnblocks, "ui-button": planMarkUnblocks}},
		{"e2e", map[string]string{"callback": planMarkNeeds, "ui-button": planMarkNeeds}},
		{"docs", map[string]string{"schema": planMarkNeeds, "ui-button": planMarkNeedsYou}},
	} {
		v.cursor = c.cursor
		if got := marks(); !maps.Equal(got, c.want) {
			t.Errorf("on %s: marks %v, want %v", c.cursor, got, c.want)
		}
	}

	on := style.New(true)
	v.cursor = "e2e"
	d := layoutPlanTab(v, 101, 22, now, on)
	row := func(id string) string {
		for i, task := range d.items {
			if task == id {
				return d.lines[i]
			}
		}
		t.Fatalf("no row of %s", id)
		return ""
	}
	if !strings.Contains(row("e2e"), "\x1b[7m") || strings.Contains(row("callback"), "\x1b[7m") {
		t.Errorf("reverse video: e2e %q, callback %q", row("e2e"), row("callback"))
	}
	join := slices.IndexFunc(d.lines, func(l string) bool { return strings.Contains(ansi.Strip(l), "├───╯") })
	split := slices.IndexFunc(d.lines, func(l string) bool { return strings.Contains(ansi.Strip(l), "├─╮") })
	if l := d.lines[join]; !strings.Contains(l, on.Focused("╯")) || !strings.Contains(l, on.Focused("├")) {
		t.Errorf("the edges into e2e are not in the accent style: %q", l)
	}
	if l := d.lines[split]; !strings.Contains(l, on.Muted("╮")) || strings.Contains(l, on.Focused("╮")) {
		t.Errorf("an edge of others is not muted: %q", l)
	}
}

// TestPlanTabKeys: the keys line says only what the plan's state takes, then the
// list's keys and what Enter does on the row under the cursor: a task failed in
// a plan over is read, not answered; a plan of plans' plan is opened.
func TestPlanTabKeys(t *testing.T) {
	t.Parallel()
	v, _ := ssoRun()
	items := planItems(v.content)
	failed, done, running := items[4], items[0], items[3]
	parent, closed := "auth", api.PlanRunPausedReasonProjectClosed
	archivedAt := time.Unix(1, 0)
	for _, c := range []struct {
		name   string
		plan   func(p *api.Plan, run *api.PlanRun)
		cursor planItem
		want   string
	}{
		{"a draft", func(p *api.Plan, _ *api.PlanRun) { p.State = api.PlanStateDraft }, done, "r run  y copy  ↑↓ select  Enter read it"},
		{"a child's draft", func(p *api.Plan, _ *api.PlanRun) { p.State, p.Parent = api.PlanStateDraft, &parent }, done, "runs with auth  y copy  ↑↓ select  Enter read it"},
		{"running", func(*api.Plan, *api.PlanRun) {}, failed, "p pause  c cancel  y copy  ↑↓ select  Enter answer it"},
		{"running, on a running task", func(*api.Plan, *api.PlanRun) {}, running, "p pause  c cancel  y copy  ↑↓ select  Enter go to its agent"},
		{"paused", func(p *api.Plan, _ *api.PlanRun) { p.State = api.PlanStatePaused }, done, "p resume  c cancel  ↑↓ select  Enter read it"},
		{"paused, its project closed", func(p *api.Plan, r *api.PlanRun) { p.State, r.PausedReason = api.PlanStatePaused, &closed }, failed, "c cancel  ↑↓ select  Enter answer it"},
		{"done", func(p *api.Plan, _ *api.PlanRun) { p.State = api.PlanStateDone }, failed, "a archive  y copy  ↑↓ select  Enter read it"},
		{"cancelled", func(p *api.Plan, _ *api.PlanRun) { p.State = api.PlanStateCancelled }, done, "a archive  y copy  ↑↓ select  Enter read it"},
		{"archived", func(p *api.Plan, _ *api.PlanRun) { p.State, p.ArchivedAt = api.PlanStateDone, &archivedAt }, done, "a unarchive  y copy  ↑↓ select  Enter read it"},
	} {
		content := *v.content
		run := *content.Run
		content.Run = &run
		c.plan(&content.Plan, content.Run)
		if got := planKeys(&content, c.cursor, "", false, style.New(false)); got != " "+c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	parentView, _ := authRun()
	if got := planKeys(parentView.content, planItems(parentView.content)[1], "", false, style.New(false)); got != " p pause  c cancel  y copy  ↑↓ select  Enter open it" {
		t.Errorf("a plan of plans: %q", got)
	}
	content := *v.content
	if got := planKeys(&content, done, "constraints", false, style.New(false)); !strings.HasSuffix(got, "  → constraints") {
		t.Errorf("folded: %q", got)
	}
	if got := planKeys(&content, done, "constraints", true, style.New(false)); !strings.HasSuffix(got, "  ← constraints") {
		t.Errorf("unfolded: %q", got)
	}
}

// TestPlanTabColumnsDropped: as the pane narrows the list drops the agent, then
// the time, then the failure's reason; the rail, the ids, the states and the
// marks stay.
func TestPlanTabColumnsDropped(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		width               int
		agent, time, reason bool
	}{
		{61, true, true, true},
		{60, false, true, true},
		{50, false, false, true},
		{46, false, false, false},
	} {
		v, now := ssoRun()
		got := planTabText(v, c.width, 19, now)
		row := got[strings.Index(got, "│▌"):]
		row = row[:strings.Index(row, "\n")]
		for what, want := range map[string]bool{"codex-d4": c.agent, " 3m": c.time, "failed: check": c.reason, "│▌│   ✗   ui-button   failed": true, "needs you": true} {
			if strings.Contains(row, what) != want {
				t.Errorf("%d wide: %q shown %v, want %v: %q", c.width, what, !want, want, row)
			}
		}
	}
}

// TestPlanTabScrolls: the list scrolls under the header to keep the cursor in
// view, saying how many tasks are out of sight.
func TestPlanTabScrolls(t *testing.T) {
	t.Parallel()
	v, now := ssoRun()
	v.cursor = "schema"
	got := planTabText(v, 72, 17, now)
	if !strings.Contains(got, "▾ 3 more") || strings.Contains(got, "▴") {
		t.Errorf("at the top:\n%s", got)
	}
	for range 5 {
		v.step(1)
	}
	got = planTabText(v, 72, 17, now)
	if !strings.Contains(got, "▴ 2 more") || strings.Contains(got, "▾") || !strings.Contains(got, "│▌○       e2e") || !strings.HasPrefix(got, " sso-login") {
		t.Errorf("at the end:\n%s", got)
	}
}

// TestPlanTabFolds: → unfolds the constraints and ←  folds them again; where the
// pane is short the goal folds with them.
func TestPlanTabFolds(t *testing.T) {
	t.Parallel()
	m := plansModel(t)
	v, now := ssoRun()
	m.now = func() time.Time { return now }
	tall, short := planTabText(v, 101, 22, now), planTabText(v, 101, 16, now)
	if !strings.Contains(tall, "Goal  Let users") || !strings.Contains(tall, "▸ 3 constraints") || strings.Contains(tall, "No new API.") {
		t.Errorf("folded:\n%s", tall)
	}
	if strings.Contains(short, "Goal") || !strings.Contains(short, "▸ goal and 3 constraints") {
		t.Errorf("short:\n%s", short)
	}
	m.planTabKey(v, keyMsgs("right")[0])
	if got := planTabText(v, 101, 16, now); !strings.Contains(got, "▾ goal and 3 constraints") || !strings.Contains(got, "Goal  Let users") || !strings.Contains(got, "   - Keep password login.") {
		t.Errorf("unfolded:\n%s", got)
	}
	m.planTabKey(v, keyMsgs("left")[0])
	if v.unfolded {
		t.Error("← left them unfolded")
	}
}

// TestPlanTabPlainList: past the rail's lanes the tasks are a plain list, their
// glyphs in a column of their own.
func TestPlanTabPlainList(t *testing.T) {
	t.Parallel()
	v := ssoDraft()
	var tasks []api.PlanTask
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		tasks = append(tasks, api.PlanTask{Id: id, After: []string{}, State: api.PlanTaskStatePending, WaitingFor: []string{}})
	}
	tasks = append(tasks, api.PlanTask{Id: "h", After: []string{"a", "b", "c", "d", "e", "f", "g"}, State: api.PlanTaskStatePending, WaitingFor: []string{"a"}})
	v.content.Definition.Tasks = &tasks
	got := planTabText(v, 101, 22, now0())
	if strings.ContainsAny(got[strings.Index(got, "┌"):], "├╯╮") || !strings.Contains(got, "│▌○   a ready") || !strings.Contains(got, "│ ○   h waiting") {
		t.Errorf("a plain list:\n%s", got)
	}
}

func now0() time.Time { return time.Unix(0, 0) }

// TestPlanTabMinute: while a task runs the tab asks for a minute's tick, one at
// a time, and draws its time again when it comes.
func TestPlanTabMinute(t *testing.T) {
	t.Parallel()
	m := plansModel(t)
	var asked []time.Duration
	var next func(time.Time) tea.Msg
	m.tick = func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
		asked, next = append(asked, d), fn
		return nil
	}
	v, now := ssoRun()
	m.now = func() time.Time { return now }
	m.moveToPlan(3) // sso
	keys(m, "enter")
	loadPlan(m, "doc-sso", *v.content)
	wantText(t, m, "callback    running       6m")
	m.Update(nil)
	if len(asked) != 1 || asked[0] != time.Minute {
		t.Fatalf("ticks asked: %v", asked)
	}
	now = now.Add(time.Minute)
	m.Update(next(now))
	wantText(t, m, "callback    running       7m")
	if len(asked) != 2 {
		t.Errorf("the tick not asked again once it came: %v", asked)
	}

	done := *v.content
	tasks := slices.Clone(*done.Definition.Tasks)
	tasks[3].State, tasks[3].FinishedAt = api.PlanTaskStateDone, &now
	done.Definition.Tasks = &tasks
	loadPlan(m, "doc-sso", done)
	m.Update(next(now))
	if len(asked) != 2 {
		t.Errorf("a tick asked with no task running: %v", asked)
	}
}
